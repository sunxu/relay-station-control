package cliproxyapi

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	rootdrivers "github.com/sunxu/relay-station-control/internal/drivers"
)

var authorizedTestIP = netip.MustParseAddr("10.42.0.8")

type resolverResult struct {
	addresses []netip.Addr
	err       error
}

type sequenceResolver struct {
	mu      sync.Mutex
	results []resolverResult
	calls   int
}

func (r *sequenceResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if len(r.results) == 0 {
		return nil, nil
	}
	index := r.calls - 1
	if index >= len(r.results) {
		index = len(r.results) - 1
	}
	result := r.results[index]
	return append([]netip.Addr(nil), result.addresses...), result.err
}

func (r *sequenceResolver) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

type mappedDialer struct {
	actual     string
	advertised netip.Addr
	wait       bool
	mu         sync.Mutex
	addresses  []string
}

func (d *mappedDialer) DialContext(ctx context.Context, _, address string) (net.Conn, error) {
	d.mu.Lock()
	d.addresses = append(d.addresses, address)
	d.mu.Unlock()
	if d.wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", d.actual)
	if err != nil {
		return nil, err
	}
	return &reportedRemoteConn{Conn: connection, remote: &net.TCPAddr{IP: net.IP(d.advertised.AsSlice())}}, nil
}

func (d *mappedDialer) callCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.addresses)
}

type reportedRemoteConn struct {
	net.Conn
	remote net.Addr
}

func (c *reportedRemoteConn) RemoteAddr() net.Addr { return c.remote }

func TestSecureTransportFixedGETNoProxyNoCookiesAndDNSPerConnection(t *testing.T) {
	var proxyRequests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		proxyRequests.Add(1)
	}))
	defer proxy.Close()
	for _, variable := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(variable, proxy.URL)
	}

	var requests atomic.Int32
	var inventoryKey string
	var healthCookie string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Method != http.MethodGet || request.Body != http.NoBody || request.URL.RawQuery != "" {
			t.Errorf("unexpected request shape: method=%q query=%q body=%T", request.Method, request.URL.RawQuery, request.Body)
		}
		switch request.URL.Path {
		case "/base/healthz":
			healthCookie = request.Header.Get("Cookie")
			if got := request.Header.Get("X-Management-Key"); got != "" {
				t.Errorf("health received management key")
			}
			_, _ = io.WriteString(response, `{"status":"ok"}`)
		case "/base/v0/management/auth-files":
			inventoryKey = request.Header.Get("X-Management-Key")
			http.SetCookie(response, &http.Cookie{Name: "management", Value: "must-not-return"})
			response.Header().Set("X-CPA-VERSION", "v7.2.141")
			response.Header().Set("X-CPA-COMMIT", "abcdef1")
			response.Header().Set("X-Sensitive-Upstream", "discarded")
			_, _ = io.WriteString(response, `{"files":[]}`)
		default:
			t.Errorf("unexpected path %q", request.URL.Path)
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	resolver := &sequenceResolver{results: []resolverResult{{addresses: []netip.Addr{authorizedTestIP}}}}
	dialer := &mappedDialer{actual: server.Listener.Addr().String(), advertised: authorizedTestIP}
	transport := newHTTPTestTransport(t, server.Listener.Addr().String(), "/base", resolver, dialer)

	inventory, err := transport.getAccountInventory(context.Background(), "management-key-canary")
	if err != nil {
		t.Fatalf("inventory request: %v", err)
	}
	if inventory.StatusCode != http.StatusOK || inventory.Header.Get("X-CPA-VERSION") != "v7.2.141" ||
		inventory.Header.Get("X-Sensitive-Upstream") != "" {
		t.Fatalf("unexpected inventory projection: status=%d header=%v", inventory.StatusCode, inventory.Header)
	}
	_, _ = io.Copy(io.Discard, inventory.Body)
	_ = inventory.Body.Close()
	health, err := transport.getHealth(context.Background())
	if err != nil {
		t.Fatalf("health request: %v", err)
	}
	_, _ = io.Copy(io.Discard, health.Body)
	_ = health.Body.Close()

	if inventoryKey != "management-key-canary" || healthCookie != "" {
		t.Fatalf("credential/cookie boundary failed: key=%q health_cookie=%q", inventoryKey, healthCookie)
	}
	if requests.Load() != 2 || proxyRequests.Load() != 0 {
		t.Fatalf("request counts: server=%d proxy=%d", requests.Load(), proxyRequests.Load())
	}
	if resolver.callCount() != 2 || dialer.callCount() != 2 {
		t.Fatalf("each connection must re-resolve: dns=%d dial=%d", resolver.callCount(), dialer.callCount())
	}
}

