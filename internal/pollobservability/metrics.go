// Package pollobservability defines the closed metrics and logging projection
// for account-inventory poll runs. It deliberately has no database or Driver
// dependency; the runtime supplies already-persisted aggregate snapshots.
package pollobservability

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	metricsTimeout       = 2 * time.Second
	maximumInstances     = 50
	maximumProviders     = 64
	maximumProviderBytes = 64
)

var providerLabelPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

type State string

// PromotionSkippedReason is the complete set of persisted reasons for a
// Provider observation not becoming the current snapshot. The empty value is
// reserved for an applied promotion and is never exported as a label.
type PromotionSkippedReason string

type AccountLifecycle string

const (
	StatePending   State = "pending"
	StateRunning   State = "running"
	StateRetryWait State = "retry_wait"
	StateFinalized State = "finalized"
	StateAbandoned State = "abandoned"

	PromotionSkippedPolicyChanged              PromotionSkippedReason = "policy_changed"
	PromotionSkippedTransportFailed            PromotionSkippedReason = "transport_failed"
	PromotionSkippedContractInvalid            PromotionSkippedReason = "contract_invalid"
	PromotionSkippedDiskFallback               PromotionSkippedReason = "disk_fallback"
	PromotionSkippedProviderIdentityIncomplete PromotionSkippedReason = "provider_identity_incomplete"
	PromotionSkippedProviderDuplicate          PromotionSkippedReason = "provider_duplicate"
	PromotionSkippedStalePoll                  PromotionSkippedReason = "stale_poll"

	AccountLifecyclePresent          AccountLifecycle = "present"
	AccountLifecycleSuspectedMissing AccountLifecycle = "suspected_missing"
	AccountLifecycleMissing          AccountLifecycle = "missing"
	AccountLifecycleOutOfScope       AccountLifecycle = "out_of_scope"
)

func (state State) Valid() bool {
	switch state {
	case StatePending, StateRunning, StateRetryWait, StateFinalized, StateAbandoned:
		return true
	default:
		return false
	}
}

func (lifecycle AccountLifecycle) Valid() bool {
	switch lifecycle {
	case AccountLifecyclePresent, AccountLifecycleSuspectedMissing, AccountLifecycleMissing, AccountLifecycleOutOfScope:
		return true
	default:
		return false
	}
}

func (reason PromotionSkippedReason) Valid() bool {
	switch reason {
	case PromotionSkippedPolicyChanged, PromotionSkippedTransportFailed, PromotionSkippedContractInvalid,
		PromotionSkippedDiskFallback, PromotionSkippedProviderIdentityIncomplete, PromotionSkippedProviderDuplicate,
		PromotionSkippedStalePoll:
		return true
	default:
		return false
	}
}

// ProviderSnapshot contains only the policy-controlled provider label and
// aggregate completeness/promotion evidence. It cannot carry an account
// identity or response. PromotionEvaluated distinguishes pre-Migration
// finalized rows from an evaluated skip.
type ProviderSnapshot struct {
	Provider               string
	SnapshotComplete       bool
	PromotionEvaluated     bool
	PromotionApplied       bool
	PromotionSkippedReason PromotionSkippedReason
}

func (ProviderSnapshot) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED PollProviderMetrics]"))
}

// InstanceSnapshot is reconstructed from persistent database times and
// aggregate evidence on every scrape. Nil optional values are not emitted.
type InstanceSnapshot struct {
	InstanceID          uuid.UUID
	State               State
	SchedulerLagSeconds *float64
	QueueWaitSeconds    *float64
	PollStartLagSeconds *float64
	TransportSuccess    *bool
	ContractValid       *bool
	Providers           []ProviderSnapshot
}

type LifecycleSnapshot struct {
	InstanceID uuid.UUID
	Provider   string
	Lifecycle  AccountLifecycle
	Count      uint64
}

func (LifecycleSnapshot) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED AccountInventoryLifecycleMetrics]"))
}

func (InstanceSnapshot) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED PollInstanceMetrics]"))
}

// Snapshot carries the union of provider names selected by pinned immutable
// policies. Provider metric rows must be members of this explicit allowlist.
type Snapshot struct {
	AllowedProviders []string
	Instances        []InstanceSnapshot
	Lifecycles       []LifecycleSnapshot
}

func (Snapshot) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED PollMetricsSnapshot]"))
}

type SnapshotProvider interface {
	AccountInventoryPollMetricsSnapshot(context.Context) (Snapshot, error)
}

