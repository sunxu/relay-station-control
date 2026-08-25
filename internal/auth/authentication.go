package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	store "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

type LoginResult struct {
	Challenge *Challenge
	Session   *Session
}

func (s *Service) Login(ctx context.Context, loginName, password string, meta RequestMeta) (LoginResult, error) {
	loginDigest, err := LoginFingerprint(s.config.Keyring, loginName)
	if err != nil {
		return LoginResult{}, serviceError(ErrUnavailable, "fingerprint_failed", err)
	}
	blockedDimension, blocked, err := s.blockedSubject(ctx, []rateSubject{{RateLimitAccount, loginDigest}, {RateLimitSource, meta.SourceFingerprint}})
	if err != nil {
		return LoginResult{}, serviceError(ErrUnavailable, "rate_limit_unavailable", err)
	}
	if blocked {
		s.metricAttempt(MetricOperationLogin, MetricResultRateLimited)
		s.metricRateLimit(blockedDimension)
		s.failureAudit(ctx, AuditIntent{Action: AuditRateLimit, Result: AuditResultLimited, ActorFingerprint: loginDigest.Sum[:], SourceFingerprint: meta.SourceFingerprint.Sum[:], RequestID: meta.RequestID, Details: map[string]any{"rate_limit_dimension": string(blockedDimension)}})
		return LoginResult{}, ErrRateLimited
	}

	normalized, normalizeErr := NormalizeLoginName(loginName)
	var admin store.ControlAdminUser
	var credential store.ControlAdminPassword
	adminFound := false
	if normalizeErr == nil {
		admin, err = s.queries.GetAdminUserByLoginName(ctx, normalized)
		if err == nil {
			credential, err = s.queries.GetAdminPassword(ctx, admin.AdminID)
			adminFound = err == nil
		}
	}
	phc := s.dummyHash
	if adminFound {
		phc = credential.PasswordPhc
	}
	matched, _, verifyErr := VerifyPassword(password, phc)
	if verifyErr != nil {
		matched = false
	}
	if !adminFound || admin.Status != "enabled" || !matched {
		if failureErr := s.recordFailure(ctx, loginDigest, meta, AuditLoginPassword); failureErr != nil {
			return LoginResult{}, serviceError(ErrUnavailable, "authentication_failure_state_unavailable", failureErr)
		}
		s.metricAttempt(MetricOperationLogin, MetricResultFailure)
		return LoginResult{}, ErrAuthentication
	}

	if s.config.MFARequired {
		token, err := GenerateBearerToken()
		if err != nil {
			return LoginResult{}, serviceError(ErrUnavailable, "challenge_token_failed", err)
		}
		digest, err := ComputeDigest(s.config.Keyring, DomainChallengeDigest, token)
		if err != nil {
			return LoginResult{}, serviceError(ErrUnavailable, "challenge_digest_failed", err)
		}
		var expires time.Time
		err = pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			q := s.queries.WithTx(tx)
			lockedAdmin, err := s.lockAndRevalidatePassword(ctx, q, admin.AdminID, password)
			if err != nil {
				return err
			}
			admin = lockedAdmin
			created, err := q.CreateAuthChallenge(ctx, store.CreateAuthChallengeParams{ChallengeID: pgUUID(uuid.New()), AdminID: admin.AdminID, TokenDigest: digest.Sum[:], KeyVersion: int32(digest.KeyVersion), SourceFingerprint: meta.SourceFingerprint.Sum[:]})
			if err != nil {
				return err
			}
			expires = created.ExpiresAt.Time.UTC()
			return s.insertAudit(ctx, q, AuditIntent{Action: AuditLoginPassword, Result: AuditResultSuccess, ActorAdminID: uuidFromPG(admin.AdminID), ActorFingerprint: loginDigest.Sum[:], SourceFingerprint: meta.SourceFingerprint.Sum[:], RequestID: meta.RequestID})
		})
		if err != nil {
			if errors.Is(err, ErrAuthentication) {
				if failureErr := s.recordFailure(ctx, loginDigest, meta, AuditLoginPassword); failureErr != nil {
					return LoginResult{}, serviceError(ErrUnavailable, "authentication_failure_state_unavailable", failureErr)
				}
				s.metricAttempt(MetricOperationLogin, MetricResultFailure)
				return LoginResult{}, ErrAuthentication
			}
			s.metricAttempt(MetricOperationLogin, MetricResultInternal)
			return LoginResult{}, serviceError(ErrUnavailable, "challenge_create_failed", err)
		}
		s.metricAttempt(MetricOperationLogin, MetricResultSuccess)
		return LoginResult{Challenge: &Challenge{Token: token, ExpiresAt: expires}}, nil
	}

	var session Session
	err = pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		lockedAdmin, err := s.lockAndRevalidatePassword(ctx, q, admin.AdminID, password)
		if err != nil {
			return err
		}
		admin = lockedAdmin
		session, err = s.createSession(ctx, q, admin, MFAMethodNone, false)
		if err != nil {
			return err
		}
		if _, err = q.UpdateAdminLastLogin(ctx, admin.AdminID); err != nil {
			return err
		}
		if _, err = q.DeleteAccountFailureWindow(ctx, store.DeleteAccountFailureWindowParams{KeyVersion: int32(loginDigest.KeyVersion), SubjectFingerprint: loginDigest.Sum[:]}); err != nil {
			return err
		}
		return s.insertAudit(ctx, q, AuditIntent{Action: AuditSessionCreate, Result: AuditResultSuccess, ActorAdminID: uuidFromPG(admin.AdminID), SourceFingerprint: meta.SourceFingerprint.Sum[:], RequestID: meta.RequestID, Details: map[string]any{"mfa_method": string(MFAMethodNone)}})
	})
	if err != nil {
		if errors.Is(err, ErrAuthentication) {
			if failureErr := s.recordFailure(ctx, loginDigest, meta, AuditLoginPassword); failureErr != nil {
				return LoginResult{}, serviceError(ErrUnavailable, "authentication_failure_state_unavailable", failureErr)
			}
			s.metricAttempt(MetricOperationLogin, MetricResultFailure)
			return LoginResult{}, ErrAuthentication
		}
		s.metricAttempt(MetricOperationLogin, MetricResultInternal)
		return LoginResult{}, serviceError(ErrUnavailable, "session_create_failed", err)
	}
	s.metricAttempt(MetricOperationLogin, MetricResultSuccess)
	_ = s.refreshActiveSessionMetric(ctx, s.queries)
	return LoginResult{Session: &session}, nil
}

