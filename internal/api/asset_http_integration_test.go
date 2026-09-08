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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	authn "github.com/sunxu/relay-station-control/internal/auth"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

type faultingAssetReader struct {
	assetstore.AssetReader
	fail atomic.Bool
}

func (reader *faultingAssetReader) Gateway(ctx context.Context) (*assetstore.GatewayAsset, error) {
	if reader.fail.Load() {
		return nil, errors.New("SECRET database-url=postgres://asset-canary")
	}
	return reader.AssetReader.Gateway(ctx)
}

func TestAssetRegistryHTTPRuntimeReadsPaginationSecurityAndRecovery(t *testing.T) {
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

	secretCanary := "vault://asset-secret-canary-" + uuid.NewString()
	if _, err = owner.Exec(ctx, `INSERT INTO environments(environment_id,name,environment_type) VALUES('asset-test','Asset Test','dev')`); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO node_drivers(node_type,driver_contract_version,display_name,lifecycle_status)
		VALUES('cliproxy.api','v1','CLI Proxy API','active')`); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO driver_capabilities(node_type,driver_contract_version,capability) VALUES
		('cliproxy.api','v1','management_health_read'),
		('cliproxy.api','v1','management_account_inventory_read')`); err != nil {
		t.Fatal(err)
	}
	gatewayID, firstNodeID, secondNodeID := uuid.New(), uuid.MustParse("018f80d8-2017-7b3e-93ec-10f4b3672f2a"), uuid.MustParse("018f80d8-2017-7b3e-93ec-10f4b3672f2b")
	if _, err = owner.Exec(ctx, `INSERT INTO gateway_instances(instance_id,display_name,management_endpoint,reader_secret_ref)
		VALUES($1,'Gateway','https://gateway.example/management',$2)`, gatewayID, secretCanary); err != nil {
		t.Fatal(err)
	}
	for id, name := range map[uuid.UUID]string{firstNodeID: "Node A", secondNodeID: "Node B"} {
		if _, err = owner.Exec(ctx, `INSERT INTO relay_node_assets(instance_id,display_name,node_type,driver_contract_version,management_endpoint,reader_secret_ref)
			VALUES($1,$2,'cliproxy.api','v1','http://node.example:8080/management',$3)`, id, name, secretCanary); err != nil {
			t.Fatal(err)
		}
		if _, err = owner.Exec(ctx, `INSERT INTO node_capabilities(instance_id,node_type,driver_contract_version,capability) VALUES
			($1,'cliproxy.api','v1','management_health_read'),
			($1,'cliproxy.api','v1','management_account_inventory_read')`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = owner.Exec(ctx, `WITH boundary AS (SELECT clock_timestamp() AS at)
		INSERT INTO relay_node_inventory_monitoring_activations(instance_id,effective_from,reason,actor,created_at)
		SELECT $1,at,'deployment_enable','asset-test',at FROM boundary`, firstNodeID); err != nil {
		t.Fatal(err)
	}
	policyID := uuid.New()
	if _, err = owner.Exec(ctx, `INSERT INTO provider_inventory_policy_versions
		(policy_version_id,node_type,driver_contract_version,active_providers,out_of_scope_providers,created_by)
		VALUES($1,'cliproxy.api','v1',ARRAY['anthropic','openai'],ARRAY['legacy'],'asset-test')`, policyID); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `WITH boundary AS (SELECT clock_timestamp() AS at)
		INSERT INTO provider_inventory_policy_activations
		(node_type,driver_contract_version,policy_version_id,effective_from,activated_by,created_at)
		SELECT 'cliproxy.api','v1',$1,at,'asset-test',at FROM boundary`, policyID); err != nil {
		t.Fatal(err)
	}

	config := testValidatedConfig(t, authn.EnvironmentDev)
	service, err := authn.NewService(runtime, config)
	if err != nil {
		t.Fatal(err)
	}
	adminID := uuid.New()
	token := "asset-session-" + uuid.NewString()
	tokenDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainSessionDigest, token)
	if err != nil {
		t.Fatal(err)
	}
	csrfDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainCSRFDigest, "asset-csrf-"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at)
		VALUES($1,$2,'Asset Reader','enabled',CURRENT_TIMESTAMP)`, adminID, "asset_reader_"+adminID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_sessions
		(session_id,admin_id,token_digest,csrf_digest,key_version,mfa_method,absolute_expires_at)
		VALUES($1,$2,$3,$4,$5,'none',CURRENT_TIMESTAMP+interval '12 hours')`,
		uuid.New(), adminID, tokenDigest.Sum[:], csrfDigest.Sum[:], int32(tokenDigest.KeyVersion)); err != nil {
		t.Fatal(err)
	}

	repository, err := assetstore.NewAssetRepository(runtime)
	if err != nil {
		t.Fatal(err)
	}
	faults := &faultingAssetReader{AssetReader: repository}
	metrics := NewAssetMetrics()
	server, err := NewAuthenticatedServerWithAssets("test", service, faults, metrics)
	if err != nil {
		t.Fatal(err)
	}
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

	protectedPaths := []string{
		"/api/environment", "/api/assets/gateway", "/api/assets/nodes",
		"/api/assets/nodes/" + firstNodeID.String(), "/api/assets/drivers",
		"/api/assets/provider-policies/current?node_type=cliproxy.api&driver_contract_version=v1",
	}
	for _, path := range protectedPaths {
		anonymous := do(http.MethodGet, path, "")
		if anonymous.Code != http.StatusUnauthorized || strings.Contains(anonymous.Body.String(), "Gateway") {
			t.Fatalf("anonymous %s status=%d body=%s", path, anonymous.Code, anonymous.Body.String())
		}
		response := do(http.MethodGet, path, token)
		if response.Code != http.StatusOK {
			t.Fatalf("authenticated %s status=%d body=%s", path, response.Code, response.Body.String())
		}
		if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Request-ID") == "" {
			t.Fatalf("authenticated %s headers=%v", path, response.Header())
		}
		if strings.Contains(response.Body.String(), secretCanary) || strings.Contains(response.Body.String(), "reader_secret_ref") {
			t.Fatalf("secret leaked from %s: %s", path, response.Body.String())
		}
	}
	policyResponse := do(http.MethodGet, "/api/assets/provider-policies/current?node_type=cliproxy.api&driver_contract_version=v1", token)
	var policy CurrentProviderInventoryPolicyResponse
	if err = json.Unmarshal(policyResponse.Body.Bytes(), &policy); err != nil || policy.Status != Configured || policy.Policy == nil ||
		strings.Join(providerStrings(policy.Policy.ActiveProviders), ",") != "anthropic,openai" ||
		strings.Join(providerStrings(policy.Policy.OutOfScopeProviders), ",") != "legacy" {
		t.Fatalf("current policy=%+v err=%v body=%s", policy, err, policyResponse.Body.String())
	}
	policy = CurrentProviderInventoryPolicyResponse{}
	unconfigured := do(http.MethodGet, "/api/assets/provider-policies/current?node_type=other.node&driver_contract_version=v1", token)
	if err = json.Unmarshal(unconfigured.Body.Bytes(), &policy); err != nil || policy.Status != NotConfigured || policy.Policy != nil {
		t.Fatalf("unconfigured policy=%+v err=%v body=%s", policy, err, unconfigured.Body.String())
	}

	firstPage := do(http.MethodGet, "/api/assets/nodes?limit=1&capability=management_health_read", token)
	var page NodeAssetListResponse
	if err = json.Unmarshal(firstPage.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.NextCursor == nil {
		t.Fatalf("first page=%+v err=%v body=%s", page, err, firstPage.Body.String())
	}
	nextCursor := *page.NextCursor
	page = NodeAssetListResponse{}
	secondPage := do(http.MethodGet, "/api/assets/nodes?limit=1&capability=management_health_read&cursor="+nextCursor, token)
	if err = json.Unmarshal(secondPage.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.NextCursor != nil || page.Items[0].InstanceId != secondNodeID {
		t.Fatalf("second page=%+v err=%v body=%s", page, err, secondPage.Body.String())
	}
	invalidCursor := do(http.MethodGet, "/api/assets/nodes?cursor=invalid", token)
	if invalidCursor.Code != http.StatusBadRequest || invalidCursor.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("invalid cursor status=%d headers=%v body=%s", invalidCursor.Code, invalidCursor.Header(), invalidCursor.Body.String())
	}
	for _, path := range []string{
		"/api/assets/nodes?cursor=",
		"/api/assets/nodes?node_type=A!", "/api/assets/nodes?node_type=" + strings.Repeat("a", 65),
		"/api/assets/nodes?capability=unknown_capability",
		"/api/assets/provider-policies/current?node_type=A!&driver_contract_version=v1",
		"/api/assets/provider-policies/current?node_type=cliproxy.api&driver_contract_version=V1!",
	} {
		invalid := do(http.MethodGet, path, token)
		if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), `"code":"validation_failed"`) {
			t.Fatalf("invalid filter %s status=%d body=%s", path, invalid.Code, invalid.Body.String())
		}
	}
	notFound := do(http.MethodGet, "/api/assets/nodes/"+uuid.NewString(), token)
	if notFound.Code != http.StatusNotFound || !strings.Contains(notFound.Body.String(), `"code":"not_found"`) {
		t.Fatalf("not found status=%d body=%s", notFound.Code, notFound.Body.String())
	}

	var logBuffer bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuffer, nil)))
	faults.fail.Store(true)
	unavailable := do(http.MethodGet, "/api/assets/gateway", token)
	slog.SetDefault(previousLogger)
	if unavailable.Code != http.StatusServiceUnavailable || strings.Contains(unavailable.Body.String(), "postgres://") || strings.Contains(unavailable.Body.String(), "SECRET") {
		t.Fatalf("database failure status=%d body=%s", unavailable.Code, unavailable.Body.String())
	}
	if logs := logBuffer.String(); !strings.Contains(logs, "reason=database_unavailable") || strings.Contains(logs, "postgres://") || strings.Contains(logs, "SECRET") {
		t.Fatalf("database failure log was not sanitized: %s", logs)
	}
	faults.fail.Store(false)
	recovered := do(http.MethodGet, "/api/assets/gateway", token)
	if recovered.Code != http.StatusOK || !strings.Contains(recovered.Body.String(), `"secret_configured":true`) {
		t.Fatalf("recovered status=%d body=%s", recovered.Code, recovered.Body.String())
	}

	var before, after int64
	if err = owner.QueryRow(ctx, `SELECT count(*) FROM gateway_instances`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		response := do(method, "/api/assets/gateway", token)
		if response.Code != http.StatusMethodNotAllowed {
			t.Fatalf("asset write %s status=%d body=%s", method, response.Code, response.Body.String())
		}
	}
	if err = owner.QueryRow(ctx, `SELECT count(*) FROM gateway_instances`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("asset write methods changed gateway count: %d -> %d", before, after)
	}
	metricJSON, err := json.Marshal(metrics.snapshot())
	if err != nil || strings.Contains(string(metricJSON), secretCanary) || strings.Contains(string(metricJSON), gatewayID.String()) {
		t.Fatalf("asset metrics leaked identity: %s err=%v", metricJSON, err)
	}
	var auditJSON string
	if err = owner.QueryRow(ctx, `SELECT COALESCE(string_agg(row_to_json(a)::text,''),'') FROM audit_logs AS a`).Scan(&auditJSON); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(auditJSON, secretCanary) || strings.Contains(auditJSON, "gateway.example") {
		t.Fatalf("asset identity leaked to audit: %s", auditJSON)
	}

	// These exclusions are a primary write-side defense. Dropping them only in
	// this disposable database simulates catalog damage or an out-of-band write
	// and verifies that every current-state read independently fails closed.
	dropAssetExclusionConstraint(t, ctx, owner, "relay_node_inventory_monitoring_activations")
	dropAssetExclusionConstraint(t, ctx, owner, "provider_inventory_policy_activations")
	if _, err = owner.Exec(ctx, `WITH boundary AS (SELECT clock_timestamp() AS at)
		INSERT INTO relay_node_inventory_monitoring_activations
		(instance_id,effective_from,reason,actor,created_at)
		SELECT $1,at,'deployment_enable','damaged-fixture',at FROM boundary`, firstNodeID); err != nil {
		t.Fatalf("insert overlapping monitoring fixture: %v", err)
	}
	overlappingPolicyID := uuid.New()
	if _, err = owner.Exec(ctx, `INSERT INTO provider_inventory_policy_versions
		(policy_version_id,node_type,driver_contract_version,active_providers,out_of_scope_providers,created_by)
		VALUES($1,'cliproxy.api','v1',ARRAY['gemini'],ARRAY['legacy'],'damaged-fixture')`, overlappingPolicyID); err != nil {
		t.Fatalf("insert overlapping policy version fixture: %v", err)
	}
	if _, err = owner.Exec(ctx, `WITH boundary AS (SELECT clock_timestamp() AS at)
		INSERT INTO provider_inventory_policy_activations
		(node_type,driver_contract_version,policy_version_id,effective_from,activated_by,created_at)
		SELECT 'cliproxy.api','v1',$1,at,'damaged-fixture',at FROM boundary`, overlappingPolicyID); err != nil {
		t.Fatalf("insert overlapping policy activation fixture: %v", err)
	}

	if _, err = repository.Node(ctx, firstNodeID); !errors.Is(err, assetstore.ErrAssetRegistryInconsistent) {
		t.Fatalf("damaged node detail error = %v, want fixed consistency error", err)
	}
	for _, monitoringActive := range []bool{true, false} {
		filter := monitoringActive
		if _, err = repository.ListNodes(ctx, assetstore.NodeListFilters{MonitoringActive: &filter}, uuid.Nil, 200); !errors.Is(err, assetstore.ErrAssetRegistryInconsistent) {
			t.Fatalf("damaged node list monitoring_active=%v error = %v, want fixed consistency error", monitoringActive, err)
		}
	}
	if _, err = repository.CurrentProviderPolicy(ctx, "cliproxy.api", "v1"); !errors.Is(err, assetstore.ErrAssetRegistryInconsistent) {
		t.Fatalf("damaged provider policy error = %v, want fixed consistency error", err)
	}

	for _, path := range []string{
		"/api/assets/nodes/" + firstNodeID.String(),
		"/api/assets/nodes?monitoring_active=true",
		"/api/assets/nodes?monitoring_active=false",
		"/api/assets/provider-policies/current?node_type=cliproxy.api&driver_contract_version=v1",
	} {
		response := do(http.MethodGet, path, token)
		body := response.Body.String()
		if response.Code != http.StatusServiceUnavailable || response.Header().Get("Cache-Control") != "no-store" ||
			!strings.Contains(body, `"code":"temporarily_unavailable"`) || strings.Contains(body, "inconsistent") ||
			strings.Contains(body, firstNodeID.String()) || strings.Contains(body, overlappingPolicyID.String()) || strings.Contains(body, secretCanary) {
			t.Fatalf("damaged registry %s status=%d headers=%v body=%s", path, response.Code, response.Header(), body)
		}
	}
}

