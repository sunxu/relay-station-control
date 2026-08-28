package historyruntime

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	metricsTimeout                      = 2 * time.Second
	MaximumCoverageInstances            = 50
	MaximumCoverageProvidersPerInstance = 64
)

type CompactionState string
type RollupState string
type FailedFrom string
type DeleteResult string

const (
	CompactionPending    CompactionState = "pending"
	CompactionSummarized CompactionState = "summarized"
	CompactionDeleting   CompactionState = "deleting"
	CompactionCompleted  CompactionState = "completed"
	CompactionFailed     CompactionState = "failed"

	RollupPending   RollupState = "pending"
	RollupCompleted RollupState = "completed"
	RollupFailed    RollupState = "failed"

	FailedFromPending    FailedFrom = "pending"
	FailedFromSummarized FailedFrom = "summarized"
	FailedFromDeleting   FailedFrom = "deleting"

	DeleteSuccess DeleteResult = "success"
	DeleteFailure DeleteResult = "failure"
)

var (
	AllCompactionStates = [...]CompactionState{
		CompactionPending, CompactionSummarized, CompactionDeleting, CompactionCompleted, CompactionFailed,
	}
	AllRollupStates  = [...]RollupState{RollupPending, RollupCompleted, RollupFailed}
	AllFailedFrom    = [...]FailedFrom{FailedFromPending, FailedFromSummarized, FailedFromDeleting}
	AllDeleteResults = [...]DeleteResult{DeleteSuccess, DeleteFailure}
)

func (state CompactionState) Valid() bool {
	switch state {
	case CompactionPending, CompactionSummarized, CompactionDeleting, CompactionCompleted, CompactionFailed:
		return true
	default:
		return false
	}
}

func (state RollupState) Valid() bool {
	switch state {
	case RollupPending, RollupCompleted, RollupFailed:
		return true
	default:
		return false
	}
}

func (phase FailedFrom) Valid() bool {
	switch phase {
	case FailedFromPending, FailedFromSummarized, FailedFromDeleting:
		return true
	default:
		return false
	}
}

func (result DeleteResult) Valid() bool {
	return result == DeleteSuccess || result == DeleteFailure
}

// MetricsSnapshot contains aggregates only. It intentionally has no date,
// policy, poll/run/fencing identity, checksum, account key, or raw error field.
type MetricsSnapshot struct {
	Runtime                         RuntimeStatus
	CompactionRuns                  map[CompactionState]int64
	RollupRuns                      map[RollupState]int64
	OldestEligibleUnfinishedSeconds float64
	Failures                        map[FailedFrom]int64
	DeleteBacklogRows               int64
	DeleteRows                      map[DeleteResult]uint64
	DeleteDurationSeconds           map[DeleteResult]float64
	ProviderCoverage                []ProviderCoverage
}

type ProviderCoverage struct {
	InstanceID uuid.UUID
	Provider   string
	Ratio      float64
	Complete   bool
}

type MetricsProvider interface {
	AccountInventoryHistoryMetricsSnapshot(context.Context) (MetricsSnapshot, error)
}

// RuntimeStatusState bridges the service lifecycle observer to a database
// metrics provider without logging either compatibility errors or identities.
type RuntimeStatusState struct {
	mu     sync.RWMutex
	status RuntimeStatus
}

func NewRuntimeStatusState() *RuntimeStatusState {
	return &RuntimeStatusState{status: RuntimeStatus{Reason: ReasonDisabled}}
}

func (state *RuntimeStatusState) ObserveRuntimeStatus(status RuntimeStatus) {
	if state == nil || !status.valid() {
		return
	}
	state.mu.Lock()
	state.status = status
	state.mu.Unlock()
}

func (state *RuntimeStatusState) RuntimeStatus() RuntimeStatus {
	if state == nil {
		return RuntimeStatus{Reason: ReasonDisabled}
	}
	state.mu.RLock()
	defer state.mu.RUnlock()
	return state.status
}

func (status RuntimeStatus) valid() bool {
	if !status.Reason.Valid() {
		return false
	}
	switch status.Reason {
	case ReasonDisabled:
		return !status.Configured && !status.Enabled
	case ReasonSchemaIncompatible:
		return !status.Enabled && !status.Compatible
	case ReasonReady:
		return status.Configured && status.Enabled && status.Compatible
	case ReasonRuntimeStopped:
		return status.Configured && !status.Enabled && status.Compatible
	default:
		return false
	}
}

