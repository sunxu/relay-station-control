package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	controlhistory "github.com/sunxu/relay-station-control/internal/historyruntime"
	controlstore "github.com/sunxu/relay-station-control/internal/store"
)

const (
	historyEnabledEnvironment                = "CONTROL_ACCOUNT_INVENTORY_HISTORY_ENABLED"
	historyScanIntervalEnvironment           = "CONTROL_ACCOUNT_INVENTORY_HISTORY_SCAN_INTERVAL"
	historyClaimLeaseEnvironment             = "CONTROL_ACCOUNT_INVENTORY_HISTORY_CLAIM_LEASE"
	historyConcurrencyEnvironment            = "CONTROL_ACCOUNT_INVENTORY_HISTORY_CONCURRENCY"
	historyDeleteBatchEnvironment            = "CONTROL_ACCOUNT_INVENTORY_HISTORY_DELETE_BATCH_SIZE"
	historyStatementTimeoutEnvironment       = "CONTROL_ACCOUNT_INVENTORY_HISTORY_STATEMENT_TIMEOUT"
	historyDatabaseBackoffInitialEnvironment = "CONTROL_ACCOUNT_INVENTORY_HISTORY_DATABASE_BACKOFF_INITIAL"
	historyDatabaseBackoffMaximumEnvironment = "CONTROL_ACCOUNT_INVENTORY_HISTORY_DATABASE_BACKOFF_MAXIMUM"
	historyShutdownGraceEnvironment          = "CONTROL_ACCOUNT_INVENTORY_HISTORY_SHUTDOWN_GRACE"
)

type accountInventoryHistoryService interface {
	Run(context.Context) error
}

// accountInventoryHistoryRuntime keeps the repository visible as a narrow
// production-wiring boundary. The metrics collector is registered separately
// from the service lifecycle and must use this same repository instance.
type accountInventoryHistoryRuntime struct {
	enabled    bool
	repository accountInventoryHistoryRepository
	service    accountInventoryHistoryService
}

type accountInventoryHistoryRepository interface {
	controlhistory.Repository
	controlhistory.CompatibilityChecker
	controlhistory.StatusObserver
	controlhistory.MetricsProvider
}

type accountInventoryHistoryRuntimeFactory struct {
	newRepository func(*pgxpool.Pool) (accountInventoryHistoryRepository, error)
	newLoops      func(controlhistory.Config, controlhistory.Repository) (controlhistory.Loops, error)
	newService    func(
		controlhistory.Config,
		controlhistory.CompatibilityChecker,
		controlhistory.Loops,
		controlhistory.StatusObserver,
	) (accountInventoryHistoryService, error)
}

var productionAccountInventoryHistoryRuntimeFactory = accountInventoryHistoryRuntimeFactory{
	newRepository: func(pool *pgxpool.Pool) (accountInventoryHistoryRepository, error) {
		return controlstore.NewAccountInventoryHistoryRepository(pool)
	},
	newLoops: func(configuration controlhistory.Config, repository controlhistory.Repository) (controlhistory.Loops, error) {
		return controlhistory.NewRepositoryLoops(configuration, repository)
	},
	newService: func(
		configuration controlhistory.Config,
		checker controlhistory.CompatibilityChecker,
		loops controlhistory.Loops,
		observer controlhistory.StatusObserver,
	) (accountInventoryHistoryService, error) {
		return controlhistory.NewService(configuration, checker, loops, observer)
	},
}

