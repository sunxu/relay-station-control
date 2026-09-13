package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	authn "github.com/sunxu/relay-station-control/internal/auth"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

func TestRelayBindingHTTPCompleteSuite(t *testing.T) {
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

	secretCanary := "vault://binding-secret-canary-" + uuid.NewString()
	gatewaySecretCanary := "secret-token-" + uuid.NewString()

	// 1. Environment & Node Drivers
	if _, err = owner.Exec(ctx, `INSERT INTO environments(environment_id,name,environment_type) VALUES('binding-http-test','Binding HTTP Test','dev')`); err != nil {
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

	// 2. Gateway & Nodes
	gatewayID := uuid.New()
	node1ID, node2ID := uuid.New(), uuid.New()
	if _, err = owner.Exec(ctx, `INSERT INTO gateway_instances(instance_id,display_name,management_endpoint,reader_secret_ref)
		VALUES($1,'Gateway Alpha','http://gw.example.com',$2)`, gatewayID, secretCanary); err != nil {
		t.Fatal(err)
	}
	for id, name := range map[uuid.UUID]string{node1ID: "Node Alpha", node2ID: "Node Beta"} {
		if _, err = owner.Exec(ctx, `INSERT INTO relay_node_assets(instance_id,display_name,node_type,driver_contract_version,management_endpoint,reader_secret_ref)
			VALUES($1,$2,'cliproxy.api','v1','http://node.example:8080/mgmt',$3)`, id, name, secretCanary); err != nil {
			t.Fatal(err)
		}
		if _, err = owner.Exec(ctx, `INSERT INTO node_capabilities(instance_id,node_type,driver_contract_version,capability) VALUES
			($1,'cliproxy.api','v1','management_health_read'),
			($1,'cliproxy.api','v1','management_account_inventory_read')`, id); err != nil {
			t.Fatal(err)
		}
	}

	// 3. Directory snapshots for Gateway
	var dbNow, slotNow time.Time
	if err = owner.QueryRow(ctx, `SELECT clock_timestamp(), to_timestamp(
		floor(extract(epoch FROM clock_timestamp())/180)*180
	)`).Scan(&dbNow, &slotNow); err != nil {
		t.Fatal(err)
	}
	snap1ID := uuid.New()
	fingerprint := make([]byte, 32)
	for i := range fingerprint {
		fingerprint[i] = byte(i + 1)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO gateway_directory_snapshots(snapshot_id,gateway_instance_id,fingerprint,fingerprint_encoding_version,schema_version,account_count,created_at)
		VALUES($1,$2,$3,1,1,6,$4)`, snap1ID, gatewayID, fingerprint, dbNow); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO gateway_directory_snapshot_items(snapshot_id,account_id,name,platform,type,url,status)
		VALUES($1,1001,'Account 1','openai','apikey','https://gw.example.com/acc/1','active'),
		      ($1,1002,'Account 2','anthropic','apikey',NULL,'active'),
 ($1,9007199254740991,'Precision 1','openai','apikey',NULL,'active'),
 ($1,9007199254740992,'Precision 2','openai','apikey',NULL,'active'),
 ($1,9007199254740993,'Precision 3','openai','apikey',NULL,'active'),
 ($1,9223372036854775807,'Precision 4','openai','apikey',NULL,'active')`, snap1ID); err != nil {
		t.Fatal(err)
	}
	runID := uuid.New()
	if _, err = owner.Exec(ctx, `INSERT INTO gateway_directory_ingestion_runs(
		ingestion_run_id,gateway_instance_id,scheduled_at,status,attempt_count,created_at,first_started_at,last_started_at,
		outcome,source_generated_at,terminal_at,received_at,content_fingerprint,snapshot_id,account_count
	) VALUES ($1,$2,$3,'succeeded',1,$3,$3,$3,'changed',$4,$4,$4,$5,$6,6)`,
		runID, gatewayID, slotNow, dbNow, fingerprint, snap1ID); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO gateway_directory_current_state(
		gateway_instance_id,current_snapshot_id,current_content_fingerprint,last_success_received_at,last_source_generated_at,last_success_run_id,updated_at
	) VALUES($1,$2,$3,$4,$4,$5,$4)`, gatewayID, snap1ID, fingerprint, dbNow, runID); err != nil {
		t.Fatal(err)
	}

	// 4. Admins & Sessions: super_admin (enabled) vs disabled admin vs unauthenticated
	config := testValidatedConfig(t, authn.EnvironmentDev)
	service, err := authn.NewService(runtime, config)
	if err != nil {
		t.Fatal(err)
	}

	superAdminID, disabledAdminID := uuid.New(), uuid.New()
	superToken, superCSRF := "super-session-"+uuid.NewString(), "super-csrf-"+uuid.NewString()
	disabledToken, disabledCSRF := "disabled-session-"+uuid.NewString(), "disabled-csrf-"+uuid.NewString()

	createSession := func(adminID uuid.UUID, login, status, token, csrf string) {
		tokenDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainSessionDigest, token)
		if err != nil {
			t.Fatal(err)
		}
		csrfDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainCSRFDigest, csrf)
		if err != nil {
			t.Fatal(err)
		}
		disabledAt := "NULL"
		if status == "disabled" {
			disabledAt = "CURRENT_TIMESTAMP"
		}
		if _, err = owner.Exec(ctx, fmt.Sprintf(`INSERT INTO control_admin_users(admin_id,login_name,display_name,role,status,activated_at,disabled_at)
			VALUES($1,$2,'Admin User','super_admin',$3,CURRENT_TIMESTAMP,%s)`, disabledAt), adminID, login, status); err != nil {
			t.Fatal(err)
		}
		if _, err = owner.Exec(ctx, `INSERT INTO control_admin_sessions
			(session_id,admin_id,token_digest,csrf_digest,key_version,mfa_method,absolute_expires_at)
			VALUES($1,$2,$3,$4,$5,'none',CURRENT_TIMESTAMP+interval '12 hours')`,
			uuid.New(), adminID, tokenDigest.Sum[:], csrfDigest.Sum[:], int32(tokenDigest.KeyVersion)); err != nil {
			t.Fatal(err)
		}
	}

	createSession(superAdminID, "super_admin_user", "enabled", superToken, superCSRF)
	createSession(disabledAdminID, "disabled_admin_user", "disabled", disabledToken, disabledCSRF)
	expiredToken := "expired-session-" + uuid.NewString()
	expiredCSRF := "expired-csrf-" + uuid.NewString()
	expiredTokenDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainSessionDigest, expiredToken)
	if err != nil {
		t.Fatal(err)
	}
	expiredCSRFDigest, err := authn.ComputeDigest(config.Keyring, authn.DomainCSRFDigest, expiredCSRF)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `INSERT INTO control_admin_sessions(
		session_id,admin_id,token_digest,csrf_digest,key_version,mfa_method,
		created_at,last_activity_at,absolute_expires_at
	) VALUES($1,$2,$3,$4,$5,'none',CURRENT_TIMESTAMP-interval '2 hours',
		CURRENT_TIMESTAMP-interval '2 hours',CURRENT_TIMESTAMP-interval '1 hour')`,
		uuid.New(), superAdminID, expiredTokenDigest.Sum[:], expiredCSRFDigest.Sum[:], int32(expiredTokenDigest.KeyVersion)); err != nil {
		t.Fatal(err)
	}

	// 5. Build server & handler
	assetRepo, err := assetstore.NewAssetRepository(runtime)
	if err != nil {
		t.Fatal(err)
	}
	relayBindingRepo, err := assetstore.NewRelayBindingRepository(runtime)
	if err != nil {
		t.Fatal(err)
	}
	server := NewAuthenticatedServer("test", service)
	server.assets = assetRepo
	server.SetRelayBindingRepository(relayBindingRepo)

	handler := HandlerWithOptions(server, ChiServerOptions{ErrorHandlerFunc: server.PrepareGeneratedError})

	do := func(method, path, cookie, csrfProof string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var bodyReader *bytes.Reader
		if body != nil {
			switch b := body.(type) {
			case string:
				bodyReader = bytes.NewReader([]byte(b))
			default:
				raw, err := json.Marshal(b)
				if err != nil {
					t.Fatal(err)
				}
				bodyReader = bytes.NewReader(raw)
			}
		} else {
			bodyReader = bytes.NewReader(nil)
		}
		request := httptest.NewRequest(method, path, bodyReader)
		if body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		if csrfProof != "" {
			request.Header.Set("X-CSRF-Token", csrfProof)
		}
		if cookie != "" {
			request.AddCookie(&http.Cookie{Name: authn.DevSessionCookieName, Value: cookie})
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	t.Run("Security & Authentication on Read Endpoints", func(t *testing.T) {
		readPaths := []string{
			"/api/relay-bindings/nodes/" + node1ID.String(),
			"/api/relay-bindings/gateways/" + gatewayID.String(),
			"/api/relay-bindings/unresolved",
		}
		for _, path := range readPaths {
			// Unauthenticated -> 401
			unauth := do(http.MethodGet, path, "", "", nil)
			if unauth.Code != http.StatusUnauthorized {
				t.Fatalf("%s unauthenticated code=%d want 401", path, unauth.Code)
			}
			if unauth.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("%s missing no-store header", path)
			}

			// Disabled session -> 401
			dis := do(http.MethodGet, path, disabledToken, "", nil)
			if dis.Code != http.StatusUnauthorized {
				t.Fatalf("%s disabled session code=%d want 401", path, dis.Code)
			}

			// Authenticated read -> 200
			authResp := do(http.MethodGet, path, superToken, "", nil)
			if authResp.Code != http.StatusOK {
				t.Fatalf("%s authenticated code=%d want 200, body=%s", path, authResp.Code, authResp.Body.String())
			}
			if authResp.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("%s missing no-store header", path)
			}
			// Canary check: response must never contain secret canaries
			if strings.Contains(authResp.Body.String(), secretCanary) || strings.Contains(authResp.Body.String(), gatewaySecretCanary) {
				t.Fatalf("%s leaked secret canary in response: %s", path, authResp.Body.String())
			}
		}
	})

	t.Run("Security & Authorization on Mutation Endpoints", func(t *testing.T) {
		bindBody := BindRelayNodeRequest{
			RelayNodeId:       node1ID,
			GatewayInstanceId: gatewayID,
			GatewayAccountId:  "1001",
		}

		// 1. Unauthenticated -> 401
		r1 := do(http.MethodPost, "/api/relay-bindings/bind", "", superCSRF, bindBody)
		if r1.Code != http.StatusUnauthorized {
			t.Fatalf("bind unauthenticated code=%d want 401", r1.Code)
		}

		// 2. Missing/invalid CSRF -> 403
		r2 := do(http.MethodPost, "/api/relay-bindings/bind", superToken, "", bindBody)
		if r2.Code != http.StatusForbidden {
			t.Fatalf("bind missing csrf code=%d want 403", r2.Code)
		}
		r2b := do(http.MethodPost, "/api/relay-bindings/bind", superToken, "wrong-csrf-proof-token-too-short", bindBody)
		if r2b.Code != http.StatusBadRequest && r2b.Code != http.StatusForbidden {
			t.Fatalf("bind invalid csrf code=%d want 400 or 403", r2b.Code)
		}

		// 3. Invalid session token -> 401
		r3 := do(http.MethodPost, "/api/relay-bindings/bind", "invalid-token-cookie", superCSRF, bindBody)
		if r3.Code != http.StatusUnauthorized {
			t.Fatalf("bind invalid token code=%d want 401, body=%s", r3.Code, r3.Body.String())
		}

		// 4. Expired session -> 401
		rExpired := do(http.MethodPost, "/api/relay-bindings/bind", expiredToken, expiredCSRF, bindBody)
		if rExpired.Code != http.StatusUnauthorized {
			t.Fatalf("bind expired session code=%d want 401, body=%s", rExpired.Code, rExpired.Body.String())
		}

		// 5. Disabled admin -> 401
		r4 := do(http.MethodPost, "/api/relay-bindings/bind", disabledToken, disabledCSRF, bindBody)
		if r4.Code != http.StatusUnauthorized {
			t.Fatalf("bind disabled admin code=%d want 401", r4.Code)
		}

		// 6. Unknown fields in body rejected with 400
		rawWithExtra := fmt.Sprintf(`{"relay_node_id":"%s","gateway_instance_id":"%s","gateway_account_id":"1001","extra_field":"malicious"}`, node1ID, gatewayID)
		r5 := do(http.MethodPost, "/api/relay-bindings/bind", superToken, superCSRF, rawWithExtra)
		if r5.Code != http.StatusBadRequest {
			t.Fatalf("bind extra field code=%d want 400, body=%s", r5.Code, r5.Body.String())
		}
	})

	t.Run("Binding Lifecycle: Bind -> Node Read -> Rebind -> Account Read -> Unbind -> Already Unbound", func(t *testing.T) {
		// 1. Initial Node Read -> Unbound
		nodeResp := do(http.MethodGet, "/api/relay-bindings/nodes/"+node1ID.String(), superToken, "", nil)
		if nodeResp.Code != http.StatusOK {
			t.Fatalf("node read code=%d body=%s", nodeResp.Code, nodeResp.Body.String())
		}
		var nodeView NodeRelayBindingResponse
		if err := json.Unmarshal(nodeResp.Body.Bytes(), &nodeView); err != nil {
			t.Fatal(err)
		}
		if nodeView.Resolution != RelayBindingResolutionUnbound || nodeView.CurrentBinding != nil {
			t.Fatalf("expected unbound, got resolution=%s binding=%v", nodeView.Resolution, nodeView.CurrentBinding)
		}

		// 2. Valid Bind -> 200 OK with outcome=success
		bindBody := BindRelayNodeRequest{
			RelayNodeId:       node1ID,
			GatewayInstanceId: gatewayID,
			GatewayAccountId:  "1001",
		}
		bindResp := do(http.MethodPost, "/api/relay-bindings/bind", superToken, superCSRF, bindBody)
		if bindResp.Code != http.StatusOK {
			t.Fatalf("bind code=%d body=%s", bindResp.Code, bindResp.Body.String())
		}
		var mutResp RelayBindingMutationResponse
		if err := json.Unmarshal(bindResp.Body.Bytes(), &mutResp); err != nil {
			t.Fatal(err)
		}
		if mutResp.Outcome != RelayBindingMutationResponseOutcomeSuccess || mutResp.Binding == nil {
			t.Fatalf("unexpected mutation response: %+v", mutResp)
		}
		if mutResp.Binding.GatewayAccountId != "1001" || mutResp.Binding.RelayNodeId != node1ID {
			t.Fatalf("unexpected binding details: %+v", mutResp.Binding)
		}
		if bindResp.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("mutation response missing no-store")
		}

		// 3. Node Read -> Resolved with current context
		nodeResp2 := do(http.MethodGet, "/api/relay-bindings/nodes/"+node1ID.String(), superToken, "", nil)
		if nodeResp2.Code != http.StatusOK {
			t.Fatalf("node read 2 code=%d body=%s", nodeResp2.Code, nodeResp2.Body.String())
		}
		if err := json.Unmarshal(nodeResp2.Body.Bytes(), &nodeView); err != nil {
			t.Fatal(err)
		}
		if nodeView.Resolution != RelayBindingResolutionResolved || nodeView.ContextSource != "current" || nodeView.AccountContext == nil || nodeView.AccountContext.AccountId != "1001" {
			t.Fatalf("expected resolved with current context, got: %+v", nodeView)
		}

		// 4. Conflict Bind on same Node -> 409 node_conflict
		dupNodeBind := do(http.MethodPost, "/api/relay-bindings/bind", superToken, superCSRF, BindRelayNodeRequest{
			RelayNodeId:       node1ID,
			GatewayInstanceId: gatewayID,
			GatewayAccountId:  "1002",
		})
		if dupNodeBind.Code != http.StatusConflict {
			t.Fatalf("dup node bind code=%d want 409, body=%s", dupNodeBind.Code, dupNodeBind.Body.String())
		}
		var errResp ErrorResponse
		_ = json.Unmarshal(dupNodeBind.Body.Bytes(), &errResp)
		if errResp.Code != ErrorCodeNodeConflict {
			t.Fatalf("dup node bind error code=%s want node_conflict", errResp.Code)
		}

		// 5. Conflict Bind on same Account from Node 2 -> 409 account_conflict
		dupAccountBind := do(http.MethodPost, "/api/relay-bindings/bind", superToken, superCSRF, BindRelayNodeRequest{
			RelayNodeId:       node2ID,
			GatewayInstanceId: gatewayID,
			GatewayAccountId:  "1001",
		})
		if dupAccountBind.Code != http.StatusConflict {
			t.Fatalf("dup account bind code=%d want 409, body=%s", dupAccountBind.Code, dupAccountBind.Body.String())
		}
		_ = json.Unmarshal(dupAccountBind.Body.Bytes(), &errResp)
		if errResp.Code != ErrorCodeAccountConflict {
			t.Fatalf("dup account bind error code=%s want account_conflict", errResp.Code)
		}

		// 6. Valid Rebind -> rebind Node 1 from Account 1001 to Account 1002
		rebindBody := RebindRelayNodeRequest{
			RelayNodeId:          node1ID,
			NewGatewayInstanceId: gatewayID,
			NewGatewayAccountId:  "1002",
		}
		rebindResp := do(http.MethodPost, "/api/relay-bindings/rebind", superToken, superCSRF, rebindBody)
		if rebindResp.Code != http.StatusOK {
			t.Fatalf("rebind code=%d body=%s", rebindResp.Code, rebindResp.Body.String())
		}
		var rebindMutResp RelayBindingMutationResponse
		if err := json.Unmarshal(rebindResp.Body.Bytes(), &rebindMutResp); err != nil {
			t.Fatal(err)
		}
		if rebindMutResp.Outcome != RelayBindingMutationResponseOutcomeSuccess || rebindMutResp.Binding == nil || rebindMutResp.PreviousBinding == nil {
			t.Fatalf("rebind response incomplete: %+v", rebindMutResp)
		}
		if rebindMutResp.PreviousBinding.GatewayAccountId != "1001" || rebindMutResp.Binding.GatewayAccountId != "1002" {
			t.Fatalf("rebind accounts mismatch: prev=%s new=%s", rebindMutResp.PreviousBinding.GatewayAccountId, rebindMutResp.Binding.GatewayAccountId)
		}
		// Confirm exact timestamp match
		if *rebindMutResp.PreviousBinding.EndedAt != rebindMutResp.Binding.BoundAt {
			t.Fatalf("rebind interval mismatch: ended_at=%v bound_at=%v", rebindMutResp.PreviousBinding.EndedAt, rebindMutResp.Binding.BoundAt)
		}

		// 7. Gateway Account Centric Read
		gwResp := do(http.MethodGet, "/api/relay-bindings/gateways/"+gatewayID.String(), superToken, "", nil)
		if gwResp.Code != http.StatusOK {
			t.Fatalf("gateway read code=%d body=%s", gwResp.Code, gwResp.Body.String())
		}
		var gwView GatewayAccountRelayBindingsResponse
		if err := json.Unmarshal(gwResp.Body.Bytes(), &gwView); err != nil {
			t.Fatal(err)
		}
		if len(gwView.Accounts) != 6 {
			t.Fatalf("gateway view accounts count=%d want 6", len(gwView.Accounts))
		}
		// Account 1001 should now be unbound, Account 1002 should be bound to Node 1
		for _, acc := range gwView.Accounts {
			if acc.GatewayAccountId == "1001" && acc.Resolution != RelayBindingResolutionUnbound {
				t.Fatalf("account 1001 resolution=%s want unbound", acc.Resolution)
			}
			if acc.GatewayAccountId == "1002" {
				if acc.Resolution != RelayBindingResolutionResolved || acc.BoundRelayNodeId == nil || *acc.BoundRelayNodeId != node1ID {
					t.Fatalf("account 1002 resolution=%s boundNode=%v want resolved to node1", acc.Resolution, acc.BoundRelayNodeId)
				}
			}
		}

		// 8. Unbind Node 1
		unbindBody := UnbindRelayNodeRequest{RelayNodeId: node1ID}
		unbindResp := do(http.MethodPost, "/api/relay-bindings/unbind", superToken, superCSRF, unbindBody)
		if unbindResp.Code != http.StatusOK {
			t.Fatalf("unbind code=%d body=%s", unbindResp.Code, unbindResp.Body.String())
		}
		var unbindMutResp RelayBindingMutationResponse
		if err := json.Unmarshal(unbindResp.Body.Bytes(), &unbindMutResp); err != nil {
			t.Fatal(err)
		}
		if unbindMutResp.Outcome != RelayBindingMutationResponseOutcomeSuccess || unbindMutResp.PreviousBinding == nil {
			t.Fatalf("unbind mut resp invalid: %+v", unbindMutResp)
		}

		// 9. Repeat Unbind -> already_unbound (200 OK)
		repeatUnbindResp := do(http.MethodPost, "/api/relay-bindings/unbind", superToken, superCSRF, unbindBody)
		if repeatUnbindResp.Code != http.StatusOK {
			t.Fatalf("repeat unbind code=%d body=%s", repeatUnbindResp.Code, repeatUnbindResp.Body.String())
		}
		var repeatMutResp RelayBindingMutationResponse
		if err := json.Unmarshal(repeatUnbindResp.Body.Bytes(), &repeatMutResp); err != nil {
			t.Fatal(err)
		}
		if repeatMutResp.Outcome != RelayBindingMutationResponseOutcomeAlreadyUnbound {
			t.Fatalf("repeat unbind outcome=%s want already_unbound", repeatMutResp.Outcome)
		}
		if repeatMutResp.OperationAt.IsZero() {
			t.Fatal("repeat unbind operation_at is zero")
		}

		// 10. Rebind on unbound node -> 404 no_current_binding
		rebindUnbound := do(http.MethodPost, "/api/relay-bindings/rebind", superToken, superCSRF, rebindBody)
		if rebindUnbound.Code != http.StatusNotFound {
			t.Fatalf("rebind unbound node code=%d want 404, body=%s", rebindUnbound.Code, rebindUnbound.Body.String())
		}
		_ = json.Unmarshal(rebindUnbound.Body.Bytes(), &errResp)
		if errResp.Code != ErrorCodeNoCurrentBinding {
			t.Fatalf("rebind unbound error code=%s want no_current_binding", errResp.Code)
		}
	})

	t.Run("Validation & Failure Modes (Account NotFound / Stale / Node NotFound / Gateway NotFound)", func(t *testing.T) {
		var errResp ErrorResponse

		// 1. Target Account Not Found -> 404 account_not_found
		rNotFoundAcc := do(http.MethodPost, "/api/relay-bindings/bind", superToken, superCSRF, BindRelayNodeRequest{
			RelayNodeId:       node2ID,
			GatewayInstanceId: gatewayID,
			GatewayAccountId:  "999999",
		})
		if rNotFoundAcc.Code != http.StatusNotFound {
			t.Fatalf("account not found code=%d want 404, body=%s", rNotFoundAcc.Code, rNotFoundAcc.Body.String())
		}
		_ = json.Unmarshal(rNotFoundAcc.Body.Bytes(), &errResp)
		if errResp.Code != ErrorCodeAccountNotFound {
			t.Fatalf("error code=%s want account_not_found", errResp.Code)
		}

		// 2. Node Not Found -> 404 not_found
		rNotFoundNode := do(http.MethodPost, "/api/relay-bindings/bind", superToken, superCSRF, BindRelayNodeRequest{
			RelayNodeId:       uuid.New(),
			GatewayInstanceId: gatewayID,
			GatewayAccountId:  "1001",
		})
		if rNotFoundNode.Code != http.StatusNotFound {
			t.Fatalf("node not found code=%d want 404, body=%s", rNotFoundNode.Code, rNotFoundNode.Body.String())
		}
		_ = json.Unmarshal(rNotFoundNode.Body.Bytes(), &errResp)
		if errResp.Code != ErrorCodeNotFound {
			t.Fatalf("error code=%s want not_found", errResp.Code)
		}

		// 3. Gateway Not Found -> 404 not_found
		rNotFoundGw := do(http.MethodPost, "/api/relay-bindings/bind", superToken, superCSRF, BindRelayNodeRequest{
			RelayNodeId:       node2ID,
			GatewayInstanceId: uuid.New(),
			GatewayAccountId:  "1001",
		})
		if rNotFoundGw.Code != http.StatusNotFound {
			t.Fatalf("gateway not found code=%d want 404, body=%s", rNotFoundGw.Code, rNotFoundGw.Body.String())
		}
		_ = json.Unmarshal(rNotFoundGw.Body.Bytes(), &errResp)
		if errResp.Code != ErrorCodeNotFound {
			t.Fatalf("error code=%s want not_found", errResp.Code)
		}

		// 4. Stale Directory -> 409 directory_stale
		staleTime := time.Now().UTC().Add(-600 * time.Second)
		if _, err = owner.Exec(ctx, `UPDATE gateway_directory_current_state SET last_success_received_at = $1 WHERE gateway_instance_id = $2`, staleTime, gatewayID); err != nil {
			t.Fatal(err)
		}
		rStale := do(http.MethodPost, "/api/relay-bindings/bind", superToken, superCSRF, BindRelayNodeRequest{
			RelayNodeId:       node2ID,
			GatewayInstanceId: gatewayID,
			GatewayAccountId:  "1001",
		})
		if rStale.Code != http.StatusConflict {
			t.Fatalf("stale dir bind code=%d want 409, body=%s", rStale.Code, rStale.Body.String())
		}
		_ = json.Unmarshal(rStale.Body.Bytes(), &errResp)
		if errResp.Code != ErrorCodeDirectoryStale {
			t.Fatalf("error code=%s want directory_stale", errResp.Code)
		}

		// Reset Directory state to fresh
		var freshTime time.Time
		if err = owner.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&freshTime); err != nil {
			t.Fatal(err)
		}
		if _, err = owner.Exec(ctx, `UPDATE gateway_directory_current_state
			SET last_success_received_at = $1, updated_at = $1
			WHERE gateway_instance_id = $2`, freshTime, gatewayID); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("Concurrent Mutations via HTTP", func(t *testing.T) {
		// Concurrent binds targeting the same Node
		concurrency := 6
		var wg sync.WaitGroup
		successCount := 0
		var countMu sync.Mutex

		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				resp := do(http.MethodPost, "/api/relay-bindings/bind", superToken, superCSRF, BindRelayNodeRequest{
					RelayNodeId:       node2ID,
					GatewayInstanceId: gatewayID,
					GatewayAccountId:  "1001",
				})
				if resp.Code == http.StatusOK {
					countMu.Lock()
					successCount++
					countMu.Unlock()
				}
			}()
		}
		wg.Wait()

		if successCount != 1 {
			t.Fatalf("concurrent bind successCount=%d want exactly 1", successCount)
		}

		// Cleanup: unbind node 2
		_ = do(http.MethodPost, "/api/relay-bindings/unbind", superToken, superCSRF, UnbindRelayNodeRequest{RelayNodeId: node2ID})
	})

	t.Run("Identity Exact Match Only (No metadata matching)", func(t *testing.T) {
		// Snapshot items have account 1001 (name="Account 1", platform="openai", type="apikey", url="https://gw.example.com/acc/1", status="active")
		// 1. Requesting a non-existent account ID with valid integer ID must fail closed with 404 account_not_found.
		rNonExistentID := do(http.MethodPost, "/api/relay-bindings/bind", superToken, superCSRF, BindRelayNodeRequest{
			RelayNodeId:       node2ID,
			GatewayInstanceId: gatewayID,
			GatewayAccountId:  "9999",
		})
		if rNonExistentID.Code != http.StatusNotFound {
			t.Fatalf("identity non-existent id code=%d want 404, body=%s", rNonExistentID.Code, rNonExistentID.Body.String())
		}

		// 2. Requesting non-existent account ID while supplying identical metadata fields in payload.
		// Because request schema has additionalProperties: false, the schema validation rejects with 400,
		// proving that name/url/platform/type/status metadata cannot be used as fallback or alternative identity.
		rawMetadataPayload := fmt.Sprintf(`{
			"relay_node_id": "%s",
			"gateway_instance_id": "%s",
			"gateway_account_id": "9999",
			"name": "Account 1",
			"platform": "openai",
			"type": "apikey",
			"url": "https://gw.example.com/acc/1",
			"status": "active"
		}`, node2ID, gatewayID)
		rMetadataReject := do(http.MethodPost, "/api/relay-bindings/bind", superToken, superCSRF, rawMetadataPayload)
		if rMetadataReject.Code != http.StatusBadRequest {
			t.Fatalf("metadata fallback payload code=%d want 400 Bad Request, body=%s", rMetadataReject.Code, rMetadataReject.Body.String())
		}
	})

	t.Run("Binding Invariants: Read operations produce no mutations and no audits", func(t *testing.T) {
		var auditCountBefore, auditCountAfter int
		if err = owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE category = 'relay_binding'`).Scan(&auditCountBefore); err != nil {
			t.Fatal(err)
		}
		_ = do(http.MethodGet, "/api/relay-bindings/nodes/"+node1ID.String(), superToken, "", nil)
		_ = do(http.MethodGet, "/api/relay-bindings/gateways/"+gatewayID.String(), superToken, "", nil)
		_ = do(http.MethodGet, "/api/relay-bindings/unresolved", superToken, "", nil)
		if err = owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE category = 'relay_binding'`).Scan(&auditCountAfter); err != nil {
			t.Fatal(err)
		}
		if auditCountBefore != auditCountAfter {
			t.Fatalf("read endpoints triggered audit log mutation: before=%d after=%d", auditCountBefore, auditCountAfter)
		}
	})

	t.Run("Directory Snapshot Immutability across Binding Mutations (Task 4.5)", func(t *testing.T) {
		type dirCurrentState struct {
			currentSnapshotID         uuid.UUID
			currentContentFingerprint []byte
			lastSuccessReceivedAt     time.Time
			lastSourceGeneratedAt     time.Time
			lastSuccessRunID          uuid.UUID
		}
		type dirSnapshot struct {
			fingerprint  []byte
			accountCount int32
		}
		type snapshotItem struct {
			accountID int64
			name      string
			platform  string
			itemType  string
			url       string
			hasURL    bool
			status    string
		}
		type gatewayAsset struct {
			managementEndpoint string
			readerSecretRef    string
		}
		type nodeAsset struct {
			managementEndpoint string
			readerSecretRef    string
		}

		// 1. Capture exact state BEFORE binding mutations
		var stateBefore dirCurrentState
		if err = owner.QueryRow(ctx, `SELECT current_snapshot_id, current_content_fingerprint, last_success_received_at, last_source_generated_at, last_success_run_id
			FROM gateway_directory_current_state WHERE gateway_instance_id = $1`, gatewayID).Scan(
			&stateBefore.currentSnapshotID, &stateBefore.currentContentFingerprint, &stateBefore.lastSuccessReceivedAt, &stateBefore.lastSourceGeneratedAt, &stateBefore.lastSuccessRunID); err != nil {
			t.Fatal(err)
		}

		var snapBefore dirSnapshot
		if err = owner.QueryRow(ctx, `SELECT fingerprint, account_count FROM gateway_directory_snapshots WHERE snapshot_id = $1`, snap1ID).Scan(
			&snapBefore.fingerprint, &snapBefore.accountCount); err != nil {
			t.Fatal(err)
		}

		rowsBefore, err := owner.Query(ctx, `SELECT account_id, name, platform, type,
			COALESCE(url, ''), url IS NOT NULL, status
			FROM gateway_directory_snapshot_items WHERE snapshot_id = $1 ORDER BY account_id`, snap1ID)
		if err != nil {
			t.Fatal(err)
		}
		var itemsBefore []snapshotItem
		for rowsBefore.Next() {
			var it snapshotItem
			if err = rowsBefore.Scan(&it.accountID, &it.name, &it.platform, &it.itemType, &it.url, &it.hasURL, &it.status); err != nil {
				t.Fatal(err)
			}
			itemsBefore = append(itemsBefore, it)
		}
		rowsBefore.Close()

		var gwBefore gatewayAsset
		if err = owner.QueryRow(ctx, `SELECT management_endpoint, reader_secret_ref FROM gateway_instances WHERE instance_id = $1`, gatewayID).Scan(
			&gwBefore.managementEndpoint, &gwBefore.readerSecretRef); err != nil {
			t.Fatal(err)
		}

		var node1Before, node2Before nodeAsset
		if err = owner.QueryRow(ctx, `SELECT management_endpoint, reader_secret_ref FROM relay_node_assets WHERE instance_id = $1`, node1ID).Scan(
			&node1Before.managementEndpoint, &node1Before.readerSecretRef); err != nil {
			t.Fatal(err)
		}
		if err = owner.QueryRow(ctx, `SELECT management_endpoint, reader_secret_ref FROM relay_node_assets WHERE instance_id = $1`, node2ID).Scan(
			&node2Before.managementEndpoint, &node2Before.readerSecretRef); err != nil {
			t.Fatal(err)
		}

		// 2. Execute full mutation lifecycle (Bind -> Rebind -> Unbind)
		bResp := do(http.MethodPost, "/api/relay-bindings/bind", superToken, superCSRF, BindRelayNodeRequest{
			RelayNodeId:       node2ID,
			GatewayInstanceId: gatewayID,
			GatewayAccountId:  "1001",
		})
		if bResp.Code != http.StatusOK {
			t.Fatalf("bind mutation failed: %d, body=%s", bResp.Code, bResp.Body.String())
		}

		rebResp := do(http.MethodPost, "/api/relay-bindings/rebind", superToken, superCSRF, RebindRelayNodeRequest{
			RelayNodeId:          node2ID,
			NewGatewayInstanceId: gatewayID,
			NewGatewayAccountId:  "1002",
		})
		if rebResp.Code != http.StatusOK {
			t.Fatalf("rebind mutation failed: %d, body=%s", rebResp.Code, rebResp.Body.String())
		}

		uResp := do(http.MethodPost, "/api/relay-bindings/unbind", superToken, superCSRF, UnbindRelayNodeRequest{RelayNodeId: node2ID})
		if uResp.Code != http.StatusOK {
			t.Fatalf("unbind mutation failed: %d, body=%s", uResp.Code, uResp.Body.String())
		}

		// 3. Verify exact state AFTER mutations - item by item
		var stateAfter dirCurrentState
		if err = owner.QueryRow(ctx, `SELECT current_snapshot_id, current_content_fingerprint, last_success_received_at, last_source_generated_at, last_success_run_id
			FROM gateway_directory_current_state WHERE gateway_instance_id = $1`, gatewayID).Scan(
			&stateAfter.currentSnapshotID, &stateAfter.currentContentFingerprint, &stateAfter.lastSuccessReceivedAt, &stateAfter.lastSourceGeneratedAt, &stateAfter.lastSuccessRunID); err != nil {
			t.Fatal(err)
		}
		if stateBefore.currentSnapshotID != stateAfter.currentSnapshotID ||
			!bytes.Equal(stateBefore.currentContentFingerprint, stateAfter.currentContentFingerprint) ||
			!stateBefore.lastSuccessReceivedAt.Equal(stateAfter.lastSuccessReceivedAt) ||
			!stateBefore.lastSourceGeneratedAt.Equal(stateAfter.lastSourceGeneratedAt) ||
			stateBefore.lastSuccessRunID != stateAfter.lastSuccessRunID {
			t.Fatalf("gateway_directory_current_state mutated across binding operations: before=%+v after=%+v", stateBefore, stateAfter)
		}

		var snapAfter dirSnapshot
		if err = owner.QueryRow(ctx, `SELECT fingerprint, account_count FROM gateway_directory_snapshots WHERE snapshot_id = $1`, snap1ID).Scan(
			&snapAfter.fingerprint, &snapAfter.accountCount); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(snapBefore.fingerprint, snapAfter.fingerprint) || snapBefore.accountCount != snapAfter.accountCount {
			t.Fatalf("gateway_directory_snapshots mutated across binding operations: before=%+v after=%+v", snapBefore, snapAfter)
		}

		rowsAfter, err := owner.Query(ctx, `SELECT account_id, name, platform, type,
			COALESCE(url, ''), url IS NOT NULL, status
			FROM gateway_directory_snapshot_items WHERE snapshot_id = $1 ORDER BY account_id`, snap1ID)
		if err != nil {
			t.Fatal(err)
		}
		var itemsAfter []snapshotItem
		for rowsAfter.Next() {
			var it snapshotItem
			if err = rowsAfter.Scan(&it.accountID, &it.name, &it.platform, &it.itemType, &it.url, &it.hasURL, &it.status); err != nil {
				t.Fatal(err)
			}
			itemsAfter = append(itemsAfter, it)
		}
		rowsAfter.Close()
		if len(itemsBefore) != len(itemsAfter) {
			t.Fatalf("snapshot items count changed: before=%d after=%d", len(itemsBefore), len(itemsAfter))
		}
		for idx := range itemsBefore {
			if itemsBefore[idx] != itemsAfter[idx] {
				t.Fatalf("snapshot item [%d] mutated: before=%+v after=%+v", idx, itemsBefore[idx], itemsAfter[idx])
			}
		}

		var gwAfter gatewayAsset
		if err = owner.QueryRow(ctx, `SELECT management_endpoint, reader_secret_ref FROM gateway_instances WHERE instance_id = $1`, gatewayID).Scan(
			&gwAfter.managementEndpoint, &gwAfter.readerSecretRef); err != nil {
			t.Fatal(err)
		}
		if gwBefore != gwAfter {
			t.Fatalf("gateway_instances mutated: before=%+v after=%+v", gwBefore, gwAfter)
		}

		var node1After, node2After nodeAsset
		if err = owner.QueryRow(ctx, `SELECT management_endpoint, reader_secret_ref FROM relay_node_assets WHERE instance_id = $1`, node1ID).Scan(
			&node1After.managementEndpoint, &node1After.readerSecretRef); err != nil {
			t.Fatal(err)
		}
		if err = owner.QueryRow(ctx, `SELECT management_endpoint, reader_secret_ref FROM relay_node_assets WHERE instance_id = $1`, node2ID).Scan(
			&node2After.managementEndpoint, &node2After.readerSecretRef); err != nil {
			t.Fatal(err)
		}
		if node1Before != node1After || node2Before != node2After {
			t.Fatalf("relay_node_assets mutated: node1Before=%+v node1After=%+v node2Before=%+v node2After=%+v", node1Before, node1After, node2Before, node2After)
		}
	})
	t.Run("Lossless decimal int64 HTTP lifecycle", func(t *testing.T) {
		values := []string{"9007199254740991", "9007199254740992", "9007199254740993", "9223372036854775807"}
		_ = do(http.MethodPost, "/api/relay-bindings/unbind", superToken, superCSRF, UnbindRelayNodeRequest{RelayNodeId: node2ID})
		for i, id := range values {
			var response *httptest.ResponseRecorder
			if i == 0 {
				response = do(http.MethodPost, "/api/relay-bindings/bind", superToken, superCSRF, BindRelayNodeRequest{RelayNodeId: node2ID, GatewayInstanceId: gatewayID, GatewayAccountId: id})
			} else {
				response = do(http.MethodPost, "/api/relay-bindings/rebind", superToken, superCSRF, RebindRelayNodeRequest{RelayNodeId: node2ID, NewGatewayInstanceId: gatewayID, NewGatewayAccountId: id})
			}
			if response.Code != 200 {
				t.Fatalf("id=%s status=%d body=%s", id, response.Code, response.Body)
			}
			var mutation RelayBindingMutationResponse
			if err := json.Unmarshal(response.Body.Bytes(), &mutation); err != nil || mutation.Binding == nil || mutation.Binding.GatewayAccountId != id {
				t.Fatalf("lossy mutation: %s", response.Body)
			}
			if i > 0 && (mutation.PreviousBinding == nil || mutation.PreviousBinding.GatewayAccountId != values[i-1]) {
				t.Fatalf("lossy previous binding: %s", response.Body)
			}
			var persisted int64
			if err := owner.QueryRow(ctx, `SELECT gateway_account_id FROM relay_node_gateway_account_bindings WHERE relay_node_id=$1 AND ended_at IS NULL`, node2ID).Scan(&persisted); err != nil {
				t.Fatal(err)
			}
			if strconv.FormatInt(persisted, 10) != id {
				t.Fatalf("persisted identity differs for %s", id)
			}
			read := do(http.MethodGet, "/api/relay-bindings/nodes/"+node2ID.String(), superToken, "", nil)
			var view NodeRelayBindingResponse
			if err := json.Unmarshal(read.Body.Bytes(), &view); err != nil || read.Code != 200 || view.GatewayAccountId == nil || *view.GatewayAccountId != id || view.AccountContext == nil || view.AccountContext.AccountId != id || view.CurrentBinding.GatewayAccountId != id {
				t.Fatalf("lossy read: %s", read.Body)
			}
			if !strings.Contains(read.Body.String(), `"gateway_account_id":"`+id+`"`) {
				t.Fatalf("numeric wire identity: %s", read.Body)
			}
		}
		candidates := do(http.MethodGet, "/api/relay-bindings/gateways/"+gatewayID.String(), superToken, "", nil)
		for _, id := range values {
			if !strings.Contains(candidates.Body.String(), `"account_id":"`+id+`"`) {
				t.Fatalf("candidate identity missing: %s", id)
			}
		}
		var before, after int
		if err := owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE category='relay_binding'`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		for _, raw := range []string{`"9223372036854775808"`, `9007199254740993`, `null`, `""`, `"0"`, `"-1"`, `"01"`, `"+1"`, `" 1"`, `"1.0"`, `"1e3"`} {
			for _, operation := range []string{"bind", "rebind"} {
				gatewayKey, accountKey := "gateway_instance_id", "gateway_account_id"
				if operation == "rebind" {
					gatewayKey, accountKey = "new_gateway_instance_id", "new_gateway_account_id"
				}
				body := fmt.Sprintf(`{"relay_node_id":"%s","%s":"%s","%s":%s}`, node2ID, gatewayKey, gatewayID, accountKey, raw)
				response := do(http.MethodPost, "/api/relay-bindings/"+operation, superToken, superCSRF, body)
				if response.Code != 400 || !strings.Contains(response.Body.String(), `"code":"validation_failed"`) {
					t.Fatalf("invalid %s: status=%d body=%s", raw, response.Code, response.Body)
				}
			}
		}
		if err := owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE category='relay_binding'`).Scan(&after); err != nil {
			t.Fatal(err)
		}
		if before != after {
			t.Fatal("invalid IDs reached binding action/audit")
		}
		response := do(http.MethodPost, "/api/relay-bindings/unbind", superToken, superCSRF, UnbindRelayNodeRequest{RelayNodeId: node2ID})
		if response.Code != 200 || !strings.Contains(response.Body.String(), `"gateway_account_id":"9223372036854775807"`) {
			t.Fatalf("lossy unbind: %s", response.Body)
		}
	})

}