type Collector struct {
	provider      SnapshotProvider
	maxInstances  int
	state         *prometheus.Desc
	schedulerLag  *prometheus.Desc
	queueWait     *prometheus.Desc
	startLag      *prometheus.Desc
	transport     *prometheus.Desc
	contract      *prometheus.Desc
	providerDone  *prometheus.Desc
	promotion     *prometheus.Desc
	promotionSkip *prometheus.Desc
	lifecycle     *prometheus.Desc
}

func NewCollector(provider SnapshotProvider, maxInstances int) (*Collector, error) {
	if provider == nil || maxInstances < 1 || maxInstances > maximumInstances {
		return nil, fmt.Errorf("account inventory poll metrics configuration is invalid")
	}
	return &Collector{
		provider:     provider,
		maxInstances: maxInstances,
		state: prometheus.NewDesc(
			"relay_control_account_inventory_poll_run_state",
			"Current persisted account-inventory poll-run state.",
			[]string{"instance_id", "state"}, nil,
		),
		schedulerLag: prometheus.NewDesc(
			"relay_control_account_inventory_scheduler_lag_seconds",
			"Lag between the latest due fixed slot and latest finalized slot.",
			[]string{"instance_id"}, nil,
		),
		queueWait: prometheus.NewDesc(
			"relay_control_account_inventory_queue_wait_seconds",
			"Persisted duration from poll-run creation to latest claim.",
			[]string{"instance_id"}, nil,
		),
		startLag: prometheus.NewDesc(
			"relay_control_account_inventory_poll_start_lag_seconds",
			"Persisted duration from fixed slot to first claim.",
			[]string{"instance_id"}, nil,
		),
		transport: prometheus.NewDesc(
			"relay_control_account_inventory_transport_success",
			"Whether the latest finalized poll produced transport evidence.",
			[]string{"instance_id"}, nil,
		),
		contract: prometheus.NewDesc(
			"relay_control_account_inventory_contract_valid",
			"Whether the latest finalized poll response satisfied the contract.",
			[]string{"instance_id"}, nil,
		),
		providerDone: prometheus.NewDesc(
			"relay_control_account_inventory_provider_snapshot_complete",
			"Whether the pinned-policy provider aggregate was complete.",
			[]string{"instance_id", "provider"}, nil,
		),
		promotion: prometheus.NewDesc(
			"relay_control_account_inventory_provider_promotion_applied",
			"Whether the latest finalized Provider observation became the current snapshot.",
			[]string{"instance_id", "provider"}, nil,
		),
		promotionSkip: prometheus.NewDesc(
			"relay_control_account_inventory_provider_promotion_skipped",
			"A fixed reason why the latest finalized Provider observation did not become the current snapshot.",
			[]string{"instance_id", "provider", "reason"}, nil,
		),
		lifecycle: prometheus.NewDesc(
			"relay_control_account_inventory_lifecycle_total",
			"Current persisted account-inventory rows by lifecycle.",
			[]string{"instance_id", "provider", "lifecycle"}, nil,
		),
	}, nil
}

func (collector *Collector) Describe(channel chan<- *prometheus.Desc) {
	channel <- collector.state
	channel <- collector.schedulerLag
	channel <- collector.queueWait
	channel <- collector.startLag
	channel <- collector.transport
	channel <- collector.contract
	channel <- collector.providerDone
	channel <- collector.promotion
	channel <- collector.promotionSkip
	channel <- collector.lifecycle
}

func (collector *Collector) Collect(channel chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), metricsTimeout)
	defer cancel()
	snapshot, err := collector.provider.AccountInventoryPollMetricsSnapshot(ctx)
	if err != nil || !snapshot.valid(collector.maxInstances) {
		channel <- prometheus.NewInvalidMetric(collector.state, fmt.Errorf("account inventory poll metrics unavailable"))
		return
	}
	for _, instance := range snapshot.Instances {
		instanceID := instance.InstanceID.String()
		channel <- prometheus.MustNewConstMetric(collector.state, prometheus.GaugeValue, 1, instanceID, string(instance.State))
		emitOptionalGauge(channel, collector.schedulerLag, instance.SchedulerLagSeconds, instanceID)
		emitOptionalGauge(channel, collector.queueWait, instance.QueueWaitSeconds, instanceID)
		emitOptionalGauge(channel, collector.startLag, instance.PollStartLagSeconds, instanceID)
		emitOptionalBool(channel, collector.transport, instance.TransportSuccess, instanceID)
		emitOptionalBool(channel, collector.contract, instance.ContractValid, instanceID)
		for _, provider := range instance.Providers {
			value := 0.0
			if provider.SnapshotComplete {
				value = 1
			}
			channel <- prometheus.MustNewConstMetric(collector.providerDone, prometheus.GaugeValue, value, instanceID, provider.Provider)
			if provider.PromotionEvaluated {
				promotionValue := 0.0
				if provider.PromotionApplied {
					promotionValue = 1
				}
				channel <- prometheus.MustNewConstMetric(collector.promotion, prometheus.GaugeValue, promotionValue, instanceID, provider.Provider)
			}
			if provider.PromotionEvaluated && !provider.PromotionApplied {
				channel <- prometheus.MustNewConstMetric(
					collector.promotionSkip, prometheus.GaugeValue, 1,
					instanceID, provider.Provider, string(provider.PromotionSkippedReason),
				)
			}
		}
	}
	for _, lifecycle := range snapshot.Lifecycles {
		channel <- prometheus.MustNewConstMetric(
			collector.lifecycle, prometheus.GaugeValue, float64(lifecycle.Count),
			lifecycle.InstanceID.String(), lifecycle.Provider, string(lifecycle.Lifecycle),
		)
	}
}

