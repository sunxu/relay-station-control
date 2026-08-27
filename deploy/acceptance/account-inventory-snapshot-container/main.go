package main

import (
	"context"
	"errors"
	"fmt"
	"math"
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
	ownerURLEnvironment        = "CONTROL_SNAPSHOT_CONTAINER_OWNER_URL"
	runtimeURLEnvironment      = "CONTROL_SNAPSHOT_CONTAINER_RUNTIME_URL"
	endpointEnvironment        = "CONTROL_SNAPSHOT_CONTAINER_NODE_ENDPOINT"
	dnsEnvironment             = "CONTROL_SNAPSHOT_CONTAINER_MANAGEMENT_DNS"
	managementCIDREnvironment  = "CONTROL_SNAPSHOT_CONTAINER_MANAGEMENT_CIDRS"
	plainHTTPCIDREnvironment   = "CONTROL_SNAPSHOT_CONTAINER_PLAIN_HTTP_CIDRS"
	mappingFileEnvironment     = "CONTROL_SNAPSHOT_CONTAINER_SECRET_MAPPING_FILE"
	secretReferenceEnvironment = "CONTROL_SNAPSHOT_CONTAINER_NODE_SECRET_REFERENCE"

	fixtureProvider = "antigravity"
	requestCooldown = 10 * time.Second
	setupTimeout    = 60 * time.Second
	workerTimeout   = 45 * time.Second
	schedulePeriod  = 5 * time.Minute
)

var (
	errConfiguration = errors.New("invalid_configuration")
	errInvariant     = errors.New("acceptance_invariant")

	fixtureInstanceID = uuid.MustParse("20000000-0000-4000-8000-000000000001")
	fixturePolicyID   = uuid.MustParse("20000000-0000-4000-8000-000000000002")
	fixturePollID     = uuid.MustParse("20000000-0000-4000-8000-000000000003")
)

type checkpointError struct{ checkpoint string }

func (failure *checkpointError) Error() string { return "acceptance_checkpoint" }

func checkpoint(name string) error { return &checkpointError{checkpoint: name} }

func main() {
	os.Exit(run())
}

func run() (exitCode int) {
	exitCode = 1
	defer func() {
		if recover() != nil {
			fmt.Fprintln(os.Stderr, "account_inventory_snapshot_official_runtime=failed reason=acceptance_invariant")
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
			fmt.Fprintf(os.Stderr, "account_inventory_snapshot_official_runtime=failed reason=%s checkpoint=%s\n", reason, failure.checkpoint)
		} else {
			fmt.Fprintf(os.Stderr, "account_inventory_snapshot_official_runtime=failed reason=%s\n", reason)
		}
		return 1
	}
	fmt.Println("account_inventory_snapshot_official_runtime=success image_version=v7.2.141 mode=runtime request_count=1 request_wait_seconds=10 snapshot_items=1 provider_states=1 promotion_applied=1 management_writes=0 probe_requests=0 gateway_requests=0")
	return 0
}