func (s *Service) lockAndRevalidatePassword(ctx context.Context, q *store.Queries, adminID pgtype.UUID, password string) (store.ControlAdminUser, error) {
	admin, err := q.LockAdminUser(ctx, adminID)
	if err != nil || admin.Status != "enabled" || admin.AuthSource != "local" || admin.Role != "super_admin" {
		return store.ControlAdminUser{}, ErrAuthentication
	}
	credential, err := q.GetAdminPassword(ctx, admin.AdminID)
	if err != nil {
		return store.ControlAdminUser{}, ErrAuthentication
	}
	matched, _, err := VerifyPassword(password, credential.PasswordPhc)
	if err != nil || !matched {
		return store.ControlAdminUser{}, ErrAuthentication
	}
	return admin, nil
}

func (s *Service) CompleteLoginMFA(ctx context.Context, challengeToken string, method MFAMethod, code string, meta RequestMeta) (Session, error) {
	if err := validateMFAInput(method, code); err != nil {
		return Session{}, err
	}
	metricOperation := MetricOperationMFA
	if method == MFAMethodRecoveryCode {
		metricOperation = MetricOperationRecoveryCode
	}
	var session Session
	var accountDigest Digest
	challengePreview, digest, previewErr := s.lookupChallenge(ctx, s.queries, challengeToken)
	if previewErr == nil {
		if adminPreview, adminErr := s.queries.GetAdminUserByID(ctx, challengePreview.AdminID); adminErr == nil {
			accountDigest, _ = LoginFingerprint(s.config.Keyring, adminPreview.LoginName)
		}
	}
	if accountDigest.KeyVersion == 0 {
		accountDigest = digest
	}
	blockedDimension, blocked, blockErr := s.blockedSubject(ctx, []rateSubject{{RateLimitAccount, accountDigest}, {RateLimitSource, meta.SourceFingerprint}})
	if blockErr != nil {
		return Session{}, serviceError(ErrUnavailable, "rate_limit_unavailable", blockErr)
	}
	if blocked {
		s.metricAttempt(metricOperation, MetricResultRateLimited)
		s.metricRateLimit(blockedDimension)
		s.failureAudit(ctx, AuditIntent{Action: AuditRateLimit, Result: AuditResultLimited, ActorFingerprint: accountDigest.Sum[:], SourceFingerprint: meta.SourceFingerprint.Sum[:], RequestID: meta.RequestID, Details: map[string]any{"rate_limit_dimension": string(blockedDimension)}})
		return Session{}, ErrRateLimited
	}
	err := pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		challenge, _, err := s.lookupChallenge(ctx, q, challengeToken)
		if err != nil {
			if isNoRows(err) {
				return ErrAuthentication
			}
			return err
		}
		if !hmac.Equal(challenge.SourceFingerprint, meta.SourceFingerprint.Sum[:]) {
			return ErrAuthentication
		}
		admin, err := q.LockAdminUser(ctx, challenge.AdminID)
		if err != nil || admin.Status != "enabled" {
			return ErrAuthentication
		}
		accountDigest, err = LoginFingerprint(s.config.Keyring, admin.LoginName)
		if err != nil {
			return err
		}
		switch method {
		case MFAMethodTOTP:
			factor, err := q.LockAdminTOTP(ctx, admin.AdminID)
			if err != nil || !factor.ConfirmedAt.Valid {
				return ErrAuthentication
			}
			plain, err := s.decryptTOTP(admin.AdminID, factor)
			if err != nil {
				return err
			}
			last := int64(-1)
			if factor.LastUsedStep.Valid {
				last = factor.LastUsedStep.Int64
			}
			databaseTime, err := q.GetDatabaseTime(ctx)
			if err != nil {
				return err
			}
			step, valid, err := ValidateTOTP(string(plain), code, databaseTime.Time.UTC(), last)
			if err != nil || !valid {
				return ErrAuthentication
			}
			if _, err = q.ConsumeTOTPTimeStep(ctx, store.ConsumeTOTPTimeStepParams{AdminID: admin.AdminID, LastUsedStep: pgtype.Int8{Int64: step, Valid: true}}); err != nil {
				return ErrAuthentication
			}
		case MFAMethodRecoveryCode:
			if err := s.consumeRecovery(ctx, q, admin.AdminID, code); err != nil {
				return err
			}
		default:
			return ErrAuthentication
		}
		if _, err = q.ConsumeAuthChallenge(ctx, challenge.ChallengeID); err != nil {
			return ErrAuthentication
		}
		session, err = s.createSession(ctx, q, admin, method, false)
		if err != nil {
			return err
		}
		if _, err = q.UpdateAdminLastLogin(ctx, admin.AdminID); err != nil {
			return err
		}
		if _, err = q.DeleteAccountFailureWindow(ctx, store.DeleteAccountFailureWindowParams{KeyVersion: int32(accountDigest.KeyVersion), SubjectFingerprint: accountDigest.Sum[:]}); err != nil {
			return err
		}
		details := map[string]any{"mfa_method": string(method)}
		if method == MFAMethodRecoveryCode {
			details["recovery_codes_remaining"] = int(session.RecoveryCodesRemaining)
		}
		action := AuditLoginMFA
		if method == MFAMethodRecoveryCode {
			action = AuditRecoveryCodeUse
		}
		if err = s.insertAudit(ctx, q, AuditIntent{Action: action, Result: AuditResultSuccess, ActorAdminID: uuidFromPG(admin.AdminID), ActorFingerprint: accountDigest.Sum[:], SourceFingerprint: meta.SourceFingerprint.Sum[:], RequestID: meta.RequestID, Details: details}); err != nil {
			return err
		}
		return s.insertAudit(ctx, q, AuditIntent{Action: AuditSessionCreate, Result: AuditResultSuccess, ActorAdminID: uuidFromPG(admin.AdminID), SourceFingerprint: meta.SourceFingerprint.Sum[:], RequestID: meta.RequestID, Details: map[string]any{"mfa_method": string(method)}})
	})
	if err != nil {
		if errors.Is(err, ErrAuthentication) {
			if accountDigest.KeyVersion == 0 {
				accountDigest = digest
			}
			if failureErr := s.recordFailure(ctx, accountDigest, meta, AuditLoginMFA); failureErr != nil {
				return Session{}, serviceError(ErrUnavailable, "authentication_failure_state_unavailable", failureErr)
			}
			s.metricAttempt(metricOperation, MetricResultFailure)
			return Session{}, ErrAuthentication
		}
		s.metricAttempt(MetricOperationMFA, MetricResultInternal)
		return Session{}, serviceError(ErrUnavailable, "mfa_completion_failed", err)
	}
	s.metricAttempt(metricOperation, MetricResultSuccess)
	_ = s.refreshActiveSessionMetric(ctx, s.queries)
	return session, nil
}

