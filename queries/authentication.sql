-- name: GetDatabaseTime :one
SELECT CURRENT_TIMESTAMP::timestamptz AS database_time;

-- name: GetBootstrapState :one
SELECT singleton_id, state, pending_admin_id, started_at, completed_at, updated_at
FROM control_bootstrap_state
WHERE singleton_id = 1;

-- name: LockBootstrapState :one
SELECT singleton_id, state, pending_admin_id, started_at, completed_at, updated_at
FROM control_bootstrap_state
WHERE singleton_id = 1
FOR UPDATE;

-- name: StartBootstrap :one
UPDATE control_bootstrap_state
SET state = 'in_progress',
    pending_admin_id = $1,
    started_at = CURRENT_TIMESTAMP,
    completed_at = NULL,
    updated_at = CURRENT_TIMESTAMP
WHERE singleton_id = 1 AND state = 'required'
RETURNING singleton_id, state, pending_admin_id, started_at, completed_at, updated_at;

-- name: ResetPendingBootstrap :one
UPDATE control_bootstrap_state
SET state = 'required',
    pending_admin_id = NULL,
    started_at = NULL,
    completed_at = NULL,
    updated_at = CURRENT_TIMESTAMP
WHERE singleton_id = 1 AND state = 'in_progress' AND pending_admin_id = $1
RETURNING singleton_id, state, pending_admin_id, started_at, completed_at, updated_at;

-- name: CompleteBootstrap :one
UPDATE control_bootstrap_state
SET state = 'completed',
    pending_admin_id = NULL,
    completed_at = CURRENT_TIMESTAMP,
    updated_at = CURRENT_TIMESTAMP
WHERE singleton_id = 1 AND state = 'in_progress' AND pending_admin_id = $1
RETURNING singleton_id, state, pending_admin_id, started_at, completed_at, updated_at;

-- name: CreateAdminUser :one
INSERT INTO control_admin_users (admin_id, login_name, display_name)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetAdminUserByID :one
SELECT * FROM control_admin_users WHERE admin_id = $1;

-- name: GetAdminUserByLoginName :one
SELECT * FROM control_admin_users WHERE login_name = $1;

-- name: LockAdminUser :one
SELECT * FROM control_admin_users WHERE admin_id = $1 FOR UPDATE;

-- name: ListAdminUsers :many
SELECT * FROM control_admin_users
ORDER BY login_name, admin_id;

-- name: ActivateAdminUser :one
UPDATE control_admin_users
SET status = 'enabled', activated_at = CURRENT_TIMESTAMP, disabled_at = NULL,
    updated_at = CURRENT_TIMESTAMP
WHERE admin_id = $1 AND status = 'pending'
RETURNING *;

-- name: DisableAdminUser :one
UPDATE control_admin_users
SET status = 'disabled', disabled_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
WHERE admin_id = $1 AND status = 'enabled'
RETURNING *;

-- name: ResetAdminUserToPending :one
UPDATE control_admin_users
SET status = 'pending', disabled_at = NULL, updated_at = CURRENT_TIMESTAMP
WHERE admin_id = $1 AND status IN ('enabled', 'disabled')
RETURNING *;

-- name: UpdateAdminLastLogin :one
UPDATE control_admin_users
SET last_login_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
WHERE admin_id = $1 AND status = 'enabled'
RETURNING *;

-- name: CountEnabledAdminUsers :one
SELECT count(*) FROM control_admin_users WHERE status = 'enabled';

-- name: DeletePendingAdminUser :execrows
DELETE FROM control_admin_users WHERE admin_id = $1 AND status = 'pending';

-- name: UpsertAdminPassword :one
INSERT INTO control_admin_passwords (admin_id, password_phc, parameter_version)
VALUES ($1, $2, $3)
ON CONFLICT (admin_id) DO UPDATE
SET password_phc = EXCLUDED.password_phc,
    parameter_version = EXCLUDED.parameter_version,
    changed_at = CURRENT_TIMESTAMP
RETURNING *;

-- name: GetAdminPassword :one
SELECT * FROM control_admin_passwords WHERE admin_id = $1;

