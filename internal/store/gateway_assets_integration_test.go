package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

func TestGatewayLifecycleCommandsAndReplayPG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, database, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
	defer cleanup()
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "33"); err != nil {
		t.Fatal(err)
	}
	adminID := uuid.New()
	if _, err := database.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,'gateway-command-admin','Gateway Command Admin','enabled',clock_timestamp())`, adminID); err != nil {
		t.Fatal(err)
	}
	database.Close(ctx)
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	poolConfig.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE relay_control_runtime`)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repository, err := assetstore.NewGatewayLifecycleRepository(pool, []byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}

	first := uuid.New()
	register := assetstore.GatewayCommand{CommandID: uuid.New(), ActorAdminID: adminID, RequestID: "register-1", NewInstanceID: first, DisplayName: assetstore.StringPatch{Present: true, Value: "Gateway A"}, ManagementEndpoint: assetstore.StringPatch{Present: true, Value: "http://gateway-a.example"}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretSet, Value: "env://gateway/reader"}}
	result, err := repository.Register(ctx, register)
	if err != nil || result.HTTPStatus != 201 {
		t.Fatalf("register: status=%d err=%v", result.HTTPStatus, err)
	}
	replay, err := repository.Register(ctx, register)
	if err != nil || !replay.Replayed || !jsonEqual(replay.Body, result.Body) {
		t.Fatalf("register replay: %#v %v", replay, err)
	}
	restarted, err := assetstore.NewGatewayLifecycleRepository(pool, []byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	restartReplay, err := restarted.Register(ctx, register)
	if err != nil || !restartReplay.Replayed || !jsonEqual(restartReplay.Body, result.Body) {
		t.Fatalf("register replay after repository restart: %#v %v", restartReplay, err)
	}
	wrongKey, err := assetstore.NewGatewayLifecycleRepository(pool, []byte("abcdefghijklmnopqrstuvwxyzABCDEF"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = wrongKey.Register(ctx, register); !errors.Is(err, assetstore.ErrCommandConflict) {
		t.Fatalf("replay with unavailable historical K1 material=%v", err)
	}
	conflict := register
	conflict.ActorAdminID = uuid.New()
	if _, err = repository.Register(ctx, conflict); err != assetstore.ErrCommandConflict {
		t.Fatalf("actor conflict=%v", err)
	}

	edit := assetstore.GatewayCommand{CommandID: uuid.New(), ActorAdminID: adminID, RequestID: "edit-1", InstanceID: first, ExpectedRevision: 1, DisplayName: assetstore.StringPatch{Present: true, Value: "Gateway A2"}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretAbsent}}
	if result, err = repository.Edit(ctx, edit); err != nil || result.HTTPStatus != 200 {
		t.Fatalf("edit: status=%d err=%v", result.HTTPStatus, err)
	}
	if _, err = repository.Edit(ctx, assetstore.GatewayCommand{CommandID: uuid.New(), ActorAdminID: adminID, RequestID: "stale", InstanceID: first, ExpectedRevision: 1, DisplayName: assetstore.StringPatch{Present: true, Value: "stale"}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretAbsent}}); err != assetstore.ErrStaleAssetRevision {
		t.Fatalf("stale edit=%v", err)
	}

	second := uuid.New()
	replace := assetstore.GatewayCommand{CommandID: uuid.New(), ActorAdminID: adminID, RequestID: "replace-1", InstanceID: first, ExpectedRevision: 2, NewInstanceID: second, DisplayName: assetstore.StringPatch{Present: true, Value: "Gateway B"}, ManagementEndpoint: assetstore.StringPatch{Present: true, Value: "http://gateway-b.example"}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretClear}}
	if result, err = repository.Replace(ctx, replace); err != nil || result.HTTPStatus != 200 {
		t.Fatalf("replace: status=%d body=%s err=%v", result.HTTPStatus, result.Body, err)
	}
	detail, err := repository.Detail(ctx, first)
	if err != nil || detail.Asset.LifecycleStatus != "retired" || detail.Successor == nil || detail.Successor.NewInstanceID != second {
		t.Fatalf("old detail=%#v err=%v", detail, err)
	}
	current, err := repository.Current(ctx)
	if err != nil || current == nil || current.InstanceID != second || current.Revision != 1 {
		t.Fatalf("current=%#v err=%v", current, err)
	}
	retire := assetstore.GatewayCommand{CommandID: uuid.New(), ActorAdminID: adminID, RequestID: "retire-1", InstanceID: second, ExpectedRevision: 1, Secret: assetstore.SecretPatch{Operation: assetstore.SecretAbsent}}
	if result, err = repository.Retire(ctx, retire); err != nil || result.HTTPStatus != 200 {
		t.Fatalf("retire: status=%d err=%v", result.HTTPStatus, err)
	}
	if current, err = repository.Current(ctx); err != nil || current != nil {
		t.Fatalf("current after retire=%#v err=%v", current, err)
	}
	var audits, receipts int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE category='asset_gateway'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM asset_admin_command_receipts`).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if audits != 4 || receipts != 4 {
		t.Fatalf("audits=%d receipts=%d", audits, receipts)
	}
	var saved map[string]any
	if err = json.Unmarshal(result.Body, &saved); err != nil || saved["result"] != "retired" {
		t.Fatalf("saved result=%s err=%v", result.Body, err)
	}
}

func TestGatewayCanonicalIntentRequiresStableKeyForSecret(t *testing.T) {
	ctx := context.Background()
	_ = ctx
	if _, err := assetstore.NormalizeGatewayManagementEndpoint("https://gateway.example"); err != assetstore.ErrInvalidGatewayEndpoint {
		t.Fatalf("https err=%v", err)
	}
}

func TestGatewayConcurrentRegisterDifferentCommandsPG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, repository, adminID, cleanup := newGatewayRuntimeFixture(t, ctx)
	defer cleanup()

	start := make(chan struct{})
	type outcome struct {
		result assetstore.GatewayCommandResult
		err    error
	}
	outcomes := make(chan outcome, 2)
	for index := 0; index < 2; index++ {
		index := index
		go func() {
			command := assetstore.GatewayCommand{
				CommandID:          uuid.New(),
				ActorAdminID:       adminID,
				RequestID:          fmt.Sprintf("concurrent-register-%d", index),
				NewInstanceID:      uuid.New(),
				DisplayName:        assetstore.StringPatch{Present: true, Value: fmt.Sprintf("Concurrent Gateway %d", index)},
				ManagementEndpoint: assetstore.StringPatch{Present: true, Value: fmt.Sprintf("http://gateway-%d.example", index)},
				Secret:             assetstore.SecretPatch{Operation: assetstore.SecretAbsent},
			}
			<-start
			result, err := repository.Register(ctx, command)
			outcomes <- outcome{result: result, err: err}
		}()
	}
	close(start)

	successes, conflicts := 0, 0
	for range 2 {
		got := <-outcomes
		if got.err == nil && got.result.HTTPStatus == 201 {
			successes++
			continue
		}
		if errors.Is(got.err, assetstore.ErrCurrentGatewayExists) {
			conflicts++
			continue
		}
		t.Fatalf("unexpected concurrent register outcome: result=%#v err=%v", got.result, got.err)
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent register outcomes: successes=%d conflicts=%d, want 1/1", successes, conflicts)
	}
	assertGatewayCounts(t, ctx, pool, 1, 0, 1)
}

func TestGatewaySameCommandConcurrencyReplayAndConflictsPG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, repository, adminID, cleanup := newGatewayRuntimeFixture(t, ctx)
	defer cleanup()

	command := assetstore.GatewayCommand{
		CommandID:          uuid.New(),
		ActorAdminID:       adminID,
		RequestID:          "same-command-register",
		NewInstanceID:      uuid.New(),
		DisplayName:        assetstore.StringPatch{Present: true, Value: "Replay Gateway"},
		ManagementEndpoint: assetstore.StringPatch{Present: true, Value: "http://replay-gateway.example"},
		Secret:             assetstore.SecretPatch{Operation: assetstore.SecretAbsent},
	}
	start := make(chan struct{})
	type outcome struct {
		result assetstore.GatewayCommandResult
		err    error
	}
	outcomes := make(chan outcome, 2)
	for range 2 {
		go func() {
			<-start
			result, err := repository.Register(ctx, command)
			outcomes <- outcome{result: result, err: err}
		}()
	}
	close(start)

	var first, replay assetstore.GatewayCommandResult
	for range 2 {
		got := <-outcomes
		if got.err != nil {
			t.Fatalf("same command register: %v", got.err)
		}
		if got.result.Replayed {
			replay = got.result
		} else {
			first = got.result
		}
	}
	if first.HTTPStatus != 201 || replay.HTTPStatus != 201 || !jsonEqual(first.Body, replay.Body) {
		t.Fatalf("same command results: first=%#v replay=%#v", first, replay)
	}
	var gatewayCount, auditCount, receiptCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gateway_instances`).Scan(&gatewayCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE category='asset_gateway' AND action='gateway.register'`).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM asset_admin_command_receipts WHERE command_id=$1`, command.CommandID).Scan(&receiptCount); err != nil {
		t.Fatal(err)
	}
	if gatewayCount != 1 || auditCount != 1 || receiptCount != 1 {
		t.Fatalf("same command durable counts: gateways=%d audits=%d receipts=%d, want 1/1/1", gatewayCount, auditCount, receiptCount)
	}

	edit := assetstore.GatewayCommand{
		CommandID:        uuid.New(),
		ActorAdminID:     adminID,
		RequestID:        "state-change-after-register",
		InstanceID:       command.NewInstanceID,
		ExpectedRevision: 1,
		DisplayName:      assetstore.StringPatch{Present: true, Value: "Changed Gateway"},
		Secret:           assetstore.SecretPatch{Operation: assetstore.SecretAbsent},
	}
	if _, err := repository.Edit(ctx, edit); err != nil {
		t.Fatalf("edit after register: %v", err)
	}
	afterChangeReplay, err := repository.Register(ctx, command)
	if err != nil || !afterChangeReplay.Replayed || !jsonEqual(afterChangeReplay.Body, first.Body) {
		t.Fatalf("replay after state change: result=%#v err=%v", afterChangeReplay, err)
	}

	actorConflict := command
	actorConflict.ActorAdminID = uuid.New()
	if _, err := repository.Register(ctx, actorConflict); !errors.Is(err, assetstore.ErrCommandConflict) {
		t.Fatalf("actor conflict=%v", err)
	}
	intentConflict := command
	intentConflict.DisplayName = assetstore.StringPatch{Present: true, Value: "Different Intent"}
	if _, err := repository.Register(ctx, intentConflict); !errors.Is(err, assetstore.ErrCommandConflict) {
		t.Fatalf("intent conflict=%v", err)
	}
}

