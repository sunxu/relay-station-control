package cliproxyapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v2"
)

func testPolicy(t *testing.T, active, out []string) ProviderPolicy {
	t.Helper()
	policy, err := newProviderPolicy(ProviderPolicyVersionV1, active, out)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("testdata", "auth-files-v1", name)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func parseFixture(t *testing.T, name string, policy ProviderPolicy) InventoryObservation {
	t.Helper()
	return parseInventory(200, nil, bytes.NewReader(fixture(t, name)), InventoryParseOptions{
		Policy: policy,
		Now:    time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC),
	})
}

func TestPhaseZeroFixtureContract(t *testing.T) {
	t.Parallel()
	policy := testPolicy(t, []string{"antigravity"}, nil)
	tests := []struct {
		name           string
		contract       bool
		shape          bool
		mode           InventoryMode
		degraded       bool
		unidentifiable int
	}{
		{name: "runtime-valid.json", contract: true, shape: true, mode: InventoryModeRuntime},
		{name: "disk-fallback.json", contract: true, shape: true, mode: InventoryModeDiskFallback, degraded: true},
		{name: "empty.json", contract: true, shape: true, mode: InventoryModeDiskFallback, degraded: true},
		{name: "invalid-json.txt"},
		{name: "invalid-files.json"},
		{name: "missing-files.json"},
		{name: "identity-errors.json", contract: true, shape: true, mode: InventoryModeRuntime, unidentifiable: 3},
		{name: "unknown-fields.json", contract: true, shape: true, mode: InventoryModeRuntime},
		{name: "unknown-source.json", shape: true},
		{name: "node-duplicate.json", contract: true, shape: true, mode: InventoryModeRuntime},
		{name: "cross-node-a.json", contract: true, shape: true, mode: InventoryModeRuntime},
		{name: "cross-node-b.json", contract: true, shape: true, mode: InventoryModeRuntime},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := parseFixture(t, test.name, policy)
			if got.ContractValid != test.contract || got.ResponseShapeValid != test.shape || got.Mode != test.mode || got.Degraded != test.degraded || got.UnidentifiableCount != test.unidentifiable {
				t.Fatalf("parseInventory(%s) = %#v", test.name, got)
			}
		})
	}
}

