package store

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	authn "github.com/sunxu/relay-station-control/internal/auth"
)

const (
	problemAccountCursorVersion       = 1
	problemAccountCursorMaxEncoded    = 2048
	problemAccountCursorMACDomain     = "relay-station-control/problems-v1"
	problemAccountCursorSortSemantics = "severity_desc,since_asc,email_asc,node_id_asc"
)

// ProblemAccountCursorCodec signs problem-account continuation state. The
// cursor is not an authorization credential; callers must authenticate and
// authorize every request before decoding it.
type ProblemAccountCursorCodec struct {
	keyring *authn.Keyring
}

type problemAccountCursorPayload struct {
	Version    int                     `json:"v"`
	KeyVersion uint32                  `json:"key_version"`
	Actor      string                  `json:"actor"`
	FiltersSHA string                  `json:"filters_sha"`
	Sort       string                  `json:"sort"`
	RowCursor  problemAccountRowCursor `json:"row_cursor"`
	Digest     string                  `json:"digest"`
}

type problemAccountRowCursor struct {
	Severity string    `json:"severity"`
	Since    time.Time `json:"since"`
	Email    string    `json:"email"`
	NodeID   uuid.UUID `json:"node_id"`
}

type problemAccountCursorFilterIdentity struct {
	Provider string `json:"provider"`
	NodeID   string `json:"node_id"`
	Severity string `json:"severity"`
	Reason   string `json:"reason"`
	Email    string `json:"email"`
}

func NewProblemAccountCursorCodec(keyring *authn.Keyring) (*ProblemAccountCursorCodec, error) {
	if keyring == nil {
		return nil, ErrInvalidProblemAccountQuery
	}
	if _, _, err := keyring.DeriveCurrent(authn.DomainAssetCursorDigest, sha256.Size); err != nil {
		return nil, ErrInvalidProblemAccountQuery
	}
	return &ProblemAccountCursorCodec{keyring: keyring}, nil
}

func (codec *ProblemAccountCursorCodec) Encode(actor uuid.UUID, filters ProblemAccountFilters, cursor ProblemAccountCursor) (string, error) {
	cursor.Since = cursor.Since.UTC()
	if codec == nil || codec.keyring == nil || actor == uuid.Nil || !validProblemAccountCursorFilters(filters) || !validProblemAccountCursor(cursor) {
		return "", ErrInvalidProblemAccountQuery
	}
	keyVersion, key, err := codec.keyring.DeriveCurrent(authn.DomainAssetCursorDigest, sha256.Size)
	if err != nil {
		return "", ErrInvalidProblemAccountQuery
	}
	payload := problemAccountCursorPayload{
		Version: problemAccountCursorVersion, KeyVersion: uint32(keyVersion),
		Actor: actor.String(), FiltersSHA: problemAccountFilterHash(filters),
		Sort: problemAccountCursorSortSemantics, RowCursor: problemAccountRowCursorFrom(canonicalProblemAccountCursor(cursor)),
	}
	payload.Digest = hex.EncodeToString(problemAccountCursorDigest(key, payload))
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", ErrInvalidProblemAccountQuery
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	if len(encoded) > problemAccountCursorMaxEncoded {
		return "", ErrInvalidProblemAccountQuery
	}
	return encoded, nil
}

func (codec *ProblemAccountCursorCodec) Decode(encoded string, actor uuid.UUID, filters ProblemAccountFilters) (ProblemAccountCursor, error) {
	if codec == nil || codec.keyring == nil || actor == uuid.Nil || encoded == "" || len(encoded) > problemAccountCursorMaxEncoded || !validProblemAccountCursorFilters(filters) {
		return ProblemAccountCursor{}, ErrInvalidProblemAccountQuery
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return ProblemAccountCursor{}, ErrInvalidProblemAccountQuery
	}
	var payload problemAccountCursorPayload
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return ProblemAccountCursor{}, ErrInvalidProblemAccountQuery
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return ProblemAccountCursor{}, ErrInvalidProblemAccountQuery
	}
	key, err := codec.keyring.Derive(authn.KeyVersion(payload.KeyVersion), authn.DomainAssetCursorDigest, sha256.Size)
	if err != nil {
		return ProblemAccountCursor{}, ErrInvalidProblemAccountQuery
	}
	provided, err := hex.DecodeString(payload.Digest)
	if err != nil || payload.Version != problemAccountCursorVersion ||
		payload.Actor != actor.String() || payload.FiltersSHA != problemAccountFilterHash(filters) ||
		payload.Sort != problemAccountCursorSortSemantics ||
		!hmac.Equal(provided, problemAccountCursorDigest(key, payload)) ||
		!validProblemAccountRowCursor(payload.RowCursor) {
		return ProblemAccountCursor{}, ErrInvalidProblemAccountQuery
	}
	return problemAccountCursorFrom(payload.RowCursor), nil
}

