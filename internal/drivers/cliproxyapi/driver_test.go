package cliproxyapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunxu/relay-station-control/internal/drivers"
)

func TestDriverProbeAndInventoryProjectFixedContract(t *testing.T) {
	secretCanary := "driver-secret-canary-72f4"
	emailCanary := "driver-email-canary-72f4@example.invalid"
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Method != http.MethodGet || request.URL.RawQuery != "" || request.Body != http.NoBody {
			t.Errorf("unexpected request shape: %s %s", request.Method, request.URL.String())
		}
		if strings.Contains(request.Header.Get("User-Agent"), secretCanary) {
			t.Fatal("Management Key entered User-Agent")
		}
		switch request.URL.Path {
		case "/base/healthz":
			if request.Header.Get("X-Management-Key") != "" {
				t.Fatal("health request carried Management Key")
			}
			_, _ = io.WriteString(response, `{"status":"ok","sensitive_future_field":"discarded"}`)
		case "/base/v0/management/auth-files":
			if request.Header.Get("X-Management-Key") != secretCanary {
				t.Fatal("inventory did not receive the resolved Management Key")
			}
			response.Header().Set("X-CPA-VERSION", "v7.2.141")
			response.Header().Set("X-CPA-COMMIT", "dc3c3b1ec3ed04bb0917e76451eaf98c6842674d")
			_, _ = io.WriteString(response, `{"files":[{"provider":"Antigravity","email":" `+emailCanary+` ","source":"file","status":"active","disabled":false,"success":8,"failed":1,"status_message":"secret body canary","token":"discarded"}]}`)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	directory := t.TempDir()
	secretPath := filepath.Join(directory, "management-key")
	if err := os.WriteFile(secretPath, []byte(secretCanary+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mappingPath := filepath.Join(directory, "mapping.json")
	mapping, err := json.Marshal(map[string]any{
		"provider": "file",
		"references": []map[string]string{{
			"reference": "file://node/test", "path": secretPath,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(mappingPath, mapping, 0o600); err != nil {
		t.Fatal(err)
	}
	secretResolver, err := drivers.NewFileSecretResolver(drivers.FileSecretResolverConfig{MappingFile: mappingPath})
	if err != nil {
		t.Fatal(err)
	}
	resolver := &sequenceResolver{results: []resolverResult{{addresses: []netip.Addr{authorizedTestIP}}}}
	dialer := &mappedDialer{actual: server.Listener.Addr().String(), advertised: authorizedTestIP}
	metrics := NewDriverMetrics()
	var logs bytes.Buffer
	driver, err := newDriver(DriverConfig{
		Management: drivers.ManagementConfig{
			AllowedDNSNames:        []string{"node.example.invalid"},
			AllowedManagementCIDRs: []string{"10.42.0.0/16"},
			AllowedPlainHTTPCIDRs:  []string{"10.42.0.0/24"},
		},
		SecretResolver: secretResolver,
		Observer:       NewObserver(metrics, slog.New(slog.NewJSONHandler(&logs, nil))),
		Now:            func() time.Time { return time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC) },
	}, resolver, dialer)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := drivers.NewRegistry(drivers.Registration{
		NodeType:              drivers.NodeTypeCLIProxyAPI,
		DriverContractVersion: drivers.DriverContractCLIProxyAPIAuthFilesV1,
		Capabilities: []drivers.Capability{
			drivers.CapabilityManagementHealthRead,
			drivers.CapabilityManagementAccountInventoryRead,
		},
		Driver: driver,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	target := drivers.NodeTarget{
		InstanceID: uuid.New(), NodeType: drivers.NodeTypeCLIProxyAPI,
		DriverContractVersion: drivers.DriverContractCLIProxyAPIAuthFilesV1,
		ManagementEndpoint:    "http://node.example.invalid:" + port + "/base",
		ReaderSecretReference: drivers.NewSecretReference("file://node/test"),
		Capabilities: []drivers.Capability{
			drivers.CapabilityManagementHealthRead,
			drivers.CapabilityManagementAccountInventoryRead,
		},
	}
	probe, err := registry.Probe(context.Background(), drivers.ProbeRequest{Target: target})
	if err != nil || !probe.Reachable || probe.Result != drivers.ResultSuccess || probe.Reason != drivers.ReasonNone {
		t.Fatalf("probe = %#v, %v", probe, err)
	}
	inventory, err := registry.ListAccountInventory(context.Background(), drivers.InventoryRequest{
		Target: target,
		ProviderPolicy: drivers.ProviderPolicySnapshot{
			VersionID: uuid.New(), ActiveProviders: []string{"antigravity"},
		},
	})
	if err != nil || inventory.Result != drivers.ResultSuccess || !inventory.TransportSuccess ||
		!inventory.ResponseShapeValid || !inventory.ContractValid || inventory.Mode != drivers.InventoryModeRuntime {
		t.Fatalf("inventory = %#v, %v", inventory, err)
	}
	if len(inventory.Providers) != 1 || len(inventory.Providers[0].Accounts) != 1 ||
		inventory.Providers[0].Accounts[0].Email != emailCanary ||
		inventory.Providers[0].Accounts[0].State != drivers.AccountStateActive {
		t.Fatalf("account projection = %#v", inventory)
	}
	if requests.Load() != 2 || resolver.callCount() != 2 || dialer.callCount() != 2 {
		t.Fatalf("request boundary = requests=%d dns=%d dial=%d", requests.Load(), resolver.callCount(), dialer.callCount())
	}
	for _, forbidden := range []string{secretCanary, emailCanary, secretPath, "status_message", "token"} {
		if strings.Contains(logs.String(), forbidden) {
			t.Fatalf("observability leaked %q: %s", forbidden, logs.String())
		}
	}
	if count := testutil.ToFloat64(metrics.requests.WithLabelValues("cliproxyapi", "probe", "success")); count != 1 {
		t.Fatalf("probe metric = %v", count)
	}
	if count := testutil.ToFloat64(metrics.requests.WithLabelValues("cliproxyapi", "account_inventory", "success")); count != 1 {
		t.Fatalf("inventory metric = %v", count)
	}
}

type countingSecretResolver struct{ calls atomic.Int32 }

func (resolver *countingSecretResolver) Resolve(context.Context, drivers.SecretReference) (*drivers.Secret, error) {
	resolver.calls.Add(1)
	return nil, drivers.ErrSecretReferenceUnknown
}

func TestDriverFailsBeforeSecretDNSOrNetworkAndConstructorIsDormant(t *testing.T) {
	secretResolver := &countingSecretResolver{}
	dnsResolver := &sequenceResolver{results: []resolverResult{{addresses: []netip.Addr{authorizedTestIP}}}}
	dialer := &mappedDialer{actual: "127.0.0.1:1", advertised: authorizedTestIP}
	driver, err := newDriver(DriverConfig{
		Management: drivers.ManagementConfig{
			AllowedDNSNames:        []string{"node.example.invalid"},
			AllowedManagementCIDRs: []string{"10.42.0.0/16"},
		},
		SecretResolver: secretResolver,
	}, dnsResolver, dialer)
	if err != nil {
		t.Fatal(err)
	}
	if secretResolver.calls.Load() != 0 || dnsResolver.callCount() != 0 || dialer.callCount() != 0 {
		t.Fatal("Driver construction performed runtime work")
	}
	target := drivers.NodeTarget{
		InstanceID: uuid.New(), NodeType: drivers.NodeTypeCLIProxyAPI,
		DriverContractVersion: drivers.DriverContractCLIProxyAPIAuthFilesV1,
		ManagementEndpoint:    "https://node.example.invalid",
		ReaderSecretReference: drivers.NewSecretReference("file://unknown/canary"),
		Capabilities:          []drivers.Capability{drivers.CapabilityManagementAccountInventoryRead},
	}
	invalidPolicy := drivers.InventoryRequest{Target: target}
	observation, err := driver.ListAccountInventory(context.Background(), invalidPolicy)
	if err == nil || observation.Reason != drivers.ReasonContractInvalid || secretResolver.calls.Load() != 0 || dnsResolver.callCount() != 0 {
		t.Fatalf("policy ordering = %#v, %v, secret=%d dns=%d", observation, err, secretResolver.calls.Load(), dnsResolver.callCount())
	}
	validPolicy := invalidPolicy
	validPolicy.ProviderPolicy = drivers.ProviderPolicySnapshot{VersionID: uuid.New(), ActiveProviders: []string{"antigravity"}}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	observation, err = driver.ListAccountInventory(cancelled, validPolicy)
	if err == nil || observation.Reason != drivers.ReasonCancelled || secretResolver.calls.Load() != 0 || dnsResolver.callCount() != 0 {
		t.Fatalf("cancel ordering = %#v, %v", observation, err)
	}
	deadline, deadlineCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer deadlineCancel()
	observation, err = driver.ListAccountInventory(deadline, validPolicy)
	if err == nil || observation.Reason != drivers.ReasonTimeout || secretResolver.calls.Load() != 0 || dnsResolver.callCount() != 0 {
		t.Fatalf("deadline ordering = %#v, %v", observation, err)
	}

	observation, err = driver.ListAccountInventory(context.Background(), validPolicy)
	if err == nil || observation.Reason != drivers.ReasonSecretReferenceUnknown || secretResolver.calls.Load() != 1 || dnsResolver.callCount() != 0 || dialer.callCount() != 0 {
		t.Fatalf("secret ordering = %#v, %v, secret=%d dns=%d dial=%d", observation, err, secretResolver.calls.Load(), dnsResolver.callCount(), dialer.callCount())
	}

	missingCapability := target
	missingCapability.Capabilities = []drivers.Capability{drivers.CapabilityManagementHealthRead}
	observation, err = driver.ListAccountInventory(context.Background(), drivers.InventoryRequest{
		Target: missingCapability, ProviderPolicy: validPolicy.ProviderPolicy,
	})
	if err == nil || observation.Reason != drivers.ReasonCapabilityUnsupported || secretResolver.calls.Load() != 1 || dnsResolver.callCount() != 0 {
		t.Fatalf("capability ordering = %#v, %v", observation, err)
	}
}

func TestDriverErrorsAndFormattingDoNotLeakInputs(t *testing.T) {
	canaries := []string{"endpoint-canary", "reference-canary", "email-canary", "raw-error-canary"}
	failure := &DriverError{Operation: drivers.OperationAccountInventory, Reason: drivers.ReasonTLSRejected}
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		projected := fmt.Sprintf(format, failure)
		for _, canary := range canaries {
			if strings.Contains(projected, canary) {
				t.Fatalf("error format %s leaked %q", format, canary)
			}
		}
	}
	if errors.Unwrap(failure) != nil {
		t.Fatal("DriverError unexpectedly unwraps an arbitrary cause")
	}
}

func TestDriverInventoryHTTPFailureDoesNotReadBody(t *testing.T) {
	parsed := parseInventory(http.StatusServiceUnavailable, nil, readerThatFails{}, InventoryParseOptions{})
	if parsed.TransportSuccess || parsed.ResponseShapeValid || parsed.ContractValid || parsed.Reason != InventoryReasonHTTPStatus {
		t.Fatalf("non-200 projection = %#v", parsed)
	}
}

type readerThatFails struct{}

func (readerThatFails) Read([]byte) (int, error) { return 0, errors.New("raw-error-canary") }
