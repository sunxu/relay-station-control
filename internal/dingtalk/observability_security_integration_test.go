package dingtalk_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sunxu/relay-station-control/internal/api"
	authn "github.com/sunxu/relay-station-control/internal/auth"
	"github.com/sunxu/relay-station-control/internal/dingtalk"
	"github.com/sunxu/relay-station-control/internal/jobs"
	"github.com/sunxu/relay-station-control/internal/store"
)

type deliveryLogCapture struct {
	mu      sync.Mutex
	records []jobs.LogRecord
}

func (l *deliveryLogCapture) Log(_ context.Context, record jobs.LogRecord) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = append(l.records, record)
}
func (l *deliveryLogCapture) snapshot() []jobs.LogRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]jobs.LogRecord(nil), l.records...)
}

func assertNoDeliverySecrets(t *testing.T, surface string, content []byte, secrets []string) {
	t.Helper()
	for _, secret := range secrets {
		if bytes.Contains(content, []byte(secret)) {
			t.Fatalf("secret-negative failed on %s (canary value withheld)", surface)
		}
	}
}

// A committed ACTIVE occurrence is a fixture, not a second confirmation
// algorithm. Its notification is delivered by the real Executor/Worker/store;
// this test checks that delivery cannot mutate that independent domain truth.
func TestDingTalkObservabilitySecurityPostgres(t *testing.T) {
	for _, tc := range []struct {
		name             string
		status, attempts int
		code             string
	}{
		{"retry_budget", http.StatusServiceUnavailable, 5, "max_attempts_exhausted"},
		{"raw_business_rejection", http.StatusOK, 1, "dingtalk_business_rejected"},
		{"oversized_response", http.StatusOK, 1, "dingtalk_invalid_response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newRuntimeJobDatabase(t)
			ctx := context.Background()
			token, signing, credential, raw := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
			var hits atomic.Int32
			endpoint := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				if r.URL.Query().Get("access_token") != token || r.URL.Query().Get("sign") == "" || r.URL.Query().Get("timestamp") == "" {
					t.Error("runtime signing material missing at controlled endpoint")
				}
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(tc.status)
				if tc.name == "oversized_response" {
					_, _ = io.WriteString(w, strings.Repeat(raw, 512))
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 310000, "errmsg": raw + credential})
			}))
			defer endpoint.Close()
			secrets := []string{endpoint.URL, token, signing, credential, raw, "?access_token=", "timestamp=", "sign="}
			executor := dingtalk.RuntimeSecretExecutorForTest(t, endpoint, token, signing)
			registry, err := jobs.NewRegistry(dingtalk.Definition(executor))
			if err != nil {
				t.Fatal(err)
			}
			job, occurrence, email := seedObservedNotification(t, db, registry)
			domainBefore := snapshotAvailability(t, db)
			var auditBefore int
			if err := db.owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs`).Scan(&auditBefore); err != nil {
				t.Fatal(err)
			}
			repository := mustJobRepository(t, db.runtime)
			server, session := deliveryReadAPI(t, db, repository)
			checkHealth := func() {
				r := httptest.NewRecorder()
				server.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/api/healthz", nil))
				if r.Code != 200 || !strings.Contains(r.Body.String(), `"status":"ok"`) {
					t.Fatal("delivery failure affected healthz")
				}
				assertNoDeliverySecrets(t, "healthz", r.Body.Bytes(), secrets)
			}
			checkHealth()
			logs := &deliveryLogCapture{}
			worker, err := jobs.NewWorker(repository, registry, jobs.WorkerConfig{Owner: "observability-worker", Concurrency: 1,
				PollInterval: 5 * time.Millisecond, DatabaseBackoff: 5 * time.Millisecond, ShutdownGrace: 2 * time.Second, Retry: runtimeBackoff(), Logger: logs})
			if err != nil {
				t.Fatal(err)
			}
			runWorkerUntil(t, worker, db, job.ID, jobs.StatusFailed, 20*time.Second)
			checkHealth()
			if !bytes.Equal(domainBefore, snapshotAvailability(t, db)) {
				t.Fatal("delivery mutated occurrence truth")
			}
			var auditAfter int
			if err := db.owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs`).Scan(&auditAfter); err != nil {
				t.Fatal(err)
			}
			if auditAfter != auditBefore {
				t.Fatal("delivery added unexpected audit records")
			}
			detail, err := repository.Job(ctx, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if detail.Status != "failed" || detail.JobKind != dingtalk.JobKind || detail.AttemptCount != tc.attempts || detail.MaxAttempts != 5 || detail.ErrorCode == nil || *detail.ErrorCode != tc.code || detail.StartedAt == nil || detail.CompletedAt == nil || detail.CreatedAt.IsZero() || detail.UpdatedAt.IsZero() {
				t.Fatal("failed job diagnostics incomplete")
			}
			if int(hits.Load()) != tc.attempts {
				t.Fatal("controlled delivery count differs from durable attempt count")
			}
			foundFailure := false
			for _, record := range logs.snapshot() {
				if !record.Valid(registry) {
					t.Fatal("invalid structured log")
				}
				if record.Component == jobs.ComponentWorker && record.Action == jobs.ActionExecute && record.JobKind == dingtalk.JobKind && record.Result == jobs.ResultFailure && record.ErrorCode == tc.code {
					foundFailure = true
				}
			}
			if !foundFailure {
				t.Fatal("final failure log missing")
			}
			encodedLogs, err := json.Marshal(logs.snapshot())
			if err != nil {
				t.Fatal(err)
			}
			assertNoDeliverySecrets(t, "structured logs", encodedLogs, secrets)
			for _, path := range []string{"/api/jobs?job_kind=dingtalk_alert_delivery&status=failed", "/api/jobs/" + job.ID.String()} {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				req.AddCookie(&http.Cookie{Name: authn.DevSessionCookieName, Value: session})
				response := httptest.NewRecorder()
				server.ServeHTTP(response, req)
				if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), tc.code) || !strings.Contains(response.Body.String(), `"status":"failed"`) {
					t.Fatalf("Jobs API missing safe failure diagnostics: status %d", response.Code)
				}
				assertNoDeliverySecrets(t, "Jobs API", response.Body.Bytes(), secrets)
				for _, denied := range []string{"payload", "payload_hash", "webhook_url", "access_token", "signing_secret", "refresh_token", "authorization", "raw_response"} {
					if strings.Contains(response.Body.String(), `"`+denied+`"`) {
						t.Fatal("Jobs API exposed delivery material")
					}
				}
			}
			collector, err := jobs.NewCollector(repository)
			if err != nil {
				t.Fatal(err)
			}
			metrics := prometheus.NewRegistry()
			metrics.MustRegister(collector)
			families, err := metrics.Gather()
			if err != nil {
				t.Fatal(err)
			}
			if len(families) != 4 {
				t.Fatal("unexpected notification metrics added")
			}
			for _, family := range families {
				for _, metric := range family.Metric {
					for _, label := range metric.Label {
						if label.GetName() != "status" {
							t.Fatal("high-cardinality metric label")
						}
					}
				}
			}
			response := httptest.NewRecorder()
			promhttp.HandlerFor(metrics, promhttp.HandlerOpts{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
			assertNoDeliverySecrets(t, "metrics", response.Body.Bytes(), append(secrets, email, "antigravity:"+email, occurrence.String(), "Observed node"))
			if !strings.Contains(response.Body.String(), `relay_control_async_jobs{status="failed"} 1`) {
				t.Fatal("failed job absent from existing metrics")
			}
			for _, table := range []string{"async_job_kinds", "async_jobs", "async_job_events", "operation_outbox", "audit_logs", "account_availability_occurrences"} {
				rows, err := db.owner.Query(ctx, "SELECT to_jsonb(t)::text FROM "+pgx.Identifier{table}.Sanitize()+" t")
				if err != nil {
					t.Fatal(err)
				}
				n := 0
				for rows.Next() {
					var data string
					if err := rows.Scan(&data); err != nil {
						rows.Close()
						t.Fatal(err)
					}
					assertNoDeliverySecrets(t, table, []byte(data), secrets)
					n++
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					t.Fatal(err)
				}
				if table != "audit_logs" && n == 0 {
					t.Fatal("secret scan skipped expected durable rows")
				}
			}
			var payload []byte
			if err := db.owner.QueryRow(ctx, `SELECT payload FROM async_jobs WHERE job_id=$1`, job.ID).Scan(&payload); err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(payload, &fields); err != nil {
				t.Fatal(err)
			}
			keys := make([]string, 0, len(fields))
			for key := range fields {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			allowed := []string{"occurrence_id", "occurrence_type", "transition", "reason", "severity", "environment_id", "environment_name", "account_key", "email", "provider", "instance_ids", "node_names", "started_at", "transitioned_at"}
			sort.Strings(allowed)
			if !reflect.DeepEqual(keys, allowed) || fields["email"] != email {
				t.Fatal("payload violated frozen boundary or removed allowed email")
			}
			for _, forbidden := range []string{"webhook_url", "access_token", "signing_secret", "authorization", "credential", "refresh_token", "raw_response"} {
				fields[forbidden] = credential
				invalid, _ := json.Marshal(fields)
				if _, _, _, err := registry.ValidateAndHash(dingtalk.JobKind, 1, invalid); err != jobs.ErrInvalidPayload {
					t.Fatal("credential field accepted by payload schema")
				}
				delete(fields, forbidden)
			}
			t.Log("webhook_canary_hits=0 signing_canary_hits=0 credential_canary_hits=0 raw_response_canary_hits=0")
		})
	}
}

