// Package accountadmin contains the synchronous, request-driven composition
// of the durable account operation store and the native CLIProxyAPI adapter.
// It intentionally owns no queue, retry loop, workflow, or observation state.
package accountadmin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/sunxu/relay-station-control/internal/drivers/cliproxyapi"
	"github.com/sunxu/relay-station-control/internal/store"
)

var (
	ErrInvalidCommand      = errors.New("account admin: invalid command")
	ErrUnsupportedProvider = errors.New("account admin: unsupported provider")
	ErrNodeNotFound        = errors.New("account admin: node not found")
)

// NodeState is the small, read-only result needed by this composition. The
// adapter is constructed by the existing Node/Secret boundary; this service
// never reads or stores a management key.
type NodeState struct {
	Adapter              *cliproxyapi.NativeAdapter
	LifecycleActive      bool
	MonitoringEligible   bool
	InventoryReadAllowed bool
	ProviderPolicyActive bool
}

type NodeResolver interface {
	Resolve(context.Context, uuid.UUID, string) (NodeState, error)
}

type operationStore interface {
	ReplayTerminal(context.Context, store.AccountOperationAcceptance) (store.AccountCommandReceipt, error)
	ReplayCurrent(context.Context, store.AccountOperationAcceptance) (store.AccountAdminOperation, error)
	Operation(context.Context, uuid.UUID) (store.AccountAdminOperation, error)
	Accept(context.Context, store.AccountOperationAcceptance) (store.AccountAdminOperation, error)
	AcceptWithIntentKey(context.Context, string, store.AccountOperationAcceptance, []byte) (store.AccountAdminOperation, error)
	TerminalizePreDispatchFailure(context.Context, uuid.UUID, store.AccountFailure, string) error
	AdmitAccountNoop(context.Context, uuid.UUID, uuid.UUID, string, string) (bool, store.AccountAdminOperation, error)
	AdmitAccountDispatch(context.Context, uuid.UUID, uuid.UUID, string, string) (bool, store.AccountAdminOperation, error)
	TransitionAccountOperation(context.Context, uuid.UUID, store.AccountOperationState, store.AccountOperationState) (store.AccountAdminOperation, error)
	TerminalizeDispatchedFailure(context.Context, uuid.UUID, store.AccountFailure, string) (store.AccountAdminOperation, error)
	TerminalizeApplied(context.Context, uuid.UUID, string) (store.AccountAdminOperation, error)
	ApplyLifecycleOverride(context.Context, store.AccountOperationOverride) error
	ApplySameAccountOverride(context.Context, store.AccountOperationOverride) error
}

type Command struct {
	CommandID       uuid.UUID
	ActorAdminID    uuid.UUID
	NodeInstanceID  uuid.UUID
	AccountKey      string
	Kind            store.AccountOperationKind
	CanonicalIntent []byte
	IntentKeyPath   string
	Credential      []byte
	RequestID       string
}

type OverrideCommand struct {
	CommandID, ActorAdminID, TargetOperation uuid.UUID
	Reason, Detail, Confirmation, RequestID  string
	DetailPresent                            bool
}

type Service struct {
	operations     operationStore
	nodes          NodeResolver
	intentKeyPath  string
	intentKeyBound bool
}

func NewService(operations operationStore, nodes NodeResolver) (*Service, error) {
	return newService(operations, nodes, "", false)
}

// NewServiceWithIntentKeyPath binds the deployment-controlled upload intent
// key path. Production composition uses this constructor so request data
// cannot select a different key file; the empty-path constructor remains for
// focused domain tests and callers that provide a validated test path.
func NewServiceWithIntentKeyPath(operations operationStore, nodes NodeResolver, intentKeyPath string) (*Service, error) {
	return newService(operations, nodes, intentKeyPath, true)
}

