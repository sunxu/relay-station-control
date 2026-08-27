package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type accountInventoryMetricResponseWriter struct {
	http.ResponseWriter
	status int
}

func (writer *accountInventoryMetricResponseWriter) WriteHeader(status int) {
	if writer.status != 0 {
		return
	}
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *accountInventoryMetricResponseWriter) Write(body []byte) (int, error) {
	if writer.status == 0 {
		writer.status = http.StatusOK
	}
	return writer.ResponseWriter.Write(body)
}

type accountInventoryMetricResult string
type accountInventoryMetricError string

const (
	accountInventoryMetricOperationQuery = "query"

	accountInventoryMetricResultSuccess accountInventoryMetricResult = "success"
	accountInventoryMetricResultFailure accountInventoryMetricResult = "failure"

	accountInventoryMetricErrorNone         accountInventoryMetricError = "none"
	accountInventoryMetricErrorUnauthorized accountInventoryMetricError = "unauthorized"
	accountInventoryMetricErrorForbidden    accountInventoryMetricError = "forbidden"
	accountInventoryMetricErrorInvalid      accountInventoryMetricError = "invalid_request"
	accountInventoryMetricErrorNotFound     accountInventoryMetricError = "not_found"
	accountInventoryMetricErrorConflict     accountInventoryMetricError = "capability_unsupported"
	accountInventoryMetricErrorUnavailable  accountInventoryMetricError = "unavailable"
)

// AccountInventoryMetrics exposes only closed, low-cardinality labels. It
// deliberately has no identity, filter, cursor, request, policy, or version
// dimensions.
type AccountInventoryMetrics struct {
	queries  *prometheus.CounterVec
	duration *prometheus.HistogramVec
	results  *prometheus.CounterVec
}

func NewAccountInventoryMetrics() *AccountInventoryMetrics {
	return &AccountInventoryMetrics{
		queries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "relay_control_account_inventory_queries_total",
			Help: "Account inventory product queries by closed result and error.",
		}, []string{"operation", "result", "error"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "relay_control_account_inventory_query_duration_seconds",
			Help:    "Account inventory product query latency by closed result.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
		}, []string{"operation", "result"}),
		results: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "relay_control_account_inventory_query_result_pages_total",
			Help: "Account inventory result pages by closed size bucket.",
		}, []string{"operation", "result"}),
	}
}

func (metrics *AccountInventoryMetrics) Describe(channel chan<- *prometheus.Desc) {
	metrics.queries.Describe(channel)
	metrics.duration.Describe(channel)
	metrics.results.Describe(channel)
}

func (metrics *AccountInventoryMetrics) Collect(channel chan<- prometheus.Metric) {
	metrics.queries.Collect(channel)
	metrics.duration.Collect(channel)
	metrics.results.Collect(channel)
}

func (metrics *AccountInventoryMetrics) record(
	result accountInventoryMetricResult,
	errorCode accountInventoryMetricError,
	resultCount int,
	duration time.Duration,
) error {
	if metrics == nil || !validAccountInventoryMetricResult(result) ||
		!validAccountInventoryMetricError(errorCode) || duration < 0 || resultCount < 0 || resultCount > 100 ||
		(result == accountInventoryMetricResultSuccess) != (errorCode == accountInventoryMetricErrorNone) {
		return errors.New("api: invalid account inventory metric")
	}
	metrics.queries.WithLabelValues(accountInventoryMetricOperationQuery, string(result), string(errorCode)).Inc()
	metrics.duration.WithLabelValues(accountInventoryMetricOperationQuery, string(result)).Observe(duration.Seconds())
	if result == accountInventoryMetricResultSuccess {
		metrics.results.WithLabelValues(accountInventoryMetricOperationQuery, accountInventoryResultBucket(resultCount)).Inc()
	}
	return nil
}

func validAccountInventoryMetricResult(result accountInventoryMetricResult) bool {
	return result == accountInventoryMetricResultSuccess || result == accountInventoryMetricResultFailure
}

func validAccountInventoryMetricError(errorCode accountInventoryMetricError) bool {
	switch errorCode {
	case accountInventoryMetricErrorNone, accountInventoryMetricErrorUnauthorized,
		accountInventoryMetricErrorForbidden, accountInventoryMetricErrorInvalid,
		accountInventoryMetricErrorNotFound, accountInventoryMetricErrorConflict,
		accountInventoryMetricErrorUnavailable:
		return true
	default:
		return false
	}
}

func accountInventoryResultBucket(count int) string {
	switch {
	case count == 0:
		return "zero"
	case count <= 25:
		return "1_25"
	case count <= 50:
		return "26_50"
	default:
		return "51_100"
	}
}

func accountInventoryMetricStatus(status int) (accountInventoryMetricResult, accountInventoryMetricError) {
	switch status {
	case http.StatusOK:
		return accountInventoryMetricResultSuccess, accountInventoryMetricErrorNone
	case http.StatusBadRequest:
		return accountInventoryMetricResultFailure, accountInventoryMetricErrorInvalid
	case http.StatusUnauthorized:
		return accountInventoryMetricResultFailure, accountInventoryMetricErrorUnauthorized
	case http.StatusForbidden:
		return accountInventoryMetricResultFailure, accountInventoryMetricErrorForbidden
	case http.StatusNotFound:
		return accountInventoryMetricResultFailure, accountInventoryMetricErrorNotFound
	case http.StatusConflict:
		return accountInventoryMetricResultFailure, accountInventoryMetricErrorConflict
	default:
		return accountInventoryMetricResultFailure, accountInventoryMetricErrorUnavailable
	}
}
