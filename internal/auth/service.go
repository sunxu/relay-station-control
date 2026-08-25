package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	store "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

const (
	SessionCookieName      = "__Host-relay_control_session"
	ChallengeCookieName    = "__Secure-relay_control_mfa"
	DevSessionCookieName   = "relay_control_session"
	DevChallengeCookieName = "relay_control_mfa"
)

var (
	ErrAuthentication = &ServiceError{Code: ErrorCodeAuthenticationFailed, HTTPStatus: 401}
	ErrUnauthorized   = &ServiceError{Code: ErrorCodeUnauthorized, HTTPStatus: 401}
	ErrForbidden      = &ServiceError{Code: ErrorCodeForbidden, HTTPStatus: 403}
	ErrCSRF           = &ServiceError{Code: ErrorCodeCSRFRejected, HTTPStatus: 403}
	ErrReauthenticate = &ServiceError{Code: ErrorCodeReauthentication, HTTPStatus: 403}
	ErrBootstrap      = &ServiceError{Code: ErrorCodeBootstrapClosed, HTTPStatus: 409}
	ErrRateLimited    = &ServiceError{Code: ErrorCodeRateLimited, HTTPStatus: 429}
	ErrInvalid        = &ServiceError{Code: ErrorCodeInvalidRequest, HTTPStatus: 400}
	ErrConflict       = &ServiceError{Code: ErrorCodeInvalidRequest, HTTPStatus: 409}
	ErrUnavailable    = &ServiceError{Code: ErrorCodeInternal, HTTPStatus: 503}
)

type ServiceError struct {
	Code       ErrorCode
	HTTPStatus int
	Kind       string
	Err        error
}

func (e *ServiceError) Error() string {
	if e == nil || e.Err == nil {
		return "authentication operation failed"
	}
	return "authentication operation failed: " + e.Err.Error()
}

func (e *ServiceError) Unwrap() error { return e.Err }

func serviceError(base *ServiceError, kind string, err error) error {
	return &ServiceError{Code: base.Code, HTTPStatus: base.HTTPStatus, Kind: kind, Err: err}
}

type RequestMeta struct {
	RequestID         string
	SourceFingerprint Digest
}

type AuditIntent struct {
	Action            AuditAction
	Result            AuditResult
	ActorAdminID      uuid.UUID
	TargetAdminID     uuid.UUID
	ActorFingerprint  []byte
	SourceFingerprint []byte
	Reason            string
	RequestID         string
	Details           map[string]any
}

type Enrollment struct {
	URI string
}

type Session struct {
	ID                     uuid.UUID
	AdminID                uuid.UUID
	LoginName              string
	DisplayName            string
	Role                   string
	Status                 string
	MFAMethod              MFAMethod
	CreatedAt              time.Time
	LastActivityAt         time.Time
	AbsoluteExpiresAt      time.Time
	ReauthenticatedAt      *time.Time
	RecoveryCodesRemaining int64
	Token                  string
	CSRFToken              string
	KeyVersion             KeyVersion
	csrfDigest             []byte
	MFARequired            bool
}

type Challenge struct {
	Token     string
	ExpiresAt time.Time
}

type Admin struct {
	ID          uuid.UUID
	LoginName   string
	DisplayName string
	AuthSource  string
	Role        string
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ActivatedAt *time.Time
	DisabledAt  *time.Time
	LastLoginAt *time.Time
}

type ActivationToken struct {
	Admin     Admin
	Token     string
	ExpiresAt time.Time
}

type Service struct {
	pool      *pgxpool.Pool
	config    *ValidatedConfig
	queries   *store.Queries
	dummyHash string
	metrics   *AuthMetrics
}

func NewService(pool *pgxpool.Pool, config *ValidatedConfig) (*Service, error) {
	if pool == nil || config == nil || config.Keyring == nil {
		return nil, errors.New("auth: database and keyring are required")
	}
	dummy, err := NewDummyPasswordHash()
	if err != nil {
		return nil, err
	}
	metrics := NewAuthMetrics()
	service := &Service{pool: pool, config: config, queries: store.New(pool), dummyHash: dummy, metrics: metrics}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = service.refreshActiveSessionMetric(ctx, service.queries); err != nil {
		return nil, fmt.Errorf("auth: initialize active-session metric: %w", err)
	}
	return service, nil
}

