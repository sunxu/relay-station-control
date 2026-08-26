package cliproxyapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	rootdrivers "github.com/sunxu/relay-station-control/internal/drivers"
)

const (
	DefaultConnectTimeout = 3 * time.Second
	DefaultRequestTimeout = 15 * time.Second

	healthPath    = "/healthz"
	inventoryPath = "/v0/management/auth-files"
)

type Operation string

const (
	OperationHealth    Operation = "health"
	OperationInventory Operation = "account_inventory"
)

type FailureReason string

const (
	FailureTargetRejected     FailureReason = "target_rejected"
	FailureDNSRejected        FailureReason = "dns_rejected"
	FailureNetworkUnavailable FailureReason = "network_unavailable"
	FailureTLSRejected        FailureReason = "tls_rejected"
	FailureRedirectRejected   FailureReason = "redirect_rejected"
	FailureTimeout            FailureReason = "timeout"
	FailureCancelled          FailureReason = "cancelled"
	FailureRequestRejected    FailureReason = "request_rejected"
)

// RequestError exposes only closed enums. It deliberately does not implement
// Unwrap and never includes a URL, host, IP, certificate, or raw network error.
type RequestError struct {
	Operation Operation
	Reason    FailureReason
}

func (e *RequestError) Error() string {
	return "cliproxyapi request failed: " + string(e.Operation) + ": " + string(e.Reason)
}

// Resolver is intentionally the narrow subset used by the secure dialer.
type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

// ContextDialer is the narrow net.Dialer-compatible interface used after DNS
// and CIDR authorization. Implementations used in production must not resolve
// the supplied address again; safeTransport always supplies an IP literal.
type ContextDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type transportOptions struct {
	RootCAs  *x509.CertPool
	Resolver Resolver
	Dialer   ContextDialer
}

// httpResponse is the minimal response projection required by the bounded
// parsers. Request metadata, arbitrary response headers, cookies, and the URL
// are intentionally unavailable.
type httpResponse struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
}

// safeTransport is bound to one endpoint and can express only the two approved
// read-only operations.
type safeTransport struct {
	endpointRaw    string
	endpoint       validatedEndpoint
	policy         targetPolicy
	resolver       Resolver
	dialer         ContextDialer
	connectTimeout time.Duration
	healthLimit    int64
	inventoryLimit int64
	client         *http.Client
}

type defaultResolver struct{}

func (defaultResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, network, host)
}

// newSecureTransport performs startup validation and constructs an isolated HTTP
// stack. It never consults proxy environment variables and never shares a
// cookie jar or the process-wide default transport.
func newSecureTransport(endpoint string, config rootdrivers.ValidatedManagementConfig, options transportOptions) (*safeTransport, error) {
	policy, err := targetPolicyFromConfig(config)
	if err != nil {
		return nil, err
	}
	validated, err := validateEndpoint(endpoint, policy)
	if err != nil {
		return nil, err
	}
	connectTimeout, requestTimeout, err := validatedTimeouts(config.ConnectTimeout, config.RequestTimeout)
	if err != nil {
		return nil, err
	}
	resolver := options.Resolver
	if resolver == nil {
		resolver = defaultResolver{}
	}
	dialer := options.Dialer
	if dialer == nil {
		dialer = &net.Dialer{Timeout: connectTimeout, KeepAlive: -1}
	}
	result := &safeTransport{
		endpointRaw:    endpoint,
		endpoint:       validated,
		policy:         policy,
		resolver:       resolver,
		dialer:         dialer,
		connectTimeout: connectTimeout,
		healthLimit:    config.MaxHealthResponseBytes,
		inventoryLimit: config.MaxInventoryResponseBytes,
	}
	if result.healthLimit <= 0 {
		result.healthLimit = rootdrivers.DefaultHealthResponseBytes
	}
	if result.inventoryLimit <= 0 {
		result.inventoryLimit = rootdrivers.DefaultInventoryResponseBytes
	}
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    options.RootCAs,
		ServerName: validated.hostname,
	}
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            result.dialContext,
		ForceAttemptHTTP2:      false,
		DisableKeepAlives:      true,
		DisableCompression:     true,
		MaxConnsPerHost:        1,
		IdleConnTimeout:        time.Second,
		TLSHandshakeTimeout:    connectTimeout,
		ResponseHeaderTimeout:  requestTimeout,
		ExpectContinueTimeout:  0,
		MaxResponseHeaderBytes: 64 << 10,
		TLSClientConfig:        tlsConfig,
		TLSNextProto:           make(map[string]func(string, *tls.Conn) http.RoundTripper),
	}
	result.client = &http.Client{
		Transport: transport,
		Timeout:   requestTimeout,
		Jar:       nil,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errRedirectRejected
		},
	}
	return result, nil
}

