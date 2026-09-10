package dingtalk

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sunxu/relay-station-control/internal/jobs"
)

func transportSecurityCanary(t *testing.T, label string) string {
	t.Helper()
	var raw [16]byte
	if _, err := crand.Read(raw[:]); err != nil {
		t.Fatal("could not create runtime canary")
	}
	return label + "-" + hex.EncodeToString(raw[:])
}

type transportSecurityLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *transportSecurityLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *transportSecurityLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func transportSecurityLogs(t *testing.T) *transportSecurityLogBuffer {
	t.Helper()
	logs := new(transportSecurityLogBuffer)
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return logs
}

func transportSecurityExecutorAt(t *testing.T, server *httptest.Server, accessToken, signingSecret string) *Executor {
	t.Helper()
	e := executorAt(t, server, signingSecret)
	u := *e.config.webhook
	query := u.Query()
	query.Set("access_token", accessToken)
	u.RawQuery = query.Encode()
	e.config.webhook = &u
	return e
}

func mapTransportToServer(t *testing.T, e *Executor, host string, server *httptest.Server) {
	t.Helper()
	e.config.webhook.Host = host
	transport := e.client.Transport.(*http.Transport)
	tlsConfig := transport.TLSClientConfig.Clone()
	tlsConfig.ServerName = "example.com"
	transport.TLSClientConfig = tlsConfig
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != host {
			return nil, errors.New("unexpected test dial address")
		}
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, server.Listener.Addr().String())
	}
}

func assertTransportResult(t *testing.T, got jobs.ExecuteResult, wantDisposition jobs.ExecuteDisposition, wantCode string, logs *transportSecurityLogBuffer, canaries ...string) {
	t.Helper()
	if got.Disposition != wantDisposition || got.ErrorCode != wantCode {
		t.Fatal("unexpected executor result for fixed-code case")
	}
	output := got.ErrorCode + logs.String()
	for _, canary := range canaries {
		if strings.Contains(output, canary) {
			t.Fatal("runtime canary leaked into executor result or logs")
		}
	}
}

func TestTransportSecurityIgnoresAllProxyEnvironment(t *testing.T) {
	logs := transportSecurityLogs(t)
	urlCanary := transportSecurityCanary(t, "url")
	signingCanary := transportSecurityCanary(t, "signing")

	var targetHits, proxyHits atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		proxyHits.Add(1)
		if hijacker, ok := w.(http.Hijacker); ok {
			conn, _, err := hijacker.Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()
	for _, key := range []string{
		"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY",
		"http_proxy", "https_proxy", "all_proxy",
	} {
		t.Setenv(key, proxy.URL)
	}
	// An empty value clears inherited bypass rules without using NO_PROXY=*.
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")

	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Error("unexpected target request contract")
		}
		query := r.URL.Query()
		if query.Get("access_token") != urlCanary || query.Get("sign") != signature(query.Get("timestamp"), signingCanary) || len(query.Get("timestamp")) != 13 {
			t.Error("unexpected target signing query")
		}
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer target.Close()

	e := transportSecurityExecutorAt(t, target, urlCanary, signingCanary)
	const host = "example.com:443"
	mapTransportToServer(t, e, host, target)
	transport := e.client.Transport.(*http.Transport)
	if transport.Proxy != nil {
		t.Fatal("production transport must disable proxy")
	}

	got := e.Execute(context.Background(), jobs.Execution{Payload: executorTestPayload()})
	if got.Disposition != jobs.ExecuteSucceeded || got.ErrorCode != "" {
		t.Fatal("unexpected direct delivery result")
	}
	if targetHits.Load() <= 0 {
		t.Fatal("mapped non-loopback target was not reached")
	}
	if proxyHits.Load() != 0 {
		t.Fatal("dead proxy received a request")
	}
	for _, canary := range []string{urlCanary, signingCanary} {
		if strings.Contains(logs.String(), canary) {
			t.Fatal("runtime canary leaked into logs")
		}
	}
}

