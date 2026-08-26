package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/sunxu/relay-station-control/internal/drivers"
	"github.com/sunxu/relay-station-control/internal/inventorypoll"
	pollstore "github.com/sunxu/relay-station-control/internal/store"
)

type pollFixture struct {
	instanceID, policyID uuid.UUID
	nodeType, contract   string
	activeProviders      []string
}

func loadPollFixture(t *testing.T, ctx context.Context, query interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) pollFixture {
	t.Helper()
	var fixture pollFixture
	err := query.QueryRow(ctx, `SELECT asset.instance_id, asset.node_type,
		asset.driver_contract_version, policy.policy_version_id, policy.active_providers
		FROM relay_node_assets AS asset
		JOIN provider_inventory_policy_versions AS policy
		  ON policy.node_type=asset.node_type
		 AND policy.driver_contract_version=asset.driver_contract_version
		WHERE cardinality(policy.active_providers)>0
		ORDER BY asset.instance_id, policy.created_at LIMIT 1`).Scan(
		&fixture.instanceID, &fixture.nodeType, &fixture.contract,
		&fixture.policyID, &fixture.activeProviders)
	if errors.Is(err, pgx.ErrNoRows) {
		t.Skip("account inventory poll schema test requires an asset and nonempty provider policy fixture")
	}
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}

func currentPollSlot(t *testing.T, ctx context.Context, query interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) time.Time {
	t.Helper()
	var slot time.Time
	if err := query.QueryRow(ctx, `SELECT to_timestamp(
		floor(extract(epoch FROM clock_timestamp())/300)*300
	)`).Scan(&slot); err != nil {
		t.Fatal(err)
	}
	return slot.UTC()
}

