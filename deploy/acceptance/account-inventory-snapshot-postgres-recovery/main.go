package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sunxu/relay-station-control/internal/drivers"
	"github.com/sunxu/relay-station-control/internal/inventorypoll"
	pollstore "github.com/sunxu/relay-station-control/internal/store"
)

const (
	ownerURLEnvironment   = "CONTROL_SNAPSHOT_RECOVERY_OWNER_URL"
	runtimeURLEnvironment = "CONTROL_SNAPSHOT_RECOVERY_RUNTIME_URL"
	readyFileEnvironment  = "CONTROL_SNAPSHOT_RECOVERY_READY_FILE"

	fixtureNodeType = "snapshot-recovery-test"
	fixtureContract = "v1"
	fixtureProvider = "openai"
	fixtureEmail    = "snapshot-recovery@example.invalid"
)

var (
	errInvalidConfiguration = errors.New("invalid_configuration")
	errAcceptanceInvariant  = errors.New("acceptance_invariant")

	fixtureInstanceID = uuid.MustParse("10000000-0000-4000-8000-000000000001")
	timeoutInstanceID = uuid.MustParse("10000000-0000-4000-8000-000000000008")
	fixturePolicyID   = uuid.MustParse("10000000-0000-4000-8000-000000000002")
	fixturePollID     = uuid.MustParse("10000000-0000-4000-8000-000000000003")
	timeoutPollID     = uuid.MustParse("10000000-0000-4000-8000-000000000004")
	firstFence        = uuid.MustParse("10000000-0000-4000-8000-000000000005")
	secondFence       = uuid.MustParse("10000000-0000-4000-8000-000000000006")
	timeoutFence      = uuid.MustParse("10000000-0000-4000-8000-000000000007")
)

func main() {
	os.Exit(runMain())
}

func runMain() (exitCode int) {
	exitCode = 1
	phase := "invalid"
	defer func() {
		if recover() != nil {
			fmt.Fprintf(os.Stderr, "account_inventory_snapshot_postgres_recovery=failed phase=%s reason=acceptance_invariant\n", phase)
			exitCode = 1
		}
	}()
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "account_inventory_snapshot_postgres_recovery=failed phase=invalid reason=invalid_configuration")
		return 2
	}
	phase = os.Args[1]
	var err error
	switch phase {
	case "prepare":
		err = runPrepare()
	case "outage":
		err = runOutage()
	case "interrupted-finalize":
		err = runInterruptedFinalize()
	case "wait":
		err = runWait()
	case "recover":
		err = runRecover()
	case "transaction-timeout":
		err = runTransactionTimeout()
	case "exhaustion":
		err = runExhaustion()
	case "restart-verify":
		err = runRestartVerify()
	default:
		fmt.Fprintln(os.Stderr, "account_inventory_snapshot_postgres_recovery=failed phase=invalid reason=invalid_configuration")
		return 2
	}
	if err != nil {
		reason := "acceptance_invariant"
		if errors.Is(err, errInvalidConfiguration) {
			reason = "invalid_configuration"
		}
		fmt.Fprintf(os.Stderr, "account_inventory_snapshot_postgres_recovery=failed phase=%s reason=%s\n", phase, reason)
		return 1
	}
	return 0
}

func runWait() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := openPool(ctx, ownerURLEnvironment, 1, 250*time.Millisecond, 0)
	if err != nil {
		return err
	}
	defer pool.Close()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		pingContext, pingCancel := context.WithTimeout(ctx, 500*time.Millisecond)
		err := pool.Ping(pingContext)
		pingCancel()
		if err == nil {
			fmt.Println("account_inventory_snapshot_postgres_recovery=success phase=wait host_database=ready")
			return nil
		}
		select {
		case <-ctx.Done():
			return errAcceptanceInvariant
		case <-ticker.C:
		}
	}
}

