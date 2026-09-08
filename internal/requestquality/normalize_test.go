package requestquality

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/sunxu/relay-station-control/internal/inventorypoll"
	"strings"
	"testing"
	"time"
)

func TestNormalizeIdentityEvidence(t *testing.T) {
	node := uuid.New()
	a := Identity{AuthIndex: "index", Provider: " OpenAI ", Email: " Alice@Example.invalid "}
	b := Identity{AuthIndex: "index", Provider: "openai", Email: "bob@example.invalid"}
	for _, tc := range []struct {
		name     string
		fields   map[string]any
		lookup   []Identity
		key      string
		provider string
	}{
		{"direct email", map[string]any{"email": " Alice@Example.invalid "}, nil, "openai:alice@example.invalid", "openai"},
		{"direct source email", map[string]any{"source": "alice@example.invalid"}, nil, "openai:alice@example.invalid", "openai"},
		{"direct account email", map[string]any{"account": "alice@example.invalid"}, nil, "openai:alice@example.invalid", "openai"},
		{"canonical explicit email", map[string]any{"email": "inventory-non-RFC-identity"}, nil, "openai:inventory-non-rfc-identity", "openai"},
		{"unique index", map[string]any{"auth_index": "index"}, []Identity{a}, "openai:alice@example.invalid", "openai"},
		{"same mapping twice", map[string]any{"auth_index": "index"}, []Identity{a, a}, "openai:alice@example.invalid", "openai"},
		{"index supplies provider", map[string]any{"auth_index": "index", "provider": ""}, []Identity{a}, "openai:alice@example.invalid", "openai"},
		{"missing index", map[string]any{"auth_index": "missing"}, []Identity{a}, "", "openai"},
		{"deleted current missing", map[string]any{"auth_index": "index"}, nil, "", "openai"},
		{"ambiguous index", map[string]any{"auth_index": "index"}, []Identity{a, b}, "", "openai"},
		{"direct conflicts lookup", map[string]any{"auth_index": "index", "email": "bob@example.invalid"}, []Identity{a}, "", "openai"},
		{"direct matches one of ambiguous", map[string]any{"auth_index": "index", "email": "alice@example.invalid"}, []Identity{a, b}, "", "openai"},
		{"provider conflict", map[string]any{"auth_index": "index", "provider": "codex"}, []Identity{a}, "", "codex"},
		{"snapshot provider conflict", map[string]any{"email": "alice@example.invalid", "auth_provider_snapshot": "codex"}, nil, "", "openai"},
		{"direct evidence conflict", map[string]any{"email": "alice@example.invalid", "source": "bob@example.invalid"}, nil, "", "openai"},
		{"missing email in matching snapshot", map[string]any{"auth_index": "index"}, []Identity{a, {AuthIndex: "index", Provider: "openai"}}, "", "openai"},
		{"API key not identity", map[string]any{"source": "sk-not-an-email", "auth_index": "index"}, nil, "", "openai"},
		{"project not identity", map[string]any{"source": "project-id"}, nil, "", "openai"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields := map[string]any{"timestamp": "2026-09-08T00:00:00Z", "provider": "OpenAI", "failed": false}
			for k, v := range tc.fields {
				fields[k] = v
			}
			raw, _ := json.Marshal(fields)
			event, err := Normalize(node, raw, tc.lookup)
			if err != nil {
				t.Fatal(err)
			}
			got := ""
			if event.AccountKey != nil {
				got = *event.AccountKey
			}
			if got != tc.key || event.Provider != tc.provider {
				t.Fatalf("key=%q provider=%q want=%q/%q", got, event.Provider, tc.key, tc.provider)
			}
			if got != "" {
				parts := strings.SplitN(got, ":", 2)
				_, _, canonical, err := inventorypoll.NormalizeAccountIdentity(parts[0], parts[1])
				if err != nil || canonical != got {
					t.Fatal("canonical mismatch")
				}
			}
		})
	}
}

