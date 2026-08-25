package auth

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestSensitiveRedactionAcrossTextAndStructuredLogs(t *testing.T) {
	canary := "canary-TOTP-session-recovery-secret"
	if got := fmt.Sprintf("%s", Sensitive(canary)); got != Redacted {
		t.Fatalf("Sensitive.String() = %q", got)
	}
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	logger.Info("authentication", "credential", Sensitive(canary))
	redacted := RedactText("response trace metric "+canary, canary)
	combined := output.String() + redacted
	if strings.Contains(combined, canary) {
		t.Fatal("canary secret leaked through redaction helpers")
	}
	if !strings.Contains(combined, Redacted) {
		t.Fatal("redaction marker missing")
	}
}

func TestAuditDetailsAllowlist(t *testing.T) {
	details, err := SanitizeAuditDetails(AuditSessionCreate, map[string]any{"mfa_method": string(MFAMethodTOTP)})
	if err != nil || details["mfa_method"] != string(MFAMethodTOTP) {
		t.Fatalf("SanitizeAuditDetails() = %#v, %v", details, err)
	}
	blocked := []map[string]any{
		{"password": "canary"},
		{"session_token": "canary"},
		{"login_name": "operator"},
		{"source_ip": "198.51.100.8"},
		{"mfa_method": "attacker-controlled"},
	}
	for _, candidate := range blocked {
		if _, err := SanitizeAuditDetails(AuditSessionCreate, candidate); err == nil {
			t.Fatalf("audit details accepted %#v", candidate)
		}
	}
	original := map[string]any{"recovery_codes_remaining": 10}
	sanitized, err := SanitizeAuditDetails(AuditRecoveryCodesRegenerate, original)
	if err != nil {
		t.Fatal(err)
	}
	original["recovery_codes_remaining"] = 9
	if sanitized["recovery_codes_remaining"] != 10 {
		t.Fatal("sanitized audit details alias caller map")
	}
}
