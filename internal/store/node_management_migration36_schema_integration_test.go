package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNodeManagementMigration36CleanDatabasePG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, owner, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
	defer cleanup()
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "36"); err != nil {
		t.Fatal(err)
	}

	var version, floor, serverMajor int
	if err := owner.QueryRow(ctx, `SELECT max(version_id) FILTER (WHERE is_applied),
		(SELECT phase6_evidence_floor FROM control_runtime_compatibility WHERE singleton_id=1),
		current_setting('server_version_num')::integer / 10000
		FROM goose_db_version`).Scan(&version, &floor, &serverMajor); err != nil {
		t.Fatal(err)
	}
	if version != 36 || floor != 2 || serverMajor != 18 {
		t.Fatalf("migration/floor/server=%d/%d/%d", version, floor, serverMajor)
	}

	var validated bool
	if err := owner.QueryRow(ctx, `SELECT convalidated FROM pg_constraint
		WHERE conrelid='public.relay_node_assets'::regclass
		  AND conname='relay_node_assets_http_only_check'`).Scan(&validated); err != nil {
		t.Fatal(err)
	}
	if !validated {
		t.Fatal("Node HTTP-only constraint is not validated")
	}
	var volatility string
	if err := owner.QueryRow(ctx, `SELECT provolatile FROM pg_proc
		WHERE oid='public.control_authorize_node_probe_v1(uuid)'::regprocedure`).Scan(&volatility); err != nil {
		t.Fatal(err)
	}
	if volatility != "v" {
		t.Fatalf("probe authorizer volatility=%q, want volatile for FOR SHARE", volatility)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO node_drivers(node_type,driver_contract_version,display_name)
		VALUES('cliproxyapi','v1','Migration 36 Driver')`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO relay_node_assets(
		instance_id,display_name,node_type,driver_contract_version,management_endpoint)
		VALUES($1,'direct HTTPS','cliproxyapi','v1','https://node.example')`, uuid.New()); err == nil {
		t.Fatal("direct HTTPS Node insert unexpectedly succeeded")
	}
	validNodeID := uuid.New()
	if _, err := owner.Exec(ctx, `INSERT INTO relay_node_assets(
		instance_id,display_name,node_type,driver_contract_version,management_endpoint)
		VALUES($1,'direct HTTP','cliproxyapi','v1','http://node.example')`, validNodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `UPDATE relay_node_assets
		SET management_endpoint='https://node.example' WHERE instance_id=$1`, validNodeID); err == nil {
		t.Fatal("direct HTTPS Node update unexpectedly succeeded")
	}
}

func TestNodeManagementMigration36LegacyHTTPSFailsClosedPG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, owner, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
	defer cleanup()
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "35"); err != nil {
		t.Fatal(err)
	}
	nodeID := uuid.New()
	if _, err := owner.Exec(ctx, `INSERT INTO node_drivers(node_type,driver_contract_version,display_name)
		VALUES('cliproxyapi','v1','Migration 36 Driver')`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO relay_node_assets(
		instance_id,display_name,node_type,driver_contract_version,management_endpoint,lifecycle_status,revision)
		VALUES($1,'Legacy HTTPS','cliproxyapi','v1','https://legacy.example','active',1)`, nodeID); err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(ctx); err != nil {
		t.Fatal(err)
	}
	migrationErr := applyGatewayLifecycleMigration(t, ctx, databaseURL, "36")
	if migrationErr == nil {
		t.Fatal("migration 36 accepted an existing HTTPS Node")
	}
	if !strings.Contains(migrationErr.Error(), nodeID.String()) {
		t.Fatalf("legacy migration error omitted affected instance ID: %v", migrationErr)
	}

	check, err := pgx.ConnectConfig(ctx, mustParseDatabaseConfig(t, databaseURL))
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close(ctx)
	var version int
	if err := check.QueryRow(ctx, `SELECT max(version_id) FILTER (WHERE is_applied) FROM goose_db_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 35 {
		t.Fatalf("failed migration advanced version to %d", version)
	}
	var endpoint string
	if err := check.QueryRow(ctx, `SELECT management_endpoint FROM relay_node_assets WHERE instance_id=$1`, nodeID).Scan(&endpoint); err != nil {
		t.Fatal(err)
	}
	if endpoint != "https://legacy.example" {
		t.Fatalf("legacy HTTPS endpoint changed to %q", endpoint)
	}
	var constraintCount int
	if err := check.QueryRow(ctx, `SELECT count(*) FROM pg_constraint
		WHERE conrelid='public.relay_node_assets'::regclass
		  AND conname='relay_node_assets_http_only_check'`).Scan(&constraintCount); err != nil {
		t.Fatal(err)
	}
	if constraintCount != 0 {
		t.Fatal("failed migration left the Node HTTP-only constraint installed")
	}
}

