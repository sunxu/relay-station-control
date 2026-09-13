package store

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	authn "github.com/sunxu/relay-station-control/internal/auth"
)

func testNodeCursorCodec(t *testing.T, current int, versions ...int) *NodeCursorCodec {
	t.Helper()
	keys := ""
	for index, version := range versions {
		if index > 0 {
			keys += ","
		}
		keys += fmt.Sprintf(`{"version":%d,"key":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY"}`, version)
	}
	document := fmt.Sprintf(`{"format_version":1,"environment":"dev","current":%d,"keys":[%s]}`, current, keys)
	keyring, err := authn.ParseKeyring([]byte(document), authn.EnvironmentDev)
	if err != nil {
		t.Fatalf("parse test keyring: %v", err)
	}
	codec, err := NewNodeCursorCodec(keyring)
	if err != nil {
		t.Fatalf("new cursor codec: %v", err)
	}
	return codec
}

func testGatewayCursorCodec(t *testing.T) *GatewayCursorCodec {
	t.Helper()
	keyring, err := authn.ParseKeyring([]byte(`{"format_version":1,"environment":"dev","current":1,"keys":[{"version":1,"key":"MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY"}]}`), authn.EnvironmentDev)
	if err != nil {
		t.Fatalf("parse test keyring: %v", err)
	}
	codec, err := NewGatewayCursorCodec(keyring)
	if err != nil {
		t.Fatalf("new Gateway cursor codec: %v", err)
	}
	return codec
}

func TestGatewayCursorRoundTripAndScope(t *testing.T) {
	codec := testGatewayCursorCodec(t)
	want := GatewayCursor{
		After:       uuid.MustParse("018f80d8-2017-7b3e-93ec-10f4b3672f2a"),
		Environment: "dev-environment",
		Lifecycle:   "retired",
		Generation:  "7",
	}
	cursor, err := codec.Encode(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := codec.Decode(cursor, want.Lifecycle, want.Environment)
	if err != nil || got != want {
		t.Fatalf("Gateway cursor round trip = %#v, %v; want %#v", got, err, want)
	}
	for _, scope := range []struct{ lifecycle, environment string }{
		{"active", want.Environment},
		{want.Lifecycle, "another-environment"},
	} {
		if _, err := codec.Decode(cursor, scope.lifecycle, scope.environment); err != ErrInvalidGatewayCursor {
			t.Fatalf("cross-scope Gateway cursor error = %v, want %v", err, ErrInvalidGatewayCursor)
		}
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		t.Fatal(err)
	}
	tampered := base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(string(raw), `"generation":"7"`, `"generation":"8"`, 1)))
	if _, err := codec.Decode(tampered, want.Lifecycle, want.Environment); err != ErrInvalidGatewayCursor {
		t.Fatalf("tampered Gateway cursor error = %v, want %v", err, ErrInvalidGatewayCursor)
	}
}

func TestNodeCursorRoundTripAndEmptyFirstPage(t *testing.T) {
	active := true
	codec := testNodeCursorCodec(t, 1, 1)
	filters := NodeListFilters{NodeType: "cliproxyapi", Capability: "management_health_read", MonitoringActive: &active}
	after := uuid.MustParse("018f80d8-2017-7b3e-93ec-10f4b3672f2a")

	cursor, err := codec.Encode(after, filters)
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}
	decoded, err := codec.Decode(cursor, filters)
	if err != nil || decoded != after {
		t.Fatalf("decode cursor = %s, %v; want %s", decoded, err, after)
	}
	first, err := codec.Decode("", filters)
	if err != nil || first != uuid.Nil {
		t.Fatalf("empty cursor = %s, %v; want nil UUID", first, err)
	}
}

func TestNodeCursorRejectsTamperCrossFilterAndInvalidUUID(t *testing.T) {
	after := uuid.MustParse("018f80d8-2017-7b3e-93ec-10f4b3672f2a")
	codec := testNodeCursorCodec(t, 1, 1)
	filters := NodeListFilters{NodeType: "cliproxyapi"}
	cursor, err := codec.Encode(after, filters)
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		t.Fatalf("decode test cursor: %v", err)
	}
	tampered := base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(string(raw), after.String(), "018f80d8-2017-7b3e-93ec-10f4b3672f2b", 1)))

	falseValue := false
	tests := []struct {
		name    string
		cursor  string
		filters NodeListFilters
	}{
		{name: "tampered", cursor: tampered, filters: filters},
		{name: "cross filter", cursor: cursor, filters: NodeListFilters{NodeType: "cliproxyapi", MonitoringActive: &falseValue}},
		{name: "invalid uuid", cursor: base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"after":"not-a-uuid","filters_hash":"x","digest":"x"}`)), filters: filters},
		{name: "invalid base64", cursor: "not+base64", filters: filters},
		{name: "trailing json", cursor: base64.RawURLEncoding.EncodeToString(append(raw, []byte(` {}`)...)), filters: filters},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := codec.Decode(test.cursor, test.filters); err != ErrInvalidNodeCursor {
				t.Fatalf("DecodeNodeCursor error = %v, want %v", err, ErrInvalidNodeCursor)
			}
		})
	}
}

func TestEncodeNodeCursorRejectsNilUUID(t *testing.T) {
	codec := testNodeCursorCodec(t, 1, 1)
	if _, err := codec.Encode(uuid.Nil, NodeListFilters{}); err != ErrInvalidNodeCursor {
		t.Fatalf("EncodeNodeCursor error = %v, want %v", err, ErrInvalidNodeCursor)
	}
}

func TestNodeCursorAcceptsRetainedOldKeyAndRejectsRemovedKey(t *testing.T) {
	after := uuid.MustParse("018f80d8-2017-7b3e-93ec-10f4b3672f2a")
	oldCodec := testNodeCursorCodec(t, 1, 1)
	cursor, err := oldCodec.Encode(after, NodeListFilters{})
	if err != nil {
		t.Fatalf("encode old-key cursor: %v", err)
	}
	rotated := testNodeCursorCodec(t, 2, 1, 2)
	if decoded, err := rotated.Decode(cursor, NodeListFilters{}); err != nil || decoded != after {
		t.Fatalf("retained old-key cursor = %s, %v", decoded, err)
	}
	removed := testNodeCursorCodec(t, 2, 2)
	if _, err := removed.Decode(cursor, NodeListFilters{}); err != ErrInvalidNodeCursor {
		t.Fatalf("removed old-key cursor error = %v, want %v", err, ErrInvalidNodeCursor)
	}
}