func TestCasesManifestHasDataDrivenCoverage(t *testing.T) {
	t.Parallel()
	type expectedResult struct {
		TransportSuccess         *bool  `yaml:"transport_success"`
		ContractValid            *bool  `yaml:"contract_valid"`
		InventoryMode            string `yaml:"inventory_mode"`
		Degraded                 *bool  `yaml:"degraded"`
		NodeIdentityComplete     *bool  `yaml:"node_identity_complete"`
		UnidentifiableCount      *int   `yaml:"unidentifiable_count"`
		UnsupportedProviderCount *int   `yaml:"unsupported_provider_count"`
		OutOfScopeProviderCount  *int   `yaml:"out_of_scope_provider_count"`
		ProviderSnapshotComplete *bool  `yaml:"provider_snapshot_complete"`
		DuplicateGroupCount      *int   `yaml:"duplicate_group_count"`
		UnknownFieldsPersisted   *bool  `yaml:"unknown_fields_persisted"`
	}
	type manifestCase struct {
		ID               string            `yaml:"id"`
		Status           int               `yaml:"status"`
		Headers          map[string]string `yaml:"headers"`
		Body             string            `yaml:"body"`
		GeneratedFixture string            `yaml:"generated_fixture"`
		PolicyOverride   struct {
			OutOfScope []string `yaml:"out_of_scope"`
		} `yaml:"policy_override"`
		Expected expectedResult `yaml:"expected"`
	}
	var manifest struct {
		SchemaVersion int            `yaml:"schema_version"`
		Cases         []manifestCase `yaml:"cases"`
	}
	if err := yaml.Unmarshal(fixture(t, "cases.yaml"), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != 1 {
		t.Fatalf("manifest schema = %d", manifest.SchemaVersion)
	}
	want := []string{
		"runtime_valid", "disk_fallback", "empty_files", "invalid_json", "invalid_files",
		"missing_files", "http_non_200", "identity_errors", "provider_scope", "node_duplicate",
		"cross_node_a", "cross_node_b", "unknown_fields", "unknown_source_shape",
		"response_body_over_limit", "record_count_over_limit",
	}
	if len(manifest.Cases) != len(want) {
		t.Fatalf("manifest case count = %d, want %d", len(manifest.Cases), len(want))
	}
	for index, test := range manifest.Cases {
		if test.ID != want[index] {
			t.Fatalf("manifest case %d = %q, want %q", index, test.ID, want[index])
		}
		status := test.Status
		if status == 0 {
			status = http.StatusOK
		}
		var body []byte
		switch test.GeneratedFixture {
		case "":
			body = fixture(t, test.Body)
		case "repeat runtime-valid record until encoded body exceeds 5 MiB":
			body = append([]byte(`{"files":[],"discarded":"`), bytes.Repeat([]byte("x"), int(DefaultInventoryBodyLimit))...)
			body = append(body, []byte(`"}`)...)
		case "create 1001 synthetic runtime records with unique example.invalid emails":
			records := make([]string, 1001)
			for record := range records {
				records[record] = fmt.Sprintf(`{"provider":"antigravity","email":"manifest-%04d@example.invalid","source":"file","status":"active"}`, record)
			}
			body = []byte(`{"files":[` + strings.Join(records, ",") + `]}`)
		default:
			t.Fatalf("unknown generated fixture for %s", test.ID)
		}
		headers := make(http.Header)
		for name, value := range test.Headers {
			headers.Set(name, value)
		}
		policy := testPolicy(t, []string{"antigravity"}, test.PolicyOverride.OutOfScope)
		got := parseInventory(status, headers, bytes.NewReader(body), InventoryParseOptions{Policy: policy})
		if test.Expected.TransportSuccess != nil && got.TransportSuccess != *test.Expected.TransportSuccess {
			t.Fatalf("%s transport = %t", test.ID, got.TransportSuccess)
		}
		if test.Expected.ContractValid != nil && got.ContractValid != *test.Expected.ContractValid {
			t.Fatalf("%s contract = %t", test.ID, got.ContractValid)
		}
		if test.Expected.InventoryMode != "" && string(got.Mode) != test.Expected.InventoryMode {
			t.Fatalf("%s mode = %s", test.ID, got.Mode)
		}
		if test.Expected.Degraded != nil && got.Degraded != *test.Expected.Degraded {
			t.Fatalf("%s degraded = %t", test.ID, got.Degraded)
		}
		if test.Expected.NodeIdentityComplete != nil && got.NodeIdentityComplete != *test.Expected.NodeIdentityComplete {
			t.Fatalf("%s identity complete = %t", test.ID, got.NodeIdentityComplete)
		}
		if test.Expected.UnidentifiableCount != nil && got.UnidentifiableCount != *test.Expected.UnidentifiableCount {
			t.Fatalf("%s unidentifiable = %d", test.ID, got.UnidentifiableCount)
		}
		if test.Expected.UnsupportedProviderCount != nil && got.UnsupportedProviderCount != *test.Expected.UnsupportedProviderCount {
			t.Fatalf("%s unsupported = %d", test.ID, got.UnsupportedProviderCount)
		}
		if test.Expected.OutOfScopeProviderCount != nil && got.OutOfScopeProviderCount != *test.Expected.OutOfScopeProviderCount {
			t.Fatalf("%s out-of-scope = %d", test.ID, got.OutOfScopeProviderCount)
		}
		if test.Expected.DuplicateGroupCount != nil && got.DuplicateGroupCount != *test.Expected.DuplicateGroupCount {
			t.Fatalf("%s duplicate groups = %d", test.ID, got.DuplicateGroupCount)
		}
		if test.Expected.ProviderSnapshotComplete != nil {
			if len(got.Providers) == 0 || got.Providers[0].ProviderSnapshotComplete != *test.Expected.ProviderSnapshotComplete {
				t.Fatalf("%s provider snapshot completeness mismatch", test.ID)
			}
		}
		if test.Expected.UnknownFieldsPersisted != nil && !*test.Expected.UnknownFieldsPersisted {
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range []string{"status_message", "synthetic message", "future_optional"} {
				if bytes.Contains(bytes.ToLower(encoded), []byte(forbidden)) {
					t.Fatalf("%s persisted unknown field %q", test.ID, forbidden)
				}
			}
		}
	}
}

func TestInventoryTransportShapeAndBounds(t *testing.T) {
	t.Parallel()
	policy := testPolicy(t, []string{"antigravity"}, nil)
	body := []byte(`{"files":[]}`)
	for _, test := range []struct {
		name      string
		status    int
		body      []byte
		limits    InventoryLimits
		transport bool
		shape     bool
		contract  bool
		reason    InventoryReason
	}{
		{name: "http non 200", status: 503, body: []byte("CANARY"), transport: false, reason: InventoryReasonHTTPStatus},
		{name: "exact byte limit", status: 200, body: body, limits: InventoryLimits{MaxBodyBytes: int64(len(body))}, transport: true, shape: true, contract: true, reason: InventoryReasonOK},
		{name: "one byte over", status: 200, body: body, limits: InventoryLimits{MaxBodyBytes: int64(len(body) - 1)}, transport: true, reason: InventoryReasonResponseTooLarge},
		{name: "top array", status: 200, body: []byte(`[]`), transport: true, reason: InventoryReasonResponseInvalid},
		{name: "files null", status: 200, body: []byte(`{"files":null}`), transport: true, reason: InventoryReasonResponseInvalid},
		{name: "trailing", status: 200, body: []byte(`{"files":[]} {}`), transport: true, reason: InventoryReasonResponseInvalid},
		{name: "duplicate top files", status: 200, body: []byte(`{"files":[],"files":[]}`), transport: true, reason: InventoryReasonResponseInvalid},
		{name: "duplicate record field", status: 200, body: []byte(`{"files":[{"provider":"antigravity","provider":"future","email":"a@example.invalid","source":"file"}]}`), transport: true, shape: true, reason: InventoryReasonContractInvalid},
		{name: "unsafe body configuration", status: 200, body: body, limits: InventoryLimits{MaxBodyBytes: DefaultInventoryBodyLimit + 1, MaxRecords: 1}, transport: true, reason: InventoryReasonContractInvalid},
		{name: "unsafe record configuration", status: 200, body: body, limits: InventoryLimits{MaxBodyBytes: 1024, MaxRecords: DefaultInventoryRecordLimit + 1}, transport: true, reason: InventoryReasonContractInvalid},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := parseInventory(test.status, nil, bytes.NewReader(test.body), InventoryParseOptions{Policy: policy, Limits: test.limits})
			if got.TransportSuccess != test.transport || got.ResponseShapeValid != test.shape || got.ContractValid != test.contract || got.Reason != test.reason {
				t.Fatalf("got %#v", got)
			}
		})
	}

	makeRecords := func(count int) []byte {
		records := make([]string, count)
		for i := range records {
			records[i] = fmt.Sprintf(`{"provider":"antigravity","email":"account-%04d@example.invalid","source":"file","status":"active"}`, i)
		}
		return []byte(`{"files":[` + strings.Join(records, ",") + `]}`)
	}
	if got := parseInventory(200, nil, bytes.NewReader(makeRecords(1000)), InventoryParseOptions{Policy: policy}); !got.ContractValid || len(got.Accounts) != 1000 {
		t.Fatalf("1000 records rejected: contract=%t accounts=%d reason=%s", got.ContractValid, len(got.Accounts), got.Reason)
	}
	if got := parseInventory(200, nil, bytes.NewReader(makeRecords(1001)), InventoryParseOptions{Policy: policy}); got.Reason != InventoryReasonRecordLimit || len(got.Accounts) != 0 {
		t.Fatalf("1001 records = %#v", got)
	}
	overFiveMiB := append([]byte(`{"files":[],"discarded":"`), bytes.Repeat([]byte("x"), int(DefaultInventoryBodyLimit))...)
	overFiveMiB = append(overFiveMiB, []byte(`"}`)...)
	if got := parseInventory(200, nil, bytes.NewReader(overFiveMiB), InventoryParseOptions{Policy: policy}); got.Reason != InventoryReasonResponseTooLarge || got.ResponseShapeValid || len(got.Accounts) != 0 {
		t.Fatalf("generated >5 MiB body = %s", got.Reason)
	}
}