// CanonicalIntentForCommand prepares the frozen command identity bytes for an
// API caller. Upload fingerprints are calculated inside the service boundary;
// the credential is never returned or persisted by this helper.
func (s *Service) CanonicalIntentForCommand(command Command) ([]byte, error) {
	if err := validateCommandRequest(command); err != nil {
		return nil, err
	}
	if command.Kind == store.AccountUploadNew || command.Kind == store.AccountReplaceExisting {
		path := command.IntentKeyPath
		if s.intentKeyBound {
			path = s.intentKeyPath
		}
		_, fingerprint, err := store.AccountUploadIntentFingerprint(path, command.Credential)
		if err != nil {
			return nil, err
		}
		return json.Marshal([]any{"account-intent-v1", "account." + string(command.Kind), command.NodeInstanceID.String(), command.AccountKey, 1, hex.EncodeToString(fingerprint)})
	}
	return canonicalNonUploadIntent(command)
}

func newService(operations operationStore, nodes NodeResolver, intentKeyPath string, intentKeyBound bool) (*Service, error) {
	if operations == nil || nodes == nil {
		return nil, errors.New("account admin: missing dependency")
	}
	return &Service{operations: operations, nodes: nodes, intentKeyPath: intentKeyPath, intentKeyBound: intentKeyBound}, nil
}

func (s *Service) LifecycleOverride(ctx context.Context, command OverrideCommand) (store.AccountAdminOperation, error) {
	return s.applyOverride(ctx, command, store.AccountLifecycleOverride, "OVERRIDE UNKNOWN OPERATION LIFECYCLE BLOCK", true)
}

func (s *Service) SameAccountOverride(ctx context.Context, command OverrideCommand) (store.AccountAdminOperation, error) {
	return s.applyOverride(ctx, command, store.AccountSameAccountOverride, "OVERRIDE UNKNOWN OPERATION SAME-ACCOUNT BLOCK", false)
}

func (s *Service) applyOverride(ctx context.Context, command OverrideCommand, kind store.AccountOperationKind, confirmation string, lifecycle bool) (store.AccountAdminOperation, error) {
	detailPresent := command.DetailPresent || command.Detail != ""
	if command.CommandID == uuid.Nil || command.ActorAdminID == uuid.Nil || command.TargetOperation == uuid.Nil || command.Reason == "" || command.Confirmation != confirmation || (detailPresent && !validOverrideDetail(command.Detail)) || (command.Reason != "process_restarted" && command.Reason != "node_stopped" && command.Reason != "risk_accepted") {
		return store.AccountAdminOperation{}, ErrInvalidCommand
	}
	intent, err := CanonicalOverrideIntentV1(kind, command.TargetOperation, command.Reason, command.Detail)
	if err != nil {
		return store.AccountAdminOperation{}, err
	}
	acceptance := store.AccountOperationAcceptance{CommandID: command.CommandID, ActorAdminID: command.ActorAdminID, OperationKind: kind, NodeInstanceID: command.TargetOperation, AccountKey: command.Reason, CanonicalIntentHash: hashIntent(intent)}
	if receipt, replayErr := s.operations.ReplayTerminal(ctx, acceptance); replayErr == nil {
		if receipt.TargetOperationCommandID == nil {
			return store.AccountAdminOperation{}, nil
		}
		return s.operations.Operation(ctx, *receipt.TargetOperationCommandID)
	} else if !errors.Is(replayErr, store.ErrAccountOperationNotFound) && !errors.Is(replayErr, store.ErrAccountOperationState) {
		return store.AccountAdminOperation{}, replayErr
	}
	override := store.AccountOperationOverride{CommandID: command.CommandID, ActorAdminID: command.ActorAdminID, TargetOperation: command.TargetOperation, Reason: command.Reason, Detail: command.Detail, RequestID: command.RequestID}
	if lifecycle {
		err = s.operations.ApplyLifecycleOverride(ctx, override)
	} else {
		err = s.operations.ApplySameAccountOverride(ctx, override)
	}
	if err != nil {
		return store.AccountAdminOperation{}, err
	}
	return s.operations.Operation(ctx, command.TargetOperation)
}

