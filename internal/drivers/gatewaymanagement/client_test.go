package gatewaymanagement

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func requireProbeReason(t *testing.T, err error, want ProbeReason) *ProbeError {
	t.Helper()
	var probeErr *ProbeError
	if !errors.As(err, &probeErr) {
		t.Fatalf("error %T is not a ProbeError: %v", err, err)
	}
	if probeErr.Reason != want {
		t.Fatalf("reason = %s, want %s", probeErr.Reason, want)
	}
	return probeErr
}

func TestValidateOriginHTTPOnly(t *testing.T) {
	if _, err := ValidateOrigin("http://gateway.example.invalid/"); err != nil {
		t.Fatalf("HTTP origin rejected: %v", err)
	}
	for _, testCase := range []struct {
		name   string
		raw    string
		reason OriginErrorReason
	}{
		{name: "https", raw: "https://gateway.example.invalid", reason: OriginHTTPSReject},
		{name: "ftp", raw: "ftp://gateway.example.invalid", reason: OriginSchemeReject},
		{name: "path", raw: "http://gateway.example.invalid/base", reason: OriginInvalid},
		{name: "credentials", raw: "http://user:pass@gateway.example.invalid", reason: OriginInvalid},
		{name: "query", raw: "http://gateway.example.invalid?token=canary", reason: OriginInvalid},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := ValidateOrigin(testCase.raw)
			var originErr *OriginError
			if !errors.As(err, &originErr) || originErr.Reason != testCase.reason {
				t.Fatalf("error = %v, want reason %s", err, testCase.reason)
			}
		})
	}
}

func TestProbeFixedHTTPHealthContract(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != HealthPath {
			t.Fatalf("request = %s %s, want GET %s", r.Method, r.URL.Path, HealthPath)
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-Management-Key") != "" {
			t.Fatal("probe sent credentials")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","secret":"must-not-be-exposed"}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := client.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !observation.Healthy || observation.HTTPStatus != http.StatusOK || observation.Reason != ProbeReasonNone {
		t.Fatalf("observation = %#v", observation)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
}

func TestProbeNon200IsBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("raw upstream error must not be projected"))
	}))
	defer server.Close()
	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := client.Probe(context.Background())
	probeErr := requireProbeReason(t, err, ProbeReasonHTTPStatus)
	if probeErr.HTTPStatus != http.StatusServiceUnavailable || observation.Healthy || observation.HTTPStatus != http.StatusServiceUnavailable {
		t.Fatalf("observation = %#v, error = %#v", observation, probeErr)
	}
}

func TestProbeRedirectRejectedWithoutFollowing(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { targetCalls.Add(1) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer server.Close()
	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Probe(context.Background())
	requireProbeReason(t, err, ProbeReasonRedirectRejected)
	if targetCalls.Load() != 0 {
		t.Fatal("redirect target was contacted")
	}
}

func TestProbeTimeoutUsesCallerContext(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	client, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = client.Probe(ctx)
	<-started
	requireProbeReason(t, err, ProbeReasonTimeout)
}

func TestHTTPSRejectedBeforeAnyRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { calls.Add(1) }))
	defer server.Close()
	client, err := NewClient(strings.Replace(server.URL, "http://", "https://", 1))
	if client != nil {
		t.Fatal("client constructed for HTTPS origin")
	}
	var originErr *OriginError
	if !errors.As(err, &originErr) || originErr.Reason != OriginHTTPSReject {
		t.Fatalf("error = %v, want HTTPS rejection", err)
	}
	if calls.Load() != 0 {
		t.Fatal("HTTPS validation caused an outbound request")
	}
}
