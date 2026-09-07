package cliproxyapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunxu/relay-station-control/internal/drivers"
	controlpoll "github.com/sunxu/relay-station-control/internal/inventorypoll"
)

type lifecycleCanaryDialer struct {
	errorText string
}

func (dialer lifecycleCanaryDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New(dialer.errorText)
}

func TestAccountInventoryLifecycleCanariesTraverseDriverWorkerArtifacts(t *testing.T) {
	artifactRoot := os.Getenv("CONTROL_LIFECYCLE_CANARY_ARTIFACT_DIR")
	if artifactRoot == "" {
		t.Skip("lifecycle canary artifact acceptance is opt-in")
	}
	canary := lifecycleCanaryEnvironment(t)
	successDirectory := filepath.Join(artifactRoot, "success")
	failureDirectory := filepath.Join(artifactRoot, "failure")
	for _, directory := range []string{successDirectory, failureDirectory} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal("canary artifact directory unavailable")
		}
	}

	clearProxyEnvironment(t)
	var driverLogs bytes.Buffer
	metrics := NewDriverMetrics()
	observer := NewObserver(metrics, slog.New(slog.NewJSONHandler(&driverLogs, nil)))
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/v0/management/auth-files" ||
			request.Header.Get("X-Management-Key") != canary["SECRET_VALUE"] {
			t.Error("canary Driver request shape invalid")
		}
		response.Header().Set("X-CPA-VERSION", canary["VERSION"])
		response.Header().Set("X-CPA-COMMIT", canary["COMMIT"])
		response.Header().Set("X-Lifecycle-Canary", canary["RESPONSE_HEADER"])
		body, err := json.Marshal(map[string]any{
			"files": []map[string]any{{
				"provider": "openai", "email": canary["EMAIL"],
				"source": "memory", "status": "active",
				canary["UNKNOWN_FIELD"]: canary["RESPONSE_BODY"],
			}},
		})
		if err != nil {
			t.Error("canary response encoding failed")
			return
		}
		_, _ = response.Write(body)
	}))
	t.Cleanup(server.Close)

	directory := t.TempDir()
	secretPath := filepath.Join(directory, "management-key")
	if err := os.WriteFile(secretPath, []byte(canary["SECRET_VALUE"]+"\n"), 0o600); err != nil {
		t.Fatal("canary Secret fixture unavailable")
	}
	mappingPath := filepath.Join(directory, "mapping.json")
	mapping, err := json.Marshal(map[string]any{
		"provider":   "file",
		"references": []map[string]string{{"reference": canary["SECRET_REFERENCE"], "path": secretPath}},
	})
	if err != nil {
		t.Fatal("canary Secret mapping invalid")
	}
	if err = os.WriteFile(mappingPath, mapping, 0o600); err != nil {
		t.Fatal("canary Secret mapping unavailable")
	}
	secretResolver, err := drivers.NewFileSecretResolver(drivers.FileSecretResolverConfig{MappingFile: mappingPath})
	if err != nil {
		t.Fatal("canary Secret resolver unavailable")
	}
	advertised, err := netip.ParseAddr(canary["IP"])
	if err != nil {
		t.Fatal("canary IP invalid")
	}
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal("synthetic Driver port invalid")
	}
	dialer := &mappedDialer{actual: server.Listener.Addr().String(), advertised: advertised}
	driver, err := newDriver(DriverConfig{
		Management: drivers.ManagementConfig{
			AllowedDNSNames:        []string{canary["ENDPOINT"]},
			AllowedManagementCIDRs: []string{"10.42.0.0/16"},
			AllowedPlainHTTPCIDRs:  []string{"10.42.0.0/16"},
		},
		SecretResolver: secretResolver, Observer: observer,
		Now: func() time.Time { return time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC) },
	}, dialer)
	if err != nil {
		t.Fatal("canary Driver construction failed")
	}
	instanceID := uuid.New()
	pollID := uuid.MustParse(canary["POLL_ID"])
	policyID := uuid.MustParse(canary["POLICY_ID"])
	target := drivers.NodeTarget{
		InstanceID: instanceID, NodeType: drivers.NodeTypeCLIProxyAPI,
		DriverContractVersion: drivers.DriverContractCLIProxyAPIAuthFilesV1,
		ManagementEndpoint:    "http://" + canary["ENDPOINT"] + ":" + port,
		ReaderSecretReference: drivers.NewSecretReference(canary["SECRET_REFERENCE"]),
		Capabilities:          []drivers.Capability{drivers.CapabilityManagementAccountInventoryRead},
	}
	repository := &workerProjectionRepository{claim: &controlpoll.ClaimedRun{
		PollRunID: pollID, InstanceID: instanceID, PolicyVersionID: policyID,
		ScheduledAt: time.Unix(1800, 0).UTC(), Attempt: 1, MaxAttempts: 2,
		GraceRemaining: 5 * time.Second, Target: target,
		ProviderPolicy: drivers.ProviderPolicySnapshot{VersionID: policyID, ActiveProviders: []string{"openai"}},
	}}
	worker, err := controlpoll.NewWorker(repository, driver, workerProjectionConfig())
	if err != nil {
		t.Fatal("canary Worker construction failed")
	}
	workerContext, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(workerContext) }()
	finalize := repository.waitForFinalize(t)
	cancel()
	if err := <-done; err != nil {
		t.Fatal("canary Worker shutdown failed")
	}
	if len(finalize.SnapshotItems) != 1 || finalize.SnapshotItems[0].Email != canary["EMAIL"] ||
		finalize.SnapshotItems[0].AccountKey != canary["ACCOUNT_KEY"] || finalize.Node.Version != canary["VERSION"] ||
		finalize.Node.Commit != canary["COMMIT"] || dialer.callCount() != 1 {
		t.Fatal("canaries did not traverse the production Driver and Worker projection")
	}

	requestCount := testutil.ToFloat64(metrics.requests.WithLabelValues(
		string(drivers.NodeTypeCLIProxyAPI), string(drivers.OperationAccountInventory), string(drivers.ResultSuccess),
	))
	if requestCount != 1 {
		t.Fatal("canary Driver metric count invalid")
	}
	writeLifecycleCanaryArtifact(t, filepath.Join(successDirectory, "driver.log"), driverLogs.Bytes())
	writeLifecycleCanaryArtifact(t, filepath.Join(successDirectory, "metrics.log"), []byte("driver_requests_success=1\n"))
	writeLifecycleCanaryArtifact(t, filepath.Join(successDirectory, "test.log"), []byte("driver_worker_projection=success request_count=1 snapshot_items=1\n"))

	var failureLogs bytes.Buffer
	failureMetrics := NewDriverMetrics()
	failureDriver, err := newDriver(DriverConfig{
		Management: drivers.ManagementConfig{
			AllowedDNSNames:        []string{canary["ENDPOINT"]},
			AllowedManagementCIDRs: []string{"10.42.0.0/16"},
			AllowedPlainHTTPCIDRs:  []string{"10.42.0.0/16"},
		},
		SecretResolver: secretResolver,
		Observer:       NewObserver(failureMetrics, slog.New(slog.NewJSONHandler(&failureLogs, nil))),
	},
		lifecycleCanaryDialer{errorText: canary["RAW_ERROR"]})
	if err != nil {
		t.Fatal("failure Driver construction failed")
	}
	failed, failureErr := failureDriver.ListAccountInventory(context.Background(), drivers.InventoryRequest{
		Target: target, ProviderPolicy: drivers.ProviderPolicySnapshot{
			VersionID: policyID, ActiveProviders: []string{"openai"},
		},
	})
	if failureErr == nil || failed.Result != drivers.ResultFailed || failed.Reason != drivers.ReasonNetworkUnavailable ||
		strings.Contains(failureErr.Error(), canary["RAW_ERROR"]) {
		t.Fatal("raw failure canary did not collapse to the fixed Driver error")
	}
	writeLifecycleCanaryArtifact(t, filepath.Join(failureDirectory, "driver.log"), failureLogs.Bytes())
	writeLifecycleCanaryArtifact(t, filepath.Join(failureDirectory, "error.log"), []byte(failureErr.Error()+"\n"))
	writeLifecycleCanaryArtifact(t, filepath.Join(failureDirectory, "test.log"), []byte("driver_failure=network_unavailable request_count=1\n"))
}

