package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sunxu/relay-station-control/internal/drivers"
	"github.com/sunxu/relay-station-control/internal/drivers/cliproxyapi"
	"github.com/sunxu/relay-station-control/internal/inventorypoll"
	pollstore "github.com/sunxu/relay-station-control/internal/store"
)

const (
	ownerURLEnvironment       = "CONTROL_SNAPSHOT_REAL_NODE_OWNER_URL"
	runtimeURLEnvironment     = "CONTROL_SNAPSHOT_REAL_NODE_RUNTIME_URL"
	mappingFileEnvironment    = "CONTROL_SNAPSHOT_REAL_NODE_SECRET_MAPPING_FILE"
	managementCIDREnvironment = "CONTROL_SNAPSHOT_REAL_NODE_MANAGEMENT_CIDRS"

	firstEndpoint        = "http://relay-phase0-node-a:8317"
	secondEndpoint       = "http://relay-phase0-node-b:8317"
	firstReference       = "file://phase0/node-a-management-key"
	secondReference      = "file://phase0/node-b-management-key"
	fixtureProvider      = "antigravity"
	expectedVersion      = "7.2.141"
	expectedCommitPrefix = "dc3c3b1"
	requestCooldown      = 10 * time.Second

	successSummary = "account_inventory_snapshot_real_node=success node_count=2 request_count=2 request_wait_seconds=10 runtime_mode_count=2 disk_fallback_mode_count=0 snapshot_items=6 provider_results=2 promotion_applied=2 promotion_skipped=0 management_writes=0 probe_requests=0 gateway_requests=0"
)

var (
	errConfiguration = errors.New("invalid_configuration")
	errInvariant     = errors.New("acceptance_invariant")

	firstInstanceID  = uuid.MustParse("30000000-0000-4000-8000-000000000001")
	secondInstanceID = uuid.MustParse("30000000-0000-4000-8000-000000000002")
	fixturePolicyID  = uuid.MustParse("30000000-0000-4000-8000-000000000003")
	firstPollID      = uuid.MustParse("30000000-0000-4000-8000-000000000004")
	secondPollID     = uuid.MustParse("30000000-0000-4000-8000-000000000005")
)

type checkpointError struct{ checkpoint string }

func (*checkpointError) Error() string { return "acceptance_checkpoint" }
func checkpoint(name string) error     { return &checkpointError{checkpoint: name} }
func main()                            { os.Exit(run()) }

func run() (exitCode int) {
	exitCode = 1
	defer func() {
		if recover() != nil {
			fmt.Fprintln(os.Stderr, "account_inventory_snapshot_real_node=failed reason=acceptance_invariant")
			exitCode = 1
		}
	}()
	if err := execute(); err != nil {
		reason := "acceptance_invariant"
		if errors.Is(err, errConfiguration) {
			reason = "invalid_configuration"
		}
		var failure *checkpointError
		if errors.As(err, &failure) {
			fmt.Fprintf(os.Stderr, "account_inventory_snapshot_real_node=failed reason=%s checkpoint=%s\n", reason, failure.checkpoint)
		} else {
			fmt.Fprintf(os.Stderr, "account_inventory_snapshot_real_node=failed reason=%s\n", reason)
		}
		return 1
	}
	fmt.Println(successSummary)
	return 0
}

type acceptanceConfiguration struct {
	ownerURL, runtimeURL, mappingFile string
	managementCIDRs                   []string
}

func loadConfiguration() (acceptanceConfiguration, error) {
	configuration := acceptanceConfiguration{
		ownerURL: os.Getenv(ownerURLEnvironment), runtimeURL: os.Getenv(runtimeURLEnvironment),
		mappingFile: os.Getenv(mappingFileEnvironment), managementCIDRs: splitCSV(os.Getenv(managementCIDREnvironment)),
	}
	if configuration.ownerURL == "" || configuration.runtimeURL == "" || configuration.mappingFile == "" ||
		len(configuration.managementCIDRs) != 2 {
		return acceptanceConfiguration{}, errConfiguration
	}
	return configuration, nil
}