func runPrepare() error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	owner, err := openPool(ctx, ownerURLEnvironment, 2, 2*time.Second, 0)
	if err != nil {
		return err
	}
	defer owner.Close()
	runtime, err := openPool(ctx, runtimeURLEnvironment, 2, 2*time.Second, 0)
	if err != nil {
		return err
	}
	defer runtime.Close()
	if err := seedFixture(ctx, owner); err != nil {
		return errAcceptanceInvariant
	}
	repository, err := pollstore.NewInventoryPollRepository(runtime)
	if err != nil {
		return errAcceptanceInvariant
	}
	claim, err := repository.ClaimRunnable(ctx, inventorypoll.ClaimRequest{
		Token: firstFence, LeaseDuration: 5 * time.Second,
	})
	if err != nil || claim == nil || claim.PollRunID != fixturePollID || claim.Attempt != 1 ||
		claim.FencingToken != firstFence {
		return errAcceptanceInvariant
	}
	fmt.Println("account_inventory_snapshot_postgres_recovery=success phase=prepare poll_count=1 attempt=1 lease_class=short")
	return nil
}

func runInterruptedFinalize() error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	owner, err := openPool(ctx, ownerURLEnvironment, 3, 2*time.Second, 0)
	if err != nil {
		return err
	}
	defer owner.Close()
	runtime, err := openNamedPool(ctx, runtimeURLEnvironment, 1, 2*time.Second, 0, "snapshot-recovery-interrupted-finalize")
	if err != nil {
		return err
	}
	defer runtime.Close()
	blocker, err := owner.Begin(ctx)
	if err != nil {
		return errAcceptanceInvariant
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	if _, err := blocker.Exec(ctx, `SELECT policy_version_id
		FROM provider_inventory_policy_bindings
		WHERE node_type=$1 AND driver_contract_version=$2 FOR UPDATE`, fixtureNodeType, fixtureContract); err != nil {
		return errAcceptanceInvariant
	}
	repository, err := pollstore.NewInventoryPollRepository(runtime)
	if err != nil {
		return errAcceptanceInvariant
	}
	finalizeDone := make(chan error, 1)
	go func() { finalizeDone <- repository.FinalizeFenced(ctx, completeFixture(firstFence, fixturePollID, 10)) }()
	if err := waitForApplicationLock(ctx, owner, "snapshot-recovery-interrupted-finalize"); err != nil {
		return errAcceptanceInvariant
	}
	readyFile := os.Getenv(readyFileEnvironment)
	if readyFile == "" || os.WriteFile(readyFile, []byte("ready\n"), 0o600) != nil {
		return errInvalidConfiguration
	}
	select {
	case finalizeErr := <-finalizeDone:
		if finalizeErr == nil {
			return errAcceptanceInvariant
		}
	case <-ctx.Done():
		return errAcceptanceInvariant
	}
	fmt.Println("account_inventory_snapshot_postgres_recovery=success phase=interrupted-finalize database_stop_observed=true transaction_result=rolled_back")
	return nil
}

func runOutage() error {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	probe, err := openPool(ctx, runtimeURLEnvironment, 1, 100*time.Millisecond, 0)
	if err != nil {
		return err
	}
	defer probe.Close()
	if pingErr := probe.Ping(ctx); pingErr == nil {
		return errAcceptanceInvariant
	}

	pool, err := openPool(context.Background(), runtimeURLEnvironment, 1, 100*time.Millisecond, 0)
	if err != nil {
		return err
	}
	defer pool.Close()
	repository, err := pollstore.NewInventoryPollRepository(pool)
	if err != nil {
		return errAcceptanceInvariant
	}
	driver := &countingDriver{}
	observer := &countingObserver{}
	service, err := inventorypoll.NewService(repository, driver, recoveryConfig(observer))
	if err != nil {
		return errAcceptanceInvariant
	}
	serviceContext, stopService := context.WithTimeout(context.Background(), 450*time.Millisecond)
	defer stopService()
	serviceDone := make(chan error, 1)
	go func() { serviceDone <- service.Run(serviceContext) }()
	dataPlaneDone := runDataPlaneSimulator(50)

	if err := awaitService(serviceDone, 2*time.Second); err != nil {
		return errAcceptanceInvariant
	}
	processed := <-dataPlaneDone
	attempts := observer.reconcileFailures.Load()
	if processed != 50 || driver.calls.Load() != 0 || attempts < 1 || attempts > 10 {
		return errAcceptanceInvariant
	}
	fmt.Printf("account_inventory_snapshot_postgres_recovery=success phase=outage startup_barrier=canceled driver_calls=0 reconcile_failures=%d synthetic_data_plane_http=50/50\n", attempts)
	return nil
}

