package store_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
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
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "38"); err != nil {
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
	op, err := r.Accept(ctx, store.AccountOperationAcceptance{CommandID: firstID, ActorAdminID: admin, OperationKind: store.AccountDisable, NodeInstanceID: node, AccountKey: "antigravity:test@example.invalid", CanonicalIntentHash: hash[:]})
	if err != nil || op.ExecutionState != store.AccountPrepared {
		t.Fatalf("accept = %#v, %v", op, err)
	}
	if err := r.TerminalizePreDispatchFailure(ctx, firstID, mustFailure(t, "account_target_not_found"), []byte(`{"error":{"code":"account_target_not_found"}}`), "schema-proof"); err != nil {
		t.Fatal(err)
	}
	receipt, err := r.Receipt(ctx, firstID)
	if err != nil || receipt.HTTPStatus != 409 {
		t.Fatalf("receipt = %#v, %v", receipt, err)
	}
	var savedBody map[string]any
	if err := json.Unmarshal(receipt.ResponseBody, &savedBody); err != nil {
		t.Fatal(err)
	}
	if savedBody["error"] == nil {
		t.Fatal("receipt did not preserve response body")
	}
	if _, err := r.TransitionAccountOperation(ctx, firstID, store.AccountPrepared, store.AccountDispatched, nil); !errors.Is(err, store.ErrAccountOperationState) {
		t.Fatalf("terminal transition = %v", err)
	}

	secondID := uuid.New()
	secondHash := sha256.Sum256([]byte("second"))
	if _, err := r.Accept(ctx, store.AccountOperationAcceptance{CommandID: secondID, ActorAdminID: admin, OperationKind: store.AccountEnable, NodeInstanceID: node, AccountKey: "antigravity:other@example.invalid", CanonicalIntentHash: secondHash[:]}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.TransitionAccountOperation(ctx, secondID, store.AccountPrepared, store.AccountDispatched, nil); err != nil {
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
}

func mustFailure(t *testing.T, code string) store.AccountFailure {
	t.Helper()
	failure, err := store.NewAccountFailure(code, store.AccountPreDispatchPostAccept)
	if err != nil {
		t.Fatal(err)
	}
	return failure
}
