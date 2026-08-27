package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sunxu/relay-station-control/internal/drivers"
	"github.com/sunxu/relay-station-control/internal/inventorypoll"
	controlstore "github.com/sunxu/relay-station-control/internal/store"
)

const (
	ownerURLEnvironment   = "CONTROL_LIFECYCLE_ROLLBACK_OWNER_URL"
	runtimeURLEnvironment = "CONTROL_LIFECYCLE_ROLLBACK_RUNTIME_URL"
	fixtureProvider       = "openai"
	fixtureNodeType       = "rollback-snapshot-test"
	fixtureContract       = "v1"
	fixtureEmail          = "rollback-lifecycle@example.invalid"
)

var (
	fixtureInstanceID = uuid.MustParse("00000000-0000-4000-8000-000000000821")
	fixturePolicyID   = uuid.MustParse("00000000-0000-4000-8000-000000000822")
	baselinePollID    = uuid.MustParse("00000000-0000-4000-8000-000000000823")
	suspectedPollID   = uuid.MustParse("00000000-0000-4000-8000-000000000824")
	advancePollID     = uuid.MustParse("00000000-0000-4000-8000-000000000825")
	baselineFence     = uuid.MustParse("00000000-0000-4000-8000-000000000826")
	suspectedFence    = uuid.MustParse("00000000-0000-4000-8000-000000000827")
	advanceFence      = uuid.MustParse("00000000-0000-4000-8000-000000000828")
)

type rollbackHarness struct {
	owner      *pgxpool.Pool
	runtime    *pgxpool.Pool
	repository *controlstore.InventoryPollRepository
}

