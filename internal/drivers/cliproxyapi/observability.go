package cliproxyapi

import (
	"context"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sunxu/relay-station-control/internal/drivers"
)

// DriverMetrics contains only closed, low-cardinality dimensions. It does not
// accept a target, provider, version, error, or any other caller-derived value.
type DriverMetrics struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

func NewDriverMetrics() *DriverMetrics {
	return &DriverMetrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "relay_control_node_driver_requests_total",
			Help: "Completed fixed Control Node Driver operations.",
		}, []string{"node_type", "operation", "result"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "relay_control_node_driver_request_duration_seconds",
			Help:    "Duration of completed fixed Control Node Driver operations.",
			Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 3, 5, 10, 15},
		}, []string{"node_type", "operation", "result"}),
	}
}

func (metrics *DriverMetrics) Describe(channel chan<- *prometheus.Desc) {
	if metrics == nil {
		return
	}
	metrics.requests.Describe(channel)
	metrics.duration.Describe(channel)
}

func (metrics *DriverMetrics) Collect(channel chan<- prometheus.Metric) {
	if metrics == nil {
		return
	}
	metrics.requests.Collect(channel)
	metrics.duration.Collect(channel)
}

// Observe drops any unregistered dimension instead of creating an unbounded
// time series. The Driver result remains unaffected by observability failure.
func (metrics *DriverMetrics) Observe(operation drivers.Operation, result drivers.Result, elapsed time.Duration) bool {
	if metrics == nil || !validOperation(operation) || !validResult(result) {
		return false
	}
	if elapsed < 0 {
		elapsed = 0
	}
	labels := []string{string(drivers.NodeTypeCLIProxyAPI), string(operation), string(result)}
	metrics.requests.WithLabelValues(labels...).Inc()
	metrics.duration.WithLabelValues(labels...).Observe(elapsed.Seconds())
	return true
}

// Observer is the complete Driver observation projection. It deliberately has
// no API for arbitrary attributes or error values.
type Observer struct {
	metrics *DriverMetrics
	logger  *slog.Logger
}

func NewObserver(metrics *DriverMetrics, logger *slog.Logger) *Observer {
	return &Observer{metrics: metrics, logger: logger}
}

func (observer *Observer) Record(
	ctx context.Context,
	operation drivers.Operation,
	result drivers.Result,
	reason drivers.Reason,
	elapsed time.Duration,
) bool {
	if observer == nil || !validOperation(operation) || !validResult(result) || !validReason(reason) {
		return false
	}
	if observer.metrics != nil {
		observer.metrics.Observe(operation, result, elapsed)
	}
	if observer.logger != nil {
		observer.logger.LogAttrs(ctx, slog.LevelInfo, "node driver request",
			slog.String("component", "node_driver"),
			slog.String("node_type", string(drivers.NodeTypeCLIProxyAPI)),
			slog.String("action", string(operation)),
			slog.String("result", string(result)),
			slog.String("reason", string(reason)),
		)
	}
	return true
}

func validOperation(operation drivers.Operation) bool {
	return operation == drivers.OperationProbe || operation == drivers.OperationAccountInventory
}

func validResult(result drivers.Result) bool {
	switch result {
	case drivers.ResultSuccess, drivers.ResultFailed, drivers.ResultUnsupported, drivers.ResultDegraded:
		return true
	default:
		return false
	}
}

func validReason(reason drivers.Reason) bool {
	switch reason {
	case drivers.ReasonNone,
		drivers.ReasonCapabilityUnsupported,
		drivers.ReasonNodeTypeUnsupported,
		drivers.ReasonDriverContractMismatch,
		drivers.ReasonSecretUnavailable,
		drivers.ReasonSecretReferenceUnknown,
		drivers.ReasonSecretProviderUnknown,
		drivers.ReasonSecretFileUnsafe,
		drivers.ReasonTargetRejected,
		drivers.ReasonDNSRejected,
		drivers.ReasonNetworkUnavailable,
		drivers.ReasonTLSRejected,
		drivers.ReasonRedirectRejected,
		drivers.ReasonTimeout,
		drivers.ReasonCancelled,
		drivers.ReasonHTTPStatus,
		drivers.ReasonResponseInvalid,
		drivers.ReasonResponseTooLarge,
		drivers.ReasonRecordLimit,
		drivers.ReasonContractInvalid:
		return true
	default:
		return false
	}
}
