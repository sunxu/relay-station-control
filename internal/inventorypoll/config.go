package inventorypoll

import (
	"context"
	"time"
)

const (
	DefaultPeriod                = 5 * time.Minute
	DefaultPollStartGrace        = 120 * time.Second
	DefaultConcurrency           = 10
	DefaultWorstCasePollDuration = 15 * time.Second
	DefaultLeaseDuration         = 30 * time.Second
	DefaultMaxAttempts           = 2
	DefaultDispatchMargin        = 10 * time.Second
	DefaultFinalizeMargin        = 10 * time.Second
	// Claim and observer budgets are fixed parts of the dispatch safety model.
	// They are deliberately not configurable: capacity must not depend on an
	// unbounded database claim or lifecycle callback.
	DefaultClaimBudget             = time.Second
	DefaultLifecycleObserverBudget = 30 * time.Second
	DefaultSchedulerInterval       = time.Second
	DefaultWorkerScanInterval      = 500 * time.Millisecond
	DefaultReconcileInterval       = 20 * time.Second
	DefaultDatabaseBackoffInitial  = time.Second
	DefaultDatabaseBackoffMaximum  = 30 * time.Second
	DefaultShutdownGrace           = 20 * time.Second
	DefaultReconcileLimit          = 100

	MaximumConcurrency    = 50
	MaximumMonitoredNodes = 50
	MaximumAttempts       = 2
)

type Config struct {
	Period                 time.Duration
	PollStartGrace         time.Duration
	Concurrency            int
	WorstCasePollDuration  time.Duration
	LeaseDuration          time.Duration
	MaxAttempts            int
	DispatchMargin         time.Duration
	FinalizeMargin         time.Duration
	SchedulerInterval      time.Duration
	WorkerScanInterval     time.Duration
	ReconcileInterval      time.Duration
	DatabaseBackoffInitial time.Duration
	DatabaseBackoffMaximum time.Duration
	ShutdownGrace          time.Duration
	ReconcileLimit         int
	Clock                  Clock
	Observer               Observer
	// LifecycleObserver is an optional hook for a caller that wants to react
	// to Account Inventory truth becoming available, changing, or simply
	// aging (freshness/staleness is time-dependent, not only
	// finalize-dependent), without this package needing to know what that
	// caller does (design intent: reuse this existing control loop as the
	// sole trigger for a downstream reconciliation, instead of adding a
	// second scheduler). It is called:
	//
	//  1. once per successful Reconciler.ReconcileOnce database round trip
	//     (Reconciler.Run's periodic loop, and transitively
	//     Service.reconcileUntilAvailable's startup barrier, since that
	//     barrier is itself a ReconcileOnce call) -- this covers both
	//     startup catch-up and ongoing periodic freshness/time-based state
	//     evolution, even when ReconcileExpired reports zero retry-wait and
	//     zero abandoned runs, and
	//  2. once per Worker-claimed run immediately after that run's
	//     FinalizeFenced call commits successfully (low-latency trigger for
	//     brand-new Account Inventory truth).
	//
	// It is never called when ReconcileExpired or a finalize fails, never
	// blocks Scheduler or Worker dispatch beyond the single claimed run or
	// reconcile pass it followed, and any error it produces is the
	// caller's own concern -- this package never inspects, logs, or
	// retries it. Defaults to a no-op when nil.
	LifecycleObserver func(ctx context.Context)
}

type ValidatedConfig struct {
	period                 time.Duration
	pollStartGrace         time.Duration
	maxMonitoredNodes      int
	concurrency            int
	worstCasePollDuration  time.Duration
	leaseDuration          time.Duration
	maxAttempts            int
	dispatchMargin         time.Duration
	finalizeMargin         time.Duration
	schedulerInterval      time.Duration
	workerScanInterval     time.Duration
	reconcileInterval      time.Duration
	databaseBackoffInitial time.Duration
	databaseBackoffMaximum time.Duration
	shutdownGrace          time.Duration
	scheduleLimit          int
	reconcileLimit         int
	clock                  Clock
	observer               Observer
	lifecycleObserver      func(ctx context.Context)
}

