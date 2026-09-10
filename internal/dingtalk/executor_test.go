package dingtalk

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sunxu/relay-station-control/internal/jobs"
)

func executorTestPayload() []byte {
	return []byte(`{"occurrence_id":"00000000-0000-4000-8000-000000000001","occurrence_type":"TOKEN_INVALID","transition":"ACTIVE","reason":"token_invalid","severity":"Critical","environment_id":"test","environment_name":"test","account_key":"antigravity:example@example.com","email":"example@example.com","provider":"antigravity","instance_ids":["00000000-0000-4000-8000-000000000003"],"node_names":["relay-a"],"started_at":"2026-09-11T00:00:00Z","transitioned_at":"2026-09-11T00:00:00Z"}`)
}

func executorAt(t *testing.T, server *httptest.Server, secret string) *Executor {
	t.Helper()
	cfg, err := LoadConfig(func(key string) string {
		if key == "DINGTALK_WEBHOOK_URL" {
			return server.URL + "/robot/send?access_token=secret-canary"
		}
		return secret
	})
	if err != nil {
		t.Fatal(err)
	}
	e := NewExecutor(cfg)
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	transport := e.client.Transport.(*http.Transport)
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	t.Cleanup(transport.CloseIdleConnections)
	return e
}

func TestConfigDisabledInvalidAndSecretIsolation(t *testing.T) {
	for _, raw := range []string{"http://example.com/secret-canary", "https://", "https://user:secret-canary@example.com", "https://example.com/#secret-canary", "https://example.com/?x=%zz"} {
		_, err := LoadConfig(func(key string) string {
			if key == "DINGTALK_WEBHOOK_URL" {
				return raw
			}
			return ""
		})
		if err == nil || strings.Contains(err.Error(), "secret-canary") {
			t.Fatalf("unsafe config result: %v", err)
		}
	}
	disabled, err := LoadConfig(func(string) string { return "" })
	if err != nil || disabled.Enabled() {
		t.Fatal("absent URL must disable without error")
	}
	if got := NewExecutor(disabled).Execute(context.Background(), jobs.Execution{}); got.Disposition != jobs.ExecutePermanentFailure || got.ErrorCode != "dingtalk_disabled" {
		t.Fatal(got)
	}
	cfg, err := LoadConfig(func(key string) string {
		if key == "DINGTALK_WEBHOOK_URL" {
			return "https://example.com/?access_token=secret-canary"
		}
		return "signing-canary"
	})
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("config", "config", cfg)
	encoded, _ := json.Marshal(cfg)
	for _, output := range []string{fmt.Sprintf("%v %#v", cfg, cfg), logs.String(), string(encoded)} {
		if strings.Contains(output, "canary") || strings.Contains(output, "example.com") {
			t.Fatal("config serialization leaked")
		}
	}
}

func TestSigningFixedVector(t *testing.T) {
	// Independent OpenSSL HMAC-SHA256 vector for the official UTF-8 input.
	if got := signature("1700000000000", "SEC-test"); got != "Sf/ft2shUSqoR1REF+DS39IoTlwQRyDPwfM8S94ODOc=" {
		t.Fatal("signature differs from independent vector")
	}
}

func TestMalformedRuntimeSecretsFailClosed(t *testing.T) {
	for _, tc := range []struct{ url, secret string }{
		{"https://example.com/robot/send", ""},
		{"https://example.com/robot/send?access_token=", ""},
		{"https://example.com/robot/send?access_token=a&access_token=b", ""},
		{"https://example.com:99999/robot/send?access_token=a", ""},
		{"https://example.com/robot/send?access_token=a", "\nsecret-canary"},
		{"https://example.com/robot/send?access_token=a", "\xff"},
	} {
		_, err := LoadConfig(func(key string) string {
			if key == "DINGTALK_WEBHOOK_URL" {
				return tc.url
			}
			return tc.secret
		})
		if err == nil || err.Error() != "dingtalk_config_invalid" {
			t.Fatal("malformed config not safely rejected")
		}
	}
}