func TestSecureTransportRejectsEveryRedirectWithoutSecondRequest(t *testing.T) {
	locations := []string{"/base/destination", "http://other.example.invalid/destination", "https://node.example.invalid/destination"}
	for _, location := range locations {
		t.Run(location, func(t *testing.T) {
			var sourceRequests atomic.Int32
			var destinationRequests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/base/destination" || request.URL.Path == "/destination" {
					destinationRequests.Add(1)
					if request.Header.Get("X-Management-Key") != "" {
						t.Error("management key reached redirect destination")
					}
					return
				}
				sourceRequests.Add(1)
				response.Header().Set("Location", location)
				response.WriteHeader(http.StatusTemporaryRedirect)
			}))
			defer server.Close()
			resolver := &sequenceResolver{results: []resolverResult{{addresses: []netip.Addr{authorizedTestIP}}}}
			dialer := &mappedDialer{actual: server.Listener.Addr().String(), advertised: authorizedTestIP}
			transport := newHTTPTestTransport(t, server.Listener.Addr().String(), "/base", resolver, dialer)
			_, err := transport.getAccountInventory(context.Background(), "redirect-key-canary")
			assertRequestReason(t, err, FailureRedirectRejected)
			if sourceRequests.Load() != 1 || destinationRequests.Load() != 0 || dialer.callCount() != 1 {
				t.Fatalf("redirect escaped one request: source=%d destination=%d dial=%d",
					sourceRequests.Load(), destinationRequests.Load(), dialer.callCount())
			}
		})
	}
}

func TestSecureTransportDNSAuthorizationAndRebinding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(response, `{"status":"ok"}`)
	}))
	defer server.Close()

	t.Run("mixed result rejects before dialing", func(t *testing.T) {
		resolver := &sequenceResolver{results: []resolverResult{{addresses: []netip.Addr{
			authorizedTestIP, netip.MustParseAddr("203.0.113.99"),
		}}}}
		dialer := &mappedDialer{actual: server.Listener.Addr().String(), advertised: authorizedTestIP}
		transport := newHTTPTestTransport(t, server.Listener.Addr().String(), "", resolver, dialer)
		_, err := transport.getHealth(context.Background())
		assertRequestReason(t, err, FailureTargetRejected)
		if dialer.callCount() != 0 {
			t.Fatalf("mixed DNS result dialed %d times", dialer.callCount())
		}
	})

	t.Run("empty result", func(t *testing.T) {
		resolver := &sequenceResolver{results: []resolverResult{{}}}
		dialer := &mappedDialer{actual: server.Listener.Addr().String(), advertised: authorizedTestIP}
		transport := newHTTPTestTransport(t, server.Listener.Addr().String(), "", resolver, dialer)
		_, err := transport.getHealth(context.Background())
		assertRequestReason(t, err, FailureDNSRejected)
		if dialer.callCount() != 0 {
			t.Fatal("empty DNS result reached dialer")
		}
	})

	t.Run("next connection sees rebinding", func(t *testing.T) {
		resolver := &sequenceResolver{results: []resolverResult{
			{addresses: []netip.Addr{authorizedTestIP}},
			{addresses: []netip.Addr{netip.MustParseAddr("169.254.169.254")}},
		}}
		dialer := &mappedDialer{actual: server.Listener.Addr().String(), advertised: authorizedTestIP}
		transport := newHTTPTestTransport(t, server.Listener.Addr().String(), "", resolver, dialer)
		response, err := transport.getHealth(context.Background())
		if err != nil {
			t.Fatalf("first request: %v", err)
		}
		_ = response.Body.Close()
		_, err = transport.getHealth(context.Background())
		assertRequestReason(t, err, FailureTargetRejected)
		if resolver.callCount() != 2 || dialer.callCount() != 1 {
			t.Fatalf("rebind counts: dns=%d dial=%d", resolver.callCount(), dialer.callCount())
		}
	})

	t.Run("actual dial address mismatch", func(t *testing.T) {
		resolver := &sequenceResolver{results: []resolverResult{{addresses: []netip.Addr{authorizedTestIP}}}}
		dialer := &mappedDialer{actual: server.Listener.Addr().String(), advertised: netip.MustParseAddr("10.43.0.8")}
		transport := newHTTPTestTransport(t, server.Listener.Addr().String(), "", resolver, dialer)
		_, err := transport.getHealth(context.Background())
		assertRequestReason(t, err, FailureTargetRejected)
	})
}

