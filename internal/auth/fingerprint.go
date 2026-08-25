package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/netip"
	"regexp"
	"strings"
	"unicode/utf8"
)

var loginNamePattern = regexp.MustCompile(`^[a-z0-9._-]{3,64}$`)

func NormalizeLoginName(value string) (string, error) {
	normalized := strings.ToLower(value)
	if !loginNamePattern.MatchString(normalized) {
		return "", errors.New("auth: invalid login name")
	}
	return normalized, nil
}

func ValidateDisplayName(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if !utf8.ValidString(trimmed) {
		return "", errors.New("auth: invalid display name")
	}
	length := utf8.RuneCountInString(trimmed)
	if length < 1 || length > 100 {
		return "", errors.New("auth: invalid display name length")
	}
	return trimmed, nil
}

func LoginFingerprint(keyring *Keyring, loginName string) (Digest, error) {
	normalized, err := NormalizeLoginName(loginName)
	if err != nil {
		// Invalid login input still needs a stable, non-enumerating bucket. It is
		// deliberately not echoed in the error or fingerprint representation.
		normalized = strings.ToLower(loginName)
	}
	return fingerprint(keyring, DomainLoginFingerprint, normalized)
}

func SourceFingerprint(keyring *Keyring, address netip.Addr) (Digest, error) {
	if !address.IsValid() {
		return Digest{}, errors.New("auth: invalid source address")
	}
	return fingerprint(keyring, DomainSourceFingerprint, address.Unmap().String())
}

func FingerprintString(value Digest) string {
	return base64.RawURLEncoding.EncodeToString(value.Sum[:])
}

func fingerprint(keyring *Keyring, domain KeyDomain, value string) (Digest, error) {
	version, key, err := keyring.DeriveCurrent(domain, sha256.Size)
	if err != nil {
		return Digest{}, err
	}
	defer clear(key)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	var sum [sha256.Size]byte
	copy(sum[:], mac.Sum(nil))
	return Digest{KeyVersion: version, Sum: sum}, nil
}
