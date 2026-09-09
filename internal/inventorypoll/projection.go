package inventorypoll

import (
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/sunxu/relay-station-control/internal/drivers"
)

const (
	maximumProviderBytes   = 64
	maximumEmailBytes      = 320
	maximumSnapshotCounter = uint64(1<<63 - 1)
	minimumSourceUnix      = int64(1)
	maximumSourceUnix      = int64(253402300799)
	maximumSnapshotRecords = uint64(drivers.DefaultInventoryRecords)
	maximumUint32          = ^uint32(0)
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
	if _, _, err := providerSets(claim.ProviderPolicy); err != nil {
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

func providerSets(policy drivers.ProviderPolicySnapshot) (map[string]struct{}, map[string]struct{}, error) {
	active, err := activeProviderSet(policy.ActiveProviders)
	if err != nil {
		return nil, nil, err
	}
	outOfScope, err := activeProviderSet(policy.OutOfScopeProviders)
	if err != nil {
		return nil, nil, err
	}
	for provider := range outOfScope {
		if _, overlap := active[provider]; overlap {
			return nil, nil, ErrInvalidRepositoryResult
		}
	}
	return active, outOfScope, nil
}

// normalizeProvider applies only the version-one deterministic provider rule.
// It never guesses from any non-identity field or consults an external source.
func normalizeProvider(value string) (string, error) {
	if !utf8.ValidString(value) || len(value) > maximumProviderBytes || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", ErrInvalidObservation
	}
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" || len(normalized) > maximumProviderBytes || !providerPattern.MatchString(normalized) {
		return "", ErrInvalidObservation
	}
	return normalized, nil
}

// normalizeEmail deliberately does not parse an RFC address, remove plus tags,
// fold domain aliases or otherwise infer identity beyond trim-and-lowercase.
func normalizeEmail(value string) (string, error) {
	if !utf8.ValidString(value) || len(value) > maximumEmailBytes || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", ErrInvalidObservation
	}
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" || len(normalized) > maximumEmailBytes {
		return "", ErrInvalidObservation
	}
	return normalized, nil
}

// NormalizeAccountIdentity exposes the existing inventory canonical identity rule
// to other read models without introducing another account identity.
func NormalizeAccountIdentity(provider, email string) (string, string, string, error) {
	return normalizeAccountIdentity(provider, email)
}

func normalizeAccountIdentity(provider, email string) (string, string, string, error) {
	normalizedProvider, err := normalizeProvider(provider)
	if err != nil {
		return "", "", "", ErrInvalidObservation
	}
	normalizedEmail, err := normalizeEmail(email)
	if err != nil {
		return "", "", "", ErrInvalidObservation
	}
	return normalizedProvider, normalizedEmail, normalizedProvider + ":" + normalizedEmail, nil
}

