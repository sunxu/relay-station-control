package gatewaydirectory

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	rootdrivers "github.com/sunxu/relay-station-control/internal/drivers"
)

const (
	FingerprintEncodingVersion = 1
	MaxAccounts                = 10_000
	MaxGeneratedAtSkewFuture   = 30 * time.Second
	MaxSourceAge               = 24 * time.Hour
	AllowedBackwardSkew        = 5 * time.Minute
)

var ErrSourceTimeInvalid = errors.New("gatewaydirectory: source time invalid")

// DirectoryResponse is a normalized, validated snapshot of the Gateway API
// Account Directory.
type DirectoryResponse struct {
	SchemaVersion int
	GeneratedAt   time.Time
	Accounts      []Account
}

type Account struct {
	ID       int64
	Name     string
	Platform string
	Type     string
	URL      *string
	Status   string
}

type SourceTimePolicy struct {
	FutureTolerance   time.Duration
	MaximumSourceAge  time.Duration
	BackwardTolerance time.Duration
}

func DefaultSourceTimePolicy() SourceTimePolicy {
	return SourceTimePolicy{
		FutureTolerance:   MaxGeneratedAtSkewFuture,
		MaximumSourceAge:  MaxSourceAge,
		BackwardTolerance: AllowedBackwardSkew,
	}
}

func ValidateSourceTime(policy SourceTimePolicy, generatedAt, receivedAt time.Time, previousSuccessfulGeneratedAt *time.Time) error {
	if policy.FutureTolerance <= 0 {
		policy.FutureTolerance = MaxGeneratedAtSkewFuture
	}
	if policy.MaximumSourceAge <= 0 {
		policy.MaximumSourceAge = MaxSourceAge
	}
	if policy.BackwardTolerance <= 0 {
		policy.BackwardTolerance = AllowedBackwardSkew
	}
	if generatedAt.IsZero() || receivedAt.IsZero() {
		return ErrSourceTimeInvalid
	}
	if generatedAt.After(receivedAt.Add(policy.FutureTolerance)) {
		return ErrSourceTimeInvalid
	}
	if receivedAt.Sub(generatedAt) > policy.MaximumSourceAge {
		return ErrSourceTimeInvalid
	}
	if previousSuccessfulGeneratedAt != nil && !previousSuccessfulGeneratedAt.IsZero() {
		previous := previousSuccessfulGeneratedAt.Add(-policy.BackwardTolerance)
		if generatedAt.Before(previous) {
			return ErrSourceTimeInvalid
		}
	}
	return nil
}

func ParseDirectoryResponse(encoded []byte) (DirectoryResponse, [sha256.Size]byte, error) {
	return parseDirectoryResponse(bytes.NewReader(encoded), int64(len(encoded)))
}

func parseDirectoryResponse(body io.Reader, maxBodyBytes int64) (DirectoryResponse, [sha256.Size]byte, error) {
	if maxBodyBytes <= 0 {
		maxBodyBytes = DefaultBodyLimitBytes
	}
	if maxBodyBytes > DefaultBodyLimitBytes {
		maxBodyBytes = DefaultBodyLimitBytes
	}
	encoded, err := readBounded(body, maxBodyBytes)
	if err != nil {
		return DirectoryResponse{}, [sha256.Size]byte{}, err
	}
	if !utf8.Valid(encoded) {
		return DirectoryResponse{}, [sha256.Size]byte{}, &FetchError{Reason: rootdrivers.ReasonContractInvalid, Retryable: false}
	}
	var raw rawDirectoryResponse
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return DirectoryResponse{}, [sha256.Size]byte{}, &FetchError{
			Reason:    rootdrivers.ReasonContractInvalid,
			Retryable: false,
			Err:       err,
		}
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return DirectoryResponse{}, [sha256.Size]byte{}, &FetchError{
			Reason:    rootdrivers.ReasonContractInvalid,
			Retryable: false,
			Err:       err,
		}
	}
	response, err := normalizeRawDirectoryResponse(&raw)
	if err != nil {
		return DirectoryResponse{}, [sha256.Size]byte{}, err
	}
	return response, response.FingerprintV1(), nil
}

