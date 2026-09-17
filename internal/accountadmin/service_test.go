package accountadmin

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

type countingNodeResolver struct {
	mu    sync.Mutex
	state NodeState
	calls int
}

func (r *countingNodeResolver) Resolve(context.Context, uuid.UUID, string) (NodeState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return r.state, nil
}

func (r *countingNodeResolver) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

type fakeOperationStore struct {
	mu                       sync.Mutex
	operation                store.AccountAdminOperation
	terminal                 bool
	admitted                 bool
	dispatchCalls            int
	acceptCalls              int
	acceptWithIntentKeyCalls int
	receipt                  store.AccountCommandReceipt
	replayErr                error
}

func (s *fakeOperationStore) ReplayTerminal(context.Context, store.AccountOperationAcceptance) (store.AccountCommandReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal {
		return s.receipt, nil
	}
	if s.replayErr != nil {
		return store.AccountCommandReceipt{}, s.replayErr
	}
	return store.AccountCommandReceipt{}, store.ErrAccountOperationNotFound
}
func (s *fakeOperationStore) ReplayCurrent(context.Context, store.AccountOperationAcceptance) (store.AccountAdminOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.operation, nil
}
func (s *fakeOperationStore) Operation(context.Context, uuid.UUID) (store.AccountAdminOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.operation, nil
}
func (s *fakeOperationStore) Accept(context.Context, store.AccountOperationAcceptance) (store.AccountAdminOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acceptCalls++
	s.operation.ExecutionState = store.AccountPrepared
	return s.operation, nil
}
func (s *fakeOperationStore) AcceptWithIntentKey(context.Context, string, store.AccountOperationAcceptance, []byte) (store.AccountAdminOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acceptWithIntentKeyCalls++
	s.operation.ExecutionState = store.AccountPrepared
	return s.operation, nil
}
func (s *fakeOperationStore) TerminalizePreDispatchFailure(_ context.Context, _ uuid.UUID, f store.AccountFailure, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operation.ExecutionState, s.operation.RemoteResultCode, s.terminal = store.AccountFailed, &f.Code, true
	return nil
}
func (s *fakeOperationStore) AdmitAccountNoop(context.Context, uuid.UUID, uuid.UUID, string, string) (bool, store.AccountAdminOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operation.ExecutionState, s.terminal = store.AccountRemoteNoop, true
	s.receipt.TargetOperationCommandID = &s.operation.CommandID
	return true, s.operation, nil
}
func (s *fakeOperationStore) AdmitAccountDispatch(context.Context, uuid.UUID, uuid.UUID, string, string) (bool, store.AccountAdminOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.operation.ExecutionState != store.AccountPrepared {
		return false, s.operation, nil
	}
	s.admitted, s.operation.ExecutionState = true, store.AccountDispatched
	s.dispatchCalls++
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
func (s *fakeOperationStore) TerminalizeApplied(context.Context, uuid.UUID, string) (store.AccountAdminOperation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operation.ExecutionState, s.terminal = store.AccountRemoteApplied, true
	s.receipt.TargetOperationCommandID = &s.operation.CommandID
	return s.operation, nil
}
func (s *fakeOperationStore) ApplyLifecycleOverride(context.Context, store.AccountOperationOverride) error {
	return nil
}
func (s *fakeOperationStore) ApplySameAccountOverride(context.Context, store.AccountOperationOverride) error {
	return nil
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

func TestValidateCommandClassifiesRequestedProvider(t *testing.T) {
	command := Command{
		CommandID: uuid.New(), ActorAdminID: uuid.New(), NodeInstanceID: uuid.New(),
		AccountKey: "openai:user@example.invalid", Kind: store.AccountDisable,
		CanonicalIntent: []byte("intent"),
	}
	if err := validateCommand(command); !errors.Is(err, ErrUnsupportedProvider) {
		t.Fatalf("validateCommand error=%v, want ErrUnsupportedProvider", err)
	}
}

func TestExecuteDispatchedReplayDoesNotResolveNode(t *testing.T) {
	commandID, adminID, nodeID := uuid.New(), uuid.New(), uuid.New()
	intent, err := CanonicalIntentV1(store.AccountRemove, nodeID, "antigravity:replay@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	ops := &fakeOperationStore{
		operation: store.AccountAdminOperation{CommandID: commandID, NodeInstanceID: nodeID, AccountKey: "antigravity:replay@example.invalid", OperationKind: store.AccountRemove, ExecutionState: store.AccountDispatched},
		replayErr: store.ErrAccountOperationState,
	}
	resolver := &countingNodeResolver{}
	service, err := NewService(ops, resolver)
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.Execute(context.Background(), Command{CommandID: commandID, ActorAdminID: adminID, NodeInstanceID: nodeID, AccountKey: "antigravity:replay@example.invalid", Kind: store.AccountRemove, CanonicalIntent: intent})
	if err != nil || got.ExecutionState != store.AccountDispatched {
		t.Fatalf("dispatched replay=%#v err=%v", got, err)
	}
	if resolver.Count() != 0 {
		t.Fatalf("dispatched replay resolved Node %d times", resolver.Count())
	}
}

func TestExecuteOutcomeUnknownReplayDoesNotResolveNode(t *testing.T) {
	commandID, adminID, nodeID := uuid.New(), uuid.New(), uuid.New()
	intent, err := CanonicalIntentV1(store.AccountRemove, nodeID, "antigravity:unknown@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	ops := &fakeOperationStore{
		operation: store.AccountAdminOperation{CommandID: commandID, NodeInstanceID: nodeID, AccountKey: "antigravity:unknown@example.invalid", OperationKind: store.AccountRemove, ExecutionState: store.AccountOutcomeUnknown},
		replayErr: store.ErrAccountOperationState,
	}
	resolver := &countingNodeResolver{}
	service, err := NewService(ops, resolver)
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.Execute(context.Background(), Command{CommandID: commandID, ActorAdminID: adminID, NodeInstanceID: nodeID, AccountKey: "antigravity:unknown@example.invalid", Kind: store.AccountRemove, CanonicalIntent: intent})
	if err != nil || got.ExecutionState != store.AccountOutcomeUnknown {
		t.Fatalf("outcome_unknown replay=%#v err=%v", got, err)
	}
	if resolver.Count() != 0 {
		t.Fatalf("outcome_unknown replay resolved Node %d times", resolver.Count())
	}
}

func TestExecutePreparedReplayResumes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CPA-VERSION", cliproxyapi.FrozenRuntimeVersion)
		w.Header().Set("X-CPA-COMMIT", cliproxyapi.FrozenRuntimeCommit)
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `{"files":[{"name":"a.json","provider":"antigravity","email":"resume@example.invalid","source":"file","runtime_only":false,"auth_index":"1","disabled":false}]}`)
			return
		}
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
	intent, err := CanonicalIntentV1(store.AccountRemove, nodeID, "antigravity:resume@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	ops := &fakeOperationStore{operation: store.AccountAdminOperation{CommandID: commandID, NodeInstanceID: nodeID, AccountKey: "antigravity:resume@example.invalid", OperationKind: store.AccountRemove, ExecutionState: store.AccountPrepared}, replayErr: store.ErrAccountOperationState}
	resolver := &countingNodeResolver{state: NodeState{Adapter: adapter, LifecycleActive: true, MonitoringEligible: true, InventoryReadAllowed: true, ProviderPolicyActive: true}}
	service, err := NewService(ops, resolver)
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.Execute(context.Background(), Command{CommandID: commandID, ActorAdminID: adminID, NodeInstanceID: nodeID, AccountKey: "antigravity:resume@example.invalid", Kind: store.AccountRemove, CanonicalIntent: intent})
	if err != nil || got.ExecutionState != store.AccountRemoteApplied {
		t.Fatalf("prepared resume=%#v err=%v", got, err)
	}
	if resolver.Count() != 1 || ops.dispatchCalls != 1 {
		t.Fatalf("resolver=%d dispatch=%d", resolver.Count(), ops.dispatchCalls)
	}
}

