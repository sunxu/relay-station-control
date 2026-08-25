package auth

import (
	"context"
	"encoding/base32"
	"sync"
	"testing"
	"time"
)

const rfcTOTPSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

func TestGenerateTOTPSecretEntropyAndEncoding(t *testing.T) {
	first, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("TOTP secret was reused")
	}
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(first)
	if err != nil || len(decoded) != TOTPSecretBytes {
		t.Fatalf("TOTP secret bytes = %d, error = %v", len(decoded), err)
	}
}

func TestTOTPMatchesRFC6238AndWindow(t *testing.T) {
	at := time.Unix(59, 0)
	code, err := GenerateTOTP(rfcTOTPSecret, at)
	if err != nil {
		t.Fatal(err)
	}
	// RFC 6238's SHA-1/8-digit vector is 94287082; the required six-digit
	// authenticator form is the final six digits.
	if code != "287082" {
		t.Fatalf("GenerateTOTP() = %q, want 287082", code)
	}
	for _, offset := range []time.Duration{-30 * time.Second, 0, 30 * time.Second} {
		candidate, _ := GenerateTOTP(rfcTOTPSecret, at.Add(offset))
		step, valid, err := ValidateTOTP(rfcTOTPSecret, candidate, at, -1)
		if err != nil || !valid || step != at.Add(offset).Unix()/TOTPPeriodSeconds {
			t.Fatalf("window offset %v = %d, %v, %v", offset, step, valid, err)
		}
	}
	outside, _ := GenerateTOTP(rfcTOTPSecret, at.Add(60*time.Second))
	if _, valid, err := ValidateTOTP(rfcTOTPSecret, outside, at, -1); err != nil || valid {
		t.Fatalf("outside window accepted: valid=%v err=%v", valid, err)
	}
}

func TestTOTPReplayAndCodeValidation(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	code, _ := GenerateTOTP(rfcTOTPSecret, at)
	step := at.Unix() / TOTPPeriodSeconds
	if _, valid, err := ValidateTOTP(rfcTOTPSecret, code, at, step); err != nil || valid {
		t.Fatalf("replayed step accepted: valid=%v err=%v", valid, err)
	}
	for _, invalid := range []string{"12345", "1234567", "abcdef"} {
		if _, valid, err := ValidateTOTP(rfcTOTPSecret, invalid, at, -1); err != nil || valid {
			t.Fatalf("invalid code %q accepted", invalid)
		}
	}
}

type atomicStepConsumer struct {
	mu       sync.Mutex
	lastStep int64
}

func (c *atomicStepConsumer) ConsumeTOTP(_ context.Context, _ string, step int64) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if step <= c.lastStep {
		return false, nil
	}
	c.lastStep = step
	return true, nil
}

func TestVerifyAndConsumeTOTPConcurrentReplay(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	code, _ := GenerateTOTP(rfcTOTPSecret, at)
	consumer := &atomicStepConsumer{lastStep: -1}
	results := make(chan bool, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			accepted, err := VerifyAndConsumeTOTP(context.Background(), consumer, "admin-id", rfcTOTPSecret, code, at, -1)
			if err != nil {
				t.Errorf("VerifyAndConsumeTOTP() error = %v", err)
			}
			results <- accepted
		}()
	}
	group.Wait()
	close(results)
	successes := 0
	for result := range results {
		if result {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful concurrent uses = %d, want 1", successes)
	}
}

func TestTOTPEncryptionBindsAdministratorAndKeyVersion(t *testing.T) {
	keyring := testKeyring(t, EnvironmentProduction, 2, 1, 2)
	secret := []byte("12345678901234567890")
	first, err := EncryptTOTPSecret(keyring, "admin-1", secret)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := EncryptTOTPSecret(keyring, "admin-1", secret)
	if string(first.Nonce) == string(second.Nonce) || string(first.Ciphertext) == string(second.Ciphertext) {
		t.Fatal("TOTP encryption reused a nonce")
	}
	plaintext, err := DecryptTOTPSecret(keyring, "admin-1", first)
	if err != nil || string(plaintext) != string(secret) {
		t.Fatalf("DecryptTOTPSecret() = %q, %v", plaintext, err)
	}
	if _, err := DecryptTOTPSecret(keyring, "admin-2", first); err == nil {
		t.Fatal("ciphertext accepted for a different administrator")
	}
	wrongEnvironment := testKeyring(t, EnvironmentDev, 2, 1, 2)
	if _, err := DecryptTOTPSecret(wrongEnvironment, "admin-1", first); err == nil {
		t.Fatal("ciphertext accepted with the wrong environment key")
	}
	tampered := first
	tampered.Ciphertext = append([]byte(nil), first.Ciphertext...)
	tampered.Ciphertext[0] ^= 1
	if _, err := DecryptTOTPSecret(keyring, "admin-1", tampered); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	oldKeyring := testKeyring(t, EnvironmentProduction, 1, 1)
	oldCiphertext, err := EncryptTOTPSecret(oldKeyring, "admin-1", secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecryptTOTPSecret(keyring, "admin-1", oldCiphertext); err != nil {
		t.Fatalf("old key ciphertext not decryptable after rotation: %v", err)
	}
}