func TestSecureTransportTLSHostnameCAExpiryAndRecovery(t *testing.T) {
	validCertificate, roots := makeServerCertificate(t, "node.example.invalid", time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	var serverName string
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		serverName = request.TLS.ServerName
		_, _ = io.WriteString(response, `{"status":"ok"}`)
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{validCertificate}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()

	config := mustManagementConfig(t, []string{"node.example.invalid", "wrong.example.invalid"}, []string{"10.42.0.0/16"}, nil)
	newTLS := func(endpointHost string, pool *x509.CertPool, resolver *sequenceResolver) *safeTransport {
		dialer := &mappedDialer{actual: server.Listener.Addr().String(), advertised: authorizedTestIP}
		transport, err := newSecureTransport("https://"+endpointHost+listenerPort(t, server.Listener.Addr().String()), config,
			transportOptions{RootCAs: pool, Resolver: resolver, Dialer: dialer})
		if err != nil {
			t.Fatalf("new TLS transport: %v", err)
		}
		return transport
	}

	resolver := &sequenceResolver{results: []resolverResult{{addresses: []netip.Addr{authorizedTestIP}}}}
	response, err := newTLS("node.example.invalid", roots, resolver).getHealth(context.Background())
	if err != nil {
		t.Fatalf("valid TLS: %v", err)
	}
	_ = response.Body.Close()
	if serverName != "node.example.invalid" {
		t.Fatalf("SNI=%q", serverName)
	}

	_, err = newTLS("wrong.example.invalid", roots, resolver).getHealth(context.Background())
	assertRequestReason(t, err, FailureTLSRejected)
	_, err = newTLS("node.example.invalid", x509.NewCertPool(), resolver).getHealth(context.Background())
	assertRequestReason(t, err, FailureTLSRejected)

	expiredCertificate, expiredRoots := makeServerCertificate(t, "node.example.invalid", time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour))
	expired := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	expired.TLS = &tls.Config{Certificates: []tls.Certificate{expiredCertificate}, MinVersion: tls.VersionTLS12}
	expired.StartTLS()
	defer expired.Close()
	expiredDialer := &mappedDialer{actual: expired.Listener.Addr().String(), advertised: authorizedTestIP}
	expiredTransport, err := newSecureTransport("https://node.example.invalid"+listenerPort(t, expired.Listener.Addr().String()), config,
		transportOptions{RootCAs: expiredRoots, Resolver: resolver, Dialer: expiredDialer})
	if err != nil {
		t.Fatal(err)
	}
	_, err = expiredTransport.getHealth(context.Background())
	assertRequestReason(t, err, FailureTLSRejected)

	// A prior DNS rejection creates no permanent deny cache. The next explicit
	// request performs a fresh lookup and can recover.
	recoveringResolver := &sequenceResolver{results: []resolverResult{
		{addresses: []netip.Addr{netip.MustParseAddr("169.254.169.254")}},
		{addresses: []netip.Addr{authorizedTestIP}},
	}}
	recovering := newTLS("node.example.invalid", roots, recoveringResolver)
	_, err = recovering.getHealth(context.Background())
	assertRequestReason(t, err, FailureTargetRejected)
	response, err = recovering.getHealth(context.Background())
	if err != nil {
		t.Fatalf("recovered request: %v", err)
	}
	_ = response.Body.Close()
}