func execute() error {
	configuration, err := loadConfiguration()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	owner, err := openPool(ctx, configuration.ownerURL)
	if err != nil {
		return checkpoint("owner_database")
	}
	defer owner.Close()
	runtime, err := openPool(ctx, configuration.runtimeURL)
	if err != nil {
		return checkpoint("runtime_database")
	}
	defer runtime.Close()
	if err := seed(ctx, owner); err != nil {
		return checkpoint("seed")
	}

	resolver, err := drivers.NewFileSecretResolver(drivers.FileSecretResolverConfig{MappingFile: configuration.mappingFile})
	if err != nil {
		return checkpoint("secret_resolver")
	}
	driver, err := cliproxyapi.NewDriver(cliproxyapi.DriverConfig{
		Management: drivers.ManagementConfig{
			AllowedDNSNames:        []string{"relay-phase0-node-a", "relay-phase0-node-b"},
			AllowedManagementCIDRs: configuration.managementCIDRs,
			AllowedPlainHTTPCIDRs:  configuration.managementCIDRs,
			ConnectTimeout:         time.Second, RequestTimeout: 3 * time.Second,
		},
		SecretResolver: resolver,
	})
	if err != nil {
		return checkpoint("driver")
	}
	repository, err := pollstore.NewInventoryPollRepository(runtime)
	if err != nil {
		return checkpoint("repository")
	}
	reporting := &reportingRepository{Repository: repository, finalized: make(chan finalizeResult, 2)}
	invoker := &serialCooldownInvoker{
		inner: driver, wait: time.Sleep,
		expectedInstances: []uuid.UUID{firstInstanceID, secondInstanceID},
	}
	worker, err := inventorypoll.NewWorker(reporting, invoker, workerConfig())
	if err != nil {
		return checkpoint("worker")
	}
	workerContext, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan error, 1)
	go func() { workerDone <- worker.Run(workerContext) }()

	finalized := make([]finalizeResult, 0, 2)
	for len(finalized) < 2 {
		select {
		case result := <-reporting.finalized:
			finalized = append(finalized, result)
		case <-ctx.Done():
			stopWorker()
			return checkpoint("finalize_wait")
		}
	}
	stopWorker()
	select {
	case workerErr := <-workerDone:
		if workerErr != nil {
			return checkpoint("worker_shutdown")
		}
	case <-ctx.Done():
		return checkpoint("worker_shutdown")
	}
	if invoker.calls.Load() != 2 {
		return checkpoint("request_count")
	}
	if err := validateFinalizes(finalized); err != nil {
		return err
	}
	if err := assertPersistence(ctx, owner); err != nil {
		return checkpoint("persistence")
	}
	return nil
}

func openPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	configuration, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	configuration.MaxConns = 2
	configuration.MinConns = 0
	configuration.ConnConfig.ConnectTimeout = 2 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, configuration)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func seed(ctx context.Context, owner *pgxpool.Pool) error {
	if err := waitForSafeSlot(ctx, owner); err != nil {
		return err
	}
	transaction, err := owner.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback(context.Background()) }()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO node_drivers(node_type,driver_contract_version,display_name)
			VALUES ($1,$2,'Phase 0 Snapshot Acceptance Driver')`, []any{drivers.NodeTypeCLIProxyAPI, drivers.DriverContractCLIProxyAPIAuthFilesV1}},
		{`INSERT INTO driver_capabilities(node_type,driver_contract_version,capability)
			VALUES ($1,$2,$3)`, []any{drivers.NodeTypeCLIProxyAPI, drivers.DriverContractCLIProxyAPIAuthFilesV1, drivers.CapabilityManagementAccountInventoryRead}},
		{`INSERT INTO provider_inventory_policy_versions(policy_version_id,node_type,driver_contract_version,active_providers,out_of_scope_providers,created_by)
			VALUES ($1,$2,$3,ARRAY[$4]::text[],ARRAY['legacy']::text[],'acceptance')`, []any{fixturePolicyID, drivers.NodeTypeCLIProxyAPI, drivers.DriverContractCLIProxyAPIAuthFilesV1, fixtureProvider}},
		{`INSERT INTO provider_inventory_policy_bindings(node_type,driver_contract_version,policy_version_id,bound_by,bound_at)
			VALUES ($1,$2,$3,'acceptance',clock_timestamp())`, []any{drivers.NodeTypeCLIProxyAPI, drivers.DriverContractCLIProxyAPIAuthFilesV1, fixturePolicyID}},
		{`INSERT INTO provider_inventory_policy_activations(node_type,driver_contract_version,policy_version_id,effective_from,activated_by,created_at)
			VALUES ($1,$2,$3,clock_timestamp(),'acceptance',CURRENT_TIMESTAMP)`, []any{drivers.NodeTypeCLIProxyAPI, drivers.DriverContractCLIProxyAPIAuthFilesV1, fixturePolicyID}},
	}
	for _, statement := range statements {
		if _, err := transaction.Exec(ctx, statement.query, statement.args...); err != nil {
			return err
		}
	}
	nodes := []struct {
		instanceID uuid.UUID
		pollID     uuid.UUID
		name       string
		endpoint   string
		reference  string
	}{
		{firstInstanceID, firstPollID, "Phase 0 Snapshot Node A", firstEndpoint, firstReference},
		{secondInstanceID, secondPollID, "Phase 0 Snapshot Node B", secondEndpoint, secondReference},
	}
	for _, node := range nodes {
		if _, err := transaction.Exec(ctx, `INSERT INTO relay_node_assets(instance_id,display_name,node_type,driver_contract_version,management_endpoint,reader_secret_ref)
			VALUES ($1,$2,$3,$4,$5,$6)`, node.instanceID, node.name, drivers.NodeTypeCLIProxyAPI,
			drivers.DriverContractCLIProxyAPIAuthFilesV1, node.endpoint, node.reference); err != nil {
			return err
		}
		if _, err := transaction.Exec(ctx, `INSERT INTO node_capabilities(instance_id,node_type,driver_contract_version,capability)
			VALUES ($1,$2,$3,$4)`, node.instanceID, drivers.NodeTypeCLIProxyAPI,
			drivers.DriverContractCLIProxyAPIAuthFilesV1, drivers.CapabilityManagementAccountInventoryRead); err != nil {
			return err
		}
		if _, err := transaction.Exec(ctx, `INSERT INTO account_inventory_poll_runs(poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
			provider_policy_version,max_attempts,poll_start_grace_seconds,created_at)
			VALUES ($1,$2,$3,$4,date_bin(interval '5 minutes',clock_timestamp(),timestamptz '1970-01-01'),$5,1,299,clock_timestamp())`,
			node.pollID, node.instanceID, drivers.NodeTypeCLIProxyAPI, drivers.DriverContractCLIProxyAPIAuthFilesV1, fixturePolicyID); err != nil {
			return err
		}
	}
	return transaction.Commit(ctx)
}

func waitForSafeSlot(ctx context.Context, owner *pgxpool.Pool) error {
	var remaining int
	if err := owner.QueryRow(ctx, `SELECT 300-mod(extract(epoch FROM clock_timestamp())::bigint,300)`).Scan(&remaining); err != nil {
		return err
	}
	if remaining >= 45 {
		return nil
	}
	timer := time.NewTimer(time.Duration(remaining)*time.Second + 250*time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type serialCooldownInvoker struct {
	inner             inventorypoll.DriverInvoker
	wait              func(time.Duration)
	expectedInstances []uuid.UUID
	calls             atomic.Int32
}

func (invoker *serialCooldownInvoker) ListAccountInventory(ctx context.Context, request drivers.InventoryRequest) (observation drivers.InventoryObservation, err error) {
	call := int(invoker.calls.Add(1))
	if call < 1 || call > len(invoker.expectedInstances) || request.Target.InstanceID != invoker.expectedInstances[call-1] || invoker.wait == nil {
		return failedObservation(), errInvariant
	}
	defer invoker.wait(requestCooldown)
	defer func() {
		if recover() != nil {
			observation, err = failedObservation(), errInvariant
		}
	}()
	return invoker.inner.ListAccountInventory(ctx, request)
}

func failedObservation() drivers.InventoryObservation {
	return drivers.InventoryObservation{Result: drivers.ResultFailed, Reason: drivers.ReasonCancelled, Version: "unknown", Commit: "unknown"}
}

type finalizeResult struct {
	request inventorypoll.FinalizeRequest
	err     error
}

type reportingRepository struct {
	inventorypoll.Repository
	finalized chan finalizeResult
	mu        sync.Mutex
	reported  map[uuid.UUID]struct{}
}

func (repository *reportingRepository) FinalizeFenced(ctx context.Context, request inventorypoll.FinalizeRequest) error {
	err := repository.Repository.FinalizeFenced(ctx, request)
	repository.mu.Lock()
	if repository.reported == nil {
		repository.reported = make(map[uuid.UUID]struct{}, 2)
	}
	if _, duplicate := repository.reported[request.PollRunID]; !duplicate {
		repository.reported[request.PollRunID] = struct{}{}
		repository.finalized <- finalizeResult{request: request, err: err}
	}
	repository.mu.Unlock()
	return err
}

func workerConfig() inventorypoll.Config {
	return inventorypoll.Config{
		Period: 5 * time.Minute, PollStartGrace: 299 * time.Second,
		Concurrency: 1, WorstCasePollDuration: 15 * time.Second,
		LeaseDuration: 30 * time.Second, MaxAttempts: 1,
		DispatchMargin: time.Second, FinalizeMargin: 10 * time.Second,
		SchedulerInterval: time.Second, WorkerScanInterval: 100 * time.Millisecond,
		ReconcileInterval: 5 * time.Second, DatabaseBackoffInitial: time.Second,
		DatabaseBackoffMaximum: 5 * time.Second, ShutdownGrace: 2 * time.Second,
		ReconcileLimit: 10,
	}
}

func validateFinalizes(results []finalizeResult) error {
	if len(results) != 2 {
		return checkpoint("finalize_count")
	}
	expectedPolls := []uuid.UUID{firstPollID, secondPollID}
	for index, result := range results {
		request := result.request
		if result.err != nil || request.PollRunID != expectedPolls[index] {
			return checkpoint("finalize")
		}
		if !request.Node.TransportSuccess || !request.Node.ResponseShapeValid || !request.Node.ContractValid ||
			request.Node.InventoryMode != drivers.InventoryModeRuntime || !request.Node.NodeIdentityComplete ||
			!request.Node.SnapshotComplete || request.Node.Degraded || request.Node.Result != drivers.ResultSuccess ||
			request.Node.Reason != drivers.ReasonNone || strings.TrimPrefix(request.Node.Version, "v") != expectedVersion ||
			!strings.HasPrefix(request.Node.Commit, expectedCommitPrefix) {
			return checkpoint("projection_contract")
		}
		if request.Node.RecognizedRecordCount != 3 || request.Node.UnidentifiedRecordCount != 0 ||
			request.Node.UnsupportedProviderCount != 0 || request.Node.OutOfScopeProviderCount != 0 ||
			len(request.Providers) != 1 || len(request.SnapshotItems) != 3 || len(request.Duplicates) != 0 {
			return checkpoint("projection_counts")
		}
		provider := request.Providers[0]
		if provider.Provider != fixtureProvider || provider.RecognizedRecordCount != 3 || provider.MissingIdentityCount != 0 ||
			provider.DuplicateIdentityCount != 0 || !provider.IdentityComplete || !provider.SnapshotComplete || provider.Degraded ||
			provider.Reason != inventorypoll.ProviderReasonComplete {
			return checkpoint("projection_provider")
		}
		for _, item := range request.SnapshotItems {
			if item.Provider != fixtureProvider || !strings.HasPrefix(item.AccountKey, fixtureProvider+":") {
				return checkpoint("projection_item")
			}
		}
	}
	return nil
}

func assertPersistence(ctx context.Context, owner *pgxpool.Pool) error {
	var runs, finalized, attempts, runtimeModes, diskModes, snapshots, providers, applied, skipped, states int
	err := owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM account_inventory_poll_runs WHERE poll_run_id=ANY($1::uuid[])),
		(SELECT count(*) FROM account_inventory_poll_runs WHERE poll_run_id=ANY($1::uuid[]) AND status='finalized'),
		(SELECT coalesce(sum(attempt_count),0) FROM account_inventory_poll_runs WHERE poll_run_id=ANY($1::uuid[])),
		(SELECT count(*) FROM account_inventory_poll_runs WHERE poll_run_id=ANY($1::uuid[]) AND inventory_mode='runtime'),
		(SELECT count(*) FROM account_inventory_poll_runs WHERE poll_run_id=ANY($1::uuid[]) AND inventory_mode='disk_fallback'),
		(SELECT count(*) FROM account_inventory_snapshot_items WHERE poll_run_id=ANY($1::uuid[])),
		(SELECT count(*) FROM account_inventory_poll_provider_results WHERE poll_run_id=ANY($1::uuid[])),
		(SELECT count(*) FROM account_inventory_poll_provider_results WHERE poll_run_id=ANY($1::uuid[]) AND promotion_applied),
		(SELECT count(*) FROM account_inventory_poll_provider_results WHERE poll_run_id=ANY($1::uuid[]) AND NOT promotion_applied AND promotion_skipped_reason IS NOT NULL),
		(SELECT count(*) FROM account_inventory_provider_states WHERE instance_id=ANY($2::uuid[]) AND provider=$3)`,
		[]uuid.UUID{firstPollID, secondPollID}, []uuid.UUID{firstInstanceID, secondInstanceID}, fixtureProvider).Scan(
		&runs, &finalized, &attempts, &runtimeModes, &diskModes, &snapshots, &providers, &applied, &skipped, &states)
	if err != nil || runs != 2 || finalized != 2 || attempts != 2 || runtimeModes != 2 || diskModes != 0 ||
		snapshots != 6 || providers != 2 || applied != 2 || skipped != 0 || states != 2 {
		return errInvariant
	}
	return nil
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if item := strings.TrimSpace(part); item != "" {
			result = append(result, item)
		}
	}
	return result
}
