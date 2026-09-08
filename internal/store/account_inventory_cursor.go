package store

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	authn "github.com/sunxu/relay-station-control/internal/auth"
)

const (
	accountInventoryCursorVersion       = 1
	accountInventoryCursorTTL           = 15 * time.Minute
	accountInventoryCursorMaxTokenBytes = 1536
	accountInventoryCursorMaxPlaintext  = 1100
	accountInventoryCursorMaxKeyBytes   = 385
	accountInventoryCursorHeaderBytes   = 5
)

var ErrInvalidAccountInventoryCursor = errors.New("store: invalid account inventory cursor")

// AccountInventoryCursorFilters is the complete sensitive filter set bound to
// an account-inventory continuation. Instance and actor identities are bound
// separately, and page size is deliberately not part of query identity.
type AccountInventoryCursorFilters struct {
	Provider    string
	Lifecycle   string
	BasicStatus string
	Email       string
	Window      string
	Quality     string
	Scope       string
}

func (AccountInventoryCursorFilters) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED AccountInventoryCursorFilters]"))
}

// AccountInventoryCursorCodec encrypts sensitive continuation keys. A cursor
// is only pagination state: authentication and authorization must be repeated
// for every request.
type AccountInventoryCursorCodec struct {
	keyring *authn.Keyring
	now     func() time.Time
	random  io.Reader
}

type accountInventoryCursorPayload struct {
	Version         int    `json:"v"`
	KeyVersion      uint32 `json:"k"`
	ActorAdminID    string `json:"a"`
	InstanceID      string `json:"i"`
	FiltersHash     string `json:"f"`
	AfterAccountKey string `json:"p"`
	IssuedAt        int64  `json:"n"`
	ExpiresAt       int64  `json:"x"`
}

type normalizedAccountInventoryCursorFilters struct {
	Provider    string `json:"provider"`
	Lifecycle   string `json:"lifecycle"`
	BasicStatus string `json:"basic_status"`
	Email       string `json:"email"`
	Window      string `json:"window,omitempty"`
	Quality     string `json:"quality,omitempty"`
	Scope       string `json:"scope,omitempty"`
}

func NewAccountInventoryCursorCodec(keyring *authn.Keyring) (*AccountInventoryCursorCodec, error) {
	return newAccountInventoryCursorCodec(keyring, time.Now, rand.Reader)
}

func newAccountInventoryCursorCodec(keyring *authn.Keyring, now func() time.Time, random io.Reader) (*AccountInventoryCursorCodec, error) {
	if keyring == nil || now == nil || random == nil || !keyring.Environment().Valid() {
		return nil, ErrInvalidAccountInventoryCursor
	}
	_, key, err := keyring.DeriveCurrent(authn.DomainAccountInventoryCursorEncryption, 32)
	if err != nil {
		return nil, ErrInvalidAccountInventoryCursor
	}
	clear(key)
	return &AccountInventoryCursorCodec{keyring: keyring, now: now, random: random}, nil
}