func TestHTTPClassification(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   jobs.ExecuteDisposition
	}{
		{"success", 200, `{"errcode":0,"errmsg":"ok"}`, jobs.ExecuteSucceeded},
		{"documented string", 200, `{"errcode":"0","errmsg":"ok"}`, jobs.ExecuteSucceeded},
		{"busy", 200, `{"errcode":-1,"errmsg":"busy"}`, jobs.ExecuteResultUnknown},
		{"rate", 200, `{"errcode":410100,"errmsg":"rate limited"}`, jobs.ExecuteRetryableNoEffect},
		{"invalid", 200, `not json`, jobs.ExecutePermanentFailure},
		{"missing", 200, `{}`, jobs.ExecutePermanentFailure},
		{"null", 200, `{"errcode":null,"errmsg":"ok"}`, jobs.ExecutePermanentFailure},
		{"duplicate", 200, `{"errcode":310000,"errcode":0,"errmsg":"ok"}`, jobs.ExecutePermanentFailure},
		{"trailing", 200, `{"errcode":0,"errmsg":"ok"} {}`, jobs.ExecutePermanentFailure},
		{"wrong success message", 200, `{"errcode":0,"errmsg":"failure"}`, jobs.ExecutePermanentFailure},
		{"oversized", 200, strings.Repeat(" ", responseLimit+1), jobs.ExecutePermanentFailure},
	}
	for _, code := range []int{400, 401, 403, 404, 301, 302, 307, 308} {
		cases = append(cases, struct {
			name   string
			status int
			body   string
			want   jobs.ExecuteDisposition
		}{fmt.Sprint(code), code, `{"errcode":0,"errmsg":"ok"}`, jobs.ExecutePermanentFailure})
	}
	for _, code := range []int{408, 429, 500, 502, 503} {
		cases = append(cases, struct {
			name   string
			status int
			body   string
			want   jobs.ExecuteDisposition
		}{fmt.Sprint(code), code, "", jobs.ExecuteResultUnknown})
	}
	for _, code := range []int{40035, 43004, 400013, 400101, 400102, 400105, 400106, 430101, 430102, 430103, 430104, 310000, 999999} {
		cases = append(cases, struct {
			name   string
			status int
			body   string
			want   jobs.ExecuteDisposition
		}{fmt.Sprint("business-", code), 200, fmt.Sprintf(`{"errcode":%d,"errmsg":"secret-canary"}`, code), jobs.ExecutePermanentFailure})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer s.Close()
			got := executorAt(t, s, "").Execute(context.Background(), jobs.Execution{Payload: executorTestPayload()})
			if got.Disposition != tc.want || strings.Contains(got.ErrorCode, "canary") {
				t.Fatalf("got %+v want %s", got, tc.want)
			}
		})
	}
}

func TestProxyBypassSigningAndRedirect(t *testing.T) {
	var proxyHits, redirectHits atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { proxyHits.Add(1); w.WriteHeader(502) }))
	defer proxy.Close()
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(key, proxy.URL)
	}
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirectHits.Add(1) }))
	defer redirect.Close()
	var doRedirect atomic.Bool
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("request contract")
		}
		q := r.URL.Query()
		body, err := io.ReadAll(io.LimitReader(r.Body, jobs.MaxPayloadBytes+1))
		if err != nil || bytes.Contains(body, []byte("secret-canary")) || bytes.Contains(body, []byte("SEC-test")) || bytes.Contains(body, []byte("access_token")) {
			t.Error("transport secret leaked to message")
		}
		if q.Get("sign") != signature(q.Get("timestamp"), "SEC-test") || len(q.Get("timestamp")) != 13 {
			t.Error("signature/query")
		}
		if doRedirect.Load() {
			http.Redirect(w, r, redirect.URL, 307)
			return
		}
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer s.Close()
	e := executorAt(t, s, "SEC-test")
	// Avoid Go's localhost proxy exemption: synthetic non-loopback hostname,
	// direct dial mapped to our TLS server, whose certificate includes example.com.
	e.config.webhook.Host = "example.com:443"
	e.client.Transport.(*http.Transport).DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "example.com:443" {
			t.Errorf("unexpected proxy dial %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, s.Listener.Addr().String())
	}
	if got := e.Execute(context.Background(), jobs.Execution{Payload: executorTestPayload()}); got.Disposition != jobs.ExecuteSucceeded {
		t.Fatal(got)
	}
	doRedirect.Store(true)
	if got := e.Execute(context.Background(), jobs.Execution{Payload: executorTestPayload()}); got.Disposition != jobs.ExecutePermanentFailure {
		t.Fatal(got)
	}
	if proxyHits.Load() != 0 || redirectHits.Load() != 0 {
		t.Fatal("proxy/redirect used")
	}
}

