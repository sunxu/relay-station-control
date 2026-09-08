package store_test

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/sunxu/relay-station-control/internal/drivers"
	"github.com/sunxu/relay-station-control/internal/inventorypoll"
	pollstore "github.com/sunxu/relay-station-control/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const pollCapacityFunction = "public.control_query_account_inventory_poll_capacity_v1()"

func TestAccountInventoryPollCapacityRuntimeReadIsSchedulerCompatible(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	nodeID, firstPolicyID, secondPolicyID := uuid.New(), uuid.New(), uuid.New()
	nodeType := string(drivers.NodeTypeCLIProxyAPI)
	contract := string(drivers.DriverContractCLIProxyAPIAuthFilesV1)
	slot := currentPollSlot(t, ctx, database.owner)
	var secondsIntoSlot int64
	if err := database.owner.QueryRow(ctx, `SELECT extract(epoch FROM clock_timestamp())::bigint % 300`).Scan(&secondsIntoSlot); err != nil {
		t.Fatal(err)
	}
	if secondsIntoSlot > 285 {
		time.Sleep(time.Duration(301-secondsIntoSlot) * time.Second)
		slot = currentPollSlot(t, ctx, database.owner)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO node_drivers(
		node_type,driver_contract_version,display_name
	) VALUES ($1,$2,'CLIProxyAPI Poll Test')`, nodeType, contract); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO driver_capabilities(
		node_type,driver_contract_version,capability
	) VALUES ($1,$2,'management_health_read'),
	         ($1,$2,'management_account_inventory_read')`, nodeType, contract); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_assets(
		instance_id,display_name,node_type,driver_contract_version,management_endpoint,reader_secret_ref
	) VALUES ($1,'Poll Scheduler Node',$2,$3,'http://poll-node.example:8317',
	          'file://poll-test/management-key')`, nodeID, nodeType, contract); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO node_capabilities(
		instance_id,node_type,driver_contract_version,capability
	) VALUES ($1,$2,$3,'management_account_inventory_read')`, nodeID, nodeType, contract); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_inventory_monitoring_activations(
		instance_id,effective_from,reason,actor,created_at
	) VALUES ($1,$2,'deployment_enable','integration-test',$2)`, nodeID, slot); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO provider_inventory_policy_versions(
		policy_version_id,node_type,driver_contract_version,active_providers,out_of_scope_providers,created_by
	) VALUES ($1,$3,$4,ARRAY['openai'],ARRAY['legacy'],'integration-test'),
	         ($2,$3,$4,ARRAY['anthropic'],ARRAY['legacy'],'integration-test')`,
		firstPolicyID, secondPolicyID, nodeType, contract); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO provider_inventory_policy_activations(
		node_type,driver_contract_version,policy_version_id,effective_from,activated_by,created_at
	) VALUES ($1,$2,$3,$4,'integration-test',$4)`, nodeType, contract, firstPolicyID, slot); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO provider_inventory_policy_bindings(
		node_type,driver_contract_version,policy_version_id,bound_by,bound_at
	) VALUES ($1,$2,$3,'integration-test',$4)`, nodeType, contract, firstPolicyID, slot); err != nil {
		t.Fatal(err)
	}

	reader, err := pollstore.NewInventoryPollCapacityRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	scheduler, err := pollstore.NewInventoryPollRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	var permitted, registrar, direct, definer, publicExec bool
	var owner, volatility string
	if err := database.owner.QueryRow(ctx, `SELECT has_function_privilege('relay_control_runtime',$1,'EXECUTE'),has_function_privilege('relay_control_asset_registrar',$1,'EXECUTE'),has_table_privilege('relay_control_runtime','account_inventory_provider_states','SELECT'),p.prosecdef,r.rolname,p.provolatile::text,EXISTS(SELECT 1 FROM aclexplode(p.proacl) a WHERE a.grantee=0 AND a.privilege_type='EXECUTE') FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE p.oid=$1::regprocedure`, pollCapacityFunction).Scan(&permitted, &registrar, &direct, &definer, &owner, &volatility, &publicExec); err != nil {
		t.Fatal(err)
	}
	var fixedPath bool
	if err := database.owner.QueryRow(ctx, `SELECT proconfig @> ARRAY['search_path=pg_catalog','TimeZone=UTC'] FROM pg_proc WHERE oid=$1::regprocedure`, pollCapacityFunction).Scan(&fixedPath); err != nil || !fixedPath {
		t.Fatal("unsafe function configuration", err)
	}
	if !permitted || registrar || direct || !definer || owner != "relay_control_migrator" || volatility != "s" || publicExec {
		t.Fatal("readonly ACL boundary failed")
	}
	if _, err := database.runtime.Exec(ctx, "SELECT * FROM account_inventory_provider_states"); err == nil {
		t.Fatal("runtime selected provider states")
	} else {
		requirePostgresCode(t, err, "42501")
	}
	checkCount := func(want int64) {
		t.Helper()
		v, err := reader.GetInventoryPollCapacity(ctx)
		if err != nil || v.EligibleNodeCount != want || !v.EvaluatedSlot.Equal(slot) || !v.EvaluatedSlot.Equal(v.EvaluatedAt.Truncate(5*time.Minute)) {
			t.Fatalf("count %d: %+v err=%v", want, v, err)
		}
	}
	checkCount(1)
	var accounts int
	if err := database.owner.QueryRow(ctx, "SELECT count(*) FROM account_inventory").Scan(&accounts); err != nil || accounts != 0 {
		t.Fatal("expected zero-account fixture", err)
	}
	addNode := func() uuid.UUID {
		id := uuid.New()
		if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_assets(instance_id,display_name,node_type,driver_contract_version,management_endpoint) VALUES($1,'capacity additional',$2,$3,'http://capacity.example.invalid')`, id, nodeType, contract); err != nil {
			t.Fatal(err)
		}
		if _, err := database.owner.Exec(ctx, `INSERT INTO node_capabilities(instance_id,node_type,driver_contract_version,capability) VALUES($1,$2,$3,'management_account_inventory_read')`, id, nodeType, contract); err != nil {
			t.Fatal(err)
		}
		if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_inventory_monitoring_activations(instance_id,effective_from,reason,actor,created_at) VALUES($1,$2,'deployment_enable','integration-test',$2)`, id, slot); err != nil {
			t.Fatal(err)
		}
		return id
	}
	addNode()
	third := addNode()
	checkCount(3)
	req := inventorypoll.ScheduleRequest{Period: 5 * time.Minute, PollStartGrace: 299 * time.Second, MaxAttempts: 2, Limit: 2}
	if _, err := scheduler.ScheduleCurrent(ctx, req); !errors.Is(err, inventorypoll.ErrCapacityExceeded) {
		t.Fatalf("capacity classification: %v", err)
	}
	var runs int
	if err := database.owner.QueryRow(ctx, "SELECT count(*) FROM account_inventory_poll_runs").Scan(&runs); err != nil || runs != 0 {
		t.Fatal("over-capacity created partial runs", err)
	}
	// Restore eligibility by removing a capability in this isolated fixture;
	// production monitoring changes remain subject to slot activation semantics.
	if _, err := database.owner.Exec(ctx, "DELETE FROM node_capabilities WHERE instance_id=$1", third); err != nil {
		t.Fatal(err)
	}
	checkCount(2)
	// A future monitoring activation must not enter the current-slot count.
	future := uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_assets(instance_id,display_name,node_type,driver_contract_version,management_endpoint) VALUES($1,'future monitoring',$2,$3,'http://future.example.invalid')`, future, nodeType, contract); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO node_capabilities(instance_id,node_type,driver_contract_version,capability) VALUES($1,$2,$3,'management_account_inventory_read')`, future, nodeType, contract); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_inventory_monitoring_activations(instance_id,effective_from,reason,actor) VALUES($1,$2,'deployment_enable','integration-test')`, future, slot.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	checkCount(2)
	got, err := scheduler.ScheduleCurrent(ctx, req)
	if err != nil || got.Created != 2 || got.Eligible != 2 || !got.ScheduledAt.Equal(slot) {
		t.Fatalf("recovered scheduling: %+v %v", got, err)
	}
	again, err := scheduler.ScheduleCurrent(ctx, req)
	if err != nil || again.Created != 0 || again.Existing != 2 {
		t.Fatal("recovery not idempotent", err)
	}
	// Missing active policy cannot disappear from the diagnostic count as ready.
	if _, err := database.owner.Exec(ctx, "DELETE FROM provider_inventory_policy_activations WHERE node_type=$1", nodeType); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.GetInventoryPollCapacity(ctx); !errors.Is(err, pollstore.ErrInventoryPollCapacityInconsistent) {
		t.Fatalf("inconsistent read: %v", err)
	}
	_, err = scheduler.ScheduleCurrent(ctx, req)
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != "23514" || errors.Is(err, inventorypoll.ErrCapacityExceeded) {
		t.Fatalf("inconsistent scheduler was misclassified: %v", err)
	}
	// Down executes only its function DROP, in a rolled-back owner transaction.
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "00019_account_inventory_poll_capacity_query_access.sql"))
	if err != nil {
		t.Fatal(err)
	}
	down := strings.Split(string(raw), "-- +goose Down")[1]
	tx, err := database.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	var absent bool
	if err = tx.QueryRow(ctx, "SELECT to_regprocedure($1) IS NULL", pollCapacityFunction).Scan(&absent); err != nil || !absent {
		t.Fatal("down did not drop function", err)
	}
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM account_inventory_poll_runs").Scan(&runs); err != nil || runs != 2 {
		t.Fatal("down changed poll history", err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	t.Log("capacity ACL, zero-account eligibility, N+1 atomic rejection, N recovery, policy inconsistency, and readonly Down: PASS")
}

func TestAccountInventoryPollCapacityExpiredRetryRemainsBounded(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	slot := currentPollSlot(t, ctx, database.owner).Add(-5 * time.Minute)
	id := uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(
 poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,provider_policy_version,status,attempt_count,max_attempts,poll_start_grace_seconds,created_at,first_started_at,last_started_at,execution_reason)
 VALUES($1,$2,$3,$4,$5,$6,'retry_wait',1,2,120,$5,$5,$5,'lease_expired')`, id, fixture.instanceID, fixture.nodeType, fixture.contract, slot, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	repository, err := pollstore.NewInventoryPollRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ClaimRunnable(ctx, inventorypoll.ClaimRequest{Token: uuid.New(), LeaseDuration: 30 * time.Second}); !errors.Is(err, inventorypoll.ErrNoWork) {
		t.Fatalf("expired retry was claimable: %v", err)
	}
	result, err := repository.ReconcileExpired(ctx, inventorypoll.ReconcileRequest{PollStartGrace: 120 * time.Second, Limit: 10})
	if err != nil || result.Abandoned != 1 {
		t.Fatalf("expired retry reconciliation: %+v %v", result, err)
	}
	again, err := repository.ReconcileExpired(ctx, inventorypoll.ReconcileRequest{PollStartGrace: 120 * time.Second, Limit: 10})
	if err != nil || again.Abandoned != 0 {
		t.Fatalf("repeated terminal transition: %+v %v", again, err)
	}
	var status string
	var attempt int
	if err := database.owner.QueryRow(ctx, "SELECT status,attempt_count FROM account_inventory_poll_runs WHERE poll_run_id=$1", id).Scan(&status, &attempt); err != nil || status != "abandoned" || attempt != 1 {
		t.Fatal("expired retry changed attempt or terminal state", err)
	}
}
