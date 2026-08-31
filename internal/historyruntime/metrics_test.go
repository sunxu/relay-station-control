package historyruntime

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type staticMetricsProvider struct {
	snapshot MetricsSnapshot
	err      error
}

func (provider staticMetricsProvider) AccountInventoryHistoryMetricsSnapshot(context.Context) (MetricsSnapshot, error) {
	return provider.snapshot, provider.err
}

func TestCollectorExportsOnlyClosedLowCardinalityLabels(t *testing.T) {
	collector, err := NewCollector(staticMetricsProvider{snapshot: MetricsSnapshot{
		Runtime:                         RuntimeStatus{Configured: true, Enabled: true, Compatible: true, Reason: ReasonReady},
		CompactionRuns:                  map[CompactionState]int64{CompactionDeleting: 2},
		RollupRuns:                      map[RollupState]int64{RollupCompleted: 3},
		OldestEligibleUnfinishedSeconds: 61,
		Failures:                        map[FailedFrom]int64{FailedFromSummarized: 4},
		DeleteBacklogRows:               5,
		DeleteRows:                      map[DeleteResult]uint64{DeleteSuccess: 6},
		DeleteDurationSeconds:           map[DeleteResult]float64{DeleteSuccess: 0.7},
		ProviderCoverage: []ProviderCoverage{{
			InstanceID: uuid.MustParse("00000000-0000-4000-8000-000000000001"),
			Provider:   "openai", Ratio: 0.95, Complete: true,
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(collector)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if len(families) != 10 {
		t.Fatalf("metric families=%d", len(families))
	}
	allowedLabels := map[string]bool{
		"reason": true, "state": true, "failed_from": true, "result": true,
		"instance_id": true, "provider": true,
	}
	for _, family := range families {
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if !allowedLabels[label.GetName()] {
					t.Fatalf("unbounded label %q in %q", label.GetName(), family.GetName())
				}
				for _, forbidden := range []string{"date", "policy", "run", "fencing", "checksum", "account", "email"} {
					if strings.Contains(label.GetName(), forbidden) {
						t.Fatalf("identity label %q", label.GetName())
					}
				}
			}
		}
	}
}

func TestCollectorOmitsEmptyCoverage(t *testing.T) {
	collector, err := NewCollector(staticMetricsProvider{snapshot: MetricsSnapshot{
		Runtime: RuntimeStatus{Configured: true, Enabled: true, Compatible: true, Reason: ReasonReady},
	}})
	if err != nil {
		t.Fatal(err)
	}
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(collector)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == "relay_control_account_inventory_history_provider_coverage_ratio" ||
			family.GetName() == "relay_control_account_inventory_history_provider_coverage_complete" {
			t.Fatalf("coverage family emitted for an empty snapshot: %s", family.GetName())
		}
	}
}

func TestCollectorSchemaIncompatibleOmitsDatabaseDerivedFamilies(t *testing.T) {
	collector, err := NewCollector(staticMetricsProvider{snapshot: MetricsSnapshot{
		Runtime: RuntimeStatus{Configured: true, Reason: ReasonSchemaIncompatible},
	}})
	if err != nil {
		t.Fatal(err)
	}
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(collector)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if len(families) != 1 || families[0].GetName() != "relay_control_account_inventory_history_enabled" {
		t.Fatalf("incompatible metric families=%v", families)
	}
}

func TestCollectorFailsClosedOnUnknownLabelsOrRawProviderError(t *testing.T) {
	for _, provider := range []staticMetricsProvider{
		{snapshot: MetricsSnapshot{
			Runtime:        RuntimeStatus{Reason: ReasonDisabled},
			CompactionRuns: map[CompactionState]int64{"secret-canary": 1},
		}},
		{snapshot: MetricsSnapshot{}, err: errors.New("raw-secret-canary")},
	} {
		collector, err := NewCollector(provider)
		if err != nil {
			t.Fatal(err)
		}
		registry := prometheus.NewRegistry()
		registry.MustRegister(collector)
		if _, err = registry.Gather(); err == nil || strings.Contains(err.Error(), "raw-secret-canary") {
			t.Fatalf("unsafe metrics error=%v", err)
		}
	}
}

func TestCollectorProviderFailureDoesNotPoisonProcessMetrics(t *testing.T) {
	collector, err := NewCollector(staticMetricsProvider{
		snapshot: MetricsSnapshot{Runtime: RuntimeStatus{
			Configured: true, Enabled: true, Compatible: true, Reason: ReasonReady,
		}},
		err: errors.New("raw-secret-canary"),
	})
	if err != nil {
		t.Fatal(err)
	}
	registry := prometheus.NewRegistry()
	registry.MustRegister(collector)
	registry.MustRegister(prometheus.NewGauge(prometheus.GaugeOpts{Name: "unrelated_control_metric"}))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("metrics status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `relay_control_account_inventory_history_enabled{reason="ready"} 1`) ||
		!strings.Contains(body, "unrelated_control_metric 0") ||
		strings.Contains(body, "raw-secret-canary") ||
		strings.Contains(body, "relay_control_account_inventory_history_compaction_runs") {
		t.Fatalf("unsafe or incomplete metrics body=%q", body)
	}
}

func TestCollectorFailsClosedOnNonFiniteMetrics(t *testing.T) {
	ready := RuntimeStatus{Configured: true, Enabled: true, Compatible: true, Reason: ReasonReady}
	for _, snapshot := range []MetricsSnapshot{
		{Runtime: ready, OldestEligibleUnfinishedSeconds: math.NaN()},
		{Runtime: ready, OldestEligibleUnfinishedSeconds: math.Inf(1)},
		{Runtime: ready, DeleteDurationSeconds: map[DeleteResult]float64{DeleteSuccess: math.NaN()}},
		{Runtime: ready, DeleteDurationSeconds: map[DeleteResult]float64{DeleteFailure: math.Inf(1)}},
	} {
		collector, err := NewCollector(staticMetricsProvider{snapshot: snapshot})
		if err != nil {
			t.Fatal(err)
		}
		registry := prometheus.NewRegistry()
		registry.MustRegister(collector)
		if _, err = registry.Gather(); err == nil {
			t.Fatalf("non-finite snapshot accepted: %+v", snapshot)
		}
	}
}

func TestRuntimeStatusStateRejectsInvalidTransitions(t *testing.T) {
	state := NewRuntimeStatusState()
	if got := state.RuntimeStatus(); got.Reason != ReasonDisabled {
		t.Fatalf("initial status=%+v", got)
	}
	ready := RuntimeStatus{Configured: true, Enabled: true, Compatible: true, Reason: ReasonReady}
	state.ObserveRuntimeStatus(ready)
	if got := state.RuntimeStatus(); got != ready {
		t.Fatalf("ready status=%+v", got)
	}
	state.ObserveRuntimeStatus(RuntimeStatus{Enabled: true, Reason: ReasonDisabled})
	if got := state.RuntimeStatus(); got != ready {
		t.Fatalf("invalid status replaced truth: %+v", got)
	}
	disabled := RuntimeStatus{Compatible: true, Reason: ReasonDisabled}
	state.ObserveRuntimeStatus(disabled)
	if got := state.RuntimeStatus(); got != disabled {
		t.Fatalf("compatible disabled status=%+v", got)
	}
	incompatible := RuntimeStatus{Reason: ReasonSchemaIncompatible}
	state.ObserveRuntimeStatus(incompatible)
	if got := state.RuntimeStatus(); got != incompatible {
		t.Fatalf("disabled incompatible status=%+v", got)
	}
}

func TestCollectorRequiresProvider(t *testing.T) {
	if _, err := NewCollector(nil); err == nil {
		t.Fatal("nil metrics provider accepted")
	}
}
