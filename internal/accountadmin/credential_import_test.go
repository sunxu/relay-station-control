package accountadmin

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/sunxu/relay-station-control/internal/store"
)

func TestImportedCredentialIntentIncludesProxyAndDisabled(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "intent.key")
	if err := os.WriteFile(keyPath, bytes.Repeat([]byte("k"), 32), 0600); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(&fakeOperationStore{}, &countingNodeResolver{})
	if err != nil {
		t.Fatal(err)
	}
	cmd := Command{CommandID: uuid.New(), ActorAdminID: uuid.New(), NodeInstanceID: uuid.New(), AccountKey: "antigravity:user@example.invalid", Kind: store.AccountUploadNew, IntentKeyPath: keyPath}
	credential := func(proxy string, disabled bool) []byte {
		raw, err := json.Marshal(map[string]any{"type": "antigravity", "email": "user@example.invalid", "refresh_token": "synthetic-refresh", "proxy_url": proxy, "disabled": disabled})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	cmd.Credential = credential("socks5h://user:synthetic-password@proxy.invalid:1080", false)
	original, err := service.CanonicalIntentForCommand(cmd)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := service.CanonicalIntentForCommand(cmd)
	if err != nil || !bytes.Equal(original, replay) {
		t.Fatal("identical credential did not preserve intent")
	}
	for _, changed := range [][]byte{credential("socks5h://user:synthetic-password@other.invalid:1080", false), credential("socks5h://user:synthetic-password@proxy.invalid:1080", true)} {
		cmd.Credential = changed
		intent, err := service.CanonicalIntentForCommand(cmd)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(original, intent) {
			t.Fatal("proxy or disabled change omitted from intent")
		}
	}
	if bytes.Contains(original, []byte("synthetic-password")) || bytes.Contains(original, []byte("synthetic-refresh")) {
		t.Fatal("credential reflected in intent")
	}
}

func TestImportedCredentialBoundary(t *testing.T) {
	for _, test := range []struct {
		name  string
		extra map[string]any
		kind  store.AccountOperationKind
		valid bool
	}{
		{"new enabled proxy", map[string]any{"proxy_url": "socks5h://user:synthetic-password@proxy.example.invalid:1080", "disabled": false}, store.AccountUploadNew, true},
		{"new disabled", map[string]any{"disabled": true}, store.AccountUploadNew, true},
		{"legacy missing disabled", nil, store.AccountUploadNew, true},
		{"replace missing disabled", nil, store.AccountReplaceExisting, true},
		{"replace cannot enable", map[string]any{"disabled": false}, store.AccountReplaceExisting, false},
		{"replace cannot disable", map[string]any{"disabled": true}, store.AccountReplaceExisting, false},
		{"null disabled", map[string]any{"disabled": nil}, store.AccountUploadNew, false},
		{"string disabled", map[string]any{"disabled": "false"}, store.AccountUploadNew, false},
		{"numeric disabled", map[string]any{"disabled": 0}, store.AccountUploadNew, false},
		{"legacy proxy alias", map[string]any{"proxy-url": "http://proxy.example.invalid:8080"}, store.AccountUploadNew, true},
		{"two proxy aliases", map[string]any{"proxy_url": "http://proxy.example.invalid:8080", "proxy-url": "http://proxy.example.invalid:8080"}, store.AccountUploadNew, false},
		{"priority still denied", map[string]any{"priority": 1}, store.AccountUploadNew, false},
		{"retry alias still denied", map[string]any{"request-retry": 1}, store.AccountUploadNew, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fields := map[string]any{"type": "antigravity", "email": "user@example.invalid", "refresh_token": "synthetic-refresh"}
			for k, v := range test.extra {
				fields[k] = v
			}
			raw, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			cmd := Command{CommandID: uuid.New(), ActorAdminID: uuid.New(), NodeInstanceID: uuid.New(), AccountKey: "antigravity:user@example.invalid", Kind: test.kind, Credential: raw}
			before := string(raw)
			if got := validateCommandRequest(cmd) == nil; got != test.valid {
				t.Fatalf("valid=%v want=%v", got, test.valid)
			}
			if string(cmd.Credential) != before {
				t.Fatal("credential bytes rewritten")
			}
		})
	}
}

func TestImportedProxyValidation(t *testing.T) {
	valid := []string{"http://proxy.example.invalid", "https://proxy.example.invalid:443/", "socks5://[::1]:1080", "socks5h://user:p%40ss@proxy.example.invalid:1080"}
	invalid := []any{nil, 1, map[string]any{}, "", "ftp://proxy.example.invalid", "http://", "http://proxy.example.invalid:0", "http://proxy.example.invalid:65536", "http://proxy.example.invalid:", "http://proxy.example.invalid/path", "http://proxy.example.invalid?", "http://proxy.example.invalid#", "http://proxy.example.invalid\n", "http://user:p%0Ass@proxy.example.invalid:8080"}
	for _, value := range append(func() []any {
		a := []any{}
		for _, v := range valid {
			a = append(a, v)
		}
		return a
	}(), invalid...) {
		raw, _ := json.Marshal(map[string]any{"type": "antigravity", "email": "user@example.invalid", "proxy_url": value})
		want := false
		for _, v := range valid {
			if value == v {
				want = true
			}
		}
		// Avoid comparing composite malformed values to strings.
		if got := validateCredential(raw, "user@example.invalid") == nil; got != want {
			t.Errorf("proxy variant valid=%v want=%v", got, want)
		}
	}
}