func runRecover() error {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	owner, err := openPool(ctx, ownerURLEnvironment, 2, 2*time.Second, 0)
	if err != nil {
		return err
	}
	defer owner.Close()
	runtime, err := openPool(ctx, runtimeURLEnvironment, 2, 2*time.Second, 0)
	if err != nil {
		return err
	}
	defer runtime.Close()
	if err := owner.Ping(ctx); err != nil {
		return acceptanceCheckpoint("recover", "owner_ping")
	}
	var schemaAvailable bool
	if err := owner.QueryRow(ctx, `SELECT to_regclass('public.account_inventory_poll_runs') IS NOT NULL`).Scan(&schemaAvailable); err != nil {
		return acceptanceCheckpoint("recover", "schema_probe")
	}
	if !schemaAvailable {
		return acceptanceCheckpoint("recover", "schema_missing")
	}
	counts, err := pollCounts(ctx, owner, fixturePollID, fixtureInstanceID)
	if err != nil || counts != (boundedCounts{runs: 1}) {
		fmt.Fprintf(os.Stderr, "account_inventory_snapshot_postgres_recovery_counts=failed phase=recover runs=%d finalized=%d providers=%d snapshots=%d states=%d\n",
			counts.runs, counts.finalized, counts.providers, counts.snapshots, counts.states)
		return acceptanceCheckpoint("recover", "stop_rollback")
	}
	if err := waitForExpiredLease(ctx, owner); err != nil {
		return acceptanceCheckpoint("recover", "lease_expiry")
	}
	repository, err := pollstore.NewInventoryPollRepository(runtime)
	if err != nil {
		return errAcceptanceInvariant
	}
	reconciled, err := repository.ReconcileExpired(ctx, inventorypoll.ReconcileRequest{
		PollStartGrace: 299 * time.Second, Limit: 10,
	})
	if err != nil || reconciled.RetryWait != 1 || reconciled.Abandoned != 0 {
		return acceptanceCheckpoint("recover", "reconcile")
	}
	claim, err := repository.ClaimRunnable(ctx, inventorypoll.ClaimRequest{
		Token: secondFence, LeaseDuration: 60 * time.Second,
	})
	if err != nil || claim == nil || claim.PollRunID != fixturePollID || claim.Attempt != 2 ||
		claim.FencingToken != secondFence {
		return acceptanceCheckpoint("recover", "second_claim")
	}
	if err := repository.FinalizeFenced(ctx, completeFixture(firstFence, fixturePollID, 11)); !errors.Is(err, inventorypoll.ErrLostLease) {
		return acceptanceCheckpoint("recover", "stale_fence")
	}
	if err := repository.FinalizeFenced(ctx, completeFixture(secondFence, fixturePollID, 11)); err != nil {
		return acceptanceCheckpoint("recover", "finalize")
	}
	counts, err = pollCounts(ctx, owner, fixturePollID, fixtureInstanceID)
	if err != nil || counts != (boundedCounts{runs: 1, finalized: 1, providers: 1, snapshots: 1, states: 1}) {
		return acceptanceCheckpoint("recover", "persisted_counts")
	}
	fmt.Println("account_inventory_snapshot_postgres_recovery=success phase=recover retry_wait=1 attempt=2 stale_fence=rejected finalized=1 provider_results=1 snapshot_items=1 current_states=1")
	return nil
}