-- name: UpsertAdminTOTP :one
INSERT INTO control_admin_totp (admin_id, encrypted_secret, nonce, key_version)
VALUES ($1, $2, $3, $4)
ON CONFLICT (admin_id) DO UPDATE
SET encrypted_secret = EXCLUDED.encrypted_secret,
    nonce = EXCLUDED.nonce,
    key_version = EXCLUDED.key_version,
    created_at = CURRENT_TIMESTAMP,
    confirmed_at = NULL,
    last_used_step = NULL
RETURNING *;

-- name: GetAdminTOTP :one
SELECT * FROM control_admin_totp WHERE admin_id = $1;

-- name: LockAdminTOTP :one
SELECT * FROM control_admin_totp WHERE admin_id = $1 FOR UPDATE;

-- name: ConfirmAdminTOTP :one
UPDATE control_admin_totp
SET confirmed_at = CURRENT_TIMESTAMP, last_used_step = $2
WHERE admin_id = $1 AND confirmed_at IS NULL
RETURNING *;

-- name: ConsumeTOTPTimeStep :one
UPDATE control_admin_totp
SET last_used_step = $2
WHERE admin_id = $1
  AND confirmed_at IS NOT NULL
  AND (last_used_step IS NULL OR last_used_step < $2)
RETURNING *;

-- name: DeleteAdminTOTP :execrows
DELETE FROM control_admin_totp WHERE admin_id = $1;

-- name: CreateRecoveryCode :one
INSERT INTO control_admin_recovery_codes
    (recovery_code_id, admin_id, batch_id, code_digest, key_version)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ConsumeRecoveryCode :one
UPDATE control_admin_recovery_codes
SET consumed_at = CURRENT_TIMESTAMP
WHERE admin_id = $1 AND key_version = $2 AND code_digest = $3
  AND consumed_at IS NULL AND revoked_at IS NULL
RETURNING *;

-- name: RevokeAvailableRecoveryCodes :execrows
UPDATE control_admin_recovery_codes
SET revoked_at = CURRENT_TIMESTAMP
WHERE admin_id = $1 AND consumed_at IS NULL AND revoked_at IS NULL;

-- name: CountAvailableRecoveryCodes :one
SELECT count(*) FROM control_admin_recovery_codes
WHERE admin_id = $1 AND consumed_at IS NULL AND revoked_at IS NULL;

-- name: CreateActivationToken :one
INSERT INTO control_admin_activation_tokens
    (activation_token_id, admin_id, created_by_admin_id, token_digest, key_version, expires_at)
VALUES ($1, $2, $3, $4, $5, CURRENT_TIMESTAMP + interval '24 hours')
RETURNING *;

-- name: LockActiveActivationTokenByDigest :one
SELECT t.*
FROM control_admin_activation_tokens AS t
JOIN control_admin_users AS u ON u.admin_id = t.admin_id
WHERE t.key_version = $1 AND t.token_digest = $2
  AND t.consumed_at IS NULL AND t.revoked_at IS NULL
  AND t.expires_at > CURRENT_TIMESTAMP
  AND u.status = 'pending'
FOR UPDATE OF t;

-- name: ConsumeActivationToken :one
UPDATE control_admin_activation_tokens
SET consumed_at = CURRENT_TIMESTAMP
WHERE key_version = $1 AND token_digest = $2
  AND consumed_at IS NULL AND revoked_at IS NULL
  AND expires_at > CURRENT_TIMESTAMP
RETURNING *;

-- name: RevokeActivationTokensForAdmin :execrows
UPDATE control_admin_activation_tokens
SET revoked_at = CURRENT_TIMESTAMP
WHERE admin_id = $1 AND consumed_at IS NULL AND revoked_at IS NULL;

-- name: CreateAuthChallenge :one
INSERT INTO control_auth_challenges
    (challenge_id, admin_id, token_digest, key_version, source_fingerprint, expires_at)
VALUES ($1, $2, $3, $4, $5, CURRENT_TIMESTAMP + interval '5 minutes')
RETURNING *;

-- name: GetActiveAuthChallengeByDigest :one
SELECT * FROM control_auth_challenges
WHERE key_version = $1 AND token_digest = $2
  AND consumed_at IS NULL AND revoked_at IS NULL
  AND expires_at > CURRENT_TIMESTAMP;

-- name: ConsumeAuthChallenge :one
UPDATE control_auth_challenges
SET consumed_at = CURRENT_TIMESTAMP
WHERE challenge_id = $1 AND consumed_at IS NULL AND revoked_at IS NULL
  AND expires_at > CURRENT_TIMESTAMP