type Collector struct {
	provider       MetricsProvider
	enabled        *prometheus.Desc
	compactionRuns *prometheus.Desc
	rollupRuns     *prometheus.Desc
	oldest         *prometheus.Desc
	failures       *prometheus.Desc
	deleteBacklog  *prometheus.Desc
	deleteRows     *prometheus.Desc
	deleteDuration *prometheus.Desc
	coverageRatio  *prometheus.Desc
	coverageState  *prometheus.Desc
}

func NewCollector(provider MetricsProvider) (*Collector, error) {
	if provider == nil {
		return nil, fmt.Errorf("account inventory history metrics provider is nil")
	}
	return &Collector{
		provider: provider,
		enabled: prometheus.NewDesc(
			"relay_control_account_inventory_history_enabled",
			"Whether the account-inventory history runner passed its compatibility gate and is enabled.",
			[]string{"reason"}, nil,
		),
		compactionRuns: prometheus.NewDesc(
			"relay_control_account_inventory_history_compaction_runs",
			"Account-inventory history compaction runs by closed persisted state.",
			[]string{"state"}, nil,
		),
		rollupRuns: prometheus.NewDesc(
			"relay_control_account_inventory_history_rollup_runs",
			"Account-inventory daily rollup runs by closed persisted state.",
			[]string{"state"}, nil,
		),
		oldest: prometheus.NewDesc(
			"relay_control_account_inventory_history_oldest_eligible_unfinished_seconds",
			"Age of the oldest eligible unfinished history key.", nil, nil,
		),
		failures: prometheus.NewDesc(
			"relay_control_account_inventory_history_compaction_failures",
			"Failed compaction runs by the closed phase from which they can recover.",
			[]string{"failed_from"}, nil,
		),
		deleteBacklog: prometheus.NewDesc(
			"relay_control_account_inventory_history_delete_backlog_rows",
			"Persisted source rows awaiting controlled history deletion.", nil, nil,
		),
		deleteRows: prometheus.NewDesc(
			"relay_control_account_inventory_history_delete_rows_total",
			"Rows processed by controlled history deletion, by closed result.",
			[]string{"result"}, nil,
		),
		deleteDuration: prometheus.NewDesc(
			"relay_control_account_inventory_history_delete_duration_seconds_total",
			"Cumulative controlled history deletion transaction duration, by closed result.",
			[]string{"result"}, nil,
		),
		coverageRatio: prometheus.NewDesc(
			"relay_control_account_inventory_history_provider_coverage_ratio",
			"Most recent retained completed daily Provider coverage ratio.",
			[]string{"instance_id", "provider"}, nil,
		),
		coverageState: prometheus.NewDesc(
			"relay_control_account_inventory_history_provider_coverage_complete",
			"Whether the most recent retained completed daily Provider coverage meets the fixed threshold.",
			[]string{"instance_id", "provider"}, nil,
		),
	}, nil
}

func (collector *Collector) Describe(channel chan<- *prometheus.Desc) {
	channel <- collector.enabled
	channel <- collector.compactionRuns
	channel <- collector.rollupRuns
	channel <- collector.oldest
	channel <- collector.failures
	channel <- collector.deleteBacklog
	channel <- collector.deleteRows
	channel <- collector.deleteDuration
	channel <- collector.coverageRatio
	channel <- collector.coverageState
}