func snapshotAvailability(t *testing.T, db *runtimeJobDatabase) []byte {
	t.Helper()
	var encoded []byte
	if err := db.owner.QueryRow(context.Background(), `SELECT COALESCE(jsonb_agg(to_jsonb(o) ORDER BY occurrence_id),'[]'::jsonb) FROM account_availability_occurrences o`).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	return encoded
}

func seedObservedNotification(t *testing.T, db *runtimeJobDatabase, registry *jobs.Registry) (jobs.Job, uuid.UUID, string) {
	t.Helper()
	ctx := context.Background()
	occurrence, node := uuid.New(), uuid.New()
	email := "observed@example.invalid"
	var now time.Time
	if err := db.owner.QueryRow(ctx, `INSERT INTO account_availability_occurrences(occurrence_id,node_id,account_key,reason,severity,status,first_seen_at,last_failure_at,confirmed_at) VALUES($1,$2,$3,'token_invalid','Critical','ACTIVE',statement_timestamp(),statement_timestamp(),statement_timestamp()) RETURNING confirmed_at`, occurrence, node, "antigravity:"+email).Scan(&now); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(dingtalk.Payload{OccurrenceID: occurrence.String(), OccurrenceType: "TOKEN_INVALID", Transition: "ACTIVE", Reason: "token_invalid", Severity: "Critical", EnvironmentID: "observability", EnvironmentName: "Observability", AccountKey: "antigravity:" + email, Email: email, Provider: "antigravity", InstanceIDs: []string{node.String()}, NodeNames: []string{"Observed node"}, StartedAt: now.UTC().Format(time.RFC3339Nano), TransitionedAt: now.UTC().Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	key := "dingtalk:availability:" + occurrence.String() + ":active"
	tx, err := db.runtime.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	txStore, err := store.NewJobTxStore(tx)
	if err != nil {
		t.Fatal(err)
	}
	result, err := jobs.EnqueueTx(ctx, txStore, registry, jobs.EnqueueRequest{Kind: dingtalk.JobKind, SchemaVersion: 1, Priority: 50, Actor: jobs.ActorService, IdempotencyKey: key, OperationID: uuid.NewSHA1(uuid.MustParse("94db90f6-d7e6-4cce-a045-890b63171d86"), []byte(key)), Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return result.Job, occurrence, email
}

func deliveryReadAPI(t *testing.T, db *runtimeJobDatabase, repository *store.JobRepository) (http.Handler, string) {
	t.Helper()
	ctx := context.Background()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	document := fmt.Sprintf(`{"format_version":1,"environment":"dev","current":1,"keys":[{"version":1,"key":%q}]}`, base64.RawStdEncoding.EncodeToString(key))
	keyring, err := authn.ParseKeyring([]byte(document), authn.EnvironmentDev)
	if err != nil {
		t.Fatal(err)
	}
	service, err := authn.NewService(db.runtime, &authn.ValidatedConfig{Config: authn.Config{Environment: authn.EnvironmentDev, BindAddress: "127.0.0.1:8080"}, Keyring: keyring})
	if err != nil {
		t.Fatal(err)
	}
	admin, sessionID := uuid.New(), uuid.New()
	session := uuid.NewString()
	digest, err := authn.ComputeDigest(keyring, authn.DomainSessionDigest, session)
	if err != nil {
		t.Fatal(err)
	}
	csrf, err := authn.ComputeDigest(keyring, authn.DomainCSRFDigest, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,'observability','Observability','enabled',CURRENT_TIMESTAMP)`, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := db.owner.Exec(ctx, `INSERT INTO control_admin_sessions(session_id,admin_id,token_digest,csrf_digest,key_version,mfa_method,absolute_expires_at) VALUES($1,$2,$3,$4,$5,'none',CURRENT_TIMESTAMP+interval '12 hours')`, sessionID, admin, digest.Sum[:], csrf.Sum[:], int32(digest.KeyVersion)); err != nil {
		t.Fatal(err)
	}
	assets, err := store.NewAssetRepository(db.runtime)
	if err != nil {
		t.Fatal(err)
	}
	server, err := api.NewAuthenticatedServerWithAssetsAndJobs("test", service, assets, nil, repository)
	if err != nil {
		t.Fatal(err)
	}
	return api.HandlerWithOptions(server, api.ChiServerOptions{ErrorHandlerFunc: server.PrepareGeneratedError}), session
}
