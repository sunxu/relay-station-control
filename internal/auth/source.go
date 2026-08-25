package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
)

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

type SourceResolver struct {
	trusted []netip.Prefix
}

func NewSourceResolver(trusted []netip.Prefix) *SourceResolver {
	copyOfTrusted := append([]netip.Prefix(nil), trusted...)
	return &SourceResolver{trusted: copyOfTrusted}
}

func (r *SourceResolver) ClientAddress(request *http.Request) (netip.Addr, error) {
	direct, err := remoteAddress(request.RemoteAddr)
	if err != nil {
		return netip.Addr{}, err
	}
	if !r.isTrusted(direct) {
		return direct.Unmap(), nil
	}

	forwarded := request.Header.Values("X-Forwarded-For")
	if len(forwarded) == 0 {
		return direct.Unmap(), nil
	}
	var chain []netip.Addr
	for _, header := range forwarded {
		for _, value := range strings.Split(header, ",") {
			address, err := netip.ParseAddr(strings.TrimSpace(value))
			if err != nil {
				return netip.Addr{}, errors.New("auth: invalid forwarded source")
			}
			chain = append(chain, address.Unmap())
		}
	}
	if len(chain) == 0 {
		return direct.Unmap(), nil
	}
	// Walk from the application backwards. Trusted hops are infrastructure;
	// the first untrusted hop is the client source observed by the service.
	for index := len(chain) - 1; index >= 0; index-- {
		if !r.isTrusted(chain[index]) {
			return chain[index], nil
		}
	}
	return chain[0], nil
}

func (r *SourceResolver) RequestID(request *http.Request) string {
	direct, err := remoteAddress(request.RemoteAddr)
	if err == nil && r.isTrusted(direct) {
		candidate := request.Header.Get("X-Request-ID")
		if requestIDPattern.MatchString(candidate) {
			return candidate
		}
	}
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		// CSPRNG failure is unrecoverable for request correlation; return a fixed,
		// valid non-secret value rather than accept attacker input.
		return "request-id-unavailable"
	}
	return base64.RawURLEncoding.EncodeToString(value)
}

func (r *SourceResolver) isTrusted(address netip.Addr) bool {
	address = address.Unmap()
	for _, prefix := range r.trusted {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func remoteAddress(value string) (netip.Addr, error) {
	host, _, err := net.SplitHostPort(value)
	if err != nil {
		return netip.Addr{}, errors.New("auth: invalid remote address")
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, errors.New("auth: invalid remote address")
	}
	return address.Unmap(), nil
}