func TestTransportSecurityRejectsRedirectsWithoutFollowing(t *testing.T) {
	for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			logs := transportSecurityLogs(t)
			urlCanary := transportSecurityCanary(t, "url")
			signingCanary := transportSecurityCanary(t, "signing")
			var sourceHits, destinationHits atomic.Int32
			destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				destinationHits.Add(1)
				_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
			}))
			defer destination.Close()
			source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				sourceHits.Add(1)
				w.Header().Set("Location", "https://destination.example.com:443/destination")
				w.WriteHeader(status)
			}))
			defer source.Close()

			e := transportSecurityExecutorAt(t, source, urlCanary, signingCanary)
			const sourceHost = "source.example.com:443"
			const destinationHost = "destination.example.com:443"
			e.config.webhook.Host = sourceHost
			e.config.webhook.Path = "/source"
			transport := e.client.Transport.(*http.Transport)
			roots := x509.NewCertPool()
			roots.AddCert(source.Certificate())
			roots.AddCert(destination.Certificate())
			tlsConfig := transport.TLSClientConfig.Clone()
			tlsConfig.RootCAs = roots
			tlsConfig.ServerName = "example.com"
			transport.TLSClientConfig = tlsConfig
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				switch address {
				case sourceHost:
					return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, source.Listener.Addr().String())
				case destinationHost:
					return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, destination.Listener.Addr().String())
				default:
					return nil, errors.New("unexpected redirect dial address")
				}
			}
			got := e.Execute(context.Background(), jobs.Execution{Payload: executorTestPayload()})
			assertTransportResult(t, got, jobs.ExecutePermanentFailure, "dingtalk_http_rejected", logs, urlCanary, signingCanary)
			if sourceHits.Load() != 1 || destinationHits.Load() != 0 {
				t.Fatal("redirect source/destination hit counts violate no-follow contract")
			}
		})
	}
}

