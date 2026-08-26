package inventorypoll

import (
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sunxu/relay-station-control/internal/drivers"
)

var (
	providerPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	versionPattern  = regexp.MustCompile(`^(?:unknown|[A-Za-z0-9][A-Za-z0-9._+-]{0,63})$`)
	commitPattern   = regexp.MustCompile(`^(?:unknown|[0-9a-f]{7,64})$`)
)

func validateClaim(claim *ClaimedRun) error {
	if claim == nil || claim.PollRunID == uuid.Nil || claim.InstanceID == uuid.Nil ||
		claim.PolicyVersionID == uuid.Nil || claim.FencingToken == uuid.Nil ||
		claim.Target.InstanceID != claim.InstanceID ||
		claim.ProviderPolicy.VersionID != claim.PolicyVersionID ||
		claim.Attempt < 1 || claim.MaxAttempts < claim.Attempt || claim.MaxAttempts > MaximumAttempts ||
		claim.GraceRemaining <= 0 || !claim.ScheduledAt.Equal(claim.ScheduledAt.UTC()) ||
		claim.ScheduledAt.Unix()%int64(DefaultPeriod/time.Second) != 0 ||
		claim.Target.NodeType != drivers.NodeTypeCLIProxyAPI ||
		claim.Target.DriverContractVersion != drivers.DriverContractCLIProxyAPIAuthFilesV1 ||
		!hasInventoryCapability(claim.Target.Capabilities) {
		return ErrInvalidRepositoryResult
	}
	if _, err := activeProviderSet(claim.ProviderPolicy.ActiveProviders); err != nil {
		return err
	}
	return nil
}

func hasInventoryCapability(capabilities []drivers.Capability) bool {
	seen := make(map[drivers.Capability]struct{}, len(capabilities))
	found := false
	for _, capability := range capabilities {
		if _, duplicate := seen[capability]; duplicate {
			return false
		}
		seen[capability] = struct{}{}
		if capability != drivers.CapabilityManagementHealthRead && capability != drivers.CapabilityManagementAccountInventoryRead {
			return false
		}
		if capability == drivers.CapabilityManagementAccountInventoryRead {
			found = true
		}
	}
	return found
}

func activeProviderSet(providers []string) (map[string]struct{}, error) {
	result := make(map[string]struct{}, len(providers))
	for _, provider := range providers {
		if provider == "" || provider != strings.TrimSpace(provider) || provider != strings.ToLower(provider) ||
			!providerPattern.MatchString(provider) {
			return nil, ErrInvalidRepositoryResult
		}
		if _, duplicate := result[provider]; duplicate {
			return nil, ErrInvalidRepositoryResult
		}
		result[provider] = struct{}{}
	}
	return result, nil
}

