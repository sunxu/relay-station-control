package drivers

import (
	"errors"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"time"
)

const (
	DefaultConnectTimeout         = 3 * time.Second
	DefaultRequestTimeout         = 15 * time.Second
	DefaultHealthResponseBytes    = int64(64 << 10)
	DefaultInventoryResponseBytes = int64(5 << 20)
	DefaultInventoryRecords       = 1000
	DefaultSecretBytes            = int64(4 << 10)
	DefaultSecretMappingBytes     = int64(64 << 10)

	minimumConnectTimeout         = 100 * time.Millisecond
	maximumConnectTimeout         = 10 * time.Second
	minimumRequestTimeout         = time.Second
	maximumRequestTimeout         = 30 * time.Second
	minimumHealthResponseBytes    = int64(128)
	maximumHealthResponseBytes    = int64(1 << 20)
	minimumInventoryResponseBytes = int64(1024)
	maximumInventoryResponseBytes = int64(5 << 20)
	minimumSecretBytes            = int64(1)
	maximumSecretBytes            = int64(64 << 10)
	maximumSecretMappingBytes     = int64(1 << 20)
)

var (
	ErrInvalidManagementConfig = errors.New("node driver: invalid management configuration")
	dnsNamePattern             = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$`)
)

// ManagementConfig is startup configuration, not per-call input. CIDRs must be
// canonical strings so host bits cannot be silently discarded.
type ManagementConfig struct {
	AllowedDNSNames           []string
	AllowedManagementCIDRs    []string
	AllowedPlainHTTPCIDRs     []string
	ConnectTimeout            time.Duration
	RequestTimeout            time.Duration
	MaxHealthResponseBytes    int64
	MaxInventoryResponseBytes int64
	MaxInventoryRecords       int
	MaxSecretBytes            int64
	MaxSecretMappingBytes     int64
}

type ValidatedManagementConfig struct {
	AllowedDNSNames           []string
	AllowedManagementPrefixes []netip.Prefix
	AllowedPlainHTTPPrefixes  []netip.Prefix
	ConnectTimeout            time.Duration
	RequestTimeout            time.Duration
	MaxHealthResponseBytes    int64
	MaxInventoryResponseBytes int64
	MaxInventoryRecords       int
	MaxSecretBytes            int64
	MaxSecretMappingBytes     int64
}

func (configuration ManagementConfig) Validate() (ValidatedManagementConfig, error) {
	configuration.applyDefaults()
	if configuration.ConnectTimeout < minimumConnectTimeout || configuration.ConnectTimeout > maximumConnectTimeout ||
		configuration.RequestTimeout < minimumRequestTimeout || configuration.RequestTimeout > maximumRequestTimeout ||
		configuration.RequestTimeout < configuration.ConnectTimeout ||
		configuration.MaxHealthResponseBytes < minimumHealthResponseBytes || configuration.MaxHealthResponseBytes > maximumHealthResponseBytes ||
		configuration.MaxInventoryResponseBytes < minimumInventoryResponseBytes || configuration.MaxInventoryResponseBytes > maximumInventoryResponseBytes ||
		configuration.MaxInventoryRecords < 1 || configuration.MaxInventoryRecords > DefaultInventoryRecords ||
		configuration.MaxSecretBytes < minimumSecretBytes || configuration.MaxSecretBytes > maximumSecretBytes ||
		configuration.MaxSecretMappingBytes < configuration.MaxSecretBytes || configuration.MaxSecretMappingBytes > maximumSecretMappingBytes {
		return ValidatedManagementConfig{}, ErrInvalidManagementConfig
	}

	dnsNames, err := validateDNSNames(configuration.AllowedDNSNames)
	if err != nil {
		return ValidatedManagementConfig{}, err
	}
	management, err := validateCIDRs(configuration.AllowedManagementCIDRs)
	if err != nil || len(management) == 0 {
		return ValidatedManagementConfig{}, ErrInvalidManagementConfig
	}
	plainHTTP, err := validateCIDRs(configuration.AllowedPlainHTTPCIDRs)
	if err != nil {
		return ValidatedManagementConfig{}, err
	}
	for _, candidate := range plainHTTP {
		if !prefixContainedByAny(candidate, management) {
			return ValidatedManagementConfig{}, ErrInvalidManagementConfig
		}
	}

	return ValidatedManagementConfig{
		AllowedDNSNames:           append([]string(nil), dnsNames...),
		AllowedManagementPrefixes: append([]netip.Prefix(nil), management...),
		AllowedPlainHTTPPrefixes:  append([]netip.Prefix(nil), plainHTTP...),
		ConnectTimeout:            configuration.ConnectTimeout,
		RequestTimeout:            configuration.RequestTimeout,
		MaxHealthResponseBytes:    configuration.MaxHealthResponseBytes,
		MaxInventoryResponseBytes: configuration.MaxInventoryResponseBytes,
		MaxInventoryRecords:       configuration.MaxInventoryRecords,
		MaxSecretBytes:            configuration.MaxSecretBytes,
		MaxSecretMappingBytes:     configuration.MaxSecretMappingBytes,
	}, nil
}

func (configuration *ManagementConfig) applyDefaults() {
	if configuration.ConnectTimeout == 0 {
		configuration.ConnectTimeout = DefaultConnectTimeout
	}
	if configuration.RequestTimeout == 0 {
		configuration.RequestTimeout = DefaultRequestTimeout
	}
	if configuration.MaxHealthResponseBytes == 0 {
		configuration.MaxHealthResponseBytes = DefaultHealthResponseBytes
	}
	if configuration.MaxInventoryResponseBytes == 0 {
		configuration.MaxInventoryResponseBytes = DefaultInventoryResponseBytes
	}
	if configuration.MaxInventoryRecords == 0 {
		configuration.MaxInventoryRecords = DefaultInventoryRecords
	}
	if configuration.MaxSecretBytes == 0 {
		configuration.MaxSecretBytes = DefaultSecretBytes
	}
	if configuration.MaxSecretMappingBytes == 0 {
		configuration.MaxSecretMappingBytes = DefaultSecretMappingBytes
	}
}

func validateDNSNames(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, ErrInvalidManagementConfig
	}
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || value != strings.ToLower(value) || strings.HasSuffix(value, ".") || strings.Contains(value, "*") {
			return nil, ErrInvalidManagementConfig
		}
		candidate := value
		if strings.HasPrefix(candidate, ".") {
			candidate = candidate[1:]
		}
		if len(candidate) > 253 || !dnsNamePattern.MatchString(candidate) || candidate == "localhost" || strings.HasSuffix(candidate, ".localhost") {
			return nil, ErrInvalidManagementConfig
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, ErrInvalidManagementConfig
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	return normalized, nil
}

func validateCIDRs(values []string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, 0, len(values))
	seen := make(map[netip.Prefix]struct{}, len(values))
	for _, value := range values {
		prefix, err := netip.ParsePrefix(value)
		if err != nil || prefix != prefix.Masked() || prefix.Addr().Is4In6() || prefixContainsForbiddenAddress(prefix) {
			return nil, ErrInvalidManagementConfig
		}
		if _, duplicate := seen[prefix]; duplicate {
			return nil, ErrInvalidManagementConfig
		}
		seen[prefix] = struct{}{}
		prefixes = append(prefixes, prefix)
	}
	slices.SortFunc(prefixes, func(left, right netip.Prefix) int { return strings.Compare(left.String(), right.String()) })
	return prefixes, nil
}

func prefixContainsForbiddenAddress(prefix netip.Prefix) bool {
	for _, forbidden := range forbiddenPrefixes {
		if prefix.Addr().BitLen() == forbidden.Addr().BitLen() && prefixesOverlap(prefix, forbidden) {
			return true
		}
	}
	return false
}

var forbiddenPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("100.100.100.200/32"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

func prefixesOverlap(left, right netip.Prefix) bool {
	return left.Contains(right.Addr()) || right.Contains(left.Addr())
}

func prefixContainedByAny(candidate netip.Prefix, allowed []netip.Prefix) bool {
	for _, outer := range allowed {
		if candidate.Addr().BitLen() == outer.Addr().BitLen() && outer.Bits() <= candidate.Bits() && outer.Contains(candidate.Addr()) {
			return true
		}
	}
	return false
}