// Execute performs one synchronous account command. A terminal receipt is
// checked before node, provider, snapshot, or native re-evaluation.
func (s *Service) Execute(ctx context.Context, command Command) (store.AccountAdminOperation, error) {
	if err := validateCommand(command); err != nil {
		return store.AccountAdminOperation{}, err
	}
	acceptance := store.AccountOperationAcceptance{
		CommandID: command.CommandID, ActorAdminID: command.ActorAdminID,
		OperationKind: command.Kind, NodeInstanceID: command.NodeInstanceID,
		AccountKey: command.AccountKey, CanonicalIntentHash: hashIntent(command.CanonicalIntent),
	}
	if command.Kind == store.AccountUploadNew || command.Kind == store.AccountReplaceExisting {
		intentKeyPath := command.IntentKeyPath
		if s.intentKeyBound {
			intentKeyPath = s.intentKeyPath
		}
		version, fingerprint, err := store.AccountUploadIntentFingerprint(intentKeyPath, command.Credential)
		if err != nil {
			return store.AccountAdminOperation{}, err
		}
		acceptance.SecretFingerprintVersion = &version
		acceptance.UploadIntentFingerprint = fingerprint
	}
	resumePrepared := false
	freshAcceptance := false
	var operation store.AccountAdminOperation
	if receipt, err := s.operations.ReplayTerminal(ctx, acceptance); err == nil {
		if receipt.TargetOperationCommandID == nil {
			return store.AccountAdminOperation{}, nil
		}
		return s.operations.Operation(ctx, *receipt.TargetOperationCommandID)
	} else {
		switch {
		case errors.Is(err, store.ErrAccountOperationNotFound):
			freshAcceptance = true
		case errors.Is(err, store.ErrCommandConflict):
			return store.AccountAdminOperation{}, err
		case errors.Is(err, store.ErrAccountOperationState):
			operation, err = s.operations.ReplayCurrent(ctx, acceptance)
			if err != nil {
				return store.AccountAdminOperation{}, err
			}
			switch operation.ExecutionState {
			case store.AccountDispatched, store.AccountOutcomeUnknown:
				return operation, nil
			case store.AccountPrepared:
				resumePrepared = true
			default:
				return store.AccountAdminOperation{}, store.ErrAccountOperationState
			}
		default:
			return store.AccountAdminOperation{}, err
		}
	}
	if freshAcceptance && command.Kind == store.AccountUploadNew {
		provider, email, _ := strings.Cut(command.AccountKey, ":")
		if !cliproxyapi.ValidUploadNewIdentity(provider, email) {
			return store.AccountAdminOperation{}, ErrInvalidCommand
		}
	}
	if err := validateCanonicalIntent(command, acceptance.UploadIntentFingerprint); err != nil {
		return store.AccountAdminOperation{}, err
	}
	provider, _, _ := strings.Cut(command.AccountKey, ":")
	node, err := s.nodes.Resolve(ctx, command.NodeInstanceID, provider)
	if err != nil {
		return store.AccountAdminOperation{}, ErrNodeNotFound
	}
	if !resumePrepared && (command.Kind == store.AccountUploadNew || command.Kind == store.AccountReplaceExisting) {
		intentKeyPath := command.IntentKeyPath
		if s.intentKeyBound {
			intentKeyPath = s.intentKeyPath
		}
		operation, err = s.operations.AcceptWithIntentKey(ctx, intentKeyPath, acceptance, command.Credential)
	} else if !resumePrepared {
		operation, err = s.operations.Accept(ctx, acceptance)
	}
	if err != nil {
		return store.AccountAdminOperation{}, err
	}
	fail := func(code string) (store.AccountAdminOperation, error) {
		failure, failureErr := store.NewAccountFailure(code, store.AccountPreDispatchPostAccept)
		if failureErr != nil {
			return store.AccountAdminOperation{}, failureErr
		}
		if failureErr = s.operations.TerminalizePreDispatchFailure(ctx, operation.CommandID, failure, command.RequestID); failureErr != nil {
			return store.AccountAdminOperation{}, failureErr
		}
		return s.operations.Operation(ctx, operation.CommandID)
	}
	if !node.LifecycleActive {
		return fail("node_retired")
	}
	if !node.MonitoringEligible {
		return fail("node_monitoring_ineligible")
	}
	if !node.InventoryReadAllowed {
		return fail("node_management_unavailable")
	}
	if !node.ProviderPolicyActive {
		return fail("unsupported_provider")
	}
	if node.Adapter == nil {
		return fail("node_management_unavailable")
	}
	mutation, prepareErr := prepareMutation(node.Adapter, ctx, command)
	if prepareErr != nil {
		return fail(nativeFailureCode(prepareErr))
	}
	if mutation.Noop() {
		_, operation, err = s.operations.AdmitAccountNoop(ctx, operation.CommandID, command.NodeInstanceID, command.AccountKey, command.RequestID)
		return operation, err
	}
	admitted, operation, err := s.operations.AdmitAccountDispatch(ctx, operation.CommandID, command.NodeInstanceID, command.AccountKey, command.RequestID)
	if err != nil || !admitted {
		return operation, err
	}
	outcome, dispatchErr := mutation.Dispatch(ctx)
	switch outcome.Kind {
	case cliproxyapi.NativeOutcomeApplied:
		return s.operations.TerminalizeApplied(ctx, operation.CommandID, command.RequestID)
	case cliproxyapi.NativeOutcomeUnknown:
		return s.operations.TransitionAccountOperation(ctx, operation.CommandID, store.AccountDispatched, store.AccountOutcomeUnknown)
	case cliproxyapi.NativeOutcomeFailed:
		failure, failureErr := store.NewAccountFailure(string(outcome.FailureCode), store.AccountDispatchedPhase)
		if failureErr != nil {
			return store.AccountAdminOperation{}, failureErr
		}
		return s.operations.TerminalizeDispatchedFailure(ctx, operation.CommandID, failure, command.RequestID)
	default:
		if dispatchErr != nil {
			return store.AccountAdminOperation{}, dispatchErr
		}
		return store.AccountAdminOperation{}, errors.New("account admin: unknown native outcome")
	}
}

