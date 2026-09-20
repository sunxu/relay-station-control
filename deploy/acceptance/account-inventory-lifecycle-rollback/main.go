package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sunxu/relay-station-control/internal/drivers"
	"github.com/sunxu/relay-station-control/internal/inventorypoll"
	controlstore "github.com/sunxu/relay-station-control/internal/store"
)

type seedFailure struct {
	checkpoint string
	class      string
	err        error
}

func (failure *seedFailure) Error() string { return failure.class }
func (failure *seedFailure) Unwrap() error { return failure.err }

func databaseErrorClass(err error) string {
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) {
		return "database_failure"
	}
	switch pgError.Code {
	case "23503":
		return "reference_violation"
	case "23505":
		return "duplicate"
	case "23514", "23P01":
		return "constraint_violation"
	case "42501":
		return "permission_denied"
	case "57014":
		return "statement_timeout"
	case "40001":
		return "serialization_failure"
	default:
		return "sqlstate_" + pgError.Code
	}
}

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
	fixtureAuditID    = uuid.MustParse("00000000-0000-4000-8000-000000000829")
	fixtureAuditActor = uuid.MustParse("00000000-0000-4000-8000-000000000830")
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
		var failure *seedFailure
		if errors.As(err, &failure) {
			fmt.Fprintf(os.Stderr, "account_inventory_lifecycle_rollback_harness=diagnostic checkpoint=%s class=%s\n", failure.checkpoint, failure.class)
		}
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
		return &seedFailure{checkpoint: "seed.transaction_begin", class: databaseErrorClass(err), err: err}
	}
	defer transaction.Rollback(context.Background())
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO environments(singleton_id,environment_id,name,environment_type)
			VALUES (1,'rollback-acceptance','Rollback Acceptance','dev')`, nil},
		{`INSERT INTO control_admin_users(admin_id,login_name,display_name)
			VALUES ($1,'rollback_audit','Rollback Audit Fixture')`, []any{fixtureAuditActor}},
		{`INSERT INTO node_drivers(node_type,driver_contract_version,display_name)
			VALUES ($1,$2,'Rollback Snapshot Driver')`, []any{fixtureNodeType, fixtureContract}},
		{`INSERT INTO driver_capabilities(node_type,driver_contract_version,capability)
			VALUES ($1,$2,'management_account_inventory_read')`, []any{fixtureNodeType, fixtureContract}},
		{`INSERT INTO relay_node_assets(instance_id,display_name,node_type,
			driver_contract_version,management_endpoint,reader_secret_ref)
			VALUES ($1,'Rollback Snapshot Node',$2,$3,'http://127.0.0.1:9',
			'docker-secret://synthetic/rollback-reader')`, []any{fixtureInstanceID, fixtureNodeType, fixtureContract}},
		{`INSERT INTO node_capabilities(instance_id,node_type,driver_contract_version,capability)
			VALUES ($1,$2,$3,'management_account_inventory_read')`, []any{fixtureInstanceID, fixtureNodeType, fixtureContract}},
		{`INSERT INTO relay_node_inventory_monitoring_activations(
			instance_id,effective_from,reason,actor,created_at)
			VALUES ($1,clock_timestamp(),'deployment_enable','rollback-acceptance',clock_timestamp())`, []any{fixtureInstanceID}},
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
			VALUES ($1,$2,$3,clock_timestamp(),'rollback-acceptance',CURRENT_TIMESTAMP)`,
			[]any{fixtureNodeType, fixtureContract, fixturePolicyID}},
		{`INSERT INTO audit_logs(audit_id,category,action,result,actor_admin_id,
			source_fingerprint,request_id,details)
			VALUES ($1,'account_inventory','account_inventory.view','success',$2,
			decode(repeat('42',32),'hex'),'rollback-readonly-view',jsonb_build_object(
			'instance_id',$3::text,'provider_filter_used',false,
			'lifecycle_filter_used',false,'basic_status_filter_used',false,
			'email_filter_used',false,'cursor_used',false,'result_count',1))`,
			[]any{fixtureAuditID, fixtureAuditActor, fixtureInstanceID}},
	}
	checkpoints := []string{
		"seed.environment", "seed.admin_user", "seed.driver", "seed.driver_capability", "seed.asset",
		"seed.node_capability", "seed.monitoring_activation", "seed.policy", "seed.policy_binding",
		"seed.policy_activation", "seed.audit",
	}
	for index, statement := range statements {
		if _, err := transaction.Exec(ctx, statement.query, statement.args...); err != nil {
			return &seedFailure{checkpoint: checkpoints[index], class: databaseErrorClass(err), err: err}
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return &seedFailure{checkpoint: "seed.commit", class: databaseErrorClass(err), err: err}
	}

	var baseSlot time.Time
	if err := harness.owner.QueryRow(ctx, `SELECT date_bin(
		interval '5 minutes',clock_timestamp(),timestamptz '1970-01-01'
	)-interval '15 minutes'`).Scan(&baseSlot); err != nil {
		return &seedFailure{checkpoint: "seed.base_slot", class: databaseErrorClass(err), err: err}
	}
	if err := harness.insertRunningPoll(ctx, baselinePollID, baselineFence, baseSlot); err != nil {
		return &seedFailure{checkpoint: "seed.baseline_poll", class: databaseErrorClass(err), err: err}
	}
	if err := harness.finalize(ctx, baselinePollID, baselineFence, true); err != nil {
		return &seedFailure{checkpoint: "seed.baseline_finalize", class: databaseErrorClass(err), err: err}
	}
	if err := harness.insertRunningPoll(ctx, suspectedPollID, suspectedFence, baseSlot.Add(5*time.Minute)); err != nil {
		return &seedFailure{checkpoint: "seed.suspected_poll", class: databaseErrorClass(err), err: err}
	}
	if err := harness.finalize(ctx, suspectedPollID, suspectedFence, false); err != nil {
		return &seedFailure{checkpoint: "seed.suspected_finalize", class: databaseErrorClass(err), err: err}
	}
	if err := harness.verifyFrozen(ctx); err != nil {
		var failure *seedFailure
		if errors.As(err, &failure) {
			return failure
		}
		checkpoint := "seed.verify.query"
		switch err.Error() {
		case "rollback lifecycle state mismatch":
			checkpoint = "seed.verify.lifecycle_state"
		case "rollback poll pointer mismatch":
			checkpoint = "seed.verify.poll_pointer"
		case "rollback durable row count mismatch":
			checkpoint = "seed.verify.row_counts"
		case "rollback unexpected audit state":
			checkpoint = "seed.verify.unexpected_audit"
		case "rollback readonly audit state mismatch":
			checkpoint = "seed.verify.readonly_audit"
		}
		return &seedFailure{checkpoint: checkpoint, class: "guard_rejected", err: err}
	}
	return nil
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
	var missing, lifecycleRows, pollRows, finalizedRows, snapshotRows, policyRows, auditRows, viewAuditRows int
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
		(SELECT count(*) FROM account_inventory_scope_transition_audits WHERE node_type=$2),
		(SELECT count(*) FROM audit_logs WHERE audit_id=$3
		 AND category='account_inventory' AND action='account_inventory.view'
		 AND result='success' AND actor_admin_id=$4
		 AND request_id='rollback-readonly-view'
		 AND details->>'instance_id'=$1::text
		 AND details->>'result_count'='1')`,
		fixtureInstanceID, fixtureNodeType, fixtureAuditID, fixtureAuditActor).Scan(&lifecycle, &missing, &accountPoll,
		&providerPoll, &lifecycleRows, &pollRows, &finalizedRows, &snapshotRows,
		&policyRows, &auditRows, &viewAuditRows)
	if err != nil {
		return &seedFailure{checkpoint: "seed.verify.query", class: databaseErrorClass(err), err: err}
	}
	if lifecycle != "suspected_missing" || missing != 1 {
		return errors.New("rollback lifecycle state mismatch")
	}
	if accountPoll != baselinePollID || providerPoll != suspectedPollID {
		return errors.New("rollback poll pointer mismatch")
	}
	if lifecycleRows != 1 || pollRows != 2 || finalizedRows != 2 || snapshotRows != 1 || policyRows != 1 {
		return errors.New("rollback durable row count mismatch")
	}
	if auditRows != 0 {
		return errors.New("rollback unexpected audit state")
	}
	if viewAuditRows != 1 {
		return errors.New("rollback readonly audit state mismatch")
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
	var missing, lifecycleRows, pollRows, finalizedRows, snapshotRows, viewAuditRows int
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
		(SELECT count(*) FROM audit_logs WHERE audit_id=$2
		 AND category='account_inventory' AND action='account_inventory.view'
		 AND result='success' AND actor_admin_id=$3
		 AND request_id='rollback-readonly-view'
		 AND details->>'instance_id'=$1::text
		 AND details->>'result_count'='1')`,
		fixtureInstanceID, fixtureAuditID, fixtureAuditActor).Scan(&lifecycle, &missing, &accountPoll, &providerPoll,
		&lifecycleRows, &pollRows, &finalizedRows, &snapshotRows, &viewAuditRows)
	if err != nil {
		return err
	}
	if lifecycle != "missing" || missing != 2 || accountPoll != baselinePollID ||
		providerPoll != advancePollID || lifecycleRows != 1 || pollRows != 3 ||
		finalizedRows != 3 || snapshotRows != 1 || viewAuditRows != 1 {
		return errors.New("restored lifecycle did not advance exactly once")
	}
	return nil
}