RETURNING *;

-- name: RevokeAuthChallengesForAdmin :execrows
UPDATE control_auth_challenges
SET revoked_at = CURRENT_TIMESTAMP
WHERE admin_id = $1 AND consumed_at IS NULL AND revoked_at IS NULL;

-- name: DeleteExpiredAuthChallenges :execrows
DELETE FROM control_auth_challenges
WHERE expires_at <= CURRENT_TIMESTAMP OR consumed_at IS NOT NULL OR revoked_at IS NOT NULL;

-- name: CreateAdminSession :one
INSERT INTO control_admin_sessions
    (session_id, admin_id, token_digest, csrf_digest, key_version, mfa_method,
     mfa_completed_at, absolute_expires_at, reauthenticated_at)
VALUES (
    $1, $2, $3, $4, $5, $6,
    CASE WHEN $6 = 'none' THEN NULL ELSE CURRENT_TIMESTAMP END,
    CURRENT_TIMESTAMP + interval '12 hours',
    CASE WHEN sqlc.arg(reauthenticated)::boolean THEN CURRENT_TIMESTAMP ELSE NULL END
)
RETURNING *;

-- name: GetActiveAdminSessionByDigest :one
SELECT s.*, u.login_name, u.display_name, u.auth_source, u.role, u.status AS admin_status
FROM control_admin_sessions AS s
JOIN control_admin_users AS u ON u.admin_id = s.admin_id
WHERE s.key_version = $1 AND s.token_digest = $2
  AND s.revoked_at IS NULL
  AND s.absolute_expires_at > CURRENT_TIMESTAMP
  AND s.last_activity_at > CURRENT_TIMESTAMP - interval '30 minutes'
  AND u.status = 'enabled';

-- name: TouchAdminSession :one
UPDATE control_admin_sessions
SET last_activity_at = CURRENT_TIMESTAMP
WHERE session_id = $1 AND revoked_at IS NULL
  AND absolute_expires_at > CURRENT_TIMESTAMP
  AND last_activity_at > CURRENT_TIMESTAMP - interval '30 minutes'
  AND last_activity_at <= CURRENT_TIMESTAMP - interval '1 minute'
RETURNING *;

-- name: MarkAdminSessionReauthenticated :one
UPDATE control_admin_sessions
SET reauthenticated_at = CURRENT_TIMESTAMP
WHERE session_id = $1 AND revoked_at IS NULL
  AND absolute_expires_at > CURRENT_TIMESTAMP
  AND last_activity_at > CURRENT_TIMESTAMP - interval '30 minutes'
RETURNING *;

-- name: LockFreshReauthenticatedSession :one
SELECT s.*
FROM control_admin_sessions AS s
JOIN control_admin_users AS u ON u.admin_id = s.admin_id
WHERE s.session_id = $1
  AND s.admin_id = $2
  AND s.revoked_at IS NULL
  AND s.absolute_expires_at > CURRENT_TIMESTAMP
  AND s.last_activity_at > CURRENT_TIMESTAMP - interval '30 minutes'
  AND s.reauthenticated_at > CURRENT_TIMESTAMP - interval '5 minutes'
  AND u.status = 'enabled'
  AND u.auth_source = 'local'
  AND u.role = 'super_admin'
FOR UPDATE OF s, u;

-- name: LockActiveAdminSession :one
SELECT s.*
FROM control_admin_sessions AS s
JOIN control_admin_users AS u ON u.admin_id = s.admin_id
WHERE s.session_id = $1
  AND s.admin_id = $2
  AND s.revoked_at IS NULL
  AND s.absolute_expires_at > CURRENT_TIMESTAMP
  AND s.last_activity_at > CURRENT_TIMESTAMP - interval '30 minutes'
  AND u.status = 'enabled'
  AND u.auth_source = 'local'
  AND u.role = 'super_admin'
FOR UPDATE OF s, u;

-- name: RotateAdminSessionCSRF :one
UPDATE control_admin_sessions
SET csrf_digest = $2
WHERE session_id = $1 AND revoked_at IS NULL
  AND absolute_expires_at > CURRENT_TIMESTAMP
  AND last_activity_at > CURRENT_TIMESTAMP - interval '30 minutes'
  AND EXISTS (
      SELECT 1 FROM control_admin_users AS u
      WHERE u.admin_id = control_admin_sessions.admin_id AND u.status = 'enabled'
  )
