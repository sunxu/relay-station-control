package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	authn "github.com/sunxu/relay-station-control/internal/auth"
	jobstore "github.com/sunxu/relay-station-control/internal/store"
)

type fakeJobReader struct {
	first  jobstore.JobSummary
	second jobstore.JobSummary
	detail jobstore.JobDetail
	fail   atomic.Bool
}

func (reader *fakeJobReader) ListJobs(_ context.Context, _ jobstore.JobListFilters, cursor *jobstore.JobCursor, _ int) (jobstore.JobPage, error) {
	if reader.fail.Load() {
		return jobstore.JobPage{}, errors.New("SECRET postgres://job-reader:password@database")
	}
	if cursor == nil {
		return jobstore.JobPage{Items: []jobstore.JobSummary{reader.first}, HasMore: true}, nil
	}
	return jobstore.JobPage{Items: []jobstore.JobSummary{reader.second}}, nil
}

func (reader *fakeJobReader) Job(_ context.Context, jobID uuid.UUID) (jobstore.JobDetail, error) {
	if reader.fail.Load() {
		return jobstore.JobDetail{}, errors.New("SECRET lease-token-and-database-url")
	}
	if jobID != reader.detail.JobID {
		return jobstore.JobDetail{}, jobstore.ErrJobNotFound
	}
	return reader.detail, nil
}

func (reader *fakeJobReader) JobKinds(context.Context) ([]string, error) {
	return []string{"synthetic.noop"}, nil
}

