package cliproxyapi

import (
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	rootdrivers "github.com/sunxu/relay-station-control/internal/drivers"
)

// targetPolicy is an immutable copy derived from the shared, validated driver
// configuration. A leading dot in AllowedDNSNames denotes a subdomain-only
// suffix; other values are exact names.
type targetPolicy struct {
	exactDNS  map[string]struct{}
	dnsSuffix []string
	manage    []netip.Prefix
	plainHTTP []netip.Prefix
}

// ConfigurationReason is deliberately closed and contains no configuration
// value, endpoint, hostname, or address.
type ConfigurationReason string

const (
	ConfigurationInvalidDNSName             ConfigurationReason = "invalid_dns_name"
	ConfigurationInvalidDNSSuffix           ConfigurationReason = "invalid_dns_suffix"
	ConfigurationInvalidCIDR                ConfigurationReason = "invalid_cidr"
	ConfigurationUnsafeCIDR                 ConfigurationReason = "unsafe_cidr"
	ConfigurationMissingManagementCIDR      ConfigurationReason = "missing_management_cidr"
	ConfigurationPlainHTTPOutsideManagement ConfigurationReason = "plain_http_outside_management"
	ConfigurationInvalidEndpoint            ConfigurationReason = "invalid_endpoint"
	ConfigurationTargetNotAllowed           ConfigurationReason = "target_not_allowed"
)

// ConfigurationError is safe to expose in diagnostics. It never wraps the
// underlying parser value.
type ConfigurationError struct {
	Reason ConfigurationReason
}

func (e *ConfigurationError) Error() string {
	return "cliproxyapi configuration rejected: " + string(e.Reason)
}

func targetPolicyFromConfig(config rootdrivers.ValidatedManagementConfig) (targetPolicy, error) {
	policy := targetPolicy{exactDNS: make(map[string]struct{})}
	for _, value := range config.AllowedDNSNames {
		if strings.HasPrefix(value, ".") {
			name, ok := normalizeDNSName(strings.TrimPrefix(value, "."))
			if !ok || strings.Contains(value, "*") {
				return targetPolicy{}, &ConfigurationError{Reason: ConfigurationInvalidDNSSuffix}
			}
			policy.dnsSuffix = append(policy.dnsSuffix, "."+name)
			continue
		}
		name, ok := normalizeDNSName(value)
		if !ok || strings.Contains(value, "*") {
			return targetPolicy{}, &ConfigurationError{Reason: ConfigurationInvalidDNSName}
		}
		policy.exactDNS[name] = struct{}{}
	}
	policy.manage = append([]netip.Prefix(nil), config.AllowedManagementPrefixes...)
	policy.plainHTTP = append([]netip.Prefix(nil), config.AllowedPlainHTTPPrefixes...)
	for _, prefix := range append(append([]netip.Prefix(nil), policy.manage...), policy.plainHTTP...) {
		if !prefix.IsValid() || prefix != prefix.Masked() || prefix.Addr().Is4In6() ||
			prefix.Addr().Zone() != "" || prefixOverlapsForbidden(prefix) {
			return targetPolicy{}, &ConfigurationError{Reason: ConfigurationUnsafeCIDR}
		}
	}
	if len(policy.manage) == 0 {
		return targetPolicy{}, &ConfigurationError{Reason: ConfigurationMissingManagementCIDR}
	}
	for _, plain := range policy.plainHTTP {
		if !prefixContainedByAny(plain, policy.manage) {
			return targetPolicy{}, &ConfigurationError{Reason: ConfigurationPlainHTTPOutsideManagement}
		}
	}
	return policy, nil
}

func prefixContainedByAny(candidate netip.Prefix, allowed []netip.Prefix) bool {
	for _, parent := range allowed {
		if parent.Addr().BitLen() == candidate.Addr().BitLen() &&
			parent.Bits() <= candidate.Bits() && parent.Contains(candidate.Addr()) {
			return true
		}
	}
	return false
}

var forbiddenPrefixes = mustPrefixes(
	"0.0.0.0/8",
	"100.100.100.200/32",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"224.0.0.0/4",
	"255.255.255.255/32",
	"::/128",
	"::1/128",
	"fe80::/10",
	"ff00::/8",
	"fd00:ec2::254/128",
)

func mustPrefixes(values ...string) []netip.Prefix {
	result := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		result = append(result, netip.MustParsePrefix(value))
	}
	return result
}

