// CPA-compatible minimal projection; parsing helpers and hash field ordering
// follow local CPA Manager Plus usage/event.go (see CPA-MIT-LICENSE.txt).
package requestquality

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/sunxu/relay-station-control/internal/inventorypoll"
)

var ErrMalformedEvent = errors.New("malformed usage event")

// Normalize preserves unproven identity as nil. Current lookup never changes
// content hash, so identity evidence refresh cannot manufacture a new event.
func Normalize(nodeID uuid.UUID, raw []byte, identities []Identity) (Event, error) {
	if nodeID == uuid.Nil {
		return Event{}, ErrMalformedEvent
	}
	var record map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&record); err != nil || record == nil {
		return Event{}, ErrMalformedEvent
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return Event{}, ErrMalformedEvent
	}
	_, stamp := readTimestamp(record)
	occurred, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil || occurred.Year() < 1 || occurred.Year() > 9999 {
		return Event{}, ErrMalformedEvent
	}
	duration := readOptionalInt(record, "latency_ms", "latencyMs", "duration_ms", "durationMs", "elapsed_ms", "elapsedMs")
	if duration != nil && *duration < 0 {
		return Event{}, ErrMalformedEvent
	}
	provider := readString(record, "provider", "type", "auth_type", "authType")
	source := readString(record, "source", "api_key", "apiKey", "key", "account", "email")
	authIndex := readString(record, "auth_index", "authIndex", "AuthIndex")
	provider, key := resolveIdentity(record, provider, authIndex, identities)
	model := readString(record, "alias", "requested_model", "requestedModel")
	if model == "" {
		model = readString(record, "resolved_model", "resolvedModel", "model", "model_name", "modelName")
	}
	if model == "" {
		model = "-"
	}
	endpoint := readString(record, "endpoint", "api", "request", "operation")
	method := strings.ToUpper(readString(record, "method", "http_method", "httpMethod"))
	path := readString(record, "path", "url_path", "urlPath", "route")
	if endpoint == "" && method != "" && path != "" {
		endpoint = method + " " + path
	}
	if endpoint == "" {
		endpoint = "-"
	}
	failed := readFailed(record)
	status, body := readFailFields(record)
	if status == 0 {
		status = readInt(record, "status", "status_code", "statusCode", "http_status", "httpStatus")
	}
	summary := readString(record, "fail_summary", "failSummary")
	if summary == "" {
		summary = body
	}
	if summary == "" {
		summary = readString(record, "error", "error_message", "errorMessage")
	}
	var class *string
	if failed {
		v := classifyFailure(status, summary)
		class = &v
	}
	requestID := readString(record, "request_id", "requestId", "id")
	// This is CPA buildEventHash's normalized content identity, not a raw JSON hash
	// or a uniqueness claim for request_id. Tokens are hash inputs only, never DB columns.
	parts := []string{requestID, stamp, endpoint, model, authIndex, hashString(source),
		strconv.FormatInt(readNestedThenTopInt(record, []string{"input_tokens", "inputTokens", "prompt_tokens", "promptTokens"}), 10),
		strconv.FormatInt(readNestedThenTopInt(record, []string{"output_tokens", "outputTokens", "completion_tokens", "completionTokens"}), 10),
		strconv.FormatInt(readNestedThenTopInt(record, []string{"reasoning_tokens", "reasoningTokens"}), 10),
		strconv.FormatInt(max(readNestedThenTopInt(record, []string{"cached_tokens", "cachedTokens"}), readNestedThenTopInt(record, []string{"cache_tokens", "cacheTokens"})), 10),
		strconv.FormatBool(failed)}
	if duration != nil {
		parts = append(parts, strconv.FormatInt(*duration, 10))
	}
	event := Event{EventHash: hashString(strings.Join(parts, "|")), RequestID: requestID, NodeID: nodeID, Provider: provider, AccountKey: key, Model: model, OccurredAt: occurred.UTC(), DurationMS: duration, Success: !failed, FailureClass: class}
	// Invalid PostgreSQL text must not poison retries of all valid events in the batch.
	for _, v := range []string{event.RequestID, event.Provider, event.Model} {
		if !utf8.ValidString(v) || strings.ContainsRune(v, 0) {
			return Event{}, ErrMalformedEvent
		}
	}
	return event, nil
}

func canonical(provider, email string) (string, string) {
	p, _, key, err := inventorypoll.NormalizeAccountIdentity(provider, email)
	if err != nil {
		return "", ""
	}
	return p, key
}

func resolveIdentity(record map[string]any, rawProvider, index string, identities []Identity) (string, *string) {
	provider, _ := canonical(rawProvider, "identity-validation")
	snapshotProvider := readString(record, "auth_provider_snapshot", "authProviderSnapshot")
	sp, _ := canonical(snapshotProvider, "identity-validation")
	conflict := provider != "" && sp != "" && provider != sp
	if provider == "" {
		provider = sp
	}
	direct := ""
	for _, field := range []string{"email", "account_email", "accountEmail", "account", "account_snapshot", "accountSnapshot", "source"} {
		value, ok := record[field].(string)
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		if field != "email" && field != "account_email" && field != "accountEmail" {
			address, err := mail.ParseAddress(strings.TrimSpace(value))
			if err != nil || address.Address != strings.TrimSpace(value) {
				continue
			}
		}
		_, key := canonical(provider, value)
		if key == "" {
			continue
		}
		if direct != "" && direct != key {
			conflict = true
		}
		direct = key
	}
	matches := map[string]string{}
	invalidMatch := false
	if index != "" {
		for _, entry := range identities {
			if strings.TrimSpace(entry.AuthIndex) != index {
				continue
			}
			p, key := canonical(entry.Provider, entry.Email)
			if key == "" {
				invalidMatch = true
				continue
			}
			matches[key] = p
		}
	}
	if len(matches) > 1 || invalidMatch {
		conflict = true
	}
	for key, p := range matches {
		if provider != "" && provider != p {
			conflict = true
		}
		if direct != "" && direct != key {
			conflict = true
		}
	}
	if provider == "" && len(matches) == 1 {
		for _, p := range matches {
			provider = p
		}
	}
	if provider == "" {
		provider = "unknown"
	}
	if conflict {
		return provider, nil
	}
	if direct != "" {
		return provider, &direct
	}
	if len(matches) == 1 {
		for key := range matches {
			return provider, &key
		}
	}
	return provider, nil
}

func classifyFailure(status int64, summary string) string {
	summary = strings.ToLower(summary)
	if status == 401 || status == 403 || strings.Contains(summary, "token_revoked") || strings.Contains(summary, "token_invalidated") || strings.Contains(summary, "account_deactivated") {
		return "auth"
	}
	if strings.Contains(summary, "insufficient_quota") || strings.Contains(summary, "quota_exceeded") || strings.Contains(summary, "quota exceeded") {
		return "quota"
	}
	if status == 429 {
		return "rate_limit"
	}
	if status >= 500 && status <= 599 {
		return "upstream"
	}
	return "unknown"
}