func (collector *Collector) Collect(channel chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), metricsTimeout)
	defer cancel()
	snapshot, err := collector.provider.AccountInventoryHistoryMetricsSnapshot(ctx)
	if err != nil {
		// A history-local metrics read failure must not poison the process-wide
		// Prometheus registry.  Preserve the independently observed runtime truth
		// and omit all database-derived families; never project the raw error.
		if snapshot.Runtime.valid() {
			enabled := 0.0
			if snapshot.Runtime.Enabled {
				enabled = 1
			}
			channel <- prometheus.MustNewConstMetric(
				collector.enabled, prometheus.GaugeValue, enabled, string(snapshot.Runtime.Reason),
			)
			return
		}
		channel <- prometheus.NewInvalidMetric(collector.enabled, fmt.Errorf("account inventory history metrics unavailable"))
		return
	}
	if !snapshot.valid() {
		channel <- prometheus.NewInvalidMetric(collector.enabled, fmt.Errorf("account inventory history metrics unavailable"))
		return
	}
	enabled := 0.0
	if snapshot.Runtime.Enabled {
		enabled = 1
	}
	channel <- prometheus.MustNewConstMetric(collector.enabled, prometheus.GaugeValue, enabled, string(snapshot.Runtime.Reason))
	if !snapshot.Runtime.Compatible {
		return
	}
	for _, state := range AllCompactionStates {
		channel <- prometheus.MustNewConstMetric(
			collector.compactionRuns, prometheus.GaugeValue, float64(snapshot.CompactionRuns[state]), string(state),
		)
	}
	for _, state := range AllRollupStates {
		channel <- prometheus.MustNewConstMetric(
			collector.rollupRuns, prometheus.GaugeValue, float64(snapshot.RollupRuns[state]), string(state),
		)
	}
	channel <- prometheus.MustNewConstMetric(
		collector.oldest, prometheus.GaugeValue, snapshot.OldestEligibleUnfinishedSeconds,
	)
	for _, phase := range AllFailedFrom {
		channel <- prometheus.MustNewConstMetric(
			collector.failures, prometheus.GaugeValue, float64(snapshot.Failures[phase]), string(phase),
		)
	}
	channel <- prometheus.MustNewConstMetric(collector.deleteBacklog, prometheus.GaugeValue, float64(snapshot.DeleteBacklogRows))
	for _, result := range AllDeleteResults {
		channel <- prometheus.MustNewConstMetric(
			collector.deleteRows, prometheus.CounterValue, float64(snapshot.DeleteRows[result]), string(result),
		)
		channel <- prometheus.MustNewConstMetric(
			collector.deleteDuration, prometheus.CounterValue, snapshot.DeleteDurationSeconds[result], string(result),
		)
	}
	for _, coverage := range snapshot.ProviderCoverage {
		complete := 0.0
		if coverage.Complete {
			complete = 1
		}
		labels := []string{coverage.InstanceID.String(), coverage.Provider}
		channel <- prometheus.MustNewConstMetric(
			collector.coverageRatio, prometheus.GaugeValue, coverage.Ratio, labels...,
		)
		channel <- prometheus.MustNewConstMetric(
			collector.coverageState, prometheus.GaugeValue, complete, labels...,
		)
	}
}

func (snapshot MetricsSnapshot) valid() bool {
	if !snapshot.Runtime.valid() {
		return false
	}
	if !snapshot.Runtime.Compatible {
		return len(snapshot.CompactionRuns) == 0 && len(snapshot.RollupRuns) == 0 &&
			snapshot.OldestEligibleUnfinishedSeconds == 0 && len(snapshot.Failures) == 0 &&
			snapshot.DeleteBacklogRows == 0 && len(snapshot.DeleteRows) == 0 &&
			len(snapshot.DeleteDurationSeconds) == 0 && len(snapshot.ProviderCoverage) == 0
	}
	if math.IsNaN(snapshot.OldestEligibleUnfinishedSeconds) ||
		math.IsInf(snapshot.OldestEligibleUnfinishedSeconds, 0) ||
		snapshot.OldestEligibleUnfinishedSeconds < 0 || snapshot.DeleteBacklogRows < 0 {
		return false
	}
	for state, count := range snapshot.CompactionRuns {
		if !state.Valid() || count < 0 {
			return false
		}
	}
	for state, count := range snapshot.RollupRuns {
		if !state.Valid() || count < 0 {
			return false
		}
	}
	for phase, count := range snapshot.Failures {
		if !phase.Valid() || count < 0 {
			return false
		}
	}
	for result := range snapshot.DeleteRows {
		if !result.Valid() {
			return false
		}
	}
	for result, seconds := range snapshot.DeleteDurationSeconds {
		if !result.Valid() || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 {
			return false
		}
	}
	instances := make(map[uuid.UUID]int)
	seenCoverage := make(map[string]struct{}, len(snapshot.ProviderCoverage))
	for _, coverage := range snapshot.ProviderCoverage {
		if coverage.InstanceID == uuid.Nil || !validCoverageProvider(coverage.Provider) ||
			math.IsNaN(coverage.Ratio) || math.IsInf(coverage.Ratio, 0) ||
			coverage.Ratio < 0 || coverage.Ratio > 1 || coverage.Complete != (coverage.Ratio >= 0.95) {
			return false
		}
		key := coverage.InstanceID.String() + "\x00" + coverage.Provider
		if _, duplicate := seenCoverage[key]; duplicate {
			return false
		}
		seenCoverage[key] = struct{}{}
		instances[coverage.InstanceID]++
		if instances[coverage.InstanceID] > MaximumCoverageProvidersPerInstance ||
			len(instances) > MaximumCoverageInstances {
			return false
		}
	}
	return true
}

func validCoverageProvider(provider string) bool {
	if len(provider) < 1 || len(provider) > 64 || !lowerAlphaNumeric(provider[0]) {
		return false
	}
	for index := 1; index < len(provider); index++ {
		character := provider[index]
		if !lowerAlphaNumeric(character) && character != '.' && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func lowerAlphaNumeric(character byte) bool {
	return character >= 'a' && character <= 'z' || character >= '0' && character <= '9'
}
