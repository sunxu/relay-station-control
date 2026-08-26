package main

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	controlpoll "github.com/sunxu/relay-station-control/internal/inventorypoll"
	controlpollobs "github.com/sunxu/relay-station-control/internal/pollobservability"
	controlstore "github.com/sunxu/relay-station-control/internal/store"
)

type accountInventoryPollRuntimeConfig struct {
	enabled          bool
	lifecycleEnabled bool
	poll             controlpoll.Config
}

type accountInventoryPollRuntime struct {
	enabled   bool
	service   *controlpoll.Service
	collector *controlpollobs.Collector
}

type accountInventoryPollMetricsStore interface {
	MetricsWithLifecycle(context.Context, bool) (
		[]controlstore.PollRunMetric, []controlstore.PollProviderMetric,
		[]controlstore.AccountInventoryLifecycleMetric, error,
	)
}

type accountInventoryPollMetricsProvider struct {
	store            accountInventoryPollMetricsStore
	lifecycleEnabled bool
}

type accountInventoryPollLogObserver struct {
	observer *controlpollobs.Observer
}

func (adapter accountInventoryPollLogObserver) Observe(ctx context.Context, event controlpoll.Event) {
	if adapter.observer == nil {
		return
	}
	record := controlpollobs.LogRecord{
		Component: controlpollobs.Component(event.Component), Action: controlpollobs.Action(event.Action),
		Result: controlpollobs.LogResult(event.Result), Reason: controlpollobs.Reason(event.Reason),
		State: controlpollobs.State(event.State), AttemptBucket: controlpollobs.AttemptBucket(event.AttemptBucket),
		InstanceID: event.InstanceID,
	}
	if event.InstanceID != uuid.Nil {
		record.NodeType = controlpollobs.NodeTypeCLIProxyAPI
	}
	adapter.observer.Record(ctx, record)
}

