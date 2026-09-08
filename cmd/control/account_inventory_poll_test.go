package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	controlpoll "github.com/sunxu/relay-station-control/internal/inventorypoll"
	controlpollobs "github.com/sunxu/relay-station-control/internal/pollobservability"
	controlstore "github.com/sunxu/relay-station-control/internal/store"
)

func TestAccountInventoryPollLogObserverEmitsCapacityExceeded(t *testing.T) {
	var output bytes.Buffer
	adapter := accountInventoryPollLogObserver{
		observer: controlpollobs.NewObserver(slog.New(slog.NewJSONHandler(&output, nil))),
	}
	adapter.Observe(context.Background(), controlpoll.Event{
		Component: controlpoll.EventComponentScheduler,
		Action:    controlpoll.EventActionSchedule,
		Result:    controlpoll.EventResultFailure,
		Reason:    controlpoll.ControlReasonCapacityExceeded,
	})
	var fields map[string]any
	if err := json.Unmarshal(output.Bytes(), &fields); err != nil {
		t.Fatalf("adapter emitted invalid log: %v; output=%q", err, output.String())
	}
	if fields["reason"] != string(controlpoll.ControlReasonCapacityExceeded) {
		t.Fatalf("adapter reason = %v, want %q", fields["reason"], controlpoll.ControlReasonCapacityExceeded)
	}
}

type fakeAccountInventoryPollMetricsStore struct {
	runs       []controlstore.PollRunMetric
	providers  []controlstore.PollProviderMetric
	lifecycles []controlstore.AccountInventoryLifecycleMetric
	err        error
}

func (store fakeAccountInventoryPollMetricsStore) MetricsWithLifecycle(_ context.Context, includeLifecycle bool) (
	[]controlstore.PollRunMetric, []controlstore.PollProviderMetric,
	[]controlstore.AccountInventoryLifecycleMetric, error,
) {
	if !includeLifecycle {
		return store.runs, store.providers, nil, store.err
	}
	return store.runs, store.providers, store.lifecycles, store.err
}

func TestLoadAccountInventoryPollRuntimeConfigDefaultsAndBounds(t *testing.T) {
	for _, name := range []string{
		"CONTROL_ACCOUNT_INVENTORY_POLL_ENABLED", "CONTROL_ACCOUNT_INVENTORY_LIFECYCLE_ENABLED",
		"CONTROL_ACCOUNT_INVENTORY_POLL_MAX_NODES",
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
	if configuration.lifecycleEnabled {
		t.Fatal("lifecycle writes must require explicit enablement")
	}
	validated, err := configuration.poll.Validate()
	if err != nil || validated.Concurrency() != 10 || validated.PollStartGrace() != 120*time.Second ||
		validated.LeaseDuration() != 30*time.Second || validated.MaxAttempts() != 2 || validated.EffectiveCapacity() != 20 {
		t.Fatalf("unexpected defaults: config=%+v err=%v", configuration.poll, err)
	}

	t.Setenv("CONTROL_ACCOUNT_INVENTORY_POLL_MAX_NODES", "50")
	t.Setenv("CONTROL_ACCOUNT_INVENTORY_POLL_CONCURRENCY", "9")
	if configuration, err = loadAccountInventoryPollRuntimeConfig(); err != nil {
		t.Fatalf("derived capacity with legacy max-node variable rejected: %+v/%v", configuration, err)
	}
	validated, err = configuration.poll.Validate()
	if err != nil || validated.EffectiveCapacity() != 18 {
		t.Fatalf("unexpected derived legacy capacity: config=%+v err=%v", configuration.poll, err)
	}
	t.Setenv("CONTROL_ACCOUNT_INVENTORY_POLL_MAX_NODES", "1")
	t.Setenv("CONTROL_ACCOUNT_INVENTORY_POLL_CONCURRENCY", "1")
	t.Setenv("CONTROL_ACCOUNT_INVENTORY_POLL_ENABLED", "true")
	if _, err = loadAccountInventoryPollRuntimeConfig(); err == nil {
		t.Fatal("polling without lifecycle-aware finalize was accepted")
	}
	t.Setenv("CONTROL_ACCOUNT_INVENTORY_LIFECYCLE_ENABLED", "true")
	if configuration, err = loadAccountInventoryPollRuntimeConfig(); err != nil || !configuration.enabled || !configuration.lifecycleEnabled {
		t.Fatalf("explicit lifecycle-aware polling rejected: %+v/%v", configuration, err)
	}
}

func TestAccountInventoryPollMetricsProviderBuildsPinnedProviderAllowlist(t *testing.T) {
	first, second := uuid.New(), uuid.New()
	truth := true
	provider := accountInventoryPollMetricsProvider{lifecycleEnabled: true, store: fakeAccountInventoryPollMetricsStore{
		runs: []controlstore.PollRunMetric{
			{InstanceID: first, Status: controlstore.PollRunFinalized, SchedulerLag: time.Second, TransportSuccess: &truth},
			{InstanceID: second, Status: controlstore.PollRunPending, QueueWait: 2 * time.Second},
		},
		providers: []controlstore.PollProviderMetric{
			{
				InstanceID: first, Provider: "zeta", SnapshotComplete: false,
				PromotionEvaluated: true, PromotionSkippedReason: "provider_identity_incomplete",
			},
			{
				InstanceID: first, Provider: "alpha", SnapshotComplete: true,
				PromotionEvaluated: true, PromotionApplied: true,
			},
		},
		lifecycles: []controlstore.AccountInventoryLifecycleMetric{
			{InstanceID: first, Provider: "zeta", Lifecycle: controlstore.AccountInventoryMissing, Count: 2},
			{InstanceID: first, Provider: "archived", Lifecycle: controlstore.AccountInventoryOutOfScope, Count: 1},
		},
	}}
	snapshot, err := provider.AccountInventoryPollMetricsSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.AllowedProviders, []string{"alpha", "archived", "zeta"}) ||
		len(snapshot.Instances) != 2 || len(snapshot.Lifecycles) != 2 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if snapshot.Instances[0].State != controlpollobs.StateFinalized || len(snapshot.Instances[0].Providers) != 2 ||
		snapshot.Instances[0].Providers[0].Provider != "alpha" ||
		!snapshot.Instances[0].Providers[0].PromotionEvaluated ||
		!snapshot.Instances[0].Providers[0].PromotionApplied ||
		snapshot.Instances[0].Providers[1].PromotionSkippedReason != controlpollobs.PromotionSkippedProviderIdentityIncomplete {
		t.Fatalf("unexpected instance projection: %+v", snapshot.Instances[0])
	}
	if snapshot.Instances[1].State != controlpollobs.StatePending || snapshot.Instances[1].PollStartLagSeconds != nil {
		t.Fatalf("pending poll exported a synthetic start lag: %+v", snapshot.Instances[1])
	}
}

