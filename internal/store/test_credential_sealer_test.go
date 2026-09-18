package store_test

import (
	"crypto/sha256"
	"errors"
	"sync"

	"github.com/google/uuid"

	"github.com/sunxu/relay-station-control/internal/assetcredential"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

type deterministicTestSealer struct{}

func (deterministicTestSealer) Available() bool { return true }

func (deterministicTestSealer) Seal(kind assetcredential.CredentialKind, id uuid.UUID, plaintext []byte) ([]byte, error) {
	h := sha256.New()
	_, _ = h.Write([]byte(kind))
	_, _ = h.Write(id[:])
	_, _ = h.Write(plaintext)
	digest := h.Sum(nil)
	blob := make([]byte, 29)
	copy(blob, digest)
	blob[28] ^= 0xa5
	return blob, nil
}

type unavailableTestSealer struct{}

func (unavailableTestSealer) Available() bool { return false }

func (unavailableTestSealer) Seal(assetcredential.CredentialKind, uuid.UUID, []byte) ([]byte, error) {
	return nil, errors.New("test credential sealer unavailable")
}

type countingTestSealer struct {
	inner assetstore.AssetCredentialSealer
	mu    sync.Mutex
	calls int
}

func (s *countingTestSealer) Available() bool { return s.inner.Available() }

func (s *countingTestSealer) Seal(kind assetcredential.CredentialKind, id uuid.UUID, plaintext []byte) ([]byte, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return s.inner.Seal(kind, id, plaintext)
}

func (s *countingTestSealer) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func newAvailableTestSealer() assetstore.AssetCredentialSealer {
	return deterministicTestSealer{}
}

func newUnavailableTestSealer() assetstore.AssetCredentialSealer {
	return unavailableTestSealer{}
}

func newCountingTestSealer() *countingTestSealer {
	return &countingTestSealer{inner: deterministicTestSealer{}}
}
