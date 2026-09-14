package store_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
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
	if !bytes.Equal(storedHash, referenceOverrideHash(t, "account.lifecycle_override", operation.CommandID, command.Reason, command.Detail)) {
		t.Fatal("derived override hash did not match independent reference encoder")
	}
	// Both override kinds are legal on the same target. Keep every semantic
	// input equal except the frozen kind/confirmation pair.
	sameKindCommand := accountadmin.OverrideCommand{
		CommandID: uuid.New(), ActorAdminID: admin, TargetOperation: operation.CommandID,
		Reason: "risk_accepted", Confirmation: "OVERRIDE UNKNOWN OPERATION SAME-ACCOUNT BLOCK",
		RequestID: command.RequestID, Detail: command.Detail,
	}
	if _, err := service.SameAccountOverride(ctx, sameKindCommand); err != nil {
		t.Fatal(err)
	}
	sameKindHash := queryOverrideHash(t, ctx, p, sameKindCommand.CommandID)
	if !bytes.Equal(sameKindHash, referenceOverrideHash(t, "account.same_account_override", operation.CommandID, sameKindCommand.Reason, sameKindCommand.Detail)) || bytes.Equal(storedHash, sameKindHash) {
		t.Fatal("override-kind binding did not match independent hashes")
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
	var sameHash []byte
	if err := p.QueryRow(ctx, `SELECT canonical_intent_hash FROM admin_command_registry WHERE command_id=$1`, sameCommand.CommandID).Scan(&sameHash); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sameHash, referenceOverrideHash(t, "account.same_account_override", secondTarget.CommandID, sameCommand.Reason, sameCommand.Detail)) {
		t.Fatal("same-account override hash did not match independent reference encoder")
	}
	if bytes.Equal(wantHash[:], sameHash) {
		t.Fatal("different override kinds unexpectedly share canonical hash")
	}

	// A route-specific override is immutable, so compare the same target/kind in
	// isolated real databases to vary only the reason while retaining stored DB
	// hashes for both commands.
	reasonAHash := overrideHashInFreshDatabase(t, "risk_accepted")
	reasonBHash := overrideHashInFreshDatabase(t, "process_restarted")
	if bytes.Equal(reasonAHash, reasonBHash) {
		t.Fatal("reason binding did not produce distinct hashes")
	}

	// The same kind and reason with two targets must also produce distinct hashes.
	targetA := acceptOverrideTarget(t, ctx, repo, admin, node, "target-binding-a@example.invalid")
	targetB := acceptOverrideTarget(t, ctx, repo, admin, node, "target-binding-b@example.invalid")
	for _, target := range []store.AccountAdminOperation{targetA, targetB} {
		if _, _, err := repo.AdmitAccountDispatch(ctx, target.CommandID, node, target.AccountKey, "target-binding"); err != nil {
			t.Fatal(err)
		}
	}
	targetCommandA := accountadmin.OverrideCommand{CommandID: uuid.New(), ActorAdminID: admin, TargetOperation: targetA.CommandID, Reason: "risk_accepted", Confirmation: "OVERRIDE UNKNOWN OPERATION LIFECYCLE BLOCK"}
	targetCommandB := accountadmin.OverrideCommand{CommandID: uuid.New(), ActorAdminID: admin, TargetOperation: targetB.CommandID, Reason: "risk_accepted", Confirmation: "OVERRIDE UNKNOWN OPERATION LIFECYCLE BLOCK"}
	if _, err := service.LifecycleOverride(ctx, targetCommandA); err != nil {
		t.Fatal(err)
	}
	if _, err := service.LifecycleOverride(ctx, targetCommandB); err != nil {
		t.Fatal(err)
	}
	targetHashA := queryOverrideHash(t, ctx, p, targetCommandA.CommandID)
	targetHashB := queryOverrideHash(t, ctx, p, targetCommandB.CommandID)
	if !bytes.Equal(targetHashA, referenceOverrideHash(t, "account.lifecycle_override", targetA.CommandID, targetCommandA.Reason, targetCommandA.Detail)) || !bytes.Equal(targetHashB, referenceOverrideHash(t, "account.lifecycle_override", targetB.CommandID, targetCommandB.Reason, targetCommandB.Detail)) || bytes.Equal(targetHashA, targetHashB) {
		t.Fatal("target binding was not reflected in the canonical hash")
	}

	// Invalid and missing targets are terminal override commands with no target mutation.
	invalid := accountadmin.OverrideCommand{CommandID: uuid.New(), ActorAdminID: admin, TargetOperation: uuid.New(), Reason: "unknown", Confirmation: "OVERRIDE UNKNOWN OPERATION LIFECYCLE BLOCK"}
	invalidBefore := queryOverrideFields(t, ctx, p, operation.CommandID)
	if _, err := service.LifecycleOverride(ctx, invalid); !errors.Is(err, store.ErrInvalidAccountOperation) && !errors.Is(err, accountadmin.ErrInvalidCommand) {
		t.Fatalf("invalid reason error=%v", err)
	}
	if got := queryOverrideFields(t, ctx, p, operation.CommandID); got != invalidBefore {
		t.Fatalf("invalid reason changed override fields: before=%#v after=%#v", invalidBefore, got)
	}
	assertNoCommandEvidence(t, ctx, p, invalid.CommandID)
	invalidTarget := accountadmin.OverrideCommand{CommandID: uuid.New(), ActorAdminID: admin, TargetOperation: uuid.Nil, Reason: "risk_accepted", Confirmation: "OVERRIDE UNKNOWN OPERATION LIFECYCLE BLOCK"}
	invalidTargetBefore := queryOverrideFields(t, ctx, p, operation.CommandID)
	if _, err := service.LifecycleOverride(ctx, invalidTarget); !errors.Is(err, accountadmin.ErrInvalidCommand) {
		t.Fatalf("invalid target error=%v", err)
	}
	if got := queryOverrideFields(t, ctx, p, operation.CommandID); got != invalidTargetBefore {
		t.Fatalf("invalid target changed override fields: before=%#v after=%#v", invalidTargetBefore, got)
	}
	assertNoCommandEvidence(t, ctx, p, invalidTarget.CommandID)
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
	legacyCommandID := uuid.New()
	if _, err := p.Exec(ctx, `SELECT public.control_apply_lifecycle_override_v1($1,$2,$3,$4,$5,$6,$7)`, legacyCommandID, admin, operation.CommandID, "risk_accepted", "OVERRIDE UNKNOWN OPERATION LIFECYCLE BLOCK", bytes.Repeat([]byte{0xa5}, 32), "legacy"); err == nil {
		t.Fatal("legacy override signature unexpectedly remained callable")
	}
	assertNoCommandEvidence(t, ctx, p, legacyCommandID)
}

