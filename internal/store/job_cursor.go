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

	"github.com/google/uuid"

	authn "github.com/sunxu/relay-station-control/internal/auth"
)

const jobCursorVersion = 1

type jobCursorPayload struct {
	Version     int    `json:"v"`
	KeyVersion  uint32 `json:"key_version"`
	CreatedAt   string `json:"created_at"`
	JobID       string `json:"job_id"`
	FiltersHash string `json:"filters_hash"`
	Digest      string `json:"digest"`
}

type JobCursorCodec struct{ keyring *authn.Keyring }

func NewJobCursorCodec(keyring *authn.Keyring) (*JobCursorCodec, error) {
	if keyring == nil {
		return nil, ErrInvalidJobCursor
	}
	if _, _, err := keyring.DeriveCurrent(authn.DomainJobCursorDigest, sha256.Size); err != nil {
		return nil, ErrInvalidJobCursor
	}
	return &JobCursorCodec{keyring: keyring}, nil
}

func (codec *JobCursorCodec) Encode(filters JobListFilters, cursor JobCursor) (string, error) {
	if !validJobCursor(&cursor) {
		return "", ErrInvalidJobCursor
	}
	version, key, err := codec.keyring.DeriveCurrent(authn.DomainJobCursorDigest, sha256.Size)
	if err != nil {
		return "", ErrInvalidJobCursor
	}
	payload := jobCursorPayload{
		Version: jobCursorVersion, KeyVersion: uint32(version),
		CreatedAt: cursor.CreatedAt.UTC().Format(time.RFC3339Nano), JobID: cursor.JobID.String(),
		FiltersHash: jobFilterHash(filters),
	}
	payload.Digest = hex.EncodeToString(jobCursorDigest(key, payload))
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", ErrInvalidJobCursor
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (codec *JobCursorCodec) Decode(encoded string, filters JobListFilters) (JobCursor, error) {
	if encoded == "" || len(encoded) > 512 {
		return JobCursor{}, ErrInvalidJobCursor
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return JobCursor{}, ErrInvalidJobCursor
	}
	var payload jobCursorPayload
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return JobCursor{}, ErrInvalidJobCursor
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return JobCursor{}, ErrInvalidJobCursor
	}
	key, err := codec.keyring.Derive(authn.KeyVersion(payload.KeyVersion), authn.DomainJobCursorDigest, sha256.Size)
	if err != nil {
		return JobCursor{}, ErrInvalidJobCursor
	}
	provided, err := hex.DecodeString(payload.Digest)
	if err != nil || payload.Version != jobCursorVersion || payload.FiltersHash != jobFilterHash(filters) ||
		!hmac.Equal(provided, jobCursorDigest(key, payload)) {
		return JobCursor{}, ErrInvalidJobCursor
	}
	createdAt, err := time.Parse(time.RFC3339Nano, payload.CreatedAt)
	if err != nil || payload.CreatedAt != createdAt.UTC().Format(time.RFC3339Nano) {
		return JobCursor{}, ErrInvalidJobCursor
	}
	jobID, err := uuid.Parse(payload.JobID)
	if err != nil || jobID == uuid.Nil {
		return JobCursor{}, ErrInvalidJobCursor
	}
	return JobCursor{CreatedAt: createdAt.UTC(), JobID: jobID}, nil
}

func jobFilterHash(filters JobListFilters) string {
	formatTime := func(value *time.Time) string {
		if value == nil {
			return ""
		}
		return value.UTC().Format(time.RFC3339Nano)
	}
	raw, _ := json.Marshal(struct {
		JobKind     string `json:"job_kind"`
		Status      string `json:"status"`
		CreatedFrom string `json:"created_from"`
		CreatedTo   string `json:"created_to"`
	}{strings.TrimSpace(filters.JobKind), strings.TrimSpace(filters.Status), formatTime(filters.CreatedFrom), formatTime(filters.CreatedTo)})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func jobCursorDigest(key []byte, payload jobCursorPayload) []byte {
	raw, _ := json.Marshal(struct {
		Domain      string `json:"domain"`
		Version     int    `json:"v"`
		KeyVersion  uint32 `json:"key_version"`
		CreatedAt   string `json:"created_at"`
		JobID       string `json:"job_id"`
		FiltersHash string `json:"filters_hash"`
	}{"relay-control-job-cursor-v1", payload.Version, payload.KeyVersion, payload.CreatedAt, payload.JobID, payload.FiltersHash})
	digest := hmac.New(sha256.New, key)
	_, _ = digest.Write(raw)
	return digest.Sum(nil)
}