func dropAssetExclusionConstraint(t *testing.T, ctx context.Context, owner *pgxpool.Pool, table string) {
	t.Helper()
	var constraint string
	if err := owner.QueryRow(ctx, `SELECT conname FROM pg_constraint WHERE conrelid=$1::regclass AND contype='x'`, table).Scan(&constraint); err != nil {
		t.Fatalf("find exclusion constraint on %s: %v", table, err)
	}
	statement := "ALTER TABLE " + pgx.Identifier{table}.Sanitize() + " DROP CONSTRAINT " + pgx.Identifier{constraint}.Sanitize()
	if _, err := owner.Exec(ctx, statement); err != nil {
		t.Fatalf("drop exclusion constraint on %s: %v", table, err)
	}
}

func providerStrings(values []ProviderName) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, string(value))
	}
	return result
}

func TestAssetRegistryRejectsForgedExpiredAndRevokedSessions(t *testing.T) {
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
	if _, err := owner.Exec(ctx, `INSERT INTO environments(environment_id,name,environment_type) VALUES('asset-auth-test','Asset Auth Test','dev')`); err != nil {
		t.Fatal(err)
	}
	config := testValidatedConfig(t, authn.EnvironmentDev)
	service, err := authn.NewService(runtime, config)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := assetstore.NewAssetRepository(runtime)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewAuthenticatedServerWithAssets("test", service, repository, NewAssetMetrics())
	if err != nil {
		t.Fatal(err)
	}
	handler := HandlerWithOptions(server, ChiServerOptions{ErrorHandlerFunc: server.PrepareGeneratedError})
	adminID := uuid.New()
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at)
		VALUES($1,$2,'Asset Auth Reader','enabled',CURRENT_TIMESTAMP)`, adminID, "asset_auth_"+adminID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	createSession := func(token, state string) {
		digest, _ := authn.ComputeDigest(config.Keyring, authn.DomainSessionDigest, token)
		csrf, _ := authn.ComputeDigest(config.Keyring, authn.DomainCSRFDigest, "csrf-"+uuid.NewString())
		revokedAt, reason := any(nil), any(nil)
		createdAt, absoluteExpiresAt := time.Now().UTC(), time.Now().UTC().Add(time.Hour)
		if state == "revoked" {
			revokedAt, reason = time.Now().UTC(), "operator_revoked"
		}
		if state == "expired" {
			createdAt, absoluteExpiresAt = time.Now().UTC().Add(-2*time.Hour), time.Now().UTC().Add(-time.Hour)
		}
		_, err = owner.Exec(ctx, `INSERT INTO control_admin_sessions
			(session_id,admin_id,token_digest,csrf_digest,key_version,mfa_method,created_at,last_activity_at,absolute_expires_at,revoked_at,revoke_reason)
			VALUES($1,$2,$3,$4,$5,'none',$6,$6,$7,$8,$9)`, uuid.New(), adminID, digest.Sum[:], csrf.Sum[:], int32(digest.KeyVersion), createdAt, absoluteExpiresAt, revokedAt, reason)
		if err != nil {
			t.Fatal(err)
		}
	}
	createSession("revoked-asset-session", "revoked")
	createSession("expired-asset-session", "expired")
	for name, token := range map[string]string{"forged": "forged-cookie", "revoked": "revoked-asset-session", "expired": "expired-asset-session"} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/assets/gateway", nil)
			request.AddCookie(&http.Cookie{Name: authn.DevSessionCookieName, Value: token})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
			}
		})
	}
}