func (codec *AccountInventoryCursorCodec) Encode(
	actorAdminID, instanceID uuid.UUID,
	filters AccountInventoryCursorFilters,
	afterAccountKey string,
) (string, error) {
	if codec == nil || actorAdminID == uuid.Nil || instanceID == uuid.Nil ||
		!validAccountInventoryCursorFilters(filters) || !validAccountInventoryCursorKey(afterAccountKey, filters) {
		return "", ErrInvalidAccountInventoryCursor
	}
	now := codec.now().UTC()
	issuedAt := now.Unix()
	if issuedAt <= 0 || issuedAt > math.MaxInt64-int64(accountInventoryCursorTTL/time.Second) {
		return "", ErrInvalidAccountInventoryCursor
	}
	keyVersion, key, err := codec.keyring.DeriveCurrent(authn.DomainAccountInventoryCursorEncryption, 32)
	if err != nil {
		return "", ErrInvalidAccountInventoryCursor
	}
	defer clear(key)
	aead, err := accountInventoryCursorAEAD(key)
	if err != nil {
		return "", ErrInvalidAccountInventoryCursor
	}
	payload := accountInventoryCursorPayload{
		Version: accountInventoryCursorVersion, KeyVersion: uint32(keyVersion),
		ActorAdminID: actorAdminID.String(), InstanceID: instanceID.String(),
		FiltersHash: accountInventoryCursorFilterHash(filters), AfterAccountKey: afterAccountKey,
		IssuedAt: issuedAt, ExpiresAt: issuedAt + int64(accountInventoryCursorTTL/time.Second),
	}
	plaintext, err := marshalCanonicalAccountInventoryCursorJSON(payload)
	if err != nil || len(plaintext) > accountInventoryCursorMaxPlaintext {
		return "", ErrInvalidAccountInventoryCursor
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(codec.random, nonce); err != nil {
		return "", ErrInvalidAccountInventoryCursor
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, accountInventoryCursorAAD(codec.keyring.Environment(), keyVersion))
	raw := make([]byte, accountInventoryCursorHeaderBytes+len(nonce)+len(ciphertext))
	raw[0] = accountInventoryCursorVersion
	binary.BigEndian.PutUint32(raw[1:accountInventoryCursorHeaderBytes], uint32(keyVersion))
	copy(raw[accountInventoryCursorHeaderBytes:], nonce)
	copy(raw[accountInventoryCursorHeaderBytes+len(nonce):], ciphertext)
	token := base64.RawURLEncoding.EncodeToString(raw)
	if len(token) > accountInventoryCursorMaxTokenBytes {
		return "", ErrInvalidAccountInventoryCursor
	}
	return token, nil
}

func (codec *AccountInventoryCursorCodec) Decode(
	token string,
	actorAdminID, instanceID uuid.UUID,
	filters AccountInventoryCursorFilters,
) (string, error) {
	if codec == nil || token == "" || len(token) > accountInventoryCursorMaxTokenBytes ||
		actorAdminID == uuid.Nil || instanceID == uuid.Nil || !validAccountInventoryCursorFilters(filters) {
		return "", ErrInvalidAccountInventoryCursor
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(raw) < accountInventoryCursorHeaderBytes+12+16 || len(raw) > accountInventoryCursorMaxTokenBytes {
		return "", ErrInvalidAccountInventoryCursor
	}
	if int(raw[0]) != accountInventoryCursorVersion {
		return "", ErrInvalidAccountInventoryCursor
	}
	keyVersion := authn.KeyVersion(binary.BigEndian.Uint32(raw[1:accountInventoryCursorHeaderBytes]))
	if keyVersion == 0 {
		return "", ErrInvalidAccountInventoryCursor
	}
	key, err := codec.keyring.Derive(keyVersion, authn.DomainAccountInventoryCursorEncryption, 32)
	if err != nil {
		return "", ErrInvalidAccountInventoryCursor
	}
	defer clear(key)
	aead, err := accountInventoryCursorAEAD(key)
	if err != nil || len(raw) < accountInventoryCursorHeaderBytes+aead.NonceSize()+aead.Overhead() {
		return "", ErrInvalidAccountInventoryCursor
	}
	nonce := raw[accountInventoryCursorHeaderBytes : accountInventoryCursorHeaderBytes+aead.NonceSize()]
	ciphertext := raw[accountInventoryCursorHeaderBytes+aead.NonceSize():]
	if len(ciphertext) > accountInventoryCursorMaxPlaintext+aead.Overhead() {
		return "", ErrInvalidAccountInventoryCursor
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, accountInventoryCursorAAD(codec.keyring.Environment(), keyVersion))
	if err != nil || len(plaintext) == 0 || len(plaintext) > accountInventoryCursorMaxPlaintext {
		return "", ErrInvalidAccountInventoryCursor
	}
	defer clear(plaintext)
	var payload accountInventoryCursorPayload
	nowUnix := codec.now().UTC().Unix()
	if !decodeCanonicalAccountInventoryCursorJSON(plaintext, &payload) ||
		payload.Version != accountInventoryCursorVersion || payload.KeyVersion != uint32(keyVersion) ||
		payload.ActorAdminID != actorAdminID.String() || payload.InstanceID != instanceID.String() ||
		payload.FiltersHash != accountInventoryCursorFilterHash(filters) ||
		payload.IssuedAt <= 0 || payload.IssuedAt > math.MaxInt64-int64(accountInventoryCursorTTL/time.Second) ||
		payload.ExpiresAt != payload.IssuedAt+int64(accountInventoryCursorTTL/time.Second) ||
		nowUnix < payload.IssuedAt || nowUnix >= payload.ExpiresAt ||
		!validAccountInventoryCursorKey(payload.AfterAccountKey, filters) {
		return "", ErrInvalidAccountInventoryCursor
	}
	return payload.AfterAccountKey, nil
}

func accountInventoryCursorAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func accountInventoryCursorAAD(environment authn.Environment, keyVersion authn.KeyVersion) []byte {
	return []byte(fmt.Sprintf(
		"relay-station-control/account-inventory-cursor/v1/environment/%s/key/%d",
		environment, keyVersion,
	))
}

func accountInventoryCursorFilterHash(filters AccountInventoryCursorFilters) string {
	normalized := normalizeAccountInventoryCursorFilters(filters)
	raw, _ := json.Marshal(normalized)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func normalizeAccountInventoryCursorFilters(filters AccountInventoryCursorFilters) normalizedAccountInventoryCursorFilters {
	return normalizedAccountInventoryCursorFilters{
		Provider:    strings.ToLower(strings.TrimSpace(filters.Provider)),
		Lifecycle:   strings.ToLower(strings.TrimSpace(filters.Lifecycle)),
		BasicStatus: strings.ToLower(strings.TrimSpace(filters.BasicStatus)),
		Email:       strings.ToLower(strings.TrimSpace(filters.Email)),
		Window:      strings.ToLower(strings.TrimSpace(filters.Window)),
		Quality:     strings.ToLower(strings.TrimSpace(filters.Quality)),
		Scope:       strings.ToLower(strings.TrimSpace(filters.Scope)),
	}
}

func validAccountInventoryCursorFilters(filters AccountInventoryCursorFilters) bool {
	normalized := normalizeAccountInventoryCursorFilters(filters)
	if normalized.Provider != "" && !validProviderName(normalized.Provider) {
		return false
	}
	switch normalized.Lifecycle {
	case "", string(AccountInventoryPresent), string(AccountInventorySuspectedMissing),
		string(AccountInventoryMissing), string(AccountInventoryOutOfScope):
	default:
		return false
	}
	switch normalized.BasicStatus {
	case "", "reported_active", "disabled", "unavailable", "error", "unknown":
	default:
		return false
	}
	if normalized.Window != "" && normalized.Window != "15m" && normalized.Window != "1h" {
		return false
	}
	if normalized.Quality != "" && normalized.Quality != "good" && normalized.Quality != "degraded" && normalized.Quality != "bad" && normalized.Quality != "unknown" {
		return false
	}
	if normalized.Scope != "" && normalized.Scope != "node-account-quality" {
		return false
	}
	return normalized.Email == "" || validAccountInventoryCursorIdentity(normalized.Email, 320)
}

func validAccountInventoryCursorKey(accountKey string, filters AccountInventoryCursorFilters) bool {
	if len(accountKey) < 3 || len(accountKey) > accountInventoryCursorMaxKeyBytes || !utf8.ValidString(accountKey) ||
		accountKey != strings.ToLower(strings.TrimSpace(accountKey)) {
		return false
	}
	provider, email, found := strings.Cut(accountKey, ":")
	if !found || !validProviderName(provider) || !validAccountInventoryCursorIdentity(email, 320) {
		return false
	}
	normalized := normalizeAccountInventoryCursorFilters(filters)
	return (normalized.Provider == "" || provider == normalized.Provider) &&
		(normalized.Email == "" || email == normalized.Email)
}

func validAccountInventoryCursorIdentity(value string, maximum int) bool {
	if !utf8.ValidString(value) || len(value) == 0 || len(value) > maximum ||
		value != strings.ToLower(strings.TrimSpace(value)) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func decodeCanonicalAccountInventoryCursorJSON(raw []byte, destination any) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return false
	}
	canonical, err := marshalCanonicalAccountInventoryCursorJSON(destination)
	return err == nil && bytes.Equal(raw, canonical)
}

func marshalCanonicalAccountInventoryCursorJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'}), nil
}