func projectObservation(policy drivers.ProviderPolicySnapshot, observation drivers.InventoryObservation) (NodeEvidence, []ProviderEvidence, error) {
	active, err := activeProviderSet(policy.ActiveProviders)
	version := observation.Version
	if version == "" {
		version = "unknown"
	}
	commit := observation.Commit
	if commit == "" {
		commit = "unknown"
	}
	if err != nil || policy.VersionID == uuid.Nil || !validDriverResult(observation.Result) ||
		!validDriverReason(observation.Reason) || !versionPattern.MatchString(version) ||
		!commitPattern.MatchString(commit) {
		return NodeEvidence{}, nil, ErrInvalidObservation
	}
	if !observation.TransportSuccess && (observation.ResponseShapeValid || observation.ContractValid) {
		return NodeEvidence{}, nil, ErrInvalidObservation
	}
	if !observation.ResponseShapeValid && observation.ContractValid {
		return NodeEvidence{}, nil, ErrInvalidObservation
	}
	if observation.ContractValid {
		if observation.Mode != drivers.InventoryModeRuntime && observation.Mode != drivers.InventoryModeDiskFallback {
			return NodeEvidence{}, nil, ErrInvalidObservation
		}
	} else if observation.Mode != "" {
		return NodeEvidence{}, nil, ErrInvalidObservation
	}

	node := NodeEvidence{
		TransportSuccess: observation.TransportSuccess, ResponseShapeValid: observation.ResponseShapeValid,
		ContractValid: observation.ContractValid, InventoryMode: observation.Mode,
		NodeIdentityComplete:     observation.NodeIdentityComplete,
		UnidentifiedRecordCount:  observation.UnidentifiedRecordCount,
		UnsupportedProviderCount: observation.UnsupportedProviderCount,
		OutOfScopeProviderCount:  observation.OutOfScopeProviderCount,
		Result:                   observation.Result, Reason: observation.Reason,
		Version: version, Commit: commit,
	}
	if !observation.ContractValid {
		if len(observation.Providers) != 0 {
			return NodeEvidence{}, nil, ErrInvalidObservation
		}
		reason := ProviderReasonContractInvalid
		if !observation.TransportSuccess {
			reason = ProviderReasonTransportFailed
		}
		providers := incompleteProviders(policy.ActiveProviders, reason)
		node.Degraded = true
		return node, providers, nil
	}

	byProvider := make(map[string]drivers.ProviderObservation, len(observation.Providers))
	for _, provider := range observation.Providers {
		if _, expected := active[provider.Provider]; !expected {
			return NodeEvidence{}, nil, ErrInvalidObservation
		}
		if _, duplicate := byProvider[provider.Provider]; duplicate {
			return NodeEvidence{}, nil, ErrInvalidObservation
		}
		byProvider[provider.Provider] = provider
	}
	if len(byProvider) != len(active) {
		return NodeEvidence{}, nil, ErrInvalidObservation
	}

	providerEvidence := make([]ProviderEvidence, 0, len(policy.ActiveProviders))
	allComplete := observation.Mode == drivers.InventoryModeRuntime && observation.NodeIdentityComplete
	var recognized uint64
	for _, providerName := range policy.ActiveProviders {
		provider := byProvider[providerName]
		providerRecognized := uint64(len(provider.Accounts))
		for _, account := range provider.Accounts {
			if account.Provider != providerName || account.OccurrenceCount < 1 {
				return NodeEvidence{}, nil, ErrInvalidObservation
			}
		}
		recognized += providerRecognized
		if providerRecognized > uint64(^uint32(0)) || recognized > uint64(^uint32(0)) {
			return NodeEvidence{}, nil, ErrInvalidObservation
		}
		identityComplete := observation.NodeIdentityComplete && provider.MissingIdentityCount == 0 && provider.DuplicateIdentityCount == 0
		if provider.SnapshotComplete && (!identityComplete || observation.Mode != drivers.InventoryModeRuntime) {
			return NodeEvidence{}, nil, ErrInvalidObservation
		}
		reason := ProviderReasonComplete
		switch {
		case observation.Mode == drivers.InventoryModeDiskFallback:
			reason = ProviderReasonDiskFallback
		case !observation.NodeIdentityComplete:
			reason = ProviderReasonNodeIdentityIncomplete
		case !identityComplete || !provider.SnapshotComplete:
			reason = ProviderReasonIdentityIncomplete
		}
		degraded := provider.Degraded || !provider.SnapshotComplete || observation.Mode == drivers.InventoryModeDiskFallback
		if !provider.SnapshotComplete {
			allComplete = false
		}
		providerEvidence = append(providerEvidence, ProviderEvidence{
			Provider: providerName, RecognizedRecordCount: uint32(providerRecognized),
			MissingIdentityCount:   provider.MissingIdentityCount,
			DuplicateIdentityCount: provider.DuplicateIdentityCount,
			IdentityComplete:       identityComplete, SnapshotComplete: provider.SnapshotComplete,
			Degraded: degraded, Reason: reason,
		})
	}
	node.RecognizedRecordCount = uint32(recognized)
	node.SnapshotComplete = allComplete
	node.Degraded = observation.Result == drivers.ResultDegraded || !allComplete || observation.Mode == drivers.InventoryModeDiskFallback
	return node, providerEvidence, nil
}

func incompleteProviders(active []string, reason ProviderReason) []ProviderEvidence {
	providers := make([]ProviderEvidence, 0, len(active))
	for _, provider := range active {
		providers = append(providers, ProviderEvidence{Provider: provider, Degraded: true, Reason: reason})
	}
	return providers
}

func validDriverResult(result drivers.Result) bool {
	switch result {
	case drivers.ResultSuccess, drivers.ResultFailed, drivers.ResultUnsupported, drivers.ResultDegraded:
		return true
	default:
		return false
	}
}

func validDriverReason(reason drivers.Reason) bool {
	switch reason {
	case drivers.ReasonNone, drivers.ReasonCapabilityUnsupported, drivers.ReasonNodeTypeUnsupported,
		drivers.ReasonDriverContractMismatch, drivers.ReasonSecretUnavailable,
		drivers.ReasonSecretReferenceUnknown, drivers.ReasonSecretProviderUnknown,
		drivers.ReasonSecretFileUnsafe, drivers.ReasonTargetRejected, drivers.ReasonDNSRejected,
		drivers.ReasonNetworkUnavailable, drivers.ReasonTLSRejected, drivers.ReasonRedirectRejected,
		drivers.ReasonTimeout, drivers.ReasonCancelled, drivers.ReasonHTTPStatus,
		drivers.ReasonResponseInvalid, drivers.ReasonResponseTooLarge,
		drivers.ReasonRecordLimit, drivers.ReasonContractInvalid:
		return true
	default:
		return false
	}
}
