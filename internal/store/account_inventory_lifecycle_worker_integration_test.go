package store_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sunxu/relay-station-control/internal/drivers"
	"github.com/sunxu/relay-station-control/internal/inventorypoll"
	pollstore "github.com/sunxu/relay-station-control/internal/store"
)

type lifecycleWorkerFixture struct {
	instanceID uuid.UUID
	nodeType   string
	contract   string
	baseSlot   time.Time
	nextPoll   int
}

type lifecycleSequenceDriver struct {
	mu          sync.Mutex
	observation drivers.InventoryObservation
	calls       atomic.Int32
}

func (driver *lifecycleSequenceDriver) ListAccountInventory(
	_ context.Context, _ drivers.InventoryRequest,
) (drivers.InventoryObservation, error) {
	driver.calls.Add(1)
	driver.mu.Lock()
	defer driver.mu.Unlock()
	return driver.observation, nil
}

type lifecycleClaimStore struct {
	database  *isolatedJobDatabase
	store     *pollstore.InventoryPollRepository
	claim     inventorypoll.ClaimedRun
	claimOnce atomic.Bool
	finalized chan error
}

func (*lifecycleClaimStore) ScheduleCurrent(context.Context, inventorypoll.ScheduleRequest) (inventorypoll.ScheduleResult, error) {
	return inventorypoll.ScheduleResult{}, nil
}

func (repository *lifecycleClaimStore) ClaimRunnable(
	ctx context.Context, request inventorypoll.ClaimRequest,
) (*inventorypoll.ClaimedRun, error) {
	if !repository.claimOnce.CompareAndSwap(false, true) {
		return nil, inventorypoll.ErrNoWork
	}
	if _, err := repository.database.owner.Exec(ctx, `UPDATE account_inventory_poll_runs
		SET status='running',attempt_count=1,first_started_at=clock_timestamp(),
		last_started_at=clock_timestamp(),lease_expires_at=clock_timestamp()+interval '30 seconds',
		lease_fencing_token=$2 WHERE poll_run_id=$1 AND status='pending'`,
		repository.claim.PollRunID, request.Token); err != nil {
		return nil, err
	}
	claim := repository.claim
	claim.FencingToken = request.Token
	return &claim, nil
}

func (repository *lifecycleClaimStore) FinalizeFenced(
	ctx context.Context, request inventorypoll.FinalizeRequest,
) error {
	err := repository.store.FinalizeFenced(ctx, request)
	select {
	case repository.finalized <- err:
	default:
	}
	return err
}

func (*lifecycleClaimStore) ReconcileExpired(context.Context, inventorypoll.ReconcileRequest) (inventorypoll.ReconcileResult, error) {
	return inventorypoll.ReconcileResult{}, nil
}

