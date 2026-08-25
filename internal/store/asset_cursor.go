package store

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/google/uuid"

	authn "github.com/sunxu/relay-station-control/internal/auth"
)

const nodeCursorVersion = 1

var ErrInvalidNodeCursor = errors.New("store: invalid node cursor")

// NodeListFilters is the complete filter set bound into a node-list cursor.
// Keeping this type closed prevents a cursor from being replayed under a
// different query without being rejected.
type NodeListFilters struct {
	NodeType         string
	Capability       string
	MonitoringActive *bool
}

type nodeCursorPayload struct {
	Version     int    `json:"v"`
	KeyVersion  uint32 `json:"key_version"`
	After       string `json:"after"`
	FiltersHash string `json:"filters_hash"`
	Digest      string `json:"digest"`
}

type NodeCursorCodec struct {
	keyring *authn.Keyring
}

func NewNodeCursorCodec(keyring *authn.Keyring) (*NodeCursorCodec, error) {
	if keyring == nil {
		return nil, ErrInvalidNodeCursor
	}
	if _, _, err := keyring.DeriveCurrent(authn.DomainAssetCursorDigest, sha256.Size); err != nil {
		return nil, ErrInvalidNodeCursor
	}
	return &NodeCursorCodec{keyring: keyring}, nil
}

type normalizedNodeListFilters struct {
	NodeType         string `json:"node_type"`
	Capability       string `json:"capability"`
	MonitoringActive string `json:"monitoring_active"`
}

// EncodeNodeCursor creates an opaque, URL-safe cursor containing only the last
// immutable instance ID and a hash of the normalized filters. The HMAC is an
// authenticated integrity tag; the cursor is not an authorization credential.
func (codec *NodeCursorCodec) Encode(after uuid.UUID, filters NodeListFilters) (string, error) {
	if after == uuid.Nil {
		return "", ErrInvalidNodeCursor
	}
	keyVersion, key, err := codec.keyring.DeriveCurrent(authn.DomainAssetCursorDigest, sha256.Size)
	if err != nil {
		return "", ErrInvalidNodeCursor
	}
	filterHash := nodeFilterHash(filters)
	payload := nodeCursorPayload{
		Version:     nodeCursorVersion,
		KeyVersion:  uint32(keyVersion),
		After:       after.String(),
		FiltersHash: filterHash,
	}
	payload.Digest = hex.EncodeToString(nodeCursorDigest(key, payload.Version, payload.KeyVersion, payload.After, payload.FiltersHash))
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", ErrInvalidNodeCursor
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

// DecodeNodeCursor accepts an empty cursor as the first page. Non-empty cursors
// must have valid structure, UUID, integrity checksum, and filter binding.
func (codec *NodeCursorCodec) Decode(cursor string, filters NodeListFilters) (uuid.UUID, error) {
	if cursor == "" {
		return uuid.Nil, nil
	}
	if len(cursor) > 512 {
		return uuid.Nil, ErrInvalidNodeCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return uuid.Nil, ErrInvalidNodeCursor
	}
	var payload nodeCursorPayload
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return uuid.Nil, ErrInvalidNodeCursor
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return uuid.Nil, ErrInvalidNodeCursor
	}
	key, err := codec.keyring.Derive(authn.KeyVersion(payload.KeyVersion), authn.DomainAssetCursorDigest, sha256.Size)
	if err != nil {
		return uuid.Nil, ErrInvalidNodeCursor
	}
	expectedDigest := nodeCursorDigest(key, payload.Version, payload.KeyVersion, payload.After, payload.FiltersHash)
	providedDigest, err := hex.DecodeString(payload.Digest)
	if err != nil || payload.Version != nodeCursorVersion || payload.FiltersHash != nodeFilterHash(filters) ||
		!hmac.Equal(providedDigest, expectedDigest) {
		return uuid.Nil, ErrInvalidNodeCursor
	}
	after, err := uuid.Parse(payload.After)
	if err != nil || after == uuid.Nil {
		return uuid.Nil, ErrInvalidNodeCursor
	}
	return after, nil
}

func nodeFilterHash(filters NodeListFilters) string {
	monitoring := "any"
	if filters.MonitoringActive != nil {
		if *filters.MonitoringActive {
			monitoring = "true"
		} else {
			monitoring = "false"
		}
	}
	normalized := normalizedNodeListFilters{
		NodeType:         strings.TrimSpace(filters.NodeType),
		Capability:       strings.TrimSpace(filters.Capability),
		MonitoringActive: monitoring,
	}
	data, _ := json.Marshal(normalized)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func nodeCursorDigest(key []byte, version int, keyVersion uint32, after, filterHash string) []byte {
	data, _ := json.Marshal(struct {
		Domain      string `json:"domain"`
		Version     int    `json:"v"`
		KeyVersion  uint32 `json:"key_version"`
		After       string `json:"after"`
		FiltersHash string `json:"filters_hash"`
	}{"relay-control-node-cursor-v1", version, keyVersion, after, filterHash})
	digest := hmac.New(sha256.New, key)
	_, _ = digest.Write(data)
	return digest.Sum(nil)
}