func TestNodeManagementMigration36ControlledCreateHTTPSRejectedPG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, owner, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
	defer cleanup()
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "36"); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO node_drivers(node_type,driver_contract_version,display_name)
		VALUES('cliproxyapi','v1','Migration 36 Driver')`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO driver_capabilities(node_type,driver_contract_version,capability)
		VALUES('cliproxyapi','v1','management_health_read')`); err != nil {
		t.Fatal(err)
	}
	runtime := newMigration36RuntimePool(t, ctx, databaseURL)
	defer runtime.Close()
	validNodeID := uuid.New()
	if _, err := runtime.Exec(ctx, `SELECT public.control_create_relay_node_asset(
		$1,'HTTP controlled create','cliproxyapi','v1','http://node.example',NULL,
		ARRAY['management_health_read']::text[])`, validNodeID); err != nil {
		t.Fatal(err)
	}
	before := migration36TruthCounts(t, ctx, owner)
	if _, err := runtime.Exec(ctx, `SELECT public.control_create_relay_node_asset(
		$1,'HTTPS controlled create','cliproxyapi','v1','https://node.example',NULL,
		ARRAY['management_health_read']::text[])`, uuid.New()); err == nil {
		t.Fatal("controlled create accepted HTTPS")
	}
	after := migration36TruthCounts(t, ctx, owner)
	if before != after {
		t.Fatalf("rejected controlled create changed durable truth: before=%v after=%v", before, after)
	}
	if _, err := runtime.Exec(ctx, `UPDATE relay_node_assets
		SET management_endpoint='https://node.example' WHERE instance_id=$1`, validNodeID); err == nil {
		t.Fatal("runtime HTTPS Node update unexpectedly succeeded")
	}
	var endpoint string
	if err := owner.QueryRow(ctx, `SELECT management_endpoint FROM relay_node_assets WHERE instance_id=$1`, validNodeID).Scan(&endpoint); err != nil {
		t.Fatal(err)
	}
	if endpoint != "http://node.example" {
		t.Fatalf("runtime rejected update changed endpoint to %q", endpoint)
	}
}