func TestSecureTransportTimeoutCancellationAndNoRetry(t *testing.T) {
	t.Run("standard network timeout classification", func(t *testing.T) {
		err := classifyRequestError(OperationHealth, context.Background(), timeoutTestError{})
		assertRequestReason(t, err, FailureTimeout)
	})
	t.Run("resolver early network timeout", func(t *testing.T) {
		resolver := &sequenceResolver{results: []resolverResult{{err: timeoutTestError{}}}}
		dialer := &mappedDialer{advertised: authorizedTestIP}
		transport := newCustomTimeoutTransport(t, resolver, dialer, time.Second, 2*time.Second)
		_, err := transport.getHealth(context.Background())
		assertRequestReason(t, err, FailureTimeout)
		if resolver.callCount() != 1 || dialer.callCount() != 0 {
			t.Fatalf("early DNS timeout counts: dns=%d dial=%d", resolver.callCount(), dialer.callCount())
		}
	})
	t.Run("dialer early network timeout", func(t *testing.T) {
		resolver := &sequenceResolver{results: []resolverResult{{addresses: []netip.Addr{authorizedTestIP}}}}
		dialer := &immediateErrorDialer{err: timeoutTestError{}}
		transport := newCustomTimeoutTransport(t, resolver, dialer, time.Second, 2*time.Second)
		_, err := transport.getHealth(context.Background())
		assertRequestReason(t, err, FailureTimeout)
		if resolver.callCount() != 1 || dialer.calls.Load() != 1 {
			t.Fatalf("early dial timeout counts: dns=%d dial=%d", resolver.callCount(), dialer.calls.Load())
		}
	})
	t.Run("slow DNS", func(t *testing.T) {
		resolver := blockingResolver{}
		dialer := &mappedDialer{advertised: authorizedTestIP}
		transport := newCustomTimeoutTransport(t, resolver, dialer, 100*time.Millisecond, time.Second)
		started := time.Now()
		_, err := transport.getHealth(context.Background())
		assertRequestReason(t, err, FailureTimeout)
		if elapsed := time.Since(started); elapsed > 750*time.Millisecond {
			t.Fatalf("DNS timeout was not bounded: %v", elapsed)
		}
		if dialer.callCount() != 0 {
			t.Fatal("slow DNS reached dialer")
		}
	})

	t.Run("slow connect", func(t *testing.T) {
		resolver := &sequenceResolver{results: []resolverResult{{addresses: []netip.Addr{authorizedTestIP}}}}
		dialer := &mappedDialer{advertised: authorizedTestIP, wait: true}
		transport := newCustomTimeoutTransport(t, resolver, dialer, 100*time.Millisecond, time.Second)
		_, err := transport.getHealth(context.Background())
		assertRequestReason(t, err, FailureTimeout)
		if dialer.callCount() != 1 {
			t.Fatalf("dial count=%d", dialer.callCount())
		}
	})

	t.Run("caller cancellation", func(t *testing.T) {
		resolver := &sequenceResolver{results: []resolverResult{{addresses: []netip.Addr{authorizedTestIP}}}}
		dialer := &mappedDialer{advertised: authorizedTestIP, wait: true}
		transport := newCustomTimeoutTransport(t, resolver, dialer, time.Second, 2*time.Second)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := transport.getHealth(ctx)
		assertRequestReason(t, err, FailureCancelled)
	})

	t.Run("slow header total timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			time.Sleep(1200 * time.Millisecond)
			_, _ = io.WriteString(response, `{"status":"ok"}`)
		}))
		defer server.Close()
		resolver := &sequenceResolver{results: []resolverResult{{addresses: []netip.Addr{authorizedTestIP}}}}
		dialer := &mappedDialer{actual: server.Listener.Addr().String(), advertised: authorizedTestIP}
		transport := newCustomTimeoutTransportWithServer(t, server.Listener.Addr().String(), resolver, dialer, 500*time.Millisecond, time.Second)
		_, err := transport.getHealth(context.Background())
		assertRequestReason(t, err, FailureTimeout)
	})

	t.Run("slow body remains bounded", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.Header().Set("Content-Type", "application/json")
			response.WriteHeader(http.StatusOK)
			if flush, ok := response.(http.Flusher); ok {
				flush.Flush()
			}
			time.Sleep(1200 * time.Millisecond)
			_, _ = io.WriteString(response, `{"status":"ok"}`)
		}))
		defer server.Close()
		resolver := &sequenceResolver{results: []resolverResult{{addresses: []netip.Addr{authorizedTestIP}}}}
		dialer := &mappedDialer{actual: server.Listener.Addr().String(), advertised: authorizedTestIP}
		transport := newCustomTimeoutTransportWithServer(t, server.Listener.Addr().String(), resolver, dialer, 500*time.Millisecond, time.Second)
		_, err := transport.getHealth(context.Background())
		assertRequestReason(t, err, FailureTimeout)
	})

	t.Run("transport does not retry failed request", func(t *testing.T) {
		var requests atomic.Int32
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			hijacker := response.(http.Hijacker)
			connection, _, err := hijacker.Hijack()
			if err == nil {
				_ = connection.Close()
			}
		}))
		server.Start()
		defer server.Close()
		resolver := &sequenceResolver{results: []resolverResult{{addresses: []netip.Addr{authorizedTestIP}}}}
		dialer := &mappedDialer{actual: server.Listener.Addr().String(), advertised: authorizedTestIP}
		transport := newHTTPTestTransport(t, server.Listener.Addr().String(), "", resolver, dialer)
		_, err := transport.getHealth(context.Background())
		assertRequestReason(t, err, FailureNetworkUnavailable)
		if requests.Load() != 1 || dialer.callCount() != 1 {
			t.Fatalf("hidden retry detected: requests=%d dials=%d", requests.Load(), dialer.callCount())
		}
	})
}

