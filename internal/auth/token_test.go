package auth

import (
	"encoding/base32"
	"encoding/base64"
	"strings"
	"testing"
)

func TestTokenEntropyEncodingAndDigest(t *testing.T) {
	keyring := testKeyring(t, EnvironmentProduction, 1, 1)
	token, err := GenerateBearerToken()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(decoded) != BearerTokenBytes {
		t.Fatalf("bearer token bytes = %d, err = %v", len(decoded), err)
	}
	digest, err := ComputeDigest(keyring, DomainSessionDigest, token)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(FingerprintString(digest), token) {
		t.Fatal("digest representation contains plaintext token")
	}
	valid, err := VerifyDigest(keyring, DomainSessionDigest, token, digest)
	if err != nil || !valid {
		t.Fatalf("VerifyDigest() = %v, %v", valid, err)
	}
	if valid, _ := VerifyDigest(keyring, DomainSessionDigest, token+"x", digest); valid {
		t.Fatal("wrong token verified")
	}
	if valid, _ := VerifyDigest(keyring, DomainChallengeDigest, token, digest); valid {
		t.Fatal("digest verified across domains")
	}
	if _, err := ComputeDigest(keyring, DomainTOTPEncryption, token); err == nil {
		t.Fatal("encryption domain accepted for a digest")
	}
}

func TestRecoveryCodeEntropyNormalizationAndDistinctness(t *testing.T) {
	keyring := testKeyring(t, EnvironmentProduction, 1, 1)
	codes, err := GenerateRecoveryCodes(10)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for _, code := range codes {
		normalized, err := NormalizeRecoveryCode(strings.ToLower(code))
		if err != nil {
			t.Fatalf("NormalizeRecoveryCode(%q): %v", code, err)
		}
		decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(normalized)
		if err != nil || len(decoded) != RecoveryCodeBytes {
			t.Fatalf("recovery code bytes = %d, err = %v", len(decoded), err)
		}
		if seen[normalized] {
			t.Fatal("duplicate recovery code")
		}
		seen[normalized] = true
		digest, err := ComputeDigest(keyring, DomainRecoveryCodeDigest, code)
		if err != nil {
			t.Fatal(err)
		}
		valid, err := VerifyDigest(keyring, DomainRecoveryCodeDigest, strings.ToLower(strings.ReplaceAll(code, "-", "")), digest)
		if err != nil || !valid {
			t.Fatalf("normalized recovery code did not verify: %v, %v", valid, err)
		}
	}
}