func normalizeRawDirectoryResponse(raw *rawDirectoryResponse) (DirectoryResponse, error) {
	if raw == nil || raw.SchemaVersion == nil || raw.GeneratedAt == nil || raw.Accounts == nil {
		return DirectoryResponse{}, &FetchError{Reason: rootdrivers.ReasonContractInvalid, Retryable: false}
	}
	if *raw.SchemaVersion != 1 {
		return DirectoryResponse{}, &FetchError{Reason: rootdrivers.ReasonContractInvalid, Retryable: false}
	}
	generatedAt, err := time.Parse(time.RFC3339Nano, *raw.GeneratedAt)
	if err != nil {
		return DirectoryResponse{}, &FetchError{Reason: rootdrivers.ReasonContractInvalid, Retryable: false, Err: err}
	}
	accounts := *raw.Accounts
	if len(accounts) > MaxAccounts {
		return DirectoryResponse{}, &FetchError{Reason: rootdrivers.ReasonRecordLimit, Retryable: false}
	}
	result := DirectoryResponse{
		SchemaVersion: *raw.SchemaVersion,
		GeneratedAt:   generatedAt,
		Accounts:      make([]Account, 0, len(accounts)),
	}
	var previousID int64
	for index, account := range accounts {
		normalized, err := normalizeRawAccount(account)
		if err != nil {
			return DirectoryResponse{}, err
		}
		if normalized.ID <= 0 {
			return DirectoryResponse{}, &FetchError{Reason: rootdrivers.ReasonContractInvalid, Retryable: false}
		}
		if index > 0 && normalized.ID <= previousID {
			return DirectoryResponse{}, &FetchError{Reason: rootdrivers.ReasonContractInvalid, Retryable: false}
		}
		previousID = normalized.ID
		result.Accounts = append(result.Accounts, normalized)
	}
	return result, nil
}

func normalizeRawAccount(raw rawAccount) (Account, error) {
	if raw.ID == nil || raw.Name == nil || raw.Platform == nil || raw.Type == nil || raw.Status == nil || raw.URL == nil {
		return Account{}, &FetchError{Reason: rootdrivers.ReasonContractInvalid, Retryable: false}
	}
	if *raw.Type != "apikey" && *raw.Type != "upstream" {
		return Account{}, &FetchError{Reason: rootdrivers.ReasonContractInvalid, Retryable: false}
	}
	normalizedURL, err := validateCanonicalOriginURL(raw.URL)
	if err != nil {
		return Account{}, err
	}
	return Account{
		ID:       *raw.ID,
		Name:     *raw.Name,
		Platform: *raw.Platform,
		Type:     *raw.Type,
		URL:      normalizedURL,
		Status:   *raw.Status,
	}, nil
}

func validateCanonicalOriginURL(raw json.RawMessage) (*string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, &FetchError{Reason: rootdrivers.ReasonContractInvalid, Retryable: false}
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var value string
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return nil, &FetchError{Reason: rootdrivers.ReasonContractInvalid, Retryable: false}
	}
	canonical, err := canonicalOriginString(value)
	if err != nil || canonical != value {
		return nil, &FetchError{Reason: rootdrivers.ReasonContractInvalid, Retryable: false}
	}
	return &value, nil
}

func canonicalOriginString(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("gatewaydirectory: invalid url")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || parsed.Opaque != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("gatewaydirectory: invalid url")
	}
	if parsed.Path != "" || parsed.RawPath != "" {
		return "", errors.New("gatewaydirectory: invalid url")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errors.New("gatewaydirectory: invalid url")
	}
	host := parsed.Hostname()
	if host == "" {
		return "", errors.New("gatewaydirectory: invalid url")
	}
	canonicalHost, err := canonicalHost(host)
	if err != nil {
		return "", err
	}
	port := parsed.Port()
	if port != "" {
		portValue, parseErr := strconv.ParseUint(port, 10, 16)
		if parseErr != nil || portValue == 0 {
			return "", errors.New("gatewaydirectory: invalid url")
		}
		port = strconv.FormatUint(portValue, 10)
	}
	result := scheme + "://" + canonicalHost
	if port != "" {
		result += ":" + port
	}
	return result, nil
}

