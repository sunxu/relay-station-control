package cliproxyapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	DefaultInventoryBodyLimit   int64 = 5 << 20
	DefaultInventoryRecordLimit       = 1000
	UnknownVersionHeader              = "unknown"
	ProviderPolicyVersionV1           = "v1"
	maxCounter                  int64 = 1<<53 - 1
)

type InventoryMode string

const (
	InventoryModeRuntime      InventoryMode = "runtime"
	InventoryModeDiskFallback InventoryMode = "disk_fallback"
)

type InventoryReason string

const (
	InventoryReasonOK               InventoryReason = "ok"
	InventoryReasonHTTPStatus       InventoryReason = "http_status"
	InventoryReasonResponseInvalid  InventoryReason = "response_invalid"
	InventoryReasonResponseTooLarge InventoryReason = "response_too_large"
	InventoryReasonRecordLimit      InventoryReason = "record_limit"
	InventoryReasonContractInvalid  InventoryReason = "contract_invalid"
	InventoryReasonPolicyInvalid    InventoryReason = "policy_invalid"
)

type BaseStatus string

const (
	BaseStatusDisabled    BaseStatus = "disabled"
	BaseStatusUnavailable BaseStatus = "unavailable"
	BaseStatusError       BaseStatus = "error"
	BaseStatusActive      BaseStatus = "active"
	BaseStatusUnknown     BaseStatus = "unknown"
)

type InventoryLimits struct {
	MaxBodyBytes int64
	MaxRecords   int
}

func (l InventoryLimits) withDefaults() InventoryLimits {
	if l.MaxBodyBytes <= 0 {
		l.MaxBodyBytes = DefaultInventoryBodyLimit
	}
	if l.MaxRecords <= 0 {
		l.MaxRecords = DefaultInventoryRecordLimit
	}
	return l
}

func (l InventoryLimits) valid() bool {
	return l.MaxBodyBytes > 0 && l.MaxBodyBytes <= DefaultInventoryBodyLimit &&
		l.MaxRecords > 0 && l.MaxRecords <= DefaultInventoryRecordLimit
}

// ProviderPolicy is an immutable-by-construction normalized policy snapshot.
// Slices passed to newProviderPolicy are copied and its internal sets are not
// exposed to callers.
type ProviderPolicy struct {
	version    string
	active     []string
	outOfScope []string
	activeSet  map[string]struct{}
	outSet     map[string]struct{}
}

var providerNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

func newProviderPolicy(version string, active, outOfScope []string) (ProviderPolicy, error) {
	if version != ProviderPolicyVersionV1 || len(active) == 0 {
		return ProviderPolicy{}, errors.New("provider_policy_invalid")
	}
	policy := ProviderPolicy{
		version:   version,
		activeSet: make(map[string]struct{}, len(active)),
		outSet:    make(map[string]struct{}, len(outOfScope)),
	}
	for _, input := range active {
		provider, ok := normalizePolicyProvider(input)
		if !ok {
			return ProviderPolicy{}, errors.New("provider_policy_invalid")
		}
		if _, duplicate := policy.activeSet[provider]; duplicate {
			return ProviderPolicy{}, errors.New("provider_policy_invalid")
		}
		policy.activeSet[provider] = struct{}{}
		policy.active = append(policy.active, provider)
	}
	for _, input := range outOfScope {
		provider, ok := normalizePolicyProvider(input)
		if !ok {
			return ProviderPolicy{}, errors.New("provider_policy_invalid")
		}
		if _, overlap := policy.activeSet[provider]; overlap {
			return ProviderPolicy{}, errors.New("provider_policy_invalid")
		}
		if _, duplicate := policy.outSet[provider]; duplicate {
			return ProviderPolicy{}, errors.New("provider_policy_invalid")
		}
		policy.outSet[provider] = struct{}{}
		policy.outOfScope = append(policy.outOfScope, provider)
	}
	sort.Strings(policy.active)
	sort.Strings(policy.outOfScope)
	return policy, nil
}

func normalizePolicyProvider(value string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	return normalized, normalized != "" && providerNamePattern.MatchString(normalized)
}

func (p ProviderPolicy) valid() bool {
	if p.version != ProviderPolicyVersionV1 || len(p.active) == 0 || p.activeSet == nil || p.outSet == nil {
		return false
	}
	for _, provider := range p.active {
		if _, ok := p.activeSet[provider]; !ok {
			return false
		}
		if _, overlap := p.outSet[provider]; overlap {
			return false
		}
	}
	return true
}

func (p ProviderPolicy) Version() string { return p.version }

func (p ProviderPolicy) ActiveProviders() []string {
	return append([]string(nil), p.active...)
}

func (p ProviderPolicy) OutOfScopeProviders() []string {
	return append([]string(nil), p.outOfScope...)
}