func TestInventoryModeMatrix(t *testing.T) {
	t.Parallel()
	policy := testPolicy(t, []string{"antigravity"}, nil)
	record := func(source string, includeSource bool) string {
		field := ""
		if includeSource {
			field = `,"source":"` + source + `"`
		}
		return `{"provider":"antigravity","email":"a@example.invalid","status":"active","disabled":false` + field + `}`
	}
	for _, test := range []struct {
		name  string
		body  string
		mode  InventoryMode
		valid bool
	}{
		{name: "file runtime", body: `{"files":[` + record("file", true) + `]}`, mode: InventoryModeRuntime, valid: true},
		{name: "memory runtime", body: `{"files":[` + record("memory", true) + `]}`, mode: InventoryModeRuntime, valid: true},
		{name: "mixed runtime", body: `{"files":[` + record("file", true) + `,` + record("memory", true) + `]}`, mode: InventoryModeRuntime, valid: true},
		{name: "disk", body: `{"files":[` + record("", false) + `]}`, mode: InventoryModeDiskFallback, valid: true},
		{name: "mixed source presence", body: `{"files":[` + record("file", true) + `,` + record("", false) + `]}`},
		{name: "unknown source", body: `{"files":[` + record("future", true) + `]}`},
		{name: "unknown disk shape", body: `{"files":[{"provider":"antigravity","email":"a@example.invalid"}]}`},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := parseInventory(200, nil, strings.NewReader(test.body), InventoryParseOptions{Policy: policy})
			if got.ContractValid != test.valid || got.Mode != test.mode {
				t.Fatalf("got %#v", got)
			}
		})
	}
}

