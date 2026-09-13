package cliproxyapi

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sunxu/relay-station-control/internal/drivers"
	controlpoll "github.com/sunxu/relay-station-control/internal/inventorypoll"
)

func TestDriverToWorkerPreservesProviderLocalCompletenessAndGlobalCounts(t *testing.T) {
	tests := []struct {
		name               string
		activeProviders    []string
		outOfScope         []string
		body               string
		assertFinalization func(*testing.T, controlpoll.FinalizeRequest)
	}{
		{
			name:            "unsupported and out of scope degrade node without blocking active provider",
			activeProviders: []string{"antigravity"}, outOfScope: []string{"codex"},
			body: `{"files":[` +
				`{"provider":"antigravity","email":"active@example.invalid","source":"memory","status":"active"},` +
				`{"provider":"codex","email":"out@example.invalid","source":"memory","status":"active"},` +
				`{"provider":"future","email":"unsupported@example.invalid","source":"memory","status":"active"}` +
				`]}`,
			assertFinalization: func(t *testing.T, request controlpoll.FinalizeRequest) {
				t.Helper()
				if !request.Node.TransportSuccess || !request.Node.ContractValid || request.Node.InventoryMode != drivers.InventoryModeRuntime ||
					!request.Node.SnapshotComplete || !request.Node.Degraded || request.Node.UnsupportedProviderCount != 1 ||
					request.Node.OutOfScopeProviderCount != 1 || request.Node.UnidentifiedRecordCount != 0 ||
					len(request.Providers) != 1 || !request.Providers[0].IdentityComplete || !request.Providers[0].SnapshotComplete ||
					len(request.SnapshotItems) != 1 || request.SnapshotItems[0].Provider != "antigravity" || len(request.Duplicates) != 0 {
					t.Fatalf("unexpected unsupported/out-of-scope projection: node=%#v providers=%#v item_count=%d duplicate_count=%d",
						request.Node, request.Providers, len(request.SnapshotItems), len(request.Duplicates))
				}
			},
		},
		{
			name:            "global missing identity is counted once and provider completeness stays local",
			activeProviders: []string{"antigravity", "codex"},
			body: `{"files":[` +
				`{"email":"orphan@example.invalid","source":"memory","status":"active"},` +
				`{"provider":"antigravity","source":"memory","status":"active"},` +
				`{"provider":"antigravity","email":"active@example.invalid","source":"memory","status":"active"},` +
				`{"provider":"codex","email":"local@example.invalid","source":"memory","status":"active"}` +
				`]}`,
			assertFinalization: func(t *testing.T, request controlpoll.FinalizeRequest) {
				t.Helper()
				if request.Node.NodeIdentityComplete || request.Node.SnapshotComplete || !request.Node.Degraded ||
					request.Node.UnidentifiedRecordCount != 2 || request.Node.UnsupportedProviderCount != 0 ||
					request.Node.OutOfScopeProviderCount != 0 || len(request.Providers) != 2 {
					t.Fatalf("unexpected global identity projection: node=%#v providers=%#v", request.Node, request.Providers)
				}
				antigravity, codex := request.Providers[0], request.Providers[1]
				if antigravity.Provider != "antigravity" || antigravity.RecognizedRecordCount != 1 ||
					antigravity.MissingIdentityCount != 1 || antigravity.IdentityComplete ||
					antigravity.SnapshotComplete || codex.Provider != "codex" || codex.MissingIdentityCount != 0 ||
					codex.RecognizedRecordCount != 1 || !codex.IdentityComplete || codex.SnapshotComplete ||
					antigravity.Reason != controlpoll.ProviderReasonNodeIdentityIncomplete ||
					codex.Reason != controlpoll.ProviderReasonNodeIdentityIncomplete || len(request.SnapshotItems) != 2 ||
					request.SnapshotItems[0].Provider != "antigravity" || request.SnapshotItems[1].Provider != "codex" ||
					len(request.Duplicates) != 0 {
					t.Fatalf("provider-local identity semantics changed: providers=%#v item_count=%d duplicate_count=%d",
						request.Providers, len(request.SnapshotItems), len(request.Duplicates))
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearProxyEnvironment(t)
			driver, target, requests := syntheticWorkerDriver(t, test.body)
			policyID := uuid.New()
			repository := &workerProjectionRepository{claim: &controlpoll.ClaimedRun{
				PollRunID: uuid.New(), InstanceID: target.InstanceID, PolicyVersionID: policyID,
				ScheduledAt: time.Unix(1_800, 0).UTC(), Attempt: 1, MaxAttempts: 2,
				GraceRemaining: 5 * time.Second, Target: target,
				ProviderPolicy: drivers.ProviderPolicySnapshot{
					VersionID: policyID, ActiveProviders: test.activeProviders, OutOfScopeProviders: test.outOfScope,
				},
			}}
			worker, err := controlpoll.NewWorker(repository, driver, workerProjectionConfig())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- worker.Run(ctx) }()
			finalize := repository.waitForFinalize(t)
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if requests.Load() != 1 {
				t.Fatalf("management request count=%d, want 1", requests.Load())
			}
			test.assertFinalization(t, finalize)
		})
	}
}