func loadAccountInventoryPollRuntimeConfig() (accountInventoryPollRuntimeConfig, error) {
	invalid := func() (accountInventoryPollRuntimeConfig, error) {
		return accountInventoryPollRuntimeConfig{}, errors.New("account inventory poll configuration is invalid")
	}
	enabled, err := envBool("CONTROL_ACCOUNT_INVENTORY_POLL_ENABLED", false)
	if err != nil {
		return invalid()
	}
	lifecycleEnabled, err := envBool("CONTROL_ACCOUNT_INVENTORY_LIFECYCLE_ENABLED", false)
	if err != nil || enabled && !lifecycleEnabled {
		return invalid()
	}
	maxNodes, err := envIntBounded("CONTROL_ACCOUNT_INVENTORY_POLL_MAX_NODES", controlpoll.DefaultMaxMonitoredNodes, 1, controlpoll.MaximumMonitoredNodes)
	if err != nil {
		return invalid()
	}
	concurrency, err := envIntBounded("CONTROL_ACCOUNT_INVENTORY_POLL_CONCURRENCY", controlpoll.DefaultConcurrency, 1, controlpoll.MaximumConcurrency)
	if err != nil {
		return invalid()
	}
	grace, err := envDurationBounded("CONTROL_ACCOUNT_INVENTORY_POLL_START_GRACE", controlpoll.DefaultPollStartGrace, time.Second, 299*time.Second)
	if err != nil {
		return invalid()
	}
	worstCase, err := envDurationBounded("CONTROL_ACCOUNT_INVENTORY_POLL_REQUEST_TIMEOUT", controlpoll.DefaultWorstCasePollDuration, time.Second, controlpoll.DefaultWorstCasePollDuration)
	if err != nil {
		return invalid()
	}
	lease, err := envDurationBounded("CONTROL_ACCOUNT_INVENTORY_POLL_LEASE", controlpoll.DefaultLeaseDuration, time.Second, 120*time.Second)
	if err != nil {
		return invalid()
	}
	maxAttempts, err := envIntBounded("CONTROL_ACCOUNT_INVENTORY_POLL_MAX_ATTEMPTS", controlpoll.DefaultMaxAttempts, 1, controlpoll.MaximumAttempts)
	if err != nil {
		return invalid()
	}
	schedulerInterval, err := envDurationBounded("CONTROL_ACCOUNT_INVENTORY_POLL_SCHEDULER_INTERVAL", controlpoll.DefaultSchedulerInterval, 100*time.Millisecond, time.Minute)
	if err != nil {
		return invalid()
	}
	workerScanInterval, err := envDurationBounded("CONTROL_ACCOUNT_INVENTORY_POLL_WORKER_SCAN_INTERVAL", controlpoll.DefaultWorkerScanInterval, 100*time.Millisecond, time.Minute)
	if err != nil {
		return invalid()
	}
	reconcileInterval, err := envDurationBounded("CONTROL_ACCOUNT_INVENTORY_POLL_RECONCILE_INTERVAL", controlpoll.DefaultReconcileInterval, time.Second, 29*time.Second)
	if err != nil {
		return invalid()
	}
	databaseBackoffInitial, err := envDurationBounded("CONTROL_ACCOUNT_INVENTORY_POLL_DATABASE_BACKOFF_INITIAL", controlpoll.DefaultDatabaseBackoffInitial, 100*time.Millisecond, time.Minute)
	if err != nil {
		return invalid()
	}
	databaseBackoffMaximum, err := envDurationBounded("CONTROL_ACCOUNT_INVENTORY_POLL_DATABASE_BACKOFF_MAXIMUM", controlpoll.DefaultDatabaseBackoffMaximum, time.Second, 5*time.Minute)
	if err != nil {
		return invalid()
	}
	shutdownGrace, err := envDurationBounded("CONTROL_ACCOUNT_INVENTORY_POLL_SHUTDOWN_GRACE", controlpoll.DefaultShutdownGrace, time.Second, time.Minute)
	if err != nil {
		return invalid()
	}
	poll := controlpoll.Config{
		Period: controlpoll.DefaultPeriod, PollStartGrace: grace,
		MaxMonitoredNodes: maxNodes, Concurrency: concurrency,
		WorstCasePollDuration: worstCase, LeaseDuration: lease, MaxAttempts: maxAttempts,
		DispatchMargin: controlpoll.DefaultDispatchMargin, FinalizeMargin: controlpoll.DefaultFinalizeMargin,
		SchedulerInterval: schedulerInterval, WorkerScanInterval: workerScanInterval,
		ReconcileInterval: reconcileInterval, DatabaseBackoffInitial: databaseBackoffInitial,
		DatabaseBackoffMaximum: databaseBackoffMaximum, ShutdownGrace: shutdownGrace,
		ScheduleLimit: maxNodes, ReconcileLimit: controlpoll.DefaultReconcileLimit,
	}
	if _, err = poll.Validate(); err != nil {
		return invalid()
	}
	return accountInventoryPollRuntimeConfig{enabled: enabled, lifecycleEnabled: lifecycleEnabled, poll: poll}, nil
}

func newAccountInventoryPollRuntime(
	pool *pgxpool.Pool,
	nodeDrivers nodeDriverRuntime,
	configuration accountInventoryPollRuntimeConfig,
	logger *slog.Logger,
) (accountInventoryPollRuntime, error) {
	repository, err := controlstore.NewInventoryPollRepository(pool)
	if err != nil {
		return accountInventoryPollRuntime{}, err
	}
	if configuration.lifecycleEnabled {
		compatibilityContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = repository.CheckLifecycleCompatibility(compatibilityContext)
		cancel()
		if err != nil {
			return accountInventoryPollRuntime{}, errors.New("account inventory lifecycle database is incompatible")
		}
	}
	collector, err := controlpollobs.NewCollector(
		accountInventoryPollMetricsProvider{store: repository, lifecycleEnabled: configuration.lifecycleEnabled},
		configuration.poll.ConfiguredMaxMonitoredNodes(),
	)
	if err != nil {
		return accountInventoryPollRuntime{}, err
	}
	runtime := accountInventoryPollRuntime{enabled: configuration.enabled, collector: collector}
	if !configuration.enabled {
		return runtime, nil
	}
	if !nodeDrivers.enabled || nodeDrivers.registry == nil {
		return accountInventoryPollRuntime{}, errors.New("account inventory poll requires the read-only node driver")
	}
	configuration.poll.Observer = accountInventoryPollLogObserver{observer: controlpollobs.NewObserver(logger)}
	service, err := controlpoll.NewService(repository, nodeDrivers.registry, configuration.poll)
	if err != nil {
		return accountInventoryPollRuntime{}, err
	}
	runtime.service = service
	return runtime, nil
}

