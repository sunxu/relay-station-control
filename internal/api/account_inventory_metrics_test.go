package api

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestAccountInventoryMetricResponseStatusIsClosed(t *testing.T) {
	for status, want := range map[int]accountInventoryMetricError{
		http.StatusBadRequest:         accountInventoryMetricErrorInvalid,
		http.StatusUnauthorized:       accountInventoryMetricErrorUnauthorized,
		http.StatusForbidden:          accountInventoryMetricErrorForbidden,
		http.StatusNotFound:           accountInventoryMetricErrorNotFound,
		http.StatusConflict:           accountInventoryMetricErrorConflict,
		http.StatusServiceUnavailable: accountInventoryMetricErrorUnavailable,
	} {
		result, errorCode := accountInventoryMetricStatus(status)
		if result != accountInventoryMetricResultFailure || errorCode != want {
			t.Fatalf("status %d = (%q,%q), want failure/%q", status, result, errorCode, want)
		}
	}
	result, errorCode := accountInventoryMetricStatus(http.StatusOK)
	if result != accountInventoryMetricResultSuccess || errorCode != accountInventoryMetricErrorNone {
		t.Fatalf("success = (%q,%q)", result, errorCode)
	}

	recorder := httptest.NewRecorder()
	writer := &accountInventoryMetricResponseWriter{ResponseWriter: recorder}
	writer.WriteHeader(http.StatusConflict)
	writer.WriteHeader(http.StatusOK)
	if writer.status != http.StatusConflict || recorder.Code != http.StatusConflict {
		t.Fatalf("captured status=%d response=%d", writer.status, recorder.Code)
	}
}

func TestAccountInventoryGeneratedValidationFailureIsCounted(t *testing.T) {
	server := NewServer("test")
	server.accountInventoryMetrics = NewAccountInventoryMetrics()
	request := httptest.NewRequest(http.MethodPost, "/api/account-inventory/query", nil)
	response := httptest.NewRecorder()
	server.PrepareGeneratedError(response, request, &RequiredHeaderError{ParamName: "X-CSRF-Token"})
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d", response.Code)
	}
	got := testutil.ToFloat64(server.accountInventoryMetrics.queries.WithLabelValues(
		accountInventoryMetricOperationQuery,
		string(accountInventoryMetricResultFailure),
		string(accountInventoryMetricErrorForbidden),
	))
	if got != 1 {
		t.Fatalf("generated validation failure count=%v, want 1", got)
	}
}

func TestAccountInventoryMetricsUseOnlyClosedLabels(t *testing.T) {
	metrics := NewAccountInventoryMetrics()
	if err := metrics.record(accountInventoryMetricResultSuccess, accountInventoryMetricErrorNone, 0, 10*time.Millisecond); err != nil {
		t.Fatalf("record empty success: %v", err)
	}
	if err := metrics.record(accountInventoryMetricResultSuccess, accountInventoryMetricErrorNone, 57, 20*time.Millisecond); err != nil {
		t.Fatalf("record populated success: %v", err)
	}
	if err := metrics.record(accountInventoryMetricResultFailure, accountInventoryMetricErrorUnavailable, 0, 30*time.Millisecond); err != nil {
		t.Fatalf("record failure: %v", err)
	}

	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(metrics)
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	encoded := ""
	allowedFamilies := map[string]map[string]bool{
		"relay_control_account_inventory_queries_total": {
			"operation=query": true, "result=success": true, "result=failure": true,
			"error=none": true, "error=unavailable": true,
		},
		"relay_control_account_inventory_query_duration_seconds": {
			"operation=query": true, "result=success": true, "result=failure": true,
		},
		"relay_control_account_inventory_query_result_pages_total": {
			"operation=query": true, "result=zero": true, "result=51_100": true,
		},
	}
	for _, family := range families {
		encoded += family.String()
		allowed, ok := allowedFamilies[family.GetName()]
		if !ok {
			t.Fatalf("unexpected account inventory metric family %q", family.GetName())
		}
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				pair := label.GetName() + "=" + label.GetValue()
				if !allowed[pair] {
					t.Fatalf("open or unexpected label %q in %s", pair, family.GetName())
				}
			}
		}
	}
	for _, forbidden := range []string{
		"operator@example.invalid", "provider:operator@example.invalid", "cursor-canary",
		"instance_id", "request_id", "filter", "version", "commit",
	} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("metrics contain forbidden dimension %q: %s", forbidden, encoded)
		}
	}
	if count := testutil.CollectAndCount(metrics); count == 0 {
		t.Fatal("account inventory metrics were not collected")
	}
}

func TestAccountInventoryResultBucketsAreClosedAndExhaustive(t *testing.T) {
	tests := map[int]string{
		0: "zero", 1: "1_25", 25: "1_25", 26: "26_50", 50: "26_50", 51: "51_100", 100: "51_100",
	}
	got := make([]string, 0, len(tests))
	for count, want := range tests {
		bucket := accountInventoryResultBucket(count)
		if bucket != want {
			t.Fatalf("accountInventoryResultBucket(%d) = %q, want %q", count, bucket, want)
		}
		got = append(got, bucket)
	}
	sort.Strings(got)
	want := []string{"1_25", "1_25", "26_50", "26_50", "51_100", "51_100", "zero"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("result buckets = %v, want %v", got, want)
	}
}

func TestAccountInventoryMetricsRejectOpenValues(t *testing.T) {
	metrics := NewAccountInventoryMetrics()
	for name, testCase := range map[string]struct {
		result accountInventoryMetricResult
		error  accountInventoryMetricError
		count  int
	}{
		"open result":    {result: "operator@example.invalid", error: accountInventoryMetricErrorNone},
		"open error":     {result: accountInventoryMetricResultFailure, error: "raw database failure"},
		"success error":  {result: accountInventoryMetricResultSuccess, error: accountInventoryMetricErrorUnavailable},
		"failure none":   {result: accountInventoryMetricResultFailure, error: accountInventoryMetricErrorNone},
		"large count":    {result: accountInventoryMetricResultSuccess, error: accountInventoryMetricErrorNone, count: 101},
		"negative count": {result: accountInventoryMetricResultSuccess, error: accountInventoryMetricErrorNone, count: -1},
	} {
		t.Run(name, func(t *testing.T) {
			if err := metrics.record(testCase.result, testCase.error, testCase.count, time.Millisecond); err == nil {
				t.Fatal("invalid metric was accepted")
			}
		})
	}
	if err := metrics.record(accountInventoryMetricResultSuccess, accountInventoryMetricErrorNone, 1, -time.Nanosecond); err == nil {
		t.Fatal("negative duration was accepted")
	}
	var unavailable *AccountInventoryMetrics
	if err := unavailable.record(accountInventoryMetricResultSuccess, accountInventoryMetricErrorNone, 1, time.Millisecond); err == nil {
		t.Fatal("nil metrics receiver was accepted")
	}
}
