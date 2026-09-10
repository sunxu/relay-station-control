package store

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	authn "github.com/sunxu/relay-station-control/internal/auth"
)

func TestProblemAccountCursorCodecRoundTripAndBindings(t *testing.T) {
	keyring := problemAccountTestKeyring(t, 1)
	codec, err := NewProblemAccountCursorCodec(keyring)
	if err != nil {
		t.Fatal(err)
	}
	actor, node := uuid.New(), uuid.New()
	filters := ProblemAccountFilters{Provider: " OpenAI ", NodeID: node, Severity: "Critical", Reason: "TOKEN_INVALID", Email: " Alice@Example.com "}
	want := ProblemAccountCursor{Severity: "Critical", Since: time.Date(2026, 9, 11, 1, 2, 3, 123456789, time.UTC), Email: "alice@example.com", NodeID: node}
	encoded, err := codec.Encode(actor, filters, want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := codec.Decode(encoded, actor, filters)
	if err != nil {
		t.Fatal(err)
	}
	if got != (ProblemAccountCursor{Severity: want.Severity, Since: want.Since, Email: "alice@example.com", NodeID: node}) {
		t.Fatalf("cursor=%+v", got)
	}
	if len(encoded) > problemAccountCursorMaxEncoded {
		t.Fatalf("encoded cursor length=%d", len(encoded))
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var payload problemAccountCursorPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Sort != "severity_desc,since_asc,email_asc,node_id_asc" {
		t.Fatalf("sort identity=%q", payload.Sort)
	}
	localCursor := want
	localCursor.Since = want.Since.In(time.FixedZone("database connection", 8*60*60))
	localEncoded, err := codec.Encode(actor, filters, localCursor)
	if err != nil || localEncoded != encoded {
		t.Fatalf("DB timezone altered canonical cursor: %v", err)
	}

	for name, mutate := range map[string]func(ProblemAccountFilters, ProblemAccountCursor, uuid.UUID) (ProblemAccountFilters, ProblemAccountCursor, uuid.UUID){
		"wrong actor": func(f ProblemAccountFilters, c ProblemAccountCursor, a uuid.UUID) (ProblemAccountFilters, ProblemAccountCursor, uuid.UUID) {
			return f, c, uuid.New()
		},
		"wrong filter": func(f ProblemAccountFilters, c ProblemAccountCursor, a uuid.UUID) (ProblemAccountFilters, ProblemAccountCursor, uuid.UUID) {
			f.Reason = "ACCOUNT_BLOCKED"
			return f, c, a
		},
	} {
		f, c, a := mutate(filters, want, actor)
		candidate, err := codec.Encode(a, f, c)
		if err != nil {
			continue
		}
		if _, err := codec.Decode(candidate, actor, filters); err != ErrInvalidProblemAccountQuery {
			t.Fatalf("%s: err=%v", name, err)
		}
	}
}

func TestProblemAccountCursorCodecRejectsMalformedAndStrictPayloads(t *testing.T) {
	keyring := problemAccountTestKeyring(t, 1)
	codec, err := NewProblemAccountCursorCodec(keyring)
	if err != nil {
		t.Fatal(err)
	}
	actor, node := uuid.New(), uuid.New()
	filters := ProblemAccountFilters{NodeID: node}
	cursor := ProblemAccountCursor{Severity: "Warning", Since: time.Date(2026, 9, 11, 1, 2, 3, 0, time.UTC), Email: "user@example.com", NodeID: node}
	encoded, err := codec.Encode(actor, filters, cursor)
	if err != nil {
		t.Fatal(err)
	}
	bad := []string{"", "not-base64!", encoded + "="}
	for _, value := range bad {
		if _, err := codec.Decode(value, actor, filters); err != ErrInvalidProblemAccountQuery {
			t.Fatalf("malformed %q: err=%v", value, err)
		}
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	tamperedRow := payload["row_cursor"].(map[string]any)
	tamperedRow["email"] = "tampered@example.com"
	tamperedRaw, _ := json.Marshal(payload)
	if _, err := codec.Decode(base64.RawURLEncoding.EncodeToString(tamperedRaw), actor, filters); err != ErrInvalidProblemAccountQuery {
		t.Fatalf("tampered cursor err=%v", err)
	}
	payload["unexpected"] = true
	raw, _ = json.Marshal(payload)
	strict := base64.RawURLEncoding.EncodeToString(raw)
	if _, err := codec.Decode(strict, actor, filters); err != ErrInvalidProblemAccountQuery {
		t.Fatalf("unknown field err=%v", err)
	}
	if _, err := codec.Decode(encoded+strings.Repeat("x", problemAccountCursorMaxEncoded), actor, filters); err != ErrInvalidProblemAccountQuery {
		t.Fatalf("oversized err=%v", err)
	}
}

func TestProblemAccountCursorCodecRejectsUnknownKeyVersion(t *testing.T) {
	old := problemAccountTestKeyring(t, 1)
	codec, err := NewProblemAccountCursorCodec(old)
	if err != nil {
		t.Fatal(err)
	}
	actor, node := uuid.New(), uuid.New()
	filters := ProblemAccountFilters{Provider: "openai", NodeID: node}
	cursor := ProblemAccountCursor{Severity: "Critical", Since: time.Date(2026, 9, 11, 1, 2, 3, 4, time.UTC), Email: "user@example.com", NodeID: node}
	encoded, err := codec.Encode(actor, filters, cursor)
	if err != nil {
		t.Fatal(err)
	}
	rotated := problemAccountTestKeyring(t, 2)
	rotatedCodec, err := NewProblemAccountCursorCodec(rotated)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rotatedCodec.Decode(encoded, actor, filters); err != ErrInvalidProblemAccountQuery {
		t.Fatalf("missing previous key should reject: %v", err)
	}
}

func problemAccountTestKeyring(t *testing.T, version int) *authn.Keyring {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + version)
	}
	document := fmt.Sprintf(`{"format_version":1,"environment":"dev","current":%d,"keys":[{"version":%d,"key":"%s"}]}`, version, version, base64.RawStdEncoding.EncodeToString(key))
	keyring, err := authn.ParseKeyring([]byte(document), authn.EnvironmentDev)
	if err != nil {
		t.Fatal(err)
	}
	return keyring
}