type rateSubject struct {
	dimension RateLimitDimension
	digest    Digest
}

func (s *Service) isBlocked(ctx context.Context, subjects []rateSubject) (bool, error) {
	_, blocked, err := s.blockedSubject(ctx, subjects)
	return blocked, err
}

func (s *Service) blockedSubject(ctx context.Context, subjects []rateSubject) (RateLimitDimension, bool, error) {
	for _, subject := range subjects {
		blocked, err := s.queries.IsAuthFailureSubjectBlocked(ctx, store.IsAuthFailureSubjectBlockedParams{Dimension: string(subject.dimension), KeyVersion: int32(subject.digest.KeyVersion), SubjectFingerprint: subject.digest.Sum[:]})
		if err != nil {
			return "", false, err
		}
		if blocked {
			return subject.dimension, true, nil
		}
	}
	return "", false, nil
}

func (s *Service) recordFailure(ctx context.Context, account Digest, meta RequestMeta, action AuditAction) error {
	var err error
	for attempt := 0; attempt < 10; attempt++ {
		err = pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) error {
			q := s.queries.WithTx(tx)
			for _, subject := range []rateSubject{{RateLimitAccount, account}, {RateLimitSource, meta.SourceFingerprint}} {
				threshold := int32(5)
				if subject.dimension == RateLimitSource {
					threshold = 20
				}
				if _, updateErr := q.RecordAuthFailure(ctx, store.RecordAuthFailureParams{Dimension: string(subject.dimension), KeyVersion: int32(subject.digest.KeyVersion), SubjectFingerprint: subject.digest.Sum[:], FailureThreshold: threshold}); updateErr != nil {
					return updateErr
				}
			}
			return s.insertAudit(ctx, q, AuditIntent{Action: action, Result: AuditResultFailure, ActorFingerprint: account.Sum[:], SourceFingerprint: meta.SourceFingerprint.Sum[:], RequestID: meta.RequestID})
		})
		if !isSerializationFailure(err) {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * time.Millisecond)
	}
	return err
}

