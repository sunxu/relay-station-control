package cliproxyapi

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/sunxu/relay-station-control/internal/drivers"
)

// DriverConfig is validated once at startup. A Driver is not a scheduler and
// construction does not resolve DNS, read a target Secret, or start goroutines.
type DriverConfig struct {
	Management     drivers.ManagementConfig
	SecretResolver drivers.SecretResolver
	Observer       *Observer
	Now            func() time.Time
}

// Driver implements the fixed CLIProxyAPI read-only management contract.
type Driver struct {
	management      drivers.ValidatedManagementConfig
	secretResolver  drivers.SecretResolver
	dialer          ContextDialer
	healthBodyLimit int64
	inventoryLimits InventoryLimits
	observer        *Observer
	now             func() time.Time
}

// DriverError exposes only the operation and a closed reason. It deliberately
// does not unwrap network, TLS, filesystem, URL, JSON, or policy errors.
type DriverError struct {
	Operation drivers.Operation
	Reason    drivers.Reason
}

func (failure *DriverError) Error() string {
	if failure == nil {
		return "cliproxyapi driver failed"
	}
	return "cliproxyapi driver failed: " + string(failure.Operation) + ": " + string(failure.Reason)
}

func (*DriverError) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("cliproxyapi driver failed"))
}

func NewDriver(configuration DriverConfig) (*Driver, error) {
	return newDriver(configuration, nil)
}

// newDriver keeps synthetic dialing fixtures package-private. Production callers
// use the standard network dialer.
func newDriver(configuration DriverConfig, dialer ContextDialer) (*Driver, error) {
	validated, err := configuration.Management.Validate()
	if err != nil || configuration.SecretResolver == nil {
		return nil, drivers.ErrInvalidManagementConfig
	}
	now := configuration.Now
	if now == nil {
		now = time.Now
	}
	return &Driver{
		management: validated, secretResolver: configuration.SecretResolver,
		dialer:          dialer,
		healthBodyLimit: validated.MaxHealthResponseBytes,
		inventoryLimits: InventoryLimits{
			MaxBodyBytes: validated.MaxInventoryResponseBytes,
			MaxRecords:   validated.MaxInventoryRecords,
		},
		observer: configuration.Observer, now: now,
	}, nil
}

func (driver *Driver) Probe(ctx context.Context, request drivers.ProbeRequest) (drivers.ProbeObservation, error) {
	started := time.Now()
	operation := drivers.OperationProbe
	finish := func(observation drivers.ProbeObservation, err error) (drivers.ProbeObservation, error) {
		driver.observe(ctx, operation, observation.Result, observation.Reason, time.Since(started))
		return observation, err
	}
	if driver == nil || ctx == nil {
		return finish(failedProbe(drivers.ReasonCancelled), newDriverError(operation, drivers.ReasonCancelled))
	}
	if reason := contextReason(ctx); reason != drivers.ReasonNone {
		return finish(failedProbe(reason), newDriverError(operation, reason))
	}
	if reason := validateTarget(request.Target, drivers.CapabilityManagementHealthRead); reason != drivers.ReasonNone {
		return finish(failedProbe(reason), newDriverError(operation, reason))
	}
	transport, err := driver.transport(request.Target.ManagementEndpoint)
	if err != nil {
		return finish(failedProbe(drivers.ReasonTargetRejected), newDriverError(operation, drivers.ReasonTargetRejected))
	}
	response, err := transport.getHealth(ctx)
	if err != nil {
		reason := transportReason(err)
		return finish(failedProbe(reason), newDriverError(operation, reason))
	}
	defer response.Body.Close()
	parsed := parseHealth(response.StatusCode, response.Body, driver.healthBodyLimit)
	observation := drivers.ProbeObservation{
		Reachable: parsed.Reachable,
		Result:    drivers.ResultFailed,
		Reason:    healthReason(parsed.Reason),
	}
	if parsed.Reachable {
		observation.Result = drivers.ResultSuccess
		observation.Reason = drivers.ReasonNone
		return finish(observation, nil)
	}
	return finish(observation, newDriverError(operation, observation.Reason))
}