func TestAccountInventoryPollSchemaRejectsInvalidSlotsDuplicatesAndAbandonedEvidence(t *testing.T) {
	conn := connectTestDatabase(t)
	ctx := context.Background()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	fixture := loadPollFixture(t, ctx, tx)
	slot := currentPollSlot(t, ctx, tx)

	insert := `INSERT INTO account_inventory_poll_runs (
		poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
		provider_policy_version,max_attempts,poll_start_grace_seconds,created_at
	) VALUES ($1,$2,$3,$4,$5,$6,2,299,clock_timestamp())`
	firstID := uuid.New()
	if _, err := tx.Exec(ctx, insert, firstID, fixture.instanceID, fixture.nodeType,
		fixture.contract, slot, fixture.policyID); err != nil {
		t.Fatal(err)
	}
	err = assetSavepoint(t, ctx, tx, "unaligned slot", func() error {
		_, err := tx.Exec(ctx, insert, uuid.New(), fixture.instanceID, fixture.nodeType,
			fixture.contract, slot.Add(time.Second), fixture.policyID)
		return err
	})
	requireSQLState(t, err, "23514")
	err = assetSavepoint(t, ctx, tx, "duplicate node slot", func() error {
		_, err := tx.Exec(ctx, insert, uuid.New(), fixture.instanceID, fixture.nodeType,
			fixture.contract, slot, fixture.policyID)
		return err
	})
	requireSQLState(t, err, "23505")

	abandonedID := uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO account_inventory_poll_runs (
		poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
		provider_policy_version,status,max_attempts,poll_start_grace_seconds,
		created_at,abandoned_at,execution_reason
	) VALUES ($1,$2,$3,$4,$5,$6,'abandoned',2,299,
		clock_timestamp(),clock_timestamp(),'poll_start_grace_expired')`,
		abandonedID, fixture.instanceID, fixture.nodeType, fixture.contract,
		slot.Add(-5*time.Minute), fixture.policyID); err != nil {
		t.Fatal(err)
	}
	err = assetSavepoint(t, ctx, tx, "provider evidence on abandoned poll", func() error {
		_, err := tx.Exec(ctx, `INSERT INTO account_inventory_poll_provider_results (
			poll_run_id,provider,identifiable_count,missing_identity_count,
			duplicate_identity_count,identity_complete,snapshot_complete,degraded,reason
		) VALUES ($1,$2,0,0,0,true,false,true,'transport_failed')`,
			abandonedID, fixture.activeProviders[0])
		return err
	})
	requireSQLState(t, err, "23514")
}

func TestInventoryPollRepositoryClaimFinalizeAndFencing(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := pollFixture{
		instanceID: uuid.New(), policyID: uuid.New(), nodeType: "poll-test",
		contract: "v1", activeProviders: []string{"antigravity", "openai"},
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO node_drivers(
		node_type,driver_contract_version,display_name
	) VALUES ($1,$2,'Poll Test Driver')`, fixture.nodeType, fixture.contract); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_assets(
		instance_id,display_name,node_type,driver_contract_version,
		management_endpoint,reader_secret_ref
	) VALUES ($3,'Poll Test Node',$1,$2,'http://node.example',
		'docker-secret://synthetic/node-reader')`, fixture.nodeType, fixture.contract,
		fixture.instanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO provider_inventory_policy_versions(
		policy_version_id,node_type,driver_contract_version,active_providers,
		out_of_scope_providers,created_by
	) VALUES ($3,$1,$2,$4,ARRAY['gemini'],'integration-test')`,
		fixture.nodeType, fixture.contract, fixture.policyID, fixture.activeProviders); err != nil {
		t.Fatal(err)
	}
	slot := currentPollSlot(t, ctx, database.owner)
	pollRunID := uuid.New()
	_, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs (
		poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
		provider_policy_version,max_attempts,poll_start_grace_seconds,created_at
	) VALUES ($1,$2,$3,$4,$5,$6,2,299,clock_timestamp())`, pollRunID,
		fixture.instanceID, fixture.nodeType, fixture.contract, slot, fixture.policyID)
	if err != nil {
		t.Fatal(err)
	}
	repository, err := pollstore.NewInventoryPollRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.New()
	claim, err := repository.ClaimRunnable(ctx, inventorypoll.ClaimRequest{
		Token: token, LeaseDuration: 30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if claim.PollRunID != pollRunID || claim.FencingToken != token || claim.Attempt != 1 ||
		claim.GraceRemaining <= 0 || claim.PolicyVersionID != fixture.policyID {
		t.Fatalf("unexpected claim metadata: poll=%s attempt=%d", claim.PollRunID, claim.Attempt)
	}
	providers := make([]inventorypoll.ProviderEvidence, 0, len(fixture.activeProviders))
	for _, provider := range fixture.activeProviders {
		providers = append(providers, inventorypoll.ProviderEvidence{
			Provider: provider, IdentityComplete: true, SnapshotComplete: false,
			Degraded: true, Reason: inventorypoll.ProviderReasonTransportFailed,
		})
	}
	request := inventorypoll.FinalizeRequest{
		PollRunID: pollRunID, FencingToken: token,
		Node: inventorypoll.NodeEvidence{
			Result: drivers.ResultFailed, Reason: drivers.ReasonTimeout,
			Version: "unknown", Commit: "unknown", Degraded: true,
		},
		Providers: providers,
	}
	missingProvider := request
	missingProvider.Providers = append([]inventorypoll.ProviderEvidence(nil), providers[:len(providers)-1]...)
	if err := repository.FinalizeFenced(ctx, missingProvider); err == nil {
		t.Fatal("finalize unexpectedly accepted an incomplete pinned provider set")
	}
	var preFinalizeStatus string
	var preFinalizeProviders int
	if err := database.owner.QueryRow(ctx, `SELECT status,
		(SELECT count(*) FROM account_inventory_poll_provider_results WHERE poll_run_id=$1)
		FROM account_inventory_poll_runs WHERE poll_run_id=$1`, pollRunID).
		Scan(&preFinalizeStatus, &preFinalizeProviders); err != nil {
		t.Fatal(err)
	}
	if preFinalizeStatus != "running" || preFinalizeProviders != 0 {
		t.Fatalf("failed finalize left partial evidence: %s/%d", preFinalizeStatus, preFinalizeProviders)
	}
	if err := repository.FinalizeFenced(ctx, request); err != nil {
		t.Fatal(err)
	}
	if err := repository.FinalizeFenced(ctx, request); !errors.Is(err, inventorypoll.ErrLostLease) {
		t.Fatalf("stale finalize error = %v", err)
	}
	var status string
	var observedAt time.Time
	var providerCount int
	if err := database.owner.QueryRow(ctx, `SELECT status,observed_at,
		(SELECT count(*) FROM account_inventory_poll_provider_results WHERE poll_run_id=$1)
		FROM account_inventory_poll_runs WHERE poll_run_id=$1`, pollRunID).
		Scan(&status, &observedAt, &providerCount); err != nil {
		t.Fatal(err)
	}
	if status != "finalized" || observedAt.IsZero() || providerCount != len(fixture.activeProviders) {
		t.Fatalf("finalized evidence = %s/%v/%d", status, observedAt, providerCount)
	}
}

func TestInventoryPollSchedulerPinsPolicyAndReconcilesWithStoredGrace(t *testing.T) {
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
	if secondsIntoSlot > 290 {
		t.Skip("current database slot has insufficient deterministic grace for lease-expiry test")
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

	repository, err := pollstore.NewInventoryPollRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	scheduleRequest := inventorypoll.ScheduleRequest{
		Period: 5 * time.Minute, PollStartGrace: 299 * time.Second, MaxAttempts: 2, Limit: 50,
	}
	first, err := repository.ScheduleCurrent(ctx, scheduleRequest)
	if err != nil {
		t.Fatal(err)
	}
	second, err := repository.ScheduleCurrent(ctx, scheduleRequest)
	if err != nil {
		t.Fatal(err)
	}
	if first.Created != 1 || first.Eligible != 1 || second.Created != 0 || second.Existing != 1 ||
		!first.ScheduledAt.Equal(second.ScheduledAt) {
		t.Fatalf("scheduler idempotency = first=%+v second=%+v", first, second)
	}

	var switchAt time.Time
	if err := database.owner.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&switchAt); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `UPDATE provider_inventory_policy_activations
		SET effective_to=$1 WHERE policy_version_id=$2`, switchAt, firstPolicyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO provider_inventory_policy_activations(
		node_type,driver_contract_version,policy_version_id,effective_from,activated_by,created_at
	) VALUES ($1,$2,$3,$4,'integration-test',$4)`, nodeType, contract, secondPolicyID, switchAt); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `UPDATE provider_inventory_policy_bindings
		SET policy_version_id=$1,bound_at=$2 WHERE node_type=$3 AND driver_contract_version=$4`,
		secondPolicyID, switchAt, nodeType, contract); err != nil {
		t.Fatal(err)
	}

	claim, err := repository.ClaimRunnable(ctx, inventorypoll.ClaimRequest{Token: uuid.New(), LeaseDuration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if claim.PolicyVersionID != firstPolicyID || len(claim.ProviderPolicy.ActiveProviders) != 1 ||
		claim.ProviderPolicy.ActiveProviders[0] != "openai" {
		t.Fatalf("claim did not retain pinned policy: %+v", claim.ProviderPolicy)
	}
	time.Sleep(1100 * time.Millisecond)
	reconciled, err := repository.ReconcileExpired(ctx, inventorypoll.ReconcileRequest{
		// Deliberately differs from the stored 299s. PostgreSQL must classify
		// using the poll run's immutable grace, not this current config value.
		PollStartGrace: time.Second, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.RetryWait != 1 || reconciled.Abandoned != 0 {
		t.Fatalf("reconcile ignored stored grace: %+v", reconciled)
	}

	secondNodeID := uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_assets(
		instance_id,display_name,node_type,driver_contract_version,management_endpoint,reader_secret_ref
	) VALUES ($1,'Second Poll Scheduler Node',$2,$3,'http://poll-node-two.example:8317',
	          'file://poll-test/second-management-key')`, secondNodeID, nodeType, contract); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO node_capabilities(
		instance_id,node_type,driver_contract_version,capability
	) VALUES ($1,$2,$3,'management_account_inventory_read')`, secondNodeID, nodeType, contract); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_inventory_monitoring_activations(
		instance_id,effective_from,reason,actor,created_at
	) VALUES ($1,$2,'deployment_enable','integration-test',$2)`, secondNodeID, slot); err != nil {
		t.Fatal(err)
	}
	overCapacity := scheduleRequest
	overCapacity.Limit = 1
	if _, err := repository.ScheduleCurrent(ctx, overCapacity); err == nil {
		t.Fatal("scheduler unexpectedly accepted eligible Nodes above configured capacity")
	}
	var pollRunCount int
	if err := database.owner.QueryRow(ctx, `SELECT count(*) FROM account_inventory_poll_runs`).Scan(&pollRunCount); err != nil {
		t.Fatal(err)
	}
	if pollRunCount != 1 {
		t.Fatalf("capacity rejection changed poll runs: %d", pollRunCount)
	}
}

func TestAccountInventoryPollRuntimeRoleHasNoDirectWriteSurface(t *testing.T) {
	conn := connectRuntimeDatabase(t)
	ctx := context.Background()
	for name, statement := range map[string]string{
		"insert":   `INSERT INTO account_inventory_poll_runs DEFAULT VALUES`,
		"update":   `UPDATE account_inventory_poll_runs SET status='pending' WHERE false`,
		"delete":   `DELETE FROM account_inventory_poll_runs WHERE false`,
		"truncate": `TRUNCATE account_inventory_poll_provider_results, account_inventory_poll_runs`,
	} {
		_, err := conn.Exec(ctx, statement)
		requireRejected(t, err, "runtime direct poll "+name)
	}
}

func TestAccountInventoryPollMigrationDownRefusesEvidence(t *testing.T) {
	database := newIsolatedJobDatabase(t)
	ctx := context.Background()
	instanceID, policyID := uuid.New(), uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO node_drivers(
		node_type,driver_contract_version,display_name
	) VALUES ('poll-down-test','v1','Poll Down Test')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_assets(
		instance_id,display_name,node_type,driver_contract_version,
		management_endpoint,reader_secret_ref
	) VALUES ($1,'Poll Down Node','poll-down-test','v1','http://node.example',
		'docker-secret://synthetic/node-reader')`, instanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO provider_inventory_policy_versions(
		policy_version_id,node_type,driver_contract_version,active_providers,
		out_of_scope_providers,created_by
	) VALUES ($1,'poll-down-test','v1',ARRAY['openai'],ARRAY['gemini'],'integration-test')`, policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(
		instance_id,node_type,driver_contract_version,scheduled_at,
		provider_policy_version,created_at
	) VALUES ($1,'poll-down-test','v1',to_timestamp(
		floor(extract(epoch FROM clock_timestamp())/300)*300
	),$2,clock_timestamp())`, instanceID, policyID); err != nil {
		t.Fatal(err)
	}
	if err := runAssetGoose(t, ctx, "../..", database.ownerURL, "down"); err == nil {
		t.Fatal("migration down unexpectedly removed nonempty poll evidence")
	}
	var present bool
	if err := database.owner.QueryRow(ctx, `SELECT to_regclass(
		'public.account_inventory_poll_runs') IS NOT NULL`).Scan(&present); err != nil {
		t.Fatal(err)
	}
	if !present {
		t.Fatal("failed migration down removed poll evidence table")
	}
}