func (configuration Config) Validate() (ValidatedConfig, error) {
	configuration.applyDefaults()
	if configuration.Period != DefaultPeriod ||
		configuration.PollStartGrace <= 0 || configuration.PollStartGrace >= configuration.Period ||
		configuration.Concurrency < 1 || configuration.Concurrency > MaximumConcurrency ||
		configuration.WorstCasePollDuration < time.Second || configuration.WorstCasePollDuration > DefaultWorstCasePollDuration ||
		configuration.LeaseDuration <= 0 || configuration.LeaseDuration > configuration.PollStartGrace ||
		configuration.MaxAttempts < 1 || configuration.MaxAttempts > MaximumAttempts ||
		configuration.DispatchMargin <= 0 || configuration.FinalizeMargin <= 0 ||
		configuration.SchedulerInterval <= 0 || configuration.SchedulerInterval > configuration.Period ||
		configuration.WorkerScanInterval <= 0 || configuration.WorkerScanInterval > configuration.PollStartGrace ||
		configuration.ReconcileInterval <= 0 || configuration.ReconcileInterval >= configuration.LeaseDuration ||
		configuration.DatabaseBackoffInitial <= 0 || configuration.DatabaseBackoffMaximum < configuration.DatabaseBackoffInitial ||
		configuration.DatabaseBackoffMaximum > configuration.Period ||
		configuration.ShutdownGrace <= 0 || configuration.ShutdownGrace > time.Minute ||
		configuration.ReconcileLimit < 1 || configuration.ReconcileLimit > 1000 {
		return ValidatedConfig{}, ErrInvalidConfig
	}
	derived := deriveCapacity(configuration.Concurrency, configuration.WorstCasePollDuration,
		configuration.FinalizeMargin, DefaultLifecycleObserverBudget, DefaultClaimBudget,
		configuration.DispatchMargin, configuration.PollStartGrace)
	if derived < 1 || configuration.LeaseDuration < configuration.WorstCasePollDuration+configuration.FinalizeMargin {
		return ValidatedConfig{}, ErrInvalidConfig
	}
	clock := configuration.Clock
	if clock == nil {
		clock = RealClock{}
	}
	observer := configuration.Observer
	if observer == nil {
		observer = discardObserver{}
	}
	lifecycleObserver := configuration.LifecycleObserver
	if lifecycleObserver == nil {
		lifecycleObserver = func(context.Context) {}
	}
	return ValidatedConfig{
		period: configuration.Period, pollStartGrace: configuration.PollStartGrace,
		maxMonitoredNodes: derived, concurrency: configuration.Concurrency,
		worstCasePollDuration: configuration.WorstCasePollDuration, leaseDuration: configuration.LeaseDuration,
		maxAttempts: configuration.MaxAttempts, dispatchMargin: configuration.DispatchMargin,
		finalizeMargin: configuration.FinalizeMargin, schedulerInterval: configuration.SchedulerInterval,
		workerScanInterval: configuration.WorkerScanInterval, reconcileInterval: configuration.ReconcileInterval,
		databaseBackoffInitial: configuration.DatabaseBackoffInitial,
		databaseBackoffMaximum: configuration.DatabaseBackoffMaximum,
		shutdownGrace:          configuration.ShutdownGrace, scheduleLimit: derived,
		reconcileLimit: configuration.ReconcileLimit, clock: clock, observer: observer,
		lifecycleObserver: lifecycleObserver,
	}, nil
}