func acceptanceCheckpoint(phase, checkpoint string) error {
	fmt.Fprintf(os.Stderr, "account_inventory_snapshot_postgres_recovery_checkpoint=failed phase=%s checkpoint=%s\n", phase, checkpoint)
	return errAcceptanceInvariant
}

func runTransactionTimeout() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	owner, err := openPool(ctx, ownerURLEnvironment, 3, 2*time.Second, 0)
	if err != nil {
		return err
	}
	defer owner.Close()
	runtime, err := openPool(ctx, runtimeURLEnvironment, 1, 2*time.Second, 500*time.Millisecond)
	if err != nil {
		return err
	}
	defer runtime.Close()
	if err := seedTimeoutPoll(ctx, owner); err != nil {
		return errAcceptanceInvariant
	}
	repository, err := pollstore.NewInventoryPollRepository(runtime)
	if err != nil {
		return errAcceptanceInvariant
	}
	claim, err := repository.ClaimRunnable(ctx, inventorypoll.ClaimRequest{
		Token: timeoutFence, LeaseDuration: 60 * time.Second,
	})
	if err != nil || claim == nil || claim.PollRunID != timeoutPollID || claim.Attempt != 1 {
		return errAcceptanceInvariant
	}
	blocker, err := owner.Begin(ctx)
	if err != nil {
		return errAcceptanceInvariant
	}
	released := false
	defer func() {
		if !released {
			_ = blocker.Rollback(context.Background())
		}
	}()
	if _, err := blocker.Exec(ctx, `SELECT policy_version_id
		FROM provider_inventory_policy_bindings
		WHERE node_type=$1 AND driver_contract_version=$2 FOR UPDATE`, fixtureNodeType, fixtureContract); err != nil {
		return errAcceptanceInvariant
	}
	err = repository.FinalizeFenced(ctx, completeFixture(timeoutFence, timeoutPollID, 12))
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) || databaseError.Code != "57014" {
		return errAcceptanceInvariant
	}
	counts, err := pollCounts(ctx, owner, timeoutPollID, timeoutInstanceID)
	if err != nil || counts != (boundedCounts{runs: 1}) {
		return errAcceptanceInvariant
	}
	if err := blocker.Rollback(ctx); err != nil {
		return errAcceptanceInvariant
	}
	released = true
	if err := repository.FinalizeFenced(ctx, completeFixture(timeoutFence, timeoutPollID, 12)); err != nil {
		return errAcceptanceInvariant
	}
	counts, err = pollCounts(ctx, owner, timeoutPollID, timeoutInstanceID)
	if err != nil || counts != (boundedCounts{runs: 1, finalized: 1, providers: 1, snapshots: 1, states: 1}) {
		return errAcceptanceInvariant
	}
	fmt.Println("account_inventory_snapshot_postgres_recovery=success phase=transaction-timeout timeout_class=statement_timeout rollback_runs=1 rollback_provider_results=0 rollback_snapshot_items=0 finalized_after_release=1")
	return nil
}

