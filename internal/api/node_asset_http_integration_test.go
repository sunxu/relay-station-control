package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	authn "github.com/sunxu/relay-station-control/internal/auth"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

func TestNodeAssetHTTPRoutesLifecycleReplayAndCursorPG18(t *testing.T) {
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

	if _, err = owner.Exec(ctx, `INSERT INTO environments(environment_id,name,environment_type)
		VALUES('node-http-test','Node HTTP Test','dev')`); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO node_drivers(node_type,driver_contract_version,display_name)
		VALUES('cliproxyapi','v1','CLIProxyAPI')`); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO driver_capabilities(node_type,driver_contract_version,capability)
		VALUES('cliproxyapi','v1','management_account_inventory_read'),
		      ('cliproxyapi','v1','management_health_read')`); err != nil {
		t.Fatal(err)
	}

	config := testValidatedConfig(t, authn.EnvironmentDev)
	service, err := authn.NewService(runtime, config)
	if err != nil {
		t.Fatal(err)
	}
	adminID := uuid.New()
	token := "node-http-session-" + uuid.NewString()
	csrf := "node-http-csrf-" + uuid.NewString()
	tokenDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainSessionDigest, token)
	if err != nil {
		t.Fatal(err)
	}
	csrfDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainCSRFDigest, csrf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,role,status,activated_at)
		VALUES($1,$2,'Node API Super Admin','super_admin','enabled',clock_timestamp())`, adminID, "node_http_"+adminID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_sessions(session_id,admin_id,token_digest,csrf_digest,key_version,mfa_method,absolute_expires_at)
		VALUES($1,$2,$3,$4,$5,'none',clock_timestamp()+interval '1 hour')`,
		uuid.New(), adminID, tokenDigest.Sum[:], csrfDigest.Sum[:], int32(tokenDigest.KeyVersion)); err != nil {
		t.Fatal(err)
	}
	assets, err := assetstore.NewAssetRepository(runtime)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := assetstore.NewNodeLifecycleRepositoryWithSealer(runtime, []byte("01234567890123456789012345678901"), apiTestCredentialSealer{})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewAuthenticatedServerWithAssets("test", service, assets, NewAssetMetrics())
	if err != nil {
		t.Fatal(err)
	}
	if err = server.SetNodeLifecycleManager(manager); err != nil {
		t.Fatal(err)
	}
	handler := HandlerWithOptions(server, ChiServerOptions{ErrorHandlerFunc: server.PrepareGeneratedError})
	do := func(method, path, body, csrfProof string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, "http://127.0.0.1:18080"+path, strings.NewReader(body))
		request.Header.Set("Origin", "http://127.0.0.1:18080")
		request.Header.Set("Content-Type", "application/json")
		if csrfProof != "" {
			request.Header.Set("X-CSRF-Token", csrfProof)
		}
		request.AddCookie(&http.Cookie{Name: authn.DevSessionCookieName, Value: token})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Request-ID") == "" {
			t.Fatalf("%s %s missing security headers: %v", method, path, response.Header())
		}
		return response
	}

	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/assets/nodes", nil))
	if unauthenticated.Code != http.StatusUnauthorized || unauthenticated.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unauthenticated status=%d headers=%v", unauthenticated.Code, unauthenticated.Header())
	}
	if response := do(http.MethodPost, "/api/assets/nodes", `{}`, ""); response.Code != http.StatusForbidden {
		t.Fatalf("missing csrf status=%d body=%s", response.Code, response.Body.String())
	}

	ids := []uuid.UUID{
		uuid.MustParse("00000000-0000-4000-8000-000000000101"),
		uuid.MustParse("00000000-0000-4000-8000-000000000102"),
		uuid.MustParse("00000000-0000-4000-8000-000000000103"),
	}
	registerBodies := make([]string, len(ids))
	for index, id := range ids {
		registerBodies[index] = fmt.Sprintf(`{"command_id":"%s","new_instance_id":"%s","display_name":"Node %d","management_endpoint":"http://node-%d.example","node_type":"cliproxyapi","driver_contract_version":"v1","capabilities":["management_account_inventory_read","management_health_read"],"management_credential":"node-secret-%d"}`,
			uuid.New(), id, index+1, index+1, index+1)
		response := do(http.MethodPost, "/api/assets/nodes", registerBodies[index], csrf)
		if response.Code != http.StatusCreated || strings.Contains(response.Body.String(), "management_credential") || !strings.Contains(response.Body.String(), `"revision":"1"`) {
			t.Fatalf("register %d status=%d body=%s", index, response.Code, response.Body.String())
		}
		if index == 0 {
			replay := do(http.MethodPost, "/api/assets/nodes", registerBodies[index], csrf)
			if replay.Code != http.StatusCreated || !gatewayJSONEqual(response.Body.Bytes(), replay.Body.Bytes()) {
				t.Fatalf("register replay status=%d body=%s", replay.Code, replay.Body.String())
			}
		}
	}
	nullRegister := fmt.Sprintf(`{"command_id":"%s","new_instance_id":"%s","display_name":"Null Secret","management_endpoint":"http://null-secret.example","node_type":"cliproxyapi","driver_contract_version":"v1","capabilities":["management_health_read"],"management_credential":null}`, uuid.New(), uuid.New())
	if response := do(http.MethodPost, "/api/assets/nodes", nullRegister, csrf); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"secret_configuration_invalid"`) {
		t.Fatalf("null register credential status=%d body=%s", response.Code, response.Body.String())
	}

	var beforeInvalidNodes, beforeInvalidReceipts, beforeInvalidAudits int
	if err = owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM relay_node_assets),
		(SELECT count(*) FROM asset_admin_command_receipts WHERE command_kind LIKE 'node.%'),
		(SELECT count(*) FROM audit_logs WHERE category='asset_node')`).Scan(&beforeInvalidNodes, &beforeInvalidReceipts, &beforeInvalidAudits); err != nil {
		t.Fatal(err)
	}
	invalidRegisterID := uuid.MustParse("00000000-0000-4000-8000-000000000105")
	invalidRegister := fmt.Sprintf(`{"command_id":"%s","new_instance_id":"%s","display_name":"Invalid HTTPS","management_endpoint":"https://invalid-register.example","node_type":"cliproxyapi","driver_contract_version":"v1","capabilities":["management_health_read"]}`,
		uuid.New(), invalidRegisterID)
	invalidEdit := fmt.Sprintf(`{"command_id":"%s","expected_revision":"1","management_endpoint":"https://invalid-edit.example"}`, uuid.New())
	invalidReplaceID := uuid.MustParse("00000000-0000-4000-8000-000000000106")
	invalidReplace := fmt.Sprintf(`{"command_id":"%s","expected_revision":"1","new_instance_id":"%s","display_name":"Invalid replacement","management_endpoint":"https://invalid-replace.example","node_type":"cliproxyapi","driver_contract_version":"v1","capabilities":["management_health_read"]}`,
		uuid.New(), invalidReplaceID)
	for name, request := range map[string]struct{ method, path, body string }{
		"register": {method: http.MethodPost, path: "/api/assets/nodes", body: invalidRegister},
		"edit":     {method: http.MethodPatch, path: "/api/assets/nodes/" + ids[1].String(), body: invalidEdit},
		"replace":  {method: http.MethodPost, path: "/api/assets/nodes/" + ids[1].String() + "/replace", body: invalidReplace},
	} {
		response := do(request.method, request.path, request.body, csrf)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_endpoint"`) {
			t.Fatalf("%s HTTPS admission status=%d body=%s", name, response.Code, response.Body.String())
		}
	}
	var afterInvalidNodes, afterInvalidReceipts, afterInvalidAudits int
	if err = owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM relay_node_assets),
		(SELECT count(*) FROM asset_admin_command_receipts WHERE command_kind LIKE 'node.%'),
		(SELECT count(*) FROM audit_logs WHERE category='asset_node')`).Scan(&afterInvalidNodes, &afterInvalidReceipts, &afterInvalidAudits); err != nil {
		t.Fatal(err)
	}
	if beforeInvalidNodes != afterInvalidNodes || beforeInvalidReceipts != afterInvalidReceipts || beforeInvalidAudits != afterInvalidAudits {
		t.Fatalf("HTTPS admission changed nodes/receipts/audits=%d/%d/%d -> %d/%d/%d", beforeInvalidNodes, beforeInvalidReceipts, beforeInvalidAudits, afterInvalidNodes, afterInvalidReceipts, afterInvalidAudits)
	}

	registrar, err := owner.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = registrar.Exec(ctx, `SET ROLE relay_control_asset_registrar`); err != nil {
		registrar.Release()
		t.Fatal(err)
	}
	var disableFence *uuid.UUID
	if err = registrar.QueryRow(ctx, `SELECT control_latest_node_disable_fence_v1($1)`, ids[1]).Scan(&disableFence); err != nil {
		registrar.Release()
		t.Fatal(err)
	}
	if _, err = registrar.Exec(ctx, `SELECT control_set_node_inventory_monitoring($1,true,clock_timestamp()+interval '600 milliseconds','scheduled_enable','node-http-test',$2)`, ids[1], disableFence); err != nil {
		registrar.Release()
		t.Fatal(err)
	}
	registrar.Release()
	firstPage := do(http.MethodGet, "/api/assets/nodes?lifecycle=active&monitoring_active=false&limit=1", "", "")
	if firstPage.Code != http.StatusOK || !strings.Contains(firstPage.Body.String(), ids[0].String()) {
		t.Fatalf("first page status=%d body=%s", firstPage.Code, firstPage.Body.String())
	}
	var page NodeAssetListResponse
	if err = json.Unmarshal(firstPage.Body.Bytes(), &page); err != nil || page.NextCursor == nil || page.NodeCounts.Total != 3 {
		t.Fatalf("first page=%+v err=%v", page, err)
	}
	time.Sleep(750 * time.Millisecond)
	secondPage := do(http.MethodGet, "/api/assets/nodes?lifecycle=active&monitoring_active=false&limit=1&cursor="+url.QueryEscape(*page.NextCursor), "", "")
	if secondPage.Code != http.StatusOK || !strings.Contains(secondPage.Body.String(), ids[1].String()) {
		t.Fatalf("read_as_of page status=%d body=%s", secondPage.Code, secondPage.Body.String())
	}

	editBody := fmt.Sprintf(`{"command_id":"%s","expected_revision":"1","display_name":"Node 3 edited"}`, uuid.New())
	edited := do(http.MethodPatch, "/api/assets/nodes/"+ids[2].String(), editBody, csrf)
	if edited.Code != http.StatusOK || !strings.Contains(edited.Body.String(), `"revision":"2"`) {
		t.Fatalf("edit status=%d body=%s", edited.Code, edited.Body.String())
	}
	setBody := fmt.Sprintf(`{"command_id":"%s","expected_revision":"2","management_credential":"node-secret-edited"}`, uuid.New())
	set := do(http.MethodPatch, "/api/assets/nodes/"+ids[2].String(), setBody, csrf)
	if set.Code != http.StatusOK || !strings.Contains(set.Body.String(), `"revision":"3"`) || !strings.Contains(set.Body.String(), `"secret_configured":true`) || strings.Contains(set.Body.String(), "node-secret-edited") {
		t.Fatalf("credential set status=%d body=%s", set.Code, set.Body.String())
	}
	clearBody := fmt.Sprintf(`{"command_id":"%s","expected_revision":"3","management_credential":null}`, uuid.New())
	cleared := do(http.MethodPatch, "/api/assets/nodes/"+ids[2].String(), clearBody, csrf)
	if cleared.Code != http.StatusOK || !strings.Contains(cleared.Body.String(), `"revision":"4"`) || !strings.Contains(cleared.Body.String(), `"secret_configured":false`) {
		t.Fatalf("credential clear status=%d body=%s", cleared.Code, cleared.Body.String())
	}
	boundary4096 := strings.Repeat("a", 4093) + "€"
	set4096 := fmt.Sprintf(`{"command_id":"%s","expected_revision":"4","management_credential":%q}`, uuid.New(), boundary4096)
	if response := do(http.MethodPatch, "/api/assets/nodes/"+ids[2].String(), set4096, csrf); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"revision":"5"`) {
		t.Fatalf("4096-byte credential status=%d body=%s", response.Code, response.Body.String())
	}
	over4096 := strings.Repeat("a", 4094) + "€"
	set4097 := fmt.Sprintf(`{"command_id":"%s","expected_revision":"5","management_credential":%q}`, uuid.New(), over4096)
	if response := do(http.MethodPatch, "/api/assets/nodes/"+ids[2].String(), set4097, csrf); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"secret_configuration_invalid"`) {
		t.Fatalf("4097-byte credential status=%d body=%s", response.Code, response.Body.String())
	}
	staleCursor := do(http.MethodGet, "/api/assets/nodes?lifecycle=active&monitoring_active=false&limit=1&cursor="+url.QueryEscape(*page.NextCursor), "", "")
	if staleCursor.Code != http.StatusConflict || !strings.Contains(staleCursor.Body.String(), `"code":"cursor_stale"`) {
		t.Fatalf("stale cursor status=%d body=%s", staleCursor.Code, staleCursor.Body.String())
	}

	replacementID := uuid.MustParse("00000000-0000-4000-8000-000000000104")
	nullReplace := fmt.Sprintf(`{"command_id":"%s","expected_revision":"1","new_instance_id":"%s","display_name":"Invalid replacement","management_endpoint":"http://invalid-replacement.example","node_type":"cliproxyapi","driver_contract_version":"v1","capabilities":["management_account_inventory_read"],"management_credential":null}`, uuid.New(), uuid.New())
	if response := do(http.MethodPost, "/api/assets/nodes/"+ids[0].String()+"/replace", nullReplace, csrf); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"secret_configuration_invalid"`) {
		t.Fatalf("null replace credential status=%d body=%s", response.Code, response.Body.String())
	}
	replaceBody := fmt.Sprintf(`{"command_id":"%s","expected_revision":"1","new_instance_id":"%s","display_name":"Node replacement","management_endpoint":"http://replacement.example","node_type":"cliproxyapi","driver_contract_version":"v1","capabilities":["management_account_inventory_read"],"management_credential":"node-secret-replacement"}`, uuid.New(), replacementID)
	replaced := do(http.MethodPost, "/api/assets/nodes/"+ids[0].String()+"/replace", replaceBody, csrf)
	if replaced.Code != http.StatusOK || !strings.Contains(replaced.Body.String(), replacementID.String()) || !strings.Contains(replaced.Body.String(), `"secret_configured":true`) || strings.Contains(replaced.Body.String(), "node-secret-replacement") {
		t.Fatalf("replace status=%d body=%s", replaced.Code, replaced.Body.String())
	}
	detail := do(http.MethodGet, "/api/assets/nodes/"+ids[0].String(), "", "")
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), `"lifecycle_status":"retired"`) || !strings.Contains(detail.Body.String(), replacementID.String()) {
		t.Fatalf("historical detail status=%d body=%s", detail.Code, detail.Body.String())
	}
	retireBody := fmt.Sprintf(`{"command_id":"%s","expected_revision":"1"}`, uuid.New())
	retired := do(http.MethodPost, "/api/assets/nodes/"+replacementID.String()+"/retire", retireBody, csrf)
	if retired.Code != http.StatusOK || !strings.Contains(retired.Body.String(), `"lifecycle_status":"retired"`) {
		t.Fatalf("retire status=%d body=%s", retired.Code, retired.Body.String())
	}
	if strings.Contains(strings.ToLower(retired.Body.String()), "vault://") {
		t.Fatalf("retire response exposed secret: %s", retired.Body.String())
	}
}