type workerProjectionRepository struct {
	mu        sync.Mutex
	claim     *controlpoll.ClaimedRun
	finalizes []controlpoll.FinalizeRequest
}

func (*workerProjectionRepository) ScheduleCurrent(context.Context, controlpoll.ScheduleRequest) (controlpoll.ScheduleResult, error) {
	return controlpoll.ScheduleResult{}, nil
}

func (*workerProjectionRepository) AuthorizeDispatch(context.Context, controlpoll.DispatchAuthorizationRequest) (controlpoll.DispatchAuthorization, error) {
	return controlpoll.DispatchAuthorization{LeaseRemaining: time.Minute, GraceRemaining: time.Minute}, nil
}

func (repository *workerProjectionRepository) ClaimRunnable(_ context.Context, request controlpoll.ClaimRequest) (*controlpoll.ClaimedRun, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.claim == nil {
		return nil, controlpoll.ErrNoWork
	}
	claim := *repository.claim
	repository.claim = nil
	claim.FencingToken = request.Token
	return &claim, nil
}

func (repository *workerProjectionRepository) FinalizeFenced(_ context.Context, request controlpoll.FinalizeRequest) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.finalizes = append(repository.finalizes, request)
	return nil
}

func (*workerProjectionRepository) ReconcileExpired(context.Context, controlpoll.ReconcileRequest) (controlpoll.ReconcileResult, error) {
	return controlpoll.ReconcileResult{}, nil
}

func (repository *workerProjectionRepository) waitForFinalize(t *testing.T) controlpoll.FinalizeRequest {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		repository.mu.Lock()
		if len(repository.finalizes) == 1 {
			request := repository.finalizes[0]
			repository.mu.Unlock()
			return request
		}
		repository.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("worker did not finalize synthetic Driver observation")
	return controlpoll.FinalizeRequest{}
}

func workerProjectionConfig() controlpoll.Config {
	return controlpoll.Config{
		PollStartGrace: 40 * time.Second, Concurrency: 1,
		WorstCasePollDuration: time.Second, LeaseDuration: 3 * time.Second,
		DispatchMargin: time.Second, FinalizeMargin: time.Second,
		SchedulerInterval: 10 * time.Millisecond, WorkerScanInterval: 10 * time.Millisecond,
		ReconcileInterval: 20 * time.Millisecond, DatabaseBackoffInitial: 10 * time.Millisecond,
		DatabaseBackoffMaximum: 40 * time.Millisecond, ShutdownGrace: 200 * time.Millisecond,
		ReconcileLimit: 10,
	}
}

func syntheticWorkerDriver(t *testing.T, body string) (*Driver, drivers.NodeTarget, *atomic.Int32) {
	t.Helper()
	requests := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Method != http.MethodGet || request.URL.Path != "/v0/management/auth-files" || request.URL.RawQuery != "" ||
			request.Body != http.NoBody || request.Header.Get("X-Management-Key") != "synthetic-management-key" {
			t.Error("unexpected synthetic management request shape")
		}
		response.Header().Set("X-CPA-VERSION", "v7.2.141")
		response.Header().Set("X-CPA-COMMIT", "abcdef1")
		_, _ = io.WriteString(response, body)
	}))
	t.Cleanup(server.Close)

	directory := t.TempDir()
	secretPath := filepath.Join(directory, "management-key")
	if err := os.WriteFile(secretPath, []byte("synthetic-management-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mappingPath := filepath.Join(directory, "mapping.json")
	mapping, err := json.Marshal(map[string]any{
		"provider":   "file",
		"references": []map[string]string{{"reference": "file://snapshot/worker", "path": secretPath}},
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
	dialer := &mappedDialer{actual: server.Listener.Addr().String(), advertised: authorizedTestIP}
	driver, err := newDriver(DriverConfig{
		Management: drivers.ManagementConfig{
			AllowedDNSNames: []string{"node.example.invalid"}, AllowedManagementCIDRs: []string{"10.42.0.0/16"},
			AllowedPlainHTTPCIDRs: []string{"10.42.0.0/24"},
		},
		SecretResolver: secretResolver,
		Now:            func() time.Time { return time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC) },
	}, dialer)
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
		ManagementEndpoint:    "http://node.example.invalid:" + port,
		ReaderSecretReference: drivers.NewSecretReference("file://snapshot/worker"),
		Capabilities:          []drivers.Capability{drivers.CapabilityManagementAccountInventoryRead},
	}
	return driver, target, requests
}

func clearProxyEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(name, "")
	}
	t.Setenv("NO_PROXY", "*")
	t.Setenv("no_proxy", "*")
}
