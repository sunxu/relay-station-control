package api

import (
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

func TestAssetMetricsUseOnlyClosedLowCardinalityLabels(t *testing.T) {
	metrics := NewAssetMetrics()
	if err := metrics.SetCount(AssetKindNode, 3); err != nil {
		t.Fatalf("set node count: %v", err)
	}
	if err := metrics.RecordRead(AssetReadNodes, AssetReadResultSuccess); err != nil {
		t.Fatalf("record asset read: %v", err)
	}
	metrics.RecordNodeMutation("replace", "success")

	collector := NewAssetPrometheusCollector(metrics)
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(collector)
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather asset metrics: %v", err)
	}
	if len(families) != 3 {
		t.Fatalf("metric family count = %d, want 3", len(families))
	}
	allowed := map[string]bool{"asset_kind": true, "asset_type": true, "action": true, "operation": true, "result": true}
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

func TestNodeMutationMetricResultUsesFixedTaxonomy(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{"invalid node", assetstore.ErrInvalidNode, "invalid"},
		{"invalid endpoint", assetstore.ErrInvalidNodeEndpoint, "invalid"},
		{"invalid secret", assetstore.ErrInvalidNodeSecret, "invalid"},
		{"command conflict", assetstore.ErrCommandConflict, "conflict"},
		{"retired", assetstore.ErrNodeRetired, "conflict"},
		{"identity", assetstore.ErrNodeIdentityExists, "conflict"},
		{"revision", assetstore.ErrStaleAssetRevision, "conflict"},
		{"revision exhausted", assetstore.ErrAssetRevisionExhausted, "conflict"},
		{"key unavailable", assetstore.ErrReceiptKeyUnavailable, "unavailable"},
		{"encoding unknown", assetstore.ErrReceiptEncodingUnknown, "unavailable"},
		{"database unavailable", errors.New("database unavailable"), "unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := nodeMutationMetricResult(test.err); got != test.want {
				t.Fatalf("result=%q, want %q", got, test.want)
			}
		})
	}
	metrics := NewAssetMetrics()
	metrics.RecordNodeMutation("edit", "success")
	metrics.RecordNodeMutation("edit", "replay")
	if got := len(metrics.snapshot()); got != 7 {
		t.Fatalf("snapshot samples=%d, want five gauges plus success/replay", got)
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
