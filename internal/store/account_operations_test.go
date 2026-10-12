package store

import (
	"crypto/hmac"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAccountOperationTransitionMatrix(t *testing.T) {
	states := []AccountOperationState{AccountPrepared, AccountDispatched, AccountRemoteApplied, AccountRemoteNoop, AccountOutcomeUnknown, AccountFailed}
	allowed := map[[2]AccountOperationState]bool{
		{AccountPrepared, AccountDispatched}: true, {AccountPrepared, AccountRemoteNoop}: true, {AccountPrepared, AccountFailed}: true,
		{AccountDispatched, AccountRemoteApplied}: true, {AccountDispatched, AccountFailed}: true, {AccountDispatched, AccountOutcomeUnknown}: true,
	}
	for _, from := range states {
		for _, to := range states {
			if got := allowedAccountTransition(from, to); got != allowed[[2]AccountOperationState{from, to}] {
				t.Errorf("transition %s -> %s = %v, want %v", from, to, got, allowed[[2]AccountOperationState{from, to}])
			}
		}
	}
}

func TestNewAccountFailurePhaseAwareMapping(t *testing.T) {
	tests := []struct {
		code, phase string
		status      int
	}{
		{"unsupported_provider", string(AccountPreAcceptance), 409},
		{"unsupported_provider", string(AccountPreDispatchPostAccept), 409},
		{"invalid_request", string(AccountPreAcceptance), 400},
		{"invalid_request", string(AccountPreDispatchPostAccept), 400},
		{"service_unavailable", string(AccountPreAcceptance), 503},
	}
	for _, tt := range tests {
		failure, err := NewAccountFailure(tt.code, AccountFailurePhase(tt.phase))
		if err != nil || failure.HTTPStatus != tt.status || failure.Code != tt.code {
			t.Errorf("%s/%s = %#v, %v", tt.code, tt.phase, failure, err)
		}
	}
	if _, err := NewAccountFailure("not-a-stable-code", AccountPreAcceptance); err == nil {
		t.Fatal("unknown failure code accepted")
	}
}

func TestLoadAccountOperationIntentKeyPermissions(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	for _, mode := range []os.FileMode{0400, 0600, 0000, 0200, 0500, 0700, 0640, 0644, 0660, 0666} {
		t.Run(mode.String(), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "intent.key")
			if err := os.WriteFile(path, key, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, mode); err != nil {
				t.Fatal(err)
			}
			got, err := LoadAccountOperationIntentKey(path)
			if mode == 0400 || mode == 0600 {
				if err != nil || string(got) != string(key) {
					t.Fatal("owner-only readable key rejected or changed")
				}
			} else if !errors.Is(err, ErrAccountIntentKeyUnavailable) {
				t.Fatal("unsafe permissions accepted")
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != mode {
				t.Fatal("key reader changed permissions")
			}
		})
	}
}

func TestLoadAccountOperationIntentKeyRejectsUnsafeInputs(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "intent.key")
	if err := os.WriteFile(keyPath, []byte("01234567890123456789012345678901"), 0400); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "intent.link")
	if err := os.Symlink(keyPath, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", filepath.Join(dir, "missing"), dir, link} {
		if _, err := LoadAccountOperationIntentKey(path); !errors.Is(err, ErrAccountIntentKeyUnavailable) {
			t.Fatal("unsafe key path accepted")
		}
	}
	for _, length := range []int{0, 31, 33} {
		path := filepath.Join(t.TempDir(), "wrong-length.key")
		if err := os.WriteFile(path, make([]byte, length), 0400); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadAccountOperationIntentKey(path); !errors.Is(err, ErrAccountIntentKeyUnavailable) {
			t.Fatal("wrong key length accepted")
		}
	}
}

func TestAccountUploadIntentFingerprintUsesExactCredentialBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "intent.key")
	if err := os.WriteFile(path, []byte("01234567890123456789012345678901"), 0600); err != nil {
		t.Fatal(err)
	}
	versionA, fingerprintA, err := AccountUploadIntentFingerprint(path, []byte(`{"type":"antigravity","email":"a@example.invalid"}`))
	if err != nil {
		t.Fatal(err)
	}
	versionB, fingerprintB, err := AccountUploadIntentFingerprint(path, []byte(`{"email":"a@example.invalid","type":"antigravity"}`))
	if err != nil {
		t.Fatal(err)
	}
	versionA2, fingerprintA2, err := AccountUploadIntentFingerprint(path, []byte(`{"type":"antigravity","email":"a@example.invalid"}`))
	if err != nil {
		t.Fatal(err)
	}
	if versionA != 1 || versionB != 1 || versionA2 != 1 || !hmac.Equal(fingerprintA, fingerprintA2) || hmac.Equal(fingerprintA, fingerprintB) {
		t.Fatalf("fingerprints did not bind exact bytes: versions=%d/%d/%d equal=%v/%v", versionA, versionB, versionA2, hmac.Equal(fingerprintA, fingerprintA2), hmac.Equal(fingerprintA, fingerprintB))
	}
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	versionReadOnly, fingerprintReadOnly, err := AccountUploadIntentFingerprint(path, []byte(`{"type":"antigravity","email":"a@example.invalid"}`))
	if err != nil || versionReadOnly != versionA || !hmac.Equal(fingerprintA, fingerprintReadOnly) {
		t.Fatal("read-only key changed or rejected exact credential fingerprint")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0400 {
		t.Fatal("fingerprinting changed read-only key mode")
	}
}
