package pollobservability

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
)

type staticSnapshotProvider struct {
	snapshot Snapshot
	err      error
}

func (provider staticSnapshotProvider) AccountInventoryPollMetricsSnapshot(context.Context) (Snapshot, error) {
	return provider.snapshot, provider.err
}

func TestCollectorExportsOnlyPersistentAggregateAllowlist(t *testing.T) {
	instanceID := uuid.New()
	schedulerLag, queueWait, startLag := 17.0, 4.0, 9.0
	transport, contract := false, false
	provider := staticSnapshotProvider{snapshot: Snapshot{
		AllowedProviders: []string{"antigravity", "geminicli"},
		Instances: []InstanceSnapshot{{
			InstanceID: instanceID, State: StateFinalized,
			SchedulerLagSeconds: &schedulerLag, QueueWaitSeconds: &queueWait, PollStartLagSeconds: &startLag,
			TransportSuccess: &transport, ContractValid: &contract,
			Providers: []ProviderSnapshot{
				{Provider: "antigravity", SnapshotComplete: true, PromotionEvaluated: true, PromotionApplied: true},
				{Provider: "geminicli", SnapshotComplete: false, PromotionEvaluated: true, PromotionSkippedReason: PromotionSkippedPolicyChanged},
			},
		}},
		Lifecycles: []LifecycleSnapshot{{
			InstanceID: instanceID, Provider: "antigravity", Lifecycle: AccountLifecyclePresent, Count: 3,
		}},
	}}
	collector, err := NewCollector(provider, 50)
	if err != nil {
		t.Fatal(err)
	}
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(collector)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	wantLabels := map[string]map[string]struct{}{
		"relay_control_account_inventory_poll_run_state":             {"instance_id": {}, "state": {}},
		"relay_control_account_inventory_scheduler_lag_seconds":      {"instance_id": {}},
		"relay_control_account_inventory_queue_wait_seconds":         {"instance_id": {}},
		"relay_control_account_inventory_poll_start_lag_seconds":     {"instance_id": {}},
		"relay_control_account_inventory_transport_success":          {"instance_id": {}},
		"relay_control_account_inventory_contract_valid":             {"instance_id": {}},
		"relay_control_account_inventory_provider_snapshot_complete": {"instance_id": {}, "provider": {}},
		"relay_control_account_inventory_provider_promotion_applied": {"instance_id": {}, "provider": {}},
		"relay_control_account_inventory_provider_promotion_skipped": {"instance_id": {}, "provider": {}, "reason": {}},
		"relay_control_account_inventory_lifecycle_total":            {"instance_id": {}, "provider": {}, "lifecycle": {}},
	}
	if len(families) != len(wantLabels) {
		t.Fatalf("metric families = %d, want %d", len(families), len(wantLabels))
	}
	for _, family := range families {
		allowed, exists := wantLabels[family.GetName()]
		if !exists {
			t.Fatalf("metric outside allowlist: %s", family.GetName())
		}
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if _, ok := allowed[label.GetName()]; !ok {
					t.Fatalf("metric %s has forbidden label %s", family.GetName(), label.GetName())
				}
			}
		}
	}

	// Reconstructing the collector from the same persistent snapshot must retain
	// finalized-failure lag and false transport/contract evidence.
	restarted, err := NewCollector(provider, 50)
	if err != nil {
		t.Fatal(err)
	}
	restartedRegistry := prometheus.NewRegistry()
	restartedRegistry.MustRegister(restarted)
	restartedFamilies, err := restartedRegistry.Gather()
	if err != nil || len(restartedFamilies) != len(families) {
		t.Fatalf("restart gather = %d families, %v", len(restartedFamilies), err)
	}
}

func TestCollectorRejectsContradictoryOrUncontrolledPromotionEvidence(t *testing.T) {
	tests := []ProviderSnapshot{
		{Provider: "openai", PromotionApplied: true},
		{Provider: "openai", PromotionSkippedReason: PromotionSkippedPolicyChanged},
		{Provider: "openai", PromotionEvaluated: true, PromotionApplied: true, PromotionSkippedReason: PromotionSkippedPolicyChanged},
		{Provider: "openai", PromotionEvaluated: true, PromotionSkippedReason: PromotionSkippedReason("raw-error-canary")},
	}
	for _, provider := range tests {
		collector, err := NewCollector(staticSnapshotProvider{snapshot: Snapshot{
			AllowedProviders: []string{"openai"},
			Instances: []InstanceSnapshot{{
				InstanceID: uuid.New(), State: StateFinalized, Providers: []ProviderSnapshot{provider},
			}},
		}}, 50)
		if err != nil {
			t.Fatal(err)
		}
		registry := prometheus.NewRegistry()
		registry.MustRegister(collector)
		_, gatherErr := registry.Gather()
		if gatherErr == nil || strings.Contains(gatherErr.Error(), "raw-error-canary") {
			t.Fatalf("invalid promotion evidence was exported: %v", gatherErr)
		}
	}
}

func TestCollectorKeepsPreSnapshotFinalizedRowsObservableWithoutSyntheticPromotion(t *testing.T) {
	collector, err := NewCollector(staticSnapshotProvider{snapshot: Snapshot{
		AllowedProviders: []string{"openai"},
		Instances: []InstanceSnapshot{{
			InstanceID: uuid.New(), State: StateFinalized,
			Providers: []ProviderSnapshot{{Provider: "openai", SnapshotComplete: true}},
		}},
	}}, 50)
	if err != nil {
		t.Fatal(err)
	}
	registry := prometheus.NewRegistry()
	registry.MustRegister(collector)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == "relay_control_account_inventory_provider_promotion_applied" ||
			family.GetName() == "relay_control_account_inventory_provider_promotion_skipped" {
			t.Fatalf("pre-snapshot row emitted synthetic promotion metric %s", family.GetName())
		}
	}
}

