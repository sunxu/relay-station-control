package store_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sunxu/relay-station-control/internal/accountadmin"
	store "github.com/sunxu/relay-station-control/internal/store"
)

func TestAccountOperationOverridesPG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	databaseURL, owner, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
	defer cleanup()
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "37"); err != nil {
		t.Fatal(err)
	}
	admin, node := uuid.New(), uuid.New()
	if _, err := owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,$2,'Override Admin','enabled',clock_timestamp())`, admin, "override-"+admin.String()[:12]); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO node_drivers(node_type,driver_contract_version,display_name) VALUES('cliproxyapi','v1','CLIProxyAPI') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO relay_node_assets(instance_id,display_name,node_type,driver_contract_version,management_endpoint) VALUES($1,'Override Node','cliproxyapi','v1','http://node.example/')`, node); err != nil {
		t.Fatal(err)
	}
	owner.Close(ctx)
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "47"); err != nil {
		t.Fatal(err)
	}
	p, err := runtimePool(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	repo, err := store.NewAccountOperationRepository(p)
	if err != nil {
		t.Fatal(err)
	}
	operation := acceptOverrideTarget(t, ctx, repo, admin, node, "override@example.invalid")
	if _, _, err := repo.AdmitAccountDispatch(ctx, operation.CommandID, node, operation.AccountKey, "override-dispatch"); err != nil {
		t.Fatal(err)
	}
	resolver := integrationNodeResolver{}
	service, err := accountadmin.NewService(repo, resolver)
	if err != nil {
		t.Fatal(err)
	}
	command := accountadmin.OverrideCommand{
		CommandID: uuid.New(), ActorAdminID: admin, TargetOperation: operation.CommandID,
		Reason: "risk_accepted", Confirmation: "OVERRIDE UNKNOWN OPERATION LIFECYCLE BLOCK", RequestID: "override-request",
	}
	if _, err := service.LifecycleOverride(ctx, command); err != nil {
		t.Fatal(err)
	}
	intent, err := accountadmin.CanonicalOverrideIntentV1(store.AccountLifecycleOverride, operation.CommandID, command.Reason, command.Detail)
	if err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256(intent)
	var storedHash []byte
	if err := p.QueryRow(ctx, `SELECT canonical_intent_hash FROM admin_command_registry WHERE command_id=$1`, command.CommandID).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(storedHash, wantHash[:]) {
		t.Fatalf("derived override hash=%x, want %x", storedHash, wantHash)
	}
	firstReceipt, err := repo.Receipt(ctx, command.CommandID)
	if err != nil {
		t.Fatal(err)
	}
	firstOperation, err := repo.Operation(ctx, operation.CommandID)
	if err != nil {
		t.Fatal(err)
	}
	if firstOperation.LifecycleOverrideAt == nil || firstOperation.ExecutionState != store.AccountDispatched {
		t.Fatalf("lifecycle override projection = %#v", firstOperation)
	}
	if _, err := service.LifecycleOverride(ctx, command); err != nil {
		t.Fatal(err)
	}
	secondReceipt, err := repo.Receipt(ctx, command.CommandID)
	if err != nil || !bytes.Equal(firstReceipt.ResponseBody, secondReceipt.ResponseBody) || firstReceipt.HTTPStatus != secondReceipt.HTTPStatus {
		t.Fatalf("override replay = %#v, %#v, err=%v", firstReceipt, secondReceipt, err)
	}
	var auditCount int
	if err := p.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='account.lifecycle_override' AND details->>'command_id'=$1`, command.CommandID.String()).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("override audit count=%d, want 1", auditCount)
	}
	alreadySet := accountadmin.OverrideCommand{
		CommandID: uuid.New(), ActorAdminID: admin, TargetOperation: operation.CommandID,
		Reason: "risk_accepted", Confirmation: "OVERRIDE UNKNOWN OPERATION LIFECYCLE BLOCK",
	}
	if _, err := service.LifecycleOverride(ctx, alreadySet); !errors.Is(err, store.ErrLifecycleOverrideAlreadySet) {
		t.Fatalf("already-set override error=%v", err)
	}
	alreadyReceipt, err := repo.Receipt(ctx, alreadySet.CommandID)
	if err != nil || alreadyReceipt.HTTPStatus != 409 {
		t.Fatalf("already-set receipt=%#v err=%v", alreadyReceipt, err)
	}

	// The other override uses its own command namespace identity and fields.
	secondTarget := acceptOverrideTarget(t, ctx, repo, admin, node, "same-account@example.invalid")
	if _, _, err := repo.AdmitAccountDispatch(ctx, secondTarget.CommandID, node, secondTarget.AccountKey, "same-account-dispatch"); err != nil {
		t.Fatal(err)
	}
	sameCommand := accountadmin.OverrideCommand{
		CommandID: uuid.New(), ActorAdminID: admin, TargetOperation: secondTarget.CommandID,
		Reason: "process_restarted", Confirmation: "OVERRIDE UNKNOWN OPERATION SAME-ACCOUNT BLOCK", RequestID: "same-override-request",
	}
	if _, err := service.SameAccountOverride(ctx, sameCommand); err != nil {
		t.Fatal(err)
	}
	updated, err := repo.Operation(ctx, secondTarget.CommandID)
	if err != nil || updated.SameAccountOverrideAt == nil || updated.ExecutionState != store.AccountDispatched {
		t.Fatalf("same-account override projection = %#v, err=%v", updated, err)
	}

	// Invalid and missing targets are terminal override commands with no target mutation.
	invalid := accountadmin.OverrideCommand{CommandID: uuid.New(), ActorAdminID: admin, TargetOperation: uuid.New(), Reason: "unknown", Confirmation: "OVERRIDE UNKNOWN OPERATION LIFECYCLE BLOCK"}
	if _, err := service.LifecycleOverride(ctx, invalid); !errors.Is(err, store.ErrInvalidAccountOperation) && !errors.Is(err, accountadmin.ErrInvalidCommand) {
		t.Fatalf("invalid reason error=%v", err)
	}
	missing := accountadmin.OverrideCommand{CommandID: uuid.New(), ActorAdminID: admin, TargetOperation: uuid.New(), Reason: "risk_accepted", Confirmation: "OVERRIDE UNKNOWN OPERATION LIFECYCLE BLOCK"}
	if _, err := service.LifecycleOverride(ctx, missing); !errors.Is(err, store.ErrAccountOperationNotFound) {
		t.Fatalf("missing target error=%v", err)
	}
	missingReceipt, err := repo.Receipt(ctx, missing.CommandID)
	if err != nil || missingReceipt.HTTPStatus != 404 || missingReceipt.TargetOperationCommandID != nil {
		t.Fatalf("missing target receipt=%#v err=%v", missingReceipt, err)
	}
	ineligible := acceptOverrideTarget(t, ctx, repo, admin, node, "ineligible@example.invalid")
	ineligibleCommand := accountadmin.OverrideCommand{
		CommandID: uuid.New(), ActorAdminID: admin, TargetOperation: ineligible.CommandID,
		Reason: "node_stopped", Confirmation: "OVERRIDE UNKNOWN OPERATION SAME-ACCOUNT BLOCK",
	}
	if _, err := service.SameAccountOverride(ctx, ineligibleCommand); !errors.Is(err, store.ErrAccountOperationNotOverridable) {
		t.Fatalf("ineligible override error=%v", err)
	}
	ineligibleReceipt, err := repo.Receipt(ctx, ineligibleCommand.CommandID)
	if err != nil || ineligibleReceipt.HTTPStatus != 409 {
		t.Fatalf("ineligible receipt=%#v err=%v", ineligibleReceipt, err)
	}

	if _, err := p.Exec(ctx, `UPDATE public.account_admin_operations SET lifecycle_override_reason='risk_accepted' WHERE command_id=$1`, operation.CommandID); err == nil {
		t.Fatal("runtime direct override update unexpectedly succeeded")
	}
	// Migration 46's permissive seven-argument runtime signature is gone.
	// These four attempts model the old caller-controlled hash cases. Since the
	// legacy entry point is absent, none can create a reservation or any other
	// durable state through the runtime role.
	negativeCases := []struct {
		name, kind, reason string
		target             uuid.UUID
		hash               []byte
	}{
		{name: "wrong reason", kind: "lifecycle", reason: "node_stopped", target: operation.CommandID, hash: mustOverrideHash(t, store.AccountLifecycleOverride, operation.CommandID, "process_restarted")},
		{name: "wrong target", kind: "lifecycle", reason: "risk_accepted", target: operation.CommandID, hash: mustOverrideHash(t, store.AccountLifecycleOverride, uuid.New(), "risk_accepted")},
		{name: "wrong kind", kind: "same", reason: "risk_accepted", target: operation.CommandID, hash: mustOverrideHash(t, store.AccountLifecycleOverride, operation.CommandID, "risk_accepted")},
		{name: "random hash", kind: "lifecycle", reason: "risk_accepted", target: operation.CommandID, hash: bytes.Repeat([]byte{0xa5}, 32)},
	}
	for _, tc := range negativeCases {
		t.Run(tc.name, func(t *testing.T) {
			commandID := uuid.New()
			var err error
			if tc.kind == "same" {
				_, err = p.Exec(ctx, `SELECT public.control_apply_same_account_override_v1($1,$2,$3,$4,$5,$6,$7)`, commandID, admin, tc.target, tc.reason, "OVERRIDE UNKNOWN OPERATION SAME-ACCOUNT BLOCK", tc.hash, "negative")
			} else {
				_, err = p.Exec(ctx, `SELECT public.control_apply_lifecycle_override_v1($1,$2,$3,$4,$5,$6,$7)`, commandID, admin, tc.target, tc.reason, "OVERRIDE UNKNOWN OPERATION LIFECYCLE BLOCK", tc.hash, "negative")
			}
			if err == nil {
				t.Fatal("legacy permissive override signature unexpectedly callable")
			}
			var count int
			for _, query := range []string{
				`SELECT count(*) FROM admin_command_registry WHERE command_id=$1`,
				`SELECT count(*) FROM account_admin_command_receipts WHERE command_id=$1`,
				`SELECT count(*) FROM audit_logs WHERE details->>'command_id'=$1`,
			} {
				if err := p.QueryRow(ctx, query, commandID).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Fatalf("%s created %d rows", query, count)
				}
			}
		})
	}
}

func mustOverrideHash(t *testing.T, kind store.AccountOperationKind, target uuid.UUID, reason string) []byte {
	t.Helper()
	intent, err := accountadmin.CanonicalOverrideIntentV1(kind, target, reason, "")
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(intent)
	return hash[:]
}

func acceptOverrideTarget(t *testing.T, ctx context.Context, repo *store.AccountOperationRepository, admin, node uuid.UUID, email string) store.AccountAdminOperation {
	t.Helper()
	hash := sha256.Sum256([]byte(email))
	op, err := repo.Accept(ctx, store.AccountOperationAcceptance{CommandID: uuid.New(), ActorAdminID: admin, OperationKind: store.AccountDisable, NodeInstanceID: node, AccountKey: "antigravity:" + email, CanonicalIntentHash: hash[:]})
	if err != nil {
		t.Fatal(err)
	}
	return op
}
