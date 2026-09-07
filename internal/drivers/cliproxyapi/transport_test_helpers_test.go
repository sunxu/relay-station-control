package cliproxyapi

import (
	"net"
	"net/netip"
	"strings"

	rootdrivers "github.com/sunxu/relay-station-control/internal/drivers"
)

func mustManagementConfig(t interface {
	Helper()
	Fatalf(string, ...any)
}, _ []string, _ []string, _ []string) rootdrivers.ValidatedManagementConfig {
	t.Helper()
	config, err := (rootdrivers.ManagementConfig{}).Validate()
	if err != nil {
		t.Fatalf("validate management config: %v", err)
	}
	return config
}

// remoteAddress is used by legacy dialer tests; production no longer
// re-validates the connected address against an allowlist.
func remoteAddress(address net.Addr) (netip.Addr, bool) {
	if address == nil {
		return netip.Addr{}, false
	}
	if tcp, ok := address.(*net.TCPAddr); ok {
		addr, ok := netip.AddrFromSlice(tcp.IP)
		return addr.Unmap(), ok
	}
	host, _, err := net.SplitHostPort(address.String())
	if err != nil {
		return netip.Addr{}, false
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	return addr.Unmap(), err == nil
}