func isSerializationFailure(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40001"
}

func (s *Service) Authenticate(ctx context.Context, token string) (Session, error) {
	if token == "" {
		return Session{}, ErrUnauthorized
	}
	row, err := s.lookupSession(ctx, token)
	if err != nil {
		if isNoRows(err) {
			_, _ = s.queries.ExpireAdminSessions(ctx)
			return Session{}, ErrUnauthorized
		}
		return Session{}, serviceError(ErrUnavailable, "session_lookup_failed", err)
	}
	if row.AuthSource != "local" || row.Role != "super_admin" || row.AdminStatus != "enabled" {
		return Session{}, ErrForbidden
	}
	if s.config.MFARequired && (row.MfaMethod == string(MFAMethodNone) || !row.MfaCompletedAt.Valid) {
		return Session{}, ErrForbidden
	}
	touched, touchErr := s.queries.TouchAdminSession(ctx, row.SessionID)
	base := store.ControlAdminSession{SessionID: row.SessionID, AdminID: row.AdminID, KeyVersion: row.KeyVersion, CsrfDigest: row.CsrfDigest, MfaMethod: row.MfaMethod, MfaCompletedAt: row.MfaCompletedAt, CreatedAt: row.CreatedAt, LastActivityAt: row.LastActivityAt, AbsoluteExpiresAt: row.AbsoluteExpiresAt, ReauthenticatedAt: row.ReauthenticatedAt}
	if touchErr == nil {
		base = touched
	} else if isNoRows(touchErr) {
		// A no-op touch is ambiguous: activity may be newer than one minute, or
		// the session may have been revoked/expired after the digest lookup.
		// Re-lock and revalidate database truth so a concurrent revoke cannot be
		// mistaken for the intentional per-minute write throttle.
		active, activeErr := s.queries.LockActiveAdminSession(ctx, store.LockActiveAdminSessionParams{SessionID: row.SessionID, AdminID: row.AdminID})
		if activeErr != nil {
			if isNoRows(activeErr) {
				return Session{}, ErrUnauthorized
			}
			return Session{}, serviceError(ErrUnavailable, "session_revalidation_failed", activeErr)
		}
		base = active
	} else {
		return Session{}, serviceError(ErrUnavailable, "session_touch_failed", touchErr)
	}
	admin := store.ControlAdminUser{AdminID: row.AdminID, LoginName: row.LoginName, DisplayName: row.DisplayName, AuthSource: row.AuthSource, Role: row.Role, Status: row.AdminStatus}
	count, err := s.queries.CountAvailableRecoveryCodes(ctx, row.AdminID)
	if err != nil {
		return Session{}, serviceError(ErrUnavailable, "session_state_unavailable", err)
	}
	return s.sessionFromRows(base, admin, count, "", ""), nil
}

