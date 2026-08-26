package cliproxyapi

import (
	"net/netip"
	"strings"
	"testing"

	rootdrivers "github.com/sunxu/relay-station-control/internal/drivers"
)

func TestTargetPolicyDNSAndEndpointNormalization(t *testing.T) {
	config := mustManagementConfig(t, []string{"node.example.invalid", ".nodes.example.invalid"},
		[]string{"10.42.0.0/16", "fd12:3456:789a::/64"}, []string{"10.42.0.0/24"})
	policy, err := targetPolicyFromConfig(config)
	if err != nil {
		t.Fatalf("target policy: %v", err)
	}

	tests := []struct {
		name     string
		endpoint string
		wantHost string
		wantPath string
		ok       bool
	}{
		{name: "exact uppercase trailing dot", endpoint: "https://NODE.EXAMPLE.INVALID.:8443/management", wantHost: "node.example.invalid", wantPath: "/management", ok: true},
		{name: "suffix subdomain", endpoint: "https://a.nodes.example.invalid", wantHost: "a.nodes.example.invalid", ok: true},
		{name: "suffix does not include apex", endpoint: "https://nodes.example.invalid", ok: false},
		{name: "unregistered name", endpoint: "https://other.example.invalid", ok: false},
		{name: "unicode idn rejected", endpoint: "https://nöde.example.invalid", ok: false},
		{name: "encoded idn rejected", endpoint: "https://xn--nde-mna.example.invalid", ok: false},
		{name: "query rejected", endpoint: "https://node.example.invalid?path=/healthz", ok: false},
		{name: "fragment rejected", endpoint: "https://node.example.invalid/#x", ok: false},
		{name: "userinfo rejected", endpoint: "https://key@node.example.invalid", ok: false},
		{name: "escaped traversal rejected", endpoint: "https://node.example.invalid/base/%2e%2e", ok: false},
		{name: "double slash rejected", endpoint: "https://node.example.invalid/base//nested", ok: false},
		{name: "unsupported scheme", endpoint: "ftp://node.example.invalid", ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			endpoint, endpointErr := validateEndpoint(test.endpoint, policy)
			if (endpointErr == nil) != test.ok {
				t.Fatalf("validate endpoint error = %v, want ok=%v", endpointErr, test.ok)
			}
			if test.ok && (endpoint.hostname != test.wantHost || endpoint.basePath != test.wantPath) {
				t.Fatalf("endpoint = %#v", endpoint)
			}
		})
	}
}

func TestTargetPolicyIPLiteralAndPlainHTTP(t *testing.T) {
	config := mustManagementConfig(t, []string{"unused.example.invalid"},
		[]string{"10.42.0.0/16", "fd12:3456:789a::/64"}, []string{"10.42.0.0/24"})
	policy, err := targetPolicyFromConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		endpoint string
		ok       bool
	}{
		{endpoint: "https://10.42.200.4:9443", ok: true},
		{endpoint: "http://10.42.0.8:8080", ok: true},
		{endpoint: "http://10.42.2.8:8080", ok: false},
		{endpoint: "https://[fd12:3456:789a::8]:9443", ok: true},
		{endpoint: "http://[fd12:3456:789a::8]:8080", ok: false},
		{endpoint: "https://10.43.0.8", ok: false},
		{endpoint: "https://[::ffff:10.42.0.8]", ok: false},
	}
	for _, test := range tests {
		_, endpointErr := validateEndpoint(test.endpoint, policy)
		if (endpointErr == nil) != test.ok {
			t.Errorf("validateEndpoint(%q) error=%v, want ok=%v", test.endpoint, endpointErr, test.ok)
		}
	}
}

func TestTargetPolicyRejectsUnsafeAndContradictoryValidatedConfig(t *testing.T) {
	tests := []struct {
		name   string
		manage []netip.Prefix
		plain  []netip.Prefix
	}{
		{name: "wide ipv4", manage: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}},
		{name: "loopback", manage: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}},
		{name: "link local", manage: []netip.Prefix{netip.MustParsePrefix("169.254.0.0/16")}},
		{name: "metadata", manage: []netip.Prefix{netip.MustParsePrefix("100.100.100.200/32")}},
		{name: "multicast", manage: []netip.Prefix{netip.MustParsePrefix("224.0.0.0/4")}},
		{name: "wide ipv6", manage: []netip.Prefix{netip.MustParsePrefix("::/0")}},
		{name: "ipv6 metadata", manage: []netip.Prefix{netip.MustParsePrefix("fd00:ec2::254/128")}},
		{name: "plain outside management", manage: []netip.Prefix{netip.MustParsePrefix("10.42.0.0/16")}, plain: []netip.Prefix{netip.MustParsePrefix("10.43.0.0/16")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := targetPolicyFromConfig(rootdrivers.ValidatedManagementConfig{
				AllowedDNSNames:           []string{"node.example.invalid"},
				AllowedManagementPrefixes: test.manage,
				AllowedPlainHTTPPrefixes:  test.plain,
				ConnectTimeout:            rootdrivers.DefaultConnectTimeout,
				RequestTimeout:            rootdrivers.DefaultRequestTimeout,
			})
			if err == nil {
				t.Fatal("unsafe policy accepted")
			}
			if strings.Contains(err.Error(), test.manage[0].String()) {
				t.Fatalf("diagnostic leaked CIDR: %v", err)
			}
		})
	}
}

func TestForbiddenAddressClosedMatrix(t *testing.T) {
	for _, value := range []string{
		"0.0.0.0", "127.0.0.1", "169.254.1.1", "169.254.169.254",
		"100.100.100.200", "224.0.0.1", "255.255.255.255",
		"::", "::1", "fe80::1", "ff02::1", "fd00:ec2::254",
	} {
		if !forbiddenAddress(netip.MustParseAddr(value)) {
			t.Errorf("forbidden address accepted: %s", value)
		}
	}
	for _, value := range []string{"10.42.0.8", "192.168.4.2", "fd12:3456:789a::8"} {
		if forbiddenAddress(netip.MustParseAddr(value)) {
			t.Errorf("management address rejected as special: %s", value)
		}
	}
}

func TestSharedConfigurationRejectsNonCanonicalCIDRAndAmbiguousDNS(t *testing.T) {
	for _, mutate := range []func(*rootdrivers.ManagementConfig){
		func(config *rootdrivers.ManagementConfig) { config.AllowedManagementCIDRs = []string{"10.42.1.1/16"} },
		func(config *rootdrivers.ManagementConfig) { config.AllowedDNSNames = []string{"*.example.invalid"} },
		func(config *rootdrivers.ManagementConfig) { config.AllowedDNSNames = []string{"N.example.invalid"} },
		func(config *rootdrivers.ManagementConfig) { config.AllowedDNSNames = []string{"n.example.invalid."} },
	} {
		config := rootdrivers.ManagementConfig{
			AllowedDNSNames:        []string{"node.example.invalid"},
			AllowedManagementCIDRs: []string{"10.42.0.0/16"},
		}
		mutate(&config)
		if _, err := config.Validate(); err == nil {
			t.Fatal("invalid shared configuration accepted")
		}
	}
}

func mustManagementConfig(t *testing.T, names, management, plain []string) rootdrivers.ValidatedManagementConfig {
	t.Helper()
	config, err := (rootdrivers.ManagementConfig{
		AllowedDNSNames:        names,
		AllowedManagementCIDRs: management,
		AllowedPlainHTTPCIDRs:  plain,
	}).Validate()
	if err != nil {
		t.Fatalf("validate management config: %v", err)
	}
	return config
}
