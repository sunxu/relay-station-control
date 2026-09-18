package store

import (
	"bytes"
	"errors"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/sunxu/relay-station-control/internal/assetcredential"
)

// AssetCredentialSealer is the narrow process capability used by asset
// lifecycle commands. It owns neither K2 loading nor commitment persistence.
type AssetCredentialSealer interface {
	Seal(assetcredential.CredentialKind, uuid.UUID, []byte) ([]byte, error)
	Available() bool
}

type unavailableCredentialSealer struct{}

func (unavailableCredentialSealer) Seal(assetcredential.CredentialKind, uuid.UUID, []byte) ([]byte, error) {
	return nil, ErrCredentialCipherUnavailable
}

func (unavailableCredentialSealer) Available() bool { return false }

var ErrCredentialCipherUnavailable = errors.New("store: credential cipher unavailable")

func validateCredentialPlaintext(value string) bool {
	b := []byte(value)
	if len(b) == 0 || len(b) > 4096 || !utf8.Valid(b) {
		return false
	}
	for _, forbidden := range []byte{0, '\r', '\n'} {
		if bytes.IndexByte(b, forbidden) >= 0 {
			return false
		}
	}
	return true
}
