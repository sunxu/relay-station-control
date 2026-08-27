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

func TestSanitizeAccountInventoryViewDetails(t *testing.T) {
	details, err := SanitizeAuditDetails(AuditAccountInventoryView, map[string]any{
		"instance_id":              "4b58290d-3b20-4f45-a5c5-14a5aebfd3a6",
		"provider_filter_used":     true,
		"lifecycle_filter_used":    false,
		"basic_status_filter_used": true,
		"email_filter_used":        true,
		"cursor_used":              false,
		"result_count":             100,
	})
	if err != nil {
		t.Fatalf("SanitizeAuditDetails() error = %v", err)
	}
	if details["result_count"] != 100 || details["email_filter_used"] != true {
		t.Fatalf("SanitizeAuditDetails() = %#v", details)
	}
}

func TestSanitizeAccountInventoryViewDetailsRejectsIdentityAndUnboundedFields(t *testing.T) {
	for name, candidate := range map[string]map[string]any{
		"email":             {"email": "identity-canary@example.com"},
		"account key":       {"account_key": "provider:identity-canary@example.com"},
		"cursor":            {"cursor": "cursor-canary"},
		"filter hash":       {"filter_hash": "hash-canary"},
		"filter value":      {"provider": "provider-canary"},
		"nil instance":      {"instance_id": "00000000-0000-0000-0000-000000000000"},
		"noncanonical UUID": {"instance_id": "4B58290D-3B20-4F45-A5C5-14A5AEBFD3A6"},
		"count overflow":    {"result_count": 101},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := SanitizeAuditDetails(AuditAccountInventoryView, candidate); err == nil {
				t.Fatalf("account inventory audit accepted %#v", candidate)
			}
		})
	}
}