func (s *Service) Config() *ValidatedConfig { return s.config }
func (s *Service) Metrics() *AuthMetrics    { return s.metrics }

func (s *Service) metricAttempt(operation MetricOperation, result MetricResult) {
	if s.metrics != nil {
		_ = s.metrics.RecordAttempt(s.config.Environment, operation, result)
	}
}

func (s *Service) metricRateLimit(dimension RateLimitDimension) {
	if s.metrics != nil {
		_ = s.metrics.RecordRateLimit(s.config.Environment, dimension)
	}
}

func (s *Service) refreshActiveSessionMetric(ctx context.Context, q *store.Queries) error {
	count, err := q.CountActiveAdminSessions(ctx)
	if err != nil {
		return err
	}
	if s.metrics != nil {
		_ = s.metrics.SetActiveSessions(s.config.Environment, count)
	}
	return nil
}

func (s *Service) RequestMeta(source netip.Addr, requestID string) (RequestMeta, error) {
	fingerprint, err := SourceFingerprint(s.config.Keyring, source)
	if err != nil {
		return RequestMeta{}, err
	}
	return RequestMeta{RequestID: requestID, SourceFingerprint: fingerprint}, nil
}

func (s *Service) BootstrapStatus(ctx context.Context) (string, error) {
	state, err := s.queries.GetBootstrapState(ctx)
	if err != nil {
		return "", serviceError(ErrUnavailable, "bootstrap_state_unavailable", err)
	}
	return state.State, nil
}

func (s *Service) verifyBootstrapSecret(provided string) error {
	if s.config.BootstrapSecretFile == "" {
		return serviceError(ErrForbidden, "bootstrap_secret_unavailable", nil)
	}
	expected, err := os.ReadFile(s.config.BootstrapSecretFile)
	if err != nil {
		return serviceError(ErrForbidden, "bootstrap_secret_unavailable", err)
	}
	expected = []byte(strings.TrimSpace(string(expected)))
	actual := []byte(provided)
	if len(expected) < minBootstrapSecretBytes || len(actual) != len(expected) || subtle.ConstantTimeCompare(expected, actual) != 1 {
		return serviceError(ErrForbidden, "bootstrap_secret_invalid", nil)
	}
	return nil
}

func (s *Service) StartBootstrap(ctx context.Context, secret, loginName, displayName, password string, meta RequestMeta) (Enrollment, error) {
	if err := s.verifyBootstrapSecret(secret); err != nil {
		s.failureAudit(ctx, AuditIntent{Action: AuditBootstrapStart, Result: AuditResultDenied, SourceFingerprint: meta.SourceFingerprint.Sum[:], RequestID: meta.RequestID})
		return Enrollment{}, err
	}
	login, err := NormalizeLoginName(loginName)
	if err != nil {
		return Enrollment{}, serviceError(ErrInvalid, "invalid_bootstrap_input", err)
	}
	display, err := ValidateDisplayName(displayName)
	if err != nil {
		return Enrollment{}, serviceError(ErrInvalid, "invalid_bootstrap_input", err)
	}
	if err := ValidatePassword(password, login); err != nil {
		return Enrollment{}, serviceError(ErrInvalid, "invalid_bootstrap_input", err)
	}
	passwordPHC, err := HashPassword(password)
	if err != nil {
		return Enrollment{}, serviceError(ErrUnavailable, "password_hash_failed", err)
	}

	var enrollment Enrollment
	err = pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		state, err := q.LockBootstrapState(ctx)
		if err != nil {
			return err
		}
		if state.State == "completed" {
			return ErrBootstrap
		}
		if state.State == "in_progress" {
			admin, err := q.GetAdminUserByID(ctx, state.PendingAdminID)
			if err != nil || admin.LoginName != login || admin.DisplayName != display {
				return ErrConflict
			}
			factor, err := q.GetAdminTOTP(ctx, state.PendingAdminID)
			if err != nil {
				return err
			}
			plain, err := s.decryptTOTP(admin.AdminID, factor)
			if err != nil {
				return err
			}
			enrollment.URI = totpURI(admin.LoginName, string(plain))
			return nil
		}

		adminID := uuid.New()
		adminPG := pgUUID(adminID)
		admin, err := q.CreateAdminUser(ctx, store.CreateAdminUserParams{AdminID: adminPG, LoginName: login, DisplayName: display})
		if err != nil {
			return err
		}
		if _, err = q.UpsertAdminPassword(ctx, store.UpsertAdminPasswordParams{AdminID: adminPG, PasswordPhc: passwordPHC, ParameterVersion: 1}); err != nil {
			return err
		}
		totpSecret, err := GenerateTOTPSecret()
		if err != nil {
			return err
		}
		encrypted, err := EncryptTOTPSecret(s.config.Keyring, adminID.String(), []byte(totpSecret))
		if err != nil {
			return err
		}
		if _, err = q.UpsertAdminTOTP(ctx, store.UpsertAdminTOTPParams{AdminID: adminPG, EncryptedSecret: encrypted.Ciphertext, Nonce: encrypted.Nonce, KeyVersion: int32(encrypted.KeyVersion)}); err != nil {
			return err
		}
		if _, err = q.StartBootstrap(ctx, adminPG); err != nil {
			return err
		}
		if err = s.insertAudit(ctx, q, AuditIntent{Action: AuditBootstrapStart, Result: AuditResultSuccess, TargetAdminID: adminID, SourceFingerprint: meta.SourceFingerprint.Sum[:], RequestID: meta.RequestID, Details: map[string]any{"bootstrap_state": "in_progress"}}); err != nil {
			return err
		}
		enrollment.URI = totpURI(admin.LoginName, totpSecret)
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrBootstrap) || errors.Is(err, ErrConflict) {
			s.failureAudit(ctx, AuditIntent{Action: AuditBootstrapStart, Result: AuditResultDenied, SourceFingerprint: meta.SourceFingerprint.Sum[:], RequestID: meta.RequestID})
			return Enrollment{}, err
		}
		return Enrollment{}, serviceError(ErrUnavailable, "bootstrap_start_failed", err)
	}
	return enrollment, nil
}