func TestDurableJobHTTPReadOnlySecurityPaginationAndRecovery(t *testing.T) {
	ownerURL, runtimeURL := isolatedRuntimeDatabaseURLs(t)
	ctx := context.Background()
	owner, err := pgxpool.New(ctx, ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	runtime, err := pgxpool.New(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if _, err = owner.Exec(ctx, `INSERT INTO environments(environment_id,name,environment_type) VALUES('job-http-test','Job HTTP Test','dev')`); err != nil {
		t.Fatal(err)
	}

	config := testValidatedConfig(t, authn.EnvironmentDev, false)
	service, err := authn.NewService(runtime, config)
	if err != nil {
		t.Fatal(err)
	}
	adminID := uuid.New()
	token := "job-session-" + uuid.NewString()
	tokenDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainSessionDigest, token)
	if err != nil {
		t.Fatal(err)
	}
	csrfDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainCSRFDigest, "job-csrf-"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at)
		VALUES($1,$2,'Job Reader','enabled',CURRENT_TIMESTAMP)`, adminID, "job_reader_"+adminID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_sessions
		(session_id,admin_id,token_digest,csrf_digest,key_version,mfa_method,absolute_expires_at)
		VALUES($1,$2,$3,$4,$5,'none',CURRENT_TIMESTAMP+interval '12 hours')`,
		uuid.New(), adminID, tokenDigest.Sum[:], csrfDigest.Sum[:], int32(tokenDigest.KeyVersion)); err != nil {
		t.Fatal(err)
	}
	for state, rejectedToken := range map[string]string{"revoked": "revoked-job-session", "expired": "expired-job-session"} {
		digest, digestErr := authn.ComputeDigest(config.Keyring, authn.DomainSessionDigest, rejectedToken)
		if digestErr != nil {
			t.Fatal(digestErr)
		}
		csrf, csrfErr := authn.ComputeDigest(config.Keyring, authn.DomainCSRFDigest, "csrf-"+rejectedToken)
		if csrfErr != nil {
			t.Fatal(csrfErr)
		}
		createdAt, expiresAt := time.Now().UTC(), time.Now().UTC().Add(time.Hour)
		var revokedAt, revokeReason any
		if state == "revoked" {
			revokedAt, revokeReason = time.Now().UTC(), "operator_revoked"
		} else {
			createdAt, expiresAt = time.Now().UTC().Add(-2*time.Hour), time.Now().UTC().Add(-time.Hour)
		}
		if _, err = owner.Exec(ctx, `INSERT INTO control_admin_sessions
			(session_id,admin_id,token_digest,csrf_digest,key_version,mfa_method,created_at,last_activity_at,absolute_expires_at,revoked_at,revoke_reason)
			VALUES($1,$2,$3,$4,$5,'none',$6,$6,$7,$8,$9)`, uuid.New(), adminID, digest.Sum[:], csrf.Sum[:], int32(digest.KeyVersion), createdAt, expiresAt, revokedAt, revokeReason); err != nil {
			t.Fatal(err)
		}
	}

	created := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	first := jobstore.JobSummary{
		JobID: uuid.New(), OperationID: uuid.New(), JobKind: "synthetic.noop", Status: "running",
		AttemptCount: 1, MaxAttempts: 3, AvailableAt: created, StartedAt: timePointer(created.Add(time.Minute)),
		CancelRequested: false, OutboxStatus: "suppressed", CreatedAt: created, UpdatedAt: created.Add(time.Minute),
	}
	second := first
	second.JobID = uuid.New()
	second.CreatedAt = created.Add(-time.Minute)
	detail := jobstore.JobDetail{JobSummary: first, Events: []jobstore.JobEvent{{
		Sequence: 1, EventType: "enqueued", ToStatus: "pending", Attempt: 0, ActorType: "service",
		ReasonCode: stringPointer("job_enqueued"), OccurredAt: created,
	}}}
	reader := &fakeJobReader{first: first, second: second, detail: detail}
	surfaceCanary := "job-api-surface-canary-" + uuid.NewString()
	assets, err := jobstore.NewAssetRepository(runtime)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewAuthenticatedServerWithAssetsAndJobs("test", service, assets, NewAssetMetrics(), reader)
	if err != nil {
		t.Fatal(err)
	}
	handler := HandlerWithOptions(server, ChiServerOptions{ErrorHandlerFunc: server.PrepareGeneratedError})
	do := func(method, path, cookie string, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, nil)
		if cookie != "" {
			request.AddCookie(&http.Cookie{Name: authn.DevSessionCookieName, Value: cookie})
		}
		for name, value := range headers {
			request.Header.Set(name, value)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	for _, path := range []string{"/api/jobs", "/api/jobs/" + first.JobID.String()} {
		for state, rejectedToken := range map[string]string{"forged": "forged-session", "revoked": "revoked-job-session", "expired": "expired-job-session"} {
			rejected := do(http.MethodGet, path, rejectedToken, nil)
			if rejected.Code != http.StatusUnauthorized || rejected.Header().Get("Cache-Control") != "no-store" || strings.Contains(rejected.Body.String(), first.JobID.String()) {
				t.Fatalf("%s %s status=%d headers=%v body=%s", state, path, rejected.Code, rejected.Header(), rejected.Body.String())
			}
		}
	}

	list := do(http.MethodGet, "/api/jobs?job_kind=synthetic.noop&status=running&created_from=2026-08-25T00:00:00Z&created_to=2026-08-26T00:00:00Z&limit=1", token, map[string]string{
		"Origin": "https://forged.invalid", "X-CSRF-Token": "forged-proof", "X-Job-Canary": surfaceCanary,
	})
	if list.Code != http.StatusOK || list.Header().Get("Cache-Control") != "no-store" || list.Header().Get("X-Request-ID") == "" {
		t.Fatalf("list status=%d headers=%v body=%s", list.Code, list.Header(), list.Body.String())
	}
	listHeaders, err := json.Marshal(list.Header())
	if err != nil || strings.Contains(string(listHeaders), surfaceCanary) {
		t.Fatalf("request header canary reached response headers: err=%v headers=%s", err, listHeaders)
	}
	var page JobListResponse
	if err = json.Unmarshal(list.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.NextCursor == nil || page.Items[0].JobId != first.JobID {
		t.Fatalf("list page=%+v err=%v body=%s", page, err, list.Body.String())
	}
	secondPage := do(http.MethodGet, "/api/jobs?job_kind=synthetic.noop&status=running&created_from=2026-08-25T00:00:00Z&created_to=2026-08-26T00:00:00Z&limit=1&cursor="+*page.NextCursor, token, nil)
	page = JobListResponse{}
	if err = json.Unmarshal(secondPage.Body.Bytes(), &page); err != nil || secondPage.Code != http.StatusOK || len(page.Items) != 1 || page.NextCursor != nil || page.Items[0].JobId != second.JobID {
		t.Fatalf("second page=%+v err=%v status=%d body=%s", page, err, secondPage.Code, secondPage.Body.String())
	}

	detailResponse := do(http.MethodGet, "/api/jobs/"+first.JobID.String(), token, nil)
	if detailResponse.Code != http.StatusOK || detailResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("detail status=%d headers=%v body=%s", detailResponse.Code, detailResponse.Header(), detailResponse.Body.String())
	}
	for _, forbidden := range []string{"payload", "payload_hash", "idempotency", "lease_owner", "lease_fencing", "error_summary", "outbox_envelope"} {
		if strings.Contains(detailResponse.Body.String(), forbidden) {
			t.Fatalf("detail leaked forbidden field %q: %s", forbidden, detailResponse.Body.String())
		}
	}

	for _, path := range []string{
		"/api/jobs?cursor=forged", "/api/jobs?job_kind=A!", "/api/jobs?status=unknown", "/api/jobs?limit=201",
		"/api/jobs?created_from=2026-08-26T00:00:00Z&created_to=2026-08-25T00:00:00Z", "/api/jobs/not-a-uuid",
	} {
		response := do(http.MethodGet, path, token, nil)
		if response.Code != http.StatusBadRequest || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("invalid %s status=%d headers=%v body=%s", path, response.Code, response.Header(), response.Body.String())
		}
	}
	missing := do(http.MethodGet, "/api/jobs/"+uuid.NewString(), token, nil)
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), `"code":"not_found"`) {
		t.Fatalf("missing status=%d body=%s", missing.Code, missing.Body.String())
	}

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		for _, path := range []string{"/api/jobs", "/api/jobs/" + first.JobID.String()} {
			response := do(method, path, token, nil)
			if response.Code != http.StatusMethodNotAllowed {
				t.Fatalf("write %s %s status=%d body=%s", method, path, response.Code, response.Body.String())
			}
		}
	}

	var logBuffer bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuffer, nil)))
	reader.fail.Store(true)
	unavailable := do(http.MethodGet, "/api/jobs", token, nil)
	slog.SetDefault(previousLogger)
	if unavailable.Code != http.StatusServiceUnavailable || strings.Contains(unavailable.Body.String(), "SECRET") || strings.Contains(unavailable.Body.String(), "postgres://") {
		t.Fatalf("unavailable status=%d body=%s", unavailable.Code, unavailable.Body.String())
	}
	if logs := logBuffer.String(); !strings.Contains(logs, "result=database_unavailable") || strings.Contains(logs, "SECRET") || strings.Contains(logs, "postgres://") {
		t.Fatalf("unavailable log not sanitized: %s", logs)
	}
	unavailableHeaders, err := json.Marshal(unavailable.Header())
	if err != nil {
		t.Fatal(err)
	}
	var auditJSON string
	if err = owner.QueryRow(ctx, `SELECT COALESCE(string_agg(row_to_json(audit)::text,''),'') FROM audit_logs AS audit`).Scan(&auditJSON); err != nil {
		t.Fatal(err)
	}
	for surface, value := range map[string]string{
		"list_body": list.Body.String(), "list_headers": string(listHeaders), "unavailable_body": unavailable.Body.String(),
		"unavailable_headers": string(unavailableHeaders),
		"logs":                logBuffer.String(), "audit": auditJSON,
	} {
		for _, canary := range []string{surfaceCanary, "job-reader:password", "lease-token-and-database-url"} {
			if strings.Contains(value, canary) {
				t.Fatalf("job read canary %q leaked through %s", canary, surface)
			}
		}
	}
	reader.fail.Store(false)
	recovered := do(http.MethodGet, "/api/jobs", token, nil)
	if recovered.Code != http.StatusOK || !strings.Contains(recovered.Body.String(), first.JobID.String()) {
		t.Fatalf("recovered status=%d body=%s", recovered.Code, recovered.Body.String())
	}
}

func timePointer(value time.Time) *time.Time { return &value }
func stringPointer(value string) *string     { return &value }

var _ jobstore.JobReader = (*fakeJobReader)(nil)