func (driver *Driver) ListAccountInventory(ctx context.Context, request drivers.InventoryRequest) (drivers.InventoryObservation, error) {
	started := time.Now()
	operation := drivers.OperationAccountInventory
	finish := func(observation drivers.InventoryObservation, err error) (drivers.InventoryObservation, error) {
		driver.observe(ctx, operation, observation.Result, observation.Reason, time.Since(started))
		return observation, err
	}
	if driver == nil || ctx == nil {
		return finish(failedInventory(drivers.ReasonCancelled), newDriverError(operation, drivers.ReasonCancelled))
	}
	if reason := contextReason(ctx); reason != drivers.ReasonNone {
		return finish(failedInventory(reason), newDriverError(operation, reason))
	}
	if reason := validateTarget(request.Target, drivers.CapabilityManagementAccountInventoryRead); reason != drivers.ReasonNone {
		return finish(failedInventory(reason), newDriverError(operation, reason))
	}
	transport, err := driver.transport(request.Target.ManagementEndpoint)
	if err != nil {
		return finish(failedInventory(drivers.ReasonTargetRejected), newDriverError(operation, drivers.ReasonTargetRejected))
	}
	policy, err := driver.providerPolicy(request.ProviderPolicy)
	if err != nil {
		return finish(failedInventory(drivers.ReasonContractInvalid), newDriverError(operation, drivers.ReasonContractInvalid))
	}
	secret, err := driver.secretResolver.Resolve(ctx, request.Target.ReaderSecretReference)
	if err != nil {
		reason := contextReason(ctx)
		if reason == drivers.ReasonNone {
			reason = secretReason(err)
		}
		return finish(failedInventory(reason), newDriverError(operation, reason))
	}
	defer secret.Destroy()

	var response *httpResponse
	err = secret.Use(func(managementKey string) error {
		var requestErr error
		response, requestErr = transport.getAccountInventory(ctx, managementKey)
		return requestErr
	})
	if err != nil {
		reason := contextReason(ctx)
		if reason == drivers.ReasonNone {
			reason = secretReason(err)
		}
		var requestFailure *RequestError
		if errors.As(err, &requestFailure) {
			reason = transportReason(err)
		}
		return finish(failedInventory(reason), newDriverError(operation, reason))
	}
	defer response.Body.Close()
	parsed := parseInventory(response.StatusCode, response.Header, response.Body, InventoryParseOptions{
		Limits: driver.inventoryLimits,
		Policy: policy,
		Now:    driver.now().UTC(),
	})
	observation := projectInventory(parsed)
	if observation.Result == drivers.ResultFailed {
		return finish(observation, newDriverError(operation, observation.Reason))
	}
	return finish(observation, nil)
}

func (driver *Driver) transport(endpoint string) (*safeTransport, error) {
	return newTransport(endpoint, driver.management, transportOptions{
		Dialer: driver.dialer,
	})
}

func (driver *Driver) providerPolicy(snapshot drivers.ProviderPolicySnapshot) (ProviderPolicy, error) {
	if snapshot.VersionID == uuid.Nil {
		return ProviderPolicy{}, errors.New("provider_policy_invalid")
	}
	return newProviderPolicy(ProviderPolicyVersionV1, snapshot.ActiveProviders, snapshot.OutOfScopeProviders)
}

func (driver *Driver) observe(ctx context.Context, operation drivers.Operation, result drivers.Result, reason drivers.Reason, elapsed time.Duration) {
	if driver != nil && driver.observer != nil {
		driver.observer.Record(ctx, operation, result, reason, elapsed)
	}
}

func validateTarget(target drivers.NodeTarget, required drivers.Capability) drivers.Reason {
	if target.InstanceID == uuid.Nil {
		return drivers.ReasonTargetRejected
	}
	if target.NodeType != drivers.NodeTypeCLIProxyAPI {
		return drivers.ReasonNodeTypeUnsupported
	}
	if target.DriverContractVersion != drivers.DriverContractCLIProxyAPIAuthFilesV1 {
		return drivers.ReasonDriverContractMismatch
	}
	declared := false
	seen := make(map[drivers.Capability]struct{}, len(target.Capabilities))
	for _, capability := range target.Capabilities {
		if capability != drivers.CapabilityManagementHealthRead && capability != drivers.CapabilityManagementAccountInventoryRead {
			return drivers.ReasonCapabilityUnsupported
		}
		if _, duplicate := seen[capability]; duplicate {
			return drivers.ReasonCapabilityUnsupported
		}
		seen[capability] = struct{}{}
		if capability == required {
			declared = true
		}
	}
	if !declared {
		return drivers.ReasonCapabilityUnsupported
	}
	return drivers.ReasonNone
}