func (s *Service) ResetBootstrap(ctx context.Context, secret string, meta RequestMeta) error {
	if err := s.verifyBootstrapSecret(secret); err != nil {
		return err
	}
	err := pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		state, err := q.LockBootstrapState(ctx)
		if err != nil {
			return err
		}
		if state.State != "in_progress" || !state.PendingAdminID.Valid {
			return ErrBootstrap
		}
		pending := uuidFromPG(state.PendingAdminID)
		if _, err = q.ResetPendingBootstrap(ctx, state.PendingAdminID); err != nil {
			return err
		}
		if _, err = q.DeletePendingAdminUser(ctx, state.PendingAdminID); err != nil {
			return err
		}
		return s.insertAudit(ctx, q, AuditIntent{Action: AuditBootstrapReset, Result: AuditResultSuccess, TargetAdminID: pending, SourceFingerprint: meta.SourceFingerprint.Sum[:], RequestID: meta.RequestID, Details: map[string]any{"bootstrap_state": "required"}})
	})
	if err != nil {
		if errors.Is(err, ErrBootstrap) {
			return err
		}
		return serviceError(ErrUnavailable, "bootstrap_reset_failed", err)
	}
	return nil
}

func (s *Service) CompleteBootstrap(ctx context.Context, secret, code string, meta RequestMeta) (Session, []string, error) {
	if err := s.verifyBootstrapSecret(secret); err != nil {
		return Session{}, nil, err
	}
	var result Session
	var recoveryCodes []string
	err := pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		state, err := q.LockBootstrapState(ctx)
		if err != nil {
			return err
		}
		if state.State != "in_progress" || !state.PendingAdminID.Valid {
			return ErrBootstrap
		}
		adminID := uuidFromPG(state.PendingAdminID)
		factor, err := q.LockAdminTOTP(ctx, state.PendingAdminID)
		if err != nil {
			return err
		}
		plain, err := s.decryptTOTP(state.PendingAdminID, factor)
		if err != nil {
			return err
		}
		databaseTime, err := q.GetDatabaseTime(ctx)
		if err != nil {
			return err
		}
		step, valid, err := ValidateTOTP(string(plain), code, databaseTime.Time.UTC(), -1)
		if err != nil || !valid {
			return ErrAuthentication
		}
		if _, err = q.ConfirmAdminTOTP(ctx, store.ConfirmAdminTOTPParams{AdminID: state.PendingAdminID, LastUsedStep: pgtype.Int8{Int64: step, Valid: true}}); err != nil {
			return err
		}
		recoveryCodes, err = s.replaceRecoveryCodes(ctx, q, state.PendingAdminID)
		if err != nil {
			return err
		}
		adminRow, err := q.ActivateAdminUser(ctx, state.PendingAdminID)
		if err != nil {
			return err
		}
		if _, err = q.CompleteBootstrap(ctx, state.PendingAdminID); err != nil {
			return err
		}
		result, err = s.createSession(ctx, q, adminRow, MFAMethodTOTP, true)
		if err != nil {
			return err
		}
		if err = s.insertAudit(ctx, q, AuditIntent{Action: AuditBootstrapComplete, Result: AuditResultSuccess, TargetAdminID: adminID, SourceFingerprint: meta.SourceFingerprint.Sum[:], RequestID: meta.RequestID, Details: map[string]any{"bootstrap_state": "completed", "mfa_method": string(MFAMethodTOTP)}}); err != nil {
			return err
		}
		return s.insertAudit(ctx, q, AuditIntent{Action: AuditSessionCreate, Result: AuditResultSuccess, ActorAdminID: adminID, TargetAdminID: adminID, SourceFingerprint: meta.SourceFingerprint.Sum[:], RequestID: meta.RequestID, Details: map[string]any{"mfa_method": string(MFAMethodTOTP)}})
	})
	if err != nil {
		if errors.Is(err, ErrAuthentication) {
			s.failureAudit(ctx, AuditIntent{Action: AuditBootstrapComplete, Result: AuditResultFailure, SourceFingerprint: meta.SourceFingerprint.Sum[:], RequestID: meta.RequestID})
			return Session{}, nil, err
		}
		if errors.Is(err, ErrBootstrap) {
			return Session{}, nil, err
		}
		return Session{}, nil, serviceError(ErrUnavailable, "bootstrap_complete_failed", err)
	}
	_ = s.refreshActiveSessionMetric(ctx, s.queries)
	return result, recoveryCodes, nil
}