func TestNodeManagementMigration36ProbeLifecycleLockAndSecretFreePG18(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, owner, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
	defer cleanup()
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "36"); err != nil {
		t.Fatal(err)
	}
	nodeID, adminID := uuid.New(), uuid.New()
	if _, err := owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at)
		VALUES($1,$2,'Migration 36 Admin','enabled',clock_timestamp())`, adminID, "migration36_"+nodeID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO node_drivers(node_type,driver_contract_version,display_name)
		VALUES('cliproxyapi','v1','Migration 36 Driver')`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO driver_capabilities(node_type,driver_contract_version,capability)
		VALUES('cliproxyapi','v1','management_health_read')`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO relay_node_assets(
		instance_id,display_name,node_type,driver_contract_version,management_endpoint,reader_secret_ref)
		VALUES($1,'Probe Lock Node','cliproxyapi','v1','http://node.example','vault://reader-secret')`, nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO node_capabilities(instance_id,node_type,driver_contract_version,capability)
		VALUES($1,'cliproxyapi','v1','management_health_read')`, nodeID); err != nil {
		t.Fatal(err)
	}
	var secretReadable bool
	if err := owner.QueryRow(ctx, `SELECT has_column_privilege('relay_control_runtime','relay_node_assets','reader_secret_ref','SELECT')`).Scan(&secretReadable); err != nil {
		t.Fatal(err)
	}
	if secretReadable {
		t.Fatal("runtime can read reader_secret_ref")
	}

	runtimeConn, err := pgx.ConnectConfig(ctx, mustParseDatabaseConfig(t, databaseURL))
	if err != nil {
		t.Fatal(err)
	}
	defer runtimeConn.Close(ctx)
	if _, err := runtimeConn.Exec(ctx, `SET ROLE relay_control_runtime`); err != nil {
		t.Fatal(err)
	}
	lockTx, err := owner.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockTx.Exec(ctx, `SELECT 1 FROM relay_node_assets WHERE instance_id=$1 FOR UPDATE`, nodeID); err != nil {
		lockTx.Rollback(ctx)
		t.Fatal(err)
	}
	probeDone := make(chan struct {
		lifecycle string
		endpoint  string
		caps      []string
		err       error
	}, 1)
	go func() {
		var result struct {
			lifecycle string
			endpoint  string
			caps      []string
		}
		err := runtimeConn.QueryRow(ctx, `SELECT lifecycle_status,management_endpoint,capabilities
			FROM public.control_authorize_node_probe_v1($1)`, nodeID).Scan(&result.lifecycle, &result.endpoint, &result.caps)
		probeDone <- struct {
			lifecycle string
			endpoint  string
			caps      []string
			err       error
		}{result.lifecycle, result.endpoint, result.caps, err}
	}()
	select {
	case result := <-probeDone:
		lockTx.Rollback(ctx)
		t.Fatalf("probe authorizer bypassed Node lock: result=%+v", result)
	case <-time.After(250 * time.Millisecond):
	}
	if _, err := lockTx.Exec(ctx, `UPDATE relay_node_assets
		SET lifecycle_status='retired',revision=2,retired_at=clock_timestamp(),retired_by=$2,retire_reason='administrator_retire'
		WHERE instance_id=$1`, nodeID, adminID); err != nil {
		lockTx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := lockTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-probeDone:
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.lifecycle != "retired" || result.endpoint != "http://node.example" || len(result.caps) != 1 || result.caps[0] != "management_health_read" {
			t.Fatalf("probe projection after lifecycle commit=%+v", result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("probe authorizer did not observe committed lifecycle row")
	}
}

func TestNodeManagementMigration36ProbeWaitsForReplaceLifecycleLockPG18(t *testing.T) {
	f := newMigration36ProbeFixture(t)
	lockTx, err := f.owner.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockTx.Exec(f.ctx, `SELECT 1 FROM relay_node_assets WHERE instance_id=$1 FOR UPDATE`, f.nodeID); err != nil {
		lockTx.Rollback(f.ctx)
		t.Fatal(err)
	}
	probeDone := make(chan migration36ProbeResult, 1)
	go func() { probeDone <- f.authorizeProbe() }()
	select {
	case result := <-probeDone:
		lockTx.Rollback(f.ctx)
		t.Fatalf("probe bypassed Replace lifecycle lock: %+v", result)
	case <-time.After(250 * time.Millisecond):
	}

	replacementID := uuid.New()
	if _, err := lockTx.Exec(f.ctx, `UPDATE relay_node_assets
		SET lifecycle_status='retired',revision=2,retired_at=clock_timestamp(),retired_by=$2,retire_reason='replacement'
		WHERE instance_id=$1`, f.nodeID, f.adminID); err != nil {
		lockTx.Rollback(f.ctx)
		t.Fatal(err)
	}
	if _, err := lockTx.Exec(f.ctx, `INSERT INTO relay_node_assets(
		instance_id,display_name,node_type,driver_contract_version,management_endpoint)
		VALUES($1,'Replacement Node','cliproxyapi','v1','http://replacement.example')`, replacementID); err != nil {
		lockTx.Rollback(f.ctx)
		t.Fatal(err)
	}
	if _, err := lockTx.Exec(f.ctx, `INSERT INTO relay_node_asset_replacements(
		old_instance_id,new_instance_id,replaced_at,replaced_by,command_id)
		VALUES($1,$2,clock_timestamp(),$3,$4)`, f.nodeID, replacementID, f.adminID, uuid.New()); err != nil {
		lockTx.Rollback(f.ctx)
		t.Fatal(err)
	}
	if err := lockTx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	result := <-probeDone
	if result.err != nil {
		t.Fatal(result.err)
	}
	if result.lifecycle != "retired" || result.endpoint != "http://node.example" {
		t.Fatalf("probe projection after Replace=%+v", result)
	}
	var replacementLifecycle string
	if err := f.owner.QueryRow(f.ctx, `SELECT lifecycle_status FROM relay_node_assets WHERE instance_id=$1`, replacementID).Scan(&replacementLifecycle); err != nil {
		t.Fatal(err)
	}
	if replacementLifecycle != "active" {
		t.Fatalf("replacement lifecycle=%q", replacementLifecycle)
	}
}

func TestNodeManagementMigration36ProbeFirstLifecycleWaitsPG18(t *testing.T) {
	f := newMigration36ProbeFixture(t)
	probeTx, err := f.runtime.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		t.Fatal(err)
	}
	var lifecycle, endpoint string
	var capabilities []string
	if err := probeTx.QueryRow(f.ctx, `SELECT lifecycle_status,management_endpoint,capabilities
		FROM public.control_authorize_node_probe_v1($1)`, f.nodeID).Scan(&lifecycle, &endpoint, &capabilities); err != nil {
		probeTx.Rollback(f.ctx)
		t.Fatal(err)
	}
	if lifecycle != "active" || endpoint != "http://node.example" || len(capabilities) != 1 {
		probeTx.Rollback(f.ctx)
		t.Fatalf("probe-first projection=%q/%q/%v", lifecycle, endpoint, capabilities)
	}

	writer, err := pgx.ConnectConfig(f.ctx, mustParseDatabaseConfig(t, f.databaseURL))
	if err != nil {
		probeTx.Rollback(f.ctx)
		t.Fatal(err)
	}
	defer writer.Close(f.ctx)
	retireDone := make(chan error, 1)
	go func() {
		_, updateErr := writer.Exec(f.ctx, `UPDATE relay_node_assets
			SET lifecycle_status='retired',revision=2,retired_at=clock_timestamp(),retired_by=$2,retire_reason='administrator_retire'
			WHERE instance_id=$1`, f.nodeID, f.adminID)
		retireDone <- updateErr
	}()
	select {
	case updateErr := <-retireDone:
		probeTx.Rollback(f.ctx)
		t.Fatalf("lifecycle writer bypassed probe authorization lock: %v", updateErr)
	case <-time.After(250 * time.Millisecond):
	}
	if err := probeTx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-retireDone; err != nil {
		t.Fatal(err)
	}
}

type migration36ProbeResult struct {
	lifecycle string
	endpoint  string
	caps      []string
	err       error
}

type migration36ProbeFixture struct {
	ctx         context.Context
	databaseURL string
	owner       *pgx.Conn
	runtime     *pgx.Conn
	nodeID      uuid.UUID
	adminID     uuid.UUID
}

func newMigration36ProbeFixture(t *testing.T) *migration36ProbeFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	databaseURL, owner, cleanup := newGatewayLifecycleMigrationDatabase(t, ctx)
	t.Cleanup(cleanup)
	if err := applyGatewayLifecycleMigration(t, ctx, databaseURL, "36"); err != nil {
		t.Fatal(err)
	}
	nodeID, adminID := uuid.New(), uuid.New()
	if _, err := owner.Exec(ctx, `INSERT INTO control_admin_users(admin_id,login_name,display_name,status,activated_at)
		VALUES($1,$2,'Migration 36 Probe Admin','enabled',clock_timestamp())`, adminID, "migration36_probe_"+nodeID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO node_drivers(node_type,driver_contract_version,display_name)
		VALUES('cliproxyapi','v1','Migration 36 Probe Driver')`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO driver_capabilities(node_type,driver_contract_version,capability)
		VALUES('cliproxyapi','v1','management_health_read')`); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO relay_node_assets(
		instance_id,display_name,node_type,driver_contract_version,management_endpoint,reader_secret_ref)
		VALUES($1,'Migration 36 Probe Node','cliproxyapi','v1','http://node.example','vault://reader-secret')`, nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec(ctx, `INSERT INTO node_capabilities(instance_id,node_type,driver_contract_version,capability)
		VALUES($1,'cliproxyapi','v1','management_health_read')`, nodeID); err != nil {
		t.Fatal(err)
	}
	runtime, err := pgx.ConnectConfig(ctx, mustParseDatabaseConfig(t, databaseURL))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Exec(ctx, `SET ROLE relay_control_runtime`); err != nil {
		runtime.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close(context.Background()); owner.Close(context.Background()) })
	return &migration36ProbeFixture{ctx: ctx, databaseURL: databaseURL, owner: owner, runtime: runtime, nodeID: nodeID, adminID: adminID}
}

