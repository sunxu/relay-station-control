package cliproxyapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	rootdrivers "github.com/sunxu/relay-station-control/internal/drivers"
)

func TestManagementTransportAllowsHTTPWithoutTargetPolicy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	}))
	defer server.Close()
	validated, err := (rootdrivers.ManagementConfig{}).Validate()
	if err != nil {
		t.Fatal(err)
	}
	transport, err := newTransport(server.URL, validated, transportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	response, err := transport.getHealth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
}

func TestManagementEndpointKeepsURLStructureValidation(t *testing.T) {
	for _, endpoint := range []string{
		"http://127.0.0.1:8080", "http://169.254.169.254",
	} {
		if _, err := validateEndpoint(endpoint); err != nil {
			t.Errorf("valid management endpoint rejected: %s", endpoint)
		}
	}
	for _, endpoint := range []string{
		"", "ftp://example.test", "https://user:password@example.test", "https://example.test?token=redacted",
		"https://example.test#fragment", "http://example.test:0", "http://example.test:65536", "http://example.test/../management",
	} {
		if _, err := validateEndpoint(endpoint); err == nil {
			t.Errorf("invalid URL structure accepted: %s", endpoint)
		}
	}
}