func (s *Service) ListAdmins(ctx context.Context) ([]Admin, error) {
	rows, err := s.queries.ListAdminUsers(ctx)
	if err != nil {
		return nil, serviceError(ErrUnavailable, "administrator_list_failed", err)
	}
	result := make([]Admin, 0, len(rows))
	for _, row := range rows {
		result = append(result, adminFromRow(row))
	}
	return result, nil
}

func (s *Service) CreateAdmin(ctx context.Context, actor Session, loginName, displayName, reason string, meta RequestMeta) (ActivationToken, error) {
	if err := requireReauthenticated(actor, reason); err != nil {
		return ActivationToken{}, err
	}
	reason = strings.TrimSpace(reason)
	login, err := NormalizeLoginName(loginName)
	if err != nil {
		return ActivationToken{}, serviceError(ErrInvalid, "invalid_administrator", err)
	}
	display, err := ValidateDisplayName(displayName)
	if err != nil {
		return ActivationToken{}, serviceError(ErrInvalid, "invalid_administrator", err)
	}
	adminID := uuid.New()
	token, digest, err := s.newActivationToken()
	if err != nil {
		return ActivationToken{}, err
	}
	var expires time.Time
	var created store.ControlAdminUser
	err = pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		if freshErr := requireFreshReauthentication(ctx, q, actor); freshErr != nil {
			return freshErr
		}
		created, err = q.CreateAdminUser(ctx, store.CreateAdminUserParams{AdminID: pgUUID(adminID), LoginName: login, DisplayName: display})
		if err != nil {
			return err
		}
		createdToken, err := q.CreateActivationToken(ctx, store.CreateActivationTokenParams{ActivationTokenID: pgUUID(uuid.New()), AdminID: pgUUID(adminID), CreatedByAdminID: pgUUID(actor.AdminID), TokenDigest: digest.Sum[:], KeyVersion: int32(digest.KeyVersion)})
		if err != nil {
			return err
		}
		expires = createdToken.ExpiresAt.Time.UTC()
		return s.insertAudit(ctx, q, AuditIntent{Action: AuditAdministratorCreate, Result: AuditResultSuccess, ActorAdminID: actor.AdminID, TargetAdminID: adminID, SourceFingerprint: meta.SourceFingerprint.Sum[:], Reason: reason, RequestID: meta.RequestID})
	})
	if err != nil {
		var se *ServiceError
		if errors.As(err, &se) {
			return ActivationToken{}, err
		}
		return ActivationToken{}, serviceError(ErrConflict, "administrator_create_failed", err)
	}
	return ActivationToken{Admin: adminFromRow(created), Token: token, ExpiresAt: expires}, nil
}