type AccountObservation struct {
	Provider        string
	Email           string
	Source          string
	Status          BaseStatus
	Success         int64
	Failed          int64
	RecentRequests  int
	LastRefresh     *time.Time
	NextRetryAfter  *time.Time
	UpdatedAt       *time.Time
	OccurrenceCount int
}

// String and GoString prevent accidental fmt/log projection of the in-memory
// identity fields. Callers needing structured data must select fields
// explicitly; observability must never format this DTO.
func (AccountObservation) String() string   { return "account_observation" }
func (AccountObservation) GoString() string { return "account_observation" }

type ProviderObservation struct {
	Provider                 string
	RecordCount              int
	ProviderSnapshotComplete bool
	IdentityErrorCount       int
	DuplicateGroupCount      int
}

type InventoryObservation struct {
	TransportSuccess         bool
	ResponseShapeValid       bool
	ContractValid            bool
	Mode                     InventoryMode
	Degraded                 bool
	Reason                   InventoryReason
	Version                  string
	Commit                   string
	NodeIdentityComplete     bool
	UnidentifiableCount      int
	UnsupportedProviderCount int
	OutOfScopeProviderCount  int
	DuplicateGroupCount      int
	Accounts                 []AccountObservation
	Providers                []ProviderObservation
}

func (InventoryObservation) String() string   { return "inventory_observation" }
func (InventoryObservation) GoString() string { return "inventory_observation" }

type InventoryParseOptions struct {
	Limits InventoryLimits
	Policy ProviderPolicy
	Now    time.Time
}

var (
	versionHeaderPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)
	commitHeaderPattern  = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)
)

func sanitizeVersionHeaders(headers http.Header) (string, string) {
	version := sanitizeHeaderValues(headers.Values("X-CPA-VERSION"), versionHeaderPattern)
	commit := sanitizeHeaderValues(headers.Values("X-CPA-COMMIT"), commitHeaderPattern)
	if commit != UnknownVersionHeader {
		commit = strings.ToLower(commit)
	}
	return version, commit
}

func sanitizeHeaderValues(values []string, pattern *regexp.Regexp) string {
	if len(values) != 1 {
		return UnknownVersionHeader
	}
	return sanitizeHeader(values[0], pattern)
}

func sanitizeHeader(value string, pattern *regexp.Regexp) string {
	if value == "" || !pattern.MatchString(value) {
		return UnknownVersionHeader
	}
	return value
}

// parseInventory parses a completed fixed auth-files response. It performs no
// DNS, network, persistence, logging, or cross-node duplicate detection.
func parseInventory(statusCode int, headers http.Header, body io.Reader, options InventoryParseOptions) InventoryObservation {
	version, commit := sanitizeVersionHeaders(headers)
	observation := InventoryObservation{
		Reason:               InventoryReasonHTTPStatus,
		Version:              version,
		Commit:               commit,
		NodeIdentityComplete: true,
	}
	if statusCode != http.StatusOK {
		return observation
	}
	observation.TransportSuccess = true
	if !options.Policy.valid() {
		observation.Reason = InventoryReasonPolicyInvalid
		return observation
	}
	limits := options.Limits.withDefaults()
	if !limits.valid() {
		observation.Reason = InventoryReasonContractInvalid
		return observation
	}
	encoded, tooLarge, err := readBounded(body, limits.MaxBodyBytes)
	if tooLarge {
		observation.Reason = InventoryReasonResponseTooLarge
		return observation
	}
	if err != nil {
		observation.Reason = InventoryReasonResponseInvalid
		return observation
	}

	top, err := decodeJSONObject(encoded)
	if err != nil {
		observation.Reason = InventoryReasonResponseInvalid
		return observation
	}
	rawFiles, ok := top["files"]
	if !ok {
		observation.Reason = InventoryReasonResponseInvalid
		return observation
	}
	var records []json.RawMessage
	if err := json.Unmarshal(rawFiles, &records); err != nil || records == nil {
		observation.Reason = InventoryReasonResponseInvalid
		return observation
	}
	if len(records) > limits.MaxRecords {
		observation.Reason = InventoryReasonRecordLimit
		return observation
	}
	observation.ResponseShapeValid = true

	parsed := make([]parsedAccount, 0, len(records))
	for _, record := range records {
		account, err := parseAccount(record, options.Now)
		if err != nil {
			observation.Reason = InventoryReasonContractInvalid
			return observation
		}
		parsed = append(parsed, account)
	}
	mode, ok := classifyMode(parsed)
	if !ok {
		observation.Reason = InventoryReasonContractInvalid
		return observation
	}
	observation.ContractValid = true
	observation.Mode = mode
	observation.Degraded = mode == InventoryModeDiskFallback
	observation.Reason = InventoryReasonOK
	classifyProviders(&observation, parsed, options.Policy)
	return observation
}

