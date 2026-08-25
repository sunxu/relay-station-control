package auth

import "fmt"

type Environment string

const (
	EnvironmentDev        Environment = "dev"
	EnvironmentStaging    Environment = "staging"
	EnvironmentProduction Environment = "production"
)

func (v Environment) Valid() bool {
	return v == EnvironmentDev || v == EnvironmentStaging || v == EnvironmentProduction
}

type ErrorCode string

const (
	ErrorCodeAuthenticationFailed ErrorCode = "authentication_failed"
	ErrorCodeRateLimited          ErrorCode = "rate_limited"
	ErrorCodeInvalidRequest       ErrorCode = "invalid_request"
	ErrorCodeUnauthorized         ErrorCode = "unauthorized"
	ErrorCodeForbidden            ErrorCode = "forbidden"
	ErrorCodeCSRFRejected         ErrorCode = "csrf_rejected"
	ErrorCodeReauthentication     ErrorCode = "reauthentication_required"
	ErrorCodeBootstrapClosed      ErrorCode = "bootstrap_closed"
	ErrorCodeInternal             ErrorCode = "internal_error"
)

func (v ErrorCode) Valid() bool {
	switch v {
	case ErrorCodeAuthenticationFailed, ErrorCodeRateLimited, ErrorCodeInvalidRequest,
		ErrorCodeUnauthorized, ErrorCodeForbidden, ErrorCodeCSRFRejected,
		ErrorCodeReauthentication, ErrorCodeBootstrapClosed, ErrorCodeInternal:
		return true
	default:
		return false
	}
}

type AuditAction string

type AuditCategory string

const (
	AuditCategoryBootstrap        AuditCategory = "bootstrap"
	AuditCategoryAdministrator    AuditCategory = "administrator"
	AuditCategoryPassword         AuditCategory = "password"
	AuditCategoryMFA              AuditCategory = "mfa"
	AuditCategorySession          AuditCategory = "session"
	AuditCategoryReauthentication AuditCategory = "reauthentication"
	AuditCategoryAuthorization    AuditCategory = "authorization"
	AuditCategoryRateLimit        AuditCategory = "rate_limit"
)

func (v AuditCategory) Valid() bool {
	switch v {
	case AuditCategoryBootstrap, AuditCategoryAdministrator, AuditCategoryPassword,
		AuditCategoryMFA, AuditCategorySession, AuditCategoryReauthentication,
		AuditCategoryAuthorization, AuditCategoryRateLimit:
		return true
	default:
		return false
	}
}

const (
	AuditBootstrapStart          AuditAction = "bootstrap.start"
	AuditBootstrapComplete       AuditAction = "bootstrap.complete"
	AuditBootstrapReset          AuditAction = "bootstrap.reset"
	AuditLoginPassword           AuditAction = "auth.login_password"
	AuditLoginMFA                AuditAction = "auth.login_mfa"
	AuditLogout                  AuditAction = "auth.logout"
	AuditPasswordChange          AuditAction = "auth.password_change"
	AuditMFAEnroll               AuditAction = "auth.mfa_enroll"
	AuditMFAReset                AuditAction = "auth.mfa_reset"
	AuditRecoveryCodeUse         AuditAction = "auth.recovery_code_use"
	AuditRecoveryCodesRegenerate AuditAction = "auth.recovery_codes_regenerate"
	AuditSessionCreate           AuditAction = "session.create"
	AuditSessionRevoke           AuditAction = "session.revoke"
	AuditReauthenticate          AuditAction = "auth.reauthenticate"
	AuditAdministratorCreate     AuditAction = "administrator.create"
	AuditAdministratorActivate   AuditAction = "administrator.activate"
	AuditAdministratorDisable    AuditAction = "administrator.disable"
	AuditActivationTokenGenerate AuditAction = "administrator.activation_token_generate"
	AuditAuthorization           AuditAction = "authorization.check"
	AuditRateLimit               AuditAction = "auth.rate_limit"
	AuditCSRF                    AuditAction = "auth.csrf"
)

