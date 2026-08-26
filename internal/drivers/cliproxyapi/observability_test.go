package cliproxyapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunxu/relay-station-control/internal/drivers"
)

func TestObserverProjectsOnlyClosedLowCardinalityFields(t *testing.T) {
	metrics := NewDriverMetrics()
	var output bytes.Buffer
	observer := NewObserver(metrics, slog.New(slog.NewJSONHandler(&output, nil)))
	if !observer.Record(context.Background(), drivers.OperationProbe, drivers.ResultFailed, drivers.ReasonTLSRejected, 250*time.Millisecond) {
		t.Fatal("valid observation was dropped")
	}

	wantMetrics := `
# HELP relay_control_node_driver_request_duration_seconds Duration of completed fixed Control Node Driver operations.
# TYPE relay_control_node_driver_request_duration_seconds histogram
relay_control_node_driver_request_duration_seconds_bucket{node_type="cliproxyapi",operation="probe",result="failed",le="0.01"} 0
relay_control_node_driver_request_duration_seconds_bucket{node_type="cliproxyapi",operation="probe",result="failed",le="0.05"} 0
relay_control_node_driver_request_duration_seconds_bucket{node_type="cliproxyapi",operation="probe",result="failed",le="0.1"} 0
relay_control_node_driver_request_duration_seconds_bucket{node_type="cliproxyapi",operation="probe",result="failed",le="0.25"} 1
relay_control_node_driver_request_duration_seconds_bucket{node_type="cliproxyapi",operation="probe",result="failed",le="0.5"} 1
relay_control_node_driver_request_duration_seconds_bucket{node_type="cliproxyapi",operation="probe",result="failed",le="1"} 1
relay_control_node_driver_request_duration_seconds_bucket{node_type="cliproxyapi",operation="probe",result="failed",le="3"} 1
relay_control_node_driver_request_duration_seconds_bucket{node_type="cliproxyapi",operation="probe",result="failed",le="5"} 1
relay_control_node_driver_request_duration_seconds_bucket{node_type="cliproxyapi",operation="probe",result="failed",le="10"} 1
relay_control_node_driver_request_duration_seconds_bucket{node_type="cliproxyapi",operation="probe",result="failed",le="15"} 1
relay_control_node_driver_request_duration_seconds_bucket{node_type="cliproxyapi",operation="probe",result="failed",le="+Inf"} 1
relay_control_node_driver_request_duration_seconds_sum{node_type="cliproxyapi",operation="probe",result="failed"} 0.25
relay_control_node_driver_request_duration_seconds_count{node_type="cliproxyapi",operation="probe",result="failed"} 1
# HELP relay_control_node_driver_requests_total Completed fixed Control Node Driver operations.
# TYPE relay_control_node_driver_requests_total counter
relay_control_node_driver_requests_total{node_type="cliproxyapi",operation="probe",result="failed"} 1
`
	if err := testutil.CollectAndCompare(metrics, strings.NewReader(wantMetrics)); err != nil {
		t.Fatal(err)
	}

	line := output.String()
	for _, wanted := range []string{
		`"component":"node_driver"`, `"node_type":"cliproxyapi"`,
		`"action":"probe"`, `"result":"failed"`, `"reason":"tls_rejected"`,
	} {
		if !strings.Contains(line, wanted) {
			t.Fatalf("log does not contain %s: %s", wanted, line)
		}
	}
	var fields map[string]any
	if err := json.Unmarshal(output.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"instance_id", "endpoint", "hostname", "ip", "provider", "email", "version", "secret", "error", "header", "body"} {
		if _, exists := fields[forbidden]; exists {
			t.Fatalf("log contains forbidden dimension %q: %s", forbidden, line)
		}
	}
}

func TestObserverDropsUnregisteredDimensionsAndCanaries(t *testing.T) {
	metrics := NewDriverMetrics()
	var output bytes.Buffer
	observer := NewObserver(metrics, slog.New(slog.NewJSONHandler(&output, nil)))
	canary := "endpoint-secret-email-error-canary"
	if observer.Record(context.Background(), drivers.Operation(canary), drivers.Result(canary), drivers.Reason(canary), time.Second) {
		t.Fatal("unregistered dimensions were accepted")
	}
	if output.Len() != 0 {
		t.Fatalf("invalid observation reached logs: %s", output.String())
	}
	if count := testutil.CollectAndCount(metrics); count != 0 {
		t.Fatalf("invalid observation created %d metric families", count)
	}

	registry := prometheus.NewRegistry()
	registry.MustRegister(metrics)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if len(families) != 0 {
		t.Fatalf("empty collector unexpectedly emitted %d families", len(families))
	}
}