type parsedAccount struct {
	observation AccountObservation
	providerSet bool
	emailSet    bool
	sourceSet   bool
	diskShape   bool
}

func parseAccount(encoded json.RawMessage, now time.Time) (parsedAccount, error) {
	fields, err := decodeJSONObject(encoded)
	if err != nil {
		return parsedAccount{}, errors.New("contract_invalid")
	}
	result := parsedAccount{}
	var upstreamStatus string
	var disabled, unavailable bool
	var nextRetry *time.Time

	for key, raw := range fields {
		switch key {
		case "provider":
			value, err := decodeString(raw)
			if err != nil {
				return parsedAccount{}, err
			}
			if !validIdentityScalar(value, 64) {
				return parsedAccount{}, errors.New("contract_invalid")
			}
			result.providerSet = true
			result.observation.Provider = strings.ToLower(strings.TrimSpace(value))
			if result.observation.Provider != "" && !providerNamePattern.MatchString(result.observation.Provider) {
				return parsedAccount{}, errors.New("contract_invalid")
			}
		case "email":
			value, err := decodeString(raw)
			if err != nil {
				return parsedAccount{}, err
			}
			if !validIdentityScalar(value, 320) {
				return parsedAccount{}, errors.New("contract_invalid")
			}
			result.emailSet = true
			result.observation.Email = strings.ToLower(strings.TrimSpace(value))
		case "source":
			value, err := decodeString(raw)
			if err != nil {
				return parsedAccount{}, err
			}
			if !validIdentityScalar(value, 32) {
				return parsedAccount{}, errors.New("contract_invalid")
			}
			result.sourceSet = true
			result.observation.Source = strings.ToLower(strings.TrimSpace(value))
		case "status":
			value, err := decodeString(raw)
			if err != nil {
				return parsedAccount{}, err
			}
			if !validIdentityScalar(value, 64) {
				return parsedAccount{}, errors.New("contract_invalid")
			}
			upstreamStatus = strings.ToLower(strings.TrimSpace(value))
		case "disabled":
			value, err := decodeBool(raw)
			if err != nil {
				return parsedAccount{}, err
			}
			disabled = value
		case "unavailable":
			value, err := decodeBool(raw)
			if err != nil {
				return parsedAccount{}, err
			}
			unavailable = value
		case "success":
			value, err := decodeCounter(raw)
			if err != nil {
				return parsedAccount{}, err
			}
			result.observation.Success = value
		case "failed":
			value, err := decodeCounter(raw)
			if err != nil {
				return parsedAccount{}, err
			}
			result.observation.Failed = value
		case "recent_requests":
			var values []json.RawMessage
			if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &values) != nil || values == nil || len(values) > DefaultInventoryRecordLimit {
				return parsedAccount{}, errors.New("contract_invalid")
			}
			result.observation.RecentRequests = len(values)
		case "last_refresh":
			value, err := decodeTime(raw)
			if err != nil {
				return parsedAccount{}, err
			}
			result.observation.LastRefresh = value
		case "next_retry_after":
			value, err := decodeTime(raw)
			if err != nil {
				return parsedAccount{}, err
			}
			result.observation.NextRetryAfter = value
			nextRetry = value
		case "updated_at":
			value, err := decodeTime(raw)
			if err != nil {
				return parsedAccount{}, err
			}
			result.observation.UpdatedAt = value
		default:
			// All non-allowlisted and future fields are discarded at the boundary.
		}
	}

	_, hasStatus := fields["status"]
	_, hasDisabled := fields["disabled"]
	result.diskShape = result.providerSet && result.emailSet && hasStatus && hasDisabled
	result.observation.Status = classifyBaseStatus(disabled, unavailable, upstreamStatus, nextRetry, now)
	return result, nil
}

func decodeString(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || raw[0] != '"' {
		return "", errors.New("contract_invalid")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", errors.New("contract_invalid")
	}
	return value, nil
}

func validIdentityScalar(value string, maximumBytes int) bool {
	return utf8.ValidString(value) && len(value) <= maximumBytes &&
		strings.IndexFunc(value, unicode.IsControl) < 0
}

func decodeBool(raw json.RawMessage) (bool, error) {
	switch string(raw) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, errors.New("contract_invalid")
	}
}

func decodeCounter(raw json.RawMessage) (int64, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value json.Number
	if err := decoder.Decode(&value); err != nil {
		return 0, errors.New("contract_invalid")
	}
	number, err := value.Int64()
	if err != nil || number < 0 || number > maxCounter {
		return 0, errors.New("contract_invalid")
	}
	return number, nil
}