func TestExecuteConcurrentPreparedRetriesDispatchOnce(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CPA-VERSION", cliproxyapi.FrozenRuntimeVersion)
		w.Header().Set("X-CPA-COMMIT", cliproxyapi.FrozenRuntimeCommit)
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `{"files":[{"name":"a.json","provider":"antigravity","email":"concurrent@example.invalid","source":"file","runtime_only":false,"auth_index":"1","disabled":false}]}`)
			return
		}
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
	accountKey := "antigravity:concurrent@example.invalid"
	intent, err := CanonicalIntentV1(store.AccountRemove, nodeID, accountKey)
	if err != nil {
		t.Fatal(err)
	}
	ops := &fakeOperationStore{operation: store.AccountAdminOperation{CommandID: commandID, NodeInstanceID: nodeID, AccountKey: accountKey, OperationKind: store.AccountRemove, ExecutionState: store.AccountPrepared}, replayErr: store.ErrAccountOperationState}
	resolver := &countingNodeResolver{state: NodeState{Adapter: adapter, LifecycleActive: true, MonitoringEligible: true, InventoryReadAllowed: true, ProviderPolicyActive: true}}
	service, err := NewService(ops, resolver)
	if err != nil {
		t.Fatal(err)
	}
	command := Command{CommandID: commandID, ActorAdminID: adminID, NodeInstanceID: nodeID, AccountKey: accountKey, Kind: store.AccountRemove, CanonicalIntent: intent}
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := service.Execute(context.Background(), command)
			results <- err
		}()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if ops.dispatchCalls != 1 {
		t.Fatalf("dispatch calls=%d, want 1", ops.dispatchCalls)
	}
}