func TestAccountInventoryLifecycleFakeDriverRealStoreSequence(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleWorkerFixture(t, ctx, database)
	driver := &lifecycleSequenceDriver{}
	requests := int32(0)

	accountA := drivers.AccountObservation{
		Provider: "openai", Email: "sequence-a@example.invalid",
		State: drivers.AccountStateActive, OccurrenceCount: 1, SuccessCount: 1,
	}
	accountB := drivers.AccountObservation{
		Provider: "legacy", Email: "sequence-b@example.invalid",
		State: drivers.AccountStateActive, OccurrenceCount: 1, SuccessCount: 2,
	}

	run := func(observation drivers.InventoryObservation) uuid.UUID {
		t.Helper()
		driver.mu.Lock()
		driver.observation = observation
		driver.mu.Unlock()
		pollID := runLifecycleWorkerPoll(t, ctx, database, fixture, driver)
		requests++
		if driver.calls.Load() != requests {
			t.Fatalf("fake Driver calls=%d want=%d", driver.calls.Load(), requests)
		}
		return pollID
	}

	run(lifecycleCompleteObservation(map[string][]drivers.AccountObservation{
		"openai": {accountA}, "legacy": {accountB},
	}))
	assertLifecycleWorkerStates(t, ctx, database, fixture, map[string]string{
		"openai:sequence-a@example.invalid": "present",
		"legacy:sequence-b@example.invalid": "present",
	})

	run(lifecycleCompleteObservation(map[string][]drivers.AccountObservation{"openai": {}, "legacy": {}}))
	assertLifecycleWorkerStates(t, ctx, database, fixture, map[string]string{
		"openai:sequence-a@example.invalid": "suspected_missing",
		"legacy:sequence-b@example.invalid": "suspected_missing",
	})
	run(lifecycleCompleteObservation(map[string][]drivers.AccountObservation{"openai": {}, "legacy": {}}))
	assertLifecycleWorkerStates(t, ctx, database, fixture, map[string]string{
		"openai:sequence-a@example.invalid": "missing",
		"legacy:sequence-b@example.invalid": "missing",
	})
	run(lifecycleCompleteObservation(map[string][]drivers.AccountObservation{
		"openai": {accountA}, "legacy": {accountB},
	}))
	assertLifecycleWorkerStates(t, ctx, database, fixture, map[string]string{
		"openai:sequence-a@example.invalid": "present",
		"legacy:sequence-b@example.invalid": "present",
	})

	independentPoll := run(lifecycleIndependentObservation(accountA))
	var openAIApplied, legacyApplied bool
	var legacyReason *string
	if err := database.owner.QueryRow(ctx, `SELECT
		bool_or(promotion_applied) FILTER (WHERE provider='openai'),
		bool_or(promotion_applied) FILTER (WHERE provider='legacy'),
		max(promotion_skipped_reason) FILTER (WHERE provider='legacy')
		FROM account_inventory_poll_provider_results WHERE poll_run_id=$1`, independentPoll).
		Scan(&openAIApplied, &legacyApplied, &legacyReason); err != nil {
		t.Fatal(err)
	}
	if !openAIApplied || legacyApplied || legacyReason == nil || *legacyReason != "provider_identity_incomplete" {
		reason := "none"
		if legacyReason != nil {
			reason = *legacyReason
		}
		t.Fatalf("multi-Provider promotion applied=%t/%t legacy_reason=%s",
			openAIApplied, legacyApplied, reason)
	}

	for _, skipped := range lifecycleSkippedObservations() {
		t.Run(skipped.name, func(t *testing.T) {
			before := lifecycleWorkerStateCounts(t, ctx, database, fixture)
			run(skipped.observation)
			after := lifecycleWorkerStateCounts(t, ctx, database, fixture)
			if before != after {
				t.Fatalf("skipped fake Driver observation changed lifecycle: before=%v after=%v", before, after)
			}
		})
	}

	activateLifecycleWorkerPolicy(t, ctx, database, fixture, []string{"openai"}, []string{"legacy"}, "fake-driver-oos")
	if driver.calls.Load() != requests {
		t.Fatal("Provider scope activation issued a Driver request")
	}
	assertLifecycleWorkerStates(t, ctx, database, fixture, map[string]string{
		"openai:sequence-a@example.invalid": "present",
		"legacy:sequence-b@example.invalid": "out_of_scope",
	})
	activateLifecycleWorkerPolicy(t, ctx, database, fixture, []string{"openai", "legacy"}, []string{}, "fake-driver-readd")
	if driver.calls.Load() != requests {
		t.Fatal("Provider re-add issued a Driver request")
	}
	assertLifecycleWorkerStates(t, ctx, database, fixture, map[string]string{
		"openai:sequence-a@example.invalid": "present",
		"legacy:sequence-b@example.invalid": "out_of_scope",
	})
	run(lifecycleCompleteObservation(map[string][]drivers.AccountObservation{
		"openai": {accountA}, "legacy": {accountB},
	}))
	assertLifecycleWorkerStates(t, ctx, database, fixture, map[string]string{
		"openai:sequence-a@example.invalid": "present",
		"legacy:sequence-b@example.invalid": "present",
	})
}

