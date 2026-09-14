package store_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	store "github.com/sunxu/relay-station-control/internal/store"
)

func TestAccountOperationsPersistencePG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	databaseURL, owner, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
	defer cleanup()
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "37"); err != nil {
		t.Fatal(err)
	}
	admin, node := uuid.New(), uuid.New()
	if _, err := owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,$2,'Account Admin','enabled',clock_timestamp())`, admin, "account-"+admin.String()[:12]); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO node_drivers(node_type,driver_contract_version,display_name) VALUES('cliproxyapi','v1','CLIProxyAPI') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO relay_node_assets(instance_id,display_name,node_type,driver_contract_version,management_endpoint) VALUES($1,'Account Node','cliproxyapi','v1','http://node.example/')`, node); err != nil {
		t.Fatal(err)
	}
	owner.Close(ctx)
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "41"); err != nil {
		t.Fatal(err)
	}

	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	poolConfig.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE relay_control_runtime`)
		return err
	}
	p, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	r, err := store.NewAccountOperationRepository(p)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte("account-operation-intent"))
	firstID := uuid.New()
	firstCommand := store.AccountOperationAcceptance{CommandID: firstID, ActorAdminID: admin, OperationKind: store.AccountDisable, NodeInstanceID: node, AccountKey: "antigravity:test@example.invalid", CanonicalIntentHash: hash[:]}
	op, err := r.Accept(ctx, firstCommand)
	if err != nil || op.ExecutionState != store.AccountPrepared {
		t.Fatalf("accept = %#v, %v", op, err)
	}
	for _, statement := range []string{
		`INSERT INTO account_admin_operations(command_id,node_instance_id,account_key,operation_kind) VALUES(gen_random_uuid(),$1,'antigravity:direct@example.invalid','disable')`,
		`UPDATE account_admin_operations SET account_key='changed' WHERE command_id=$1`,
		`UPDATE account_admin_operations SET upload_fingerprint_key_version=2 WHERE command_id=$1`,
		`UPDATE account_admin_operations SET upload_intent_fingerprint=decode(repeat('00',32),'hex') WHERE command_id=$1`,
		`DELETE FROM account_admin_operations WHERE command_id=$1`,
	} {
		if _, err := p.Exec(ctx, statement, firstID); err == nil {
			t.Fatalf("runtime operation DML unexpectedly succeeded: %s", statement)
		}
	}
	if _, err := p.Exec(ctx, `TRUNCATE account_admin_operations`); err == nil {
		t.Fatal("runtime operation TRUNCATE unexpectedly succeeded")
	}
	if _, err := p.Exec(ctx, `INSERT INTO account_admin_command_receipts(command_id,actor_admin_id,command_kind,intent_encoding_version,canonical_intent_hash,http_status,response_body) VALUES($1,$2,'account.disable',1,decode(repeat('00',32),'hex'),200,'{}')`, uuid.New(), admin); err == nil {
		t.Fatal("runtime receipt INSERT unexpectedly succeeded")
	}
	if err := r.TerminalizePreDispatchFailure(ctx, firstID, mustFailure(t, "account_target_not_found"), "schema-proof"); err != nil {
		t.Fatal(err)
	}
	receipt, err := r.Receipt(ctx, firstID)
	if err != nil || receipt.HTTPStatus != 409 {
		t.Fatalf("receipt = %#v, %v", receipt, err)
	}
	replay, err := r.Receipt(ctx, firstID)
	if err != nil || replay.HTTPStatus != receipt.HTTPStatus || !bytes.Equal(replay.ResponseBody, receipt.ResponseBody) {
		t.Fatalf("receipt replay = %#v, %v", replay, err)
	}
	for _, statement := range []string{
		`UPDATE account_admin_command_receipts SET http_status=201 WHERE command_id=$1`,
		`DELETE FROM account_admin_command_receipts WHERE command_id=$1`,
	} {
		if _, err := p.Exec(ctx, statement, firstID); err == nil {
			t.Fatalf("runtime receipt DML unexpectedly succeeded: %s", statement)
		}
	}
	if _, err := p.Exec(ctx, `TRUNCATE account_admin_command_receipts`); err == nil {
		t.Fatal("runtime receipt TRUNCATE unexpectedly succeeded")
	}
	commandReplay, err := r.ReplayTerminal(ctx, firstCommand)
	if err != nil || commandReplay.HTTPStatus != receipt.HTTPStatus || !bytes.Equal(commandReplay.ResponseBody, receipt.ResponseBody) {
		t.Fatalf("command replay = %#v, %v", commandReplay, err)
	}
	var savedBody map[string]any
	if err := json.Unmarshal(receipt.ResponseBody, &savedBody); err != nil {
		t.Fatal(err)
	}
	if savedBody["error"] == nil {
		t.Fatal("receipt did not preserve response body")
	}
	operationBody, ok := savedBody["operation"].(map[string]any)
	if !ok || operationBody["execution_state"] != "failed" || operationBody["error_code"] != "account_target_not_found" {
		t.Fatalf("derived terminal operation body = %#v", savedBody["operation"])
	}
	var forgedID = uuid.New()
	forgedHash := sha256.Sum256([]byte("forged"))
	if _, err := r.Accept(ctx, store.AccountOperationAcceptance{CommandID: forgedID, ActorAdminID: admin, OperationKind: store.AccountDisable, NodeInstanceID: node, AccountKey: "antigravity:forged@example.invalid", CanonicalIntentHash: forgedHash[:]}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `SELECT control_terminalize_account_operation_failure_v1($1,'made_up_error','forged')`, forgedID); err == nil {
		t.Fatal("forged failure code unexpectedly accepted")
	}
	if _, err := p.Exec(ctx, `SELECT control_terminalize_account_operation_failure_v1($1,'node_retired','forged')`, forgedID); err != nil {
		t.Fatalf("valid derived failure rejected: %v", err)
	}
	if _, err := r.TransitionAccountOperation(ctx, firstID, store.AccountPrepared, store.AccountDispatched); !errors.Is(err, store.ErrAccountOperationState) {
		t.Fatalf("terminal transition = %v", err)
	}

	secondID := uuid.New()
	secondHash := sha256.Sum256([]byte("second"))
	if _, err := r.Accept(ctx, store.AccountOperationAcceptance{CommandID: secondID, ActorAdminID: admin, OperationKind: store.AccountEnable, NodeInstanceID: node, AccountKey: "antigravity:other@example.invalid", CanonicalIntentHash: secondHash[:]}); err != nil {
		t.Fatal(err)
	}
	admitted, _, err := r.AdmitAccountDispatch(ctx, secondID, node, "antigravity:other@example.invalid", "schema-proof")
	if err != nil || !admitted {
		t.Fatal(err)
	}
	thirdID := uuid.New()
	thirdHash := sha256.Sum256([]byte("third"))
	third, err := r.Accept(ctx, store.AccountOperationAcceptance{CommandID: thirdID, ActorAdminID: admin, OperationKind: store.AccountRemove, NodeInstanceID: node, AccountKey: "antigravity:other@example.invalid", CanonicalIntentHash: thirdHash[:]})
	if err != nil || third.ExecutionState != store.AccountPrepared {
		t.Fatalf("third accept = %#v, %v", third, err)
	}
	blocked, err := r.SameAccountBlocked(ctx, node, third.AccountKey, thirdID)
	if err != nil || !blocked {
		t.Fatalf("same-account blocker = %v, %v", blocked, err)
	}

	// Two prepared operations for one account race the single SQL admission
	// function. The advisory key and partial unique index are both exercised.
	runAdmissionRace(t, ctx, p, r, admin, node, "antigravity:race@example.invalid")
	runAdmissionRace(t, ctx, p, r, admin, node, "antigravity:race-two@example.invalid")
	runIndependentAdmissionRace(t, ctx, r, admin, node)

	keyPath := filepath.Join(t.TempDir(), "missing.key")
	keyCommand := store.AccountOperationAcceptance{CommandID: uuid.New(), ActorAdminID: admin, OperationKind: store.AccountUploadNew, NodeInstanceID: node, AccountKey: "antigravity:key@example.invalid", CanonicalIntentHash: hash[:]}
	if _, err := r.AcceptWithIntentKey(ctx, keyPath, keyCommand, []byte("synthetic-intent")); !errors.Is(err, store.ErrAccountIntentKeyUnavailable) {
		t.Fatalf("missing intent key error = %v", err)
	}
	invalidPaths := []string{filepath.Join(t.TempDir(), "short.key"), filepath.Join(t.TempDir(), "mode.key"), filepath.Join(t.TempDir(), "link.key")}
	if err := os.WriteFile(invalidPaths[0], []byte("short"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(invalidPaths[1], []byte("01234567890123456789012345678901"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(invalidPaths[1], invalidPaths[2]); err != nil {
		t.Fatal(err)
	}
	invalidCommands := make([]uuid.UUID, 0, len(invalidPaths))
	for _, invalidPath := range invalidPaths {
		command := keyCommand
		command.CommandID = uuid.New()
		invalidCommands = append(invalidCommands, command.CommandID)
		if _, err := r.AcceptWithIntentKey(ctx, invalidPath, command, []byte("synthetic-intent")); !errors.Is(err, store.ErrAccountIntentKeyUnavailable) {
			t.Fatalf("invalid intent key %s error = %v", filepath.Base(invalidPath), err)
		}
	}
	invalidCommands = append(invalidCommands, keyCommand.CommandID)
	for _, commandID := range invalidCommands {
		var registryCount, operationCount, receiptCount int
		if err := p.QueryRow(ctx, `SELECT (SELECT count(*) FROM admin_command_registry WHERE command_id=$1), (SELECT count(*) FROM account_admin_operations WHERE command_id=$1), (SELECT count(*) FROM account_admin_command_receipts WHERE command_id=$1)`, commandID).Scan(&registryCount, &operationCount, &receiptCount); err != nil {
			t.Fatal(err)
		}
		if registryCount != 0 || operationCount != 0 || receiptCount != 0 {
			t.Fatalf("failed key %s persisted state=%d/%d/%d", commandID, registryCount, operationCount, receiptCount)
		}
	}
}

func runAdmissionRace(t *testing.T, ctx context.Context, p *pgxpool.Pool, r *store.AccountOperationRepository, admin, node uuid.UUID, accountKey string) {
	t.Helper()
	ids := []uuid.UUID{uuid.New(), uuid.New()}
	for _, id := range ids {
		h := sha256.Sum256([]byte(id.String()))
		if _, err := r.Accept(ctx, store.AccountOperationAcceptance{CommandID: id, ActorAdminID: admin, OperationKind: store.AccountDisable, NodeInstanceID: node, AccountKey: accountKey, CanonicalIntentHash: h[:]}); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	type result struct {
		admitted bool
		err      error
	}
	results := make(chan result, 2)
	for _, id := range ids {
		wg.Add(1)
		go func(id uuid.UUID) {
			defer wg.Done()
			<-start
			admitted, _, err := r.AdmitAccountDispatch(ctx, id, node, accountKey, "race")
			results <- result{admitted, err}
		}(id)
	}
	close(start)
	wg.Wait()
	close(results)
	winner, loser := 0, 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("admission race error = %v", result.err)
		}
		if result.admitted {
			winner++
		} else {
			loser++
		}
	}
	if winner != 1 || loser != 1 {
		t.Fatalf("admission race winner/loser=%d/%d", winner, loser)
	}
	var failed, receipts, audits int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM account_admin_operations WHERE account_key=$1 AND execution_state='failed'`, accountKey).Scan(&failed); err != nil {
		t.Fatal(err)
	}
	if err := p.QueryRow(ctx, `SELECT count(*) FROM account_admin_command_receipts r JOIN account_admin_operations o ON o.command_id=r.target_operation_command_id WHERE o.account_key=$1`, accountKey).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err := p.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='account.operation_failed' AND details->>'error_code'='account_operation_in_progress' AND details->>'command_id' IN (SELECT command_id::text FROM account_admin_operations WHERE account_key=$1)`, accountKey).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if failed != 1 || receipts != 1 || audits != 1 {
		t.Fatalf("race loser evidence failed/receipts/audits=%d/%d/%d", failed, receipts, audits)
	}
	var loserCode string
	var loserStatus int
	var loserBody []byte
	if err := p.QueryRow(ctx, `SELECT o.remote_result_code,r.http_status,r.response_body FROM account_admin_operations o JOIN account_admin_command_receipts r ON r.target_operation_command_id=o.command_id WHERE o.account_key=$1 AND o.execution_state='failed'`, accountKey).Scan(&loserCode, &loserStatus, &loserBody); err != nil {
		t.Fatal(err)
	}
	if loserCode != "account_operation_in_progress" || loserStatus != 409 || !bytes.Contains(loserBody, []byte(`"account_operation_in_progress"`)) {
		t.Fatalf("derived blocker receipt = code=%s status=%d body=%s", loserCode, loserStatus, loserBody)
	}
}

func runIndependentAdmissionRace(t *testing.T, ctx context.Context, r *store.AccountOperationRepository, admin, node uuid.UUID) {
	t.Helper()
	ids := []uuid.UUID{uuid.New(), uuid.New()}
	keys := []string{"antigravity:independent-a@example.invalid", "antigravity:independent-b@example.invalid"}
	for i, id := range ids {
		h := sha256.Sum256([]byte(id.String()))
		if _, err := r.Accept(ctx, store.AccountOperationAcceptance{CommandID: id, ActorAdminID: admin, OperationKind: store.AccountDisable, NodeInstanceID: node, AccountKey: keys[i], CanonicalIntentHash: h[:]}); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id uuid.UUID) {
			defer wg.Done()
			<-start
			admitted, _, err := r.AdmitAccountDispatch(ctx, id, node, keys[i], "independent")
			if err == nil && !admitted {
				err = errors.New("independent account was blocked")
			}
			results <- err
		}(i, id)
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func mustFailure(t *testing.T, code string) store.AccountFailure {
	t.Helper()
	failure, err := store.NewAccountFailure(code, store.AccountPreDispatchPostAccept)
	if err != nil {
		t.Fatal(err)
	}
	return failure
}