func TestGatewayTransactionFailureRollsBackReceiptPG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, repository, adminID, cleanup := newGatewayRuntimeFixture(t, ctx)
	defer cleanup()

	command := assetstore.GatewayCommand{
		CommandID:          uuid.New(),
		ActorAdminID:       adminID,
		RequestID:          strings.Repeat("x", 129),
		NewInstanceID:      uuid.New(),
		DisplayName:        assetstore.StringPatch{Present: true, Value: "Rolled Back Gateway"},
		ManagementEndpoint: assetstore.StringPatch{Present: true, Value: "http://rollback-gateway.example"},
		Secret:             assetstore.SecretPatch{Operation: assetstore.SecretAbsent},
	}
	if _, err := repository.Register(ctx, command); err == nil {
		t.Fatal("register unexpectedly succeeded with invalid audit request_id")
	}
	var gatewayCount, auditCount, receiptCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM gateway_instances`).Scan(&gatewayCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs`).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM asset_admin_command_receipts WHERE command_id=$1`, command.CommandID).Scan(&receiptCount); err != nil {
		t.Fatal(err)
	}
	if gatewayCount != 0 || auditCount != 0 || receiptCount != 0 {
		t.Fatalf("rollback left durable state: gateways=%d audits=%d receipts=%d", gatewayCount, auditCount, receiptCount)
	}
}

func TestGatewayHistoryPaginationAndGenerationFencePG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	_, repository, adminID, cleanup := newGatewayRuntimeFixture(t, ctx)
	defer cleanup()

	ids := []uuid.UUID{
		uuid.MustParse("00000000-0000-4000-8000-000000000001"),
		uuid.MustParse("00000000-0000-4000-8000-000000000002"),
		uuid.MustParse("00000000-0000-4000-8000-000000000003"),
	}
	if _, err := repository.Register(ctx, assetstore.GatewayCommand{
		CommandID: uuid.New(), ActorAdminID: adminID, RequestID: "page-register", NewInstanceID: ids[0],
		DisplayName: assetstore.StringPatch{Present: true, Value: "Gateway 1"}, ManagementEndpoint: assetstore.StringPatch{Present: true, Value: "http://gateway-1.example"}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretAbsent},
	}); err != nil {
		t.Fatal(err)
	}
	for index := 1; index < len(ids); index++ {
		if _, err := repository.Replace(ctx, assetstore.GatewayCommand{
			CommandID: uuid.New(), ActorAdminID: adminID, RequestID: fmt.Sprintf("page-replace-%d", index), InstanceID: ids[index-1], ExpectedRevision: 1,
			NewInstanceID: ids[index], DisplayName: assetstore.StringPatch{Present: true, Value: fmt.Sprintf("Gateway %d", index+1)}, ManagementEndpoint: assetstore.StringPatch{Present: true, Value: fmt.Sprintf("http://gateway-%d.example", index+1)}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretAbsent},
		}); err != nil {
			t.Fatal(err)
		}
	}

	first, err := repository.List(ctx, "all", uuid.Nil, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	if !first.HasMore || len(first.Items) != 2 || first.Items[0].InstanceID != ids[0] || first.Items[1].InstanceID != ids[1] {
		t.Fatalf("first Gateway history page = %#v", first)
	}
	second, err := repository.List(ctx, "all", first.Items[1].InstanceID, 2, first.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if second.HasMore || len(second.Items) != 1 || second.Items[0].InstanceID != ids[2] {
		t.Fatalf("second Gateway history page = %#v", second)
	}
	if _, err := repository.Edit(ctx, assetstore.GatewayCommand{
		CommandID: uuid.New(), ActorAdminID: adminID, RequestID: "page-generation-edit", InstanceID: ids[2], ExpectedRevision: 1,
		DisplayName: assetstore.StringPatch{Present: true, Value: "Gateway 3 updated"}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretAbsent},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.List(ctx, "all", first.Items[1].InstanceID, 2, first.Generation); !errors.Is(err, assetstore.ErrGatewayCursorStale) {
		t.Fatalf("stale Gateway history generation error = %v", err)
	}
}

func TestGatewayReceiptActorFirstAndLazyK1PG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	owner, pool, repository, adminID, cleanup := newGatewayRuntimeFixtureWithOwner(t, ctx)
	defer cleanup()

	command := assetstore.GatewayCommand{
		CommandID:          uuid.New(),
		ActorAdminID:       adminID,
		RequestID:          "actor-first-register",
		NewInstanceID:      uuid.New(),
		DisplayName:        assetstore.StringPatch{Present: true, Value: "Actor First Gateway"},
		ManagementEndpoint: assetstore.StringPatch{Present: true, Value: "http://actor-first.example"},
		Secret:             assetstore.SecretPatch{Operation: assetstore.SecretSet, Value: "vault://gateway/actor-first"},
	}
	created, err := repository.Register(ctx, command)
	if err != nil || created.HTTPStatus != 201 {
		t.Fatalf("register: result=%#v err=%v", created, err)
	}

	otherActor := uuid.New()
	actorConflicts := []struct {
		name       string
		repository *assetstore.GatewayLifecycleRepository
		operation  func(context.Context, assetstore.GatewayCommand) (assetstore.GatewayCommandResult, error)
		command    assetstore.GatewayCommand
	}{
		{name: "invalid endpoint", repository: repository, operation: repository.Register, command: func() assetstore.GatewayCommand {
			c := command
			c.ActorAdminID = otherActor
			c.ManagementEndpoint.Value = "https://invalid.example"
			return c
		}()},
		{name: "invalid secret", repository: repository, operation: repository.Register, command: func() assetstore.GatewayCommand {
			c := command
			c.ActorAdminID = otherActor
			c.Secret.Value = "http://invalid-secret"
			return c
		}()},
		{name: "missing K1", repository: mustGatewayRepository(t, pool, nil), command: func() assetstore.GatewayCommand { c := command; c.ActorAdminID = otherActor; return c }()},
		{name: "stale revision", repository: repository, operation: repository.Edit, command: assetstore.GatewayCommand{CommandID: command.CommandID, ActorAdminID: otherActor, InstanceID: command.NewInstanceID, ExpectedRevision: 999, DisplayName: assetstore.StringPatch{Present: true, Value: "stale"}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretAbsent}}},
		{name: "retired target", repository: repository, operation: repository.Retire, command: assetstore.GatewayCommand{CommandID: command.CommandID, ActorAdminID: otherActor, InstanceID: uuid.New(), ExpectedRevision: 1, Secret: assetstore.SecretPatch{Operation: assetstore.SecretAbsent}}},
	}
	actorConflicts[2].operation = actorConflicts[2].repository.Register
	for _, test := range actorConflicts {
		t.Run(test.name, func(t *testing.T) {
			if _, gotErr := test.operation(ctx, test.command); !errors.Is(gotErr, assetstore.ErrCommandConflict) {
				t.Fatalf("error=%v, want command conflict", gotErr)
			}
		})
	}

	if replay, replayErr := repository.Register(ctx, command); replayErr != nil || !replay.Replayed || replay.HTTPStatus != 201 || !jsonEqual(replay.Body, created.Body) {
		t.Fatalf("same intent replay: result=%#v err=%v", replay, replayErr)
	}
	differentSecret := command
	differentSecret.Secret.Value = "vault://gateway/different"
	if _, err = repository.Register(ctx, differentSecret); !errors.Is(err, assetstore.ErrCommandConflict) {
		t.Fatalf("different Secret intent=%v", err)
	}
	wrongKey := mustGatewayRepository(t, pool, []byte("abcdefghijklmnopqrstuvwxyzABCDEF"))
	if _, err = wrongKey.Register(ctx, command); !errors.Is(err, assetstore.ErrCommandConflict) {
		t.Fatalf("structurally valid replacement K1=%v", err)
	}
	missingKey := mustGatewayRepository(t, pool, nil)
	if _, err = missingKey.Register(ctx, command); !errors.Is(err, assetstore.ErrReceiptKeyUnavailable) {
		t.Fatalf("missing historical K1=%v", err)
	}
	if replay, replayErr := repository.Register(ctx, command); replayErr != nil || !replay.Replayed || !jsonEqual(replay.Body, created.Body) {
		t.Fatalf("replay after restoring K1: result=%#v err=%v", replay, replayErr)
	}
	wrongKind := assetstore.GatewayCommand{CommandID: command.CommandID, ActorAdminID: adminID, InstanceID: command.NewInstanceID, ExpectedRevision: 999, Secret: assetstore.SecretPatch{Operation: assetstore.SecretAbsent}}
	if _, err = repository.Retire(ctx, wrongKind); !errors.Is(err, assetstore.ErrCommandConflict) {
		t.Fatalf("different receipt command kind=%v", err)
	}

	var gateways, receipts, audits int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM gateway_instances`).Scan(&gateways); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM asset_admin_command_receipts`).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE category='asset_gateway' AND result='success'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if gateways != 1 || receipts != 1 || audits != 1 {
		t.Fatalf("unexpected side effects: gateways=%d receipts=%d audits=%d", gateways, receipts, audits)
	}

	if _, err = owner.Exec(ctx, `ALTER TABLE asset_admin_command_receipts DISABLE TRIGGER asset_admin_command_receipts_immutable`); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `ALTER TABLE asset_admin_command_receipts DROP CONSTRAINT asset_admin_command_receipts_encoding_check`); err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, `UPDATE asset_admin_command_receipts SET intent_encoding_version=2 WHERE command_id=$1`, command.CommandID); err != nil {
		t.Fatal(err)
	}
	if _, err = repository.Register(ctx, command); !errors.Is(err, assetstore.ErrReceiptEncodingUnknown) {
		t.Fatalf("unknown receipt encoding=%v", err)
	}
}

func TestGatewayNonSecretCommandsAndReplayWithoutK1PG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, repository, adminID, cleanup := newGatewayRuntimeFixture(t, ctx)
	defer cleanup()

	instanceID := uuid.New()
	register := assetstore.GatewayCommand{CommandID: uuid.New(), ActorAdminID: adminID, RequestID: "no-k1-register", NewInstanceID: instanceID, DisplayName: assetstore.StringPatch{Present: true, Value: "No K1 Gateway"}, ManagementEndpoint: assetstore.StringPatch{Present: true, Value: "http://no-k1.example"}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretSet, Value: "vault://gateway/no-k1"}}
	if _, err := repository.Register(ctx, register); err != nil {
		t.Fatal(err)
	}
	withoutKey := mustGatewayRepository(t, pool, nil)

	editAbsent := assetstore.GatewayCommand{CommandID: uuid.New(), ActorAdminID: adminID, RequestID: "no-k1-edit-absent", InstanceID: instanceID, ExpectedRevision: 1, DisplayName: assetstore.StringPatch{Present: true, Value: "No K1 Edited"}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretAbsent}}
	editResult, err := withoutKey.Edit(ctx, editAbsent)
	if err != nil || editResult.HTTPStatus != 200 {
		t.Fatalf("SecretAbsent edit: result=%#v err=%v", editResult, err)
	}
	if replay, replayErr := withoutKey.Edit(ctx, editAbsent); replayErr != nil || !replay.Replayed || !jsonEqual(replay.Body, editResult.Body) {
		t.Fatalf("SecretAbsent replay: result=%#v err=%v", replay, replayErr)
	}

	editClear := assetstore.GatewayCommand{CommandID: uuid.New(), ActorAdminID: adminID, RequestID: "no-k1-edit-clear", InstanceID: instanceID, ExpectedRevision: 2, Secret: assetstore.SecretPatch{Operation: assetstore.SecretClear}}
	clearResult, err := withoutKey.Edit(ctx, editClear)
	if err != nil || clearResult.HTTPStatus != 200 {
		t.Fatalf("SecretClear edit: result=%#v err=%v", clearResult, err)
	}
	if replay, replayErr := withoutKey.Edit(ctx, editClear); replayErr != nil || !replay.Replayed || !jsonEqual(replay.Body, clearResult.Body) {
		t.Fatalf("SecretClear replay: result=%#v err=%v", replay, replayErr)
	}

	retire := assetstore.GatewayCommand{CommandID: uuid.New(), ActorAdminID: adminID, RequestID: "no-k1-retire", InstanceID: instanceID, ExpectedRevision: 3, Secret: assetstore.SecretPatch{Operation: assetstore.SecretAbsent}}
	retireResult, err := withoutKey.Retire(ctx, retire)
	if err != nil || retireResult.HTTPStatus != 200 {
		t.Fatalf("Retire without K1: result=%#v err=%v", retireResult, err)
	}
	if replay, replayErr := withoutKey.Retire(ctx, retire); replayErr != nil || !replay.Replayed || !jsonEqual(replay.Body, retireResult.Body) {
		t.Fatalf("Retire replay without K1: result=%#v err=%v", replay, replayErr)
	}

	secretSet := assetstore.GatewayCommand{CommandID: uuid.New(), ActorAdminID: adminID, RequestID: "no-k1-secret-set", NewInstanceID: uuid.New(), DisplayName: assetstore.StringPatch{Present: true, Value: "Missing K1"}, ManagementEndpoint: assetstore.StringPatch{Present: true, Value: "http://missing-k1.example"}, Secret: assetstore.SecretPatch{Operation: assetstore.SecretSet, Value: "vault://gateway/missing"}}
	if _, err = withoutKey.Register(ctx, secretSet); !errors.Is(err, assetstore.ErrReceiptKeyUnavailable) {
		t.Fatalf("new SecretSet without K1=%v", err)
	}
	var receiptCount, auditCount int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM asset_admin_command_receipts WHERE command_id=$1`, secretSet.CommandID).Scan(&receiptCount); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE category='asset_gateway' AND details->>'command_id'=$1`, secretSet.CommandID.String()).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if receiptCount != 0 || auditCount != 0 {
		t.Fatalf("missing K1 side effects: receipts=%d audits=%d", receiptCount, auditCount)
	}
}

func newGatewayRuntimeFixtureWithOwner(t *testing.T, ctx context.Context) (*pgx.Conn, *pgxpool.Pool, *assetstore.GatewayLifecycleRepository, uuid.UUID, func()) {
	t.Helper()
	databaseURL, owner, cleanupDatabase := newGatewayLifecycleMigrationDatabase(t, ctx)
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "33"); err != nil {
		cleanupDatabase()
		t.Fatal(err)
	}
	adminID := uuid.New()
	if _, err := owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,$2,'Gateway Receipt Admin','enabled',clock_timestamp())`, adminID, "gateway-receipt-"+strings.ReplaceAll(adminID.String(), "-", "")[:20]); err != nil {
		cleanupDatabase()
		t.Fatal(err)
	}
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		cleanupDatabase()
		t.Fatal(err)
	}
	poolConfig.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE relay_control_runtime`)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		cleanupDatabase()
		t.Fatal(err)
	}
	repository := mustGatewayRepository(t, pool, []byte("01234567890123456789012345678901"))
	return owner, pool, repository, adminID, func() {
		pool.Close()
		owner.Close(ctx)
		cleanupDatabase()
	}
}

func mustGatewayRepository(t *testing.T, pool *pgxpool.Pool, key []byte) *assetstore.GatewayLifecycleRepository {
	t.Helper()
	repository, err := assetstore.NewGatewayLifecycleRepository(pool, key)
	if err != nil {
		t.Fatal(err)
	}
	return repository
}

func newGatewayRuntimeFixture(t *testing.T, ctx context.Context) (*pgxpool.Pool, *assetstore.GatewayLifecycleRepository, uuid.UUID, func()) {
	t.Helper()
	databaseURL, database, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "33"); err != nil {
		cleanup()
		t.Fatal(err)
	}
	adminID := uuid.New()
	if _, err := database.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at) VALUES($1,$2,'Gateway Concurrency Admin','enabled',clock_timestamp())`, adminID, "gateway-concurrency-"+strings.ReplaceAll(adminID.String(), "-", "")[:20]); err != nil {
		database.Close(ctx)
		cleanup()
		t.Fatal(err)
	}
	database.Close(ctx)
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	poolConfig.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE relay_control_runtime`)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		cleanup()
		t.Fatal(err)
	}
	repository, err := assetstore.NewGatewayLifecycleRepository(pool, []byte("01234567890123456789012345678901"))
	if err != nil {
		pool.Close()
		cleanup()
		t.Fatal(err)
	}
	return pool, repository, adminID, func() {
		pool.Close()
		cleanup()
	}
}

func assertGatewayCounts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, active, retired, total int) {
	t.Helper()
	var counts assetstore.GatewayCounts
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE lifecycle_status='active'), count(*) FILTER (WHERE lifecycle_status='retired'), count(*) FROM gateway_instances`).Scan(&counts.Active, &counts.Retired, &counts.Total); err != nil {
		t.Fatal(err)
	}
	if counts.Active != int64(active) || counts.Retired != int64(retired) || counts.Total != int64(total) {
		t.Fatalf("gateway counts=%+v, want active=%d retired=%d total=%d", counts, active, retired, total)
	}
}

func jsonEqual(left, right []byte) bool {
	var a, b any
	return json.Unmarshal(left, &a) == nil && json.Unmarshal(right, &b) == nil && reflect.DeepEqual(a, b)
}
