package auth

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestPrometheusCollectorExportsOnlyClosedLabels(t *testing.T) {
	metrics := NewAuthMetrics()
	if err := metrics.RecordAttempt(EnvironmentProduction, MetricOperationLogin, MetricResultFailure); err != nil {
		t.Fatal(err)
	}
	if err := metrics.RecordRateLimit(EnvironmentProduction, RateLimitAccount); err != nil {
		t.Fatal(err)
	}
	if err := metrics.SetActiveSessions(EnvironmentProduction, 2); err != nil {
		t.Fatal(err)
	}
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(NewPrometheusCollector(metrics))
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if len(families) != 3 {
		t.Fatalf("metric family count=%d", len(families))
	}
	allowed := map[string]bool{"environment": true, "operation": true, "result": true, "dimension": true}
	for _, family := range families {
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if !allowed[label.GetName()] {
					t.Fatalf("unbounded label %q exported", label.GetName())
				}
			}
		}
	}
}
