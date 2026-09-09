package gatewaydirectory

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	drivers "github.com/sunxu/relay-station-control/internal/drivers"
)

func TestManagementHTTPOnly(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != directoryPath || r.Header.Get("Authorization") != "Bearer test-reader" {
			t.Error("fixed request contract violated")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"schema_version":1,"generated_at":"2026-09-07T00:00:00Z","accounts":[{"id":9007199254740993,"name":"test","platform":"openai","type":"apikey","url":null,"status":"active"}]}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, writeTestSecretResolver(t, "file://test/reader", "test-reader"))
	if err != nil {
		t.Fatal(err)
	}
	result, _, err := client.Fetch(context.Background(), drivers.NewSecretReference("file://test/reader"))
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || len(result.Accounts) != 1 || result.Accounts[0].ID != 9007199254740993 {
		t.Fatal("request count or source identity mismatch")
	}
}

func TestManagementHTTPSRejectedBeforeRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer server.Close()
	client, err := NewClient(strings.Replace(server.URL, "http://", "https://", 1), writeTestSecretResolver(t, "file://test/reader", "synthetic-canary"))
	if err == nil {
		t.Fatal("expected HTTPS endpoint rejection")
	}
	requireFetchReason(t, err, drivers.ReasonTLSRejected)
	if client != nil || requests.Load() != 0 {
		t.Fatal("HTTPS endpoint reached runtime")
	}
}

func TestManagementRedirectRejected(t *testing.T) {
	var redirected atomic.Int32
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer trap.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, trap.URL, http.StatusFound) }))
	defer server.Close()
	client, err := NewClient(server.URL, writeTestSecretResolver(t, "file://test/reader", "synthetic-canary"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _, err = client.Fetch(ctx, drivers.NewSecretReference("file://test/reader"))
	requireFetchReason(t, err, drivers.ReasonRedirectRejected)
	if redirected.Load() != 0 {
		t.Fatal("redirect forwarded the authenticated request")
	}
}
