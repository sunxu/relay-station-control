package store

import (
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

func TestLoadAccountOperationIntentKeyRejectsUnsafeInputs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "intent.key")
	key := []byte("01234567890123456789012345678901")
	if err := os.WriteFile(path, key, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadAccountOperationIntentKey(path)
	if err != nil || string(got) != string(key) {
		t.Fatalf("valid key = %q, %v", got, err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAccountOperationIntentKey(path); !errors.Is(err, ErrAccountIntentKeyUnavailable) {
		t.Fatalf("unsafe permissions error = %v", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAccountOperationIntentKey(path); !errors.Is(err, ErrAccountIntentKeyUnavailable) {
		t.Fatalf("missing key error = %v", err)
	}
}