func loadAccountInventoryHistoryRuntimeConfig() (controlhistory.Config, error) {
	invalid := func() (controlhistory.Config, error) {
		return controlhistory.Config{}, errors.New("account inventory history configuration is invalid")
	}
	enabled, err := envBool(historyEnabledEnvironment, false)
	if err != nil {
		return invalid()
	}
	scanInterval, err := envDurationBounded(
		historyScanIntervalEnvironment, controlhistory.DefaultScanInterval,
		controlhistory.MinimumScanInterval, controlhistory.MaximumScanInterval,
	)
	if err != nil {
		return invalid()
	}
	claimLease, err := envDurationBounded(
		historyClaimLeaseEnvironment, controlhistory.DefaultClaimLease,
		controlhistory.MinimumClaimLease, controlhistory.MaximumClaimLease,
	)
	if err != nil {
		return invalid()
	}
	concurrency, err := envIntBounded(
		historyConcurrencyEnvironment, controlhistory.DefaultConcurrency, 1, controlhistory.MaximumConcurrency,
	)
	if err != nil {
		return invalid()
	}
	deleteBatchSize, err := envIntBounded(
		historyDeleteBatchEnvironment, controlhistory.DefaultDeleteBatchSize, 1, controlhistory.MaximumDeleteBatchSize,
	)
	if err != nil {
		return invalid()
	}
	statementTimeout, err := envDurationBounded(
		historyStatementTimeoutEnvironment, controlhistory.DefaultStatementTimeout,
		controlhistory.MinimumStatementTimeout, controlhistory.MaximumStatementTimeout,
	)
	if err != nil {
		return invalid()
	}
	databaseBackoffInitial, err := envDurationBounded(
		historyDatabaseBackoffInitialEnvironment, controlhistory.DefaultDatabaseBackoffInitial,
		controlhistory.MinimumDatabaseBackoff, controlhistory.MaximumDatabaseBackoff,
	)
	if err != nil {
		return invalid()
	}
	databaseBackoffMaximum, err := envDurationBounded(
		historyDatabaseBackoffMaximumEnvironment, controlhistory.DefaultDatabaseBackoffMaximum,
		controlhistory.MinimumDatabaseBackoff, controlhistory.MaximumDatabaseBackoff,
	)
	if err != nil {
		return invalid()
	}
	shutdownGrace, err := envDurationBounded(
		historyShutdownGraceEnvironment, controlhistory.DefaultShutdownGrace,
		controlhistory.MinimumStatementTimeout, controlhistory.MaximumShutdownGrace,
	)
	if err != nil {
		return invalid()
	}
	configuration := controlhistory.Config{
		Enabled: enabled, ScanInterval: scanInterval, ClaimLease: claimLease,
		Concurrency: concurrency, DeleteBatchSize: deleteBatchSize,
		StatementTimeout: statementTimeout, DatabaseBackoffInitial: databaseBackoffInitial,
		DatabaseBackoffMaximum: databaseBackoffMaximum, ShutdownGrace: shutdownGrace,
	}
	if _, err = configuration.Validate(); err != nil {
		return invalid()
	}
	return configuration, nil
}

func newAccountInventoryHistoryRuntime(
	pool *pgxpool.Pool,
	configuration controlhistory.Config,
) (accountInventoryHistoryRuntime, error) {
	return newAccountInventoryHistoryRuntimeWithFactory(
		pool, configuration, productionAccountInventoryHistoryRuntimeFactory,
	)
}

func newAccountInventoryHistoryRuntimeWithFactory(
	pool *pgxpool.Pool,
	configuration controlhistory.Config,
	factory accountInventoryHistoryRuntimeFactory,
) (accountInventoryHistoryRuntime, error) {
	validated, err := configuration.Validate()
	if err != nil || factory.newRepository == nil || factory.newService == nil ||
		(validated.Enabled() && factory.newLoops == nil) {
		return accountInventoryHistoryRuntime{}, controlhistory.ErrInvalidConfig
	}
	repository, err := factory.newRepository(pool)
	if err != nil || repository == nil {
		return accountInventoryHistoryRuntime{}, errors.New("account inventory history repository initialization failed")
	}

	// The repository and service are constructed even while disabled so startup
	// still runs the read-only M9 compatibility probe and the separately
	// registered collector can expose the result. Only the mutation loops stay
	// absent on this path.
	var loops controlhistory.Loops
	if validated.Enabled() {
		loops, err = factory.newLoops(configuration, repository)
		if err != nil || loops == nil {
			return accountInventoryHistoryRuntime{}, errors.New("account inventory history loops initialization failed")
		}
	}
	service, err := factory.newService(configuration, repository, loops, repository)
	if err != nil || service == nil {
		return accountInventoryHistoryRuntime{}, errors.New("account inventory history service initialization failed")
	}
	return accountInventoryHistoryRuntime{
		enabled: validated.Enabled(), repository: repository, service: service,
	}, nil
}

func runAccountInventoryHistoryRuntime(
	ctx context.Context,
	runtime accountInventoryHistoryRuntime,
	logger *slog.Logger,
) error {
	if runtime.service == nil {
		return controlhistory.ErrInvalidConfig
	}
	err := runtime.service.Run(ctx)
	if err == nil || errors.Is(err, context.Canceled) {
		return err
	}
	reason := "service_stopped"
	switch {
	case errors.Is(err, controlhistory.ErrRuntimeStopped):
		reason = string(controlhistory.ReasonRuntimeStopped)
	case errors.Is(err, controlhistory.ErrShutdownTimedOut):
		reason = "shutdown_timed_out"
	}
	if logger != nil {
		// Never attach err here: repository errors are deliberately collapsed and
		// a future service implementation must not accidentally project raw SQL.
		logger.Error("account inventory history stopped", "component", "account_inventory_history", "reason", reason)
	}
	return err
}
