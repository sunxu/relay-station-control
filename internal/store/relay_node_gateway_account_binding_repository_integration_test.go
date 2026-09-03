package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	jobstore "github.com/sunxu/relay-station-control/internal/store"
)

func insertGatewayDirectoryCurrentState(
	t *testing.T,
	ctx context.Context,
	database *isolatedJobDatabase,
	gatewayID uuid.UUID,
	snapshotID uuid.UUID,
	lastSuccessReceivedAt time.Time,
) {
	t.Helper()
	var runID uuid.UUID
	fingerprint := make([]byte, 32)
	slot := lastSuccessReceivedAt.Truncate(180 * time.Second)
	startedAt := slot

	// Check if an ingestion run already exists for this slot
	err := database.owner.QueryRow(ctx, `
		SELECT ingestion_run_id
		FROM gateway_directory_ingestion_runs
		WHERE gateway_instance_id = $1 AND scheduled_at = $2
	`, gatewayID, slot).Scan(&runID)
	if err != nil {
		newRunID := uuid.New()
		err = database.owner.QueryRow(ctx, `INSERT INTO gateway_directory_ingestion_runs(
			ingestion_run_id, gateway_instance_id, scheduled_at, status, attempt_count,
			created_at, first_started_at, last_started_at,
			outcome, source_generated_at,
			terminal_at, received_at, content_fingerprint, snapshot_id, account_count
		) VALUES ($1, $2, $3, 'succeeded', 1, $4, $5, $5, 'changed', $6, $6, $6, $7, $8, 1)
		RETURNING ingestion_run_id`,
			newRunID, gatewayID, slot, slot, startedAt, lastSuccessReceivedAt, fingerprint, snapshotID).Scan(&runID)
		if err != nil {
			t.Fatal(err)
		}
	}

	_, err = database.owner.Exec(ctx, `INSERT INTO gateway_directory_current_state(
		gateway_instance_id, current_snapshot_id, current_content_fingerprint,
		last_success_received_at, last_source_generated_at, last_success_run_id, updated_at
	) VALUES ($1, $2, $3, $4, $4, $5, clock_timestamp())
	ON CONFLICT (gateway_instance_id) DO UPDATE SET
		current_snapshot_id = EXCLUDED.current_snapshot_id,
		current_content_fingerprint = EXCLUDED.current_content_fingerprint,
		last_success_received_at = EXCLUDED.last_success_received_at,
		last_source_generated_at = EXCLUDED.last_source_generated_at,
		last_success_run_id = EXCLUDED.last_success_run_id,
		updated_at = clock_timestamp()`,
		gatewayID, snapshotID, fingerprint, lastSuccessReceivedAt, runID)
	if err != nil {
		t.Fatal(err)
	}
}