func prefixOverlapsForbidden(prefix netip.Prefix) bool {
	for _, forbidden := range forbiddenPrefixes {
		if prefix.Addr().BitLen() != forbidden.Addr().BitLen() {
			continue
		}
		if prefix.Contains(forbidden.Addr()) || forbidden.Contains(prefix.Addr()) {
			return true
		}
	}
	return false
}

func forbiddenAddress(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.Zone() != "" || addr.IsUnspecified() || addr.IsLoopback() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() {
		return true
	}
	for _, prefix := range forbiddenPrefixes {
		if prefix.Addr().BitLen() == addr.BitLen() && prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func (p targetPolicy) dnsAllowed(host string) bool {
	if _, ok := p.exactDNS[host]; ok {
		return true
	}
	for _, suffix := range p.dnsSuffix {
		if strings.HasSuffix(host, suffix) && len(host) > len(suffix) {
			return true
		}
	}
	return false
}

func (p targetPolicy) addressAllowed(addr netip.Addr, plainHTTP bool) bool {
	addr = addr.Unmap()
	if forbiddenAddress(addr) || !addressInPrefixes(addr, p.manage) {
		return false
	}
	return !plainHTTP || addressInPrefixes(addr, p.plainHTTP)
}

func addressInPrefixes(addr netip.Addr, prefixes []netip.Prefix) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

type validatedEndpoint struct {
	scheme   string
	hostname string
	port     string
	basePath string
	ip       netip.Addr
}

func validateEndpoint(raw string, policy targetPolicy) (validatedEndpoint, error) {
	if raw == "" || raw != strings.TrimSpace(raw) {
		return validatedEndpoint{}, &ConfigurationError{Reason: ConfigurationInvalidEndpoint}
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Opaque != "" || parsed.User != nil || parsed.Host == "" ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return validatedEndpoint{}, &ConfigurationError{Reason: ConfigurationInvalidEndpoint}
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return validatedEndpoint{}, &ConfigurationError{Reason: ConfigurationInvalidEndpoint}
	}
	hostname := parsed.Hostname()
	if hostname == "" {
		return validatedEndpoint{}, &ConfigurationError{Reason: ConfigurationInvalidEndpoint}
	}

	var literal netip.Addr
	if candidate, parseErr := netip.ParseAddr(hostname); parseErr == nil {
		literal = candidate.Unmap()
		if candidate.Is4In6() || candidate.Zone() != "" || !policy.addressAllowed(literal, scheme == "http") {
			return validatedEndpoint{}, &ConfigurationError{Reason: ConfigurationTargetNotAllowed}
		}
		hostname = literal.String()
	} else {
		var ok bool
		hostname, ok = normalizeDNSName(hostname)
		if !ok || !policy.dnsAllowed(hostname) {
			return validatedEndpoint{}, &ConfigurationError{Reason: ConfigurationTargetNotAllowed}
		}
	}

	port := parsed.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	} else {
		value, parseErr := strconv.ParseUint(port, 10, 16)
		if parseErr != nil || value == 0 {
			return validatedEndpoint{}, &ConfigurationError{Reason: ConfigurationInvalidEndpoint}
		}
		port = strconv.FormatUint(value, 10)
	}
	basePath, ok := normalizeBasePath(parsed.EscapedPath())
	if !ok {
		return validatedEndpoint{}, &ConfigurationError{Reason: ConfigurationInvalidEndpoint}
	}
	return validatedEndpoint{scheme: scheme, hostname: hostname, port: port, basePath: basePath, ip: literal}, nil
}

func normalizeDNSName(value string) (string, bool) {
	if value == "" || value != strings.TrimSpace(value) || strings.ContainsAny(value, "*%/\\:@[]") {
		return "", false
	}
	if strings.HasSuffix(value, ".") {
		value = strings.TrimSuffix(value, ".")
	}
	value = strings.ToLower(value)
	if value == "" || len(value) > 253 {
		return "", false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
				return "", false
			}
		}
	}
	return value, true
}

func normalizeBasePath(escaped string) (string, bool) {
	if escaped == "" || escaped == "/" {
		return "", true
	}
	// Base paths are configuration, not user input. Limiting them to unescaped
	// RFC 3986 unreserved characters avoids encoded traversal and ambiguous
	// normalization at reverse proxies.
	if !strings.HasPrefix(escaped, "/") || strings.HasSuffix(escaped, "/") || strings.Contains(escaped, "//") {
		return "", false
	}
	for _, segment := range strings.Split(strings.TrimPrefix(escaped, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", false
		}
		for _, char := range segment {
			if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') &&
				(char < '0' || char > '9') && !strings.ContainsRune("-._~", char) {
				return "", false
			}
		}
	}
	return escaped, true
}