func TestProviderPolicyAndIdentityClassification(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		version string
		active  []string
		out     []string
	}{
		{name: "missing version", active: []string{"a"}},
		{name: "unknown version", version: "v2", active: []string{"a"}},
		{name: "empty active set", version: "v1"},
		{name: "invalid provider", version: "v1", active: []string{"bad provider"}},
		{name: "duplicate", version: "v1", active: []string{"a", " A "}},
		{name: "overlap", version: "v1", active: []string{"a"}, out: []string{"A"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := newProviderPolicy(test.version, test.active, test.out); err == nil {
				t.Fatal("expected policy rejection")
			}
		})
	}

	policy := testPolicy(t, []string{" Antigravity ", "zero"}, []string{"legacy-provider"})
	got := parseFixture(t, "provider-scope.json", policy)
	if !got.ContractValid || got.UnsupportedProviderCount != 1 || got.OutOfScopeProviderCount != 1 || !got.Degraded {
		t.Fatalf("scope = %#v", got)
	}
	if len(got.Providers) != 2 || !got.Providers[0].ProviderSnapshotComplete || !got.Providers[1].ProviderSnapshotComplete {
		t.Fatalf("zero-record active providers incomplete: %#v", got.Providers)
	}

	identity := parseFixture(t, "identity-errors.json", testPolicy(t, []string{"antigravity", "zero"}, nil))
	if identity.NodeIdentityComplete || identity.UnidentifiableCount != 3 {
		t.Fatalf("identity = %#v", identity)
	}
	for _, provider := range identity.Providers {
		if provider.ProviderSnapshotComplete {
			t.Fatalf("global provider gap left %s complete", provider.Provider)
		}
	}
	localBody := `{"files":[
		{"provider":"antigravity","email":"","source":"file","status":"active"},
		{"provider":"zero","email":"zero@example.invalid","source":"file","status":"active"}
	]}`
	local := parseInventory(200, nil, strings.NewReader(localBody), InventoryParseOptions{Policy: testPolicy(t, []string{"antigravity", "zero"}, nil)})
	if !local.NodeIdentityComplete || local.UnidentifiableCount != 1 || local.Providers[0].ProviderSnapshotComplete || !local.Providers[1].ProviderSnapshotComplete {
		t.Fatalf("provider-local identity failure = %#v", local)
	}

	duplicate := parseFixture(t, "node-duplicate.json", testPolicy(t, []string{"antigravity"}, nil))
	if duplicate.DuplicateGroupCount != 1 || duplicate.Providers[0].ProviderSnapshotComplete || len(duplicate.Accounts) != 2 {
		t.Fatalf("duplicate = %#v", duplicate)
	}
	for _, account := range duplicate.Accounts {
		if account.OccurrenceCount != 2 {
			t.Fatalf("occurrence count = %d", account.OccurrenceCount)
		}
	}
	for _, name := range []string{"cross-node-a.json", "cross-node-b.json"} {
		cross := parseFixture(t, name, testPolicy(t, []string{"antigravity"}, nil))
		if cross.DuplicateGroupCount != 0 || cross.Accounts[0].OccurrenceCount != 1 {
			t.Fatalf("parser performed cross-node duplicate detection for %s", name)
		}
	}
}

func TestProviderPolicyFailsBeforeReadingBody(t *testing.T) {
	t.Parallel()
	reader := &countingReader{}
	got := parseInventory(200, nil, reader, InventoryParseOptions{})
	if got.Reason != InventoryReasonPolicyInvalid || reader.reads != 0 {
		t.Fatalf("got %#v, reads=%d", got, reader.reads)
	}
}