func TestAccountInventoryPollMetricsProviderOmitsLifecycleWhenDisabled(t *testing.T) {
	instanceID := uuid.New()
	provider := accountInventoryPollMetricsProvider{store: fakeAccountInventoryPollMetricsStore{
		runs: []controlstore.PollRunMetric{{InstanceID: instanceID, Status: controlstore.PollRunFinalized}},
		lifecycles: []controlstore.AccountInventoryLifecycleMetric{{
			InstanceID: instanceID, Provider: "openai", Lifecycle: controlstore.AccountInventoryPresent, Count: 1,
		}},
	}}
	snapshot, err := provider.AccountInventoryPollMetricsSnapshot(context.Background())
	if err != nil || len(snapshot.Lifecycles) != 0 {
		t.Fatalf("disabled lifecycle metrics = %+v/%v", snapshot, err)
	}
}

func TestAccountInventoryPollMetricsProviderKeepsLifecycleAfterPollHistoryCleanup(t *testing.T) {
	instanceID := uuid.New()
	provider := accountInventoryPollMetricsProvider{lifecycleEnabled: true, store: fakeAccountInventoryPollMetricsStore{
		lifecycles: []controlstore.AccountInventoryLifecycleMetric{{
			InstanceID: instanceID, Provider: "openai",
			Lifecycle: controlstore.AccountInventoryMissing, Count: 1001,
		}},
	}}
	snapshot, err := provider.AccountInventoryPollMetricsSnapshot(context.Background())
	if err != nil || len(snapshot.Instances) != 0 || len(snapshot.Lifecycles) != 1 ||
		snapshot.Lifecycles[0].InstanceID != instanceID || snapshot.Lifecycles[0].Count != 1001 ||
		!reflect.DeepEqual(snapshot.AllowedProviders, []string{"openai"}) {
		t.Fatalf("lifecycle-only metrics were lost: %+v/%v", snapshot, err)
	}
}

func TestAccountInventoryPollMetricsProviderPreservesLegacyPromotionState(t *testing.T) {
	instanceID := uuid.New()
	provider := accountInventoryPollMetricsProvider{store: fakeAccountInventoryPollMetricsStore{
		runs: []controlstore.PollRunMetric{{InstanceID: instanceID, Status: controlstore.PollRunFinalized}},
		providers: []controlstore.PollProviderMetric{{
			InstanceID: instanceID, Provider: "openai", SnapshotComplete: true,
		}},
	}}
	snapshot, err := provider.AccountInventoryPollMetricsSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	metric := snapshot.Instances[0].Providers[0]
	if metric.PromotionEvaluated || metric.PromotionApplied || metric.PromotionSkippedReason != "" {
		t.Fatalf("legacy promotion state was fabricated: %+v", metric)
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
