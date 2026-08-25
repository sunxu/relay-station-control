package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

const (
	TOTPPeriodSeconds int64 = 30
	TOTPDigits              = 6
	TOTPSkewSteps     int64 = 1
	TOTPSecretBytes         = 20
)

type EncryptedTOTPSecret struct {
	KeyVersion KeyVersion
	Nonce      []byte
	Ciphertext []byte
}

func GenerateTOTPSecret() (string, error) {
	return generateTOTPSecret(rand.Reader)
}

func generateTOTPSecret(random io.Reader) (string, error) {
	secret := make([]byte, TOTPSecretBytes)
	if _, err := io.ReadFull(random, secret); err != nil {
		return "", errors.New("auth: generate TOTP secret")
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret), nil
}

func EncryptTOTPSecret(keyring *Keyring, administratorID string, secret []byte) (EncryptedTOTPSecret, error) {
	return encryptTOTPSecret(rand.Reader, keyring, administratorID, secret)
}

func encryptTOTPSecret(random io.Reader, keyring *Keyring, administratorID string, secret []byte) (EncryptedTOTPSecret, error) {
	if administratorID == "" || len(secret) < 16 {
		return EncryptedTOTPSecret{}, errors.New("auth: invalid TOTP encryption input")
	}
	version, key, err := keyring.DeriveCurrent(DomainTOTPEncryption, 32)
	if err != nil {
		return EncryptedTOTPSecret{}, err
	}
	defer clear(key)
	aead, err := newGCM(key)
	if err != nil {
		return EncryptedTOTPSecret{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(random, nonce); err != nil {
		return EncryptedTOTPSecret{}, errors.New("auth: generate TOTP nonce")
	}
	aad := totpAAD(administratorID, version)
	ciphertext := aead.Seal(nil, nonce, secret, aad)
	return EncryptedTOTPSecret{KeyVersion: version, Nonce: nonce, Ciphertext: ciphertext}, nil
}

func DecryptTOTPSecret(keyring *Keyring, administratorID string, encrypted EncryptedTOTPSecret) ([]byte, error) {
	if administratorID == "" {
		return nil, errors.New("auth: invalid administrator ID")
	}
	key, err := keyring.Derive(encrypted.KeyVersion, DomainTOTPEncryption, 32)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(encrypted.Nonce) != aead.NonceSize() || len(encrypted.Ciphertext) < aead.Overhead() {
		return nil, errors.New("auth: invalid TOTP ciphertext")
	}
	plaintext, err := aead.Open(nil, encrypted.Nonce, encrypted.Ciphertext, totpAAD(administratorID, encrypted.KeyVersion))
	if err != nil {
		return nil, errors.New("auth: TOTP ciphertext authentication failed")
	}
	return plaintext, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("auth: initialize TOTP cipher")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("auth: initialize TOTP AEAD")
	}
	return aead, nil
}

func totpAAD(administratorID string, version KeyVersion) []byte {
	return []byte(fmt.Sprintf("relay-station-control/totp/v1/admin/%s/key/%d", administratorID, version))
}

func GenerateTOTP(secret string, at time.Time) (string, error) {
	if _, err := decodeTOTPSecret(secret); err != nil {
		return "", err
	}
	return totp.GenerateCodeCustom(secret, at, totpOptions())
}

// ValidateTOTP returns the matching time step. A step less than or equal to
// lastUsedStep is rejected, allowing a transaction to persist monotonic use.
func ValidateTOTP(secret, code string, at time.Time, lastUsedStep int64) (int64, bool, error) {
	if _, err := decodeTOTPSecret(secret); err != nil {
		return 0, false, err
	}
	if len(code) != TOTPDigits {
		return 0, false, nil
	}
	if _, err := strconv.Atoi(code); err != nil {
		return 0, false, nil
	}
	current := at.Unix() / TOTPPeriodSeconds
	// Prefer the current time step, then the adjacent steps. This prevents an
	// older adjacent code from advancing state when the current code is valid.
	for _, offset := range []int64{0, -TOTPSkewSteps, TOTPSkewSteps} {
		step := current + offset
		if step < 0 || step <= lastUsedStep {
			continue
		}
		expected, err := totp.GenerateCodeCustom(
			secret,
			time.Unix(step*TOTPPeriodSeconds, 0).UTC(),
			totpOptions(),
		)
		if err != nil {
			return 0, false, errors.New("auth: generate TOTP code")
		}
		if hmac.Equal([]byte(expected), []byte(code)) {
			return step, true, nil
		}
	}
	return 0, false, nil
}

type TOTPStepConsumer interface {
	// ConsumeTOTP must atomically accept step only if it is newer than the
	// administrator's persisted last-used step.
	ConsumeTOTP(ctx context.Context, administratorID string, step int64) (bool, error)
}

func VerifyAndConsumeTOTP(ctx context.Context, consumer TOTPStepConsumer, administratorID, secret, code string, at time.Time, lastUsedStep int64) (bool, error) {
	if consumer == nil || administratorID == "" {
		return false, errors.New("auth: TOTP step consumer is required")
	}
	step, valid, err := ValidateTOTP(secret, code, at, lastUsedStep)
	if err != nil || !valid {
		return false, err
	}
	return consumer.ConsumeTOTP(ctx, administratorID, step)
}

func decodeTOTPSecret(secret string) ([]byte, error) {
	secret = strings.ToUpper(strings.TrimSpace(secret))
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil || len(decoded) < 16 {
		return nil, errors.New("auth: invalid TOTP secret")
	}
	return decoded, nil
}

func totpOptions() totp.ValidateOpts {
	return totp.ValidateOpts{
		Period:    uint(TOTPPeriodSeconds),
		Skew:      0,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	}
}