func TestRelayBindingRepository_Bind(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	fixture := newRelayBindingSchemaFixture(t, ctx, database)

	repo, err := jobstore.NewRelayBindingRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("successful bind with audit and DB operation time", func(t *testing.T) {
		nodeID := fixture.insertNode(t, ctx, database)
		// set current state fresh (10s ago)
		var dbNow time.Time
		if err := database.owner.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
			t.Fatal(err)
		}
		insertGatewayDirectoryCurrentState(t, ctx, database, fixture.gatewayID, fixture.snapshotID, dbNow.Add(-10*time.Second))

		accountID := fixture.accountIDs[0]
		requestID := "req-bind-success-" + uuid.NewString()

		res, err := repo.Bind(ctx, jobstore.BindParams{
			RelayNodeID:       nodeID,
			GatewayInstanceID: fixture.gatewayID,
			GatewayAccountID:  accountID,
			AdminID:           fixture.adminID,
			RequestID:         requestID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Outcome != jobstore.RelayBindingOutcomeSuccess {
			t.Fatalf("expected outcome success, got %s", res.Outcome)
		}
		if res.Binding == nil {
			t.Fatal("expected binding to be non-nil")
		}
		if res.Binding.RelayNodeID != nodeID || res.Binding.GatewayAccountID != accountID {
			t.Fatalf("unexpected binding: %+v", res.Binding)
		}
		if res.Binding.EvidenceSnapshotID != fixture.snapshotID {
			t.Fatalf("expected evidence snapshot %s, got %s", fixture.snapshotID, res.Binding.EvidenceSnapshotID)
		}
		if res.Binding.BindReason != "administrator_bind" {
			t.Fatalf("expected bind_reason administrator_bind, got %s", res.Binding.BindReason)
		}
		if res.Binding.BoundAt != res.OperationAt {
			t.Fatalf("expected BoundAt == OperationAt, got %v vs %v", res.Binding.BoundAt, res.OperationAt)
		}

		// Verify audit log exists
		var auditCount int
		var detailsRaw []byte
		var action string
		var actorAdminID uuid.UUID
		if err := database.owner.QueryRow(ctx, `SELECT count(*), details, action, actor_admin_id
			FROM audit_logs WHERE request_id = $1 GROUP BY details, action, actor_admin_id`, requestID).Scan(
			&auditCount, &detailsRaw, &action, &actorAdminID); err != nil {
			t.Fatal(err)
		}
		if auditCount != 1 || action != "relay_binding.bind" || actorAdminID != fixture.adminID {
			t.Fatalf("unexpected audit: count=%d action=%s actor=%s", auditCount, action, actorAdminID)
		}
		var details map[string]any
		if err := json.Unmarshal(detailsRaw, &details); err != nil {
			t.Fatal(err)
		}
		if details["old_gateway_account_id"] != nil {
			t.Fatalf("expected old_gateway_account_id null, got %v", details["old_gateway_account_id"])
		}
		if int64(details["new_gateway_account_id"].(float64)) != accountID {
			t.Fatalf("expected new_gateway_account_id %d, got %v", accountID, details["new_gateway_account_id"])
		}
		if details["evidence_snapshot_id"] != fixture.snapshotID.String() {
			t.Fatalf("expected evidence_snapshot_id %s, got %v", fixture.snapshotID, details["evidence_snapshot_id"])
		}
		if details["reason_code"] != "administrator_bind" {
			t.Fatalf("expected reason_code administrator_bind, got %v", details["reason_code"])
		}
	})

	t.Run("directory stale fails closed", func(t *testing.T) {
		nodeID := fixture.insertNode(t, ctx, database)
		var dbNow time.Time
		if err := database.owner.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
			t.Fatal(err)
		}
		// 541s ago (stale)
		insertGatewayDirectoryCurrentState(t, ctx, database, fixture.gatewayID, fixture.snapshotID, dbNow.Add(-541*time.Second))

		res, err := repo.Bind(ctx, jobstore.BindParams{
			RelayNodeID:       nodeID,
			GatewayInstanceID: fixture.gatewayID,
			GatewayAccountID:  fixture.accountIDs[1],
			AdminID:           fixture.adminID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Outcome != jobstore.RelayBindingOutcomeDirectoryStale {
			t.Fatalf("expected directory_stale, got %s", res.Outcome)
		}
	})

	t.Run("account not found in current snapshot fails closed", func(t *testing.T) {
		nodeID := fixture.insertNode(t, ctx, database)
		var dbNow time.Time
		if err := database.owner.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
			t.Fatal(err)
		}
		insertGatewayDirectoryCurrentState(t, ctx, database, fixture.gatewayID, fixture.snapshotID, dbNow.Add(-5*time.Second))

		res, err := repo.Bind(ctx, jobstore.BindParams{
			RelayNodeID:       nodeID,
			GatewayInstanceID: fixture.gatewayID,
			GatewayAccountID:  999999, // not in snapshot
			AdminID:           fixture.adminID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Outcome != jobstore.RelayBindingOutcomeAccountNotFound {
			t.Fatalf("expected account_not_found, got %s", res.Outcome)
		}
	})

	t.Run("node conflict when node already bound", func(t *testing.T) {
		nodeID := fixture.insertNode(t, ctx, database)
		var dbNow time.Time
		if err := database.owner.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
			t.Fatal(err)
		}
		insertGatewayDirectoryCurrentState(t, ctx, database, fixture.gatewayID, fixture.snapshotID, dbNow.Add(-5*time.Second))

		// First bind succeeds
		res1, err := repo.Bind(ctx, jobstore.BindParams{
			RelayNodeID:       nodeID,
			GatewayInstanceID: fixture.gatewayID,
			GatewayAccountID:  fixture.accountIDs[2],
			AdminID:           fixture.adminID,
		})
		if err != nil || res1.Outcome != jobstore.RelayBindingOutcomeSuccess {
			t.Fatalf("first bind failed: %v, %+v", err, res1)
		}

		// Second bind on same node fails with node_conflict
		res2, err := repo.Bind(ctx, jobstore.BindParams{
			RelayNodeID:       nodeID,
			GatewayInstanceID: fixture.gatewayID,
			GatewayAccountID:  fixture.accountIDs[3],
			AdminID:           fixture.adminID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if res2.Outcome != jobstore.RelayBindingOutcomeNodeConflict {
			t.Fatalf("expected node_conflict, got %s", res2.Outcome)
		}
	})

	t.Run("node not found returns node_not_found", func(t *testing.T) {
		nonExistentNode := uuid.New()
		res, err := repo.Bind(ctx, jobstore.BindParams{
			RelayNodeID:       nonExistentNode,
			GatewayInstanceID: fixture.gatewayID,
			GatewayAccountID:  fixture.accountIDs[0],
			AdminID:           fixture.adminID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Outcome != jobstore.RelayBindingOutcomeNodeNotFound {
			t.Fatalf("expected node_not_found, got %s", res.Outcome)
		}
	})

	t.Run("gateway not found returns gateway_not_found", func(t *testing.T) {
		nodeID := fixture.insertNode(t, ctx, database)
		nonExistentGateway := uuid.New()
		res, err := repo.Bind(ctx, jobstore.BindParams{
			RelayNodeID:       nodeID,
			GatewayInstanceID: nonExistentGateway,
			GatewayAccountID:  fixture.accountIDs[0],
			AdminID:           fixture.adminID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Outcome != jobstore.RelayBindingOutcomeGatewayNotFound {
			t.Fatalf("expected gateway_not_found, got %s", res.Outcome)
		}
	})

	t.Run("gateway exists without current directory returns directory_unavailable", func(t *testing.T) {
		nodeID := fixture.insertNode(t, ctx, database)
		// Insert gateway without current directory state
		emptyGatewayID := uuid.New()
		_, err := database.owner.Exec(ctx, `INSERT INTO gateway_instances(
			instance_id, display_name, probe_endpoint, probe_status,
			consecutive_successes, consecutive_failures, created_at, updated_at
		) VALUES ($1, 'empty-gw', 'https://empty-gw.test', 'healthy', 1, 0, clock_timestamp(), clock_timestamp())`,
			emptyGatewayID)
		if err != nil {
			t.Fatal(err)
		}

		res, err := repo.Bind(ctx, jobstore.BindParams{
			RelayNodeID:       nodeID,
			GatewayInstanceID: emptyGatewayID,
			GatewayAccountID:  fixture.accountIDs[0],
			AdminID:           fixture.adminID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Outcome != jobstore.RelayBindingOutcomeDirectoryUnavailable {
			t.Fatalf("expected directory_unavailable, got %s", res.Outcome)
		}
	})

	t.Run("account conflict when account already bound", func(t *testing.T) {
		node1 := fixture.insertNode(t, ctx, database)
		node2 := fixture.insertNode(t, ctx, database)
		var dbNow time.Time
		if err := database.owner.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
			t.Fatal(err)
		}
		insertGatewayDirectoryCurrentState(t, ctx, database, fixture.gatewayID, fixture.snapshotID, dbNow.Add(-5*time.Second))

		// First bind succeeds
		res1, err := repo.Bind(ctx, jobstore.BindParams{
			RelayNodeID:       node1,
			GatewayInstanceID: fixture.gatewayID,
			GatewayAccountID:  fixture.accountIDs[4],
			AdminID:           fixture.adminID,
		})
		if err != nil || res1.Outcome != jobstore.RelayBindingOutcomeSuccess {
			t.Fatalf("first bind failed: %v, %+v", err, res1)
		}

		// Second bind on same account fails with account_conflict
		res2, err := repo.Bind(ctx, jobstore.BindParams{
			RelayNodeID:       node2,
			GatewayInstanceID: fixture.gatewayID,
			GatewayAccountID:  fixture.accountIDs[4],
			AdminID:           fixture.adminID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if res2.Outcome != jobstore.RelayBindingOutcomeAccountConflict {
			t.Fatalf("expected account_conflict, got %s", res2.Outcome)
		}
	})
}

func TestRelayBindingRepository_Rebind(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	fixture := newRelayBindingSchemaFixture(t, ctx, database)

	repo, err := jobstore.NewRelayBindingRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}

	var dbNow time.Time
	if err := database.owner.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
		t.Fatal(err)
	}
	insertGatewayDirectoryCurrentState(t, ctx, database, fixture.gatewayID, fixture.snapshotID, dbNow.Add(-5*time.Second))

	t.Run("successful rebind with exact same ended_at and bound_at", func(t *testing.T) {
		nodeID := fixture.insertNode(t, ctx, database)
		account1 := fixture.accountIDs[0]
		account2 := fixture.accountIDs[1]

		// Bind first
		bindRes, err := repo.Bind(ctx, jobstore.BindParams{
			RelayNodeID:       nodeID,
			GatewayInstanceID: fixture.gatewayID,
			GatewayAccountID:  account1,
			AdminID:           fixture.adminID,
		})
		if err != nil || bindRes.Outcome != jobstore.RelayBindingOutcomeSuccess {
			t.Fatalf("bind failed: %v, %+v", err, bindRes)
		}

		requestID := "req-rebind-success-" + uuid.NewString()
		rebindRes, err := repo.Rebind(ctx, jobstore.RebindParams{
			RelayNodeID:          nodeID,
			NewGatewayInstanceID: fixture.gatewayID,
			NewGatewayAccountID:  account2,
			AdminID:              fixture.adminID,
			RequestID:            requestID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if rebindRes.Outcome != jobstore.RelayBindingOutcomeSuccess {
			t.Fatalf("expected outcome success, got %s", rebindRes.Outcome)
		}
		if rebindRes.PreviousBinding == nil || rebindRes.Binding == nil {
			t.Fatal("expected both previous and new binding to be present")
		}

		// Verify old.ended_at == new.bound_at == operation_at
		if rebindRes.PreviousBinding.EndedAt == nil {
			t.Fatal("expected previous binding EndedAt to be non-nil")
		}
		if *rebindRes.PreviousBinding.EndedAt != rebindRes.Binding.BoundAt {
			t.Fatalf("expected previous.EndedAt == new.BoundAt, got %v vs %v", *rebindRes.PreviousBinding.EndedAt, rebindRes.Binding.BoundAt)
		}
		if rebindRes.Binding.BoundAt != rebindRes.OperationAt {
			t.Fatalf("expected new.BoundAt == OperationAt, got %v vs %v", rebindRes.Binding.BoundAt, rebindRes.OperationAt)
		}
		if *rebindRes.PreviousBinding.EndReason != "administrator_rebind" {
			t.Fatalf("expected old end_reason administrator_rebind, got %s", *rebindRes.PreviousBinding.EndReason)
		}
		if rebindRes.Binding.BindReason != "administrator_rebind" {
			t.Fatalf("expected new bind_reason administrator_rebind, got %s", rebindRes.Binding.BindReason)
		}

		// Verify audit log
		var auditCount int
		var detailsRaw []byte
		var action string
		if err := database.owner.QueryRow(ctx, `SELECT count(*), details, action
			FROM audit_logs WHERE request_id = $1 GROUP BY details, action`, requestID).Scan(
			&auditCount, &detailsRaw, &action); err != nil {
			t.Fatal(err)
		}
		if auditCount != 1 || action != "relay_binding.rebind" {
			t.Fatalf("unexpected audit: count=%d action=%s", auditCount, action)
		}
		var details map[string]any
		if err := json.Unmarshal(detailsRaw, &details); err != nil {
			t.Fatal(err)
		}
		if int64(details["old_gateway_account_id"].(float64)) != account1 {
			t.Fatalf("expected old_gateway_account_id %d, got %v", account1, details["old_gateway_account_id"])
		}
		if int64(details["new_gateway_account_id"].(float64)) != account2 {
			t.Fatalf("expected new_gateway_account_id %d, got %v", account2, details["new_gateway_account_id"])
		}
		if details["reason_code"] != "administrator_rebind" {
			t.Fatalf("expected reason_code administrator_rebind, got %v", details["reason_code"])
		}
	})

	t.Run("rebind when node not bound returns no_current_binding", func(t *testing.T) {
		nodeID := fixture.insertNode(t, ctx, database)
		res, err := repo.Rebind(ctx, jobstore.RebindParams{
			RelayNodeID:          nodeID,
			NewGatewayInstanceID: fixture.gatewayID,
			NewGatewayAccountID:  fixture.accountIDs[2],
			AdminID:              fixture.adminID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Outcome != jobstore.RelayBindingOutcomeNoCurrentBinding {
			t.Fatalf("expected no_current_binding, got %s", res.Outcome)
		}
	})

	t.Run("rebind to account already bound to another node fails with account_conflict", func(t *testing.T) {
		node1 := fixture.insertNode(t, ctx, database)
		node2 := fixture.insertNode(t, ctx, database)
		account1 := fixture.accountIDs[3]
		account2 := fixture.accountIDs[4]

		// Bind node1 -> account1, node2 -> account2
		if res, err := repo.Bind(ctx, jobstore.BindParams{
			RelayNodeID:       node1,
			GatewayInstanceID: fixture.gatewayID,
			GatewayAccountID:  account1,
			AdminID:           fixture.adminID,
		}); err != nil || res.Outcome != jobstore.RelayBindingOutcomeSuccess {
			t.Fatalf("bind node1 failed: %v", err)
		}
		if res, err := repo.Bind(ctx, jobstore.BindParams{
			RelayNodeID:       node2,
			GatewayInstanceID: fixture.gatewayID,
			GatewayAccountID:  account2,
			AdminID:           fixture.adminID,
		}); err != nil || res.Outcome != jobstore.RelayBindingOutcomeSuccess {
			t.Fatalf("bind node2 failed: %v", err)
		}

		// Rebind node1 -> account2 (already bound to node2)
		res, err := repo.Rebind(ctx, jobstore.RebindParams{
			RelayNodeID:          node1,
			NewGatewayInstanceID: fixture.gatewayID,
			NewGatewayAccountID:  account2,
			AdminID:              fixture.adminID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Outcome != jobstore.RelayBindingOutcomeAccountConflict {
			t.Fatalf("expected account_conflict, got %s", res.Outcome)
		}

		// Verify node1 is still bound to account1
		currentBinding, err := repo.GetCurrentBindingByNode(ctx, node1)
		if err != nil || currentBinding == nil {
			t.Fatalf("failed to get current binding: %v", err)
		}
		if currentBinding.GatewayAccountID != account1 {
			t.Fatalf("node1 binding changed unexpectedly: %+v", currentBinding)
		}
	})

	t.Run("rebind fails closed when directory is stale", func(t *testing.T) {
		nodeID := fixture.insertNode(t, ctx, database)
		account1 := fixture.accountIDs[0]
		account2 := fixture.accountIDs[1]

		// Bind first while fresh
		if res, err := repo.Bind(ctx, jobstore.BindParams{
			RelayNodeID:       nodeID,
			GatewayInstanceID: fixture.gatewayID,
			GatewayAccountID:  account1,
			AdminID:           fixture.adminID,
		}); err != nil || res.Outcome != jobstore.RelayBindingOutcomeSuccess {
			t.Fatalf("bind failed: %v", err)
		}

		// Make directory stale
		var dbNow time.Time
		if err := database.owner.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
			t.Fatal(err)
		}
		insertGatewayDirectoryCurrentState(t, ctx, database, fixture.gatewayID, fixture.snapshotID, dbNow.Add(-600*time.Second))

		res, err := repo.Rebind(ctx, jobstore.RebindParams{
			RelayNodeID:          nodeID,
			NewGatewayInstanceID: fixture.gatewayID,
			NewGatewayAccountID:  account2,
			AdminID:              fixture.adminID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Outcome != jobstore.RelayBindingOutcomeDirectoryStale {
			t.Fatalf("expected directory_stale, got %s", res.Outcome)
		}

		// Verify node is still bound to account1 (open interval preserved)
		currentBinding, err := repo.GetCurrentBindingByNode(ctx, nodeID)
		if err != nil || currentBinding == nil {
			t.Fatalf("failed to get current binding: %v", err)
		}
		if currentBinding.GatewayAccountID != account1 {
			t.Fatalf("node binding changed unexpectedly: %+v", currentBinding)
		}
	})

	t.Run("rebind fails closed when target account missing from snapshot", func(t *testing.T) {
		nodeID := fixture.insertNode(t, ctx, database)
		account1 := fixture.accountIDs[0]

		var dbNow time.Time
		if err := database.owner.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
			t.Fatal(err)
		}
		insertGatewayDirectoryCurrentState(t, ctx, database, fixture.gatewayID, fixture.snapshotID, dbNow.Add(-5*time.Second))

		// Bind first
		if res, err := repo.Bind(ctx, jobstore.BindParams{
			RelayNodeID:       nodeID,
			GatewayInstanceID: fixture.gatewayID,
			GatewayAccountID:  account1,
			AdminID:           fixture.adminID,
		}); err != nil || res.Outcome != jobstore.RelayBindingOutcomeSuccess {
			t.Fatalf("bind failed: %v", err)
		}

		// Rebind to non-existent account
		res, err := repo.Rebind(ctx, jobstore.RebindParams{
			RelayNodeID:          nodeID,
			NewGatewayInstanceID: fixture.gatewayID,
			NewGatewayAccountID:  888888,
			AdminID:              fixture.adminID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Outcome != jobstore.RelayBindingOutcomeAccountNotFound {
			t.Fatalf("expected account_not_found, got %s", res.Outcome)
		}

		// Verify node is still bound to account1
		currentBinding, err := repo.GetCurrentBindingByNode(ctx, nodeID)
		if err != nil || currentBinding == nil {
			t.Fatalf("failed to get current binding: %v", err)
		}
		if currentBinding.GatewayAccountID != account1 {
			t.Fatalf("node binding changed unexpectedly: %+v", currentBinding)
		}
	})
}

func TestRelayBindingRepository_Unbind(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	fixture := newRelayBindingSchemaFixture(t, ctx, database)

	repo, err := jobstore.NewRelayBindingRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("successful unbind even when directory is stale", func(t *testing.T) {
		nodeID := fixture.insertNode(t, ctx, database)
		accountID := fixture.accountIDs[0]

		var dbNow time.Time
		if err := database.owner.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
			t.Fatal(err)
		}
		insertGatewayDirectoryCurrentState(t, ctx, database, fixture.gatewayID, fixture.snapshotID, dbNow.Add(-5*time.Second))

		// Bind first
		if res, err := repo.Bind(ctx, jobstore.BindParams{
			RelayNodeID:       nodeID,
			GatewayInstanceID: fixture.gatewayID,
			GatewayAccountID:  accountID,
			AdminID:           fixture.adminID,
		}); err != nil || res.Outcome != jobstore.RelayBindingOutcomeSuccess {
			t.Fatalf("bind failed: %v", err)
		}

		// Make directory stale
		insertGatewayDirectoryCurrentState(t, ctx, database, fixture.gatewayID, fixture.snapshotID, dbNow.Add(-600*time.Second))

		requestID := "req-unbind-success-" + uuid.NewString()
		res, err := repo.Unbind(ctx, jobstore.UnbindParams{
			RelayNodeID: nodeID,
			AdminID:     fixture.adminID,
			RequestID:   requestID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Outcome != jobstore.RelayBindingOutcomeSuccess {
			t.Fatalf("expected outcome success on stale unbind, got %s", res.Outcome)
		}
		if res.PreviousBinding == nil || res.PreviousBinding.EndedAt == nil {
			t.Fatal("expected previous closed binding")
		}
		if *res.PreviousBinding.EndReason != "administrator_unbind" {
			t.Fatalf("expected end_reason administrator_unbind, got %s", *res.PreviousBinding.EndReason)
		}

		// Verify audit log
		var auditCount int
		var detailsRaw []byte
		var action string
		if err := database.owner.QueryRow(ctx, `SELECT count(*), details, action
			FROM audit_logs WHERE request_id = $1 GROUP BY details, action`, requestID).Scan(
			&auditCount, &detailsRaw, &action); err != nil {
			t.Fatal(err)
		}
		if auditCount != 1 || action != "relay_binding.unbind" {
			t.Fatalf("unexpected audit: count=%d action=%s", auditCount, action)
		}
		var details map[string]any
		if err := json.Unmarshal(detailsRaw, &details); err != nil {
			t.Fatal(err)
		}
		if int64(details["old_gateway_account_id"].(float64)) != accountID {
			t.Fatalf("expected old_gateway_account_id %d, got %v", accountID, details["old_gateway_account_id"])
		}
		if details["new_gateway_account_id"] != nil {
			t.Fatalf("expected new_gateway_account_id null, got %v", details["new_gateway_account_id"])
		}
		if details["reason_code"] != "administrator_unbind" {
			t.Fatalf("expected reason_code administrator_unbind, got %v", details["reason_code"])
		}
	})

	t.Run("unbind when already unbound returns already_unbound without new audit", func(t *testing.T) {
		nodeID := fixture.insertNode(t, ctx, database)
		requestID := "req-unbind-already-" + uuid.NewString()
		res, err := repo.Unbind(ctx, jobstore.UnbindParams{
			RelayNodeID: nodeID,
			AdminID:     fixture.adminID,
			RequestID:   requestID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Outcome != jobstore.RelayBindingOutcomeAlreadyUnbound {
			t.Fatalf("expected already_unbound, got %s", res.Outcome)
		}

		// Ensure no audit log was created for this request
		var auditCount int
		if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE request_id = $1`, requestID).Scan(&auditCount); err != nil {
			t.Fatal(err)
		}
		if auditCount != 0 {
			t.Fatalf("expected 0 audit logs for already_unbound, got %d", auditCount)
		}
	})
}

func TestRelayBindingRepository_Concurrency(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	fixture := newRelayBindingSchemaFixture(t, ctx, database)

	repo, err := jobstore.NewRelayBindingRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}

	var dbNow time.Time
	if err := database.owner.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
		t.Fatal(err)
	}
	insertGatewayDirectoryCurrentState(t, ctx, database, fixture.gatewayID, fixture.snapshotID, dbNow.Add(-5*time.Second))

	t.Run("concurrent bind same node allows at most one success", func(t *testing.T) {
		nodeID := fixture.insertNode(t, ctx, database)
		account1 := fixture.accountIDs[0]
		account2 := fixture.accountIDs[1]

		var wg sync.WaitGroup
		results := make([]jobstore.RelayBindingResult, 2)
		errs := make([]error, 2)

		startBarrier := make(chan struct{})

		for i, acct := range []int64{account1, account2} {
			idx := i
			targetAcct := acct
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-startBarrier
				results[idx], errs[idx] = repo.Bind(ctx, jobstore.BindParams{
					RelayNodeID:       nodeID,
					GatewayInstanceID: fixture.gatewayID,
					GatewayAccountID:  targetAcct,
					AdminID:           fixture.adminID,
				})
			}()
		}

		close(startBarrier)
		wg.Wait()

		successCount := 0
		conflictCount := 0
		for i := 0; i < 2; i++ {
			if errs[i] != nil {
				t.Fatalf("unexpected error in goroutine %d: %v", i, errs[i])
			}
			if results[i].Outcome == jobstore.RelayBindingOutcomeSuccess {
				successCount++
			} else if results[i].Outcome == jobstore.RelayBindingOutcomeNodeConflict {
				conflictCount++
			}
		}

		if successCount != 1 || conflictCount != 1 {
			t.Fatalf("expected exactly 1 success and 1 node_conflict, got %d successes and %d conflicts", successCount, conflictCount)
		}
	})

	t.Run("concurrent bind same account allows at most one success", func(t *testing.T) {
		node1 := fixture.insertNode(t, ctx, database)
		node2 := fixture.insertNode(t, ctx, database)
		targetAccount := fixture.accountIDs[2]

		var wg sync.WaitGroup
		results := make([]jobstore.RelayBindingResult, 2)
		errs := make([]error, 2)

		startBarrier := make(chan struct{})

		for i, n := range []uuid.UUID{node1, node2} {
			idx := i
			nodeID := n
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-startBarrier
				results[idx], errs[idx] = repo.Bind(ctx, jobstore.BindParams{
					RelayNodeID:       nodeID,
					GatewayInstanceID: fixture.gatewayID,
					GatewayAccountID:  targetAccount,
					AdminID:           fixture.adminID,
				})
			}()
		}

		close(startBarrier)
		wg.Wait()

		successCount := 0
		conflictCount := 0
		for i := 0; i < 2; i++ {
			if errs[i] != nil {
				t.Fatalf("unexpected error in goroutine %d: %v", i, errs[i])
			}
			if results[i].Outcome == jobstore.RelayBindingOutcomeSuccess {
				successCount++
			} else if results[i].Outcome == jobstore.RelayBindingOutcomeAccountConflict {
				conflictCount++
			}
		}

		if successCount != 1 || conflictCount != 1 {
			t.Fatalf("expected exactly 1 success and 1 account_conflict, got %d successes and %d conflicts", successCount, conflictCount)
		}
	})

	t.Run("concurrent mutual account swap between two bound nodes avoids deadlock and double binding", func(t *testing.T) {
		node1 := fixture.insertNode(t, ctx, database)
		node2 := fixture.insertNode(t, ctx, database)
		account1 := fixture.accountIDs[3]
		account2 := fixture.accountIDs[4]

		// node1 -> account1, node2 -> account2
		if res, err := repo.Bind(ctx, jobstore.BindParams{
			RelayNodeID:       node1,
			GatewayInstanceID: fixture.gatewayID,
			GatewayAccountID:  account1,
			AdminID:           fixture.adminID,
		}); err != nil || res.Outcome != jobstore.RelayBindingOutcomeSuccess {
			t.Fatalf("bind node1 failed: %v", err)
		}
		if res, err := repo.Bind(ctx, jobstore.BindParams{
			RelayNodeID:       node2,
			GatewayInstanceID: fixture.gatewayID,
			GatewayAccountID:  account2,
			AdminID:           fixture.adminID,
		}); err != nil || res.Outcome != jobstore.RelayBindingOutcomeSuccess {
			t.Fatalf("bind node2 failed: %v", err)
		}

		// Concurrently: node1 rebinds to account2 (held by node2), node2 rebinds to account1 (held by node1)
		var wg sync.WaitGroup
		results := make([]jobstore.RelayBindingResult, 2)
		errs := make([]error, 2)
		startBarrier := make(chan struct{})

		wg.Add(2)
		go func() {
			defer wg.Done()
			<-startBarrier
			results[0], errs[0] = repo.Rebind(ctx, jobstore.RebindParams{
				RelayNodeID:          node1,
				NewGatewayInstanceID: fixture.gatewayID,
				NewGatewayAccountID:  account2,
				AdminID:              fixture.adminID,
			})
		}()
		go func() {
			defer wg.Done()
			<-startBarrier
			results[1], errs[1] = repo.Rebind(ctx, jobstore.RebindParams{
				RelayNodeID:          node2,
				NewGatewayInstanceID: fixture.gatewayID,
				NewGatewayAccountID:  account1,
				AdminID:              fixture.adminID,
			})
		}()

		close(startBarrier)
		wg.Wait()

		for i := 0; i < 2; i++ {
			if errs[i] != nil {
				t.Fatalf("unexpected raw/deadlock error in goroutine %d: %v", i, errs[i])
			}
			// Each should either fail with account_conflict (since target account is currently bound)
			if results[i].Outcome != jobstore.RelayBindingOutcomeAccountConflict {
				t.Fatalf("expected account_conflict for swap, got %s", results[i].Outcome)
			}
		}

		// Verify original bindings remain intact
		b1, err := repo.GetCurrentBindingByNode(ctx, node1)
		if err != nil || b1 == nil || b1.GatewayAccountID != account1 {
			t.Fatalf("node1 binding corrupted: %+v, err: %v", b1, err)
		}
		b2, err := repo.GetCurrentBindingByNode(ctx, node2)
		if err != nil || b2 == nil || b2.GatewayAccountID != account2 {
			t.Fatalf("node2 binding corrupted: %+v, err: %v", b2, err)
		}
	})

	t.Run("audit log insert failure rolls back bind and rebind completely", func(t *testing.T) {
		nodeID := fixture.insertNode(t, ctx, database)
		account1 := fixture.accountIDs[0]
		account2 := fixture.accountIDs[1]

		canaryRequestID := "req-audit-fail-canary-" + uuid.NewString()

		// Install test-only trigger on audit_logs that fails with 55000 when request_id matches canary
		_, err := database.owner.Exec(ctx, `
			CREATE OR REPLACE FUNCTION test_fail_canary_audit_insert()
			RETURNS trigger AS $$
			BEGIN
				IF NEW.request_id = $1 THEN
					RAISE EXCEPTION 'injected test audit failure' USING ERRCODE = '55000';
				END IF;
				RETURN NEW;
			END;
			$$ LANGUAGE plpgsql;

			CREATE TRIGGER trg_test_fail_canary_audit_insert
			BEFORE INSERT ON audit_logs
			FOR EACH ROW
			EXECUTE FUNCTION test_fail_canary_audit_insert();
		`, canaryRequestID)
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() {
			_, _ = database.owner.Exec(context.Background(), `
				DROP TRIGGER IF EXISTS trg_test_fail_canary_audit_insert ON audit_logs;
				DROP FUNCTION IF EXISTS test_fail_canary_audit_insert();
			`)
		})

		// 1. Bind with canary request_id: audit insert fails with 55000 -> full rollback
		_, err = repo.Bind(ctx, jobstore.BindParams{
			RelayNodeID:       nodeID,
			GatewayInstanceID: fixture.gatewayID,
			GatewayAccountID:  account1,
			AdminID:           fixture.adminID,
			RequestID:         canaryRequestID,
		})
		if err == nil {
			t.Fatal("expected error on canary audit failure, got nil")
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "55000" {
			t.Fatalf("expected SQLSTATE 55000 from audit trigger, got: %v", err)
		}

		// Verify binding row does NOT exist
		current, err := repo.GetCurrentBindingByNode(ctx, nodeID)
		if err != nil {
			t.Fatal(err)
		}
		if current != nil {
			t.Fatalf("expected binding to be rolled back, found: %+v", current)
		}

		// Verify no audit log exists for canary request_id
		var auditCount int
		if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE request_id = $1`, canaryRequestID).Scan(&auditCount); err != nil {
			t.Fatal(err)
		}
		if auditCount != 0 {
			t.Fatalf("expected 0 audit logs for canary, got %d", auditCount)
		}

		// 2. Now perform a legitimate bind
		normalRequestID := "req-legit-bind-" + uuid.NewString()
		bindRes, err := repo.Bind(ctx, jobstore.BindParams{
			RelayNodeID:       nodeID,
			GatewayInstanceID: fixture.gatewayID,
			GatewayAccountID:  account1,
			AdminID:           fixture.adminID,
			RequestID:         normalRequestID,
		})
		if err != nil || bindRes.Outcome != jobstore.RelayBindingOutcomeSuccess {
			t.Fatalf("legitimate bind failed: %v, %+v", err, bindRes)
		}

		// 3. Rebind with canary request_id: audit insert fails with 55000 -> full rollback
		rebindCanaryID := "req-rebind-canary-" + uuid.NewString()
		// Update canary trigger function or check prefix
		_, err = database.owner.Exec(ctx, `
			CREATE OR REPLACE FUNCTION test_fail_canary_audit_insert()
			RETURNS trigger AS $$
			BEGIN
				IF NEW.request_id = $1 OR NEW.request_id = $2 THEN
					RAISE EXCEPTION 'injected test audit failure' USING ERRCODE = '55000';
				END IF;
				RETURN NEW;
			END;
			$$ LANGUAGE plpgsql;
		`, canaryRequestID, rebindCanaryID)
		if err != nil {
			t.Fatal(err)
		}

		_, err = repo.Rebind(ctx, jobstore.RebindParams{
			RelayNodeID:          nodeID,
			NewGatewayInstanceID: fixture.gatewayID,
			NewGatewayAccountID:  account2,
			AdminID:              fixture.adminID,
			RequestID:            rebindCanaryID,
		})
		if err == nil {
			t.Fatal("expected error on canary rebind audit failure, got nil")
		}
		if !errors.As(err, &pgErr) || pgErr.Code != "55000" {
			t.Fatalf("expected SQLSTATE 55000 on rebind from audit trigger, got: %v", err)
		}

		// Verify previous binding remains open, current, and unchanged
		current, err = repo.GetCurrentBindingByNode(ctx, nodeID)
		if err != nil || current == nil {
			t.Fatalf("failed to retrieve binding after rebind rollback: %v", err)
		}
		if current.GatewayAccountID != account1 || current.EndedAt != nil {
			t.Fatalf("expected original interval to remain open with account %d, got: %+v", account1, current)
		}

		// Verify target new binding (account2) does NOT exist
		acct2Binding, err := repo.GetCurrentBindingByAccount(ctx, fixture.gatewayID, account2)
		if err != nil {
			t.Fatal(err)
		}
		if acct2Binding != nil {
			t.Fatalf("expected no binding for account2, found: %+v", acct2Binding)
		}

		// Verify no audit log exists for rebind canary
		if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE request_id = $1`, rebindCanaryID).Scan(&auditCount); err != nil {
			t.Fatal(err)
		}
		if auditCount != 0 {
			t.Fatalf("expected 0 audit logs for rebind canary, got %d", auditCount)
		}
	})
}