func lifecycleCanaryEnvironment(t *testing.T) map[string]string {
	t.Helper()
	result := make(map[string]string)
	for _, item := range []struct{ key, environment string }{
		{"ENDPOINT", "CONTROL_LIFECYCLE_CANARY_ENDPOINT"},
		{"IP", "CONTROL_LIFECYCLE_CANARY_IP"},
		{"SECRET_REFERENCE", "CONTROL_LIFECYCLE_CANARY_SECRET_REFERENCE"},
		{"SECRET_VALUE", "CONTROL_LIFECYCLE_CANARY_SECRET_VALUE"},
		{"EMAIL", "CONTROL_LIFECYCLE_CANARY_EMAIL"},
		{"ACCOUNT_KEY", "CONTROL_LIFECYCLE_CANARY_ACCOUNT_KEY"},
		{"RESPONSE_BODY", "CONTROL_LIFECYCLE_CANARY_RESPONSE_BODY"},
		{"RESPONSE_HEADER", "CONTROL_LIFECYCLE_CANARY_RESPONSE_HEADER"},
		{"VERSION", "CONTROL_LIFECYCLE_CANARY_VERSION"},
		{"COMMIT", "CONTROL_LIFECYCLE_CANARY_COMMIT"},
		{"RAW_ERROR", "CONTROL_LIFECYCLE_CANARY_RAW_ERROR"},
		{"SQL_PARAMETER", "CONTROL_LIFECYCLE_CANARY_SQL_PARAMETER"},
		{"POLL_ID", "CONTROL_LIFECYCLE_CANARY_POLL_ID"},
		{"POLICY_ID", "CONTROL_LIFECYCLE_CANARY_POLICY_ID"},
		{"UNKNOWN_FIELD", "CONTROL_LIFECYCLE_CANARY_UNKNOWN_FIELD"},
	} {
		value := os.Getenv(item.environment)
		if value == "" {
			t.Fatalf("missing canary class %s", item.key)
		}
		result[item.key] = value
	}
	return result
}

func writeLifecycleCanaryArtifact(t *testing.T, path string, encoded []byte) {
	t.Helper()
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal("canary artifact write failed")
	}
}