func newLifecycleWorkerFixture(
	t *testing.T, ctx context.Context, database *isolatedJobDatabase,
) *lifecycleWorkerFixture {
	t.Helper()
	fixture := &lifecycleWorkerFixture{
		instanceID: uuid.New(), nodeType: string(drivers.NodeTypeCLIProxyAPI),
		contract: string(drivers.DriverContractCLIProxyAPIAuthFilesV1),
		baseSlot: time.Unix(300*1000, 0).UTC(),
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO node_drivers(
		node_type,driver_contract_version,display_name
	) VALUES ($1,$2,'Lifecycle Worker Driver')`, fixture.nodeType, fixture.contract); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO relay_node_assets(
		instance_id,display_name,node_type,driver_contract_version,management_endpoint,reader_secret_ref
	) VALUES ($1,'Lifecycle Worker Node',$2,$3,'http://worker.example',
		'docker-secret://synthetic/lifecycle-worker')`, fixture.instanceID, fixture.nodeType, fixture.contract); err != nil {
		t.Fatal(err)
	}
	policyID := uuid.New()
	if _, err := database.owner.Exec(ctx, `INSERT INTO provider_inventory_policy_versions(
		policy_version_id,node_type,driver_contract_version,active_providers,out_of_scope_providers,created_by
	) VALUES ($1,$2,$3,ARRAY['legacy','openai'],ARRAY[]::text[],'fake-driver-test')`,
		policyID, fixture.nodeType, fixture.contract); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO provider_inventory_policy_bindings(
		node_type,driver_contract_version,policy_version_id,bound_by,bound_at
	) VALUES ($1,$2,$3,'fake-driver-test',clock_timestamp())`, fixture.nodeType, fixture.contract, policyID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.owner.Exec(ctx, `INSERT INTO provider_inventory_policy_activations(
		node_type,driver_contract_version,policy_version_id,effective_from,activated_by,created_at
	) VALUES ($1,$2,$3,clock_timestamp(),'fake-driver-test',CURRENT_TIMESTAMP)`,
		fixture.nodeType, fixture.contract, policyID); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func runLifecycleWorkerPoll(
	t *testing.T, ctx context.Context, database *isolatedJobDatabase,
	fixture *lifecycleWorkerFixture, driver *lifecycleSequenceDriver,
) uuid.UUID {
	t.Helper()
	store, err := pollstore.NewInventoryPollRepository(database.runtime)
	if err != nil {
		t.Fatal(err)
	}
	var policyID uuid.UUID
	var active, outOfScope []string
	if err := database.owner.QueryRow(ctx, `SELECT policy.policy_version_id,
		policy.active_providers,policy.out_of_scope_providers
		FROM provider_inventory_policy_bindings AS binding
		JOIN provider_inventory_policy_versions AS policy USING (policy_version_id)
		WHERE binding.node_type=$1 AND binding.driver_contract_version=$2`, fixture.nodeType, fixture.contract).
		Scan(&policyID, &active, &outOfScope); err != nil {
		t.Fatal(err)
	}
	pollID := uuid.New()
	scheduledAt := fixture.baseSlot.Add(time.Duration(fixture.nextPoll) * 5 * time.Minute)
	fixture.nextPoll++
	if _, err := database.owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(
		poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
		provider_policy_version,max_attempts,poll_start_grace_seconds,created_at
	) VALUES ($1,$2,$3,$4,$5,$6,2,299,clock_timestamp())`, pollID, fixture.instanceID,
		fixture.nodeType, fixture.contract, scheduledAt, policyID); err != nil {
		t.Fatal(err)
	}
	repository := &lifecycleClaimStore{database: database, store: store, finalized: make(chan error, 1), claim: inventorypoll.ClaimedRun{
		PollRunID: pollID, InstanceID: fixture.instanceID, PolicyVersionID: policyID,
		ScheduledAt: scheduledAt, Attempt: 1, MaxAttempts: 2, GraceRemaining: 30 * time.Second,
		Target: drivers.NodeTarget{
			InstanceID: fixture.instanceID, NodeType: drivers.NodeTypeCLIProxyAPI,
			DriverContractVersion: drivers.DriverContractCLIProxyAPIAuthFilesV1,
			ManagementEndpoint:    "http://worker.example",
			ReaderSecretReference: drivers.NewSecretReference("docker-secret://synthetic/lifecycle-worker"),
			Capabilities:          []drivers.Capability{drivers.CapabilityManagementAccountInventoryRead},
		},
		ProviderPolicy: drivers.ProviderPolicySnapshot{
			VersionID: policyID, ActiveProviders: active, OutOfScopeProviders: outOfScope,
		},
	}}
	worker, err := inventorypoll.NewWorker(repository, driver, inventorypoll.Config{
		PollStartGrace: 30 * time.Second, MaxMonitoredNodes: 1, Concurrency: 1,
		WorstCasePollDuration: time.Second, LeaseDuration: 15 * time.Second,
		DispatchMargin: time.Second, FinalizeMargin: time.Second,
		SchedulerInterval: time.Second, WorkerScanInterval: 5 * time.Millisecond,
		ReconcileInterval: 100 * time.Millisecond, DatabaseBackoffInitial: 5 * time.Millisecond,
		DatabaseBackoffMaximum: 20 * time.Millisecond, ShutdownGrace: time.Second,
		ScheduleLimit: 1, ReconcileLimit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	workerContext, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- worker.Run(workerContext) }()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case finalizeErr := <-repository.finalized:
			if finalizeErr != nil {
				cancel()
				_ = <-done
				t.Fatalf("real Store finalize rejected fake Driver observation: %v", finalizeErr)
			}
		default:
		}
		var status string
		if err := database.owner.QueryRow(ctx, `SELECT status FROM account_inventory_poll_runs WHERE poll_run_id=$1`, pollID).
			Scan(&status); err != nil {
			cancel()
			t.Fatal(err)
		}
		if status == "finalized" {
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			return pollID
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	_ = <-done
	t.Fatal("fake Driver observation was not finalized by the real Store")
	return uuid.Nil
}

func lifecycleCompleteObservation(accounts map[string][]drivers.AccountObservation) drivers.InventoryObservation {
	observation := drivers.InventoryObservation{
		TransportSuccess: true, ResponseShapeValid: true, ContractValid: true,
		Mode: drivers.InventoryModeRuntime, Version: "v7.2.141", Commit: "abcdef1",
		NodeIdentityComplete: true, Result: drivers.ResultSuccess, Reason: drivers.ReasonNone,
	}
	for _, provider := range []string{"legacy", "openai"} {
		observation.Providers = append(observation.Providers, drivers.ProviderObservation{
			Provider: provider, SnapshotComplete: true, Accounts: accounts[provider],
		})
	}
	return observation
}