func TestExecuteConcurrentNonterminalRetriesDoNoNativeWork(t *testing.T) {
	for _, state := range []store.AccountOperationState{store.AccountDispatched, store.AccountOutcomeUnknown} {
		t.Run(string(state), func(t *testing.T) {
			commandID, adminID, nodeID := uuid.New(), uuid.New(), uuid.New()
			accountKey := "antigravity:nonterminal@example.invalid"
			intent, err := CanonicalIntentV1(store.AccountRemove, nodeID, accountKey)
			if err != nil {
				t.Fatal(err)
			}
			ops := &fakeOperationStore{operation: store.AccountAdminOperation{CommandID: commandID, NodeInstanceID: nodeID, AccountKey: accountKey, OperationKind: store.AccountRemove, ExecutionState: state}, replayErr: store.ErrAccountOperationState}
			resolver := &countingNodeResolver{}
			service, err := NewService(ops, resolver)
			if err != nil {
				t.Fatal(err)
			}
			command := Command{CommandID: commandID, ActorAdminID: adminID, NodeInstanceID: nodeID, AccountKey: accountKey, Kind: store.AccountRemove, CanonicalIntent: intent}
			results := make(chan error, 2)
			for range 2 {
				go func() {
					_, err := service.Execute(context.Background(), command)
					results <- err
				}()
			}
			for range 2 {
				if err := <-results; err != nil {
					t.Fatal(err)
				}
			}
			if resolver.Count() != 0 || ops.dispatchCalls != 0 {
				t.Fatalf("resolver=%d dispatch=%d", resolver.Count(), ops.dispatchCalls)
			}
		})
	}
}

func TestUploadNewRejects239ByteEmailBeforeAcceptance(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "intent.key")
	if err := os.WriteFile(keyPath, []byte("01234567890123456789012345678901"), 0600); err != nil {
		t.Fatal(err)
	}
	nodeID := uuid.New()
	email := strings.Repeat("a", 239-len("@example.invalid")) + "@example.invalid"
	accountKey := "antigravity:" + email
	credential := []byte(`{"type":"antigravity","email":"` + email + `"}`)
	commandID, adminID := uuid.New(), uuid.New()
	command := Command{CommandID: commandID, ActorAdminID: adminID, NodeInstanceID: nodeID, AccountKey: accountKey, Kind: store.AccountUploadNew, IntentKeyPath: keyPath, Credential: credential}
	service, err := NewService(&fakeOperationStore{}, &countingNodeResolver{})
	if err != nil {
		t.Fatal(err)
	}
	command.CanonicalIntent, err = service.CanonicalIntentForCommand(command)
	if err != nil {
		t.Fatal(err)
	}
	ops := service.operations.(*fakeOperationStore)
	resolver := service.nodes.(*countingNodeResolver)
	if _, err := service.Execute(context.Background(), command); !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("error=%v, want invalid command", err)
	}
	if ops.acceptCalls != 0 || ops.acceptWithIntentKeyCalls != 0 || resolver.Count() != 0 || ops.dispatchCalls != 0 {
		t.Fatalf("accept=%d accept_with_key=%d resolve=%d dispatch=%d", ops.acceptCalls, ops.acceptWithIntentKeyCalls, resolver.Count(), ops.dispatchCalls)
	}
}

func TestUploadNewRejectsUnsafeGeneratedBasenameBeforeAcceptance(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "intent.key")
	if err := os.WriteFile(keyPath, []byte("01234567890123456789012345678901"), 0600); err != nil {
		t.Fatal(err)
	}
	nodeID := uuid.New()
	email := "a/b@example.invalid"
	accountKey := "antigravity:" + email
	credential := []byte(`{"type":"antigravity","email":"` + email + `"}`)
	command := Command{CommandID: uuid.New(), ActorAdminID: uuid.New(), NodeInstanceID: nodeID, AccountKey: accountKey, Kind: store.AccountUploadNew, IntentKeyPath: keyPath, Credential: credential}
	service, err := NewService(&fakeOperationStore{}, &countingNodeResolver{})
	if err != nil {
		t.Fatal(err)
	}
	command.CanonicalIntent, err = service.CanonicalIntentForCommand(command)
	if err != nil {
		t.Fatal(err)
	}
	ops := service.operations.(*fakeOperationStore)
	resolver := service.nodes.(*countingNodeResolver)
	if _, err := service.Execute(context.Background(), command); !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("error=%v, want invalid command", err)
	}
	if ops.acceptCalls != 0 || ops.acceptWithIntentKeyCalls != 0 || resolver.Count() != 0 || ops.dispatchCalls != 0 {
		t.Fatalf("accept=%d accept_with_key=%d resolve=%d dispatch=%d", ops.acceptCalls, ops.acceptWithIntentKeyCalls, resolver.Count(), ops.dispatchCalls)
	}
}