func runExhaustion() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	owner, err := openPool(ctx, ownerURLEnvironment, 2, 2*time.Second, 0)
	if err != nil {
		return err
	}
	defer owner.Close()
	if _, err := owner.Exec(ctx, "ALTER ROLE relay_control_app_dev CONNECTION LIMIT 1"); err != nil {
		return errAcceptanceInvariant
	}
	defer func() {
		resetContext, resetCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer resetCancel()
		_, _ = owner.Exec(resetContext, "ALTER ROLE relay_control_app_dev CONNECTION LIMIT -1")
	}()
	blockerPool, err := openPool(ctx, runtimeURLEnvironment, 1, 2*time.Second, 0)
	if err != nil {
		return err
	}
	defer blockerPool.Close()
	connection, err := blockerPool.Acquire(ctx)
	if err != nil {
		return errAcceptanceInvariant
	}
	defer connection.Release()
	probeConfig, err := pgx.ParseConfig(os.Getenv(runtimeURLEnvironment))
	if err != nil {
		return errInvalidConfiguration
	}
	probeConfig.ConnectTimeout = 500 * time.Millisecond
	probe, probeErr := pgx.ConnectConfig(ctx, probeConfig)
	if probe != nil {
		_ = probe.Close(ctx)
	}
	var connectionError *pgconn.PgError
	if !errors.As(probeErr, &connectionError) || connectionError.Code != "53300" {
		return errAcceptanceInvariant
	}
	pool, err := openPool(ctx, runtimeURLEnvironment, 1, 500*time.Millisecond, 0)
	if err != nil {
		return err
	}
	defer pool.Close()
	repository, err := pollstore.NewInventoryPollRepository(pool)
	if err != nil {
		return errAcceptanceInvariant
	}
	driver := &countingDriver{}
	observer := &countingObserver{}
	service, err := inventorypoll.NewService(repository, driver, recoveryConfig(observer))
	if err != nil {
		return errAcceptanceInvariant
	}
	serviceContext, stopService := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer stopService()
	serviceDone := make(chan error, 1)
	go func() { serviceDone <- service.Run(serviceContext) }()
	if err := awaitService(serviceDone, 2*time.Second); err != nil || driver.calls.Load() != 0 ||
		observer.reconcileFailures.Load() < 1 || observer.reconcileFailures.Load() > 10 {
		return errAcceptanceInvariant
	}
	fmt.Printf("account_inventory_snapshot_postgres_recovery=success phase=exhaustion postgres_sqlstate=53300 role_capacity=1 role_connections_held=1 startup_barrier=canceled reconcile_failures=%d driver_calls=0\n", observer.reconcileFailures.Load())
	return nil
}

func runRestartVerify() error {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	pool, err := openPool(ctx, runtimeURLEnvironment, 3, 2*time.Second, 0)
	if err != nil {
		return err
	}
	defer pool.Close()
	repository, err := pollstore.NewInventoryPollRepository(pool)
	if err != nil {
		return errAcceptanceInvariant
	}
	current, err := repository.CurrentProviderSnapshot(ctx, fixtureInstanceID, fixtureProvider, "", 10)
	if err != nil || len(current) != 1 || current[0].NormalizedEmail != fixtureEmail || current[0].SuccessCount != 11 {
		return errAcceptanceInvariant
	}
	timeoutCurrent, err := repository.CurrentProviderSnapshot(ctx, timeoutInstanceID, fixtureProvider, "", 10)
	if err != nil || len(timeoutCurrent) != 1 || timeoutCurrent[0].NormalizedEmail != fixtureEmail || timeoutCurrent[0].SuccessCount != 12 {
		return errAcceptanceInvariant
	}
	runs, providers, err := repository.Metrics(ctx)
	if err != nil {
		return errAcceptanceInvariant
	}
	runCount, providerCount := 0, 0
	for _, metric := range runs {
		if metric.InstanceID == fixtureInstanceID || metric.InstanceID == timeoutInstanceID {
			runCount++
			if metric.Status != pollstore.PollRunFinalized {
				return errAcceptanceInvariant
			}
		}
	}
	for _, metric := range providers {
		if (metric.InstanceID == fixtureInstanceID || metric.InstanceID == timeoutInstanceID) && metric.Provider == fixtureProvider {
			providerCount++
			if !metric.PromotionEvaluated || !metric.PromotionApplied {
				return errAcceptanceInvariant
			}
		}
	}
	if runCount != 2 || providerCount != 2 {
		return errAcceptanceInvariant
	}
	var versionNumber string
	if err := pool.QueryRow(ctx, "SHOW server_version_num").Scan(&versionNumber); err != nil {
		return errAcceptanceInvariant
	}
	version, err := strconv.Atoi(versionNumber)
	if err != nil || version < 180000 || version >= 190000 {
		return errAcceptanceInvariant
	}
	driver := &countingDriver{}
	service, err := inventorypoll.NewService(repository, driver, recoveryConfig(&countingObserver{}))
	if err != nil {
		return errAcceptanceInvariant
	}
	serviceContext, stopService := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer stopService()
	serviceDone := make(chan error, 1)
	go func() { serviceDone <- service.Run(serviceContext) }()
	if err := awaitService(serviceDone, 2*time.Second); err != nil || driver.calls.Load() != 0 {
		return errAcceptanceInvariant
	}
	fmt.Println("account_inventory_snapshot_postgres_recovery=success phase=restart-verify server_major=18 current_items=2 run_metrics=2 provider_metrics=2 driver_calls=0")
	return nil
}