func main() {
	if len(os.Args) != 2 {
		fail("invalid_mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	harness, err := newRollbackHarness(ctx)
	if err != nil {
		fail("database_unavailable")
	}
	defer harness.close()

	switch os.Args[1] {
	case "prepare":
		err = harness.prepare(ctx)
	case "verify-frozen":
		err = harness.verifyFrozen(ctx)
	case "advance":
		err = harness.advance(ctx)
	case "replay":
		err = harness.replay(ctx)
	case "verify-advanced":
		err = harness.verifyAdvanced(ctx)
	default:
		fail("invalid_mode")
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			fail(os.Args[1] + "_timeout")
		}
		fail(os.Args[1] + "_failed")
	}
	fmt.Printf("account_inventory_lifecycle_rollback_harness=success phase=%s\n", os.Args[1])
}

func fail(reason string) {
	fmt.Fprintf(os.Stderr, "account_inventory_lifecycle_rollback_harness=failed reason=%s\n", reason)
	os.Exit(1)
}

func newRollbackHarness(ctx context.Context) (*rollbackHarness, error) {
	ownerURL, runtimeURL := os.Getenv(ownerURLEnvironment), os.Getenv(runtimeURLEnvironment)
	if ownerURL == "" || runtimeURL == "" {
		return nil, errors.New("database URL unavailable")
	}
	owner, err := pgxpool.New(ctx, ownerURL)
	if err != nil {
		return nil, err
	}
	runtime, err := pgxpool.New(ctx, runtimeURL)
	if err != nil {
		owner.Close()
		return nil, err
	}
	repository, err := controlstore.NewInventoryPollRepository(runtime)
	if err != nil {
		owner.Close()
		runtime.Close()
		return nil, err
	}
	return &rollbackHarness{owner: owner, runtime: runtime, repository: repository}, nil
}

func (harness *rollbackHarness) close() {
	harness.runtime.Close()
	harness.owner.Close()
}

func (harness *rollbackHarness) prepare(ctx context.Context) error {
	transaction, err := harness.owner.Begin(ctx)
	if err != nil {
		return err
	}
	defer transaction.Rollback(context.Background())
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO environments(singleton_id,environment_id,name,environment_type)
			VALUES (1,'rollback-acceptance','Rollback Acceptance','dev')`, nil},
		{`INSERT INTO node_drivers(node_type,driver_contract_version,display_name)
			VALUES ($1,$2,'Rollback Snapshot Driver')`, []any{fixtureNodeType, fixtureContract}},
		{`INSERT INTO relay_node_assets(instance_id,display_name,node_type,
			driver_contract_version,management_endpoint,reader_secret_ref)
			VALUES ($1,'Rollback Snapshot Node',$2,$3,'http://127.0.0.1:9',
			'docker-secret://synthetic/rollback-reader')`, []any{fixtureInstanceID, fixtureNodeType, fixtureContract}},
		{`INSERT INTO provider_inventory_policy_versions(policy_version_id,node_type,
			driver_contract_version,active_providers,out_of_scope_providers,created_by)
			VALUES ($1,$2,$3,ARRAY['openai'],ARRAY['legacy'],'rollback-acceptance')`,
			[]any{fixturePolicyID, fixtureNodeType, fixtureContract}},
		{`INSERT INTO provider_inventory_policy_bindings(node_type,driver_contract_version,
			policy_version_id,bound_by,bound_at)
			VALUES ($1,$2,$3,'rollback-acceptance',clock_timestamp())`,
			[]any{fixtureNodeType, fixtureContract, fixturePolicyID}},
		{`INSERT INTO provider_inventory_policy_activations(node_type,driver_contract_version,
			policy_version_id,effective_from,activated_by,created_at)
			VALUES ($1,$2,$3,clock_timestamp(),'rollback-acceptance',clock_timestamp())`,
			[]any{fixtureNodeType, fixtureContract, fixturePolicyID}},
	}
	for _, statement := range statements {
		if _, err := transaction.Exec(ctx, statement.query, statement.args...); err != nil {
			return err
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return err
	}

	var baseSlot time.Time
	if err := harness.owner.QueryRow(ctx, `SELECT date_bin(
		interval '5 minutes',clock_timestamp(),timestamptz '1970-01-01'
	)-interval '15 minutes'`).Scan(&baseSlot); err != nil {
		return err
	}
	if err := harness.insertRunningPoll(ctx, baselinePollID, baselineFence, baseSlot); err != nil {
		return err
	}
	if err := harness.finalize(ctx, baselinePollID, baselineFence, true); err != nil {
		return err
	}
	if err := harness.insertRunningPoll(ctx, suspectedPollID, suspectedFence, baseSlot.Add(5*time.Minute)); err != nil {
		return err
	}
	if err := harness.finalize(ctx, suspectedPollID, suspectedFence, false); err != nil {
		return err
	}
	return harness.verifyFrozen(ctx)
}

func (harness *rollbackHarness) insertRunningPoll(
	ctx context.Context, pollID, fence uuid.UUID, scheduledAt time.Time,
) error {
	_, err := harness.owner.Exec(ctx, `INSERT INTO account_inventory_poll_runs(
		poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
		provider_policy_version,status,attempt_count,max_attempts,poll_start_grace_seconds,
		created_at,first_started_at,last_started_at,lease_expires_at,lease_fencing_token
	) VALUES ($1,$2,$3,$4,$5,$6,'running',1,2,299,clock_timestamp(),
		clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '120 seconds',$7)`,
		pollID, fixtureInstanceID, fixtureNodeType, fixtureContract, scheduledAt,
		fixturePolicyID, fence)
	return err
}

func (harness *rollbackHarness) finalize(
	ctx context.Context, pollID, fence uuid.UUID, includeAccount bool,
) error {
	request := inventorypoll.FinalizeRequest{
		PollRunID: pollID, FencingToken: fence,
		Node: inventorypoll.NodeEvidence{
			TransportSuccess: true, ResponseShapeValid: true, ContractValid: true,
			InventoryMode: drivers.InventoryModeRuntime, NodeIdentityComplete: true,
			SnapshotComplete: true, Result: drivers.ResultSuccess, Reason: drivers.ReasonNone,
			Version: "v1.0.0", Commit: "abcdef1",
		},
		Providers: []inventorypoll.ProviderEvidence{{
			Provider: fixtureProvider, IdentityComplete: true, SnapshotComplete: true,
			Reason: inventorypoll.ProviderReasonComplete,
		}},
	}
	if includeAccount {
		request.Node.RecognizedRecordCount = 1
		request.Providers[0].RecognizedRecordCount = 1
		request.SnapshotItems = []inventorypoll.SnapshotCandidate{{
			Provider: fixtureProvider, AccountKey: fixtureProvider + ":" + fixtureEmail,
			Email: fixtureEmail, BasicStatus: drivers.AccountStateActive, SuccessCount: 9,
		}}
	}
	return harness.repository.FinalizeFenced(ctx, request)
}

func (harness *rollbackHarness) verifyFrozen(ctx context.Context) error {
	var lifecycle string
	var missing, lifecycleRows, pollRows, finalizedRows, snapshotRows, policyRows, auditRows int
	var accountPoll, providerPoll uuid.UUID
	err := harness.owner.QueryRow(ctx, `SELECT
		(SELECT lifecycle FROM account_inventory WHERE instance_id=$1),
		(SELECT consecutive_missing_count FROM account_inventory WHERE instance_id=$1),
		(SELECT current_poll_run_id FROM account_inventory WHERE instance_id=$1),
		(SELECT current_poll_run_id FROM account_inventory_provider_states
		 WHERE instance_id=$1 AND provider='openai'),
		(SELECT count(*) FROM account_inventory WHERE instance_id=$1),
		(SELECT count(*) FROM account_inventory_poll_runs WHERE instance_id=$1),
		(SELECT count(*) FROM account_inventory_poll_runs WHERE instance_id=$1 AND status='finalized'),
		(SELECT count(*) FROM account_inventory_snapshot_items WHERE instance_id=$1),
		(SELECT count(*) FROM provider_inventory_policy_versions WHERE node_type=$2),
		(SELECT count(*) FROM account_inventory_scope_transition_audits WHERE node_type=$2)`,
		fixtureInstanceID, fixtureNodeType).Scan(&lifecycle, &missing, &accountPoll,
		&providerPoll, &lifecycleRows, &pollRows, &finalizedRows, &snapshotRows,
		&policyRows, &auditRows)
	if err != nil {
		return err
	}
	if lifecycle != "suspected_missing" || missing != 1 || accountPoll != baselinePollID ||
		providerPoll != suspectedPollID || lifecycleRows != 1 || pollRows != 2 || finalizedRows != 2 ||
		snapshotRows != 1 || policyRows != 1 || auditRows != 0 {
		return errors.New("rollback state changed")
	}
	return nil
}

func (harness *rollbackHarness) advance(ctx context.Context) error {
	var scheduledAt time.Time
	if err := harness.owner.QueryRow(ctx, `SELECT scheduled_at+interval '5 minutes'
		FROM account_inventory_poll_runs WHERE poll_run_id=$1`, suspectedPollID).Scan(&scheduledAt); err != nil {
		return err
	}
	if err := harness.insertRunningPoll(ctx, advancePollID, advanceFence, scheduledAt); err != nil {
		return err
	}
	if err := harness.finalize(ctx, advancePollID, advanceFence, false); err != nil {
		return err
	}
	return harness.verifyAdvanced(ctx)
}

func (harness *rollbackHarness) replay(ctx context.Context) error {
	err := harness.finalize(ctx, advancePollID, advanceFence, false)
	if !errors.Is(err, inventorypoll.ErrLostLease) {
		return errors.New("finalized replay was not fenced")
	}
	return harness.verifyAdvanced(ctx)
}

func (harness *rollbackHarness) verifyAdvanced(ctx context.Context) error {
	var lifecycle string
	var missing, lifecycleRows, pollRows, finalizedRows, snapshotRows int
	var accountPoll, providerPoll uuid.UUID
	err := harness.owner.QueryRow(ctx, `SELECT
		(SELECT lifecycle FROM account_inventory WHERE instance_id=$1),
		(SELECT consecutive_missing_count FROM account_inventory WHERE instance_id=$1),
		(SELECT current_poll_run_id FROM account_inventory WHERE instance_id=$1),
		(SELECT current_poll_run_id FROM account_inventory_provider_states
		 WHERE instance_id=$1 AND provider='openai'),
		(SELECT count(*) FROM account_inventory WHERE instance_id=$1),
		(SELECT count(*) FROM account_inventory_poll_runs WHERE instance_id=$1),
		(SELECT count(*) FROM account_inventory_poll_runs WHERE instance_id=$1 AND status='finalized'),
		(SELECT count(*) FROM account_inventory_snapshot_items WHERE instance_id=$1)`,
		fixtureInstanceID).Scan(&lifecycle, &missing, &accountPoll, &providerPoll,
		&lifecycleRows, &pollRows, &finalizedRows, &snapshotRows)
	if err != nil {
		return err
	}
	if lifecycle != "missing" || missing != 2 || accountPoll != baselinePollID ||
		providerPoll != advancePollID || lifecycleRows != 1 || pollRows != 3 ||
		finalizedRows != 3 || snapshotRows != 1 {
		return errors.New("restored lifecycle did not advance exactly once")
	}
	return nil
}
