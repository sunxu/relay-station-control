package store

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
)

type GatewayDirectoryMetricsSnapshot struct {
	RunStatusCounts    map[string]int64
	FailureClassCounts map[string]int64
	FreshnessCounts    map[string]int64
}

type GatewayDirectoryMetricsProvider interface {
	GatewayDirectoryMetricsSnapshot(context.Context) (GatewayDirectoryMetricsSnapshot, error)
}

type GatewayDirectoryMetrics struct {
	provider     GatewayDirectoryMetricsProvider
	runStatus    *prometheus.Desc
	failureClass *prometheus.Desc
	freshness    *prometheus.Desc
}

var gatewayDirectoryRunStatusLabels = [...]string{"pending", "running", "retry_wait", "succeeded", "failed"}

var gatewayDirectoryFailureClassLabels = [...]string{
	"transport",
	"timeout",
	"partial_read",
	"http_429",
	"http_5xx",
	"http_non_retryable",
	"contract_invalid",
	"gateway_retired",
	"gateway_replaced",
	"source_time_invalid",
	"hard_limit",
	"secret_unavailable",
	"finalize_transient",
	"lease_lost",
	"unknown_execution",
	"start_deadline_expired",
}

var gatewayDirectoryFreshnessLabels = [...]string{"fresh", "stale", "unknown"}

func NewGatewayDirectoryMetrics(provider GatewayDirectoryMetricsProvider) (*GatewayDirectoryMetrics, error) {
	if provider == nil {
		return nil, ErrInvalidGatewayDirectoryIngestionQuery
	}
	return &GatewayDirectoryMetrics{
		provider: provider,
		runStatus: prometheus.NewDesc(
			"relay_control_gateway_directory_runs",
			"Gateway Directory ingestion runs by frozen status.",
			[]string{"status"}, nil,
		),
		failureClass: prometheus.NewDesc(
			"relay_control_gateway_directory_failures",
			"Gateway Directory ingestion failures by frozen class.",
			[]string{"failure_class"}, nil,
		),
		freshness: prometheus.NewDesc(
			"relay_control_gateway_directory_freshness",
			"Gateway Directory freshness buckets by last successful observation.",
			[]string{"freshness"}, nil,
		),
	}, nil
}

func (metrics *GatewayDirectoryMetrics) Describe(channel chan<- *prometheus.Desc) {
	if metrics == nil {
		return
	}
	channel <- metrics.runStatus
	channel <- metrics.failureClass
	channel <- metrics.freshness
}

func (metrics *GatewayDirectoryMetrics) Collect(channel chan<- prometheus.Metric) {
	if metrics == nil || metrics.provider == nil {
		return
	}
	snapshot, err := metrics.provider.GatewayDirectoryMetricsSnapshot(context.Background())
	if err != nil {
		return
	}
	for _, status := range gatewayDirectoryRunStatusLabels {
		channel <- prometheus.MustNewConstMetric(metrics.runStatus, prometheus.GaugeValue, float64(snapshot.RunStatusCounts[status]), status)
	}
	for _, failureClass := range gatewayDirectoryFailureClassLabels {
		channel <- prometheus.MustNewConstMetric(metrics.failureClass, prometheus.GaugeValue, float64(snapshot.FailureClassCounts[failureClass]), failureClass)
	}
	for _, freshness := range gatewayDirectoryFreshnessLabels {
		channel <- prometheus.MustNewConstMetric(metrics.freshness, prometheus.GaugeValue, float64(snapshot.FreshnessCounts[freshness]), freshness)
	}
}

func (repository *GatewayDirectoryIngestionRepository) GatewayDirectoryMetricsSnapshot(
	ctx context.Context,
) (GatewayDirectoryMetricsSnapshot, error) {
	if repository == nil || repository.queries == nil {
		return GatewayDirectoryMetricsSnapshot{}, ErrInvalidGatewayDirectoryIngestionQuery
	}
	runStatuses, err := repository.queries.ListGatewayDirectoryRunStatusMetrics(ctx)
	if err != nil {
		return GatewayDirectoryMetricsSnapshot{}, err
	}
	failureClasses, err := repository.queries.ListGatewayDirectoryFailureClassMetrics(ctx)
	if err != nil {
		return GatewayDirectoryMetricsSnapshot{}, err
	}
	freshness, err := repository.queries.GetGatewayDirectoryFreshnessMetrics(ctx)
	if err != nil {
		return GatewayDirectoryMetricsSnapshot{}, err
	}
	snapshot := GatewayDirectoryMetricsSnapshot{
		RunStatusCounts:    make(map[string]int64, len(runStatuses)),
		FailureClassCounts: make(map[string]int64, len(failureClasses)),
		FreshnessCounts:    make(map[string]int64, 3),
	}
	for _, row := range runStatuses {
		snapshot.RunStatusCounts[row.Status] = row.Count
	}
	for _, row := range failureClasses {
		if row.LastFailureClass.Valid {
			snapshot.FailureClassCounts[row.LastFailureClass.String] = row.Count
		}
	}
	snapshot.FreshnessCounts["unknown"] = freshness.UnknownCount
	snapshot.FreshnessCounts["fresh"] = freshness.FreshCount
	snapshot.FreshnessCounts["stale"] = freshness.StaleCount
	return snapshot, nil
}
