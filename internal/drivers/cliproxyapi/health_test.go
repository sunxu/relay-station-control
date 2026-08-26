package cliproxyapi

import (
	"errors"
	"strings"
	"testing"
)

func TestParseHealthClosedContract(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		status     int
		body       string
		wantReason HealthReason
		wantOK     bool
	}{
		{name: "ok", status: 200, body: `{"status":"ok"}`, wantReason: HealthReasonOK, wantOK: true},
		{name: "unknown fields discarded", status: 200, body: `{"status":"ok","secret":"HEALTH-CANARY"}`, wantReason: HealthReasonOK, wantOK: true},
		{name: "non 200", status: 503, body: `{"status":"ok"}`, wantReason: HealthReasonHTTPStatus},
		{name: "malformed", status: 200, body: `{`, wantReason: HealthReasonResponseInvalid},
		{name: "array", status: 200, body: `[{"status":"ok"}]`, wantReason: HealthReasonResponseInvalid},
		{name: "wrong type", status: 200, body: `{"status":true}`, wantReason: HealthReasonResponseInvalid},
		{name: "unknown state", status: 200, body: `{"status":"ready"}`, wantReason: HealthReasonResponseInvalid},
		{name: "trailing JSON", status: 200, body: `{"status":"ok"}{}`, wantReason: HealthReasonResponseInvalid},
		{name: "duplicate status", status: 200, body: `{"status":"ready","status":"ok"}`, wantReason: HealthReasonResponseInvalid},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := parseHealth(test.status, strings.NewReader(test.body), 1024)
			if got.Reason != test.wantReason || got.Reachable != test.wantOK || got.ResponseValid != test.wantOK {
				t.Fatalf("parseHealth() = %#v", got)
			}
			if got.TransportSuccess != (test.status == 200) {
				t.Fatalf("TransportSuccess = %t", got.TransportSuccess)
			}
		})
	}
}

func TestParseHealthBodyBoundaryAndReaderFailure(t *testing.T) {
	t.Parallel()
	body := `{"status":"ok"}`
	if got := parseHealth(200, strings.NewReader(body), int64(len(body))); !got.Reachable {
		t.Fatalf("exact boundary rejected: %#v", got)
	}
	if got := parseHealth(200, strings.NewReader(body), int64(len(body)-1)); got.Reason != HealthReasonResponseTooLarge {
		t.Fatalf("over boundary = %#v", got)
	}
	if got := parseHealth(200, failingReader{}, 1024); got.Reason != HealthReasonResponseInvalid {
		t.Fatalf("reader failure = %#v", got)
	}
	if got := parseHealth(200, strings.NewReader(body), maximumHealthBodyLimit+1); got.Reason != HealthReasonResponseInvalid {
		t.Fatalf("unsafe configured limit = %#v", got)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("SENSITIVE-UPSTREAM-ERROR") }