func TestCollectorAllowsLifecycleOnlyInstanceAndChurnAboveSinglePollBound(t *testing.T) {
	instanceID := uuid.New()
	collector, err := NewCollector(staticSnapshotProvider{snapshot: Snapshot{
		AllowedProviders: []string{"openai"},
		Lifecycles: []LifecycleSnapshot{{
			InstanceID: instanceID, Provider: "openai", Lifecycle: AccountLifecycleMissing, Count: 1001,
		}},
	}}, 50)
	if err != nil {
		t.Fatal(err)
	}
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(collector)
	families, err := registry.Gather()
	if err != nil || len(families) != 1 || families[0].GetName() != "relay_control_account_inventory_lifecycle_total" ||
		len(families[0].Metric) != 1 || families[0].Metric[0].GetGauge().GetValue() != 1001 {
		t.Fatalf("lifecycle-only gather = %+v/%v", families, err)
	}
}

func TestCollectorRejectsUncontrolledDimensionsWithoutLeakingCanaries(t *testing.T) {
	canary := "endpoint-secret-email-body-header-error-canary"
	tests := []staticSnapshotProvider{
		{err: errors.New(canary)},
		{snapshot: Snapshot{AllowedProviders: []string{canary + "@example.invalid"}}},
		{snapshot: Snapshot{Instances: []InstanceSnapshot{{InstanceID: uuid.New(), State: State(canary)}}}},
		{snapshot: Snapshot{
			AllowedProviders: []string{"openai"},
			Instances:        []InstanceSnapshot{{InstanceID: uuid.New(), State: StateFinalized}},
			Lifecycles:       []LifecycleSnapshot{{InstanceID: uuid.Nil, Provider: "openai", Lifecycle: AccountLifecyclePresent}},
		}},
		{snapshot: Snapshot{
			AllowedProviders: []string{"openai"},
			Instances:        []InstanceSnapshot{{InstanceID: uuid.New(), State: StateFinalized}},
			Lifecycles:       []LifecycleSnapshot{{Provider: "openai", Lifecycle: AccountLifecycle(canary)}},
		}},
	}
	for _, provider := range tests {
		collector, err := NewCollector(provider, 50)
		if err != nil {
			t.Fatal(err)
		}
		registry := prometheus.NewRegistry()
		registry.MustRegister(collector)
		_, gatherErr := registry.Gather()
		if gatherErr == nil || strings.Contains(gatherErr.Error(), canary) {
			t.Fatalf("invalid snapshot error leaked canary: %v", gatherErr)
		}
	}

	formatted := []any{
		Snapshot{AllowedProviders: []string{canary}},
		InstanceSnapshot{State: State(canary)},
		ProviderSnapshot{Provider: canary},
		LifecycleSnapshot{Provider: canary, Lifecycle: AccountLifecycle(canary)},
	}
	for _, value := range formatted {
		if projected := strings.ToLower(formatAny(value)); strings.Contains(projected, strings.ToLower(canary)) {
			t.Fatalf("%T formatting leaked canary", value)
		}
	}
}

func TestCollectorEnforcesAssetCardinalityLimit(t *testing.T) {
	if _, err := NewCollector(staticSnapshotProvider{}, maximumInstances+1); err == nil {
		t.Fatal("collector accepted instance cardinality above the asset limit")
	}
	instances := make([]InstanceSnapshot, maximumInstances+1)
	for index := range instances {
		instances[index] = InstanceSnapshot{InstanceID: uuid.New(), State: StatePending}
	}
	collector, err := NewCollector(staticSnapshotProvider{snapshot: Snapshot{Instances: instances}}, maximumInstances)
	if err != nil {
		t.Fatal(err)
	}
	registry := prometheus.NewRegistry()
	registry.MustRegister(collector)
	if _, err := registry.Gather(); err == nil {
		t.Fatal("collector emitted a snapshot above the configured asset limit")
	}
	lifecycles := make([]LifecycleSnapshot, maximumInstances+1)
	for index := range lifecycles {
		lifecycles[index] = LifecycleSnapshot{
			InstanceID: uuid.New(), Provider: "openai", Lifecycle: AccountLifecyclePresent,
		}
	}
	lifecycleCollector, err := NewCollector(staticSnapshotProvider{snapshot: Snapshot{
		AllowedProviders: []string{"openai"}, Lifecycles: lifecycles,
	}}, maximumInstances)
	if err != nil {
		t.Fatal(err)
	}
	lifecycleRegistry := prometheus.NewRegistry()
	lifecycleRegistry.MustRegister(lifecycleCollector)
	if _, err := lifecycleRegistry.Gather(); err == nil {
		t.Fatal("collector emitted lifecycle-only instances above the asset limit")
	}
}

func formatAny(value any) string {
	return strings.TrimSpace(strings.Join([]string{
		strings.TrimSpace(toFormat(value, "%v")),
		strings.TrimSpace(toFormat(value, "%+v")),
		strings.TrimSpace(toFormat(value, "%#v")),
	}, " "))
}

func toFormat(value any, format string) string {
	return fmt.Sprintf(format, value)
}