func openPool(
	ctx context.Context, environment string, maximumConnections int32, connectTimeout, statementTimeout time.Duration,
) (*pgxpool.Pool, error) {
	return openNamedPool(ctx, environment, maximumConnections, connectTimeout, statementTimeout, "")
}

func openNamedPool(
	ctx context.Context, environment string, maximumConnections int32, connectTimeout, statementTimeout time.Duration,
	applicationName string,
) (*pgxpool.Pool, error) {
	url := os.Getenv(environment)
	if url == "" {
		return nil, errInvalidConfiguration
	}
	configuration, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, errInvalidConfiguration
	}
	configuration.MaxConns = maximumConnections
	configuration.MinConns = 0
	configuration.ConnConfig.ConnectTimeout = connectTimeout
	if applicationName != "" {
		configuration.ConnConfig.RuntimeParams["application_name"] = applicationName
	}
	if statementTimeout > 0 {
		configuration.ConnConfig.RuntimeParams["statement_timeout"] = strconv.FormatInt(statementTimeout.Milliseconds(), 10)
	}
	pool, err := pgxpool.NewWithConfig(ctx, configuration)
	if err != nil {
		return nil, errAcceptanceInvariant
	}
	return pool, nil
}

func seedFixture(ctx context.Context, owner *pgxpool.Pool) error {
	if err := waitForSafeCurrentSlot(ctx, owner, 30); err != nil {
		return err
	}
	transaction, err := owner.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback(context.Background()) }()
	var existing int
	if err := transaction.QueryRow(ctx, `SELECT count(*) FROM account_inventory_poll_runs
		WHERE poll_run_id IN ($1,$2)`, fixturePollID, timeoutPollID).Scan(&existing); err != nil || existing != 0 {
		return errAcceptanceInvariant
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO node_drivers(
		node_type,driver_contract_version,display_name
	) VALUES ($1,$2,'Snapshot Recovery Test Driver')`, fixtureNodeType, fixtureContract); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO relay_node_assets(
		instance_id,display_name,node_type,driver_contract_version,management_endpoint,reader_secret_ref
	) VALUES ($1,'Snapshot Recovery Test Node',$2,$3,'http://snapshot-recovery.invalid',
		'docker-secret://synthetic/snapshot-recovery-reader')`, fixtureInstanceID, fixtureNodeType, fixtureContract); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO provider_inventory_policy_versions(
		policy_version_id,node_type,driver_contract_version,active_providers,out_of_scope_providers,created_by
	) VALUES ($1,$2,$3,ARRAY[$4]::text[],ARRAY['legacy']::text[],'acceptance')`,
		fixturePolicyID, fixtureNodeType, fixtureContract, fixtureProvider); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO provider_inventory_policy_bindings(
		node_type,driver_contract_version,policy_version_id,bound_by,bound_at
	) VALUES ($1,$2,$3,'acceptance',clock_timestamp())`, fixtureNodeType, fixtureContract, fixturePolicyID); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO provider_inventory_policy_activations(
		node_type,driver_contract_version,policy_version_id,effective_from,activated_by,created_at
	) VALUES ($1,$2,$3,clock_timestamp(),'acceptance',CURRENT_TIMESTAMP)`,
		fixtureNodeType, fixtureContract, fixturePolicyID); err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `INSERT INTO account_inventory_poll_runs(
		poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
		provider_policy_version,max_attempts,poll_start_grace_seconds,created_at
	) VALUES ($1,$2,$3,$4,
		date_bin(interval '5 minutes',clock_timestamp(),timestamptz '1970-01-01'),
		$5,2,299,clock_timestamp())`, fixturePollID, fixtureInstanceID, fixtureNodeType, fixtureContract, fixturePolicyID); err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

func seedTimeoutPoll(ctx context.Context, owner *pgxpool.Pool) error {
	if err := waitForSafeCurrentSlot(ctx, owner, 10); err != nil {
		return err
	}
	transaction, err := owner.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback(context.Background()) }()
	if _, err := transaction.Exec(ctx, `INSERT INTO relay_node_assets(
		instance_id,display_name,node_type,driver_contract_version,management_endpoint,reader_secret_ref
	) VALUES ($1,'Snapshot Recovery Timeout Node',$2,$3,'http://snapshot-timeout.invalid',
		'docker-secret://synthetic/snapshot-timeout-reader')`, timeoutInstanceID, fixtureNodeType, fixtureContract); err != nil {
		return err
	}
	command, err := transaction.Exec(ctx, `INSERT INTO account_inventory_poll_runs(
		poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
		provider_policy_version,max_attempts,poll_start_grace_seconds,created_at
	) SELECT $1,$2,node_type,driver_contract_version,
		date_bin(interval '5 minutes',clock_timestamp(),timestamptz '1970-01-01'),
		provider_policy_version,2,299,clock_timestamp()
	FROM account_inventory_poll_runs WHERE poll_run_id=$3`, timeoutPollID, timeoutInstanceID, fixturePollID)
	if err != nil || command.RowsAffected() != 1 {
		return errAcceptanceInvariant
	}
	return transaction.Commit(ctx)
}

func waitForSafeCurrentSlot(ctx context.Context, owner *pgxpool.Pool, minimumRemainingSeconds int) error {
	var remainingSeconds int
	if err := owner.QueryRow(ctx, `SELECT 300-mod(extract(epoch FROM clock_timestamp())::bigint,300)`).Scan(&remainingSeconds); err != nil {
		return err
	}
	if remainingSeconds >= minimumRemainingSeconds {
		return nil
	}
	timer := time.NewTimer(time.Duration(remainingSeconds)*time.Second + 250*time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func waitForApplicationLock(ctx context.Context, owner *pgxpool.Pool, applicationName string) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		err := owner.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM pg_stat_activity
			WHERE application_name=$1 AND wait_event_type='Lock'
		)`, applicationName).Scan(&waiting)
		if err != nil {
			return err
		}
		if waiting {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func waitForExpiredLease(ctx context.Context, owner *pgxpool.Pool) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		var expired bool
		err := owner.QueryRow(ctx, `SELECT status='running' AND lease_expires_at<=clock_timestamp()
			FROM account_inventory_poll_runs WHERE poll_run_id=$1`, fixturePollID).Scan(&expired)
		if err != nil {
			return err
		}
		if expired {
			return nil
		}
		select {
		case <-ctx.Done():
			return errAcceptanceInvariant
		case <-ticker.C:
		}
	}
}