func TestTimeoutAndPostWriteResetAreUnknown(t *testing.T) {
	for _, mode := range []string{"timeout", "reset"} {
		t.Run(mode, func(t *testing.T) {
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if mode == "timeout" {
					<-r.Context().Done()
					return
				}
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = conn.Close()
			}))
			defer s.Close()
			e := executorAt(t, s, "")
			if e.client.Timeout != 5*time.Second || Definition(e).Timeout != 10*time.Second {
				t.Fatal("timeout bounds")
			}
			started := time.Now()
			got := e.Execute(context.Background(), jobs.Execution{Payload: executorTestPayload()})
			if got.Disposition != jobs.ExecuteResultUnknown {
				t.Fatal(got)
			}
			if mode == "timeout" && (time.Since(started) < 4*time.Second || time.Since(started) > 7*time.Second) {
				t.Fatal("HTTP 5s bound")
			}
		})
	}
}

func TestPreSendDNSConnectTLSFailuresHaveNoEffect(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("must not send") }))
	defer s.Close()
	for _, mode := range []string{"dns", "connect", "tls"} {
		t.Run(mode, func(t *testing.T) {
			e := executorAt(t, s, "")
			transport := e.client.Transport.(*http.Transport)
			switch mode {
			case "dns":
				transport.DialContext = func(context.Context, string, string) (net.Conn, error) {
					return nil, &net.DNSError{Err: "not found", Name: "secret-canary", IsNotFound: true}
				}
			case "connect":
				transport.DialContext = func(context.Context, string, string) (net.Conn, error) {
					return nil, &net.OpError{Op: "dial", Net: "tcp", Err: fmt.Errorf("secret-canary")}
				}
			case "tls":
				transport.TLSClientConfig = &tls.Config{RootCAs: x509.NewCertPool()}
			}
			got := e.Execute(context.Background(), jobs.Execution{Payload: executorTestPayload()})
			if got.Disposition != jobs.ExecuteRetryableNoEffect || got.ErrorCode != "dingtalk_connect_failed" {
				t.Fatal(got)
			}
		})
	}
}

func TestInvalidIssueTupleIsPermanentAndNeverSent(t *testing.T) {
	var requests atomic.Int32
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer s.Close()
	e := executorAt(t, s, "SEC-test")
	raw := bytes.Replace(executorTestPayload(), []byte(`"reason":"token_invalid"`), []byte(`"reason":"account_blocked"`), 1)
	got := e.Execute(context.Background(), jobs.Execution{Payload: raw})
	if got.Disposition != jobs.ExecutePermanentFailure || got.ErrorCode != "dingtalk_invalid_payload" {
		t.Fatalf("got %+v", got)
	}
	if requests.Load() != 0 {
		t.Fatal("invalid payload was sent over HTTP")
	}
}

func TestInvalidPayloadIsPermanentAndNeverSent(t *testing.T) {
	var requests atomic.Int32
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer s.Close()
	e := executorAt(t, s, "SEC-test")
	for name, raw := range map[string][]byte{
		"mismatched arrays":      bytes.Replace(executorTestPayload(), []byte(`"node_names":["relay-a"]`), []byte(`"node_names":[]`), 1),
		"noncanonical timestamp": bytes.Replace(executorTestPayload(), []byte(`"started_at":"2026-09-11T00:00:00Z"`), []byte(`"started_at":"2026-09-11T08:00:00+08:00"`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			got := e.Execute(context.Background(), jobs.Execution{Payload: raw})
			if got.Disposition != jobs.ExecutePermanentFailure || got.ErrorCode != "dingtalk_invalid_payload" {
				t.Fatalf("got %+v", got)
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatal("invalid payload was sent over HTTP")
	}
}