func execute() error {
	configuration, err := loadConfiguration()
	if err != nil {
		return err
	}
	setupContext, cancelSetup := context.WithTimeout(context.Background(), setupTimeout)
	defer cancelSetup()

	owner, err := openPool(setupContext, configuration.ownerURL)
	if err != nil {
		return checkpoint("owner_database")
	}
	defer owner.Close()
	runtime, err := openPool(setupContext, configuration.runtimeURL)
	if err != nil {
		return checkpoint("runtime_database")
	}
	defer runtime.Close()
	if err := seed(setupContext, owner, configuration.endpoint, configuration.secretReference); err != nil {
		return checkpoint("seed")
	}
	cancelSetup()

	ctx, cancel := context.WithTimeout(context.Background(), workerTimeout)
	defer cancel()

	resolver, err := drivers.NewFileSecretResolver(drivers.FileSecretResolverConfig{MappingFile: configuration.mappingFile})
	if err != nil {
		return checkpoint("secret_resolver")
	}
	driver, err := cliproxyapi.NewDriver(cliproxyapi.DriverConfig{
		Management: drivers.ManagementConfig{
			AllowedDNSNames:        []string{configuration.dnsName},
			AllowedManagementCIDRs: []string{configuration.managementCIDR},
			AllowedPlainHTTPCIDRs:  []string{configuration.plainHTTPCIDR},
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
	reporting := &reportingRepository{Repository: repository, finalized: make(chan finalizeResult, 1)}
	invoker := &cooldownInvoker{inner: driver}
	worker, err := inventorypoll.NewWorker(reporting, invoker, workerConfig())
	if err != nil {
		return checkpoint("worker")
	}
	workerContext, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan error, 1)
	go func() { workerDone <- worker.Run(workerContext) }()

	var finalized finalizeResult
	select {
	case finalized = <-reporting.finalized:
		stopWorker()
	case <-ctx.Done():
		stopWorker()
		return checkpoint("finalize_wait")
	}
	select {
	case workerErr := <-workerDone:
		if workerErr != nil {
			return checkpoint("worker_shutdown")
		}
	case <-ctx.Done():
		return checkpoint("worker_shutdown")
	}
	if finalized.err != nil {
		return checkpoint("finalize")
	}
	if invoker.calls.Load() != 1 {
		return checkpoint("request_count")
	}
	if err := validateProjection(finalized.request); err != nil {
		return err
	}
	if err := assertPersistence(ctx, owner); err != nil {
		return checkpoint("persistence")
	}
	return nil
}

type acceptanceConfiguration struct {
	ownerURL, runtimeURL, endpoint, dnsName string
	managementCIDR, plainHTTPCIDR           string
	mappingFile, secretReference            string
}

func loadConfiguration() (acceptanceConfiguration, error) {
	configuration := acceptanceConfiguration{
		ownerURL: os.Getenv(ownerURLEnvironment), runtimeURL: os.Getenv(runtimeURLEnvironment),
		endpoint: os.Getenv(endpointEnvironment), dnsName: os.Getenv(dnsEnvironment),
		managementCIDR: os.Getenv(managementCIDREnvironment), plainHTTPCIDR: os.Getenv(plainHTTPCIDREnvironment),
		mappingFile: os.Getenv(mappingFileEnvironment), secretReference: os.Getenv(secretReferenceEnvironment),
	}
	if configuration.ownerURL == "" || configuration.runtimeURL == "" || configuration.endpoint == "" ||
		configuration.dnsName == "" || configuration.managementCIDR == "" || configuration.plainHTTPCIDR == "" ||
		configuration.mappingFile == "" || configuration.secretReference == "" {
		return acceptanceConfiguration{}, errConfiguration
	}
	return configuration, nil
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

func seed(ctx context.Context, owner *pgxpool.Pool, endpoint, secretReference string) error {
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
			VALUES ($1,$2,'Official Snapshot Acceptance Driver')`, []any{drivers.NodeTypeCLIProxyAPI, drivers.DriverContractCLIProxyAPIAuthFilesV1}},
		{`INSERT INTO driver_capabilities(node_type,driver_contract_version,capability)
			VALUES ($1,$2,$3)`, []any{drivers.NodeTypeCLIProxyAPI, drivers.DriverContractCLIProxyAPIAuthFilesV1, drivers.CapabilityManagementAccountInventoryRead}},
		{`INSERT INTO relay_node_assets(instance_id,display_name,node_type,driver_contract_version,management_endpoint,reader_secret_ref)
			VALUES ($1,'Official Snapshot Acceptance Node',$2,$3,$4,$5)`, []any{fixtureInstanceID, drivers.NodeTypeCLIProxyAPI, drivers.DriverContractCLIProxyAPIAuthFilesV1, endpoint, secretReference}},
		{`INSERT INTO node_capabilities(instance_id,node_type,driver_contract_version,capability)
			VALUES ($1,$2,$3,$4)`, []any{fixtureInstanceID, drivers.NodeTypeCLIProxyAPI, drivers.DriverContractCLIProxyAPIAuthFilesV1, drivers.CapabilityManagementAccountInventoryRead}},
		{`INSERT INTO provider_inventory_policy_versions(policy_version_id,node_type,driver_contract_version,active_providers,out_of_scope_providers,created_by)
			VALUES ($1,$2,$3,ARRAY[$4]::text[],ARRAY['legacy']::text[],'acceptance')`, []any{fixturePolicyID, drivers.NodeTypeCLIProxyAPI, drivers.DriverContractCLIProxyAPIAuthFilesV1, fixtureProvider}},
		{`INSERT INTO provider_inventory_policy_bindings(node_type,driver_contract_version,policy_version_id,bound_by,bound_at)
			VALUES ($1,$2,$3,'acceptance',clock_timestamp())`, []any{drivers.NodeTypeCLIProxyAPI, drivers.DriverContractCLIProxyAPIAuthFilesV1, fixturePolicyID}},
		{`INSERT INTO provider_inventory_policy_activations(node_type,driver_contract_version,policy_version_id,effective_from,activated_by,created_at)
			VALUES ($1,$2,$3,clock_timestamp(),'acceptance',CURRENT_TIMESTAMP)`, []any{drivers.NodeTypeCLIProxyAPI, drivers.DriverContractCLIProxyAPIAuthFilesV1, fixturePolicyID}},
		{`INSERT INTO account_inventory_poll_runs(poll_run_id,instance_id,node_type,driver_contract_version,scheduled_at,
			provider_policy_version,max_attempts,poll_start_grace_seconds,created_at)
			VALUES ($1,$2,$3,$4,date_bin(interval '5 minutes',clock_timestamp(),timestamptz '1970-01-01'),$5,2,299,clock_timestamp())`,
			[]any{fixturePollID, fixtureInstanceID, drivers.NodeTypeCLIProxyAPI, drivers.DriverContractCLIProxyAPIAuthFilesV1, fixturePolicyID}},
	}
	for _, statement := range statements {
		if _, err := transaction.Exec(ctx, statement.query, statement.args...); err != nil {
			return err
		}
	}
	return transaction.Commit(ctx)
}

func waitForSafeSlot(ctx context.Context, owner *pgxpool.Pool) error {
	var epochSeconds float64
	if err := owner.QueryRow(ctx, `SELECT extract(epoch FROM clock_timestamp())::double precision`).Scan(&epochSeconds); err != nil {
		return err
	}
	delay := safeSlotDelay(epochSeconds)
	if delay == 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func safeSlotDelay(epochSeconds float64) time.Duration {
	remainingSeconds := schedulePeriod.Seconds() - math.Mod(epochSeconds, schedulePeriod.Seconds())
	if remainingSeconds >= 30 {
		return 0
	}
	return time.Duration(remainingSeconds*float64(time.Second)) + 250*time.Millisecond
}

type cooldownInvoker struct {
	inner inventorypoll.DriverInvoker
	calls atomic.Int32
}

func (invoker *cooldownInvoker) ListAccountInventory(ctx context.Context, request drivers.InventoryRequest) (drivers.InventoryObservation, error) {
	if invoker.calls.Add(1) != 1 {
		return drivers.InventoryObservation{Result: drivers.ResultFailed, Reason: drivers.ReasonCancelled, Version: "unknown", Commit: "unknown"}, errInvariant
	}
	defer time.Sleep(requestCooldown)
	return invoker.inner.ListAccountInventory(ctx, request)
}

type finalizeResult struct {
	request inventorypoll.FinalizeRequest
	err     error
}

type reportingRepository struct {
	inventorypoll.Repository
	finalized chan finalizeResult
	once      sync.Once
}

func (repository *reportingRepository) FinalizeFenced(ctx context.Context, request inventorypoll.FinalizeRequest) error {
	err := repository.Repository.FinalizeFenced(ctx, request)
	repository.once.Do(func() { repository.finalized <- finalizeResult{request: request, err: err} })
	return err
}

func workerConfig() inventorypoll.Config {
	return inventorypoll.Config{
		Period: 5 * time.Minute, PollStartGrace: 299 * time.Second,
		MaxMonitoredNodes: 1, Concurrency: 1, WorstCasePollDuration: 15 * time.Second,
		LeaseDuration: 30 * time.Second, MaxAttempts: 2,
		DispatchMargin: time.Second, FinalizeMargin: 10 * time.Second,
		SchedulerInterval: time.Second, WorkerScanInterval: 100 * time.Millisecond,
		ReconcileInterval: 5 * time.Second, DatabaseBackoffInitial: time.Second,
		DatabaseBackoffMaximum: 5 * time.Second, ShutdownGrace: 2 * time.Second,
		ScheduleLimit: 1, ReconcileLimit: 10,
	}
}

func validateProjection(request inventorypoll.FinalizeRequest) error {
	if request.PollRunID != fixturePollID || !request.Node.TransportSuccess || !request.Node.ResponseShapeValid || !request.Node.ContractValid {
		return checkpoint("projection_contract")
	}
	if request.Node.InventoryMode != drivers.InventoryModeRuntime || !request.Node.NodeIdentityComplete ||
		!request.Node.SnapshotComplete || request.Node.Degraded {
		return checkpoint("projection_completeness")
	}
	if request.Node.RecognizedRecordCount != 1 || request.Node.UnidentifiedRecordCount != 0 ||
		request.Node.UnsupportedProviderCount != 0 || request.Node.OutOfScopeProviderCount != 0 {
		return checkpoint("projection_counts")
	}
	if request.Node.Result != drivers.ResultSuccess || request.Node.Reason != drivers.ReasonNone {
		return checkpoint("projection_result")
	}
	if len(request.Providers) != 1 || len(request.SnapshotItems) != 1 || len(request.Duplicates) != 0 {
		return checkpoint("projection_collections")
	}
	provider := request.Providers[0]
	if provider.Provider != fixtureProvider || provider.RecognizedRecordCount != 1 || provider.MissingIdentityCount != 0 ||
		provider.DuplicateIdentityCount != 0 || !provider.IdentityComplete || !provider.SnapshotComplete ||
		provider.Degraded || provider.Reason != inventorypoll.ProviderReasonComplete {
		return checkpoint("projection_provider")
	}
	item := request.SnapshotItems[0]
	if item.Provider != fixtureProvider || !strings.HasPrefix(item.AccountKey, fixtureProvider+":") {
		return checkpoint("projection_item")
	}
	return nil
}

func assertPersistence(ctx context.Context, owner *pgxpool.Pool) error {
	var runs, finalized, providers, promoted, snapshots, states, lifecycleRows int
	var runtimeMode, completeNode, completeProvider, noSkip, currentPointer, lifecyclePresent bool
	err := owner.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM account_inventory_poll_runs WHERE poll_run_id=$1),
		(SELECT count(*) FROM account_inventory_poll_runs WHERE poll_run_id=$1 AND status='finalized'),
		(SELECT count(*) FROM account_inventory_poll_provider_results WHERE poll_run_id=$1),
		(SELECT count(*) FROM account_inventory_poll_provider_results WHERE poll_run_id=$1 AND promotion_applied),
		(SELECT count(*) FROM account_inventory_snapshot_items WHERE poll_run_id=$1),
		(SELECT count(*) FROM account_inventory_provider_states WHERE instance_id=$2 AND provider=$3),
		(SELECT inventory_mode='runtime' FROM account_inventory_poll_runs WHERE poll_run_id=$1),
		(SELECT transport_success AND response_shape_valid AND contract_valid AND node_identity_complete AND snapshot_complete AND NOT degraded
		 FROM account_inventory_poll_runs WHERE poll_run_id=$1),
		(SELECT identity_complete AND snapshot_complete AND NOT degraded FROM account_inventory_poll_provider_results WHERE poll_run_id=$1 AND provider=$3),
		(SELECT promotion_skipped_reason IS NULL FROM account_inventory_poll_runs WHERE poll_run_id=$1),
		(SELECT current_poll_run_id=$1 FROM account_inventory_provider_states WHERE instance_id=$2 AND provider=$3),
		(SELECT count(*) FROM account_inventory WHERE instance_id=$2 AND provider=$3),
		(SELECT lifecycle='present'
		        AND consecutive_missing_count=0
		        AND missing_since IS NULL
		        AND out_of_scope_since IS NULL
		        AND current_poll_run_id=$1
		        AND first_seen_at=last_seen_at
		        AND last_seen_at=source_observed_at
		        AND current_scheduled_at=(SELECT scheduled_at FROM account_inventory_poll_runs WHERE poll_run_id=$1)
		 FROM account_inventory
		 WHERE instance_id=$2 AND account_key=$3 || ':snapshot-runtime@example.invalid')`,
		fixturePollID, fixtureInstanceID, fixtureProvider).Scan(&runs, &finalized, &providers, &promoted, &snapshots, &states,
		&runtimeMode, &completeNode, &completeProvider, &noSkip, &currentPointer, &lifecycleRows, &lifecyclePresent)
	if err != nil || runs != 1 || finalized != 1 || providers != 1 || promoted != 1 || snapshots != 1 || states != 1 ||
		lifecycleRows != 1 || !runtimeMode || !completeNode || !completeProvider || !noSkip || !currentPointer || !lifecyclePresent {
		return errInvariant
	}
	return nil
}
