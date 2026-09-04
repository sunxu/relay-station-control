package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	authn "github.com/sunxu/relay-station-control/internal/auth"
	duplicatestore "github.com/sunxu/relay-station-control/internal/store"
)

// fakeCrossNodeDuplicateOccurrenceReader is a minimal in-memory double for
// duplicatestore.CrossNodeDuplicateOwnershipOccurrenceReader, used purely
// to exercise the HTTP handler surface (routing, auth, filter/cursor
// plumbing, error mapping) without needing real occurrence fixtures.
type fakeCrossNodeDuplicateOccurrenceReader struct {
	summary  duplicatestore.CrossNodeDuplicateOccurrenceSummary
	evidence duplicatestore.CrossNodeDuplicateOccurrenceEvidenceItem
}

func (reader *fakeCrossNodeDuplicateOccurrenceReader) ListOccurrences(
	_ context.Context, filters duplicatestore.CrossNodeDuplicateOccurrenceFilters, cursor *duplicatestore.CrossNodeDuplicateOccurrenceCursor, limit int,
) (duplicatestore.CrossNodeDuplicateOccurrencePage, error) {
	// Mirrors the real store's enum/limit validation (see
	// cross_node_duplicate_ownership_read_model.go) so this double exercises
	// the handler's error-mapping path the same way production does.
	if limit < 1 || limit > 200 || (filters.Status != "" && filters.Status != "ACTIVE" && filters.Status != "RESOLVED") {
		return duplicatestore.CrossNodeDuplicateOccurrencePage{}, duplicatestore.ErrCrossNodeDuplicateOccurrenceQuery
	}
	if filters.Status != "" && filters.Status != reader.summary.Status {
		return duplicatestore.CrossNodeDuplicateOccurrencePage{}, nil
	}
	if cursor != nil {
		return duplicatestore.CrossNodeDuplicateOccurrencePage{}, nil
	}
	return duplicatestore.CrossNodeDuplicateOccurrencePage{Items: []duplicatestore.CrossNodeDuplicateOccurrenceSummary{reader.summary}, HasMore: true}, nil
}

func (reader *fakeCrossNodeDuplicateOccurrenceReader) GetOccurrence(_ context.Context, occurrenceID uuid.UUID) (duplicatestore.CrossNodeDuplicateOccurrenceSummary, error) {
	if occurrenceID != reader.summary.OccurrenceID {
		return duplicatestore.CrossNodeDuplicateOccurrenceSummary{}, duplicatestore.ErrCrossNodeDuplicateOccurrenceNotFound
	}
	return reader.summary, nil
}

func (reader *fakeCrossNodeDuplicateOccurrenceReader) ListOccurrenceEvidence(
	_ context.Context, occurrenceID uuid.UUID, _ *time.Time, _ uuid.UUID, _ int,
) (duplicatestore.CrossNodeDuplicateOccurrenceEvidencePage, error) {
	if occurrenceID != reader.summary.OccurrenceID {
		return duplicatestore.CrossNodeDuplicateOccurrenceEvidencePage{}, duplicatestore.ErrCrossNodeDuplicateOccurrenceNotFound
	}
	return duplicatestore.CrossNodeDuplicateOccurrenceEvidencePage{Items: []duplicatestore.CrossNodeDuplicateOccurrenceEvidenceItem{reader.evidence}}, nil
}

var _ duplicatestore.CrossNodeDuplicateOwnershipOccurrenceReader = (*fakeCrossNodeDuplicateOccurrenceReader)(nil)

