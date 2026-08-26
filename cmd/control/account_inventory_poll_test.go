package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	controlpollobs "github.com/sunxu/relay-station-control/internal/pollobservability"
	controlstore "github.com/sunxu/relay-station-control/internal/store"
)

type fakeAccountInventoryPollMetricsStore struct {
	runs      []controlstore.PollRunMetric
	providers []controlstore.PollProviderMetric
	err       error
}

func (store fakeAccountInventoryPollMetricsStore) Metrics(context.Context) (
	[]controlstore.PollRunMetric, []controlstore.PollProviderMetric, error,
) {
	return store.runs, store.providers, store.err
}

func TestLoadAccountInventoryPollRuntimeConfigDefaultsAndBounds(t *testing.T) {
	for _, name := range []string{
		"CONTROL_ACCOUNT_INVENTORY_POLL_ENABLED", "CONTROL_ACCOUNT_INVENTORY_POLL_MAX_NODES",
		"CONTROL_ACCOUNT_INVENTORY_POLL_CONCURRENCY", "CONTROL_ACCOUNT_INVENTORY_POLL_START_GRACE",
		"CONTROL_ACCOUNT_INVENTORY_POLL_REQUEST_TIMEOUT", "CONTROL_ACCOUNT_INVENTORY_POLL_LEASE",
		"CONTROL_ACCOUNT_INVENTORY_POLL_MAX_ATTEMPTS", "CONTROL_ACCOUNT_INVENTORY_POLL_SCHEDULER_INTERVAL",
		"CONTROL_ACCOUNT_INVENTORY_POLL_WORKER_SCAN_INTERVAL", "CONTROL_ACCOUNT_INVENTORY_POLL_RECONCILE_INTERVAL",
		"CONTROL_ACCOUNT_INVENTORY_POLL_DATABASE_BACKOFF_INITIAL", "CONTROL_ACCOUNT_INVENTORY_POLL_DATABASE_BACKOFF_MAXIMUM",
		"CONTROL_ACCOUNT_INVENTORY_POLL_SHUTDOWN_GRACE",
	} {
		t.Setenv(name, "")
	}
	configuration, err := loadAccountInventoryPollRuntimeConfig()
	if err != nil {
		t.Fatal(err)
	}
	if configuration.enabled {
		t.Fatal("polling must require explicit enablement")
	}
	validated, err := configuration.poll.Validate()
	if err != nil || validated.Concurrency() != 10 || validated.PollStartGrace() != 120*time.Second ||
		validated.LeaseDuration() != 30*time.Second || validated.MaxAttempts() != 2 {
		t.Fatalf("unexpected defaults: config=%+v err=%v", configuration.poll, err)
	}

	t.Setenv("CONTROL_ACCOUNT_INVENTORY_POLL_MAX_NODES", "50")
	t.Setenv("CONTROL_ACCOUNT_INVENTORY_POLL_CONCURRENCY", "9")
	if _, err = loadAccountInventoryPollRuntimeConfig(); err == nil {
		t.Fatal("unsafe fifty-node capacity was accepted")
	}
}

func TestAccountInventoryPollMetricsProviderBuildsPinnedProviderAllowlist(t *testing.T) {
	first, second := uuid.New(), uuid.New()
	truth := true
	provider := accountInventoryPollMetricsProvider{store: fakeAccountInventoryPollMetricsStore{
		runs: []controlstore.PollRunMetric{
			{InstanceID: first, Status: controlstore.PollRunFinalized, SchedulerLag: time.Second, TransportSuccess: &truth},
			{InstanceID: second, Status: controlstore.PollRunPending, QueueWait: 2 * time.Second},
		},
		providers: []controlstore.PollProviderMetric{
			{InstanceID: first, Provider: "zeta", SnapshotComplete: false},
			{InstanceID: first, Provider: "alpha", SnapshotComplete: true},
		},
	}}
	snapshot, err := provider.AccountInventoryPollMetricsSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.AllowedProviders, []string{"alpha", "zeta"}) || len(snapshot.Instances) != 2 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if snapshot.Instances[0].State != controlpollobs.StateFinalized || len(snapshot.Instances[0].Providers) != 2 ||
		snapshot.Instances[0].Providers[0].Provider != "alpha" {
		t.Fatalf("unexpected instance projection: %+v", snapshot.Instances[0])
	}
	if snapshot.Instances[1].State != controlpollobs.StatePending || snapshot.Instances[1].PollStartLagSeconds != nil {
		t.Fatalf("pending poll exported a synthetic start lag: %+v", snapshot.Instances[1])
	}
}

func TestAccountInventoryPollMetricsProviderFailsClosed(t *testing.T) {
	orphan := uuid.New()
	provider := accountInventoryPollMetricsProvider{store: fakeAccountInventoryPollMetricsStore{
		providers: []controlstore.PollProviderMetric{{InstanceID: orphan, Provider: "secret-canary"}},
	}}
	if _, err := provider.AccountInventoryPollMetricsSnapshot(context.Background()); err == nil ||
		err.Error() != "account inventory poll metrics unavailable" {
		t.Fatalf("orphan provider did not fail closed: %v", err)
	}
	provider.store = fakeAccountInventoryPollMetricsStore{err: errors.New("raw-secret-canary")}
	_, err := provider.AccountInventoryPollMetricsSnapshot(context.Background())
	if err == nil || err.Error() != "account inventory poll metrics unavailable" {
		t.Fatalf("raw error was projected: %v", err)
	}
}
