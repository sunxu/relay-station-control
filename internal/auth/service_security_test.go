package auth

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"fmt"
	"testing"
)

func serviceTestKeyring(t *testing.T) *Keyring {
	t.Helper()
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index + 1)
	}
	document := fmt.Sprintf(`{"format_version":1,"environment":"dev","current":1,"keys":[{"version":1,"key":%q}]}`, base64.RawStdEncoding.EncodeToString(key))
	keyring, err := ParseKeyring([]byte(document), EnvironmentDev)
	if err != nil {
		t.Fatal(err)
	}
	return keyring
}

func TestVerifyCSRFRejectsArbitraryNonEmptyProof(t *testing.T) {
	keyring := serviceTestKeyring(t)
	service := &Service{config: &ValidatedConfig{Config: Config{Environment: EnvironmentDev}, Keyring: keyring}}
	valid := "valid-csrf-proof"
	digest, err := service.digestForVersion(1, DomainCSRFDigest, valid)
	if err != nil {
		t.Fatal(err)
	}
	session := Session{KeyVersion: 1, csrfDigest: digest.Sum[:]}
	if err = service.VerifyCSRF(context.Background(), session, valid); err != nil {
		t.Fatalf("valid CSRF rejected: %v", err)
	}
	if err = service.VerifyCSRF(context.Background(), session, "attacker-controlled-proof"); err == nil {
		t.Fatal("arbitrary non-empty CSRF proof was accepted")
	}
	if !hmac.Equal(session.csrfDigest, digest.Sum[:]) {
		t.Fatal("CSRF verification mutated the persisted proof")
	}
}

func TestDigestCandidatesAlwaysTryConfiguredCurrentKeyFirst(t *testing.T) {
	keyring := testKeyring(t, EnvironmentDev, 1, 1, 2)
	service := &Service{config: &ValidatedConfig{Config: Config{Environment: EnvironmentDev}, Keyring: keyring}}
	candidates, err := service.digestCandidates(DomainChallengeDigest, "opaque-challenge")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].KeyVersion != 1 || candidates[1].KeyVersion != 2 {
		t.Fatalf("candidate key order = %+v", candidates)
	}
}
