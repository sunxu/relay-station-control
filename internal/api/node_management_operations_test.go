package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	authn "github.com/sunxu/relay-station-control/internal/auth"
	"github.com/sunxu/relay-station-control/internal/drivers"
)

type nodeProbeAuthorizerStub struct {
	target drivers.NodeTarget
	err    error
}

func (reader nodeProbeAuthorizerStub) AuthorizeNodeProbe(context.Context, uuid.UUID) (drivers.NodeTarget, error) {
	return reader.target, reader.err
}

type nodeProbeRegistryStub struct {
	observation drivers.ProbeObservation
	err         error
	calls       int
	request     drivers.ProbeRequest
}

func (registry *nodeProbeRegistryStub) Probe(_ context.Context, request drivers.ProbeRequest) (drivers.ProbeObservation, error) {
	registry.calls++
	registry.request = request
	return registry.observation, registry.err
}

type nodeProbeAuditStub struct {
	calls   int
	actor   uuid.UUID
	node    uuid.UUID
	action  string
	details map[string]any
	err     error
}

func (audit *nodeProbeAuditStub) RecordNodeProbe(_ context.Context, actor, node uuid.UUID, _ string, action string, details map[string]any) error {
	audit.calls++
	audit.actor = actor
	audit.node = node
	audit.action = action
	audit.details = details
	return audit.err
}

