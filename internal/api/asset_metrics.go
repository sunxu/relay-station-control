package api

import (
	"errors"
	"sort"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

type AssetKind string

const (
	AssetKindEnvironment    AssetKind = "environment"
	AssetKindGateway        AssetKind = "gateway"
	AssetKindNode           AssetKind = "node"
	AssetKindDriver         AssetKind = "driver"
	AssetKindProviderPolicy AssetKind = "provider_policy"
)

func (value AssetKind) valid() bool {
	switch value {
	case AssetKindEnvironment, AssetKindGateway, AssetKindNode, AssetKindDriver, AssetKindProviderPolicy:
		return true
	default:
		return false
	}
}

type AssetReadOperation string

const (
	AssetReadEnvironment           AssetReadOperation = "environment"
	AssetReadGateway               AssetReadOperation = "gateway"
	AssetReadNodes                 AssetReadOperation = "nodes"
	AssetReadNodeDetail            AssetReadOperation = "node_detail"
	AssetReadDrivers               AssetReadOperation = "drivers"
	AssetReadCurrentProviderPolicy AssetReadOperation = "current_provider_policy"
)

func (value AssetReadOperation) valid() bool {
	switch value {
	case AssetReadEnvironment, AssetReadGateway, AssetReadNodes, AssetReadNodeDetail, AssetReadDrivers, AssetReadCurrentProviderPolicy:
		return true
	default:
		return false
	}
}

type AssetReadResult string

const (
	AssetReadResultSuccess      AssetReadResult = "success"
	AssetReadResultEmpty        AssetReadResult = "empty"
	AssetReadResultNotFound     AssetReadResult = "not_found"
	AssetReadResultInvalid      AssetReadResult = "invalid_request"
	AssetReadResultUnauthorized AssetReadResult = "unauthorized"
	AssetReadResultUnavailable  AssetReadResult = "unavailable"
)

func (value AssetReadResult) valid() bool {
	switch value {
	case AssetReadResultSuccess, AssetReadResultEmpty, AssetReadResultNotFound, AssetReadResultInvalid, AssetReadResultUnauthorized, AssetReadResultUnavailable:
		return true
	default:
		return false
	}
}

type assetReadMetricKey struct {
	operation AssetReadOperation
	result    AssetReadResult
}

// AssetMetrics exposes only closed enum methods, so request and asset values can
// never become Prometheus labels.
type AssetMetrics struct {
	mu     sync.RWMutex
	counts map[AssetKind]float64
	reads  map[assetReadMetricKey]uint64
}

func NewAssetMetrics() *AssetMetrics {
	return &AssetMetrics{
		counts: map[AssetKind]float64{
			AssetKindEnvironment: 0, AssetKindGateway: 0, AssetKindNode: 0,
			AssetKindDriver: 0, AssetKindProviderPolicy: 0,
		},
		reads: make(map[assetReadMetricKey]uint64),
	}
}

func (metrics *AssetMetrics) SetCount(kind AssetKind, count int64) error {
	if !kind.valid() || count < 0 {
		return errors.New("api: invalid asset metric")
	}
	metrics.mu.Lock()
	metrics.counts[kind] = float64(count)
	metrics.mu.Unlock()
	return nil
}

func (metrics *AssetMetrics) RecordRead(operation AssetReadOperation, result AssetReadResult) error {
	if !operation.valid() || !result.valid() {
		return errors.New("api: invalid asset read metric")
	}
	metrics.mu.Lock()
	metrics.reads[assetReadMetricKey{operation: operation, result: result}]++
	metrics.mu.Unlock()
	return nil
}

type assetMetricSample struct {
	name      string
	labels    []string
	value     float64
	valueType prometheus.ValueType
}

func (metrics *AssetMetrics) snapshot() []assetMetricSample {
	metrics.mu.RLock()
	defer metrics.mu.RUnlock()
	samples := make([]assetMetricSample, 0, len(metrics.counts)+len(metrics.reads))
	for kind, count := range metrics.counts {
		samples = append(samples, assetMetricSample{name: "relay_control_assets", labels: []string{string(kind)}, value: count, valueType: prometheus.GaugeValue})
	}
	for key, count := range metrics.reads {
		samples = append(samples, assetMetricSample{name: "relay_control_asset_reads_total", labels: []string{string(key.operation), string(key.result)}, value: float64(count), valueType: prometheus.CounterValue})
	}
	sort.Slice(samples, func(i, j int) bool {
		if samples[i].name != samples[j].name {
			return samples[i].name < samples[j].name
		}
		return stringsJoin(samples[i].labels) < stringsJoin(samples[j].labels)
	})
	return samples
}

func stringsJoin(values []string) string {
	result := ""
	for _, value := range values {
		result += "\x00" + value
	}
	return result
}

type AssetPrometheusCollector struct {
	metrics   *AssetMetrics
	counts    *prometheus.Desc
	readTotal *prometheus.Desc
}

func NewAssetPrometheusCollector(metrics *AssetMetrics) *AssetPrometheusCollector {
	if metrics == nil {
		metrics = NewAssetMetrics()
	}
	return &AssetPrometheusCollector{
		metrics:   metrics,
		counts:    prometheus.NewDesc("relay_control_assets", "Registered Relay Station Control assets.", []string{"asset_kind"}, nil),
		readTotal: prometheus.NewDesc("relay_control_asset_reads_total", "Relay Station Control asset read results.", []string{"operation", "result"}, nil),
	}
}

func (collector *AssetPrometheusCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- collector.counts
	ch <- collector.readTotal
}

func (collector *AssetPrometheusCollector) Collect(ch chan<- prometheus.Metric) {
	for _, sample := range collector.metrics.snapshot() {
		desc := collector.readTotal
		if sample.name == "relay_control_assets" {
			desc = collector.counts
		}
		ch <- prometheus.MustNewConstMetric(desc, sample.valueType, sample.value, sample.labels...)
	}
}