func emitOptionalGauge(channel chan<- prometheus.Metric, description *prometheus.Desc, value *float64, labels ...string) {
	if value != nil {
		channel <- prometheus.MustNewConstMetric(description, prometheus.GaugeValue, *value, labels...)
	}
}

func emitOptionalBool(channel chan<- prometheus.Metric, description *prometheus.Desc, value *bool, labels ...string) {
	if value == nil {
		return
	}
	numeric := 0.0
	if *value {
		numeric = 1
	}
	channel <- prometheus.MustNewConstMetric(description, prometheus.GaugeValue, numeric, labels...)
}

func (snapshot Snapshot) valid(maxInstances int) bool {
	if len(snapshot.AllowedProviders) > maximumProviders || len(snapshot.Instances) > maxInstances ||
		len(snapshot.Lifecycles) > maxInstances*maximumProviders*4 {
		return false
	}
	allowed := make(map[string]struct{}, len(snapshot.AllowedProviders))
	for _, provider := range snapshot.AllowedProviders {
		if len(provider) > maximumProviderBytes || !providerLabelPattern.MatchString(provider) {
			return false
		}
		if _, duplicate := allowed[provider]; duplicate {
			return false
		}
		allowed[provider] = struct{}{}
	}
	instances := make(map[uuid.UUID]struct{}, len(snapshot.Instances)+len(snapshot.Lifecycles))
	for _, instance := range snapshot.Instances {
		if instance.InstanceID == uuid.Nil || !instance.State.Valid() || !validOptionalSeconds(instance.SchedulerLagSeconds) ||
			!validOptionalSeconds(instance.QueueWaitSeconds) || !validOptionalSeconds(instance.PollStartLagSeconds) {
			return false
		}
		if _, duplicate := instances[instance.InstanceID]; duplicate {
			return false
		}
		instances[instance.InstanceID] = struct{}{}
		providers := make(map[string]struct{}, len(instance.Providers))
		for _, provider := range instance.Providers {
			if _, permitted := allowed[provider.Provider]; !permitted {
				return false
			}
			if _, duplicate := providers[provider.Provider]; duplicate {
				return false
			}
			if (!provider.PromotionEvaluated && (provider.PromotionApplied || provider.PromotionSkippedReason != "")) ||
				(provider.PromotionEvaluated && (provider.PromotionApplied == (provider.PromotionSkippedReason != "") ||
					(!provider.PromotionApplied && !provider.PromotionSkippedReason.Valid()))) {
				return false
			}
			providers[provider.Provider] = struct{}{}
		}
	}
	lifecycles := make(map[string]struct{}, len(snapshot.Lifecycles))
	for _, lifecycle := range snapshot.Lifecycles {
		if lifecycle.InstanceID == uuid.Nil {
			return false
		}
		if _, ok := allowed[lifecycle.Provider]; !ok || !lifecycle.Lifecycle.Valid() {
			return false
		}
		key := lifecycle.InstanceID.String() + "\x00" + lifecycle.Provider + "\x00" + string(lifecycle.Lifecycle)
		if _, duplicate := lifecycles[key]; duplicate {
			return false
		}
		instances[lifecycle.InstanceID] = struct{}{}
		lifecycles[key] = struct{}{}
	}
	return len(instances) <= maxInstances
}

func validOptionalSeconds(value *float64) bool {
	return value == nil || (*value >= 0 && !math.IsNaN(*value) && !math.IsInf(*value, 0))
}