func TestCrossNodeDuplicateOccurrenceHTTPReadOnly(t *testing.T) {
	_, runtimeURL := isolatedRuntimeDatabaseURLs(t)
	ctx := context.Background()
	runtime, err := pgxpool.New(ctx, runtimeURL)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	config := testValidatedConfig(t, authn.EnvironmentDev, false)
	service, err := authn.NewService(runtime, config)
	if err != nil {
		t.Fatal(err)
	}
	adminID := uuid.New()
	token := "duplicate-session-" + uuid.NewString()
	tokenDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainSessionDigest, token)
	if err != nil {
		t.Fatal(err)
	}
	csrfDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainCSRFDigest, "duplicate-csrf-"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at)
		VALUES($1,$2,'Duplicate Reader','enabled',CURRENT_TIMESTAMP)`, adminID, "duplicate_reader_"+adminID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.Exec(ctx, `INSERT INTO control_admin_sessions
		(session_id,admin_id,token_digest,csrf_digest,key_version,mfa_method,absolute_expires_at)
		VALUES($1,$2,$3,$4,$5,'none',CURRENT_TIMESTAMP+interval '12 hours')`,
		uuid.New(), adminID, tokenDigest.Sum[:], csrfDigest.Sum[:], int32(tokenDigest.KeyVersion)); err != nil {
		t.Fatal(err)
	}

	occurrenceID := uuid.New()
	nodeA, nodeB := uuid.New(), uuid.New()
	evaluationID := uuid.New()
	seenAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	summary := duplicatestore.CrossNodeDuplicateOccurrenceSummary{
		OccurrenceID: occurrenceID, EnvironmentID: "dev", AccountKey: "openai:duplicate-http@example.invalid",
		ConflictType: "cross_node_duplicate_ownership", Status: "ACTIVE", Severity: "Critical",
		FirstSeenAt: seenAt, LastSeenAt: seenAt, EvidenceState: "complete", LatestEvaluationID: &evaluationID,
		AffectedNodes: []uuid.UUID{nodeA, nodeB},
	}
	evidence := duplicatestore.CrossNodeDuplicateOccurrenceEvidenceItem{
		ObservationID: uuid.New(), InstanceID: nodeA, ObservationKind: "owner_confirmed",
		SourceProvider: "openai", SourceScheduledAt: seenAt, SourceCompletedAt: seenAt,
		EvaluationID: evaluationID, EvaluationAt: seenAt, RecordedAt: seenAt,
	}
	reader := &fakeCrossNodeDuplicateOccurrenceReader{summary: summary, evidence: evidence}

	server := NewAuthenticatedServer("test", service)
	server.SetCrossNodeDuplicateOwnershipOccurrenceReader(reader)
	handler := HandlerWithOptions(server, ChiServerOptions{ErrorHandlerFunc: server.PrepareGeneratedError})
	do := func(method, path, cookie string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, nil)
		if cookie != "" {
			request.AddCookie(&http.Cookie{Name: authn.DevSessionCookieName, Value: cookie})
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	t.Run("unauthenticated requests are rejected", func(t *testing.T) {
		for _, path := range []string{
			"/api/cross-node-duplicate-occurrences",
			"/api/cross-node-duplicate-occurrences/" + occurrenceID.String(),
			"/api/cross-node-duplicate-occurrences/" + occurrenceID.String() + "/evidence",
		} {
			response := do(http.MethodGet, path, "")
			if response.Code != http.StatusUnauthorized || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("%s status=%d headers=%v body=%s", path, response.Code, response.Header(), response.Body.String())
			}
		}
	})

	t.Run("list returns plaintext account_key and affected nodes", func(t *testing.T) {
		response := do(http.MethodGet, "/api/cross-node-duplicate-occurrences?status=ACTIVE", token)
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
		}
		var page CrossNodeDuplicateOccurrenceListResponse
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.NextCursor == nil {
			t.Fatalf("page=%+v err=%v body=%s", page, err, response.Body.String())
		}
		item := page.Items[0]
		if item.AccountKey != summary.AccountKey {
			t.Fatalf("account_key = %q, want plaintext %q (no masking, frozen non-goal)", item.AccountKey, summary.AccountKey)
		}
		if len(item.AffectedNodes) != 2 {
			t.Fatalf("affected_nodes = %v, want 2 entries", item.AffectedNodes)
		}
	})

	t.Run("list with a non-matching status filter returns an empty page", func(t *testing.T) {
		response := do(http.MethodGet, "/api/cross-node-duplicate-occurrences?status=RESOLVED", token)
		var page CrossNodeDuplicateOccurrenceListResponse
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Items) != 0 {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("invalid query params are rejected", func(t *testing.T) {
		for _, path := range []string{
			"/api/cross-node-duplicate-occurrences?status=bogus",
			"/api/cross-node-duplicate-occurrences?limit=0",
			"/api/cross-node-duplicate-occurrences?limit=201",
			"/api/cross-node-duplicate-occurrences?instance_id=not-a-uuid",
			"/api/cross-node-duplicate-occurrences?cursor=not-valid-base64!!",
			"/api/cross-node-duplicate-occurrences/not-a-uuid",
		} {
			response := do(http.MethodGet, path, token)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
			}
		}
	})

	t.Run("detail returns the occurrence", func(t *testing.T) {
		response := do(http.MethodGet, "/api/cross-node-duplicate-occurrences/"+occurrenceID.String(), token)
		var detail CrossNodeDuplicateOccurrenceSummary
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &detail) != nil || detail.OccurrenceId != occurrenceID {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("detail for an unknown occurrence is 404", func(t *testing.T) {
		response := do(http.MethodGet, "/api/cross-node-duplicate-occurrences/"+uuid.NewString(), token)
		if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"not_found"`) {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("evidence sub-resource returns bounded evidence for the occurrence", func(t *testing.T) {
		response := do(http.MethodGet, "/api/cross-node-duplicate-occurrences/"+occurrenceID.String()+"/evidence", token)
		var page CrossNodeDuplicateOccurrenceEvidenceListResponse
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Items) != 1 {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		if page.Items[0].ObservationId != evidence.ObservationID {
			t.Fatalf("observation_id = %v, want %v", page.Items[0].ObservationId, evidence.ObservationID)
		}
	})

	t.Run("write methods are not allowed on read-only resources", func(t *testing.T) {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			for _, path := range []string{
				"/api/cross-node-duplicate-occurrences",
				"/api/cross-node-duplicate-occurrences/" + occurrenceID.String(),
				"/api/cross-node-duplicate-occurrences/" + occurrenceID.String() + "/evidence",
			} {
				response := do(method, path, token)
				if response.Code != http.StatusMethodNotAllowed {
					t.Fatalf("%s %s status=%d body=%s", method, path, response.Code, response.Body.String())
				}
			}
		}
	})

	t.Run("read is unavailable when the reader dependency is unset", func(t *testing.T) {
		bareServer := NewAuthenticatedServer("test", service)
		bareHandler := HandlerWithOptions(bareServer, ChiServerOptions{ErrorHandlerFunc: bareServer.PrepareGeneratedError})
		request := httptest.NewRequest(http.MethodGet, "/api/cross-node-duplicate-occurrences", nil)
		request.AddCookie(&http.Cookie{Name: authn.DevSessionCookieName, Value: token})
		response := httptest.NewRecorder()
		bareHandler.ServeHTTP(response, request)
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	})
}
