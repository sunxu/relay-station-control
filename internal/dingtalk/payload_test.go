package dingtalk

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sunxu/relay-station-control/internal/jobs"
)

func TestPayloadAcceptsExistingDomainStringBounds(t *testing.T) {
	var payload Payload
	if err := json.Unmarshal(validPayloadJSON(), &payload); err != nil {
		t.Fatal(err)
	}
	payload.EnvironmentName = strings.Repeat("界", 100)
	payload.NodeNames = []string{strings.Repeat("🌐", 100), strings.Repeat("界", 100)}
	payload.Email = strings.Repeat("a", 300) + "@example.invalid"
	payload.AccountKey = "antigravity:" + payload.Email
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := jobs.NewRegistry(Definition(noopExecutor{}))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := registry.ValidateAndHash(JobKind, 1, raw); err != nil {
		t.Fatalf("valid domain snapshot rejected: %v", err)
	}
	if _, err := renderPayload(raw); err != nil {
		t.Fatal(err)
	}
}

func TestResolvedDuplicateAllowsEmptyAuthoritativeMembership(t *testing.T) {
	var payload Payload
	if err := json.Unmarshal(validPayloadJSON(), &payload); err != nil {
		t.Fatal(err)
	}
	payload.OccurrenceType = "CROSS_NODE_DUPLICATE_OWNERSHIP"
	payload.Reason = "cross_node_duplicate_ownership"
	payload.Transition = "RESOLVED"
	payload.InstanceIDs = []string{}
	payload.NodeNames = []string{}
	registry, err := jobs.NewRegistry(Definition(noopExecutor{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		transition string
		nilArrays  bool
		valid      bool
	}{
		{"resolved zero owners", "RESOLVED", false, true},
		{"active zero owners", "ACTIVE", false, false},
		{"resolved null arrays", "RESOLVED", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := payload
			value.Transition = test.transition
			if test.nilArrays {
				value.InstanceIDs = nil
				value.NodeNames = nil
			}
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			_, _, _, err = registry.ValidateAndHash(JobKind, 1, raw)
			if test.valid && err != nil {
				t.Fatal(err)
			}
			if !test.valid && !errors.Is(err, jobs.ErrInvalidPayload) {
				t.Fatalf("expected invalid payload, got %v", err)
			}
			if test.valid {
				if _, err := renderPayload(raw); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func validPayloadJSON() []byte {
	return []byte(`{"occurrence_id":"018f80d8-2017-7b3e-93ec-10f4b3672f2a","occurrence_type":"TOKEN_INVALID","transition":"ACTIVE","reason":"token_invalid","severity":"Critical","environment_id":"env-1","environment_name":"Production","account_key":"account-1","email":"user@example.com","provider":"antigravity","instance_ids":["018f80d8-2017-7b3e-93ec-10f4b3672f2a","118f80d8-2017-7b3e-93ec-10f4b3672f2a"],"node_names":["node-free-001","node-free-002"],"started_at":"2026-09-11T08:00:00Z","transitioned_at":"2026-09-11T08:01:00Z"}`)
}

func TestRenderPayloadDeterministicAndOfficialShape(t *testing.T) {
	want := `{"msgtype":"text","text":{"content":"[Critical] TOKEN_INVALID\n\nAccount: user@example.com\nProvider: antigravity\nNode: node-free-001, node-free-002\nSince: 2026-09-11T08:00:00Z\nOccurrence ID: 018f80d8-2017-7b3e-93ec-10f4b3672f2a"}}`
	got, err := renderPayload(validPayloadJSON())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("rendered request = %s, want %s", got, want)
	}
	again, err := renderPayload([]byte(`{"node_names":["node-free-001","node-free-002"],"instance_ids":["018f80d8-2017-7b3e-93ec-10f4b3672f2a","118f80d8-2017-7b3e-93ec-10f4b3672f2a"],"transitioned_at":"2026-09-11T08:01:00Z","started_at":"2026-09-11T08:00:00Z","provider":"antigravity","email":"user@example.com","account_key":"account-1","environment_name":"Production","environment_id":"env-1","severity":"Critical","reason":"token_invalid","transition":"ACTIVE","occurrence_type":"TOKEN_INVALID","occurrence_id":"018f80d8-2017-7b3e-93ec-10f4b3672f2a"}`))
	if err != nil || !bytes.Equal(got, again) {
		t.Fatalf("equivalent payload did not render deterministically: %s / %s (%v)", got, again, err)
	}
}

func TestRenderPayloadTransitionsAndStrictNegativeCases(t *testing.T) {
	resolved := append([]byte{}, validPayloadJSON()...)
	resolved = bytes.Replace(resolved, []byte(`"transition":"ACTIVE"`), []byte(`"transition":"RESOLVED"`), 1)
	got, err := renderPayload(resolved)
	if err != nil || !bytes.Contains(got, []byte(`[Resolved] TOKEN_INVALID`)) || !bytes.Contains(got, []byte(`Started:`)) || !bytes.Contains(got, []byte(`Resolved:`)) {
		t.Fatalf("resolved rendering = %s, err = %v", got, err)
	}
	for name, raw := range map[string][]byte{
		"unknown field":          append(validPayloadJSON()[:len(validPayloadJSON())-1], []byte(`,"token":"secret"}`)...),
		"unsorted instance ids":  bytes.Replace(validPayloadJSON(), []byte(`"instance_ids":["018f80d8-2017-7b3e-93ec-10f4b3672f2a","118f80d8-2017-7b3e-93ec-10f4b3672f2a"]`), []byte(`"instance_ids":["118f80d8-2017-7b3e-93ec-10f4b3672f2a","018f80d8-2017-7b3e-93ec-10f4b3672f2a"]`), 1),
		"invalid transition":     bytes.Replace(validPayloadJSON(), []byte(`"transition":"ACTIVE"`), []byte(`"transition":"PENDING"`), 1),
		"invalid issue type":     bytes.Replace(validPayloadJSON(), []byte(`"occurrence_type":"TOKEN_INVALID"`), []byte(`"occurrence_type":"OTHER"`), 1),
		"invalid timestamp":      bytes.Replace(validPayloadJSON(), []byte(`"started_at":"2026-09-11T08:00:00Z"`), []byte(`"started_at":"not-a-time"`), 1),
		"empty instance ids":     bytes.Replace(validPayloadJSON(), []byte(`"instance_ids":["018f80d8-2017-7b3e-93ec-10f4b3672f2a","118f80d8-2017-7b3e-93ec-10f4b3672f2a"]`), []byte(`"instance_ids":[]`), 1),
		"invalid instance id":    bytes.Replace(validPayloadJSON(), []byte(`"instance_ids":["018f80d8-2017-7b3e-93ec-10f4b3672f2a"`), []byte(`"instance_ids":["not-an-instance-id"`), 1),
		"empty node names":       bytes.Replace(validPayloadJSON(), []byte(`"node_names":["node-free-001","node-free-002"]`), []byte(`"node_names":[]`), 1),
		"different cardinality":  bytes.Replace(validPayloadJSON(), []byte(`"node_names":["node-free-001","node-free-002"]`), []byte(`"node_names":["node-free-001"]`), 1),
		"noncanonical uuid":      bytes.Replace(validPayloadJSON(), []byte(`"instance_ids":["018f80d8-2017-7b3e-93ec-10f4b3672f2a","118f80d8-2017-7b3e-93ec-10f4b3672f2a"]`), []byte(`"instance_ids":["018F80D8-2017-7b3e-93ec-10f4b3672f2a","118f80d8-2017-7b3e-93ec-10f4b3672f2a"]`), 1),
		"uuid without hyphens":   bytes.Replace(validPayloadJSON(), []byte(`"instance_ids":["018f80d8-2017-7b3e-93ec-10f4b3672f2a","118f80d8-2017-7b3e-93ec-10f4b3672f2a"]`), []byte(`"instance_ids":["018f80d820177b3e93ec10f4b3672f2a","118f80d8-2017-7b3e-93ec-10f4b3672f2a"]`), 1),
		"nil uuid":               bytes.Replace(validPayloadJSON(), []byte(`"118f80d8-2017-7b3e-93ec-10f4b3672f2a"`), []byte(`"00000000-0000-0000-0000-000000000000"`), 1),
		"duplicate instance ids": bytes.Replace(validPayloadJSON(), []byte(`"118f80d8-2017-7b3e-93ec-10f4b3672f2a"`), []byte(`"018f80d8-2017-7b3e-93ec-10f4b3672f2a"`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := renderPayload(raw); err == nil {
				t.Fatal("invalid payload accepted")
			}
			registry, err := jobs.NewRegistry(Definition(noopExecutor{}))
			if err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := registry.ValidateAndHash(JobKind, 1, raw); !errors.Is(err, jobs.ErrInvalidPayload) {
				t.Fatalf("registry accepted invalid payload: %v", err)
			}
		})
	}
}

func TestCanonicalTimestamps(t *testing.T) {
	invalid := map[string]string{
		"offset":        "2026-09-11T09:00:00+08:00",
		"zero offset":   "2026-09-11T08:00:00+00:00",
		"zero fraction": "2026-09-11T08:00:00.000Z",
		"four fraction": "2026-09-11T08:00:00.1000Z",
		"comma":         "2026-09-11T08:00:00,1Z",
		"too precise":   "2026-09-11T08:00:00.1234567890Z",
	}
	registry, err := jobs.NewRegistry(Definition(noopExecutor{}))
	if err != nil {
		t.Fatal(err)
	}
	for field, baseline := range map[string]string{
		"started_at":      "2026-09-11T08:00:00Z",
		"transitioned_at": "2026-09-11T08:01:00Z",
	} {
		for name, timestamp := range invalid {
			t.Run(field+"/"+name, func(t *testing.T) {
				raw := bytes.Replace(validPayloadJSON(), []byte(`"`+field+`":"`+baseline+`"`), []byte(`"`+field+`":"`+timestamp+`"`), 1)
				if _, err := renderPayload(raw); !errors.Is(err, jobs.ErrInvalidPayload) {
					t.Fatalf("render got %v, want ErrInvalidPayload", err)
				}
				if _, _, _, err := registry.ValidateAndHash(JobKind, 1, raw); !errors.Is(err, jobs.ErrInvalidPayload) {
					t.Fatalf("registry got %v, want ErrInvalidPayload", err)
				}
			})
		}
	}
	base := time.Date(2026, time.September, 11, 8, 0, 0, 123456789, time.FixedZone("CST", 8*60*60)).UTC()
	ended := base.Add(time.Minute)
	canonical := map[string]string{
		"started_at":      base.Format(time.RFC3339Nano),
		"transitioned_at": ended.Format(time.RFC3339Nano),
	}
	raw := bytes.Replace(validPayloadJSON(), []byte(`"started_at":"2026-09-11T08:00:00Z"`), []byte(`"started_at":"`+canonical["started_at"]+`"`), 1)
	raw = bytes.Replace(raw, []byte(`"transitioned_at":"2026-09-11T08:01:00Z"`), []byte(`"transitioned_at":"`+canonical["transitioned_at"]+`"`), 1)
	t.Run("canonical/both fields", func(t *testing.T) {
		if _, err := renderPayload(raw); err != nil {
			t.Fatalf("render rejected canonical timestamps: %v", err)
		}
		if _, _, _, err := registry.ValidateAndHash(JobKind, 1, raw); err != nil {
			t.Fatalf("registry rejected canonical timestamps: %v", err)
		}
	})
	badOrder := bytes.Replace(validPayloadJSON(), []byte(`"transitioned_at":"2026-09-11T08:01:00Z"`), []byte(`"transitioned_at":"2026-09-11T07:59:00Z"`), 1)
	if _, err := renderPayload(badOrder); !errors.Is(err, jobs.ErrInvalidPayload) {
		t.Fatalf("reverse timestamp order: %v", err)
	}
	if _, _, _, err := registry.ValidateAndHash(JobKind, 1, badOrder); !errors.Is(err, jobs.ErrInvalidPayload) {
		t.Fatalf("registry accepted reverse timestamp order: %v", err)
	}
}

func TestAllSupportedPayloadMessagesAndSecretFields(t *testing.T) {
	for _, issue := range []string{"TOKEN_INVALID", "ACCOUNT_BLOCKED", "FORBIDDEN", "CROSS_NODE_DUPLICATE_OWNERSHIP"} {
		for _, transition := range []string{"ACTIVE", "RESOLVED"} {
			raw := bytes.ReplaceAll(validPayloadJSON(), []byte("TOKEN_INVALID"), []byte(issue))
			raw = bytes.ReplaceAll(raw, []byte("token_invalid"), []byte(strings.ToLower(issue)))
			raw = bytes.ReplaceAll(raw, []byte("ACTIVE"), []byte(transition))
			if issue == "FORBIDDEN" {
				raw = bytes.ReplaceAll(raw, []byte("Critical"), []byte("Warning"))
			}
			out, err := renderPayload(raw)
			if err != nil || !bytes.Contains(out, []byte(issue)) || !bytes.Contains(out, []byte("node-free-001, node-free-002")) {
				t.Fatalf("%s/%s: %s %v", issue, transition, out, err)
			}
		}
	}
	for _, name := range []string{"webhook_url", "signing_secret", "access_token", "refresh_token", "auth_file", "oauth_secret", "raw_response", "token_state", "last_refresh", "expected_valid_until"} {
		raw := append(validPayloadJSON()[:len(validPayloadJSON())-1], []byte(`,"`+name+`":"secret-canary"}`)...)
		if _, err := payloadSchema.Canonicalize(raw); err == nil {
			t.Fatalf("accepted forbidden field %s", name)
		}
	}
}

func TestPayloadIssueReasonSeverityTuples(t *testing.T) {
	valid := []struct {
		issue, reason, severity string
	}{
		{"TOKEN_INVALID", "token_invalid", "Critical"},
		{"ACCOUNT_BLOCKED", "account_blocked", "Critical"},
		{"FORBIDDEN", "forbidden", "Warning"},
		{"CROSS_NODE_DUPLICATE_OWNERSHIP", "cross_node_duplicate_ownership", "Critical"},
	}
	for _, tc := range valid {
		t.Run(tc.issue, func(t *testing.T) {
			raw := validPayloadJSON()
			raw = bytes.ReplaceAll(raw, []byte(`"TOKEN_INVALID"`), []byte(`"`+tc.issue+`"`))
			raw = bytes.ReplaceAll(raw, []byte(`"token_invalid"`), []byte(`"`+tc.reason+`"`))
			raw = bytes.ReplaceAll(raw, []byte(`"Critical"`), []byte(`"`+tc.severity+`"`))
			if _, err := renderPayload(raw); err != nil {
				t.Fatalf("valid tuple rejected: %v", err)
			}
			registry, err := jobs.NewRegistry(Definition(noopExecutor{}))
			if err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := registry.ValidateAndHash(JobKind, 1, raw); err != nil {
				t.Fatalf("valid tuple rejected by registry: %v", err)
			}
		})
	}

	invalid := []struct {
		name, issue, reason, severity string
	}{
		{"token invalid wrong reason", "TOKEN_INVALID", "account_blocked", "Critical"},
		{"token invalid forbidden reason", "TOKEN_INVALID", "forbidden", "Critical"},
		{"token invalid wrong severity", "TOKEN_INVALID", "token_invalid", "Warning"},
		{"account blocked wrong reason", "ACCOUNT_BLOCKED", "token_invalid", "Critical"},
		{"account blocked wrong severity", "ACCOUNT_BLOCKED", "account_blocked", "Warning"},
		{"forbidden wrong reason", "FORBIDDEN", "token_invalid", "Warning"},
		{"forbidden wrong severity", "FORBIDDEN", "forbidden", "Critical"},
		{"duplicate wrong reason", "CROSS_NODE_DUPLICATE_OWNERSHIP", "token_invalid", "Critical"},
		{"duplicate wrong severity", "CROSS_NODE_DUPLICATE_OWNERSHIP", "cross_node_duplicate_ownership", "Warning"},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			raw := validPayloadJSON()
			raw = bytes.ReplaceAll(raw, []byte(`"TOKEN_INVALID"`), []byte(`"`+tc.issue+`"`))
			raw = bytes.ReplaceAll(raw, []byte(`"token_invalid"`), []byte(`"`+tc.reason+`"`))
			raw = bytes.ReplaceAll(raw, []byte(`"Critical"`), []byte(`"`+tc.severity+`"`))
			if _, err := renderPayload(raw); !errors.Is(err, jobs.ErrInvalidPayload) {
				t.Fatalf("got %v, want ErrInvalidPayload", err)
			}
			registry, err := jobs.NewRegistry(Definition(noopExecutor{}))
			if err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := registry.ValidateAndHash(JobKind, 1, raw); !errors.Is(err, jobs.ErrInvalidPayload) {
				t.Fatalf("registry got %v, want ErrInvalidPayload", err)
			}
		})
	}
}

func TestParallelNodeCollectionsPreservePairing(t *testing.T) {
	registry, err := jobs.NewRegistry(Definition(noopExecutor{}))
	if err != nil {
		t.Fatal(err)
	}
	for name, replacement := range map[string]string{
		"duplicate display names":  `"node_names":["Relay","Relay"]`,
		"mismatched lexical order": `"node_names":["Zulu","Alpha"]`,
	} {
		t.Run(name, func(t *testing.T) {
			raw := bytes.Replace(validPayloadJSON(), []byte(`"node_names":["node-free-001","node-free-002"]`), []byte(replacement), 1)
			canonical, hash, _, err := registry.ValidateAndHash(JobKind, 1, raw)
			if err != nil {
				t.Fatalf("payload rejected: %v", err)
			}
			if len(canonical) == 0 || hash == [32]byte{} {
				t.Fatal("canonical payload/hash missing")
			}
			out, err := renderPayload(canonical)
			if err != nil || !bytes.Contains(out, []byte("Node: ")) {
				t.Fatalf("render failed: %s (%v)", out, err)
			}
			again, againHash, _, err := registry.ValidateAndHash(JobKind, 1, canonical)
			if err != nil || !bytes.Equal(canonical, again) || hash != againHash {
				t.Fatalf("canonical/hash changed on replay: %s/%x -> %s/%x (%v)", canonical, hash, again, againHash, err)
			}
			if name == "duplicate display names" && bytes.Count(out, []byte("Relay")) != 2 {
				t.Fatalf("duplicate names not rendered twice: %s", out)
			}
			if name == "mismatched lexical order" && !bytes.Contains(out, []byte("Node: Zulu, Alpha")) {
				t.Fatalf("positional names reordered: %s", out)
			}
		})
	}
	raw := bytes.Replace(validPayloadJSON(), []byte(`"node_names":["node-free-001","node-free-002"]`), []byte(`"node_names":["Zulu","Alpha"]`), 1)
	canonical, hash, _, err := registry.ValidateAndHash(JobKind, 1, raw)
	if err != nil || hash == [32]byte{} {
		t.Fatalf("parallel payload registry validation: %v", err)
	}
	out, err := renderPayload(canonical)
	if err != nil || !bytes.Contains(out, []byte("Node: Zulu, Alpha")) {
		t.Fatalf("pairing was reordered: %s (%v)", out, err)
	}
	bad := bytes.Replace(validPayloadJSON(), []byte(`"node_names":["node-free-001","node-free-002"]`), []byte(`"node_names":["A"]`), 1)
	if _, _, _, err := registry.ValidateAndHash(JobKind, 1, bad); !errors.Is(err, jobs.ErrInvalidPayload) {
		t.Fatalf("mismatched arrays accepted by registry: %v", err)
	}
}
