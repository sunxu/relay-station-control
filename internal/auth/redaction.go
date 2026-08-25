package auth

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

const Redacted = "[REDACTED]"

type Sensitive string

func (Sensitive) String() string { return Redacted }
func (Sensitive) LogValue() slog.Value {
	return slog.StringValue(Redacted)
}

func RedactText(text string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, Redacted)
		}
	}
	return text
}

type AuditDetails map[string]any

var auditDetailSchema = map[AuditAction]map[string]func(any) bool{
	AuditBootstrapStart:          {"bootstrap_state": oneOf("in_progress")},
	AuditBootstrapComplete:       {"bootstrap_state": oneOf("completed"), "mfa_method": validMFAMethod},
	AuditBootstrapReset:          {"bootstrap_state": oneOf("required")},
	AuditLoginPassword:           {},
	AuditLoginMFA:                {"mfa_method": validMFAMethod},
	AuditLogout:                  {"revoke_reason": validRevokeReason},
	AuditPasswordChange:          {"revoke_reason": validRevokeReason},
	AuditMFAEnroll:               {"mfa_method": validMFAMethod, "recovery_codes_remaining": boundedCount},
	AuditMFAReset:                {"revoke_reason": validRevokeReason},
	AuditRecoveryCodeUse:         {"mfa_method": oneOf(string(MFAMethodRecoveryCode)), "recovery_codes_remaining": boundedCount},
	AuditRecoveryCodesRegenerate: {"recovery_codes_remaining": boundedCount},
	AuditSessionCreate:           {"mfa_method": validMFAMethod},
	AuditSessionRevoke:           {"revoke_reason": validRevokeReason},
	AuditReauthenticate:          {"mfa_method": validMFAMethod},
	AuditAdministratorCreate:     {},
	AuditAdministratorActivate:   {"mfa_method": validMFAMethod},
	AuditAdministratorDisable:    {"revoke_reason": validRevokeReason},
	AuditActivationTokenGenerate: {},
	AuditAuthorization:           {"error_code": validErrorCode},
	AuditRateLimit:               {"rate_limit_dimension": validRateLimitDimension},
	AuditCSRF:                    {"error_code": oneOf(string(ErrorCodeCSRFRejected))},
}

// SanitizeAuditDetails copies only registered, bounded fields. It rejects
// unknown keys rather than attempting to redact arbitrary request objects.
func SanitizeAuditDetails(action AuditAction, details map[string]any) (AuditDetails, error) {
	if !action.Valid() {
		return nil, errors.New("auth: invalid audit action")
	}
	schema := auditDetailSchema[action]
	sanitized := make(AuditDetails, len(details))
	for key, value := range details {
		validate, allowed := schema[key]
		if !allowed || !validate(value) {
			return nil, fmt.Errorf("auth: audit detail is not allowed for action %s", action)
		}
		sanitized[key] = value
	}
	return sanitized, nil
}

func oneOf(allowed ...string) func(any) bool {
	return func(value any) bool {
		text, ok := value.(string)
		if !ok {
			return false
		}
		for _, candidate := range allowed {
			if text == candidate {
				return true
			}
		}
		return false
	}
}

func validMFAMethod(value any) bool {
	text, ok := value.(string)
	return ok && MFAMethod(text).Valid()
}

func validRevokeReason(value any) bool {
	text, ok := value.(string)
	return ok && SessionRevokeReason(text).Valid()
}

func validErrorCode(value any) bool {
	text, ok := value.(string)
	return ok && ErrorCode(text).Valid()
}

func validRateLimitDimension(value any) bool {
	text, ok := value.(string)
	return ok && RateLimitDimension(text).Valid()
}

func boundedCount(value any) bool {
	count, ok := value.(int)
	return ok && count >= 0 && count <= 100
}