func TestNormalizeCPAFieldsAndContentIdentity(t *testing.T) {
	node := uuid.New()
	raw := []byte(`{"request_id":"9007199254740993","timestamp":"2026-09-08T08:00:00+08:00","provider":"openai","source":"alice@example.invalid","auth_index":"idx","alias":"asked-model","model":"resolved-model","endpoint":"POST /v1/responses","latency_ms":123,"failed":true,"fail":{"status_code":429,"body":"rate limited"},"tokens":{"input_tokens":3,"output_tokens":4,"reasoning_tokens":5,"cached_tokens":6}}`)
	e, err := Normalize(node, raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if e.RequestID != "9007199254740993" || e.Model != "asked-model" || e.Success || e.FailureClass == nil || *e.FailureClass != "rate_limit" || e.DurationMS == nil || *e.DurationMS != 123 || e.OccurredAt.Format(time.RFC3339) != "2026-09-08T00:00:00Z" {
		t.Fatalf("bad normalized fields: %+v", e)
	}
	// CPA buildEventHash field order; no account resolution result enters identity.
	want := hashString(strings.Join([]string{"9007199254740993", "2026-09-08T00:00:00Z", "POST /v1/responses", "asked-model", "idx", hashString("alice@example.invalid"), "3", "4", "5", "6", "true", "123"}, "|"))
	if e.EventHash != want {
		t.Fatalf("hash=%s want=%s", e.EventHash, want)
	}
	var reordered map[string]any
	_ = json.Unmarshal(raw, &reordered)
	pretty, _ := json.MarshalIndent(reordered, "", " ")
	again, err := Normalize(node, pretty, []Identity{{AuthIndex: "idx", Provider: "openai", Email: "alice@example.invalid"}})
	if err != nil || again.EventHash != e.EventHash {
		t.Fatal("formatting/lookup changed content identity")
	}
	for _, stamp := range []string{`1788825600000`, `"1788825600"`, `"2026-09-08 00:00:00"`} {
		event, err := Normalize(node, []byte(`{"timestamp":`+stamp+`,"failed":false}`), nil)
		if err != nil || event.OccurredAt.Format(time.RFC3339) != "2026-09-08T00:00:00Z" {
			t.Fatalf("timestamp=%s event=%+v err=%v", stamp, event, err)
		}
	}
}
func TestNormalizeFailuresAndMalformed(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`"failed":false,"fail_status_code":500`, ""}, // failed has CPA precedence
		{`"success":true,"status":500`, ""},
		{`"failed":true,"fail":{"status_code":401}`, "auth"},
		{`"failed":true,"fail_status_code":403`, "auth"},
		{`"failed":true,"fail_summary":"token_revoked"`, "auth"},
		{`"failed":true,"fail":{"status_code":429,"body":"insufficient_quota"}`, "quota"},
		{`"failed":true,"fail_status_code":429`, "rate_limit"},
		{`"status_code":503`, "upstream"},
		{`"error":{"message":"unclassified"}`, "unknown"},
		{`"failed":true,"fail_status_code":400`, "unknown"},
	} {
		e, err := Normalize(uuid.New(), []byte(`{"timestamp":"2026-09-08T00:00:00Z",`+tc.raw+`}`), nil)
		if err != nil {
			t.Fatal(err)
		}
		got := ""
		if e.FailureClass != nil {
			got = *e.FailureClass
		}
		if got != tc.want || e.Success != (tc.want == "") {
			t.Fatalf("%s got=%q success=%v", tc.raw, got, e.Success)
		}
	}
	for _, raw := range []string{`null`, `[]`, `{}`, `{"timestamp":"invalid"}`, `{"timestamp":"2026-09-08T00:00:00Z"} {}`, `{"timestamp":"2026-09-08T00:00:00Z","latency_ms":-1}`} {
		if _, err := Normalize(uuid.New(), []byte(raw), nil); err == nil {
			t.Fatalf("accepted malformed %s", raw)
		}
	}
}