func validateCommand(command Command) error {
	if len(command.CanonicalIntent) == 0 {
		return ErrInvalidCommand
	}
	return validateCommandRequest(command)
}

func validateCommandRequest(command Command) error {
	if command.CommandID == uuid.Nil || command.ActorAdminID == uuid.Nil || command.NodeInstanceID == uuid.Nil || strings.TrimSpace(command.AccountKey) == "" {
		return ErrInvalidCommand
	}
	switch command.Kind {
	case store.AccountDisable, store.AccountEnable, store.AccountRemove, store.AccountUploadNew, store.AccountReplaceExisting:
	default:
		return ErrInvalidCommand
	}
	provider, email, ok := strings.Cut(strings.ToLower(strings.TrimSpace(command.AccountKey)), ":")
	if !ok || strings.TrimSpace(email) == "" || command.AccountKey != provider+":"+email {
		return ErrInvalidCommand
	}
	if provider != "antigravity" {
		return ErrUnsupportedProvider
	}
	if command.Kind == store.AccountUploadNew || command.Kind == store.AccountReplaceExisting {
		if err := validateCredential(command.Credential, email); err != nil {
			return err
		}
	}
	return nil
}

func validateCanonicalIntent(command Command, uploadFingerprint []byte) error {
	var expected []byte
	var err error
	if command.Kind == store.AccountUploadNew || command.Kind == store.AccountReplaceExisting {
		fingerprint := hex.EncodeToString(uploadFingerprint)
		expected, err = json.Marshal([]any{"account-intent-v1", "account." + string(command.Kind), command.NodeInstanceID.String(), command.AccountKey, 1, fingerprint})
	} else {
		expected, err = canonicalNonUploadIntent(command)
	}
	if err != nil || !bytes.Equal(expected, command.CanonicalIntent) {
		return ErrInvalidCommand
	}
	return nil
}

func canonicalNonUploadIntent(command Command) ([]byte, error) {
	prefix := []any{"account-intent-v1", "account." + string(command.Kind), command.NodeInstanceID.String(), command.AccountKey}
	if command.Kind == store.AccountRemove {
		prefix = append(prefix, "REMOVE")
	}
	return json.Marshal(prefix)
}

var deniedCredentialMetadata = map[string]struct{}{
	"disabled": {}, "weight": {}, "priority": {}, "headers": {}, "request_retry": {},
	"excluded_models": {}, "proxy_url": {}, "note": {}, "websockets": {}, "prefix": {},
	"models": {}, "disable_cooling": {}, "fingerprint_profile": {}, "base_url": {},
	"model_aliases": {}, "request_scoped_errors": {}, "tool_prefix_disabled": {},
}