type countingReader struct{ reads int }

func (r *countingReader) Read([]byte) (int, error) {
	r.reads++
	return 0, nil
}

func TestWhitelistProjectionAndStrictFieldTypes(t *testing.T) {
	t.Parallel()
	policy := testPolicy(t, []string{"antigravity"}, nil)
	got := parseFixture(t, "unknown-fields.json", policy)
	for _, formatted := range []string{fmt.Sprintf("%v", got), fmt.Sprintf("%+v", got), fmt.Sprintf("%#v", got)} {
		if formatted != "inventory_observation" {
			t.Fatalf("inventory observation formatting is not closed: %q", formatted)
		}
	}
	accountCanary := AccountObservation{Provider: "provider-canary", Email: "email-canary@example.invalid"}
	for _, formatted := range []string{fmt.Sprintf("%v", accountCanary), fmt.Sprintf("%+v", accountCanary), fmt.Sprintf("%#v", accountCanary)} {
		if formatted != "account_observation" {
			t.Fatalf("account observation formatting is not closed: %q", formatted)
		}
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"status_message", "synthetic message", "future_optional"} {
		if bytes.Contains(bytes.ToLower(encoded), []byte(forbidden)) {
			t.Fatalf("forbidden field %q projected: %s", forbidden, encoded)
		}
	}
	forbiddenBody := `{"files":[{"provider":"antigravity","email":"safe@example.invalid","source":"file","status":"active","path":"PATH-CANARY","project":"PROJECT-CANARY","auth":"AUTH-CANARY","token":"TOKEN-CANARY","account":"ACCOUNT-CANARY","name":"NAME-CANARY","nested":{"value":"NESTED-CANARY"}}]}`
	forbiddenProjection := parseInventory(200, nil, strings.NewReader(forbiddenBody), InventoryParseOptions{Policy: policy})
	forbiddenEncoded, err := json.Marshal(forbiddenProjection)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"PATH-CANARY", "PROJECT-CANARY", "AUTH-CANARY", "TOKEN-CANARY", "ACCOUNT-CANARY", "NAME-CANARY", "NESTED-CANARY"} {
		if bytes.Contains(forbiddenEncoded, []byte(forbidden)) {
			t.Fatalf("forbidden value %q projected: %s", forbidden, forbiddenEncoded)
		}
	}
	for _, body := range []string{
		`{"files":[{"provider":1,"email":"a@example.invalid","source":"file"}]}`,
		`{"files":[{"provider":null,"email":"a@example.invalid","source":"file"}]}`,
		`{"files":[{"provider":"antigravity","email":false,"source":"file"}]}`,
		`{"files":[{"provider":"antigravity","email":"a@example.invalid","source":"file","disabled":"false"}]}`,
		`{"files":[{"provider":"antigravity","email":"a@example.invalid","source":"file","disabled":null}]}`,
		`{"files":[{"provider":"antigravity","email":"a@example.invalid","source":"file","success":1.5}]}`,
		`{"files":[{"provider":"antigravity","email":"a@example.invalid","source":"file","recent_requests":null}]}`,
		`{"files":[{"provider":"antigravity","email":"a@example.invalid","source":"file","updated_at":"not-time"}]}`,
		`{"files":[{"provider":"` + strings.Repeat("a", 65) + `","email":"a@example.invalid","source":"file"}]}`,
		`{"files":[{"provider":"antigravity","email":"` + strings.Repeat("a", 310) + `@example.invalid","source":"file"}]}`,
		`{"files":[{"provider":"antigravity","email":"a\u0000@example.invalid","source":"file"}]}`,
		`{"files":[{"provider":"antigravity","email":"a@example.invalid","source":"fi\u0000le"}]}`,
	} {
		result := parseInventory(200, nil, strings.NewReader(body), InventoryParseOptions{Policy: policy})
		if result.ContractValid || !result.ResponseShapeValid || result.Reason != InventoryReasonContractInvalid {
			t.Fatalf("type confusion accepted: %s => %#v", body, result)
		}
	}
}