func canonicalHost(host string) (string, error) {
	if addr, err := netip.ParseAddr(host); err == nil {
		if addr.Zone() != "" || addr.Is4In6() {
			return "", errors.New("gatewaydirectory: invalid url")
		}
		if addr.Is6() {
			return "[" + strings.ToLower(addr.String()) + "]", nil
		}
		return addr.String(), nil
	}
	if host != strings.ToLower(host) {
		return "", errors.New("gatewaydirectory: invalid url")
	}
	return host, nil
}

func (r DirectoryResponse) CanonicalPreimageV1() ([]byte, error) {
	var buffer bytes.Buffer
	buffer.Grow(64 + len(r.Accounts)*64)
	buffer.WriteByte('[')
	buffer.WriteString("1,")
	buffer.WriteString(strconv.Itoa(r.SchemaVersion))
	buffer.WriteString(",[")
	for index, account := range r.Accounts {
		if index > 0 {
			buffer.WriteByte(',')
		}
		buffer.WriteByte('[')
		buffer.WriteString(strconv.FormatInt(account.ID, 10))
		buffer.WriteByte(',')
		if err := writeJSONString(&buffer, account.Name); err != nil {
			return nil, err
		}
		buffer.WriteByte(',')
		if err := writeJSONString(&buffer, account.Platform); err != nil {
			return nil, err
		}
		buffer.WriteByte(',')
		if err := writeJSONString(&buffer, account.Type); err != nil {
			return nil, err
		}
		buffer.WriteByte(',')
		if account.URL == nil {
			buffer.WriteString("null")
		} else if err := writeJSONString(&buffer, *account.URL); err != nil {
			return nil, err
		}
		buffer.WriteByte(',')
		if err := writeJSONString(&buffer, account.Status); err != nil {
			return nil, err
		}
		buffer.WriteByte(']')
	}
	buffer.WriteString("]]")
	return buffer.Bytes(), nil
}

func (r DirectoryResponse) FingerprintV1() [sha256.Size]byte {
	preimage, err := r.CanonicalPreimageV1()
	if err != nil {
		return [sha256.Size]byte{}
	}
	return sha256.Sum256(preimage)
}

func writeJSONString(buffer *bytes.Buffer, value string) error {
	if !utf8.ValidString(value) {
		return errors.New("gatewaydirectory: invalid utf-8")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	buffer.Write(encoded)
	return nil
}

type rawDirectoryResponse struct {
	SchemaVersion *int          `json:"schema_version"`
	GeneratedAt   *string       `json:"generated_at"`
	Accounts      *[]rawAccount `json:"accounts"`
}

type rawAccount struct {
	ID       *int64          `json:"id"`
	Name     *string         `json:"name"`
	Platform *string         `json:"platform"`
	Type     *string         `json:"type"`
	URL      json.RawMessage `json:"url"`
	Status   *string         `json:"status"`
}

func readBounded(body io.Reader, maxBytes int64) ([]byte, error) {
	if body == nil {
		return nil, &FetchError{Reason: rootdrivers.ReasonResponseInvalid, Retryable: false}
	}
	if maxBytes <= 0 {
		maxBytes = DefaultBodyLimitBytes
	}
	encoded, err := io.ReadAll(io.LimitReader(body, maxBytes+1))
	if err != nil {
		return nil, &FetchError{Reason: rootdrivers.ReasonPartialRead, Retryable: true, Err: err}
	}
	if int64(len(encoded)) > maxBytes {
		return nil, &FetchError{Reason: rootdrivers.ReasonResponseTooLarge, Retryable: false}
	}
	return encoded, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return &FetchError{Reason: rootdrivers.ReasonResponseInvalid, Retryable: false, Err: err}
	}
	return nil
}
