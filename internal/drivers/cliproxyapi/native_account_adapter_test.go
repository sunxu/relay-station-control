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

func TestNativeAdapterRuntimeGuardPreventsMutation(t *testing.T) {
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
	outcome, err := adapter.DeleteAuthFile(context.Background(), "safe.json")
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
	outcome, err := adapter.SetAuthFileDisabled(context.Background(), "account.json", "1", true)
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
	if outcome, err := adapter.UploadAuthFile(context.Background(), "a.json", []byte(`{"type":"antigravity","email":"a@example.invalid"}`)); err != nil || outcome.Kind != NativeOutcomeApplied {
		t.Fatalf("upload=%+v err=%v", outcome, err)
	}
	if outcome, err := adapter.DeleteAuthFile(context.Background(), "a.json"); err != nil || outcome.Kind != NativeOutcomeApplied {
		t.Fatalf("delete=%+v err=%v", outcome, err)
	}
	if len(mutationMethods) != 2 || mutationMethods[0] != "POST /v0/management/auth-files?name=a.json" || mutationMethods[1] != "DELETE /v0/management/auth-files?name=a.json" {
		t.Fatalf("mutations=%v", mutationMethods)
	}
}

func TestNativeSnapshotSeparatesTargetAndOccupancyEvidence(t *testing.T) {
	snapshot, err := parseNativeSnapshot([]byte(`{"files":[
{"name":"file.json","provider":"antigravity","email":"a@example.invalid","source":"file","runtime_only":false,"auth_index":"1","disabled":false},
{"name":"memory.json","provider":"antigravity","email":"b@example.invalid","source":"memory","runtime_only":true,"auth_index":"2","disabled":false}
]}`), FrozenRuntimeVersion, FrozenRuntimeCommit)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.MutationEligible) != 1 || snapshot.MutationEligible[0].Name != "file.json" || len(snapshot.OccupancyEvidence) != 2 {
		t.Fatalf("snapshot=%+v", snapshot)
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

func TestExactRuntimeIdentityRejectsMissingDuplicateAndMismatch(t *testing.T) {
	tests := []struct {
		name    string
		version []string
		commit  []string
		wantOK  bool
	}{
		{name: "exact", version: []string{FrozenRuntimeVersion}, commit: []string{FrozenRuntimeCommit}, wantOK: true},
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
			_, _, ok := exactRuntimeIdentity(headers, FrozenRuntimeVersion, FrozenRuntimeCommit)
			if ok != test.wantOK {
				t.Fatalf("ok=%v, want %v", ok, test.wantOK)
			}
		})
	}
}

func TestNativeTargetResolutionAndUploadOccupancy(t *testing.T) {
	snapshot := NativeSnapshot{MutationEligible: []NativeAuthFile{
		{Name: "a.json", Provider: "antigravity", Email: "same@example.invalid", Source: "file", AuthIndex: "1"},
		{Name: "b.json", Provider: "antigravity", Email: "same@example.invalid", Source: "file", AuthIndex: "2"},
	}, OccupancyEvidence: []NativeAuthFile{{Name: "occupied.json", Provider: "antigravity", Email: "other@example.invalid"}}}
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