func completeFixture(fence, pollID uuid.UUID, successCount uint64) inventorypoll.FinalizeRequest {
	return inventorypoll.FinalizeRequest{
		PollRunID: pollID, FencingToken: fence,
		Node: inventorypoll.NodeEvidence{
			TransportSuccess: true, ResponseShapeValid: true, ContractValid: true,
			InventoryMode: drivers.InventoryModeRuntime, NodeIdentityComplete: true,
			SnapshotComplete: true, RecognizedRecordCount: 1,
			Result: drivers.ResultSuccess, Reason: drivers.ReasonNone,
			Version: "v1.0.0", Commit: "abcdef1",
		},
		Providers: []inventorypoll.ProviderEvidence{{
			Provider: fixtureProvider, RecognizedRecordCount: 1,
			IdentityComplete: true, SnapshotComplete: true,
			Reason: inventorypoll.ProviderReasonComplete,
		}},
		SnapshotItems: []inventorypoll.SnapshotCandidate{{
			Provider: fixtureProvider, AccountKey: fixtureProvider + ":" + fixtureEmail,
			Email: fixtureEmail, BasicStatus: drivers.AccountStateActive,
			SuccessCount: successCount, FailedCount: 2, RecentRequestCount: 3,
		}},
	}
}

type boundedCounts struct {
	runs      int
	finalized int
	providers int
	snapshots int
	states    int
}