func (configuration *Config) applyDefaults() {
	if configuration.Period == 0 {
		configuration.Period = DefaultPeriod
	}
	if configuration.PollStartGrace == 0 {
		configuration.PollStartGrace = DefaultPollStartGrace
	}
	if configuration.Concurrency == 0 {
		configuration.Concurrency = DefaultConcurrency
	}
	if configuration.WorstCasePollDuration == 0 {
		configuration.WorstCasePollDuration = DefaultWorstCasePollDuration
	}
	if configuration.LeaseDuration == 0 {
		configuration.LeaseDuration = DefaultLeaseDuration
	}
	if configuration.MaxAttempts == 0 {
		configuration.MaxAttempts = DefaultMaxAttempts
	}
	if configuration.DispatchMargin == 0 {
		configuration.DispatchMargin = DefaultDispatchMargin
	}
	if configuration.FinalizeMargin == 0 {
		configuration.FinalizeMargin = DefaultFinalizeMargin
	}
	if configuration.SchedulerInterval == 0 {
		configuration.SchedulerInterval = DefaultSchedulerInterval
	}
	if configuration.WorkerScanInterval == 0 {
		configuration.WorkerScanInterval = DefaultWorkerScanInterval
	}
	if configuration.ReconcileInterval == 0 {
		configuration.ReconcileInterval = DefaultReconcileInterval
	}
	if configuration.DatabaseBackoffInitial == 0 {
		configuration.DatabaseBackoffInitial = DefaultDatabaseBackoffInitial
	}
	if configuration.DatabaseBackoffMaximum == 0 {
		configuration.DatabaseBackoffMaximum = DefaultDatabaseBackoffMaximum
	}
	if configuration.ShutdownGrace == 0 {
		configuration.ShutdownGrace = DefaultShutdownGrace
	}
	if configuration.ReconcileLimit == 0 {
		configuration.ReconcileLimit = DefaultReconcileLimit
	}
}

// deriveCapacity returns the largest eligible-node count whose first-attempt
// dispatches fit inside the start grace. The semaphore is held through
// finalize and the lifecycle callback, so those bounded costs are part of the
// batch spacing. Claim cost is charged per node as a conservative admission
// budget. This does not promise that retries complete inside the grace.
func deriveCapacity(concurrency int, request, finalize, observer, claim, margin, grace time.Duration) int {
	if concurrency <= 0 || request < 0 || finalize < 0 || observer < 0 || claim < 0 || margin < 0 || grace <= 0 {
		return 0
	}
	batchCost := request + finalize + observer
	for nodes := MaximumMonitoredNodes; nodes >= 1; nodes-- {
		batches := (nodes + concurrency - 1) / concurrency
		if time.Duration(batches-1)*batchCost+time.Duration(nodes)*claim+margin < grace {
			return nodes
		}
	}
	return 0
}

func (configuration ValidatedConfig) LastBatchStart() time.Duration {
	return configuration.CapacityBudget() - configuration.dispatchMargin
}

func (configuration ValidatedConfig) Concurrency() int       { return configuration.concurrency }
func (configuration ValidatedConfig) EffectiveCapacity() int { return configuration.maxMonitoredNodes }
func (configuration ValidatedConfig) DispatchMargin() time.Duration {
	return configuration.dispatchMargin
}
func (configuration ValidatedConfig) FinalizeMargin() time.Duration {
	return configuration.finalizeMargin
}
func (configuration ValidatedConfig) ClaimBudget() time.Duration { return DefaultClaimBudget }
func (configuration ValidatedConfig) LifecycleObserverBudget() time.Duration {
	return DefaultLifecycleObserverBudget
}
func (configuration ValidatedConfig) CapacityBudget() time.Duration {
	nodes := configuration.maxMonitoredNodes
	batches := (nodes + configuration.concurrency - 1) / configuration.concurrency
	return time.Duration(batches-1)*(configuration.worstCasePollDuration+configuration.finalizeMargin+DefaultLifecycleObserverBudget) +
		time.Duration(nodes)*DefaultClaimBudget + configuration.dispatchMargin
}
func (configuration ValidatedConfig) PollStartGrace() time.Duration {
	return configuration.pollStartGrace
}
func (configuration ValidatedConfig) LeaseDuration() time.Duration {
	return configuration.leaseDuration
}
func (configuration ValidatedConfig) RequestTimeout() time.Duration {
	return configuration.worstCasePollDuration
}
func (configuration ValidatedConfig) MaxAttempts() int { return configuration.maxAttempts }
