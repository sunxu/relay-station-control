package api

import (
	"github.com/google/uuid"

	"github.com/sunxu/relay-station-control/internal/assetcredential"
	assetstore "github.com/sunxu/relay-station-control/internal/store"
)

type apiTestCredentialSealer struct{}

func (apiTestCredentialSealer) Available() bool { return true }

func (apiTestCredentialSealer) Seal(kind assetcredential.CredentialKind, id uuid.UUID, plaintext []byte) ([]byte, error) {
	sealed := make([]byte, 32)
	copy(sealed, []byte(kind))
	copy(sealed[8:], id[:])
	copy(sealed[24:], plaintext)
	return sealed, nil
}

var _ assetstore.AssetCredentialSealer = apiTestCredentialSealer{}
