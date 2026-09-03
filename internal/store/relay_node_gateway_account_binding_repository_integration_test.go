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

func TestRelayBindingRepository_ReadModel(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	fixture := newRelayBindingSchemaFixture(t, ctx, database)

	repo, err := jobstore.NewRelayBindingRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("node-centric read: unbound node", func(t *testing.T) {
		nodeID := fixture.insertNode(t, ctx, database)
		view, err := repo.GetNodeCentricBindingView(ctx, nodeID)
		if err != nil {
			t.Fatal(err)
		}
		if view.RelayNodeID != nodeID {
			t.Fatalf("unexpected nodeID: %s", view.RelayNodeID)
		}
		if view.Resolution != jobstore.RelayBindingResolutionUnbound {
			t.Fatalf("expected unbound, got %s", view.Resolution)
		}
		if view.ContextSource != jobstore.AccountContextSourceNone {
			t.Fatalf("expected context source none, got %s", view.ContextSource)
		}
		if view.CurrentBinding != nil {
			t.Fatal("expected nil current binding")
		}
	})

	t.Run("node-centric read: resolved node with fresh current context", func(t *testing.T) {
		nodeID := fixture.insertNode(t, ctx, database)
		var dbNow time.Time
		if err := database.owner.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
			t.Fatal(err)
		}
		insertGatewayDirectoryCurrentState(t, ctx, database, fixture.gatewayID, fixture.snapshotID, dbNow.Add(-5*time.Second))

		accountID := fixture.accountIDs[0]
		bindRes, err := repo.Bind(ctx, jobstore.BindParams{
			RelayNodeID:       nodeID,
			GatewayInstanceID: fixture.gatewayID,
			GatewayAccountID:  accountID,
			AdminID:           fixture.adminID,
		})
		if err != nil || bindRes.Outcome != jobstore.RelayBindingOutcomeSuccess {
			t.Fatalf("bind failed: %v", err)
		}

		view, err := repo.GetNodeCentricBindingView(ctx, nodeID)
		if err != nil {
			t.Fatal(err)
		}
		if view.Resolution != jobstore.RelayBindingResolutionResolved {
			t.Fatalf("expected resolved, got %s", view.Resolution)
		}
		if view.DirectoryFreshness != jobstore.DirectoryFreshnessFresh {
			t.Fatalf("expected fresh, got %s", view.DirectoryFreshness)
		}
		if view.ContextSource != jobstore.AccountContextSourceCurrent {
			t.Fatalf("expected current context source, got %s", view.ContextSource)
		}
		if view.AccountContext == nil || view.AccountContext.AccountID != accountID {
			t.Fatalf("expected account context for %d, got %+v", accountID, view.AccountContext)
		}
		if view.CurrentBinding == nil || view.CurrentBinding.GatewayAccountID != accountID {
			t.Fatalf("unexpected current binding: %+v", view.CurrentBinding)
		}
	})

	t.Run("node-centric read: unknown when directory is stale (distinguishes last-known context)", func(t *testing.T) {
		nodeID := fixture.insertNode(t, ctx, database)
		var dbNow time.Time
		if err := database.owner.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
			t.Fatal(err)
		}
		// Fresh initially
		insertGatewayDirectoryCurrentState(t, ctx, database, fixture.gatewayID, fixture.snapshotID, dbNow.Add(-5*time.Second))

		accountID := fixture.accountIDs[0]
		if res, err := repo.Bind(ctx, jobstore.BindParams{
			RelayNodeID:       nodeID,
			GatewayInstanceID: fixture.gatewayID,
			GatewayAccountID:  accountID,
			AdminID:           fixture.adminID,
		}); err != nil || res.Outcome != jobstore.RelayBindingOutcomeSuccess {
			t.Fatalf("bind failed: %v", err)
		}

		// Make directory stale (> 540s)
		insertGatewayDirectoryCurrentState(t, ctx, database, fixture.gatewayID, fixture.snapshotID, dbNow.Add(-600*time.Second))

		view, err := repo.GetNodeCentricBindingView(ctx, nodeID)
		if err != nil {
			t.Fatal(err)
		}
		if view.Resolution != jobstore.RelayBindingResolutionUnknown {
			t.Fatalf("expected unknown for stale directory, got %s", view.Resolution)
		}
		if view.DirectoryFreshness != jobstore.DirectoryFreshnessStale {
			t.Fatalf("expected stale freshness, got %s", view.DirectoryFreshness)
		}
		if view.ContextSource != jobstore.AccountContextSourceLastKnown {
			t.Fatalf("expected last_known context source, got %s", view.ContextSource)
		}
		if view.AccountContext == nil || view.AccountContext.AccountID != accountID {
			t.Fatalf("expected last-known account context for %d, got %+v", accountID, view.AccountContext)
		}
	})

	t.Run("node-centric read: unresolved when account disappears from fresh snapshot", func(t *testing.T) {
		nodeID := fixture.insertNode(t, ctx, database)
		var dbNow time.Time
		if err := database.owner.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
			t.Fatal(err)
		}
		insertGatewayDirectoryCurrentState(t, ctx, database, fixture.gatewayID, fixture.snapshotID, dbNow.Add(-5*time.Second))

		accountID := fixture.accountIDs[0]
		if res, err := repo.Bind(ctx, jobstore.BindParams{
			RelayNodeID:       nodeID,
			GatewayInstanceID: fixture.gatewayID,
			GatewayAccountID:  accountID,
			AdminID:           fixture.adminID,
		}); err != nil || res.Outcome != jobstore.RelayBindingOutcomeSuccess {
			t.Fatalf("bind failed: %v", err)
		}

		// Create snapshot 2 that does NOT contain accountID
		snapshot2ID := uuid.New()
		fingerprint2 := make([]byte, 32)
		fingerprint2[0] = 0x02
		_, err = database.owner.Exec(ctx, `INSERT INTO gateway_directory_snapshots(
			snapshot_id, gateway_instance_id, fingerprint, schema_version, account_count, created_at
		) VALUES ($1, $2, $3, 1, 1, clock_timestamp())`, snapshot2ID, fixture.gatewayID, fingerprint2)
		if err != nil {
			t.Fatal(err)
		}
		// Insert item 888888 into snapshot2
		_, err = database.owner.Exec(ctx, `INSERT INTO gateway_directory_snapshot_items(
			snapshot_id, account_id, name, platform, type, url, status
		) VALUES ($1, 888888, 'other-acct', 'linux', 'apikey', 'https://other.test', 'active')`, snapshot2ID)
		if err != nil {
			t.Fatal(err)
		}

		// Update current state to snapshot2 (fresh)
		insertGatewayDirectoryCurrentState(t, ctx, database, fixture.gatewayID, snapshot2ID, dbNow.Add(-10*time.Second))

		view, err := repo.GetNodeCentricBindingView(ctx, nodeID)
		if err != nil {
			t.Fatal(err)
		}
		if view.Resolution != jobstore.RelayBindingResolutionUnresolved {
			t.Fatalf("expected unresolved, got %s", view.Resolution)
		}
		if view.DirectoryFreshness != jobstore.DirectoryFreshnessFresh {
			t.Fatalf("expected fresh directory freshness, got %s", view.DirectoryFreshness)
		}
		if view.ContextSource != jobstore.AccountContextSourceNone {
			t.Fatalf("expected context source none for missing account in fresh directory, got %s", view.ContextSource)
		}
		if view.AccountContext != nil {
			t.Fatalf("expected nil account context, got %+v", view.AccountContext)
		}
	})

	t.Run("fresh empty directory: all existing bindings become unresolved", func(t *testing.T) {
		nodeID := fixture.insertNode(t, ctx, database)
		var dbNow time.Time
		if err := database.owner.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
			t.Fatal(err)
		}
		insertGatewayDirectoryCurrentState(t, ctx, database, fixture.gatewayID, fixture.snapshotID, dbNow.Add(-5*time.Second))

		accountID := fixture.accountIDs[0]
		if res, err := repo.Bind(ctx, jobstore.BindParams{
			RelayNodeID:       nodeID,
			GatewayInstanceID: fixture.gatewayID,
			GatewayAccountID:  accountID,
			AdminID:           fixture.adminID,
		}); err != nil || res.Outcome != jobstore.RelayBindingOutcomeSuccess {
			t.Fatalf("bind failed: %v", err)
		}

		// Create empty snapshot
		emptySnapshotID := uuid.New()
		emptyFingerprint := make([]byte, 32)
		emptyFingerprint[0] = 0xee
		_, err = database.owner.Exec(ctx, `INSERT INTO gateway_directory_snapshots(
			snapshot_id, gateway_instance_id, fingerprint, schema_version, account_count, created_at
		) VALUES ($1, $2, $3, 1, 0, clock_timestamp())`, emptySnapshotID, fixture.gatewayID, emptyFingerprint)
		if err != nil {
			t.Fatal(err)
		}

		// Update current state to empty snapshot (fresh)
		insertGatewayDirectoryCurrentState(t, ctx, database, fixture.gatewayID, emptySnapshotID, dbNow.Add(-5*time.Second))

		view, err := repo.GetNodeCentricBindingView(ctx, nodeID)
		if err != nil {
			t.Fatal(err)
		}
		if view.Resolution != jobstore.RelayBindingResolutionUnresolved {
			t.Fatalf("expected unresolved on empty fresh directory, got %s", view.Resolution)
		}
		if view.DirectoryFreshness != jobstore.DirectoryFreshnessFresh {
			t.Fatalf("expected fresh directory, got %s", view.DirectoryFreshness)
		}
	})

	t.Run("account-centric read and unresolved listings", func(t *testing.T) {
		node1 := fixture.insertNode(t, ctx, database)
		node2 := fixture.insertNode(t, ctx, database)
		account1 := fixture.accountIDs[0]
		account2 := fixture.accountIDs[1]
		account3 := fixture.accountIDs[2] // unbound

		var dbNow time.Time
		if err := database.owner.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
			t.Fatal(err)
		}
		insertGatewayDirectoryCurrentState(t, ctx, database, fixture.gatewayID, fixture.snapshotID, dbNow.Add(-5*time.Second))

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

		// List gateway accounts via envelope view
		gwView, err := repo.GetGatewayAccountCentricBindingView(ctx, fixture.gatewayID)
		if err != nil {
			t.Fatal(err)
		}
		if gwView.DirectoryFreshness != jobstore.DirectoryFreshnessFresh {
			t.Fatalf("expected fresh directory, got %s", gwView.DirectoryFreshness)
		}
		if gwView.CurrentSnapshotID == nil || *gwView.CurrentSnapshotID != fixture.snapshotID {
			t.Fatalf("expected snapshot %s, got %v", fixture.snapshotID, gwView.CurrentSnapshotID)
		}
		if gwView.LastSuccessObservationAt == nil {
			t.Fatal("expected non-nil last success observation")
		}
		if len(gwView.Accounts) < 3 {
			t.Fatalf("expected at least 3 account views, got %d", len(gwView.Accounts))
		}

		// Check account1: bound to node1, resolved
		var found1, found2, found3 bool
		for _, av := range gwView.Accounts {
			if av.GatewayAccountID == account1 {
				found1 = true
				if av.BoundRelayNodeID == nil || *av.BoundRelayNodeID != node1 {
					t.Fatalf("account1 bound to wrong node: %+v", av.BoundRelayNodeID)
				}
				if av.Resolution != jobstore.RelayBindingResolutionResolved {
					t.Fatalf("account1 resolution expected resolved, got %s", av.Resolution)
				}
				if av.ContextSource != jobstore.AccountContextSourceCurrent {
					t.Fatalf("expected current context source, got %s", av.ContextSource)
				}
			}
			if av.GatewayAccountID == account2 {
				found2 = true
				if av.BoundRelayNodeID == nil || *av.BoundRelayNodeID != node2 {
					t.Fatalf("account2 bound to wrong node: %+v", av.BoundRelayNodeID)
				}
				if av.Resolution != jobstore.RelayBindingResolutionResolved {
					t.Fatalf("account2 resolution expected resolved, got %s", av.Resolution)
				}
				if av.ContextSource != jobstore.AccountContextSourceCurrent {
					t.Fatalf("expected current context source, got %s", av.ContextSource)
				}
			}
			if av.GatewayAccountID == account3 {
				found3 = true
				if av.BoundRelayNodeID != nil {
					t.Fatalf("account3 expected unbound, got bound to: %v", av.BoundRelayNodeID)
				}
				if av.Resolution != jobstore.RelayBindingResolutionUnbound {
					t.Fatalf("account3 resolution expected unbound, got %s", av.Resolution)
				}
				if av.ContextSource != jobstore.AccountContextSourceCurrent {
					t.Fatalf("expected current context source for unbound in fresh directory, got %s", av.ContextSource)
				}
			}
		}
		if !found1 || !found2 || !found3 {
			t.Fatalf("did not find expected accounts: %v, %v, %v", found1, found2, found3)
		}

		// Unresolved list should be empty when all accounts are in snapshot
		unresolvedList, err := repo.ListUnresolvedGatewayAccountBindings(ctx, fixture.gatewayID)
		if err != nil {
			t.Fatal(err)
		}
		if len(unresolvedList) != 0 {
			t.Fatalf("expected 0 unresolved bindings, got %d", len(unresolvedList))
		}

		// Now switch current state to snapshot that contains only account1
		partialSnapshotID := uuid.New()
		partialFingerprint := make([]byte, 32)
		partialFingerprint[0] = 0x33
		_, err = database.owner.Exec(ctx, `INSERT INTO gateway_directory_snapshots(
			snapshot_id, gateway_instance_id, fingerprint, schema_version, account_count, created_at
		) VALUES ($1, $2, $3, 1, 1, clock_timestamp())`, partialSnapshotID, fixture.gatewayID, partialFingerprint)
		if err != nil {
			t.Fatal(err)
		}
		_, err = database.owner.Exec(ctx, `INSERT INTO gateway_directory_snapshot_items(
			snapshot_id, account_id, name, platform, type, url, status
		) VALUES ($1, $2, 'acct1', 'linux', 'apikey', 'https://acct1.test', 'active')`, partialSnapshotID, account1)
		if err != nil {
			t.Fatal(err)
		}

		insertGatewayDirectoryCurrentState(t, ctx, database, fixture.gatewayID, partialSnapshotID, dbNow.Add(-5*time.Second))

		// Now node2 (bound to account2) is unresolved
		unresolvedList, err = repo.ListUnresolvedGatewayAccountBindings(ctx, fixture.gatewayID)
		if err != nil {
			t.Fatal(err)
		}
		if len(unresolvedList) != 1 {
			t.Fatalf("expected 1 unresolved binding, got %d", len(unresolvedList))
		}
		if unresolvedList[0].CurrentBinding.RelayNodeID != node2 || unresolvedList[0].CurrentBinding.GatewayAccountID != account2 {
			t.Fatalf("unexpected unresolved binding item: %+v", unresolvedList[0])
		}
		if unresolvedList[0].Resolution != jobstore.RelayBindingResolutionUnresolved {
			t.Fatalf("expected unresolved resolution, got %s", unresolvedList[0].Resolution)
		}
		if unresolvedList[0].LastKnownAccountContext == nil || unresolvedList[0].LastKnownAccountContext.AccountID != account2 {
			t.Fatalf("expected last-known context for account2, got %+v", unresolvedList[0].LastKnownAccountContext)
		}
	})

	t.Run("lifecycle transitions: reappear / new ID with same metadata / A->B->A reuse / stale->recovery", func(t *testing.T) {
		nodeID := fixture.insertNode(t, ctx, database)
		targetAccountID := fixture.accountIDs[0]
		var dbNow time.Time
		if err := database.owner.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
			t.Fatal(err)
		}

		// Step 1: Fresh snapshot A containing targetAccountID -> bind -> resolved
		insertGatewayDirectoryCurrentState(t, ctx, database, fixture.gatewayID, fixture.snapshotID, dbNow.Add(-5*time.Second))
		if res, err := repo.Bind(ctx, jobstore.BindParams{
			RelayNodeID:       nodeID,
			GatewayInstanceID: fixture.gatewayID,
			GatewayAccountID:  targetAccountID,
			AdminID:           fixture.adminID,
		}); err != nil || res.Outcome != jobstore.RelayBindingOutcomeSuccess {
			t.Fatalf("bind failed: %v", err)
		}

		view1, err := repo.GetNodeCentricBindingView(ctx, nodeID)
		if err != nil || view1.Resolution != jobstore.RelayBindingResolutionResolved {
			t.Fatalf("step 1 failed: %v, %+v", err, view1)
		}

		// Step 2: Switch to Snapshot B where targetAccountID disappeared (and new ID 777777 has identical name/url)
		snapshotBID := uuid.New()
		fingerprintB := make([]byte, 32)
		fingerprintB[0] = 0xbb
		_, err = database.owner.Exec(ctx, `INSERT INTO gateway_directory_snapshots(
			snapshot_id, gateway_instance_id, fingerprint, schema_version, account_count, created_at
		) VALUES ($1, $2, $3, 1, 1, clock_timestamp())`, snapshotBID, fixture.gatewayID, fingerprintB)
		if err != nil {
			t.Fatal(err)
		}
		// Insert new ID with same name "account-0" as targetAccountID
		_, err = database.owner.Exec(ctx, `INSERT INTO gateway_directory_snapshot_items(
			snapshot_id, account_id, name, platform, type, url, status
		) VALUES ($1, 777777, 'account-0', 'linux', 'apikey', 'https://account-0.test', 'active')`, snapshotBID)
		if err != nil {
			t.Fatal(err)
		}

		insertGatewayDirectoryCurrentState(t, ctx, database, fixture.gatewayID, snapshotBID, dbNow.Add(-5*time.Second))

		view2, err := repo.GetNodeCentricBindingView(ctx, nodeID)
		if err != nil {
			t.Fatal(err)
		}
		// Resolution must be unresolved (not matched to 777777 despite same metadata)
		if view2.Resolution != jobstore.RelayBindingResolutionUnresolved {
			t.Fatalf("step 2: expected unresolved, got %s", view2.Resolution)
		}
		if view2.CurrentBinding.GatewayAccountID != targetAccountID {
			t.Fatalf("step 2: binding target changed unexpectedly: %d", view2.CurrentBinding.GatewayAccountID)
		}

		// Step 3: A->B->A snapshot reuse: switch back to original snapshotID (Snapshot A)
		insertGatewayDirectoryCurrentState(t, ctx, database, fixture.gatewayID, fixture.snapshotID, dbNow.Add(-5*time.Second))

		view3, err := repo.GetNodeCentricBindingView(ctx, nodeID)
		if err != nil {
			t.Fatal(err)
		}
		// Automatically restored to resolved!
		if view3.Resolution != jobstore.RelayBindingResolutionResolved {
			t.Fatalf("step 3: expected auto-recovery to resolved, got %s", view3.Resolution)
		}
		if view3.ContextSource != jobstore.AccountContextSourceCurrent {
			t.Fatalf("step 3: expected current context source, got %s", view3.ContextSource)
		}

		// Step 4: Stale -> Recovery
		// Make snapshot stale
		insertGatewayDirectoryCurrentState(t, ctx, database, fixture.gatewayID, fixture.snapshotID, dbNow.Add(-600*time.Second))
		viewStale, err := repo.GetNodeCentricBindingView(ctx, nodeID)
		if err != nil || viewStale.Resolution != jobstore.RelayBindingResolutionUnknown {
			t.Fatalf("step 4 stale failed: %v, %+v", err, viewStale)
		}

		// Ingestion runs and recovers freshness
		insertGatewayDirectoryCurrentState(t, ctx, database, fixture.gatewayID, fixture.snapshotID, dbNow.Add(-2*time.Second))
		viewRecovered, err := repo.GetNodeCentricBindingView(ctx, nodeID)
		if err != nil || viewRecovered.Resolution != jobstore.RelayBindingResolutionResolved {
			t.Fatalf("step 4 recovery failed: %v, %+v", err, viewRecovered)
		}
	})

	t.Run("regression A: account-centric fresh empty directory", func(t *testing.T) {
		emptyGatewayID := uuid.New()
		_, err := database.owner.Exec(ctx, `INSERT INTO gateway_instances(
			instance_id, display_name, probe_endpoint, probe_status,
			consecutive_successes, consecutive_failures, created_at, updated_at
		) VALUES ($1, 'gw-fresh-empty', 'https://gw-fresh-empty.test', 'healthy', 1, 0, clock_timestamp(), clock_timestamp())`,
			emptyGatewayID)
		if err != nil {
			t.Fatal(err)
		}

		emptySnapshotID := uuid.New()
		emptyFingerprint := make([]byte, 32)
		emptyFingerprint[0] = 0xfa
		_, err = database.owner.Exec(ctx, `INSERT INTO gateway_directory_snapshots(
			snapshot_id, gateway_instance_id, fingerprint, schema_version, account_count, created_at
		) VALUES ($1, $2, $3, 1, 0, clock_timestamp())`, emptySnapshotID, emptyGatewayID, emptyFingerprint)
		if err != nil {
			t.Fatal(err)
		}

		var dbNow time.Time
		if err := database.owner.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
			t.Fatal(err)
		}
		insertGatewayDirectoryCurrentState(t, ctx, database, emptyGatewayID, emptySnapshotID, dbNow.Add(-5*time.Second))

		view, err := repo.GetGatewayAccountCentricBindingView(ctx, emptyGatewayID)
		if err != nil {
			t.Fatal(err)
		}
		if view.DirectoryFreshness != jobstore.DirectoryFreshnessFresh {
			t.Fatalf("expected fresh freshness, got %s", view.DirectoryFreshness)
		}
		if view.CurrentSnapshotID == nil || *view.CurrentSnapshotID != emptySnapshotID {
			t.Fatalf("expected current snapshot %s, got %v", emptySnapshotID, view.CurrentSnapshotID)
		}
		if view.LastSuccessObservationAt == nil {
			t.Fatal("expected non-nil last success observation")
		}
		if len(view.Accounts) != 0 {
			t.Fatalf("expected 0 accounts, got %d", len(view.Accounts))
		}
	})

	t.Run("regression B: account-centric no directory / unavailable", func(t *testing.T) {
		noDirGatewayID := uuid.New()
		_, err := database.owner.Exec(ctx, `INSERT INTO gateway_instances(
			instance_id, display_name, probe_endpoint, probe_status,
			consecutive_successes, consecutive_failures, created_at, updated_at
		) VALUES ($1, 'gw-no-dir', 'https://gw-no-dir.test', 'healthy', 1, 0, clock_timestamp(), clock_timestamp())`,
			noDirGatewayID)
		if err != nil {
			t.Fatal(err)
		}

		view, err := repo.GetGatewayAccountCentricBindingView(ctx, noDirGatewayID)
		if err != nil {
			t.Fatal(err)
		}
		if view.DirectoryFreshness != jobstore.DirectoryFreshnessUnavailable {
			t.Fatalf("expected unavailable freshness, got %s", view.DirectoryFreshness)
		}
		if view.CurrentSnapshotID != nil {
			t.Fatalf("expected nil current snapshot, got %v", view.CurrentSnapshotID)
		}
		if view.LastSuccessObservationAt != nil {
			t.Fatalf("expected nil last success observation, got %v", view.LastSuccessObservationAt)
		}
		if len(view.Accounts) != 0 {
			t.Fatalf("expected 0 accounts, got %d", len(view.Accounts))
		}
	})

	t.Run("regression C: account-centric stale empty directory", func(t *testing.T) {
		staleEmptyGatewayID := uuid.New()
		_, err := database.owner.Exec(ctx, `INSERT INTO gateway_instances(
			instance_id, display_name, probe_endpoint, probe_status,
			consecutive_successes, consecutive_failures, created_at, updated_at
		) VALUES ($1, 'gw-stale-empty', 'https://gw-stale-empty.test', 'healthy', 1, 0, clock_timestamp(), clock_timestamp())`,
			staleEmptyGatewayID)
		if err != nil {
			t.Fatal(err)
		}

		staleEmptySnapshotID := uuid.New()
		staleEmptyFingerprint := make([]byte, 32)
		staleEmptyFingerprint[0] = 0xfb
		_, err = database.owner.Exec(ctx, `INSERT INTO gateway_directory_snapshots(
			snapshot_id, gateway_instance_id, fingerprint, schema_version, account_count, created_at
		) VALUES ($1, $2, $3, 1, 0, clock_timestamp())`, staleEmptySnapshotID, staleEmptyGatewayID, staleEmptyFingerprint)
		if err != nil {
			t.Fatal(err)
		}

		var dbNow time.Time
		if err := database.owner.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
			t.Fatal(err)
		}
		insertGatewayDirectoryCurrentState(t, ctx, database, staleEmptyGatewayID, staleEmptySnapshotID, dbNow.Add(-600*time.Second))

		view, err := repo.GetGatewayAccountCentricBindingView(ctx, staleEmptyGatewayID)
		if err != nil {
			t.Fatal(err)
		}
		if view.DirectoryFreshness != jobstore.DirectoryFreshnessStale {
			t.Fatalf("expected stale freshness, got %s", view.DirectoryFreshness)
		}
		if view.CurrentSnapshotID == nil || *view.CurrentSnapshotID != staleEmptySnapshotID {
			t.Fatalf("expected current snapshot %s, got %v", staleEmptySnapshotID, view.CurrentSnapshotID)
		}
		if view.LastSuccessObservationAt == nil {
			t.Fatal("expected non-nil last success observation")
		}
		if len(view.Accounts) != 0 {
			t.Fatalf("expected 0 accounts, got %d", len(view.Accounts))
		}
	})

	t.Run("regression D: node-centric latest last-known context from current snapshot instead of old evidence snapshot", func(t *testing.T) {
		nodeID := fixture.insertNode(t, ctx, database)
		targetAccountID := fixture.accountIDs[0]

		var dbNow time.Time
		if err := database.owner.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
			t.Fatal(err)
		}

		// 1. Fresh Snapshot A where account name is "old-name" -> bind
		snapshotAID := uuid.New()
		fingerprintA := make([]byte, 32)
		fingerprintA[0] = 0xaa
		_, err := database.owner.Exec(ctx, `INSERT INTO gateway_directory_snapshots(
			snapshot_id, gateway_instance_id, fingerprint, schema_version, account_count, created_at
		) VALUES ($1, $2, $3, 1, 1, clock_timestamp())`, snapshotAID, fixture.gatewayID, fingerprintA)
		if err != nil {
			t.Fatal(err)
		}
		_, err = database.owner.Exec(ctx, `INSERT INTO gateway_directory_snapshot_items(
			snapshot_id, account_id, name, platform, type, url, status
		) VALUES ($1, $2, 'old-name', 'linux', 'apikey', 'https://old.test', 'active')`, snapshotAID, targetAccountID)
		if err != nil {
			t.Fatal(err)
		}

		insertGatewayDirectoryCurrentState(t, ctx, database, fixture.gatewayID, snapshotAID, dbNow.Add(-10*time.Second))

		bindRes, err := repo.Bind(ctx, jobstore.BindParams{
			RelayNodeID:       nodeID,
			GatewayInstanceID: fixture.gatewayID,
			GatewayAccountID:  targetAccountID,
			AdminID:           fixture.adminID,
		})
		if err != nil || bindRes.Outcome != jobstore.RelayBindingOutcomeSuccess {
			t.Fatalf("bind failed: %v", err)
		}

		// 2. Later ingestion advances to Snapshot C where targetAccountID has updated metadata "new-name"
		snapshotCID := uuid.New()
		fingerprintC := make([]byte, 32)
		fingerprintC[0] = 0xcc
		_, err = database.owner.Exec(ctx, `INSERT INTO gateway_directory_snapshots(
			snapshot_id, gateway_instance_id, fingerprint, schema_version, account_count, created_at
		) VALUES ($1, $2, $3, 1, 1, clock_timestamp())`, snapshotCID, fixture.gatewayID, fingerprintC)
		if err != nil {
			t.Fatal(err)
		}
		_, err = database.owner.Exec(ctx, `INSERT INTO gateway_directory_snapshot_items(
			snapshot_id, account_id, name, platform, type, url, status
		) VALUES ($1, $2, 'new-name', 'linux', 'apikey', 'https://new.test', 'active')`, snapshotCID, targetAccountID)
		if err != nil {
			t.Fatal(err)
		}

		// 3. Make Snapshot C stale (> 540s)
		insertGatewayDirectoryCurrentState(t, ctx, database, fixture.gatewayID, snapshotCID, dbNow.Add(-600*time.Second))

		view, err := repo.GetNodeCentricBindingView(ctx, nodeID)
		if err != nil {
			t.Fatal(err)
		}
		if view.Resolution != jobstore.RelayBindingResolutionUnknown {
			t.Fatalf("expected resolution unknown, got %s", view.Resolution)
		}
		if view.DirectoryFreshness != jobstore.DirectoryFreshnessStale {
			t.Fatalf("expected stale directory, got %s", view.DirectoryFreshness)
		}
		if view.ContextSource != jobstore.AccountContextSourceLastKnown {
			t.Fatalf("expected last_known context source, got %s", view.ContextSource)
		}
		if view.AccountContext == nil {
			t.Fatal("expected non-nil account context")
		}
		// Context must come from current snapshot C ("new-name"), NOT evidence snapshot A ("old-name")
		if view.AccountContext.Name != "new-name" {
			t.Fatalf("expected latest last-known metadata 'new-name', got '%s'", view.AccountContext.Name)
		}
	})

	t.Run("regression E: stale account-centric unbound account marked last_known", func(t *testing.T) {
		staleGwID := uuid.New()
		_, err := database.owner.Exec(ctx, `INSERT INTO gateway_instances(
			instance_id, display_name, probe_endpoint, probe_status,
			consecutive_successes, consecutive_failures, created_at, updated_at
		) VALUES ($1, 'gw-stale-unbound', 'https://gw-stale-unbound.test', 'healthy', 1, 0, clock_timestamp(), clock_timestamp())`,
			staleGwID)
		if err != nil {
			t.Fatal(err)
		}

		snapID := uuid.New()
		snapFingerprint := make([]byte, 32)
		snapFingerprint[0] = 0x99
		_, err = database.owner.Exec(ctx, `INSERT INTO gateway_directory_snapshots(
			snapshot_id, gateway_instance_id, fingerprint, schema_version, account_count, created_at
		) VALUES ($1, $2, $3, 1, 1, clock_timestamp())`, snapID, staleGwID, snapFingerprint)
		if err != nil {
			t.Fatal(err)
		}
		_, err = database.owner.Exec(ctx, `INSERT INTO gateway_directory_snapshot_items(
			snapshot_id, account_id, name, platform, type, url, status
		) VALUES ($1, 999111, 'unbound-stale-acct', 'linux', 'apikey', 'https://unbound.test', 'active')`, snapID)
		if err != nil {
			t.Fatal(err)
		}

		var dbNow time.Time
		if err := database.owner.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&dbNow); err != nil {
			t.Fatal(err)
		}
		// Stale directory
		insertGatewayDirectoryCurrentState(t, ctx, database, staleGwID, snapID, dbNow.Add(-600*time.Second))

		view, err := repo.GetGatewayAccountCentricBindingView(ctx, staleGwID)
		if err != nil {
			t.Fatal(err)
		}
		if view.DirectoryFreshness != jobstore.DirectoryFreshnessStale {
			t.Fatalf("expected stale directory, got %s", view.DirectoryFreshness)
		}
		if len(view.Accounts) != 1 {
			t.Fatalf("expected 1 account, got %d", len(view.Accounts))
		}
		acct := view.Accounts[0]
		if acct.Resolution != jobstore.RelayBindingResolutionUnbound {
			t.Fatalf("expected unbound resolution, got %s", acct.Resolution)
		}
		if acct.ContextSource != jobstore.AccountContextSourceLastKnown {
			t.Fatalf("expected last_known context source for unbound account in stale directory, got %s", acct.ContextSource)
		}
	})
}