func projectObservation(policy drivers.ProviderPolicySnapshot, observation drivers.InventoryObservation) (NodeEvidence, []ProviderEvidence, []SnapshotCandidate, []DuplicateEvidence, error) {
	active, _, err := providerSets(policy)
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
		return NodeEvidence{}, nil, nil, nil, ErrInvalidObservation
	}
	if !observation.TransportSuccess && (observation.ResponseShapeValid || observation.ContractValid) {
		return NodeEvidence{}, nil, nil, nil, ErrInvalidObservation
	}
	if !observation.ResponseShapeValid && observation.ContractValid {
		return NodeEvidence{}, nil, nil, nil, ErrInvalidObservation
	}
	if observation.ContractValid {
		if observation.Mode != drivers.InventoryModeRuntime && observation.Mode != drivers.InventoryModeDiskFallback {
			return NodeEvidence{}, nil, nil, nil, ErrInvalidObservation
		}
	} else if observation.Mode != "" {
		return NodeEvidence{}, nil, nil, nil, ErrInvalidObservation
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
			return NodeEvidence{}, nil, nil, nil, ErrInvalidObservation
		}
		reason := ProviderReasonContractInvalid
		if !observation.TransportSuccess {
			reason = ProviderReasonTransportFailed
		}
		providers := incompleteProviders(policy.ActiveProviders, reason)
		node.Degraded = true
		return node, providers, nil, nil, nil
	}

	byProvider := make(map[string]drivers.ProviderObservation, len(observation.Providers))
	for _, provider := range observation.Providers {
		normalized, normalizeErr := normalizeProvider(provider.Provider)
		if normalizeErr != nil {
			return NodeEvidence{}, nil, nil, nil, ErrInvalidObservation
		}
		if _, expected := active[normalized]; !expected {
			return NodeEvidence{}, nil, nil, nil, ErrInvalidObservation
		}
		if _, duplicate := byProvider[normalized]; duplicate {
			return NodeEvidence{}, nil, nil, nil, ErrInvalidObservation
		}
		provider.Provider = normalized
		byProvider[normalized] = provider
	}
	if len(byProvider) != len(active) {
		return NodeEvidence{}, nil, nil, nil, ErrInvalidObservation
	}

	providerEvidence := make([]ProviderEvidence, 0, len(policy.ActiveProviders))
	allCandidates := make([]SnapshotCandidate, 0)
	allDuplicates := make([]DuplicateEvidence, 0)
	allComplete := observation.Mode == drivers.InventoryModeRuntime && observation.NodeIdentityComplete
	var recognized uint64
	for _, providerName := range policy.ActiveProviders {
		provider := byProvider[providerName]
		projection, projectErr := projectProvider(providerName, provider)
		if projectErr != nil {
			return NodeEvidence{}, nil, nil, nil, projectErr
		}
		if uint64(projection.recognized)+uint64(projection.missing) > maximumSnapshotRecords {
			return NodeEvidence{}, nil, nil, nil, ErrInvalidObservation
		}
		if uint64(node.UnidentifiedRecordCount)+uint64(projection.additionalMissing) > uint64(maximumUint32) {
			return NodeEvidence{}, nil, nil, nil, ErrInvalidObservation
		}
		node.UnidentifiedRecordCount += projection.additionalMissing
		recognized += uint64(projection.recognized)
		if recognized > maximumSnapshotRecords {
			return NodeEvidence{}, nil, nil, nil, ErrInvalidObservation
		}

		// Provider identity completeness is local to the Provider and must stay
		// equivalent to its persisted missing/duplicate counts. Node-wide
		// identity completeness participates in snapshot promotion separately.
		identityComplete := projection.missing == 0 && len(projection.duplicates) == 0
		snapshotComplete := provider.SnapshotComplete && identityComplete &&
			observation.NodeIdentityComplete && observation.Mode == drivers.InventoryModeRuntime
		reason := ProviderReasonComplete
		switch {
		case observation.Mode == drivers.InventoryModeDiskFallback:
			reason = ProviderReasonDiskFallback
		case !observation.NodeIdentityComplete:
			reason = ProviderReasonNodeIdentityIncomplete
		case !identityComplete || !provider.SnapshotComplete:
			reason = ProviderReasonIdentityIncomplete
		}
		// Driver ProviderObservation.Degraded may include Node-wide degradation
		// such as an unsupported or out-of-scope record. Promotion is Provider
		// independent: a complete active Provider remains non-degraded even when
		// the Node aggregate is degraded for evidence outside that Provider.
		degraded := !snapshotComplete
		if !snapshotComplete {
			allComplete = false
		}
		// Pass every valid unique candidate to the fenced finalize so PostgreSQL
		// can close identifiable counts even for an incomplete Provider. The
		// database inserts candidates only when that Provider is promoted.
		allCandidates = append(allCandidates, projection.candidates...)
		allDuplicates = append(allDuplicates, projection.duplicates...)
		providerEvidence = append(providerEvidence, ProviderEvidence{
			Provider: providerName, RecognizedRecordCount: projection.recognized,
			MissingIdentityCount: projection.missing, DuplicateIdentityCount: uint32(len(projection.duplicates)),
			IdentityComplete: identityComplete, SnapshotComplete: snapshotComplete,
			Degraded: degraded, Reason: reason,
		})
	}
	sort.Slice(allCandidates, func(i, j int) bool { return allCandidates[i].AccountKey < allCandidates[j].AccountKey })
	sort.Slice(allDuplicates, func(i, j int) bool { return allDuplicates[i].AccountKey < allDuplicates[j].AccountKey })
	if recognized+uint64(node.UnidentifiedRecordCount)+uint64(node.UnsupportedProviderCount)+uint64(node.OutOfScopeProviderCount) > maximumSnapshotRecords {
		return NodeEvidence{}, nil, nil, nil, ErrInvalidObservation
	}
	node.RecognizedRecordCount = uint32(recognized)
	node.SnapshotComplete = allComplete
	node.Degraded = observation.Result == drivers.ResultDegraded || !allComplete || observation.Mode == drivers.InventoryModeDiskFallback
	return node, providerEvidence, allCandidates, allDuplicates, nil
}

type providerProjection struct {
	recognized        uint32
	missing           uint32
	additionalMissing uint32
	candidates        []SnapshotCandidate
	duplicates        []DuplicateEvidence
}

type groupedAccount struct {
	email   string
	records []drivers.AccountObservation
}