func TestSecureTransportFixedFailureDoesNotLeakInputs(t *testing.T) {
	const canary = "sensitive-canary-do-not-project"
	resolver := &sequenceResolver{results: []resolverResult{{err: errors.New(canary)}}}
	dialer := &mappedDialer{advertised: authorizedTestIP}
	transport := newCustomTimeoutTransport(t, resolver, dialer, time.Second, 2*time.Second)
	_, err := transport.getAccountInventory(context.Background(), canary)
	assertRequestReason(t, err, FailureDNSRejected)
	for _, forbidden := range []string{canary, "node.example.invalid", "10.42.0.8"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("error leaked %q: %v", forbidden, err)
		}
	}
	if dialer.callCount() != 0 {
		t.Fatal("DNS failure reached dialer")
	}

	_, err = transport.get(context.Background(), Operation("delete"), canary)
	assertRequestReason(t, err, FailureRequestRejected)
	if dialer.callCount() != 0 {
		t.Fatal("invalid operation reached network")
	}
}

type blockingResolver struct{}

type timeoutTestError struct{}

func (timeoutTestError) Error() string   { return "fixed timeout" }
func (timeoutTestError) Timeout() bool   { return true }
func (timeoutTestError) Temporary() bool { return true }

type immediateErrorDialer struct {
	err   error
	calls atomic.Int32
}