RETURNING *;

-- name: RevokeAdminSession :one
UPDATE control_admin_sessions
SET revoked_at = CURRENT_TIMESTAMP, revoke_reason = $2
WHERE session_id = $1 AND revoked_at IS NULL
RETURNING *;

-- name: RevokeOtherAdminSessions :execrows
UPDATE control_admin_sessions
SET revoked_at = CURRENT_TIMESTAMP, revoke_reason = $3
WHERE admin_id = $1 AND session_id <> $2 AND revoked_at IS NULL;

-- name: RevokeAllAdminSessions :execrows
UPDATE control_admin_sessions
SET revoked_at = CURRENT_TIMESTAMP, revoke_reason = $2
WHERE admin_id = $1 AND revoked_at IS NULL;

-- name: ExpireAdminSessions :execrows
UPDATE control_admin_sessions
SET revoked_at = CURRENT_TIMESTAMP,
    revoke_reason = CASE
        WHEN absolute_expires_at <= CURRENT_TIMESTAMP THEN 'absolute_expired'
        ELSE 'idle_expired'
    END
WHERE revoked_at IS NULL
  AND (absolute_expires_at <= CURRENT_TIMESTAMP
       OR last_activity_at <= CURRENT_TIMESTAMP - interval '30 minutes');

-- name: CountActiveAdminSessions :one
SELECT count(*) FROM control_admin_sessions AS s
JOIN control_admin_users AS u ON u.admin_id = s.admin_id
WHERE s.revoked_at IS NULL
  AND s.absolute_expires_at > CURRENT_TIMESTAMP
  AND s.last_activity_at > CURRENT_TIMESTAMP - interval '30 minutes'
  AND u.status = 'enabled';

-- name: GetAuthFailureWindowForUpdate :one
SELECT * FROM control_auth_failure_windows
WHERE dimension = $1 AND key_version = $2 AND subject_fingerprint = $3
FOR UPDATE;

-- name: IsAuthFailureSubjectBlocked :one
SELECT EXISTS (
    SELECT 1
    FROM control_auth_failure_windows
    WHERE dimension = $1
      AND key_version = $2
      AND subject_fingerprint = $3
      AND blocked_until > CURRENT_TIMESTAMP
) AS blocked;

-- name: RecordAuthFailure :one
SELECT
    (recorded.result).failure_count::integer AS failure_count,
    (recorded.result).blocked_until::timestamptz AS blocked_until
FROM (
    SELECT control_record_auth_failure(
        sqlc.arg(dimension),
        sqlc.arg(key_version),
        sqlc.arg(subject_fingerprint),
        sqlc.arg(failure_threshold)::integer
    ) AS result
) AS recorded;

-- name: DeleteAccountFailureWindow :execrows
DELETE FROM control_auth_failure_windows
WHERE dimension = 'account' AND key_version = $1 AND subject_fingerprint = $2;

-- name: DeleteExpiredAuthFailureWindows :execrows
DELETE FROM control_auth_failure_windows AS windows
WHERE (windows.blocked_until IS NULL OR windows.blocked_until <= CURRENT_TIMESTAMP)
  AND windows.updated_at <= CURRENT_TIMESTAMP - interval '30 minutes'
  AND NOT EXISTS (
      SELECT 1
      FROM control_auth_failure_events AS events
      WHERE events.dimension = windows.dimension
        AND events.key_version = windows.key_version
        AND events.subject_fingerprint = windows.subject_fingerprint
        AND events.occurred_at > CURRENT_TIMESTAMP - interval '15 minutes'
  );

-- name: DeleteExpiredAuthFailureEvents :execrows
DELETE FROM control_auth_failure_events
WHERE occurred_at <= CURRENT_TIMESTAMP - interval '15 minutes';

-- name: InsertAuditLog :one
INSERT INTO audit_logs
    (audit_id, category, action, result, actor_admin_id, target_admin_id,
     actor_fingerprint, source_fingerprint, reason, request_id, details)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: GetAuditLogByID :one
SELECT * FROM audit_logs WHERE audit_id = $1;