func TestNodeProbeUsesFixedBoundedProjectionAndNodeMetrics(t *testing.T) {
	instanceID := uuid.New()
	actorID := uuid.New()
	registry := &nodeProbeRegistryStub{observation: drivers.ProbeObservation{
		Reachable: true,
		Result:    drivers.ResultSuccess,
		Reason:    drivers.ReasonNone,
	}}
	audit := &nodeProbeAuditStub{}
	metrics := NewAssetMetrics()
	server := &Server{
		nodeProbeAuthorizer: nodeProbeAuthorizerStub{target: drivers.NodeTarget{InstanceID: instanceID, NodeType: drivers.NodeTypeCLIProxyAPI, DriverContractVersion: drivers.DriverContractCLIProxyAPIAuthFilesV1, ManagementEndpoint: "http://node.example", Capabilities: []drivers.Capability{drivers.CapabilityManagementHealthRead}}},
		nodeProbeRegistry:   registry,
		nodeProbeAuditor:    audit,
		assetMetrics:        metrics,
		resolver:            authn.NewSourceResolver(nil),
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/assets/nodes/"+instanceID.String()+"/health", nil)
	server.runNodeProbe(recorder, request, instanceID, actorID, "node.health")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response NodeProbeResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Result != "success" || !response.Reachable || response.Reason != "none" || response.LatencyMs < 0 {
		t.Fatalf("unexpected probe response: %+v", response)
	}
	if registry.calls != 1 {
		t.Fatalf("probe calls=%d, want 1", registry.calls)
	}
	if registry.request.Target.ManagementEndpoint != "http://node.example" {
		t.Fatalf("target endpoint=%q", registry.request.Target.ManagementEndpoint)
	}
	if registry.request.Target.ReaderSecretReference != (drivers.SecretReference{}) {
		t.Fatal("probe target carried Reader Secret reference")
	}
	if audit.calls != 1 || audit.actor != actorID || audit.node != instanceID || audit.action != "node.health" {
		t.Fatalf("unexpected audit call: %+v", audit)
	}
	if _, present := audit.details["management_endpoint"]; present {
		t.Fatal("audit persisted management endpoint")
	}
	if _, present := audit.details["secret"]; present {
		t.Fatal("audit persisted secret")
	}
	if got := audit.details["instance_id"]; got != instanceID.String() {
		t.Fatalf("audit instance_id=%v", got)
	}
	var found bool
	for _, sample := range metrics.snapshot() {
		if sample.name == "control_asset_health_total" && len(sample.labels) == 2 && sample.labels[0] == "node" && sample.labels[1] == "healthy" && sample.value == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("node health metric not recorded: %+v", metrics.snapshot())
	}
}

func TestNodeProbeAuditFailureDoesNotRetryProbe(t *testing.T) {
	instanceID := uuid.New()
	registry := &nodeProbeRegistryStub{observation: drivers.ProbeObservation{Reachable: true, Result: drivers.ResultSuccess, Reason: drivers.ReasonNone}}
	audit := &nodeProbeAuditStub{err: errors.New("audit unavailable")}
	server := &Server{
		nodeProbeAuthorizer: nodeProbeAuthorizerStub{target: drivers.NodeTarget{InstanceID: instanceID}},
		nodeProbeRegistry:   registry,
		nodeProbeAuditor:    audit,
		resolver:            authn.NewSourceResolver(nil),
	}
	recorder := httptest.NewRecorder()
	server.runNodeProbe(recorder, httptest.NewRequest(http.MethodGet, "/", nil), instanceID, uuid.New(), "node.connection_test")
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if registry.calls != 1 || audit.calls != 1 {
		t.Fatalf("probe/audit calls=%d/%d, want 1/1", registry.calls, audit.calls)
	}
}

func TestNodeProbeFailureMapsToBoundedObservation(t *testing.T) {
	instanceID := uuid.New()
	registry := &nodeProbeRegistryStub{observation: drivers.ProbeObservation{Result: drivers.ResultFailed, Reason: drivers.ReasonTimeout}, err: errors.New("timeout")}
	audit := &nodeProbeAuditStub{}
	server := &Server{
		nodeProbeAuthorizer: nodeProbeAuthorizerStub{target: drivers.NodeTarget{InstanceID: instanceID}},
		nodeProbeRegistry:   registry,
		nodeProbeAuditor:    audit,
		assetMetrics:        NewAssetMetrics(),
		resolver:            authn.NewSourceResolver(nil),
	}
	recorder := httptest.NewRecorder()
	server.runNodeProbe(recorder, httptest.NewRequest(http.MethodGet, "/", nil), instanceID, uuid.New(), "node.connection_test")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"reason":"timeout"`) || strings.Contains(recorder.Body.String(), "timeout") && strings.Contains(recorder.Body.String(), "raw") {
		t.Fatalf("unexpected bounded failure body=%s", recorder.Body.String())
	}
	for _, sample := range server.assetMetrics.snapshot() {
		if sample.name == "control_asset_connection_test_total" && sample.labels[0] == "node" && sample.labels[1] != "timeout" {
			t.Fatalf("connection-test metric result=%v", sample.labels)
		}
	}
}

func TestDecodeNodeObjectRejectsUnknownDuplicateAndTrailingJSON(t *testing.T) {
	for _, body := range []string{
		`{"command_id":"` + uuid.NewString() + `","command_id":"` + uuid.NewString() + `"}`,
		`{} {}`,
	} {
		t.Run(body, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			if _, ok := decodeNodeObject(recorder, request, nil); ok || recorder.Code != http.StatusBadRequest {
				t.Fatalf("accepted malformed node body: status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestNodeManagementRoutesHaveFrozenMethods(t *testing.T) {
	server := &Server{resolver: authn.NewSourceResolver(nil)}
	router := HandlerWithOptions(server, ChiServerOptions{ErrorHandlerFunc: server.PrepareGeneratedError})
	for _, test := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/assets/nodes/00000000-0000-0000-0000-000000000001/health"},
		{http.MethodPost, "/api/assets/nodes/00000000-0000-0000-0000-000000000001/connection-test"},
		{http.MethodPost, "/api/assets/nodes/00000000-0000-0000-0000-000000000001/monitoring-enable"},
		{http.MethodPost, "/api/assets/nodes/00000000-0000-0000-0000-000000000001/monitoring-disable"},
	} {
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(`{}`))
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code == http.StatusMethodNotAllowed || recorder.Code == http.StatusNotFound {
			t.Fatalf("route missing for %s %s: status=%d", test.method, test.path, recorder.Code)
		}
	}
}
