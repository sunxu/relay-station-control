// Package assetcredential contains the narrow Stage 0 credential protection
// primitives. It deliberately has no dependency on product asset workflows.
package assetcredential

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

const (
	KeySize   = 32
	NonceSize = 12
)

var (
	ErrInvalidKeyFile = errors.New("asset credential key file is invalid")
	ErrInvalidKey     = errors.New("asset credential key is invalid")
	ErrInvalidBlob    = errors.New("asset credential blob is invalid")
)

// LoadKeyFile validates the complete external K2 file contract and returns a
// private copy of its bytes. expectedOwner is the numeric runtime UID.
func LoadKeyFile(path string, expectedOwner uint32) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, ErrInvalidKeyFile
	}
	fileOwned := false
	defer func() {
		if !fileOwned {
			_ = unix.Close(fd)
		}
	}()

	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Uid != expectedOwner || !validMode(os.FileMode(stat.Mode&0o7777)) {
		return nil, ErrInvalidKeyFile
	}
	f := os.NewFile(uintptr(fd), path)
	if f == nil {
		return nil, ErrInvalidKeyFile
	}
	fileOwned = true
	defer f.Close()
	value, err := io.ReadAll(io.LimitReader(f, KeySize+1))
	if err != nil || len(value) != KeySize {
		return nil, ErrInvalidKeyFile
	}
	return append([]byte(nil), value...), nil
}

func validMode(mode os.FileMode) bool { return mode == 0o400 || mode == 0o600 }

// KeyLoader caches both the successful key and the failure, so a process
// cannot hot reload K2 after startup.
type KeyLoader struct {
	path  string
	owner uint32
	once  sync.Once
	key   []byte
	err   error
}

func NewKeyLoader(path string, expectedOwner uint32) *KeyLoader {
	return &KeyLoader{path: path, owner: expectedOwner}
}

func (l *KeyLoader) Load() ([]byte, error) {
	l.once.Do(func() { l.key, l.err = LoadKeyFile(l.path, l.owner) })
	if l.err != nil {
		return nil, l.err
	}
	return append([]byte(nil), l.key...), nil
}

// ProvisionKey creates a valid K2 exactly once. It never replaces an existing
// file. A temporary same-directory file is published with an exclusive hard
// link, so failed writes cannot become the target file.
func ProvisionKey(path string, rng io.Reader, mode os.FileMode) error {
	if !validMode(mode) {
		return ErrInvalidKeyFile
	}
	if _, err := os.Lstat(path); err == nil {
		if _, err := LoadKeyFile(path, uint32(os.Getuid())); err != nil {
			return ErrInvalidKeyFile
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return ErrInvalidKeyFile
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".control-asset-k2-")
	if err != nil {
		return ErrInvalidKeyFile
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		_ = tmp.Close()
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		return ErrInvalidKeyFile
	}
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(rng, key); err != nil {
		return ErrInvalidKeyFile
	}
	if _, err := tmp.Write(key); err != nil {
		return ErrInvalidKeyFile
	}
	if err := tmp.Sync(); err != nil {
		return ErrInvalidKeyFile
	}
	if err := tmp.Close(); err != nil {
		return ErrInvalidKeyFile
	}
	if err := os.Link(tmpPath, path); err != nil {
		if _, statErr := os.Lstat(path); statErr == nil {
			if _, loadErr := LoadKeyFile(path, uint32(os.Getuid())); loadErr == nil {
				cleanup = true
				return nil
			}
		}
		return ErrInvalidKeyFile
	}
	cleanup = true
	return nil
}

// ProvisionKeyFromOS is the bootstrap entry point. Runtime loading never
// calls it; only explicit provisioning may create K2, using the OS CSPRNG.
func ProvisionKeyFromOS(path string, mode os.FileMode) error {
	return ProvisionKey(path, rand.Reader, mode)
}

const commitmentDomain = "relay-station/control-asset-credential-key/v1"

func IdentityCommitment(k2 []byte) [sha256.Size]byte {
	preimage := make([]byte, 0, len(commitmentDomain)+1+len(k2))
	preimage = append(preimage, commitmentDomain...)
	preimage = append(preimage, 0)
	preimage = append(preimage, k2...)
	return sha256.Sum256(preimage)
}

type CommitmentStatus uint8

const (
	CommitmentUnavailable CommitmentStatus = iota
	CommitmentAbsent
	CommitmentMatch
	CommitmentMismatch
)

// CheckCommitment exposes only the state needed by later persistence gates;
// it never returns or formats the commitment value.
func CheckCommitment(k2 []byte, expected *[sha256.Size]byte) CommitmentStatus {
	if len(k2) != KeySize {
		return CommitmentUnavailable
	}
	if expected == nil {
		return CommitmentAbsent
	}
	derived := IdentityCommitment(k2)
	if derived == *expected {
		return CommitmentMatch
	}
	return CommitmentMismatch
}

type CredentialKind string

const (
	NodeCredential    CredentialKind = "node"
	GatewayCredential CredentialKind = "gateway"
)

func aad(kind CredentialKind, id uuid.UUID) ([]byte, error) {
	var domain string
	switch kind {
	case NodeCredential:
		domain = "relay-station/node-management-credential/v1"
	case GatewayCredential:
		domain = "relay-station/gateway-directory-credential/v1"
	default:
		return nil, ErrInvalidKey
	}
	result := make([]byte, 0, len(domain)+1+len(id))
	result = append(result, domain...)
	result = append(result, 0)
	result = append(result, id[:]...)
	return result, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != KeySize {
		return nil, ErrInvalidKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrInvalidKey
	}
	return cipher.NewGCM(block)
}

func Seal(rng io.Reader, key []byte, kind CredentialKind, id uuid.UUID, plaintext []byte) ([]byte, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	associated, err := aad(kind, id)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rng, nonce); err != nil {
		return nil, ErrInvalidBlob
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, associated)
	result := make([]byte, 0, len(nonce)+len(ciphertext))
	result = append(result, nonce...)
	result = append(result, ciphertext...)
	return result, nil
}

func Open(key []byte, kind CredentialKind, id uuid.UUID, blob []byte) ([]byte, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	if len(blob) < NonceSize+aead.Overhead() {
		return nil, ErrInvalidBlob
	}
	associated, err := aad(kind, id)
	if err != nil {
		return nil, err
	}
	plaintext, err := aead.Open(nil, blob[:NonceSize], blob[NonceSize:], associated)
	if err != nil {
		return nil, ErrInvalidBlob
	}
	return plaintext, nil
}

// StartupWarning emits exactly one sanitized warning per process instance.
type StartupWarning struct{ once sync.Once }

func (w *StartupWarning) Emit(emit func(string)) {
	w.once.Do(func() { emit("asset credential cipher unavailable") })
}
