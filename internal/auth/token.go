package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"io"
	"strings"
)

const (
	BearerTokenBytes  = 32
	RecoveryCodeBytes = 16
)

type Digest struct {
	KeyVersion KeyVersion
	Sum        [sha256.Size]byte
}

func GenerateBearerToken() (string, error) {
	return generateBearerToken(rand.Reader)
}

func generateBearerToken(random io.Reader) (string, error) {
	value := make([]byte, BearerTokenBytes)
	if _, err := io.ReadFull(random, value); err != nil {
		return "", errors.New("auth: generate bearer token")
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func GenerateRecoveryCode() (string, error) {
	return generateRecoveryCode(rand.Reader)
}

func generateRecoveryCode(random io.Reader) (string, error) {
	value := make([]byte, RecoveryCodeBytes)
	if _, err := io.ReadFull(random, value); err != nil {
		return "", errors.New("auth: generate recovery code")
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(value)
	return groupCode(encoded, 4), nil
}

func GenerateRecoveryCodes(count int) ([]string, error) {
	if count < 1 || count > 100 {
		return nil, errors.New("auth: invalid recovery code count")
	}
	codes := make([]string, 0, count)
	for range count {
		code, err := GenerateRecoveryCode()
		if err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	return codes, nil
}

func NormalizeRecoveryCode(value string) (string, error) {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(value), "-", ""))
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(normalized)
	if err != nil || len(decoded) != RecoveryCodeBytes {
		return "", errors.New("auth: invalid recovery code")
	}
	return normalized, nil
}

func ComputeDigest(keyring *Keyring, domain KeyDomain, value string) (Digest, error) {
	if !validDigestDomain(domain) {
		return Digest{}, errors.New("auth: invalid digest domain")
	}
	if domain == DomainRecoveryCodeDigest {
		normalized, err := NormalizeRecoveryCode(value)
		if err != nil {
			return Digest{}, err
		}
		value = normalized
	}
	version, key, err := keyring.DeriveCurrent(domain, sha256.Size)
	if err != nil {
		return Digest{}, err
	}
	defer clear(key)
	return computeDigestWithKey(version, key, value), nil
}

func VerifyDigest(keyring *Keyring, domain KeyDomain, value string, expected Digest) (bool, error) {
	if !validDigestDomain(domain) {
		return false, errors.New("auth: invalid digest domain")
	}
	if domain == DomainRecoveryCodeDigest {
		normalized, err := NormalizeRecoveryCode(value)
		if err != nil {
			return false, nil
		}
		value = normalized
	}
	key, err := keyring.Derive(expected.KeyVersion, domain, sha256.Size)
	if err != nil {
		return false, err
	}
	defer clear(key)
	actual := computeDigestWithKey(expected.KeyVersion, key, value)
	return subtle.ConstantTimeCompare(actual.Sum[:], expected.Sum[:]) == 1, nil
}

func validDigestDomain(domain KeyDomain) bool {
	switch domain {
	case DomainSessionDigest, DomainChallengeDigest, DomainActivationDigest,
		DomainRecoveryCodeDigest, DomainCSRFDigest:
		return true
	default:
		return false
	}
}

func computeDigestWithKey(version KeyVersion, key []byte, value string) Digest {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	var sum [sha256.Size]byte
	copy(sum[:], mac.Sum(nil))
	return Digest{KeyVersion: version, Sum: sum}
}

func groupCode(value string, size int) string {
	var builder strings.Builder
	for i, r := range value {
		if i > 0 && i%size == 0 {
			builder.WriteByte('-')
		}
		builder.WriteRune(r)
	}
	return builder.String()
}
