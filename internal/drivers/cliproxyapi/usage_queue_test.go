package cliproxyapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sunxu/relay-station-control/internal/drivers"
)

func TestUsageQueueTransportContract(t *testing.T) {
	cases := []struct {
		name, body string
		want       int
		wantErr    bool
	}{
		{"empty", `[]`, 0, false},
		{"object", `[ {"request_id":"r1"} ]`, 1, false},
		{"string", `["{\"request_id\":\"r1\"}"]`, 1, false},
		{"null", `[null,{"request_id":"r1"}]`, 1, false},
		{"malformed-item-is-preserved", `["not-json"]`, 1, false},
		{"trailing-json", `[] []`, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/base/v0/management/usage-queue" || r.URL.Query().Get("count") != "100" {
					t.Errorf("request shape: %s %s", r.Method, r.URL.String())
				}
				if got := r.Header.Get("Authorization"); got != "Bearer queue-key" {
					t.Errorf("authorization=%q", got)
				}
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			driver := queueTestDriver(t, server.URL)
			target := drivers.NodeTarget{InstanceID: uuid.New(), NodeType: drivers.NodeTypeCLIProxyAPI, DriverContractVersion: drivers.DriverContractCLIProxyAPIAuthFilesV1, ManagementEndpoint: server.URL + "/base", ReaderSecretReference: drivers.NewSecretReference("file://queue-test"), Capabilities: []drivers.Capability{drivers.CapabilityManagementAccountInventoryRead}}
			items, err := driver.PopUsage(context.Background(), target)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%t", err, tc.wantErr)
			}
			if err == nil && len(items) != tc.want {
				t.Fatalf("items=%d want %d", len(items), tc.want)
			}
			if tc.name == "malformed-item-is-preserved" && string(items[0]) != "not-json" {
				t.Fatalf("item=%q", items[0])
			}
		})
	}
}

func queueTestDriver(t *testing.T, endpoint string) *Driver {
	t.Helper()
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "key")
	if err := os.WriteFile(secretPath, []byte("queue-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mapping, _ := json.Marshal(map[string]any{"provider": "file", "references": []map[string]string{{"reference": "file://queue-test", "path": secretPath}}})
	mappingPath := filepath.Join(dir, "mapping.json")
	if err := os.WriteFile(mappingPath, mapping, 0o600); err != nil {
		t.Fatal(err)
	}
	resolver, err := drivers.NewFileSecretResolver(drivers.FileSecretResolverConfig{MappingFile: mappingPath})
	if err != nil {
		t.Fatal(err)
	}
	driver, err := NewDriver(DriverConfig{Management: drivers.ManagementConfig{}, SecretResolver: resolver})
	if err != nil {
		t.Fatal(err)
	}
	return driver
}

func TestUsageQueueTransportRejectsStatusAndBodyLimit(t *testing.T) {
	statusServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) }))
	defer statusServer.Close()
	statusResolver := &sequenceResolver{results: []resolverResult{{addresses: []netip.Addr{authorizedTestIP}}}}
	statusDialer := &mappedDialer{actual: statusServer.Listener.Addr().String(), advertised: authorizedTestIP}
	statusTransport := newHTTPTestTransport(t, statusServer.Listener.Addr().String(), "", statusResolver, statusDialer)
	statusResponse, err := statusTransport.getUsageQueue(context.Background(), "queue-key", 100)
	if err != nil {
		t.Fatal(err)
	}
	if statusResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d", statusResponse.StatusCode)
	}
	_ = statusResponse.Body.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", 256))
	}))
	defer server.Close()
	resolver := &sequenceResolver{results: []resolverResult{{addresses: []netip.Addr{authorizedTestIP}}}}
	dialer := &mappedDialer{actual: server.Listener.Addr().String(), advertised: authorizedTestIP}
	transport := newHTTPTestTransport(t, server.Listener.Addr().String(), "", resolver, dialer)
	transport.inventoryLimit = 32
	response, err := transport.getUsageQueue(context.Background(), "queue-key", 100)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if len(body) != 33 {
		t.Fatalf("bounded body length=%d, want 33", len(body))
	}
}

func TestCurrentIdentitiesHTTPProjection(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		maxBytes   int64
		wantErr    bool
	}{
		{"explicit-only", `{"files":[{"auth_index":"same","provider":"openai","email":"one@example.invalid"},{"auth_index":"same","provider":"openai","email":"two@example.invalid"},{"auth_index":"no-email","provider":"openai","account_snapshot":"fallback@example.invalid","name":"file@example.invalid"}]}`, 0, false},
		{"trailing", `{"files":[]} {}`, 0, true},
		{"missing-files", `{}`, 0, true},
		{"too-large", `{"files":[{"auth_index":"oversized"}]}`, 16, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v0/management/auth-files" || r.Header.Get("X-Management-Key") != "queue-key" {
					t.Error("wrong identity request")
				}
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			driver := queueTestDriver(t, server.URL)
			if tc.maxBytes > 0 {
				driver.inventoryLimits.MaxBodyBytes = tc.maxBytes
			}
			target := drivers.NodeTarget{InstanceID: uuid.New(), NodeType: drivers.NodeTypeCLIProxyAPI, DriverContractVersion: drivers.DriverContractCLIProxyAPIAuthFilesV1, ManagementEndpoint: server.URL, ReaderSecretReference: drivers.NewSecretReference("file://queue-test"), Capabilities: []drivers.Capability{drivers.CapabilityManagementAccountInventoryRead}}
			got, err := driver.CurrentIdentities(context.Background(), target)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantError=%v", err, tc.wantErr)
			}
			if !tc.wantErr && (len(got) != 3 || got[0].AuthIndex != "same" || got[1].AuthIndex != "same" || got[0].Email == got[1].Email || got[2].Email != "") {
				t.Fatalf("identity evidence lost or fallback introduced: %+v", got)
			}
		})
	}
}

func TestUsageQueueDriverCancellationAndRejections(t *testing.T) {
	for _, status := range []int{404, 405, 501, 503, 200} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, strings.Repeat(" ", 256)+"[]")
			}))
			defer server.Close()
			driver := queueTestDriver(t, server.URL)
			driver.inventoryLimits.MaxBodyBytes = 64
			target := drivers.NodeTarget{InstanceID: uuid.New(), NodeType: drivers.NodeTypeCLIProxyAPI, DriverContractVersion: drivers.DriverContractCLIProxyAPIAuthFilesV1, ManagementEndpoint: server.URL, ReaderSecretReference: drivers.NewSecretReference("file://queue-test"), Capabilities: []drivers.Capability{drivers.CapabilityManagementAccountInventoryRead}}
			if _, err := driver.PopUsage(context.Background(), target); err == nil {
				t.Fatal("status/oversize accepted")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := driver.PopUsage(ctx, target); err == nil {
				t.Fatal("cancelled request succeeded")
			}
		})
	}
}

func TestUsageQueueInFlightCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer server.Close()
	driver := queueTestDriver(t, server.URL)
	target := drivers.NodeTarget{InstanceID: uuid.New(), NodeType: drivers.NodeTypeCLIProxyAPI, DriverContractVersion: drivers.DriverContractCLIProxyAPIAuthFilesV1, ManagementEndpoint: server.URL, ReaderSecretReference: drivers.NewSecretReference("file://queue-test"), Capabilities: []drivers.Capability{drivers.CapabilityManagementAccountInventoryRead}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := driver.PopUsage(ctx, target); done <- err }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("HTTP request not started")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled HTTP returned success")
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP cancellation not propagated")
	}
}
