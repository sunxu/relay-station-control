// Package drivers defines the closed Control-to-Node management driver boundary.
//
// The package deliberately has no generic HTTP request type. Callers can ask for
// one of the operations represented by NodeDriver, but cannot provide a method,
// path, header, query, or request body.
package drivers

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

type NodeType string
type DriverContractVersion string
type Capability string
type Operation string
type Result string
type Reason string
type InventoryMode string
type AccountState string

const (
	NodeTypeCLIProxyAPI NodeType = "cliproxyapi"

	DriverContractCLIProxyAPIAuthFilesV1 DriverContractVersion = "cliproxyapi.auth-files.v1"

	CapabilityManagementHealthRead           Capability = "management_health_read"
	CapabilityManagementAccountInventoryRead Capability = "management_account_inventory_read"

	OperationProbe            Operation = "probe"
	OperationAccountInventory Operation = "account_inventory"

	ResultSuccess     Result = "success"
	ResultFailed      Result = "failed"
	ResultUnsupported Result = "unsupported"
	ResultDegraded    Result = "degraded"

	ReasonNone                   Reason = "none"
	ReasonCapabilityUnsupported  Reason = "capability_unsupported"
	ReasonNodeTypeUnsupported    Reason = "node_type_unsupported"
	ReasonDriverContractMismatch Reason = "driver_contract_mismatch"
	ReasonSecretUnavailable      Reason = "secret_unavailable"
	ReasonSecretReferenceUnknown Reason = "secret_reference_unknown"
	ReasonSecretProviderUnknown  Reason = "secret_provider_unknown"
	ReasonSecretFileUnsafe       Reason = "secret_file_unsafe"
	ReasonTargetRejected         Reason = "target_rejected"
	ReasonDNSRejected            Reason = "dns_rejected"
	ReasonNetworkUnavailable     Reason = "network_unavailable"
	ReasonTLSRejected            Reason = "tls_rejected"
	ReasonRedirectRejected       Reason = "redirect_rejected"
	ReasonTimeout                Reason = "timeout"
	ReasonCancelled              Reason = "cancelled"
	ReasonHTTPStatus             Reason = "http_status"
	ReasonResponseInvalid        Reason = "response_invalid"
	ReasonResponseTooLarge       Reason = "response_too_large"
	ReasonRecordLimit            Reason = "record_limit"
	ReasonContractInvalid        Reason = "contract_invalid"

	InventoryModeRuntime      InventoryMode = "runtime"
	InventoryModeDiskFallback InventoryMode = "disk_fallback"

	AccountStateDisabled    AccountState = "disabled"
	AccountStateUnavailable AccountState = "unavailable"
	AccountStateError       AccountState = "error"
	AccountStateActive      AccountState = "active"
	AccountStateUnknown     AccountState = "unknown"
)

// SecretReference is an opaque database value. Its formatter is intentionally
// redacted; Value is only for passing the opaque value to SecretResolver.
type SecretReference struct {
	value string
}

func NewSecretReference(value string) SecretReference { return SecretReference{value: value} }

// Format prevents fmt from projecting an opaque reference through any common
// formatting verb. It is not a credential, but it is still sensitive metadata.
func (SecretReference) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED]"))
}

// NodeTarget is the complete set of caller-controlled target data accepted at
// the driver boundary. There is intentionally no method/path/header/body field.
type NodeTarget struct {
	InstanceID            uuid.UUID
	NodeType              NodeType
	DriverContractVersion DriverContractVersion
	ManagementEndpoint    string
	ReaderSecretReference SecretReference
	Capabilities          []Capability
}

func (NodeTarget) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED NodeTarget]"))
}

type ProviderPolicySnapshot struct {
	VersionID           uuid.UUID
	ActiveProviders     []string
	OutOfScopeProviders []string
}

type ProbeRequest struct {
	Target NodeTarget
}

func (ProbeRequest) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED ProbeRequest]"))
}

type InventoryRequest struct {
	Target         NodeTarget
	ProviderPolicy ProviderPolicySnapshot
}

func (InventoryRequest) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED InventoryRequest]"))
}

// ProbeObservation only reports the health endpoint contract. Reachable does
// not imply inventory completeness, schedulability, or data-plane health.
type ProbeObservation struct {
	Reachable bool
	Result    Result
	Reason    Reason
}

type AccountObservation struct {
	Provider           string
	Email              string
	State              AccountState
	OccurrenceCount    uint32
	SuccessCount       uint64
	FailedCount        uint64
	RecentRequestCount uint64
	LastRefreshUnix    int64
	NextRetryUnix      int64
	UpdatedAtUnix      int64
}

func (AccountObservation) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED AccountObservation]"))
}

type ProviderObservation struct {
	Provider               string
	SnapshotComplete       bool
	Degraded               bool
	MissingIdentityCount   uint32
	DuplicateIdentityCount uint32
	Accounts               []AccountObservation
}

func (ProviderObservation) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED ProviderObservation]"))
}

type InventoryObservation struct {
	TransportSuccess         bool
	ResponseShapeValid       bool
	ContractValid            bool
	Mode                     InventoryMode
	Version                  string
	Commit                   string
	NodeIdentityComplete     bool
	UnsupportedProviderCount uint32
	OutOfScopeProviderCount  uint32
	UnidentifiedRecordCount  uint32
	Providers                []ProviderObservation
	Result                   Result
	Reason                   Reason
}

func (InventoryObservation) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED InventoryObservation]"))
}

// NodeDriver is deliberately operation-specific. Adding another management
// operation requires changing this contract and its registry capability map.
type NodeDriver interface {
	Probe(context.Context, ProbeRequest) (ProbeObservation, error)
	ListAccountInventory(context.Context, InventoryRequest) (InventoryObservation, error)
}
