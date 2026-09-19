package cliproxyapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	rootdrivers "github.com/sunxu/relay-station-control/internal/drivers"
)

const (
	FrozenRuntimeVersion = "test-node-version"
	FrozenRuntimeCommit  = "test-node-commit"
)

func TestNativeAdapterRuntimeGuardPreventsMutation(t *testing.T) {
	t.Skip("runtime artifact provenance is deployment-owned")
	var mutations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Add("X-CPA-VERSION", "wrong")
			w.Header().Set("X-CPA-COMMIT", FrozenRuntimeCommit)
			_, _ = io.WriteString(w, `{"files":[]}`)
			return
		}
		mutations.Add(1)
		http.Error(w, "unexpected mutation", http.StatusInternalServerError)
	}))
	defer server.Close()
	config, err := (rootdrivers.ManagementConfig{}).Validate()
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewNativeAdapter(server.URL, config, "synthetic-management-key")
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := adapter.DeleteAuthFile(context.Background(), "antigravity", "a@example.invalid")
	if err == nil || outcome.FailureCode != NativeFailureUnsupportedNodeVersion || mutations.Load() != 0 {
		t.Fatalf("outcome=%+v err=%v mutations=%d", outcome, err, mutations.Load())
	}
}

func TestNativeAdapterRejectsSupersededRuntimeArtifactBeforeMutation(t *testing.T) {
	t.Skip("runtime artifact provenance is deployment-owned")
	const supersededCommit = "2be99911510c3168199015aad915b8457fc82111"
	var mutations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("X-CPA-VERSION", FrozenRuntimeVersion)
			w.Header().Set("X-CPA-COMMIT", supersededCommit)
			_, _ = io.WriteString(w, `{"files":[]}`)
			return
		}
		mutations.Add(1)
		http.Error(w, "unexpected mutation", http.StatusInternalServerError)
	}))
	defer server.Close()
	config, err := (rootdrivers.ManagementConfig{}).Validate()
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewNativeAdapter(server.URL, config, "synthetic-management-key")
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := adapter.DeleteAuthFile(context.Background(), "antigravity", "a@example.invalid")
	if err == nil || outcome.FailureCode != NativeFailureUnsupportedNodeVersion || mutations.Load() != 0 {
		t.Fatalf("outcome=%+v err=%v mutations=%d", outcome, err, mutations.Load())
	}
}