func TestTransportFailureCodesAreFixedAndCanaryFree(t *testing.T) {
	logs := transportSecurityLogs(t)
	urlCanary := transportSecurityCanary(t, "url")
	signingCanary := transportSecurityCanary(t, "signing")
	responseCanary := transportSecurityCanary(t, "response")
	diagnosticCanary := transportSecurityCanary(t, "diagnostic")
	canaries := []string{urlCanary, signingCanary, responseCanary, diagnosticCanary}

	t.Run("dns", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("DNS failure reached the target")
		}))
		defer server.Close()
		e := transportSecurityExecutorAt(t, server, urlCanary, signingCanary)
		transport := e.client.Transport.(*http.Transport)
		e.config.webhook.Host = "dns.example.com:443"
		transport.DialContext = func(context.Context, string, string) (net.Conn, error) {
			return nil, &net.DNSError{Err: "not found", Name: diagnosticCanary, IsNotFound: true}
		}
		got := e.Execute(context.Background(), jobs.Execution{Payload: executorTestPayload()})
		assertTransportResult(t, got, jobs.ExecuteRetryableNoEffect, "dingtalk_connect_failed", logs, canaries...)
	})

	t.Run("connect", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("connect failure reached the target")
		}))
		defer server.Close()
		e := transportSecurityExecutorAt(t, server, urlCanary, signingCanary)
		transport := e.client.Transport.(*http.Transport)
		e.config.webhook.Host = "connect.example.com:443"
		transport.DialContext = func(context.Context, string, string) (net.Conn, error) {
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New(diagnosticCanary)}
		}
		got := e.Execute(context.Background(), jobs.Execution{Payload: executorTestPayload()})
		assertTransportResult(t, got, jobs.ExecuteRetryableNoEffect, "dingtalk_connect_failed", logs, canaries...)
	})

	t.Run("tls", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("TLS failure reached the target")
		}))
		defer server.Close()
		e := transportSecurityExecutorAt(t, server, urlCanary, signingCanary)
		mapTransportToServer(t, e, "example.com:443", server)
		transport := e.client.Transport.(*http.Transport)
		transport.TLSClientConfig = &tls.Config{RootCAs: x509.NewCertPool(), MinVersion: tls.VersionTLS12}
		got := e.Execute(context.Background(), jobs.Execution{Payload: executorTestPayload()})
		assertTransportResult(t, got, jobs.ExecuteRetryableNoEffect, "dingtalk_connect_failed", logs, canaries...)
	})

	t.Run("timeout-after-connect", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(started)
			select {
			case <-r.Context().Done():
			case <-release:
			}
		}))
		defer server.Close()
		defer close(release)
		e := transportSecurityExecutorAt(t, server, urlCanary, signingCanary)
		mapTransportToServer(t, e, "example.com:443", server)
		if e.client.Timeout != 5*time.Second {
			t.Fatal("production client timeout changed")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		resultCh := make(chan jobs.ExecuteResult, 1)
		go func() { resultCh <- e.Execute(ctx, jobs.Execution{Payload: executorTestPayload()}) }()
		select {
		case <-started:
		case <-time.After(time.Second):
			cancel()
			<-resultCh
			t.Fatal("timeout test did not reach the target")
		}
		got := <-resultCh
		assertTransportResult(t, got, jobs.ExecuteResultUnknown, "dingtalk_result_unknown", logs, canaries...)
	})

	t.Run("reset-after-write", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		}))
		defer server.Close()
		e := transportSecurityExecutorAt(t, server, urlCanary, signingCanary)
		mapTransportToServer(t, e, "example.com:443", server)
		got := e.Execute(context.Background(), jobs.Execution{Payload: executorTestPayload()})
		assertTransportResult(t, got, jobs.ExecuteResultUnknown, "dingtalk_result_unknown", logs, canaries...)
	})

	for _, tc := range []struct {
		name            string
		status          int
		body            string
		wantDisposition jobs.ExecuteDisposition
		wantErrorCode   string
	}{
		{name: "http-408", status: http.StatusRequestTimeout, body: `{"errcode":-1,"errmsg":"` + responseCanary + `"}`, wantDisposition: jobs.ExecuteResultUnknown, wantErrorCode: "dingtalk_http_retry"},
		{name: "http-429", status: http.StatusTooManyRequests, body: `{"errcode":-1,"errmsg":"` + responseCanary + `"}`, wantDisposition: jobs.ExecuteResultUnknown, wantErrorCode: "dingtalk_http_retry"},
		{name: "http-500", status: http.StatusInternalServerError, body: `{"errcode":-1,"errmsg":"` + responseCanary + `"}`, wantDisposition: jobs.ExecuteResultUnknown, wantErrorCode: "dingtalk_http_retry"},
		{name: "http-400", status: http.StatusBadRequest, body: `{"errcode":-1,"errmsg":"` + responseCanary + `"}`, wantDisposition: jobs.ExecutePermanentFailure, wantErrorCode: "dingtalk_http_rejected"},
		{name: "business-retry", status: http.StatusOK, body: `{"errcode":410100,"errmsg":"` + responseCanary + `"}`, wantDisposition: jobs.ExecuteRetryableNoEffect, wantErrorCode: "dingtalk_business_retry"},
		{name: "business-unknown", status: http.StatusOK, body: `{"errcode":-1,"errmsg":"` + responseCanary + `"}`, wantDisposition: jobs.ExecuteResultUnknown, wantErrorCode: "dingtalk_business_retry"},
		{name: "business-rejected", status: http.StatusOK, body: `{"errcode":40035,"errmsg":"` + responseCanary + `"}`, wantDisposition: jobs.ExecutePermanentFailure, wantErrorCode: "dingtalk_business_rejected"},
		{name: "invalid", status: http.StatusOK, body: "not-json-" + responseCanary, wantDisposition: jobs.ExecutePermanentFailure, wantErrorCode: "dingtalk_invalid_response"},
		{name: "oversized", status: http.StatusOK, body: strings.Repeat(responseCanary, responseLimit/len(responseCanary)+2), wantDisposition: jobs.ExecutePermanentFailure, wantErrorCode: "dingtalk_invalid_response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			e := transportSecurityExecutorAt(t, server, urlCanary, signingCanary)
			mapTransportToServer(t, e, "example.com:443", server)
			got := e.Execute(context.Background(), jobs.Execution{Payload: executorTestPayload()})
			assertTransportResult(t, got, tc.wantDisposition, tc.wantErrorCode, logs, canaries...)
		})
	}
}