func (s *Service) RegenerateActivationToken(ctx context.Context, actor Session, target uuid.UUID, reason string, meta RequestMeta) (ActivationToken, error) {
	if err := requireReauthenticated(actor, reason); err != nil {
		return ActivationToken{}, err
	}
	reason = strings.TrimSpace(reason)
	token, digest, err := s.newActivationToken()
	if err != nil {
		return ActivationToken{}, err
	}
	var expires time.Time
	var admin store.ControlAdminUser
	err = pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		if freshErr := requireFreshReauthentication(ctx, q, actor); freshErr != nil {
			return freshErr
		}
		admin, err = q.LockAdminUser(ctx, pgUUID(target))
		if err != nil || admin.Status != "pending" {
			return ErrConflict
		}
		if _, err = q.RevokeActivationTokensForAdmin(ctx, pgUUID(target)); err != nil {
			return err
		}
		createdToken, err := q.CreateActivationToken(ctx, store.CreateActivationTokenParams{ActivationTokenID: pgUUID(uuid.New()), AdminID: pgUUID(target), CreatedByAdminID: pgUUID(actor.AdminID), TokenDigest: digest.Sum[:], KeyVersion: int32(digest.KeyVersion)})
		if err != nil {
			return err
		}
		expires = createdToken.ExpiresAt.Time.UTC()
		return s.insertAudit(ctx, q, AuditIntent{Action: AuditActivationTokenGenerate, Result: AuditResultSuccess, ActorAdminID: actor.AdminID, TargetAdminID: target, SourceFingerprint: meta.SourceFingerprint.Sum[:], Reason: reason, RequestID: meta.RequestID})
	})
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return ActivationToken{}, err
		}
		var se *ServiceError
		if errors.As(err, &se) {
			return ActivationToken{}, err
		}
		return ActivationToken{}, serviceError(ErrUnavailable, "activation_token_failed", err)
	}
	return ActivationToken{Admin: adminFromRow(admin), Token: token, ExpiresAt: expires}, nil
}

func (s *Service) DisableAdmin(ctx context.Context, actor Session, target uuid.UUID, reason string, meta RequestMeta) (Admin, error) {
	if err := requireReauthenticated(actor, reason); err != nil {
		return Admin{}, err
	}
	reason = strings.TrimSpace(reason)
	if actor.AdminID == target {
		return Admin{}, serviceError(ErrForbidden, "self_disable_forbidden", nil)
	}
	var disabled store.ControlAdminUser
	err := pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		if freshErr := requireFreshReauthentication(ctx, q, actor); freshErr != nil {
			return freshErr
		}
		if _, err := q.LockAdminUser(ctx, pgUUID(target)); err != nil {
			return ErrConflict
		}
		count, err := q.CountEnabledAdminUsers(ctx)
		if err != nil {
			return err
		}
		if count <= 1 {
			return serviceError(ErrConflict, "last_administrator_protected", nil)
		}
		disabled, err = q.DisableAdminUser(ctx, pgUUID(target))
		if err != nil {
			return err
		}
		if _, err = q.RevokeAllAdminSessions(ctx, store.RevokeAllAdminSessionsParams{AdminID: pgUUID(target), RevokeReason: nullableText(string(SessionRevokeAdministrator))}); err != nil {
			return err
		}
		if _, err = q.RevokeAuthChallengesForAdmin(ctx, pgUUID(target)); err != nil {
			return err
		}
		if _, err = q.RevokeActivationTokensForAdmin(ctx, pgUUID(target)); err != nil {
			return err
		}
		return s.insertAudit(ctx, q, AuditIntent{Action: AuditAdministratorDisable, Result: AuditResultSuccess, ActorAdminID: actor.AdminID, TargetAdminID: target, SourceFingerprint: meta.SourceFingerprint.Sum[:], Reason: reason, RequestID: meta.RequestID, Details: map[string]any{"revoke_reason": string(SessionRevokeAdministrator)}})
	})
	if err != nil {
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrForbidden) {
			return Admin{}, err
		}
		var se *ServiceError
		if errors.As(err, &se) {
			return Admin{}, err
		}
		return Admin{}, serviceError(ErrUnavailable, "administrator_disable_failed", err)
	}
	return adminFromRow(disabled), nil
}