func (provider accountInventoryPollMetricsProvider) AccountInventoryPollMetricsSnapshot(
	ctx context.Context,
) (controlpollobs.Snapshot, error) {
	if provider.store == nil {
		return controlpollobs.Snapshot{}, errors.New("account inventory poll metrics unavailable")
	}
	runs, providers, lifecycles, err := provider.store.MetricsWithLifecycle(ctx, provider.lifecycleEnabled)
	if err != nil {
		return controlpollobs.Snapshot{}, errors.New("account inventory poll metrics unavailable")
	}
	instances := make(map[uuid.UUID]*controlpollobs.InstanceSnapshot, len(runs))
	snapshot := controlpollobs.Snapshot{Instances: make([]controlpollobs.InstanceSnapshot, 0, len(runs))}
	for _, run := range runs {
		if run.InstanceID == uuid.Nil {
			return controlpollobs.Snapshot{}, errors.New("account inventory poll metrics unavailable")
		}
		item := controlpollobs.InstanceSnapshot{
			InstanceID: run.InstanceID, State: controlpollobs.State(run.Status),
			SchedulerLagSeconds: durationSeconds(run.SchedulerLag),
			QueueWaitSeconds:    durationSeconds(run.QueueWait),
			PollStartLagSeconds: durationSecondsOptional(run.PollStartLag),
			TransportSuccess:    run.TransportSuccess, ContractValid: run.ContractValid,
		}
		snapshot.Instances = append(snapshot.Instances, item)
		instances[run.InstanceID] = &snapshot.Instances[len(snapshot.Instances)-1]
	}
	allowed := make(map[string]struct{})
	for _, metric := range providers {
		instance, ok := instances[metric.InstanceID]
		if !ok {
			return controlpollobs.Snapshot{}, errors.New("account inventory poll metrics unavailable")
		}
		allowed[metric.Provider] = struct{}{}
		instance.Providers = append(instance.Providers, controlpollobs.ProviderSnapshot{
			Provider: metric.Provider, SnapshotComplete: metric.SnapshotComplete,
			PromotionEvaluated: metric.PromotionEvaluated,
			PromotionApplied:   metric.PromotionApplied,
			PromotionSkippedReason: controlpollobs.PromotionSkippedReason(
				metric.PromotionSkippedReason,
			),
		})
	}
	for _, metric := range lifecycles {
		allowed[metric.Provider] = struct{}{}
		snapshot.Lifecycles = append(snapshot.Lifecycles, controlpollobs.LifecycleSnapshot{
			InstanceID: metric.InstanceID, Provider: metric.Provider,
			Lifecycle: controlpollobs.AccountLifecycle(metric.Lifecycle), Count: metric.Count,
		})
	}
	for providerName := range allowed {
		snapshot.AllowedProviders = append(snapshot.AllowedProviders, providerName)
	}
	sort.Strings(snapshot.AllowedProviders)
	for index := range snapshot.Instances {
		sort.Slice(snapshot.Instances[index].Providers, func(left, right int) bool {
			return snapshot.Instances[index].Providers[left].Provider < snapshot.Instances[index].Providers[right].Provider
		})
	}
	sort.Slice(snapshot.Lifecycles, func(left, right int) bool {
		if snapshot.Lifecycles[left].InstanceID != snapshot.Lifecycles[right].InstanceID {
			return snapshot.Lifecycles[left].InstanceID.String() < snapshot.Lifecycles[right].InstanceID.String()
		}
		if snapshot.Lifecycles[left].Provider != snapshot.Lifecycles[right].Provider {
			return snapshot.Lifecycles[left].Provider < snapshot.Lifecycles[right].Provider
		}
		return snapshot.Lifecycles[left].Lifecycle < snapshot.Lifecycles[right].Lifecycle
	})
	return snapshot, nil
}

func durationSeconds(duration time.Duration) *float64 {
	seconds := duration.Seconds()
	return &seconds
}

func durationSecondsOptional(duration *time.Duration) *float64 {
	if duration == nil {
		return nil
	}
	return durationSeconds(*duration)
}
