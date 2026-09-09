package cliproxyapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestManagementTransportDoesNotFilterTargetAddresses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"status":"ok"}`) }))
	defer server.Close()
	for _, endpoint := range []string{"http://169.254.169.254", "http://127.0.0.1", "http://[::1]", "http://unlisted.invalid"} {
		t.Run(endpoint, func(t *testing.T) {
			// All connections are redirected by the test dialer to the local fixture.
			// No actual metadata/link-local/external service is contacted.
			dialer := &mappedDialer{actual: server.Listener.Addr().String(), advertised: authorizedTestIP}
			transport, err := newTransport(endpoint, mustManagementConfig(t, nil, nil, nil), transportOptions{Dialer: dialer})
			if err != nil {
				t.Fatal(err)
			}
			response, err := transport.getHealth(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if dialer.callCount() != 1 {
				t.Fatal("unexpected dialing count")
			}
		})
	}
}