func pollCounts(ctx context.Context, owner *pgxpool.Pool, pollID, instanceID uuid.UUID) (boundedCounts, error) {
	var counts boundedCounts
	err := owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM account_inventory_poll_runs WHERE poll_run_id=$1),
		(SELECT count(*) FROM account_inventory_poll_runs WHERE poll_run_id=$1 AND status='finalized'),
		(SELECT count(*) FROM account_inventory_poll_provider_results WHERE poll_run_id=$1),
		(SELECT count(*) FROM account_inventory_snapshot_items WHERE poll_run_id=$1),
		(SELECT count(*) FROM account_inventory_provider_states WHERE instance_id=$2 AND provider=$3
		 AND current_poll_run_id=$1)`, pollID, instanceID, fixtureProvider).Scan(
		&counts.runs, &counts.finalized, &counts.providers, &counts.snapshots, &counts.states,
	)
	return counts, err
}

func recoveryConfig(observer inventorypoll.Observer) inventorypoll.Config {
	return inventorypoll.Config{
		Period: 5 * time.Minute, PollStartGrace: 20 * time.Second,
		MaxMonitoredNodes: 1, Concurrency: 1, WorstCasePollDuration: time.Second,
		LeaseDuration: 3 * time.Second, MaxAttempts: 2,
		DispatchMargin: time.Second, FinalizeMargin: time.Second,
		SchedulerInterval: 100 * time.Millisecond, WorkerScanInterval: 100 * time.Millisecond,
		ReconcileInterval:      500 * time.Millisecond,
		DatabaseBackoffInitial: 50 * time.Millisecond, DatabaseBackoffMaximum: 100 * time.Millisecond,
		ShutdownGrace: time.Second, ScheduleLimit: 1, ReconcileLimit: 10, Observer: observer,
	}
}

type countingDriver struct {
	calls atomic.Int64
}

func (driver *countingDriver) ListAccountInventory(
	context.Context, drivers.InventoryRequest,
) (drivers.InventoryObservation, error) {
	driver.calls.Add(1)
	return drivers.InventoryObservation{
		Result: drivers.ResultFailed, Reason: drivers.ReasonContractInvalid,
		Version: "unknown", Commit: "unknown",
	}, nil
}

type countingObserver struct {
	reconcileFailures atomic.Int64
}

func (observer *countingObserver) Observe(_ context.Context, event inventorypoll.Event) {
	if event.Component == inventorypoll.EventComponentReconciler &&
		event.Action == inventorypoll.EventActionReconcile && event.Result == inventorypoll.EventResultFailure {
		observer.reconcileFailures.Add(1)
	}
}

func runDataPlaneSimulator(requests int) <-chan int {
	done := make(chan int, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))
	go func() {
		defer server.Close()
		processed := 0
		client := &http.Client{Timeout: 250 * time.Millisecond}
		for index := 0; index < requests; index++ {
			response, err := client.Get(server.URL)
			if err != nil {
				continue
			}
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusNoContent {
				processed++
			}
		}
		done <- processed
	}()
	return done
}

func awaitService(done <-chan error, maximum time.Duration) error {
	timer := time.NewTimer(maximum)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		return errAcceptanceInvariant
	}
}
