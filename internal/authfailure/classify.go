// Package authfailure extracts only the approved, bounded authentication reason
// enum. Unknown fields, credentials and raw error text never leave this boundary.
package authfailure

import (
	"encoding/json"
	"strings"
)

func Classify(status int64, raw []byte) string {
	marker := parse(raw, 0)
	if marker == "account_blocked" {
		return marker
	}
	if marker == "token_invalid" || status == 401 {
		return "token_invalid"
	}
	if status == 403 {
		return "forbidden"
	}
	return "other"
}

func parse(raw []byte, depth int) string {
	if len(raw) > 8192 || len(raw) == 0 || depth > 8 {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		if result := identifier(value); result != "" {
			return result
		}
		// status_message/fail_summary may contain an encoded error JSON object.
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "{") {
			return parse([]byte(value), depth+1)
		}
		return ""
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return identifier(string(raw))
	}
	result := ""
	for _, key := range []string{"error", "code", "type"} {
		candidate := parse(object[key], depth+1)
		if candidate == "account_blocked" {
			return candidate
		}
		if candidate == "token_invalid" {
			result = candidate
		}
	}
	return result
}
func identifier(value string) string {
	switch strings.TrimSpace(value) {
	case "account_deactivated", "account_disabled", "account_suspended", "account_blocked":
		return "account_blocked"
	case "invalid_grant", "token_revoked", "token_invalidated":
		return "token_invalid"
	default:
		return ""
	}
}