func (s *Service) VerifyCSRF(ctx context.Context, session Session, csrf string) error {
	if csrf == "" {
		return ErrCSRF
	}
	digest, err := s.digestForVersion(session.KeyVersion, DomainCSRFDigest, csrf)
	if err != nil {
		return ErrCSRF
	}
	if len(session.csrfDigest) != sha256.Size || !hmac.Equal(session.csrfDigest, digest.Sum[:]) {
		return ErrCSRF
	}
	return nil
}

func (s *Service) RotateCSRF(ctx context.Context, session Session) (Session, error) {
	csrf, err := GenerateBearerToken()
	if err != nil {
		return Session{}, serviceError(ErrUnavailable, "csrf_generation_failed", err)
	}
	digest, err := s.digestForVersion(session.KeyVersion, DomainCSRFDigest, csrf)
	if err != nil {
		return Session{}, serviceError(ErrUnavailable, "csrf_digest_failed", err)
	}
	row, err := s.queries.RotateAdminSessionCSRF(ctx, store.RotateAdminSessionCSRFParams{SessionID: pgUUID(session.ID), CsrfDigest: digest.Sum[:]})
	if err != nil {
		if isNoRows(err) {
			return Session{}, ErrUnauthorized
		}
		return Session{}, serviceError(ErrUnavailable, "csrf_rotation_failed", err)
	}
	session.CSRFToken = csrf
	session.csrfDigest = append([]byte(nil), digest.Sum[:]...)
	session.LastActivityAt = row.LastActivityAt.Time.UTC()
	return session, nil
}

func (s *Service) Logout(ctx context.Context, session *Session, meta RequestMeta) error {
	if session == nil {
		return nil
	}
	err := pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		if _, err := q.RevokeAdminSession(ctx, store.RevokeAdminSessionParams{SessionID: pgUUID(session.ID), RevokeReason: nullableText(string(SessionRevokeLogout))}); err != nil && !isNoRows(err) {
			return err
		}
		return s.insertAudit(ctx, q, AuditIntent{Action: AuditLogout, Result: AuditResultSuccess, ActorAdminID: session.AdminID, SourceFingerprint: meta.SourceFingerprint.Sum[:], RequestID: meta.RequestID, Details: map[string]any{"revoke_reason": string(SessionRevokeLogout)}})
	})
	if err == nil {
		_ = s.refreshActiveSessionMetric(ctx, s.queries)
	}
	return s.closeWithInternal(err, "logout_failed")
}

