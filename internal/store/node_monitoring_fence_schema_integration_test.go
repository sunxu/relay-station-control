package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

type nodeFenceFixture struct {
	ctx       context.Context
	owner     *pgx.Conn
	ownerURL  string
	pool      *pgxpool.Pool
	registrar *pgxpool.Pool
	nodeID    uuid.UUID
	adminID   uuid.UUID
	cleanup   func()
}

func newNodeFenceFixture(t *testing.T) *nodeFenceFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	databaseURL, owner, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "37"); err != nil {
		cleanup()
		t.Fatal(err)
	}
	adminID, nodeID := uuid.New(), uuid.New()
	if _, err := owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at)
		VALUES($1,$2,'Fence Admin','enabled',clock_timestamp())`, adminID, "fence_"+assetFixtureSuffix(t)); err != nil {
		cleanup()
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO node_drivers(node_type,driver_contract_version,display_name)
		VALUES('cliproxyapi','v1','Fence Driver')`); err != nil {
		cleanup()
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO driver_capabilities(node_type,driver_contract_version,capability)
		VALUES('cliproxyapi','v1','management_health_read')`); err != nil {
		cleanup()
		t.Fatal(err)
	}
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
	if _, err := pool.Exec(ctx, `SELECT public.control_create_relay_node_asset(
		$1,'Fence Node','cliproxyapi','v1','http://node.example',NULL,
		ARRAY['management_health_read']::text[])`, nodeID); err != nil {
		pool.Close()
		cleanup()
		t.Fatal(err)
	}
	registrarConfig := poolConfig.Copy()
	registrarConfig.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, `SET ROLE relay_control_asset_registrar`)
		return err
	}
	registrar, err := pgxpool.NewWithConfig(ctx, registrarConfig)
	if err != nil {
		pool.Close()
		cleanup()
		t.Fatal(err)
	}
	t.Cleanup(func() { registrar.Close(); pool.Close(); _ = owner.Close(context.Background()); cleanup() })
	return &nodeFenceFixture{ctx: ctx, owner: owner, ownerURL: databaseURL, pool: pool, registrar: registrar, nodeID: nodeID, adminID: adminID, cleanup: cleanup}
}

func (f *nodeFenceFixture) disable(t *testing.T, requestID string) assetstore.NodeCommandResult {
	t.Helper()
	repository, err := assetstore.NewNodeMonitoringRepository(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	result, err := repository.Disable(f.ctx, assetstore.NodeMonitoringCommand{
		CommandID: uuid.New(), ActorAdminID: f.adminID, RequestID: requestID, InstanceID: f.nodeID,
	})
	if err != nil {
		t.Fatalf("disable %s: %v", requestID, err)
	}
	return result
}

func nodeFenceSQLState(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func requireNodeFenceSQLState(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("operation unexpectedly succeeded; want SQLSTATE %s", want)
	}
	if got := nodeFenceSQLState(err); got != want {
		t.Fatalf("SQLSTATE=%q want %q: %v", got, want, err)
	}
}

func insertNodeDisableFence(t *testing.T, ctx context.Context, conn *pgx.Conn, nodeID, adminID, commandID uuid.UUID, committedAt time.Time) {
	t.Helper()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin disable fence fixture: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT command_id FROM control_reserve_admin_command_v1($1::uuid,$2::uuid,'asset_admin'::text,'node.monitoring_disable'::text,1::smallint,decode(repeat('00',32),'hex'),NULL::smallint)`, commandID, adminID); err != nil {
		t.Fatalf("reserve disable fence: %v", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO asset_admin_command_receipts(
		command_id,command_kind,canonical_intent_hash,sanitized_result,response_status,actor_admin_id,committed_at)
		VALUES($1,'node.monitoring_disable',decode(repeat('00',32),'hex'),
		jsonb_build_object('instance_id',$2::text),200,$3,$4)`, commandID, nodeID, adminID, committedAt)
	if err != nil {
		t.Fatalf("insert disable fence: %v", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit disable fence fixture: %v", err)
	}
}

func TestNodeMonitoringFenceIndexAndStrictOrderingPG18(t *testing.T) {
	f := newNodeFenceFixture(t)
	for _, requestID := range []string{"disable-a", "disable-b", "disable-c"} {
		result := f.disable(t, requestID)
		if result.HTTPStatus != 200 {
			t.Fatalf("disable %s status=%d", requestID, result.HTTPStatus)
		}
	}
	var planRows []string
	planTx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer planTx.Rollback(f.ctx)
	if _, err := planTx.Exec(f.ctx, `SET LOCAL enable_seqscan=off`); err != nil {
		t.Fatal(err)
	}
	rows, err := planTx.Query(f.ctx, `EXPLAIN (COSTS OFF) SELECT command_id
		FROM asset_admin_command_receipts
		WHERE command_kind='node.monitoring_disable'
		  AND sanitized_result->>'instance_id'=$1
		ORDER BY committed_at DESC LIMIT 1`, f.nodeID.String())
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		planRows = append(planRows, line)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	if err := planTx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(planRows, "\n"), "asset_admin_command_receipts_node_disable_fence_idx") {
		t.Fatalf("EXPLAIN did not use disable fence index: %s", strings.Join(planRows, " | "))
	}

	var commandIDs []uuid.UUID
	var committed []time.Time
	rows, err = f.owner.Query(f.ctx, `SELECT command_id,committed_at
		FROM asset_admin_command_receipts
		WHERE command_kind='node.monitoring_disable' AND sanitized_result->>'instance_id'=$1
		ORDER BY committed_at ASC`, f.nodeID.String())
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id uuid.UUID
		var at time.Time
		if err := rows.Scan(&id, &at); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		commandIDs = append(commandIDs, id)
		committed = append(committed, at)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	if len(commandIDs) != 3 {
		t.Fatalf("disable receipt count=%d want 3", len(commandIDs))
	}
	for i := 1; i < len(committed); i++ {
		if !committed[i].After(committed[i-1]) {
			t.Fatalf("committed_at is not strictly increasing: %v", committed)
		}
	}
	var latest uuid.UUID
	if err := f.registrar.QueryRow(f.ctx, `SELECT public.control_latest_node_disable_fence_v1($1)`, f.nodeID).Scan(&latest); err != nil {
		t.Fatal(err)
	}
	if latest != commandIDs[len(commandIDs)-1] {
		t.Fatalf("latest fence=%s want C=%s", latest, commandIDs[len(commandIDs)-1])
	}
}

func TestNodeMonitoringFenceRuntimeReceiptMutationRejectedPG18(t *testing.T) {
	f := newNodeFenceFixture(t)
	f.disable(t, "seed-disable")
	var before int
	if err := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM asset_admin_command_receipts`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`UPDATE asset_admin_command_receipts SET response_status=201`,
		`DELETE FROM asset_admin_command_receipts`,
		`TRUNCATE asset_admin_command_receipts`,
	}
	for _, statement := range statements {
		if _, err := f.pool.Exec(f.ctx, statement); err == nil {
			t.Fatalf("runtime receipt mutation unexpectedly succeeded: %s", statement)
		}
	}
	var after int
	if err := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM asset_admin_command_receipts`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("receipt count changed after rejected runtime mutations: %d -> %d", before, after)
	}
}

func TestNodeMonitoringFenceF0F1WaitSeesCommittedDisablePG18(t *testing.T) {
	f := newNodeFenceFixture(t)
	ctx, cancel := context.WithTimeout(f.ctx, 20*time.Second)
	defer cancel()
	lockTx, err := f.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockTx.Exec(ctx, `SELECT 1 FROM relay_node_assets WHERE instance_id=$1 FOR UPDATE`, f.nodeID); err != nil {
		t.Fatal(err)
	}
	writerDone := make(chan error, 1)
	go func() {
		_, err := f.registrar.Exec(ctx, `SELECT public.control_set_node_inventory_monitoring(
			$1,true,NULL,'deployment_enable','runtime',NULL)`, f.nodeID)
		writerDone <- err
	}()
	select {
	case err := <-writerDone:
		t.Fatalf("writer did not wait for Node lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	before := countNodeActivations(t, ctx, f.owner, f.nodeID)
	insertConn, err := pgx.ConnectConfig(ctx, mustParseNodeFenceConfig(t, f.ownerURL))
	if err != nil {
		t.Fatal(err)
	}
	insertNodeDisableFence(t, ctx, insertConn, f.nodeID, f.adminID, uuid.New(), time.Now().UTC())
	insertConn.Close(ctx)
	if err := lockTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	err = <-writerDone
	requireNodeFenceSQLState(t, err, "55000")
	if after := countNodeActivations(t, ctx, f.owner, f.nodeID); after != before {
		t.Fatalf("F0/F1 conflict changed activation count: %d -> %d", before, after)
	}
}

func TestNodeMonitoringFenceAlreadyDisabledBlocksOldF0ButAcceptsNewF0PG18(t *testing.T) {
	f := newNodeFenceFixture(t)
	first := f.disable(t, "first-disable")
	var firstID uuid.UUID
	if err := f.owner.QueryRow(f.ctx, `SELECT command_id FROM asset_admin_command_receipts WHERE command_kind='node.monitoring_disable' ORDER BY committed_at DESC LIMIT 1`).Scan(&firstID); err != nil {
		t.Fatal(err)
	}
	if firstID == uuid.Nil || first.HTTPStatus != 200 {
		t.Fatal("initial disable did not create a fence")
	}
	ctx, cancel := context.WithTimeout(f.ctx, 20*time.Second)
	defer cancel()
	lockTx, err := f.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockTx.Exec(ctx, `SELECT 1 FROM relay_node_assets WHERE instance_id=$1 FOR UPDATE`, f.nodeID); err != nil {
		t.Fatal(err)
	}
	writerDone := make(chan error, 1)
	go func() {
		_, err := f.registrar.Exec(ctx, `SELECT public.control_set_node_inventory_monitoring(
			$1,true,NULL,'deployment_enable','runtime',$2)`, f.nodeID, firstID)
		writerDone <- err
	}()
	select {
	case err := <-writerDone:
		t.Fatalf("old-F0 writer did not wait for Node lock: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	insertConn, err := pgx.ConnectConfig(ctx, mustParseNodeFenceConfig(t, f.ownerURL))
	if err != nil {
		t.Fatal(err)
	}
	insertNodeDisableFence(t, ctx, insertConn, f.nodeID, f.adminID, uuid.New(), time.Now().UTC().Add(time.Microsecond))
	insertConn.Close(ctx)
	if err := lockTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	requireNodeFenceSQLState(t, <-writerDone, "55000")
	if got := countNodeActivations(t, ctx, f.owner, f.nodeID); got != 0 {
		t.Fatalf("old F0 created %d activations", got)
	}

	var currentFence uuid.UUID
	if err := f.registrar.QueryRow(ctx, `SELECT public.control_latest_node_disable_fence_v1($1)`, f.nodeID).Scan(&currentFence); err != nil {
		t.Fatal(err)
	}
	if _, err := f.registrar.Exec(ctx, `SELECT public.control_set_node_inventory_monitoring(
		$1,true,NULL,'deployment_enable','runtime',$2)`, f.nodeID, currentFence); err != nil {
		t.Fatalf("writer with new F0 failed: %v", err)
	}
	if got := countNodeActivations(t, ctx, f.owner, f.nodeID); got != 1 {
		t.Fatalf("new F0 activation count=%d want 1", got)
	}
}

func TestNodeMonitoringFenceGenerationOverflowRollsBackPG18(t *testing.T) {
	f := newNodeFenceFixture(t)
	if _, err := f.owner.Exec(f.ctx, `UPDATE asset_registry_generations SET node_generation=9223372036854775807 WHERE singleton_id=1`); err != nil {
		t.Fatal(err)
	}
	before := countNodeActivations(t, f.ctx, f.owner, f.nodeID)
	_, err := f.registrar.Exec(f.ctx, `SELECT public.control_set_node_inventory_monitoring(
		$1,true,NULL,'deployment_enable','runtime',NULL)`, f.nodeID)
	requireNodeFenceSQLState(t, err, "22003")
	var generation int64
	if err := f.owner.QueryRow(f.ctx, `SELECT node_generation FROM asset_registry_generations WHERE singleton_id=1`).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	if generation != 9223372036854775807 {
		t.Fatalf("generation changed after overflow: %d", generation)
	}
	if after := countNodeActivations(t, f.ctx, f.owner, f.nodeID); after != before {
		t.Fatalf("activation changed after overflow: %d -> %d", before, after)
	}
}

func countNodeActivations(t *testing.T, ctx context.Context, conn *pgx.Conn, nodeID uuid.UUID) int {
	t.Helper()
	var count int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM relay_node_inventory_monitoring_activations WHERE instance_id=$1`, nodeID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func mustParseNodeFenceConfig(t *testing.T, databaseURL string) *pgx.ConnConfig {
	t.Helper()
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	return config
}