func (d *immediateErrorDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	d.calls.Add(1)
	return nil, d.err
}

func (blockingResolver) LookupNetIP(ctx context.Context, _, _ string) ([]netip.Addr, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func newHTTPTestTransport(t *testing.T, actualAddress, basePath string, resolver Resolver, dialer ContextDialer) *safeTransport {
	t.Helper()
	return newCustomTimeoutTransportWithServer(t, actualAddress, resolver, dialer, rootdrivers.DefaultConnectTimeout, rootdrivers.DefaultRequestTimeout, basePath)
}

func newCustomTimeoutTransport(t *testing.T, resolver Resolver, dialer ContextDialer, connect, request time.Duration) *safeTransport {
	t.Helper()
	return newCustomTimeoutTransportWithServer(t, "127.0.0.1:18080", resolver, dialer, connect, request)
}

func newCustomTimeoutTransportWithServer(t *testing.T, actualAddress string, resolver Resolver, dialer ContextDialer, connect, request time.Duration, paths ...string) *safeTransport {
	t.Helper()
	basePath := ""
	if len(paths) > 0 {
		basePath = paths[0]
	}
	config := mustManagementConfig(t, []string{"node.example.invalid"}, []string{"10.42.0.0/16"}, []string{"10.42.0.0/24"})
	config.ConnectTimeout = connect
	config.RequestTimeout = request
	transport, err := newSecureTransport("http://node.example.invalid"+listenerPort(t, actualAddress)+basePath, config,
		transportOptions{Resolver: resolver, Dialer: dialer})
	if err != nil {
		t.Fatalf("new transport: %v", err)
	}
	return transport
}

func listenerPort(t *testing.T, address string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("listener address: %v", err)
	}
	return ":" + port
}

func assertRequestReason(t *testing.T, err error, want FailureReason) {
	t.Helper()
	var requestError *RequestError
	if !errors.As(err, &requestError) || requestError.Reason != want {
		t.Fatalf("error=%v, want RequestError reason %q", err, want)
	}
}

func makeServerCertificate(t *testing.T, dnsName string, notBefore, notAfter time.Time) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "CLIProxyAPI transport test CA"},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: dnsName},
		DNSNames:     []string{dnsName},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	leafKeyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: leafKeyDER}),
	)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	return certificate, roots
}

func TestValidatedTimeoutsMatchSharedBounds(t *testing.T) {
	for _, test := range []struct {
		connect time.Duration
		request time.Duration
		ok      bool
	}{
		{connect: 100 * time.Millisecond, request: time.Second, ok: true},
		{connect: 10 * time.Second, request: 30 * time.Second, ok: true},
		{connect: 99 * time.Millisecond, request: time.Second},
		{connect: time.Second, request: 999 * time.Millisecond},
		{connect: time.Second, request: 31 * time.Second},
	} {
		_, _, err := validatedTimeouts(test.connect, test.request)
		if (err == nil) != test.ok {
			t.Errorf("connect=%s request=%s error=%v want ok=%v", test.connect, test.request, err, test.ok)
		}
	}
}

func TestRemoteAddressIPv4AndIPv6(t *testing.T) {
	for _, value := range []string{"10.42.0.8", "fd12:3456:789a::8"} {
		address := netip.MustParseAddr(value)
		port := strconv.Itoa(9443)
		parsed, ok := remoteAddress(stringAddress(net.JoinHostPort(address.String(), port)))
		if !ok || parsed != address {
			t.Errorf("remoteAddress(%s)=(%s,%v)", value, parsed, ok)
		}
	}
}

type stringAddress string

func (a stringAddress) Network() string { return "tcp" }
func (a stringAddress) String() string  { return string(a) }