func (s *Service) ResetAdminMFA(ctx context.Context, actor Session, target uuid.UUID, reason string, meta RequestMeta) (ActivationToken, error) {
	if err := requireReauthenticated(actor, reason); err != nil {
		return ActivationToken{}, err
	}
	reason = strings.TrimSpace(reason)
	if actor.AdminID == target {
		return ActivationToken{}, serviceError(ErrForbidden, "self_mfa_reset_forbidden", nil)
	}
	token, digest, err := s.newActivationToken()
	if err != nil {
		return ActivationToken{}, err
	}
	var expires time.Time
	var admin store.ControlAdminUser
	err = pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		if freshErr := requireFreshReauthentication(ctx, q, actor); freshErr != nil {
			return freshErr
		}
		admin, err = q.LockAdminUser(ctx, pgUUID(target))
		if err != nil {
			return ErrConflict
		}
		if admin.Status == "enabled" {
			count, err := q.CountEnabledAdminUsers(ctx)
			if err != nil {
				return err
			}
			if count <= 1 {
				return serviceError(ErrConflict, "last_administrator_protected", nil)
			}
		}
		if _, err = q.DeleteAdminTOTP(ctx, pgUUID(target)); err != nil {
			return err
		}
		if _, err = q.RevokeAvailableRecoveryCodes(ctx, pgUUID(target)); err != nil {
			return err
		}
		if _, err = q.RevokeAuthChallengesForAdmin(ctx, pgUUID(target)); err != nil {
			return err
		}
		if _, err = q.RevokeAllAdminSessions(ctx, store.RevokeAllAdminSessionsParams{AdminID: pgUUID(target), RevokeReason: nullableText(string(SessionRevokeMFA))}); err != nil {
			return err
		}
		if _, err = q.RevokeActivationTokensForAdmin(ctx, pgUUID(target)); err != nil {
			return err
		}
		admin, err = q.ResetAdminUserToPending(ctx, pgUUID(target))
		if err != nil {
			return err
		}
		createdToken, err := q.CreateActivationToken(ctx, store.CreateActivationTokenParams{ActivationTokenID: pgUUID(uuid.New()), AdminID: pgUUID(target), CreatedByAdminID: pgUUID(actor.AdminID), TokenDigest: digest.Sum[:], KeyVersion: int32(digest.KeyVersion)})
		if err != nil {
			return err
		}
		expires = createdToken.ExpiresAt.Time.UTC()
		return s.insertAudit(ctx, q, AuditIntent{Action: AuditMFAReset, Result: AuditResultSuccess, ActorAdminID: actor.AdminID, TargetAdminID: target, SourceFingerprint: meta.SourceFingerprint.Sum[:], Reason: reason, RequestID: meta.RequestID, Details: map[string]any{"revoke_reason": string(SessionRevokeMFA)}})
	})
	if err != nil {
		var se *ServiceError
		if errors.As(err, &se) {
			return ActivationToken{}, err
		}
		return ActivationToken{}, serviceError(ErrUnavailable, "mfa_reset_failed", err)
	}
	return ActivationToken{Admin: adminFromRow(admin), Token: token, ExpiresAt: expires}, nil
}

func (s *Service) newActivationToken() (string, Digest, error) {
	token, err := GenerateBearerToken()
	if err != nil {
		return "", Digest{}, serviceError(ErrUnavailable, "activation_token_generation_failed", err)
	}
	digest, err := ComputeDigest(s.config.Keyring, DomainActivationDigest, token)
	if err != nil {
		return "", Digest{}, serviceError(ErrUnavailable, "activation_token_generation_failed", err)
	}
	return token, digest, nil
}