func decodeTime(raw json.RawMessage) (*time.Time, error) {
	if bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	value, err := decodeString(raw)
	if err != nil {
		return nil, err
	}
	if !validIdentityScalar(value, 64) {
		return nil, errors.New("contract_invalid")
	}
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, errors.New("contract_invalid")
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func classifyBaseStatus(disabled, unavailable bool, status string, nextRetry *time.Time, now time.Time) BaseStatus {
	if disabled {
		return BaseStatusDisabled
	}
	if unavailable || nextRetry != nil && nextRetry.After(now) {
		return BaseStatusUnavailable
	}
	if status == "error" {
		return BaseStatusError
	}
	if status == "active" {
		return BaseStatusActive
	}
	return BaseStatusUnknown
}

func classifyMode(records []parsedAccount) (InventoryMode, bool) {
	if len(records) == 0 {
		return InventoryModeDiskFallback, true
	}
	withSource := 0
	for _, record := range records {
		if record.sourceSet {
			withSource++
			if record.observation.Source != "file" && record.observation.Source != "memory" {
				return "", false
			}
		} else if !record.diskShape {
			return "", false
		}
	}
	if withSource == len(records) {
		return InventoryModeRuntime, true
	}
	if withSource == 0 {
		return InventoryModeDiskFallback, true
	}
	return "", false
}

func classifyProviders(observation *InventoryObservation, records []parsedAccount, policy ProviderPolicy) {
	providerIndex := make(map[string]int, len(policy.active))
	for _, provider := range policy.active {
		providerIndex[provider] = len(observation.Providers)
		observation.Providers = append(observation.Providers, ProviderObservation{
			Provider:                 provider,
			ProviderSnapshotComplete: observation.Mode == InventoryModeRuntime,
		})
	}

	keyCounts := make(map[string]int)
	for _, record := range records {
		provider := record.observation.Provider
		if !record.providerSet || provider == "" {
			observation.NodeIdentityComplete = false
			observation.UnidentifiableCount++
			continue
		}
		if _, active := policy.activeSet[provider]; active && (!record.emailSet || record.observation.Email == "") {
			idx := providerIndex[provider]
			observation.Providers[idx].IdentityErrorCount++
			observation.UnidentifiableCount++
			continue
		}
		if _, active := policy.activeSet[provider]; active {
			keyCounts[provider+"\x00"+record.observation.Email]++
		}
	}

	if !observation.NodeIdentityComplete {
		for i := range observation.Providers {
			observation.Providers[i].ProviderSnapshotComplete = false
		}
	}
	for _, record := range records {
		provider := record.observation.Provider
		if _, out := policy.outSet[provider]; out {
			observation.OutOfScopeProviderCount++
			continue
		}
		idx, active := providerIndex[provider]
		if !active {
			if record.providerSet && provider != "" {
				observation.UnsupportedProviderCount++
				observation.Degraded = true
			}
			continue
		}
		if !record.emailSet || record.observation.Email == "" {
			observation.Providers[idx].ProviderSnapshotComplete = false
			continue
		}
		count := keyCounts[provider+"\x00"+record.observation.Email]
		account := record.observation
		account.OccurrenceCount = count
		observation.Accounts = append(observation.Accounts, account)
		observation.Providers[idx].RecordCount++
	}
	for key, count := range keyCounts {
		if count <= 1 {
			continue
		}
		provider := key[:strings.IndexByte(key, 0)]
		idx := providerIndex[provider]
		observation.DuplicateGroupCount++
		observation.Providers[idx].DuplicateGroupCount++
		observation.Providers[idx].ProviderSnapshotComplete = false
	}
	if observation.Mode == InventoryModeDiskFallback {
		for i := range observation.Providers {
			observation.Providers[i].ProviderSnapshotComplete = false
		}
	}
}

func readBounded(reader io.Reader, maxBytes int64) ([]byte, bool, error) {
	if reader == nil || maxBytes <= 0 {
		return nil, false, errors.New("response_invalid")
	}
	limited := &io.LimitedReader{R: reader, N: maxBytes + 1}
	encoded, err := io.ReadAll(limited)
	if err != nil {
		return nil, false, errors.New("response_invalid")
	}
	if int64(len(encoded)) > maxBytes {
		return nil, true, nil
	}
	return encoded, false, nil
}

func decodeJSONObject(encoded []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("response_invalid")
	}
	result := make(map[string]json.RawMessage)
	for decoder.More() {
		keyToken, tokenErr := decoder.Token()
		key, ok := keyToken.(string)
		if tokenErr != nil || !ok {
			return nil, errors.New("response_invalid")
		}
		if _, duplicate := result[key]; duplicate {
			return nil, errors.New("response_invalid")
		}
		var value json.RawMessage
		if err = decoder.Decode(&value); err != nil {
			return nil, errors.New("response_invalid")
		}
		result[key] = value
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, errors.New("response_invalid")
	}
	if token, err = decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("response_invalid")
	}
	return result, nil
}