func lifecycleIndependentObservation(account drivers.AccountObservation) drivers.InventoryObservation {
	observation := lifecycleCompleteObservation(map[string][]drivers.AccountObservation{"openai": {account}})
	observation.Result = drivers.ResultDegraded
	observation.UnidentifiedRecordCount = 1
	observation.Providers[0] = drivers.ProviderObservation{
		Provider: "legacy", SnapshotComplete: false, Degraded: true, MissingIdentityCount: 1,
	}
	return observation
}

type lifecycleSkippedObservation struct {
	name        string
	observation drivers.InventoryObservation
}

func lifecycleSkippedObservations() []lifecycleSkippedObservation {
	transport := drivers.InventoryObservation{Result: drivers.ResultFailed, Reason: drivers.ReasonNetworkUnavailable}
	contract := drivers.InventoryObservation{
		TransportSuccess: true, ResponseShapeValid: true,
		Result: drivers.ResultFailed, Reason: drivers.ReasonContractInvalid,
	}
	disk := lifecycleCompleteObservation(map[string][]drivers.AccountObservation{"legacy": {}, "openai": {}})
	disk.Mode, disk.Result = drivers.InventoryModeDiskFallback, drivers.ResultDegraded
	identity := lifecycleCompleteObservation(map[string][]drivers.AccountObservation{"legacy": {}, "openai": {}})
	identity.NodeIdentityComplete, identity.UnidentifiedRecordCount, identity.Result = false, 1, drivers.ResultDegraded
	duplicate := lifecycleCompleteObservation(map[string][]drivers.AccountObservation{})
	duplicate.Result = drivers.ResultDegraded
	for index, provider := range []string{"legacy", "openai"} {
		email := provider + "-duplicate@example.invalid"
		duplicate.Providers[index] = drivers.ProviderObservation{
			Provider: provider, Degraded: true, DuplicateIdentityCount: 1,
			Accounts: []drivers.AccountObservation{
				{Provider: provider, Email: email, State: drivers.AccountStateActive, OccurrenceCount: 2},
				{Provider: provider, Email: email, State: drivers.AccountStateActive, OccurrenceCount: 2},
			},
		}
	}
	return []lifecycleSkippedObservation{
		{name: "transport", observation: transport},
		{name: "contract", observation: contract},
		{name: "disk", observation: disk},
		{name: "identity", observation: identity},
		{name: "duplicate", observation: duplicate},
	}
}

func lifecycleWorkerStateCounts(
	t *testing.T, ctx context.Context, database *isolatedJobDatabase, fixture *lifecycleWorkerFixture,
) [4]int {
	t.Helper()
	var counts [4]int
	if err := database.owner.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE lifecycle='present'),
		count(*) FILTER (WHERE lifecycle='suspected_missing'),
		count(*) FILTER (WHERE lifecycle='missing'),
		count(*) FILTER (WHERE lifecycle='out_of_scope')
		FROM account_inventory WHERE instance_id=$1`, fixture.instanceID).
		Scan(&counts[0], &counts[1], &counts[2], &counts[3]); err != nil {
		t.Fatal(err)
	}
	return counts
}

func assertLifecycleWorkerStates(
	t *testing.T, ctx context.Context, database *isolatedJobDatabase,
	fixture *lifecycleWorkerFixture, expected map[string]string,
) {
	t.Helper()
	for accountKey, lifecycle := range expected {
		var actual string
		if err := database.owner.QueryRow(ctx, `SELECT lifecycle FROM account_inventory
			WHERE instance_id=$1 AND account_key=$2`, fixture.instanceID, accountKey).Scan(&actual); err != nil {
			t.Fatal(err)
		}
		if actual != lifecycle {
			t.Fatalf("account lifecycle=%s want=%s", actual, lifecycle)
		}
	}
}

func activateLifecycleWorkerPolicy(
	t *testing.T, ctx context.Context, database *isolatedJobDatabase,
	fixture *lifecycleWorkerFixture, active, outOfScope []string, reason string,
) {
	t.Helper()
	var activation uuid.UUID
	if err := database.owner.QueryRow(ctx, `SELECT public.control_activate_provider_policy_with_lifecycle(
		$1,$2,$3::text[],$4::text[],'fake-driver-test',$5,NULL
	)`, fixture.nodeType, fixture.contract, active, outOfScope, reason).Scan(&activation); err != nil {
		t.Fatal(err)
	}
	if activation == uuid.Nil {
		t.Fatal(errors.New("policy activation did not return an identifier"))
	}
}