func (s *Service) Reauthenticate(ctx context.Context, current Session, password string, method MFAMethod, code string, meta RequestMeta) (Session, error) {
	if err := validateMFAInput(method, code); err != nil {
		return Session{}, err
	}
	var rotated Session
	err := pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		if _, err := q.LockActiveAdminSession(ctx, store.LockActiveAdminSessionParams{SessionID: pgUUID(current.ID), AdminID: pgUUID(current.AdminID)}); err != nil {
			if isNoRows(err) {
				return ErrAuthentication
			}
			return err
		}
		admin, err := q.LockAdminUser(ctx, pgUUID(current.AdminID))
		if err != nil || admin.Status != "enabled" {
			return ErrAuthentication
		}
		credential, err := q.GetAdminPassword(ctx, admin.AdminID)
		if err != nil {
			return ErrAuthentication
		}
		matched, _, err := VerifyPassword(password, credential.PasswordPhc)
		if err != nil || !matched {
			return ErrAuthentication
		}
		if err = s.verifyCurrentMFA(ctx, q, admin.AdminID, method, code); err != nil {
			return err
		}
		if _, err = q.RevokeAdminSession(ctx, store.RevokeAdminSessionParams{SessionID: pgUUID(current.ID), RevokeReason: nullableText(string(SessionRevokeRotated))}); err != nil {
			return err
		}
		rotated, err = s.createSession(ctx, q, admin, method, true)
		if err != nil {
			return err
		}
		return s.insertAudit(ctx, q, AuditIntent{Action: AuditReauthenticate, Result: AuditResultSuccess, ActorAdminID: current.AdminID, SourceFingerprint: meta.SourceFingerprint.Sum[:], RequestID: meta.RequestID, Details: map[string]any{"mfa_method": string(method)}})
	})
	if err != nil {
		if errors.Is(err, ErrAuthentication) {
			s.metricAttempt(MetricOperationReauth, MetricResultFailure)
			s.failureAudit(ctx, AuditIntent{Action: AuditReauthenticate, Result: AuditResultFailure, ActorAdminID: current.AdminID, SourceFingerprint: meta.SourceFingerprint.Sum[:], RequestID: meta.RequestID, Details: map[string]any{"mfa_method": string(method)}})
			return Session{}, ErrAuthentication
		}
		return Session{}, serviceError(ErrUnavailable, "reauthentication_failed", err)
	}
	s.metricAttempt(MetricOperationReauth, MetricResultSuccess)
	_ = s.refreshActiveSessionMetric(ctx, s.queries)
	return rotated, nil
}

func (s *Service) verifyCurrentMFA(ctx context.Context, q *store.Queries, adminID pgtype.UUID, method MFAMethod, code string) error {
	switch method {
	case MFAMethodTOTP:
		factor, err := q.LockAdminTOTP(ctx, adminID)
		if err != nil || !factor.ConfirmedAt.Valid {
			return ErrAuthentication
		}
		plain, err := s.decryptTOTP(adminID, factor)
		if err != nil {
			return err
		}
		last := int64(-1)
		if factor.LastUsedStep.Valid {
			last = factor.LastUsedStep.Int64
		}
		databaseTime, err := q.GetDatabaseTime(ctx)
		if err != nil {
			return err
		}
		step, valid, err := ValidateTOTP(string(plain), code, databaseTime.Time.UTC(), last)
		if err != nil || !valid {
			return ErrAuthentication
		}
		if _, err = q.ConsumeTOTPTimeStep(ctx, store.ConsumeTOTPTimeStepParams{AdminID: adminID, LastUsedStep: pgtype.Int8{Int64: step, Valid: true}}); err != nil {
			return ErrAuthentication
		}
	case MFAMethodRecoveryCode:
		if err := s.consumeRecovery(ctx, q, adminID, code); err != nil {
			return err
		}
	default:
		return ErrAuthentication
	}
	return nil
}

func (s *Service) RegenerateRecoveryCodes(ctx context.Context, current Session, reason string, meta RequestMeta) ([]string, error) {
	if err := requireReauthenticated(current, reason); err != nil {
		return nil, err
	}
	reason = strings.TrimSpace(reason)
	var codes []string
	err := pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		if freshErr := requireFreshReauthentication(ctx, q, current); freshErr != nil {
			return freshErr
		}
		var err error
		codes, err = s.replaceRecoveryCodes(ctx, q, pgUUID(current.AdminID))
		if err != nil {
			return err
		}
		return s.insertAudit(ctx, q, AuditIntent{Action: AuditRecoveryCodesRegenerate, Result: AuditResultSuccess, ActorAdminID: current.AdminID, TargetAdminID: current.AdminID, SourceFingerprint: meta.SourceFingerprint.Sum[:], Reason: reason, RequestID: meta.RequestID, Details: map[string]any{"recovery_codes_remaining": 10}})
	})
	if err != nil {
		var se *ServiceError
		if errors.As(err, &se) {
			return nil, err
		}
		return nil, serviceError(ErrUnavailable, "recovery_codes_failed", err)
	}
	return codes, nil
}