func (s *Service) replaceRecoveryCodes(ctx context.Context, q *store.Queries, adminID pgtype.UUID) ([]string, error) {
	codes, err := GenerateRecoveryCodes(10)
	if err != nil {
		return nil, err
	}
	if _, err = q.RevokeAvailableRecoveryCodes(ctx, adminID); err != nil {
		return nil, err
	}
	batchID := pgUUID(uuid.New())
	for _, code := range codes {
		digest, err := ComputeDigest(s.config.Keyring, DomainRecoveryCodeDigest, code)
		if err != nil {
			return nil, err
		}
		if _, err = q.CreateRecoveryCode(ctx, store.CreateRecoveryCodeParams{RecoveryCodeID: pgUUID(uuid.New()), AdminID: adminID, BatchID: batchID, CodeDigest: digest.Sum[:], KeyVersion: int32(digest.KeyVersion)}); err != nil {
			return nil, err
		}
	}
	return codes, nil
}

func (s *Service) createSession(ctx context.Context, q *store.Queries, admin store.ControlAdminUser, method MFAMethod, reauthenticated bool) (Session, error) {
	token, err := GenerateBearerToken()
	if err != nil {
		return Session{}, err
	}
	csrf, err := GenerateBearerToken()
	if err != nil {
		return Session{}, err
	}
	tokenDigest, err := ComputeDigest(s.config.Keyring, DomainSessionDigest, token)
	if err != nil {
		return Session{}, err
	}
	csrfDigest, err := ComputeDigest(s.config.Keyring, DomainCSRFDigest, csrf)
	if err != nil {
		return Session{}, err
	}
	row, err := q.CreateAdminSession(ctx, store.CreateAdminSessionParams{
		SessionID:       pgUUID(uuid.New()),
		AdminID:         admin.AdminID,
		TokenDigest:     tokenDigest.Sum[:],
		CsrfDigest:      csrfDigest.Sum[:],
		KeyVersion:      int32(tokenDigest.KeyVersion),
		MfaMethod:       string(method),
		Reauthenticated: reauthenticated,
	})
	if err != nil {
		return Session{}, err
	}
	count, err := q.CountAvailableRecoveryCodes(ctx, admin.AdminID)
	if err != nil {
		return Session{}, err
	}
	return s.sessionFromRows(row, admin, count, token, csrf), nil
}

func (s *Service) decryptTOTP(adminID pgtype.UUID, factor store.ControlAdminTotp) ([]byte, error) {
	return DecryptTOTPSecret(s.config.Keyring, uuidFromPG(adminID).String(), EncryptedTOTPSecret{KeyVersion: KeyVersion(factor.KeyVersion), Nonce: factor.Nonce, Ciphertext: factor.EncryptedSecret})
}

func (s *Service) insertAudit(ctx context.Context, q *store.Queries, intent AuditIntent) error {
	if intent.RequestID == "" {
		intent.RequestID = "request-id-unavailable"
	}
	category, err := AuditCategoryFor(intent.Action)
	if err != nil {
		return err
	}
	details, err := SanitizeAuditDetails(intent.Action, intent.Details)
	if err != nil {
		return err
	}
	detailsJSON, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = q.InsertAuditLog(ctx, store.InsertAuditLogParams{AuditID: pgUUID(uuid.New()), Category: string(category), Action: string(intent.Action), Result: string(intent.Result), ActorAdminID: nullableUUID(intent.ActorAdminID), TargetAdminID: nullableUUID(intent.TargetAdminID), ActorFingerprint: intent.ActorFingerprint, SourceFingerprint: intent.SourceFingerprint, Reason: nullableText(intent.Reason), RequestID: intent.RequestID, Details: detailsJSON})
	return err
}

func (s *Service) failureAudit(ctx context.Context, intent AuditIntent) {
	if err := pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		return s.insertAudit(ctx, s.queries.WithTx(tx), intent)
	}); err != nil {
		slog.ErrorContext(ctx, "authentication failure audit unavailable", "component", "auth", "error_code", string(ErrorCodeInternal))
	}
}

func requireReauthenticated(session Session, reason string) error {
	return validateReason(reason)
}

func validateReason(reason string) error {
	if utf8.RuneCountInString(strings.TrimSpace(reason)) < 10 || utf8.RuneCountInString(strings.TrimSpace(reason)) > 500 {
		return serviceError(ErrInvalid, "invalid_operation_reason", nil)
	}
	return nil
}

