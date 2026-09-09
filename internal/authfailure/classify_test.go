package authfailure

import (
	"strings"
	"testing"
)

func TestSafeClassifier(t *testing.T) {
	cases := []struct {
		raw    string
		status int64
		want   string
	}{
		{"invalid_grant", 0, "token_invalid"}, {"token_revoked", 0, "token_invalid"}, {"token_invalidated", 0, "token_invalid"},
		{"account_deactivated", 401, "account_blocked"}, {"account_disabled", 0, "account_blocked"}, {"account_suspended", 403, "account_blocked"}, {"account_blocked", 0, "account_blocked"},
		{`{"code":"invalid_grant","error":{"type":"account_blocked"}}`, 401, "account_blocked"},
		{`"{\"error\":{\"code\":\"invalid_grant\"}}"`, 0, "token_invalid"},
		{"", 401, "token_invalid"}, {"permission_denied", 403, "forbidden"}, {"", 0, "other"},
		{"account is not account_blocked", 0, "other"}, {"token_invalid", 0, "other"}, {`{"message":"account_blocked"}`, 0, "other"},
		{`{"reason":"account_blocked","status":"account_blocked","access_token":"token_revoked","refresh_token":"SECRET_CANARY"}`, 0, "other"},
		{strings.Repeat(" ", 8193) + "account_blocked", 0, "other"},
		{strings.Repeat(`{"error":`, 9) + `"account_blocked"` + strings.Repeat("}", 9), 0, "other"},
		{"quota_exceeded", 0, "other"}, {"ACCOUNT_BLOCKED", 0, "other"},
	}
	for _, tc := range cases {
		if got := Classify(tc.status, []byte(tc.raw)); got != tc.want {
			t.Errorf("status=%d got=%s want=%s", tc.status, got, tc.want)
		}
	}
}
