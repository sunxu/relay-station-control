package auth

import (
	"bytes"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
)

const (
	keyringFormatVersion = 1
	masterKeyBytes       = 32
)

type KeyVersion uint32

type KeyDomain string

const (
	DomainTOTPEncryption      KeyDomain = "totp-encryption"
	DomainSessionDigest       KeyDomain = "session-digest"
	DomainChallengeDigest     KeyDomain = "challenge-digest"
	DomainActivationDigest    KeyDomain = "activation-digest"
	DomainRecoveryCodeDigest  KeyDomain = "recovery-code-digest"
	DomainLoginFingerprint    KeyDomain = "login-fingerprint"
	DomainSourceFingerprint   KeyDomain = "source-fingerprint"
	DomainCSRFDigest          KeyDomain = "csrf-digest"
	DomainBootstrapComparison KeyDomain = "bootstrap-comparison"
	DomainAssetCursorDigest   KeyDomain = "asset-cursor-digest"
	DomainJobCursorDigest     KeyDomain = "job-cursor-digest"
)

func (v KeyDomain) Valid() bool {
	switch v {
	case DomainTOTPEncryption, DomainSessionDigest, DomainChallengeDigest,
		DomainActivationDigest, DomainRecoveryCodeDigest, DomainLoginFingerprint,
		DomainSourceFingerprint, DomainCSRFDigest, DomainBootstrapComparison,
		DomainAssetCursorDigest, DomainJobCursorDigest:
		return true
	default:
		return false
	}
}

type keyringDocument struct {
	FormatVersion int                `json:"format_version"`
	Environment   Environment        `json:"environment"`
	Current       KeyVersion         `json:"current"`
	Keys          []keyringKeyRecord `json:"keys"`
}

type keyringKeyRecord struct {
	Version KeyVersion `json:"version"`
	Key     string     `json:"key"`
}

type Keyring struct {
	environment Environment
	current     KeyVersion
	keys        map[KeyVersion][masterKeyBytes]byte
}

func LoadKeyringFile(path string, expectedEnvironment Environment) (*Keyring, error) {
	if err := validateSecretFile(path, 1); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("keyring file is unreadable")
	}
	return ParseKeyring(data, expectedEnvironment)
}

func ParseKeyring(data []byte, expectedEnvironment Environment) (*Keyring, error) {
	if !expectedEnvironment.Valid() {
		return nil, errors.New("auth: invalid expected keyring environment")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var document keyringDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, errors.New("auth: invalid keyring document")
	}
	if decoder.Decode(&struct{}{}) == nil {
		return nil, errors.New("auth: trailing keyring data")
	}
	if document.FormatVersion != keyringFormatVersion {
		return nil, errors.New("auth: unsupported keyring format version")
	}
	if document.Environment != expectedEnvironment {
		return nil, errors.New("auth: keyring environment mismatch")
	}
	if document.Current == 0 || len(document.Keys) == 0 {
		return nil, errors.New("auth: keyring current key is missing")
	}

	keys := make(map[KeyVersion][masterKeyBytes]byte, len(document.Keys))
	for _, record := range document.Keys {
		if record.Version == 0 {
			return nil, errors.New("auth: key version zero is reserved")
		}
		if _, exists := keys[record.Version]; exists {
			return nil, errors.New("auth: duplicate key version")
		}
		decoded, err := base64.RawStdEncoding.Strict().DecodeString(record.Key)
		if err != nil || len(decoded) != masterKeyBytes {
			return nil, errors.New("auth: key must be exactly 32 bytes of raw base64")
		}
		var key [masterKeyBytes]byte
		copy(key[:], decoded)
		keys[record.Version] = key
	}
	if _, exists := keys[document.Current]; !exists {
		return nil, errors.New("auth: current key version is not present")
	}
	return &Keyring{environment: document.Environment, current: document.Current, keys: keys}, nil
}

func (k *Keyring) CurrentVersion() KeyVersion {
	return k.current
}

func (k *Keyring) Versions() []KeyVersion {
	versions := make([]KeyVersion, 0, len(k.keys))
	for version := range k.keys {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	return versions
}

func (k *Keyring) Derive(version KeyVersion, domain KeyDomain, length int) ([]byte, error) {
	if k == nil || !k.environment.Valid() {
		return nil, errors.New("auth: keyring is unavailable")
	}
	if !domain.Valid() {
		return nil, errors.New("auth: invalid key domain")
	}
	if length < 16 || length > 64 {
		return nil, errors.New("auth: invalid derived key length")
	}
	master, ok := k.keys[version]
	if !ok {
		return nil, errors.New("auth: unknown key version")
	}
	salt := sha256.Sum256([]byte("relay-station-control/auth/" + string(k.environment)))
	info := fmt.Sprintf("relay-station-control/auth/v1/%s/key-version/%d", domain, version)
	return hkdf.Key(sha256.New, master[:], salt[:], info, length)
}

func (k *Keyring) DeriveCurrent(domain KeyDomain, length int) (KeyVersion, []byte, error) {
	key, err := k.Derive(k.current, domain, length)
	return k.current, key, err
}