type overrideFields struct {
	lifecycleAt, lifecycleBy, lifecycleReason, sameAt, sameBy, sameReason any
}

func queryOverrideFields(t *testing.T, ctx context.Context, p *pgxpool.Pool, operationID uuid.UUID) overrideFields {
	t.Helper()
	var fields overrideFields
	if err := p.QueryRow(ctx, `SELECT lifecycle_override_at,lifecycle_override_by,lifecycle_override_reason,same_account_override_at,same_account_override_by,same_account_override_reason FROM account_admin_operations WHERE command_id=$1`, operationID).Scan(&fields.lifecycleAt, &fields.lifecycleBy, &fields.lifecycleReason, &fields.sameAt, &fields.sameBy, &fields.sameReason); err != nil {
		t.Fatal(err)
	}
	return fields
}

func queryOverrideHash(t *testing.T, ctx context.Context, p *pgxpool.Pool, commandID uuid.UUID) []byte {
	t.Helper()
	var hash []byte
	if err := p.QueryRow(ctx, `SELECT canonical_intent_hash FROM admin_command_registry WHERE command_id=$1`, commandID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	return hash
}

func assertNoCommandEvidence(t *testing.T, ctx context.Context, p *pgxpool.Pool, commandID uuid.UUID) {
	t.Helper()
	for _, query := range []string{
		`SELECT count(*) FROM admin_command_registry WHERE command_id=$1`,
		`SELECT count(*) FROM account_admin_command_receipts WHERE command_id=$1`,
		`SELECT count(*) FROM audit_logs WHERE details->>'command_id'=$1`,
	} {
		var count int
		if err := p.QueryRow(ctx, query, commandID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s created %d rows", query, count)
		}
	}
}

// referenceOverrideHash is deliberately independent of the production encoder.
// It mirrors the frozen compact JSON array encoding defined for intent v1.
func referenceOverrideHash(t *testing.T, kind string, target uuid.UUID, reason, detail string) []byte {
	t.Helper()
	confirmation := "OVERRIDE UNKNOWN OPERATION LIFECYCLE BLOCK"
	if kind == "account.same_account_override" {
		confirmation = "OVERRIDE UNKNOWN OPERATION SAME-ACCOUNT BLOCK"
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode([]any{"account-intent-v1", kind, target.String(), reason, confirmation, nilIfEmpty(detail)}); err != nil {
		t.Fatal(err)
	}
	encodedBytes := bytes.TrimSuffix(encoded.Bytes(), []byte{'\n'})
	hash := sha256.Sum256(encodedBytes)
	return hash[:]
}

func nilIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func overrideHashInFreshDatabase(t *testing.T, reason string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	databaseURL, owner, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
	defer cleanup()
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "37"); err != nil {
		t.Fatal(err)
	}
	admin := uuid.MustParse("00000000-0000-0000-0000-000000000701")
	node := uuid.MustParse("00000000-0000-0000-0000-000000000702")
	target := uuid.MustParse("00000000-0000-0000-0000-000000000703")
	overrideID := uuid.MustParse("00000000-0000-0000-0000-000000000705")
	if _, err := owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,$2,'Override Hash Admin','enabled',clock_timestamp())`, admin, "hash-"+reason); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO node_drivers(node_type,driver_contract_version,display_name) VALUES('cliproxyapi','v1','CLIProxyAPI') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO relay_node_assets(instance_id,display_name,node_type,driver_contract_version,management_endpoint) VALUES($1,'Override Hash Node','cliproxyapi','v1','http://node.example/')`, node); err != nil {
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
	intentHash := sha256.Sum256([]byte("override-hash-target"))
	if _, err := repo.Accept(ctx, store.AccountOperationAcceptance{CommandID: target, ActorAdminID: admin, OperationKind: store.AccountDisable, NodeInstanceID: node, AccountKey: "antigravity:hash-binding", CanonicalIntentHash: intentHash[:]}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.AdmitAccountDispatch(ctx, target, node, "antigravity:hash-binding", "hash-binding"); err != nil {
		t.Fatal(err)
	}
	service, err := accountadmin.NewService(repo, integrationNodeResolver{})
	if err != nil {
		t.Fatal(err)
	}
	command := accountadmin.OverrideCommand{CommandID: overrideID, ActorAdminID: admin, TargetOperation: target, Reason: reason, Confirmation: "OVERRIDE UNKNOWN OPERATION LIFECYCLE BLOCK"}
	if _, err := service.LifecycleOverride(ctx, command); err != nil {
		t.Fatal(err)
	}
	stored := queryOverrideHash(t, ctx, p, overrideID)
	want := referenceOverrideHash(t, "account.lifecycle_override", target, reason, "")
	if !bytes.Equal(stored, want) {
		t.Fatalf("stored hash=%x, want independent reference=%x", stored, want)
	}
	return stored
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