func validatedTimeouts(connect, request time.Duration) (time.Duration, time.Duration, error) {
	if connect == 0 {
		connect = DefaultConnectTimeout
	}
	if request == 0 {
		request = DefaultRequestTimeout
	}
	if connect < 100*time.Millisecond || connect > 10*time.Second ||
		request < time.Second || request > 30*time.Second || request < connect {
		return 0, 0, &ConfigurationError{Reason: ConfigurationInvalidEndpoint}
	}
	return connect, request, nil
}

// getHealth performs exactly one GET to the fixed health path. It never accepts
// or attaches a management key.
func (t *safeTransport) getHealth(ctx context.Context) (*httpResponse, error) {
	return t.get(ctx, OperationHealth, "")
}

// getAccountInventory performs exactly one GET to the fixed auth-files path.
// The management key is placed only in X-Management-Key for this request.
func (t *safeTransport) getAccountInventory(ctx context.Context, managementKey string) (*httpResponse, error) {
	return t.get(ctx, OperationInventory, managementKey)
}

func (t *safeTransport) get(ctx context.Context, operation Operation, managementKey string) (*httpResponse, error) {
	if ctx == nil {
		return nil, &RequestError{Operation: operation, Reason: FailureRequestRejected}
	}
	// Revalidate the configured endpoint immediately before attaching a secret.
	// The value is immutable inside safeTransport, but doing this again keeps the
	// security check adjacent to request construction and prevents future config
	// reload work from accidentally bypassing it.
	endpoint, err := validateEndpoint(t.endpointRaw, t.policy)
	if err != nil || endpoint != t.endpoint {
		return nil, &RequestError{Operation: operation, Reason: FailureTargetRejected}
	}
	path := ""
	switch operation {
	case OperationHealth:
		path = healthPath
		if managementKey != "" {
			return nil, &RequestError{Operation: operation, Reason: FailureRequestRejected}
		}
	case OperationInventory:
		path = inventoryPath
		if managementKey == "" {
			return nil, &RequestError{Operation: operation, Reason: FailureRequestRejected}
		}
	default:
		return nil, &RequestError{Operation: operation, Reason: FailureRequestRejected}
	}
	host := net.JoinHostPort(endpoint.hostname, endpoint.port)
	requestURL := &url.URL{Scheme: endpoint.scheme, Host: host, Path: endpoint.basePath + path}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, &RequestError{Operation: operation, Reason: FailureRequestRejected}
	}
	request.Header.Set("User-Agent", "relay-station-control/cliproxyapi-readonly")
	if operation == OperationInventory {
		request.Header.Set("X-Management-Key", managementKey)
	}
	response, err := t.client.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, classifyRequestError(operation, ctx, err)
	}
	if response.StatusCode >= http.StatusMultipleChoices && response.StatusCode < http.StatusBadRequest {
		_ = response.Body.Close()
		return nil, &RequestError{Operation: operation, Reason: FailureRedirectRejected}
	}
	projectedHeader := http.Header{
		"X-Cpa-Version": append([]string(nil), response.Header.Values("X-CPA-VERSION")...),
		"X-Cpa-Commit":  append([]string(nil), response.Header.Values("X-CPA-COMMIT")...),
	}
	// Non-200 bodies are never useful to the closed parsers and can contain
	// arbitrary upstream diagnostics. Close them without reading. For a 200,
	// finish a max+1 bounded read while the client's total timeout is still in
	// force. This prevents a slow body from escaping as a raw parser error and
	// keeps the management Secret callback scoped to the complete exchange.
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return &httpResponse{StatusCode: response.StatusCode, Header: projectedHeader, Body: http.NoBody}, nil
	}
	limit := t.healthLimit
	if operation == OperationInventory {
		limit = t.inventoryLimit
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
	closeErr := response.Body.Close()
	if readErr != nil {
		return nil, classifyRequestError(operation, ctx, readErr)
	}
	if closeErr != nil {
		return nil, &RequestError{Operation: operation, Reason: FailureNetworkUnavailable}
	}
	return &httpResponse{
		StatusCode: response.StatusCode,
		Header:     projectedHeader,
		Body:       io.NopCloser(bytes.NewReader(body)),
	}, nil
}

var errRedirectRejected = errors.New("redirect rejected")