func TestBaseStatusPriorityCountsAndTimes(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)
	policy := testPolicy(t, []string{"antigravity"}, nil)
	body := `{"files":[
		{"provider":"antigravity","email":"disabled@example.invalid","source":"file","disabled":true,"unavailable":true,"status":"error"},
		{"provider":"antigravity","email":"unavailable@example.invalid","source":"file","unavailable":true,"status":"error"},
		{"provider":"antigravity","email":"retry@example.invalid","source":"file","status":"active","next_retry_after":"2026-08-26T00:00:01Z"},
		{"provider":"antigravity","email":"error@example.invalid","source":"file","status":"error"},
		{"provider":"antigravity","email":"active@example.invalid","source":"file","status":"active","success":9007199254740991,"failed":2,"updated_at":"2026-08-25T23:59:00+00:00"},
		{"provider":"antigravity","email":"unknown@example.invalid","source":"memory","status":"future"}
	]}`
	got := parseInventory(200, nil, strings.NewReader(body), InventoryParseOptions{Policy: policy, Now: now})
	if !got.ContractValid || len(got.Accounts) != 6 {
		t.Fatalf("got %#v", got)
	}
	want := []BaseStatus{BaseStatusDisabled, BaseStatusUnavailable, BaseStatusUnavailable, BaseStatusError, BaseStatusActive, BaseStatusUnknown}
	for i := range want {
		if got.Accounts[i].Status != want[i] {
			t.Fatalf("status %d = %s, want %s", i, got.Accounts[i].Status, want[i])
		}
	}
	if got.Accounts[4].Success != maxCounter || got.Accounts[4].Failed != 2 || got.Accounts[4].UpdatedAt == nil || got.Accounts[4].UpdatedAt.Location() != time.UTC {
		t.Fatalf("bounded fields = %#v", got.Accounts[4])
	}
}

func TestVersionHeaderAllowlist(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		version     string
		commit      string
		wantVersion string
		wantCommit  string
	}{
		{name: "valid", version: "7.2.141", commit: "DC3C3B1", wantVersion: "7.2.141", wantCommit: "dc3c3b1"},
		{name: "missing", wantVersion: "unknown", wantCommit: "unknown"},
		{name: "invalid characters", version: "7.2.141 secret", commit: "xyz", wantVersion: "unknown", wantCommit: "unknown"},
		{name: "too long", version: strings.Repeat("a", 65), commit: strings.Repeat("a", 65), wantVersion: "unknown", wantCommit: "unknown"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			headers := make(http.Header)
			headers.Set("X-CPA-VERSION", test.version)
			headers.Set("X-CPA-COMMIT", test.commit)
			version, commit := sanitizeVersionHeaders(headers)
			if version != test.wantVersion || commit != test.wantCommit {
				t.Fatalf("got (%q,%q), want (%q,%q)", version, commit, test.wantVersion, test.wantCommit)
			}
		})
	}
}

func TestVersionHeaderDuplicatesAreUnknown(t *testing.T) {
	t.Parallel()
	headers := http.Header{
		"X-Cpa-Version": []string{"7.2.141", "7.2.142"},
		"X-Cpa-Commit":  []string{"dc3c3b1", "ac3c3b1"},
	}
	version, commit := sanitizeVersionHeaders(headers)
	if version != UnknownVersionHeader || commit != UnknownVersionHeader {
		t.Fatalf("duplicate headers accepted: (%q, %q)", version, commit)
	}
}

func TestFixturesContainOnlySyntheticEmailDomain(t *testing.T) {
	t.Parallel()
	dir := filepath.Join("testdata", "auth-files-v1")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	emailPattern := regexp.MustCompile(`[A-Za-z0-9._%+-]+@([A-Za-z0-9.-]+)`)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range emailPattern.FindAllSubmatch(contents, -1) {
			if !strings.EqualFold(string(match[1]), "example.invalid") {
				t.Fatalf("fixture %s contains non-synthetic email domain", entry.Name())
			}
		}
	}
}

func TestPinnedFixturesMatchPhaseZeroSourceWhenAvailable(t *testing.T) {
	source := filepath.Join("..", "..", "..", "..", "ops", "contracts", "cliproxyapi", "auth-files", "v1")
	if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
		t.Skip("sibling ops contract is not checked out")
	} else if err != nil {
		t.Fatal(err)
	}
	local := filepath.Join("testdata", "auth-files-v1")
	entries, err := os.ReadDir(local)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "README.md" {
			continue
		}
		localBody, err := os.ReadFile(filepath.Join(local, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		sourceBody, err := os.ReadFile(filepath.Join(source, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if sha256.Sum256(localBody) != sha256.Sum256(sourceBody) {
			t.Fatalf("pinned fixture %s drifted from phase-0 source", entry.Name())
		}
	}
}