func requireFreshReauthentication(ctx context.Context, q *store.Queries, session Session) error {
	_, err := q.LockFreshReauthenticatedSession(ctx, store.LockFreshReauthenticatedSessionParams{SessionID: pgUUID(session.ID), AdminID: pgUUID(session.AdminID)})
	if err != nil {
		if isNoRows(err) {
			return ErrReauthenticate
		}
		return err
	}
	return nil
}

func totpURI(login, secret string) string {
	issuer := "Relay Station Control"
	u := &url.URL{Scheme: "otpauth", Host: "totp", Path: "/" + issuer + ":" + login}
	query := u.Query()
	query.Set("secret", secret)
	query.Set("issuer", issuer)
	query.Set("algorithm", "SHA1")
	query.Set("digits", "6")
	query.Set("period", "30")
	u.RawQuery = query.Encode()
	return u.String()
}

func pgUUID(value uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: value, Valid: true} }
func nullableUUID(value uuid.UUID) pgtype.UUID {
	if value == uuid.Nil {
		return pgtype.UUID{}
	}
	return pgUUID(value)
}
func uuidFromPG(value pgtype.UUID) uuid.UUID {
	if !value.Valid {
		return uuid.Nil
	}
	return uuid.UUID(value.Bytes)
}
func pgTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}
func nullableText(value string) pgtype.Text {
	if value == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: value, Valid: true}
}
func timePtr(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	t := value.Time.UTC()
	return &t
}

func adminFromRow(row store.ControlAdminUser) Admin {
	return Admin{ID: uuidFromPG(row.AdminID), LoginName: row.LoginName, DisplayName: row.DisplayName, AuthSource: row.AuthSource, Role: row.Role, Status: row.Status, CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(), ActivatedAt: timePtr(row.ActivatedAt), DisabledAt: timePtr(row.DisabledAt), LastLoginAt: timePtr(row.LastLoginAt)}
}

func (s *Service) sessionFromRows(row store.ControlAdminSession, admin store.ControlAdminUser, recoveryCount int64, token, csrf string) Session {
	return Session{ID: uuidFromPG(row.SessionID), AdminID: uuidFromPG(row.AdminID), LoginName: admin.LoginName, DisplayName: admin.DisplayName, Role: admin.Role, Status: admin.Status, MFAMethod: MFAMethod(row.MfaMethod), CreatedAt: row.CreatedAt.Time.UTC(), LastActivityAt: row.LastActivityAt.Time.UTC(), AbsoluteExpiresAt: row.AbsoluteExpiresAt.Time.UTC(), ReauthenticatedAt: timePtr(row.ReauthenticatedAt), RecoveryCodesRemaining: recoveryCount, Token: token, CSRFToken: csrf, KeyVersion: KeyVersion(row.KeyVersion), csrfDigest: append([]byte(nil), row.CsrfDigest...), MFARequired: s.config.MFARequired}
}

func copyDigest(value []byte) Digest { var sum [32]byte; copy(sum[:], value); return Digest{Sum: sum} }

func (s *Service) closeWithInternal(err error, kind string) error {
	if err == nil {
		return nil
	}
	var se *ServiceError
	if errors.As(err, &se) {
		return err
	}
	return serviceError(ErrUnavailable, kind, err)
}

func (s *Service) digestCandidates(domain KeyDomain, value string) ([]Digest, error) {
	if domain == DomainRecoveryCodeDigest {
		normalized, err := NormalizeRecoveryCode(value)
		if err != nil {
			return nil, err
		}
		value = normalized
	}
	versions := s.config.Keyring.Versions()
	result := make([]Digest, 0, len(versions))
	current := s.config.Keyring.CurrentVersion()
	digest, err := s.digestForVersion(current, domain, value)
	if err != nil {
		return nil, err
	}
	result = append(result, digest)
	for index := len(versions) - 1; index >= 0; index-- {
		if versions[index] == current {
			continue
		}
		digest, err := s.digestForVersion(versions[index], domain, value)
		if err != nil {
			return nil, err
		}
		result = append(result, digest)
	}
	return result, nil
}

func validateMFAInput(method MFAMethod, code string) error {
	if !method.Valid() || strings.TrimSpace(code) == "" {
		return ErrAuthentication
	}
	return nil
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func unexpectedState(name string) error { return fmt.Errorf("auth: inconsistent %s state", name) }
