package auth

import (
	"reflect"
	"strings"
	"testing"
)

func TestAuthMetricDescriptorsHaveOnlyBoundedLabels(t *testing.T) {
	allowed := map[string]bool{"environment": true, "operation": true, "result": true, "dimension": true}
	for _, descriptor := range AuthMetricDescriptors() {
		if !strings.HasPrefix(descriptor.Name, "relay_control_auth_") {
			t.Fatalf("unexpected metric name %q", descriptor.Name)
		}
		for _, label := range descriptor.Labels {
			if !allowed[label] {
				t.Fatalf("unbounded metric label %q", label)
			}
		}
	}
	descriptors := AuthMetricDescriptors()
	descriptors[0].Labels[0] = "login_name"
	if reflect.DeepEqual(descriptors, AuthMetricDescriptors()) {
		t.Fatal("descriptor caller can mutate package descriptors")
	}
}

func TestAuthMetricsRejectUnknownLabelsAndExposeFixedSamples(t *testing.T) {
	metrics := NewAuthMetrics()
	if err := metrics.RecordAttempt(EnvironmentProduction, MetricOperationLogin, MetricResultSuccess); err != nil {
		t.Fatal(err)
	}
	if err := metrics.RecordRateLimit(EnvironmentProduction, RateLimitSource); err != nil {
		t.Fatal(err)
	}
	if err := metrics.SetActiveSessions(EnvironmentProduction, 2); err != nil {
		t.Fatal(err)
	}
	if err := metrics.RecordAttempt(EnvironmentProduction, MetricOperation("operator-name"), MetricResultSuccess); err == nil {
		t.Fatal("unbounded operation label accepted")
	}
	if err := metrics.RecordRateLimit(EnvironmentProduction, RateLimitDimension("198.51.100.8")); err == nil {
		t.Fatal("IP-like dimension label accepted")
	}
	if err := metrics.SetActiveSessions(Environment("request-id"), 1); err == nil {
		t.Fatal("request-like environment label accepted")
	}

	samples := metrics.Snapshot()
	if len(samples) != 3 {
		t.Fatalf("metric sample count = %d, want 3", len(samples))
	}
	serialized := ""
	for _, sample := range samples {
		serialized += sample.Name + canonicalLabels(sample.Labels)
		for label := range sample.Labels {
			if label == "administrator_id" || label == "login_name" || label == "ip" || label == "session_id" || label == "request_id" || label == "error" {
				t.Fatalf("unbounded label %q emitted", label)
			}
		}
	}
	for _, canary := range []string{"operator-name", "198.51.100.8", "request-id"} {
		if strings.Contains(serialized, canary) {
			t.Fatalf("metric output contains canary %q", canary)
		}
	}
}
