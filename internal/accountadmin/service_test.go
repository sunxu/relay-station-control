package accountadmin

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/sunxu/relay-station-control/internal/drivers"
	"github.com/sunxu/relay-station-control/internal/drivers/cliproxyapi"
	"github.com/sunxu/relay-station-control/internal/store"
)

type fakeNodeResolver struct{ state NodeState }

func (r fakeNodeResolver) Resolve(context.Context, uuid.UUID, string) (NodeState, error) {
	return r.state, nil
}

type fakeOperationStore struct {
	mu        sync.Mutex
	operation store.AccountAdminOperation
	terminal  bool
	admitted  bool
	receipt   store.AccountCommandReceipt
}

func (s *fakeOperationStore) ReplayTerminal(context.Context, store.AccountOperationAcceptance) (store.AccountCommandReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal {
		return s.receipt, nil
	}
	return store.AccountCommandReceipt{}, store.ErrCommandConflict
}
func (s *fakeOperationStore) Operation(context.Context, uuid.UUID) (store.AccountAdminOperation, error) {
	return s.operation, nil
}
func (s *fakeOperationStore) Accept(context.Context, store.AccountOperationAcceptance) (store.AccountAdminOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operation.ExecutionState = store.AccountPrepared
	return s.operation, nil
}
func (s *fakeOperationStore) AcceptWithIntentKey(ctx context.Context, _ string, c store.AccountOperationAcceptance, _ []byte) (store.AccountAdminOperation, error) {
	return s.Accept(ctx, c)
}
func (s *fakeOperationStore) TerminalizePreDispatchFailure(_ context.Context, _ uuid.UUID, f store.AccountFailure, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operation.ExecutionState, s.operation.RemoteResultCode, s.terminal = store.AccountFailed, &f.Code, true
	return nil
}
func (s *fakeOperationStore) TerminalizeNoop(context.Context, uuid.UUID, string) (store.AccountAdminOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operation.ExecutionState, s.terminal = store.AccountRemoteNoop, true
	return s.operation, nil
}
func (s *fakeOperationStore) AdmitAccountDispatch(context.Context, uuid.UUID, uuid.UUID, string, string) (bool, store.AccountAdminOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.admitted, s.operation.ExecutionState = true, store.AccountDispatched
	return true, s.operation, nil
}
func (s *fakeOperationStore) TransitionAccountOperation(_ context.Context, _ uuid.UUID, _, to store.AccountOperationState) (store.AccountAdminOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operation.ExecutionState, s.terminal = to, to == store.AccountRemoteApplied || to == store.AccountOutcomeUnknown
	return s.operation, nil
}
func (s *fakeOperationStore) TerminalizeDispatchedFailure(context.Context, uuid.UUID, store.AccountFailure, string) (store.AccountAdminOperation, error) {
	return s.operation, nil
}

func TestExecuteAdmitsBeforeOneNativeMutation(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CPA-VERSION", cliproxyapi.FrozenRuntimeVersion)
		w.Header().Set("X-CPA-COMMIT", cliproxyapi.FrozenRuntimeCommit)
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `{"files":[{"name":"a.json","provider":"antigravity","email":"a@example.invalid","source":"file","runtime_only":false,"auth_index":"1","disabled":false}]}`)
			return
		}
		methods = append(methods, r.Method+" "+r.URL.RequestURI())
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	config, err := (drivers.ManagementConfig{}).Validate()
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := cliproxyapi.NewNativeAdapter(server.URL, config, "synthetic-management-key")
	if err != nil {
		t.Fatal(err)
	}
	commandID, adminID, nodeID := uuid.New(), uuid.New(), uuid.New()
	ops := &fakeOperationStore{operation: store.AccountAdminOperation{CommandID: commandID, NodeInstanceID: nodeID, AccountKey: "antigravity:a@example.invalid", OperationKind: store.AccountRemove}}
	service, err := NewService(ops, fakeNodeResolver{state: NodeState{Adapter: adapter, LifecycleActive: true, MonitoringEligible: true, InventoryReadAllowed: true, ProviderPolicyActive: true}})
	if err != nil {
		t.Fatal(err)
	}
	intent, err := CanonicalIntentV1(store.AccountRemove, nodeID, "antigravity:a@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Execute(context.Background(), Command{CommandID: commandID, ActorAdminID: adminID, NodeInstanceID: nodeID, AccountKey: "antigravity:a@example.invalid", Kind: store.AccountRemove, CanonicalIntent: intent, RequestID: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	if !ops.admitted || len(methods) != 1 || methods[0] != "DELETE /v0/management/auth-files?name=a.json" {
		t.Fatalf("admitted=%v methods=%v", ops.admitted, methods)
	}
}

func TestExecuteTerminalizesFreshTargetFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CPA-VERSION", cliproxyapi.FrozenRuntimeVersion)
		w.Header().Set("X-CPA-COMMIT", cliproxyapi.FrozenRuntimeCommit)
		_, _ = io.WriteString(w, `{"files":[]}`)
	}))
	defer server.Close()
	config, err := (drivers.ManagementConfig{}).Validate()
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := cliproxyapi.NewNativeAdapter(server.URL, config, "synthetic-management-key")
	if err != nil {
		t.Fatal(err)
	}
	commandID, adminID, nodeID := uuid.New(), uuid.New(), uuid.New()
	ops := &fakeOperationStore{operation: store.AccountAdminOperation{CommandID: commandID, NodeInstanceID: nodeID, AccountKey: "antigravity:missing@example.invalid", OperationKind: store.AccountRemove}}
	service, err := NewService(ops, fakeNodeResolver{state: NodeState{Adapter: adapter, LifecycleActive: true, MonitoringEligible: true, InventoryReadAllowed: true, ProviderPolicyActive: true}})
	if err != nil {
		t.Fatal(err)
	}
	intent, err := CanonicalIntentV1(store.AccountRemove, nodeID, "antigravity:missing@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Execute(context.Background(), Command{CommandID: commandID, ActorAdminID: adminID, NodeInstanceID: nodeID, AccountKey: "antigravity:missing@example.invalid", Kind: store.AccountRemove, CanonicalIntent: intent})
	if err != nil && !errors.Is(err, store.ErrAccountOperationNotFound) {
		t.Fatal(err)
	}
	if !ops.terminal || ops.admitted {
		t.Fatalf("terminal=%v admitted=%v", ops.terminal, ops.admitted)
	}
}