func (s *Service) ChangePassword(ctx context.Context, current Session, currentPassword, newPassword string, method MFAMethod, code string, meta RequestMeta) (Session, error) {
	if err := ValidatePassword(newPassword, current.LoginName); err != nil {
		return Session{}, serviceError(ErrInvalid, "invalid_new_password", err)
	}
	phc, err := HashPassword(newPassword)
	if err != nil {
		return Session{}, serviceError(ErrUnavailable, "password_hash_failed", err)
	}
	var rotated Session
	err = pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		freshErr := requireFreshReauthentication(ctx, q, current)
		if freshErr != nil {
			if !errors.Is(freshErr, ErrReauthenticate) {
				return freshErr
			}
			if _, err = q.LockActiveAdminSession(ctx, store.LockActiveAdminSessionParams{SessionID: pgUUID(current.ID), AdminID: pgUUID(current.AdminID)}); err != nil {
				if isNoRows(err) {
					return ErrAuthentication
				}
				return err
			}
		}
		admin, err := q.LockAdminUser(ctx, pgUUID(current.AdminID))
		if err != nil || admin.Status != "enabled" {
			return ErrAuthentication
		}
		credential, err := q.GetAdminPassword(ctx, admin.AdminID)
		if err != nil {
			return ErrAuthentication
		}
		matched, _, err := VerifyPassword(currentPassword, credential.PasswordPhc)
		if err != nil || !matched {
			return ErrAuthentication
		}
		if freshErr != nil {
			if err = s.verifyCurrentMFA(ctx, q, admin.AdminID, method, code); err != nil {
				return err
			}
		}
		if _, err = q.UpsertAdminPassword(ctx, store.UpsertAdminPasswordParams{AdminID: admin.AdminID, PasswordPhc: phc, ParameterVersion: 1}); err != nil {
			return err
		}
		if _, err = q.RevokeOtherAdminSessions(ctx, store.RevokeOtherAdminSessionsParams{AdminID: admin.AdminID, SessionID: pgUUID(current.ID), RevokeReason: nullableText(string(SessionRevokePassword))}); err != nil {
			return err
		}
		if _, err = q.RevokeAdminSession(ctx, store.RevokeAdminSessionParams{SessionID: pgUUID(current.ID), RevokeReason: nullableText(string(SessionRevokePassword))}); err != nil {
			return err
		}
		rotated, err = s.createSession(ctx, q, admin, current.MFAMethod, false)
		if err != nil {
			return err
		}
		return s.insertAudit(ctx, q, AuditIntent{Action: AuditPasswordChange, Result: AuditResultSuccess, ActorAdminID: current.AdminID, TargetAdminID: current.AdminID, SourceFingerprint: meta.SourceFingerprint.Sum[:], RequestID: meta.RequestID, Details: map[string]any{"revoke_reason": string(SessionRevokePassword)}})
	})
	if err != nil {
		if errors.Is(err, ErrAuthentication) {
			s.failureAudit(ctx, AuditIntent{Action: AuditPasswordChange, Result: AuditResultFailure, ActorAdminID: current.AdminID, TargetAdminID: current.AdminID, SourceFingerprint: meta.SourceFingerprint.Sum[:], RequestID: meta.RequestID})
			return Session{}, ErrAuthentication
		}
		return Session{}, serviceError(ErrUnavailable, "password_change_failed", err)
	}
	_ = s.refreshActiveSessionMetric(ctx, s.queries)
	return rotated, nil
}

// digestForVersion is kept here rather than in token.go so the cryptographic
// primitive file remains generated/reviewed independently. It is used when a
// persisted session retains an older key version during key rotation.
func (s *Service) digestForVersion(version KeyVersion, domain KeyDomain, value string) (Digest, error) {
	key, err := s.config.Keyring.Derive(version, domain, sha256.Size)
	if err != nil {
		return Digest{}, err
	}
	defer clear(key)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	var sum [sha256.Size]byte
	copy(sum[:], mac.Sum(nil))
	return Digest{KeyVersion: version, Sum: sum}, nil
}
