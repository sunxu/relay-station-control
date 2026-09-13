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
var ErrInvalidGatewayCursor = errors.New("store: invalid gateway cursor")
var ErrGatewayCursorStale = errors.New("store: gateway cursor stale")

// NodeListFilters is the complete filter set bound into a node-list cursor.
// Keeping this type closed prevents a cursor from being replayed under a
// different query without being rejected.
type NodeListFilters struct {
	NodeType         string
	Capability       string
	MonitoringActive *bool
}

type GatewayCursor struct {
	After       uuid.UUID
	Environment string
	Lifecycle   string
	Generation  string
}

type GatewayCursorCodec struct{ keyring *authn.Keyring }

type gatewayCursorPayload struct {
	Version     int    `json:"v"`
	KeyVersion  uint32 `json:"key_version"`
	Lifecycle   string `json:"lifecycle"`
	Environment string `json:"environment"`
	After       string `json:"after"`
	Generation  string `json:"generation"`
	Digest      string `json:"digest"`
}

func NewGatewayCursorCodec(keyring *authn.Keyring) (*GatewayCursorCodec, error) {
	if keyring == nil {
		return nil, ErrInvalidGatewayCursor
	}
	if _, _, err := keyring.DeriveCurrent(authn.DomainAssetCursorDigest, sha256.Size); err != nil {
		return nil, ErrInvalidGatewayCursor
	}
	return &GatewayCursorCodec{keyring: keyring}, nil
}

func (c *GatewayCursorCodec) Encode(value GatewayCursor) (string, error) {
	if value.After == uuid.Nil || value.Generation == "" || value.Lifecycle == "" || value.Environment == "" {
		return "", ErrInvalidGatewayCursor
	}
	v, key, err := c.keyring.DeriveCurrent(authn.DomainAssetCursorDigest, sha256.Size)
	if err != nil {
		return "", ErrInvalidGatewayCursor
	}
	p := gatewayCursorPayload{Version: 1, KeyVersion: uint32(v), Lifecycle: value.Lifecycle, Environment: value.Environment, After: value.After.String(), Generation: value.Generation}
	p.Digest = hex.EncodeToString(gatewayCursorDigest(key, p))
	raw, err := json.Marshal(p)
	if err != nil {
		return "", ErrInvalidGatewayCursor
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (c *GatewayCursorCodec) Decode(raw string, lifecycle, environment string) (GatewayCursor, error) {
	if raw == "" {
		return GatewayCursor{Lifecycle: lifecycle, Environment: environment}, nil
	}
	if len(raw) > 512 {
		return GatewayCursor{}, ErrInvalidGatewayCursor
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return GatewayCursor{}, ErrInvalidGatewayCursor
	}
	var p gatewayCursorPayload
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil {
		return GatewayCursor{}, ErrInvalidGatewayCursor
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return GatewayCursor{}, ErrInvalidGatewayCursor
	}
	key, err := c.keyring.Derive(authn.KeyVersion(p.KeyVersion), authn.DomainAssetCursorDigest, sha256.Size)
	if err != nil {
		return GatewayCursor{}, ErrInvalidGatewayCursor
	}
	provided, err := hex.DecodeString(p.Digest)
	if err != nil || p.Version != 1 || p.Lifecycle != lifecycle || p.Environment != environment || !hmac.Equal(provided, gatewayCursorDigest(key, p)) {
		return GatewayCursor{}, ErrInvalidGatewayCursor
	}
	after, err := uuid.Parse(p.After)
	if err != nil || after == uuid.Nil {
		return GatewayCursor{}, ErrInvalidGatewayCursor
	}
	return GatewayCursor{After: after, Lifecycle: p.Lifecycle, Environment: p.Environment, Generation: p.Generation}, nil
}

func gatewayCursorDigest(key []byte, p gatewayCursorPayload) []byte {
	data, _ := json.Marshal([]any{"relay-control-gateway-cursor-v1", p.Version, p.KeyVersion, p.Environment, p.Lifecycle, p.After, p.Generation})
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)
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