var credentialMetadataAliases = map[string]string{
	"request-retry": "request_retry", "excluded-models": "excluded_models", "proxy-url": "proxy_url",
	"disable-cooling": "disable_cooling", "fingerprint-profile": "fingerprint_profile", "base-url": "base_url",
	"model-aliases": "model_aliases", "request-scoped-errors": "request_scoped_errors", "tool-prefix-disabled": "tool_prefix_disabled",
}

func validateCredential(raw []byte, expectedEmail string) error {
	if len(raw) == 0 || len(raw) > 1<<20 {
		return ErrInvalidCommand
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return ErrInvalidCommand
	}
	var kind, email string
	if err := json.Unmarshal(fields["type"], &kind); err != nil || kind != "antigravity" {
		return ErrInvalidCommand
	}
	if err := json.Unmarshal(fields["email"], &email); err != nil || strings.ToLower(strings.TrimSpace(email)) != expectedEmail {
		return ErrInvalidCommand
	}
	for key := range fields {
		canonical := key
		if mapped, ok := credentialMetadataAliases[key]; ok {
			canonical = mapped
		}
		if _, denied := deniedCredentialMetadata[canonical]; denied {
			return ErrInvalidCommand
		}
	}
	return nil
}

func validOverrideDetail(detail string) bool {
	if len(detail) < 1 || len(detail) > 512 {
		return false
	}
	for _, r := range detail {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func prepareMutation(adapter *cliproxyapi.NativeAdapter, ctx context.Context, command Command) (*cliproxyapi.PreparedNativeMutation, error) {
	provider, email, _ := strings.Cut(strings.ToLower(strings.TrimSpace(command.AccountKey)), ":")
	switch command.Kind {
	case store.AccountDisable:
		return adapter.PrepareAuthFileDisabled(ctx, provider, email, true)
	case store.AccountEnable:
		return adapter.PrepareAuthFileDisabled(ctx, provider, email, false)
	case store.AccountRemove:
		return adapter.PrepareDeleteAuthFile(ctx, provider, email)
	case store.AccountUploadNew:
		return adapter.PrepareUploadAuthFile(ctx, provider, email, command.Credential)
	case store.AccountReplaceExisting:
		return adapter.PrepareReplaceAuthFile(ctx, provider, email, command.Credential)
	default:
		return nil, ErrInvalidCommand
	}
}

func nativeFailureCode(err error) string {
	if err == nil {
		return "node_management_unavailable"
	}
	known := []string{"unsupported_node_version", "node_management_unavailable", "account_target_not_found", "account_target_ambiguous", "account_target_exists", "account_filename_conflict", "invalid_request"}
	for _, code := range known {
		if err.Error() == code {
			return code
		}
	}
	return "node_management_unavailable"
}

func hashIntent(intent []byte) []byte {
	h := sha256.Sum256(intent)
	return h[:]
}

// CanonicalIntentV1 provides the fixed non-secret identity array used by the
// synchronous composition. Upload callers include their frozen credential
// fingerprint in the canonical bytes before passing them to Execute.
func CanonicalIntentV1(kind store.AccountOperationKind, nodeID uuid.UUID, accountKey string) ([]byte, error) {
	return canonicalNonUploadIntent(Command{Kind: kind, NodeInstanceID: nodeID, AccountKey: accountKey})
}

func CanonicalOverrideIntentV1(kind store.AccountOperationKind, target uuid.UUID, reason, detail string) ([]byte, error) {
	if kind != store.AccountLifecycleOverride && kind != store.AccountSameAccountOverride {
		return nil, ErrInvalidCommand
	}
	confirmation := "OVERRIDE UNKNOWN OPERATION LIFECYCLE BLOCK"
	if kind == store.AccountSameAccountOverride {
		confirmation = "OVERRIDE UNKNOWN OPERATION SAME-ACCOUNT BLOCK"
	}
	var value any
	if detail != "" {
		value = detail
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode([]any{"account-intent-v1", "account." + string(kind), target.String(), reason, confirmation, value}); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(encoded.Bytes(), []byte{'\n'}), nil
}

func (c Command) String() string { return fmt.Sprintf("account command %s", c.CommandID) }