func (f *migration36ProbeFixture) authorizeProbe() migration36ProbeResult {
	var result migration36ProbeResult
	result.err = f.runtime.QueryRow(f.ctx, `SELECT lifecycle_status,management_endpoint,capabilities
		FROM public.control_authorize_node_probe_v1($1)`, f.nodeID).Scan(&result.lifecycle, &result.endpoint, &result.caps)
	return result
}

type migration36TruthCount struct {
	nodes        int
	capabilities int
	audits       int
	receipts     int
	generation   int64
}

func migration36TruthCounts(t *testing.T, ctx context.Context, owner *pgx.Conn) migration36TruthCount {
	t.Helper()
	var result migration36TruthCount
	if err := owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM relay_node_assets),
		(SELECT count(*) FROM node_capabilities),
		(SELECT count(*) FROM audit_logs),
		(SELECT count(*) FROM asset_admin_command_receipts),
		(SELECT node_generation FROM asset_registry_generations WHERE singleton_id=1)`).Scan(
		&result.nodes, &result.capabilities, &result.audits, &result.receipts, &result.generation); err != nil {
		t.Fatal(err)
	}
	return result
}

func newMigration36RuntimePool(t *testing.T, ctx context.Context, databaseURL string) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET ROLE relay_control_runtime`)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	return pool
}

func mustParseDatabaseConfig(t *testing.T, databaseURL string) *pgx.ConnConfig {
	t.Helper()
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	return config
}