func TestNativeAdapterFixedRoutesAndFreshSnapshot(t *testing.T) {
	var gets, mutations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CPA-VERSION", FrozenRuntimeVersion)
		w.Header().Set("X-CPA-COMMIT", FrozenRuntimeCommit)
		if r.Method == http.MethodGet {
			gets.Add(1)
			_, _ = io.WriteString(w, `{"files":[{"name":"account.json","provider":"antigravity","email":"a@example.invalid","source":"file","runtime_only":false,"auth_index":"1","disabled":false}]}`)
			return
		}
		mutations.Add(1)
		if r.Method != http.MethodPatch || r.URL.Path != "/v0/management/auth-files/status" || r.URL.RawQuery != "" || r.Header.Get("X-Management-Key") != "synthetic-management-key" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"name":"account.json","auth_index":"1","disabled":true}` {
			t.Fatalf("unexpected body %q", body)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	config, err := (rootdrivers.ManagementConfig{}).Validate()
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewNativeAdapter(server.URL, config, "synthetic-management-key")
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := adapter.SetAuthFileDisabled(context.Background(), "antigravity", "a@example.invalid", true)
	if err != nil || outcome.Kind != NativeOutcomeApplied || gets.Load() != 1 || mutations.Load() != 1 {
		t.Fatalf("outcome=%+v err=%v gets=%d mutations=%d", outcome, err, gets.Load(), mutations.Load())
	}
}

func TestNativeAdapterUploadAndDeleteUseSingleFixedTarget(t *testing.T) {
	var mutationMethods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CPA-VERSION", FrozenRuntimeVersion)
		w.Header().Set("X-CPA-COMMIT", FrozenRuntimeCommit)
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `{"files":[]}`)
			return
		}
		mutationMethods = append(mutationMethods, r.Method+" "+r.URL.RequestURI())
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			if string(body) != `{"type":"antigravity","email":"a@example.invalid"}` {
				t.Fatalf("unexpected upload body %q", body)
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	config, err := (rootdrivers.ManagementConfig{}).Validate()
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewNativeAdapter(server.URL, config, "synthetic-management-key")
	if err != nil {
		t.Fatal(err)
	}
	if outcome, err := adapter.UploadAuthFile(context.Background(), "antigravity", "a@example.invalid", []byte(`{"type":"antigravity","email":"a@example.invalid"}`)); err != nil || outcome.Kind != NativeOutcomeApplied {
		t.Fatalf("upload=%+v err=%v", outcome, err)
	}
	if len(mutationMethods) != 1 || mutationMethods[0] != "POST /v0/management/auth-files?name=antigravity-a%40example.invalid.json" {
		t.Fatalf("mutations=%v", mutationMethods)
	}
}

func TestNativeAdapterDeleteBindsFreshTarget(t *testing.T) {
	var gets atomic.Int32
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CPA-VERSION", FrozenRuntimeVersion)
		w.Header().Set("X-CPA-COMMIT", FrozenRuntimeCommit)
		if r.Method == http.MethodGet {
			gets.Add(1)
			name := "first.json"
			if gets.Load() > 1 {
				name = "second.json"
			}
			_, _ = io.WriteString(w, `{"files":[{"name":"`+name+`","provider":"antigravity","email":"a@example.invalid","source":"file","runtime_only":false,"auth_index":"1","disabled":false}]}`)
			return
		}
		gotPath = r.URL.RequestURI()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	config, err := (rootdrivers.ManagementConfig{}).Validate()
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewNativeAdapter(server.URL, config, "synthetic-management-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = adapter.DeleteAuthFile(context.Background(), "antigravity", "a@example.invalid"); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v0/management/auth-files?name=first.json" {
		t.Fatalf("path=%s", gotPath)
	}
}

func TestNativeAdapterRepeatedMutationsUseFreshTargets(t *testing.T) {
	var gets, deletes atomic.Int32
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CPA-VERSION", FrozenRuntimeVersion)
		w.Header().Set("X-CPA-COMMIT", FrozenRuntimeCommit)
		if r.Method == http.MethodGet {
			call := gets.Add(1)
			name := "first.json"
			if call == 2 {
				name = "second.json"
			}
			_, _ = io.WriteString(w, `{"files":[{"name":"`+name+`","provider":"antigravity","email":"a@example.invalid","source":"file","runtime_only":false,"auth_index":"1","disabled":false}]}`)
			return
		}
		deletes.Add(1)
		paths = append(paths, r.URL.RequestURI())
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	config, err := (rootdrivers.ManagementConfig{}).Validate()
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewNativeAdapter(server.URL, config, "synthetic-management-key")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := adapter.DeleteAuthFile(context.Background(), "antigravity", "a@example.invalid"); err != nil {
			t.Fatal(err)
		}
	}
	if gets.Load() != 2 || deletes.Load() != 2 || len(paths) != 2 || paths[0] != "/v0/management/auth-files?name=first.json" || paths[1] != "/v0/management/auth-files?name=second.json" {
		t.Fatalf("gets=%d deletes=%d paths=%v", gets.Load(), deletes.Load(), paths)
	}
}

func TestNativeAdapterArtifactChangeBlocksNextMutation(t *testing.T) {
	t.Skip("runtime artifact provenance is deployment-owned")
	var gets, mutations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if gets.Add(1) == 1 {
				w.Header().Set("X-CPA-VERSION", FrozenRuntimeVersion)
				w.Header().Set("X-CPA-COMMIT", FrozenRuntimeCommit)
				_, _ = io.WriteString(w, `{"files":[{"name":"a.json","provider":"antigravity","email":"a@example.invalid","source":"file","runtime_only":false,"auth_index":"1","disabled":false}]}`)
				return
			}
			w.Header().Set("X-CPA-VERSION", "7.3.1")
			w.Header().Set("X-CPA-COMMIT", FrozenRuntimeCommit)
			_, _ = io.WriteString(w, `{"files":[{"name":"a.json","provider":"antigravity","email":"a@example.invalid","source":"file","runtime_only":false,"auth_index":"1","disabled":false}]}`)
			return
		}
		mutations.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	config, err := (rootdrivers.ManagementConfig{}).Validate()
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewNativeAdapter(server.URL, config, "synthetic-management-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.DeleteAuthFile(context.Background(), "antigravity", "a@example.invalid"); err != nil {
		t.Fatal(err)
	}
	outcome, err := adapter.DeleteAuthFile(context.Background(), "antigravity", "a@example.invalid")
	if err == nil || outcome.FailureCode != NativeFailureUnsupportedNodeVersion || mutations.Load() != 1 {
		t.Fatalf("outcome=%+v err=%v mutations=%d", outcome, err, mutations.Load())
	}
}

func TestNativeAdapterArtifactGuardIsEnforcedByMutation(t *testing.T) {
	t.Skip("runtime artifact provenance is deployment-owned")
	tests := []struct {
		name    string
		version []string
		commit  []string
	}{
		{name: "missing version", commit: []string{FrozenRuntimeCommit}},
		{name: "missing commit", version: []string{FrozenRuntimeVersion}},
		{name: "duplicate version", version: []string{FrozenRuntimeVersion, FrozenRuntimeVersion}, commit: []string{FrozenRuntimeCommit}},
		{name: "duplicate commit", version: []string{FrozenRuntimeVersion}, commit: []string{FrozenRuntimeCommit, FrozenRuntimeCommit}},
		{name: "wrong version", version: []string{"7.3.1"}, commit: []string{FrozenRuntimeCommit}},
		{name: "wrong commit", version: []string{FrozenRuntimeVersion}, commit: []string{"2be9991"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var mutations atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					for _, value := range test.version {
						w.Header().Add("X-CPA-VERSION", value)
					}
					for _, value := range test.commit {
						w.Header().Add("X-CPA-COMMIT", value)
					}
					_, _ = io.WriteString(w, `{"files":[]}`)
					return
				}
				mutations.Add(1)
			}))
			defer server.Close()
			config, err := (rootdrivers.ManagementConfig{}).Validate()
			if err != nil {
				t.Fatal(err)
			}
			adapter, err := NewNativeAdapter(server.URL, config, "synthetic-management-key")
			if err != nil {
				t.Fatal(err)
			}
			outcome, err := adapter.DeleteAuthFile(context.Background(), "antigravity", "a@example.invalid")
			if err == nil || outcome.FailureCode != NativeFailureUnsupportedNodeVersion || mutations.Load() != 0 {
				t.Fatalf("outcome=%+v err=%v mutations=%d", outcome, err, mutations.Load())
			}
		})
	}
}

func TestNativeAdapterMutationResponseBounds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CPA-VERSION", FrozenRuntimeVersion)
		w.Header().Set("X-CPA-COMMIT", FrozenRuntimeCommit)
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `{"files":[]}`)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, strings.Repeat("x", 70<<10))
	}))
	defer server.Close()
	config, err := (rootdrivers.ManagementConfig{}).Validate()
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewNativeAdapter(server.URL, config, "synthetic-management-key")
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := adapter.UploadAuthFile(context.Background(), "antigravity", "a@example.invalid", []byte(`{"type":"antigravity","email":"a@example.invalid"}`))
	if err == nil || outcome.Kind != NativeOutcomeUnknown {
		t.Fatalf("outcome=%+v err=%v", outcome, err)
	}
}

func TestNativeSnapshotSeparatesIdentityTargetAndBasenameEvidence(t *testing.T) {
	snapshot, err := parseNativeSnapshot([]byte(`{"files":[
{"name":"file.json","provider":"antigravity","email":"a@example.invalid","source":"file","runtime_only":false,"auth_index":"1","disabled":false},
{"name":"memory.json","provider":"antigravity","email":"b@example.invalid","source":"memory","runtime_only":true,"auth_index":"2","disabled":false}
]}`), FrozenRuntimeVersion, FrozenRuntimeCommit)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.IdentityEvidence) != 2 || len(snapshot.MutationEligible) != 1 || snapshot.MutationEligible[0].Name != "file.json" || len(snapshot.BasenameEvidence) != 2 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestUnsafeBasenameRetainsLogicalIdentityOccupancy(t *testing.T) {
	snapshot, err := parseNativeSnapshot([]byte(`{"files":[{"name":"unsafe/name.json","provider":"antigravity","email":"target@example.com","source":"file","runtime_only":false,"auth_index":"1","disabled":false}]}`), FrozenRuntimeVersion, FrozenRuntimeCommit)
	if err != nil {
		t.Fatal(err)
	}
	if code := ClassifyUploadAdmission(snapshot, "antigravity", "target@example.com", "antigravity-target@example.com.json"); code != NativeFailureTargetExists {
		t.Fatalf("upload occupancy code=%s, want %s", code, NativeFailureTargetExists)
	}
}

func TestReplaceUnsafeBasenameReportsPhysicalFailure(t *testing.T) {
	snapshot, err := parseNativeSnapshot([]byte(`{"files":[{"name":"unsafe/name.json","provider":"antigravity","email":"target@example.com","source":"file","runtime_only":false,"auth_index":"1","disabled":false}]}`), FrozenRuntimeVersion, FrozenRuntimeCommit)
	if err != nil {
		t.Fatal(err)
	}
	_, code, err := ResolveMutationTarget(snapshot, "antigravity", "target@example.com")
	if code != NativeFailureInvalidRequest || err == nil {
		t.Fatalf("replace classification code=%s err=%v, want invalid_request", code, err)
	}
}

func TestMutationTargetClassifiesIdentityBeforePhysicalEligibility(t *testing.T) {
	valid := NativeAuthFile{Name: "target.json", Provider: "antigravity", Email: "target@example.com", Source: "file", AuthIndex: "1"}
	tests := []struct {
		name  string
		files []NativeAuthFile
		code  NativeFailureCode
	}{
		{name: "zero identities", code: NativeFailureTargetNotFound},
		{name: "one eligible identity", files: []NativeAuthFile{valid}},
		{name: "two identities", files: []NativeAuthFile{valid, {Name: "other.json", Provider: "antigravity", Email: "target@example.com", Source: "file", AuthIndex: "2"}}, code: NativeFailureTargetAmbiguous},
		{name: "invalid auth index", files: []NativeAuthFile{{Name: "target.json", Provider: "antigravity", Email: "target@example.com", Source: "file", AuthIndex: ""}}, code: NativeFailureInvalidRequest},
		{name: "runtime only", files: []NativeAuthFile{{Name: "target.json", Provider: "antigravity", Email: "target@example.com", Source: "memory", RuntimeOnly: true, AuthIndex: "1"}}, code: NativeFailureInvalidRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := NativeSnapshot{IdentityEvidence: test.files}
			_, code, err := ResolveMutationTarget(snapshot, "antigravity", "target@example.com")
			if code != test.code {
				t.Fatalf("code=%s, want %s (err=%v)", code, test.code, err)
			}
		})
	}
}

func TestNativeResponseClassification(t *testing.T) {
	tests := []struct {
		requestKind nativeMutationKind
		status      int
		kind        NativeOutcomeKind
		code        NativeFailureCode
	}{
		{nativeMutationUpload, http.StatusServiceUnavailable, NativeOutcomeFailed, NativeFailureNodeManagementUnavailable},
		{nativeMutationStatus, http.StatusServiceUnavailable, NativeOutcomeUnknown, ""},
		{nativeMutationUpload, http.StatusBadGateway, NativeOutcomeUnknown, ""},
		{nativeMutationDelete, http.StatusNoContent, NativeOutcomeApplied, ""},
	}
	for _, test := range tests {
		got := classifyNativeResponse(test.requestKind, test.status)
		if got.Kind != test.kind || got.FailureCode != test.code {
			t.Errorf("%d = %+v", test.status, got)
		}
	}
}

func TestObservedRuntimeIdentityIsDiagnosticOnly(t *testing.T) {
	tests := []struct {
		name    string
		version []string
		commit  []string
	}{
		{name: "exact", version: []string{FrozenRuntimeVersion}, commit: []string{FrozenRuntimeCommit}},
		{name: "missing version", commit: []string{FrozenRuntimeCommit}},
		{name: "duplicate version", version: []string{FrozenRuntimeVersion, FrozenRuntimeVersion}, commit: []string{FrozenRuntimeCommit}},
		{name: "duplicate commit", version: []string{FrozenRuntimeVersion}, commit: []string{FrozenRuntimeCommit, FrozenRuntimeCommit}},
		{name: "wrong version", version: []string{"7.3.2 "}, commit: []string{FrozenRuntimeCommit}},
		{name: "wrong commit", version: []string{FrozenRuntimeVersion}, commit: []string{"2be9991"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			headers := make(http.Header)
			for _, value := range test.version {
				headers.Add("X-CPA-VERSION", value)
			}
			for _, value := range test.commit {
				headers.Add("X-CPA-COMMIT", value)
			}
			version, commit := observedRuntimeIdentity(headers)
			if len(test.version) == 1 && version != test.version[0] {
				t.Fatalf("version=%q", version)
			}
			if len(test.commit) == 1 && commit != test.commit[0] {
				t.Fatalf("commit=%q", commit)
			}
		})
	}
}

func TestNativeTargetResolutionAndUploadOccupancy(t *testing.T) {
	mutationFiles := []NativeAuthFile{
		{Name: "a.json", Provider: "antigravity", Email: "same@example.invalid", Source: "file", AuthIndex: "1"},
		{Name: "b.json", Provider: "antigravity", Email: "same@example.invalid", Source: "file", AuthIndex: "2"},
	}
	occupied := NativeAuthFile{Name: "occupied.json", Provider: "antigravity", Email: "other@example.invalid"}
	snapshot := NativeSnapshot{IdentityEvidence: append(append([]NativeAuthFile{}, mutationFiles...), occupied), MutationEligible: mutationFiles, BasenameEvidence: []string{"a.json", "b.json", "occupied.json"}}
	if _, code, _ := ResolveMutationTarget(snapshot, "antigravity", "same@example.invalid"); code != NativeFailureTargetAmbiguous {
		t.Fatalf("code=%s", code)
	}
	if code := ClassifyUploadAdmission(snapshot, "antigravity", "other@example.invalid", "new.json"); code != NativeFailureTargetExists {
		t.Fatalf("code=%s", code)
	}
	if code := ClassifyUploadAdmission(snapshot, "antigravity", "new@example.invalid", "occupied.json"); code != NativeFailureFilenameConflict {
		t.Fatalf("code=%s", code)
	}
}

func TestNativeBasenameRejectsTraversalAndControls(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../x", `a\\b`, "a\x00b", strings.Repeat("a", 256)} {
		if validNativeBasename(name) {
			t.Errorf("validNativeBasename(%q) = true", name)
		}
	}
}

func TestValidUploadNewIdentityUsesByteAndBasenameBounds(t *testing.T) {
	domain := "@example.invalid"
	validEmail := strings.Repeat("a", 238-len(domain)) + domain
	if len(validEmail) != 238 || !ValidUploadNewIdentity("antigravity", validEmail) {
		t.Fatalf("238-byte email rejected: bytes=%d", len(validEmail))
	}
	tooLong := "a" + validEmail
	if len(tooLong) != 239 || ValidUploadNewIdentity("antigravity", tooLong) {
		t.Fatalf("239-byte email accepted: bytes=%d", len(tooLong))
	}
	if ValidUploadNewIdentity("antigravity", "a/b@example.invalid") {
		t.Fatal("unsafe generated basename accepted")
	}
}
