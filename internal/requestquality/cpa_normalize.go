// Portions adapted from CPA Manager Plus internal/usage/event.go, local revision 1ae656c8.
// Copyright (c) 2026 Seakee. MIT License; see CPA-MIT-LICENSE.txt.
package requestquality

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func readTimestamp(record map[string]any) (int64, string) {
	raw := first(record, "timestamp", "time", "created_at", "createdAt", "created", "request_time", "requestTime")

	if raw == nil {
		return 0, ""
	}
	if number, ok := raw.(json.Number); ok {
		raw = number.String()
	}
	switch value := raw.(type) {
	case float64:
		ms := int64(value)
		if ms < 10_000_000_000 {
			ms *= 1000
		}
		return ms, time.UnixMilli(ms).UTC().Format(time.RFC3339Nano)
	case string:
		trimmed := strings.TrimSpace(value)
		if number, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			if number < 10_000_000_000 {
				number *= 1000
			}
			return number, time.UnixMilli(number).UTC().Format(time.RFC3339Nano)
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
			if parsed, err := time.Parse(layout, trimmed); err == nil {
				return parsed.UnixMilli(), parsed.UTC().Format(time.RFC3339Nano)
			}
		}
	}
	return 0, ""
}

func readNestedThenTopInt(record map[string]any, keys []string) int64 {
	for _, parent := range []string{"tokens", "usage"} {
		if nested, ok := record[parent].(map[string]any); ok {
			if value := readFirstIntFrom(nested, keys...); value != 0 {
				return value
			}
		}
	}
	return readFirstIntFrom(record, keys...)
}

func readFailed(record map[string]any) bool {
	if value, ok := first(record, "failed", "is_failed", "isFailed").(bool); ok {
		return value
	}
	if value, ok := first(record, "success", "ok").(bool); ok {
		return !value
	}
	status := readInt(record, "status", "status_code", "statusCode", "http_status", "httpStatus")
	if status >= 400 {
		return true
	}
	return first(record, "error", "error_message", "errorMessage") != nil
}

func readFailFields(record map[string]any) (int64, string) {
	fail := map[string]any{}
	if nested, ok := first(record, "fail").(map[string]any); ok {
		fail = nested
	}
	statusCode := readIntFrom(fail, "status_code", "statusCode")
	if statusCode == 0 {
		statusCode = readInt(record, "fail_status_code", "failStatusCode")
	}
	body := readString(fail, "body")
	if body == "" {
		body = readString(record, "fail_body", "failBody")
	}
	return statusCode, body
}

func readOptionalInt(record map[string]any, keys ...string) *int64 {
	value := readInt(record, keys...)
	if value == 0 && first(record, keys...) == nil {
		return nil
	}
	return &value
}

func readString(record map[string]any, keys ...string) string {
	raw := first(record, keys...)
	if raw == nil {
		return ""
	}
	switch value := raw.(type) {
	case string:
		return strings.TrimSpace(value)
	case json.Number:
		return value.String()
	case float64:
		if value == float64(int64(value)) {
			return strconv.FormatInt(int64(value), 10)
		}
		return strconv.FormatFloat(value, 'f', -1, 64)
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func readInt(record map[string]any, keys ...string) int64 {
	return readIntFrom(record, keys...)
}

func readFirstIntFrom(record map[string]any, keys ...string) int64 {
	for _, key := range keys {
		value := readIntFrom(record, key)
		if value != 0 {
			return value
		}
	}
	return 0
}

func readIntFrom(record map[string]any, keys ...string) int64 {
	raw := first(record, keys...)
	switch value := raw.(type) {
	case float64:
		return int64(value)
	case int64:
		return value
	case int:
		return int64(value)
	case json.Number:
		number, _ := value.Int64()
		return number
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		return parsed
	default:
		return 0
	}
}

func first(record map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := record[key]; ok {
			return value
		}
	}
	return nil
}

func hashString(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(trimmed))
	return hex.EncodeToString(sum[:])
}