func projectInventory(parsed InventoryObservation) drivers.InventoryObservation {
	result := drivers.InventoryObservation{
		TransportSuccess:         parsed.TransportSuccess,
		ResponseShapeValid:       parsed.ResponseShapeValid,
		ContractValid:            parsed.ContractValid,
		Mode:                     drivers.InventoryMode(parsed.Mode),
		Version:                  parsed.Version,
		Commit:                   parsed.Commit,
		NodeIdentityComplete:     parsed.NodeIdentityComplete,
		UnsupportedProviderCount: uint32(parsed.UnsupportedProviderCount),
		OutOfScopeProviderCount:  uint32(parsed.OutOfScopeProviderCount),
		UnidentifiedRecordCount:  uint32(parsed.UnidentifiableCount),
		Result:                   drivers.ResultFailed,
		Reason:                   inventoryReason(parsed.Reason),
	}
	accountsByProvider := make(map[string][]drivers.AccountObservation, len(parsed.Providers))
	for _, account := range parsed.Accounts {
		projected := drivers.AccountObservation{
			Provider:                    account.Provider,
			Email:                       account.Email,
			State:                       drivers.AccountState(account.Status),
			OccurrenceCount:             uint32(account.OccurrenceCount),
			SuccessCount:                uint64(account.Success),
			FailedCount:                 uint64(account.Failed),
			RecentRequestCount:          uint64(account.RecentRequests),
			AvailabilityRuntimeEvidence: account.AvailabilityRuntimeEvidence,
			AuthFailureReason:           account.AuthFailureReason,
		}
		if account.LastRefresh != nil {
			projected.LastRefreshUnix = account.LastRefresh.Unix()
		}
		if account.NextRetryAfter != nil {
			projected.NextRetryUnix = account.NextRetryAfter.Unix()
		}
		if account.UpdatedAt != nil {
			projected.UpdatedAtUnix = account.UpdatedAt.Unix()
		}
		accountsByProvider[account.Provider] = append(accountsByProvider[account.Provider], projected)
	}
	complete := parsed.NodeIdentityComplete
	for _, provider := range parsed.Providers {
		providerComplete := provider.ProviderSnapshotComplete
		if !providerComplete {
			complete = false
		}
		result.Providers = append(result.Providers, drivers.ProviderObservation{
			Provider:               provider.Provider,
			SnapshotComplete:       providerComplete,
			Degraded:               parsed.Degraded || !providerComplete,
			MissingIdentityCount:   uint32(provider.IdentityErrorCount),
			DuplicateIdentityCount: uint32(provider.DuplicateGroupCount),
			Accounts:               accountsByProvider[provider.Provider],
		})
	}
	if parsed.ContractValid {
		result.Reason = drivers.ReasonNone
		if parsed.Degraded || !complete {
			result.Result = drivers.ResultDegraded
		} else {
			result.Result = drivers.ResultSuccess
		}
	}
	return result
}

func failedProbe(reason drivers.Reason) drivers.ProbeObservation {
	return drivers.ProbeObservation{Result: drivers.ResultFailed, Reason: reason}
}

func failedInventory(reason drivers.Reason) drivers.InventoryObservation {
	return drivers.InventoryObservation{Result: drivers.ResultFailed, Reason: reason}
}

func newDriverError(operation drivers.Operation, reason drivers.Reason) error {
	return &DriverError{Operation: operation, Reason: reason}
}

func healthReason(reason HealthReason) drivers.Reason {
	switch reason {
	case HealthReasonHTTPStatus:
		return drivers.ReasonHTTPStatus
	case HealthReasonResponseTooLarge:
		return drivers.ReasonResponseTooLarge
	default:
		return drivers.ReasonResponseInvalid
	}
}

func inventoryReason(reason InventoryReason) drivers.Reason {
	switch reason {
	case InventoryReasonHTTPStatus:
		return drivers.ReasonHTTPStatus
	case InventoryReasonResponseTooLarge:
		return drivers.ReasonResponseTooLarge
	case InventoryReasonRecordLimit:
		return drivers.ReasonRecordLimit
	case InventoryReasonResponseInvalid:
		return drivers.ReasonResponseInvalid
	default:
		return drivers.ReasonContractInvalid
	}
}

func transportReason(err error) drivers.Reason {
	var failure *RequestError
	if !errors.As(err, &failure) {
		return drivers.ReasonNetworkUnavailable
	}
	switch failure.Reason {
	case FailureTargetRejected, FailureRequestRejected:
		return drivers.ReasonTargetRejected
	case FailureDNSRejected:
		return drivers.ReasonDNSRejected
	case FailureTLSRejected:
		return drivers.ReasonTLSRejected
	case FailureRedirectRejected:
		return drivers.ReasonRedirectRejected
	case FailureTimeout:
		return drivers.ReasonTimeout
	case FailureCancelled:
		return drivers.ReasonCancelled
	default:
		return drivers.ReasonNetworkUnavailable
	}
}

func secretReason(err error) drivers.Reason {
	switch {
	case errors.Is(err, drivers.ErrSecretReferenceUnknown):
		return drivers.ReasonSecretReferenceUnknown
	case errors.Is(err, drivers.ErrSecretProviderUnknown):
		return drivers.ReasonSecretProviderUnknown
	case errors.Is(err, drivers.ErrSecretFileUnsafe):
		return drivers.ReasonSecretFileUnsafe
	default:
		return drivers.ReasonSecretUnavailable
	}
}

func contextReason(ctx context.Context) drivers.Reason {
	if ctx == nil {
		return drivers.ReasonCancelled
	}
	switch ctx.Err() {
	case context.Canceled:
		return drivers.ReasonCancelled
	case context.DeadlineExceeded:
		return drivers.ReasonTimeout
	default:
		return drivers.ReasonNone
	}
}

var _ drivers.NodeDriver = (*Driver)(nil)
