package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	generated "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

// crossNodeDuplicateOwnershipMetricConflictType and
// crossNodeDuplicateOwnershipMetricSeverity are the two fixed,
// single-valued label values for this capability's only metric (design.md
// §6b/§6c: severity is always "Critical", conflict_type never varies).
const (
	crossNodeDuplicateOwnershipMetricConflictType = "cross_node_duplicate_ownership"
	crossNodeDuplicateOwnershipMetricSeverity     = "Critical"
)

// CrossNodeDuplicateOwnershipMetricsSnapshot is one (environment, status)
// occurrence count row (Phase 6c). Only environment_id/status/count are
// carried -- never account_key, email, occurrence_id, or instance_id
// (forbidden high-cardinality Prometheus labels).
type CrossNodeDuplicateOwnershipMetricsSnapshot struct {
	EnvironmentID   string
	Status          string
	OccurrenceCount int64
}

// CrossNodeDuplicateOwnershipMetricsProvider is queried once per Prometheus
// scrape (mirrors internal/jobs.MetricsProvider).
type CrossNodeDuplicateOwnershipMetricsProvider interface {
	CrossNodeDuplicateOwnershipMetricsSnapshot(ctx context.Context) ([]CrossNodeDuplicateOwnershipMetricsSnapshot, error)
}

// CrossNodeDuplicateOwnershipMetricsRepository implements
// CrossNodeDuplicateOwnershipMetricsProvider against the runtime pool. It
// only SELECTs from the Phase 1B occurrence table
// (relay_control_runtime already holds SELECT on it, see 00013 grants) --
// no new migration/grant.
type CrossNodeDuplicateOwnershipMetricsRepository struct {
	pool *pgxpool.Pool
}

// NewCrossNodeDuplicateOwnershipMetricsRepository constructs a repository
// bound to the given pool (production callers must pass the runtime pool).
func NewCrossNodeDuplicateOwnershipMetricsRepository(pool *pgxpool.Pool) (*CrossNodeDuplicateOwnershipMetricsRepository, error) {
	if pool == nil {
		return nil, fmt.Errorf("store: cross-node duplicate ownership metrics database is unavailable")
	}
	return &CrossNodeDuplicateOwnershipMetricsRepository{pool: pool}, nil
}

func (repository *CrossNodeDuplicateOwnershipMetricsRepository) CrossNodeDuplicateOwnershipMetricsSnapshot(
	ctx context.Context,
) ([]CrossNodeDuplicateOwnershipMetricsSnapshot, error) {
	rows, err := generated.New(repository.pool).CountCrossNodeDuplicateOccurrencesByEnvironmentAndStatus(ctx)
	if err != nil {
		return nil, err
	}
	snapshot := make([]CrossNodeDuplicateOwnershipMetricsSnapshot, 0, len(rows))
	for _, row := range rows {
		snapshot = append(snapshot, CrossNodeDuplicateOwnershipMetricsSnapshot{
			EnvironmentID: row.EnvironmentID, Status: row.Status, OccurrenceCount: row.OccurrenceCount,
		})
	}
	return snapshot, nil
}

// CrossNodeDuplicateOwnershipMetricsCollector is a Prometheus Collector
// exposing the single Phase 6c metric: current occurrence count by
// (environment, conflict_type, status, severity). It queries the provider
// once per scrape (mirrors internal/jobs.Collector), never caching stale
// counts between scrapes.
type CrossNodeDuplicateOwnershipMetricsCollector struct {
	provider CrossNodeDuplicateOwnershipMetricsProvider
	count    *prometheus.Desc
}

// NewCrossNodeDuplicateOwnershipMetricsCollector constructs a Collector
// bound to the given provider.
func NewCrossNodeDuplicateOwnershipMetricsCollector(provider CrossNodeDuplicateOwnershipMetricsProvider) (*CrossNodeDuplicateOwnershipMetricsCollector, error) {
	if provider == nil {
		return nil, fmt.Errorf("store: cross-node duplicate ownership metrics provider is nil")
	}
	return &CrossNodeDuplicateOwnershipMetricsCollector{
		provider: provider,
		count: prometheus.NewDesc(
			"relay_control_cross_node_duplicate_occurrences",
			"Current cross-node duplicate ownership occurrence count by closed status.",
			[]string{"environment", "conflict_type", "status", "severity"}, nil,
		),
	}, nil
}

func (collector *CrossNodeDuplicateOwnershipMetricsCollector) Describe(channel chan<- *prometheus.Desc) {
	channel <- collector.count
}

func (collector *CrossNodeDuplicateOwnershipMetricsCollector) Collect(channel chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	snapshot, err := collector.provider.CrossNodeDuplicateOwnershipMetricsSnapshot(ctx)
	if err != nil {
		channel <- prometheus.NewInvalidMetric(collector.count, fmt.Errorf("cross-node duplicate ownership metrics unavailable"))
		return
	}
	for _, row := range snapshot {
		if row.OccurrenceCount < 0 || (row.Status != "ACTIVE" && row.Status != "RESOLVED") || row.EnvironmentID == "" {
			channel <- prometheus.NewInvalidMetric(collector.count, fmt.Errorf("invalid cross-node duplicate ownership metrics snapshot"))
			return
		}
	}
	for _, row := range snapshot {
		channel <- prometheus.MustNewConstMetric(collector.count, prometheus.GaugeValue, float64(row.OccurrenceCount),
			row.EnvironmentID, crossNodeDuplicateOwnershipMetricConflictType, row.Status, crossNodeDuplicateOwnershipMetricSeverity)
	}
}