func projectProvider(providerName string, provider drivers.ProviderObservation) (providerProjection, error) {
	projection := providerProjection{missing: provider.MissingIdentityCount}
	groups := make(map[string]*groupedAccount, len(provider.Accounts))
	for _, account := range provider.Accounts {
		normalizedProvider, providerErr := normalizeProvider(account.Provider)
		if providerErr != nil {
			if projection.missing == maximumUint32 || projection.additionalMissing == maximumUint32 {
				return providerProjection{}, ErrInvalidObservation
			}
			projection.missing++
			projection.additionalMissing++
			continue
		}
		if normalizedProvider != providerName {
			return providerProjection{}, ErrInvalidObservation
		}
		_, normalizedEmail, key, emailErr := normalizeAccountIdentity(normalizedProvider, account.Email)
		if emailErr != nil {
			if projection.missing == maximumUint32 || projection.additionalMissing == maximumUint32 {
				return providerProjection{}, ErrInvalidObservation
			}
			projection.missing++
			projection.additionalMissing++
			continue
		}
		if !validAccountState(account.State) || account.OccurrenceCount == 0 ||
			account.OccurrenceCount > uint32(drivers.DefaultInventoryRecords) ||
			account.SuccessCount > maximumSnapshotCounter || account.FailedCount > maximumSnapshotCounter ||
			account.RecentRequestCount > maximumSnapshotRecords || !validSourceUnix(account.LastRefreshUnix) ||
			!validSourceUnix(account.NextRetryUnix) || !validSourceUnix(account.UpdatedAtUnix) {
			return providerProjection{}, ErrInvalidObservation
		}
		if !validAvailabilityRuntimeEvidence(account.AvailabilityRuntimeEvidence) || !validAuthFailureReason(account.AuthFailureReason) {
			return providerProjection{}, ErrInvalidObservation
		}
		group := groups[key]
		if group == nil {
			group = &groupedAccount{email: normalizedEmail}
			groups[key] = group
		}
		account.Provider = normalizedProvider
		account.Email = normalizedEmail
		group.records = append(group.records, account)
	}

	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		group := groups[key]
		occurrences, occurrenceErr := groupOccurrenceCount(group.records)
		if occurrenceErr != nil || uint64(projection.recognized)+uint64(occurrences) > maximumSnapshotRecords {
			return providerProjection{}, ErrInvalidObservation
		}
		projection.recognized += occurrences
		if occurrences > 1 {
			projection.duplicates = append(projection.duplicates, DuplicateEvidence{
				Provider: providerName, AccountKey: key, OccurrenceCount: occurrences,
			})
			continue
		}
		account := group.records[0]
		projection.candidates = append(projection.candidates, SnapshotCandidate{
			Provider: providerName, AccountKey: key, Email: group.email, BasicStatus: account.State,
			SuccessCount: account.SuccessCount, FailedCount: account.FailedCount,
			RecentRequestCount:          account.RecentRequestCount,
			LastRefreshUnix:             nullableSourceUnix(account.LastRefreshUnix),
			NextRetryUnix:               nullableSourceUnix(account.NextRetryUnix),
			UpdatedAtUnix:               nullableSourceUnix(account.UpdatedAtUnix),
			AvailabilityRuntimeEvidence: account.AvailabilityRuntimeEvidence,
			AuthFailureReason:           account.AuthFailureReason,
		})
	}
	return projection, nil
}

func validAvailabilityRuntimeEvidence(value *string) bool {
	if value == nil {
		return true
	}
	switch *value {
	case "file_active", "file_disabled", "file_error", "file_unavailable", "file_unknown":
		return true
	}
	return false
}

func validAuthFailureReason(value *string) bool {
	if value == nil {
		return true
	}
	switch *value {
	case "token_invalid", "account_blocked", "forbidden", "other":
		return true
	}
	return false
}

func groupOccurrenceCount(records []drivers.AccountObservation) (uint32, error) {
	if len(records) == 0 || len(records) > drivers.DefaultInventoryRecords {
		return 0, ErrInvalidObservation
	}
	if len(records) == 1 {
		return records[0].OccurrenceCount, nil
	}
	want := uint32(len(records))
	allOne, allGrouped := true, true
	for _, record := range records {
		allOne = allOne && record.OccurrenceCount == 1
		allGrouped = allGrouped && record.OccurrenceCount == want
	}
	if !allOne && !allGrouped {
		return 0, ErrInvalidObservation
	}
	return want, nil
}

func validAccountState(state drivers.AccountState) bool {
	switch state {
	case drivers.AccountStateDisabled, drivers.AccountStateUnavailable, drivers.AccountStateError,
		drivers.AccountStateActive, drivers.AccountStateUnknown:
		return true
	default:
		return false
	}
}

func validSourceUnix(value int64) bool {
	return value == 0 || value >= minimumSourceUnix && value <= maximumSourceUnix
}

func nullableSourceUnix(value int64) *int64 {
	if value == 0 {
		return nil
	}
	result := value
	return &result
}

func incompleteProviders(active []string, reason ProviderReason) []ProviderEvidence {
	providers := make([]ProviderEvidence, 0, len(active))
	for _, provider := range active {
		providers = append(providers, ProviderEvidence{
			Provider: provider, IdentityComplete: true, Degraded: true, Reason: reason,
		})
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
