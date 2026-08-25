package auth

import (
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestSourceResolverTrustBoundary(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("2001:db8:1::/48")}
	resolver := NewSourceResolver(trusted)
	tests := []struct {
		name       string
		remoteAddr string
		forwarded  string
		want       string
		wantErr    bool
	}{
		{name: "direct ignores spoof", remoteAddr: "198.51.100.8:1234", forwarded: "203.0.113.9", want: "198.51.100.8"},
		{name: "trusted single proxy", remoteAddr: "10.0.0.2:443", forwarded: "203.0.113.9", want: "203.0.113.9"},
		{name: "trusted chain", remoteAddr: "10.0.0.2:443", forwarded: "198.51.100.7, 10.0.0.3", want: "198.51.100.7"},
		{name: "untrusted injected left value", remoteAddr: "10.0.0.2:443", forwarded: "192.0.2.99, 198.51.100.7", want: "198.51.100.7"},
		{name: "IPv6 proxy", remoteAddr: "[2001:db8:1::1]:443", forwarded: "2001:db8:2::8", want: "2001:db8:2::8"},
		{name: "IPv4 mapped", remoteAddr: "[::ffff:198.51.100.8]:443", forwarded: "203.0.113.9", want: "198.51.100.8"},
		{name: "malformed trusted header", remoteAddr: "10.0.0.2:443", forwarded: "secret.invalid", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest("GET", "https://control.example.test/", nil)
			request.RemoteAddr = test.remoteAddr
			if test.forwarded != "" {
				request.Header.Set("X-Forwarded-For", test.forwarded)
			}
			address, err := resolver.ClientAddress(request)
			if (err != nil) != test.wantErr {
				t.Fatalf("ClientAddress() error = %v, wantErr %v", err, test.wantErr)
			}
			if err == nil && address.String() != test.want {
				t.Fatalf("ClientAddress() = %s, want %s", address, test.want)
			}
		})
	}
}

func TestRequestIDOnlyAcceptedFromTrustedProxy(t *testing.T) {
	resolver := NewSourceResolver([]netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})
	trusted := httptest.NewRequest("GET", "/", nil)
	trusted.RemoteAddr = "10.0.0.1:443"
	trusted.Header.Set("X-Request-ID", "trusted_request_1234")
	if got := resolver.RequestID(trusted); got != "trusted_request_1234" {
		t.Fatalf("trusted request ID = %q", got)
	}
	untrusted := trusted.Clone(trusted.Context())
	untrusted.RemoteAddr = "198.51.100.8:443"
	if got := resolver.RequestID(untrusted); got == "trusted_request_1234" || !requestIDPattern.MatchString(got) {
		t.Fatalf("untrusted request ID = %q", got)
	}
	trusted.Header.Set("X-Request-ID", strings.Repeat("x", 129))
	if got := resolver.RequestID(trusted); len(got) > 128 || got == trusted.Header.Get("X-Request-ID") {
		t.Fatalf("invalid request ID accepted: %q", got)
	}
}
