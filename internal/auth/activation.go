package auth

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	store "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

func (s *Service) StartActivation(ctx context.Context, token string, meta RequestMeta) (Enrollment, error) {
	var enrollment Enrollment
	err := pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		activation, _, err := s.lockActivation(ctx, q, token)
		if err != nil {
			if isNoRows(err) {
				return ErrAuthentication
			}
			return err
		}
		admin, err := q.LockAdminUser(ctx, activation.AdminID)
		if err != nil || admin.Status != "pending" {
			return ErrAuthentication
		}
		factor, err := q.GetAdminTOTP(ctx, admin.AdminID)
		var secret string
		if err == nil {
			if factor.ConfirmedAt.Valid {
				return ErrAuthentication
			}
			plain, err := s.decryptTOTP(admin.AdminID, factor)
			if err != nil {
				return err
			}
			secret = string(plain)
		} else if isNoRows(err) {
			secret, err = GenerateTOTPSecret()
			if err != nil {
				return err
			}
			encrypted, err := EncryptTOTPSecret(s.config.Keyring, uuidFromPG(admin.AdminID).String(), []byte(secret))
			if err != nil {
				return err
			}
			if _, err = q.UpsertAdminTOTP(ctx, store.UpsertAdminTOTPParams{AdminID: admin.AdminID, EncryptedSecret: encrypted.Ciphertext, Nonce: encrypted.Nonce, KeyVersion: int32(encrypted.KeyVersion)}); err != nil {
				return err
			}
		} else {
			return err
		}
		enrollment.URI = totpURI(admin.LoginName, secret)
		return s.insertAudit(ctx, q, AuditIntent{Action: AuditMFAEnroll, Result: AuditResultSuccess, TargetAdminID: uuidFromPG(admin.AdminID), SourceFingerprint: meta.SourceFingerprint.Sum[:], RequestID: meta.RequestID, Details: map[string]any{"mfa_method": string(MFAMethodTOTP), "recovery_codes_remaining": 0}})
	})
	if err != nil {
		if errors.Is(err, ErrAuthentication) {
			s.failureAudit(ctx, AuditIntent{Action: AuditAdministratorActivate, Result: AuditResultFailure, SourceFingerprint: meta.SourceFingerprint.Sum[:], RequestID: meta.RequestID})
			return Enrollment{}, ErrAuthentication
		}
		return Enrollment{}, serviceError(ErrUnavailable, "activation_start_failed", err)
	}
	return enrollment, nil
}

func (s *Service) CompleteActivation(ctx context.Context, token, password, code string, meta RequestMeta) (Session, []string, error) {
	var session Session
	var codes []string
	err := pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		activation, digest, err := s.lockActivation(ctx, q, token)
		if err != nil {
			if isNoRows(err) {
				return ErrAuthentication
			}
			return err
		}
		admin, err := q.LockAdminUser(ctx, activation.AdminID)
		if err != nil || admin.Status != "pending" {
			return ErrAuthentication
		}
		if err = ValidatePassword(password, admin.LoginName); err != nil {
			return serviceError(ErrInvalid, "invalid_new_password", err)
		}
		phc, err := HashPassword(password)
		if err != nil {
			return err
		}
		factor, err := q.LockAdminTOTP(ctx, admin.AdminID)
		if err != nil || factor.ConfirmedAt.Valid {
			return ErrAuthentication
		}
		plain, err := s.decryptTOTP(admin.AdminID, factor)
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
		if _, err = q.ConfirmAdminTOTP(ctx, store.ConfirmAdminTOTPParams{AdminID: admin.AdminID, LastUsedStep: pgtype.Int8{Int64: step, Valid: true}}); err != nil {
			return ErrAuthentication
		}
		if _, err = q.UpsertAdminPassword(ctx, store.UpsertAdminPasswordParams{AdminID: admin.AdminID, PasswordPhc: phc, ParameterVersion: 1}); err != nil {
			return err
		}
		codes, err = s.replaceRecoveryCodes(ctx, q, admin.AdminID)
		if err != nil {
			return err
		}
		admin, err = q.ActivateAdminUser(ctx, admin.AdminID)
		if err != nil {
			return err
		}
		if _, err = q.ConsumeActivationToken(ctx, store.ConsumeActivationTokenParams{KeyVersion: int32(digest.KeyVersion), TokenDigest: digest.Sum[:]}); err != nil {
			return ErrAuthentication
		}
		session, err = s.createSession(ctx, q, admin, MFAMethodTOTP, false)
		if err != nil {
			return err
		}
		if err = s.insertAudit(ctx, q, AuditIntent{Action: AuditAdministratorActivate, Result: AuditResultSuccess, ActorAdminID: uuidFromPG(admin.AdminID), TargetAdminID: uuidFromPG(admin.AdminID), SourceFingerprint: meta.SourceFingerprint.Sum[:], RequestID: meta.RequestID, Details: map[string]any{"mfa_method": string(MFAMethodTOTP)}}); err != nil {
			return err
		}
		return s.insertAudit(ctx, q, AuditIntent{Action: AuditSessionCreate, Result: AuditResultSuccess, ActorAdminID: uuidFromPG(admin.AdminID), TargetAdminID: uuidFromPG(admin.AdminID), SourceFingerprint: meta.SourceFingerprint.Sum[:], RequestID: meta.RequestID, Details: map[string]any{"mfa_method": string(MFAMethodTOTP)}})
	})
	if err != nil {
		if errors.Is(err, ErrAuthentication) {
			s.failureAudit(ctx, AuditIntent{Action: AuditAdministratorActivate, Result: AuditResultFailure, SourceFingerprint: meta.SourceFingerprint.Sum[:], RequestID: meta.RequestID})
			return Session{}, nil, ErrAuthentication
		}
		var se *ServiceError
		if errors.As(err, &se) {
			return Session{}, nil, err
		}
		return Session{}, nil, serviceError(ErrUnavailable, "activation_complete_failed", err)
	}
	_ = s.refreshActiveSessionMetric(ctx, s.queries)
	return session, codes, nil
}