func (v AuditAction) Valid() bool {
	switch v {
	case AuditBootstrapStart, AuditBootstrapComplete, AuditBootstrapReset,
		AuditLoginPassword, AuditLoginMFA, AuditLogout, AuditPasswordChange,
		AuditMFAEnroll, AuditMFAReset, AuditRecoveryCodeUse, AuditRecoveryCodesRegenerate,
		AuditSessionCreate, AuditSessionRevoke, AuditReauthenticate,
		AuditAdministratorCreate, AuditAdministratorActivate, AuditAdministratorDisable,
		AuditActivationTokenGenerate, AuditAuthorization, AuditRateLimit, AuditCSRF:
		return true
	default:
		return false
	}
}

func AuditCategoryFor(action AuditAction) (AuditCategory, error) {
	switch action {
	case AuditBootstrapStart, AuditBootstrapComplete, AuditBootstrapReset:
		return AuditCategoryBootstrap, nil
	case AuditAdministratorCreate, AuditAdministratorActivate, AuditAdministratorDisable, AuditActivationTokenGenerate:
		return AuditCategoryAdministrator, nil
	case AuditLoginPassword, AuditPasswordChange:
		return AuditCategoryPassword, nil
	case AuditLoginMFA, AuditMFAEnroll, AuditMFAReset, AuditRecoveryCodeUse, AuditRecoveryCodesRegenerate:
		return AuditCategoryMFA, nil
	case AuditLogout, AuditSessionCreate, AuditSessionRevoke:
		return AuditCategorySession, nil
	case AuditReauthenticate:
		return AuditCategoryReauthentication, nil
	case AuditAuthorization, AuditCSRF:
		return AuditCategoryAuthorization, nil
	case AuditRateLimit:
		return AuditCategoryRateLimit, nil
	default:
		return "", fmt.Errorf("auth: invalid audit action")
	}
}

type AuditResult string

const (
	AuditResultSuccess AuditResult = "success"
	AuditResultFailure AuditResult = "failure"
	AuditResultDenied  AuditResult = "denied"
	AuditResultLimited AuditResult = "rate_limited"
)

func (v AuditResult) Valid() bool {
	return v == AuditResultSuccess || v == AuditResultFailure || v == AuditResultDenied || v == AuditResultLimited
}

type MFAMethod string

const (
	MFAMethodNone         MFAMethod = "none"
	MFAMethodTOTP         MFAMethod = "totp"
	MFAMethodRecoveryCode MFAMethod = "recovery_code"
)

func (v MFAMethod) Valid() bool {
	return v == MFAMethodNone || v == MFAMethodTOTP || v == MFAMethodRecoveryCode
}

type SessionRevokeReason string

const (
	SessionRevokeLogout         SessionRevokeReason = "logout"
	SessionRevokeIdleExpired    SessionRevokeReason = "idle_expired"
	SessionRevokeAbsoluteExpiry SessionRevokeReason = "absolute_expired"
	SessionRevokeAdministrator  SessionRevokeReason = "administrator_disabled"
	SessionRevokePassword       SessionRevokeReason = "password_changed"
	SessionRevokeMFA            SessionRevokeReason = "mfa_changed"
	SessionRevokeRotated        SessionRevokeReason = "rotated"
	SessionRevokeOperator       SessionRevokeReason = "operator_revoked"
)

func (v SessionRevokeReason) Valid() bool {
	switch v {
	case SessionRevokeLogout, SessionRevokeIdleExpired, SessionRevokeAbsoluteExpiry,
		SessionRevokeAdministrator, SessionRevokePassword, SessionRevokeMFA,
		SessionRevokeRotated, SessionRevokeOperator:
		return true
	default:
		return false
	}
}

func requireEnum[T ~string](name string, value T, valid bool) error {
	if !valid {
		return fmt.Errorf("auth: invalid %s", name)
	}
	if value == "" {
		return fmt.Errorf("auth: empty %s", name)
	}
	return nil
}
