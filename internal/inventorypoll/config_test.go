package inventorypoll

import (
	"errors"
	"testing"
	"time"
)

func TestDefaultConfigurationMatchesApprovedCapacity(t *testing.T) {
	configuration, err := (Config{}).Validate()
	if err != nil {
		t.Fatalf("default configuration: %v", err)
	}
	if configuration.period != 5*time.Minute || configuration.pollStartGrace != 120*time.Second ||
		configuration.maxMonitoredNodes != 20 || configuration.concurrency != 10 ||
		configuration.worstCasePollDuration != 15*time.Second || configuration.leaseDuration != 30*time.Second || configuration.reconcileInterval != 20*time.Second ||
		configuration.maxAttempts != 2 || configuration.CapacityBudget() != 85*time.Second {
		t.Fatalf("unexpected defaults: %#v", configuration)
	}
	if configuration.CapacityBudget() >= configuration.pollStartGrace {
		t.Fatal("default capacity has no dispatch margin")
	}
	if configuration.leaseDuration < configuration.worstCasePollDuration+configuration.finalizeMargin {
		t.Fatal("default lease cannot cover request and finalize")
	}
}

func TestConfigurationClosedInvalidMatrix(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"period not five minutes", func(config *Config) { config.Period = 4 * time.Minute }},
		{"grace at period", func(config *Config) { config.PollStartGrace = 5 * time.Minute }},
		{"unbounded concurrency", func(config *Config) { config.Concurrency = 51 }},
		{"request exceeds approved maximum", func(config *Config) { config.WorstCasePollDuration = 16 * time.Second }},
		{"lease cannot finalize", func(config *Config) { config.LeaseDuration = 24 * time.Second }},
		{"attempts exceed bound", func(config *Config) { config.MaxAttempts = 3 }},
		{"capacity consumes margin", func(config *Config) { config.DispatchMargin = 119 * time.Second }},
		{"reconcile at lease", func(config *Config) { config.ReconcileInterval = 30 * time.Second }},
		{"backoff inverted", func(config *Config) {
			config.DatabaseBackoffInitial = 10 * time.Second
			config.DatabaseBackoffMaximum = time.Second
		}},
		{"scan unbounded", func(config *Config) { config.WorkerScanInterval = 3 * time.Minute }},
		{"shutdown unbounded", func(config *Config) { config.ShutdownGrace = 2 * time.Minute }},
		{"reconcile limit unbounded", func(config *Config) { config.ReconcileLimit = 1001 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := Config{}
			test.mutate(&config)
			_, err := config.Validate()
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestReconcileIntervalCannotEqualLease(t *testing.T) {
	if _, err := (Config{LeaseDuration: 30 * time.Second, ReconcileInterval: 30 * time.Second}).Validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("reconcile interval equal to lease accepted: %v", err)
	}
}

func TestDerivedCapacityBoundary(t *testing.T) {
	for _, test := range []struct {
		concurrency, want int
	}{{1, 2}, {2, 4}, {6, 12}, {7, 14}, {10, 20}, {25, 50}, {50, 50}} {
		configuration := Config{Concurrency: test.concurrency}
		validated, err := configuration.Validate()
		if err != nil {
			t.Fatalf("C=%d: %v", test.concurrency, err)
		}
		if validated.EffectiveCapacity() != test.want {
			t.Errorf("C=%d capacity=%d, want %d", test.concurrency, validated.EffectiveCapacity(), test.want)
		}
	}
	configuration := Config{Concurrency: 1, PollStartGrace: 11 * time.Second,
		WorstCasePollDuration: time.Second, FinalizeMargin: time.Second,
		LeaseDuration: 3 * time.Second}
	if _, err := configuration.Validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected no feasible capacity at equality, got %v", err)
	}
}

func TestPollStatusClosedTransitionMatrix(t *testing.T) {
	allowed := map[[2]Status]bool{
		{StatusPending, StatusRunning}: true, {StatusPending, StatusAbandoned}: true,
		{StatusRunning, StatusFinalized}: true, {StatusRunning, StatusRetryWait}: true,
		{StatusRunning, StatusAbandoned}: true, {StatusRetryWait, StatusRunning}: true,
		{StatusRetryWait, StatusAbandoned}: true,
	}
	for _, from := range AllStatuses {
		if !from.Valid() {
			t.Fatalf("registered status invalid: %q", from)
		}
		for _, to := range AllStatuses {
			if got := CanTransition(from, to); got != allowed[[2]Status{from, to}] {
				t.Errorf("CanTransition(%s,%s)=%v", from, to, got)
			}
		}
	}
	if !StatusFinalized.Terminal() || !StatusAbandoned.Terminal() || StatusRunning.Terminal() || Status("other").Valid() {
		t.Fatal("terminal or validity classification is open")
	}
}

func smallTestConfig() Config {
	return Config{
		PollStartGrace: 60 * time.Second, Concurrency: 1,
		WorstCasePollDuration: time.Second, LeaseDuration: 3 * time.Second,
		DispatchMargin: time.Second, FinalizeMargin: time.Second,
		SchedulerInterval: 10 * time.Millisecond, WorkerScanInterval: 10 * time.Millisecond,
		ReconcileInterval:      20 * time.Millisecond,
		DatabaseBackoffInitial: 10 * time.Millisecond, DatabaseBackoffMaximum: 40 * time.Millisecond,
		ShutdownGrace: 200 * time.Millisecond, ReconcileLimit: 10,
	}
}