func classifyRequestError(operation Operation, ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return &RequestError{Operation: operation, Reason: FailureCancelled}
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return &RequestError{Operation: operation, Reason: FailureTimeout}
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return &RequestError{Operation: operation, Reason: FailureTimeout}
	}
	if errors.Is(err, errRedirectRejected) {
		return &RequestError{Operation: operation, Reason: FailureRedirectRejected}
	}
	var failure *dialFailure
	if errors.As(err, &failure) {
		return &RequestError{Operation: operation, Reason: failure.reason}
	}
	var certificateVerification *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameError x509.HostnameError
	var certificateInvalid x509.CertificateInvalidError
	if errors.As(err, &certificateVerification) || errors.As(err, &unknownAuthority) ||
		errors.As(err, &hostnameError) || errors.As(err, &certificateInvalid) {
		return &RequestError{Operation: operation, Reason: FailureTLSRejected}
	}
	return &RequestError{Operation: operation, Reason: FailureNetworkUnavailable}
}

type dialFailure struct {
	reason FailureReason
}

func (e *dialFailure) Error() string { return "secure dial failed: " + string(e.reason) }

func (t *safeTransport) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	endpoint, err := validateEndpoint(t.endpointRaw, t.policy)
	if err != nil || endpoint != t.endpoint || (network != "tcp" && network != "tcp4" && network != "tcp6") {
		return nil, &dialFailure{reason: FailureTargetRejected}
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || port != endpoint.port || !sameEndpointHost(host, endpoint) {
		return nil, &dialFailure{reason: FailureTargetRejected}
	}
	connectContext, cancel := context.WithTimeout(ctx, t.connectTimeout)
	defer cancel()
	addresses, failure := t.resolveAndAuthorize(connectContext, endpoint)
	if failure != nil {
		return nil, failure
	}
	for _, allowed := range addresses {
		connection, dialErr := t.dialer.DialContext(connectContext, "tcp", net.JoinHostPort(allowed.String(), port))
		if dialErr != nil {
			if connectContext.Err() != nil {
				return nil, &dialFailure{reason: timeoutOrCancelled(connectContext)}
			}
			if isTimeoutError(dialErr) {
				return nil, &dialFailure{reason: FailureTimeout}
			}
			continue
		}
		remote, ok := remoteAddress(connection.RemoteAddr())
		if !ok || remote.Unmap() != allowed.Unmap() || !endpointAddressAllowed(endpoint, t.policy, remote) {
			_ = connection.Close()
			return nil, &dialFailure{reason: FailureTargetRejected}
		}
		return connection, nil
	}
	if connectContext.Err() != nil {
		return nil, &dialFailure{reason: timeoutOrCancelled(connectContext)}
	}
	return nil, &dialFailure{reason: FailureNetworkUnavailable}
}

func timeoutOrCancelled(ctx context.Context) FailureReason {
	if errors.Is(ctx.Err(), context.Canceled) {
		return FailureCancelled
	}
	return FailureTimeout
}

func sameEndpointHost(host string, endpoint validatedEndpoint) bool {
	if addr, err := netip.ParseAddr(host); err == nil {
		return endpoint.ip.IsValid() && addr.Unmap() == endpoint.ip
	}
	normalized, ok := normalizeDNSName(host)
	return ok && !endpoint.ip.IsValid() && normalized == endpoint.hostname
}

func (t *safeTransport) resolveAndAuthorize(ctx context.Context, endpoint validatedEndpoint) ([]netip.Addr, *dialFailure) {
	var addresses []netip.Addr
	if endpoint.ip.IsValid() {
		addresses = []netip.Addr{endpoint.ip}
	} else {
		resolved, err := t.resolver.LookupNetIP(ctx, "ip", endpoint.hostname)
		if err != nil {
			if ctx.Err() != nil {
				return nil, &dialFailure{reason: timeoutOrCancelled(ctx)}
			}
			if isTimeoutError(err) {
				return nil, &dialFailure{reason: FailureTimeout}
			}
			return nil, &dialFailure{reason: FailureDNSRejected}
		}
		addresses = resolved
	}
	if len(addresses) == 0 {
		return nil, &dialFailure{reason: FailureDNSRejected}
	}
	unique := make([]netip.Addr, 0, len(addresses))
	seen := make(map[netip.Addr]struct{}, len(addresses))
	for _, addr := range addresses {
		if addr.Is4In6() || addr.Zone() != "" {
			return nil, &dialFailure{reason: FailureDNSRejected}
		}
		addr = addr.Unmap()
		if !endpointAddressAllowed(endpoint, t.policy, addr) {
			return nil, &dialFailure{reason: FailureTargetRejected}
		}
		if _, exists := seen[addr]; !exists {
			seen[addr] = struct{}{}
			unique = append(unique, addr)
		}
	}
	return unique, nil
}

func isTimeoutError(err error) bool {
	var networkError net.Error
	return errors.As(err, &networkError) && networkError.Timeout()
}

func endpointAddressAllowed(endpoint validatedEndpoint, policy targetPolicy, addr netip.Addr) bool {
	return policy.addressAllowed(addr, endpoint.scheme == "http")
}

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