func validProblemAccountCursorFilters(filters ProblemAccountFilters) bool {
	identity := normalizeProblemAccountFilters(filters)
	if identity.Provider != "" && !validProviderName(identity.Provider) {
		return false
	}
	if identity.Severity != "" && !validProblemAccountSeverity(identity.Severity) {
		return false
	}
	if identity.Reason != "" && !validProblemAccountText(identity.Reason, 64) {
		return false
	}
	return identity.Email == "" || validProblemAccountCursorEmail(identity.Email)
}

func validProblemAccountCursor(cursor ProblemAccountCursor) bool {
	return cursor.NodeID != uuid.Nil && validProblemAccountSeverity(cursor.Severity) &&
		validProblemAccountCursorEmail(cursor.Email) && validProblemAccountCanonicalUTC(cursor.Since)
}

func validProblemAccountRowCursor(cursor problemAccountRowCursor) bool {
	return validProblemAccountCursor(problemAccountCursorFrom(cursor))
}

func validProblemAccountSeverity(value string) bool {
	return value == "Critical" || value == "Warning"
}

func validProblemAccountCursorEmail(value string) bool {
	return value != "" && value == strings.ToLower(strings.TrimSpace(value)) &&
		validProblemAccountText(value, 320)
}

func validProblemAccountText(value string, maximum int) bool {
	if !utf8.ValidString(value) || value == "" || len(value) > maximum {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validProblemAccountCanonicalUTC(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC && value.Format(time.RFC3339Nano) == value.UTC().Format(time.RFC3339Nano)
}

func canonicalProblemAccountCursor(cursor ProblemAccountCursor) ProblemAccountCursor {
	cursor.Since = cursor.Since.UTC()
	cursor.Email = strings.ToLower(strings.TrimSpace(cursor.Email))
	return cursor
}

func problemAccountRowCursorFrom(cursor ProblemAccountCursor) problemAccountRowCursor {
	return problemAccountRowCursor{Severity: cursor.Severity, Since: cursor.Since, Email: cursor.Email, NodeID: cursor.NodeID}
}

func problemAccountCursorFrom(cursor problemAccountRowCursor) ProblemAccountCursor {
	return ProblemAccountCursor{Severity: cursor.Severity, Since: cursor.Since, Email: cursor.Email, NodeID: cursor.NodeID}
}

func normalizeProblemAccountFilters(filters ProblemAccountFilters) problemAccountCursorFilterIdentity {
	nodeID := ""
	if filters.NodeID != uuid.Nil {
		nodeID = filters.NodeID.String()
	}
	return problemAccountCursorFilterIdentity{
		Provider: strings.ToLower(strings.TrimSpace(filters.Provider)), NodeID: nodeID,
		Severity: strings.TrimSpace(filters.Severity), Reason: strings.ToLower(strings.TrimSpace(filters.Reason)),
		Email: strings.ToLower(strings.TrimSpace(filters.Email)),
	}
}

func problemAccountFilterHash(filters ProblemAccountFilters) string {
	raw, _ := json.Marshal(normalizeProblemAccountFilters(filters))
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func problemAccountCursorDigest(key []byte, payload problemAccountCursorPayload) []byte {
	raw, _ := json.Marshal(struct {
		Domain     string                  `json:"domain"`
		Version    int                     `json:"v"`
		KeyVersion uint32                  `json:"key_version"`
		Actor      string                  `json:"actor"`
		FiltersSHA string                  `json:"filters_sha"`
		Sort       string                  `json:"sort"`
		RowCursor  problemAccountRowCursor `json:"row_cursor"`
	}{problemAccountCursorMACDomain, payload.Version, payload.KeyVersion, payload.Actor, payload.FiltersSHA, payload.Sort, payload.RowCursor})
	digest := hmac.New(sha256.New, key)
	_, _ = digest.Write(raw)
	return digest.Sum(nil)
}
