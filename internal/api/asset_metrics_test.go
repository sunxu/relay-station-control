package api

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestAssetMetricsUseOnlyClosedLowCardinalityLabels(t *testing.T) {
	metrics := NewAssetMetrics()
	if err := metrics.SetCount(AssetKindNode, 3); err != nil {
		t.Fatalf("set node count: %v", err)
	}
	if err := metrics.RecordRead(AssetReadNodes, AssetReadResultSuccess); err != nil {
		t.Fatalf("record asset read: %v", err)
	}

	collector := NewAssetPrometheusCollector(metrics)
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(collector)
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather asset metrics: %v", err)
	}
	if len(families) != 2 {
		t.Fatalf("metric family count = %d, want 2", len(families))
	}
	allowed := map[string]bool{"asset_kind": true, "operation": true, "result": true}
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

func TestAssetMetricsRejectDerivedLabelsAndNegativeCounts(t *testing.T) {
	metrics := NewAssetMetrics()
	if err := metrics.SetCount(AssetKind("node-018f80d8"), 1); err == nil {
		t.Fatal("derived asset kind was accepted")
	}
	if err := metrics.SetCount(AssetKindNode, -1); err == nil {
		t.Fatal("negative asset count was accepted")
	}
	if err := metrics.RecordRead(AssetReadOperation("https://secret.example"), AssetReadResultSuccess); err == nil {
		t.Fatal("derived operation was accepted")
	}
	if err := metrics.RecordRead(AssetReadNodes, AssetReadResult("vault://reader-secret")); err == nil {
		t.Fatal("derived result was accepted")
	}
}
