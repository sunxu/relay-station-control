-- +goose Up
CREATE TABLE control_admin_users (
    admin_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    login_name text NOT NULL,
    display_name text NOT NULL,
    auth_source text NOT NULL DEFAULT 'local',
    role text NOT NULL DEFAULT 'super_admin',
    status text NOT NULL DEFAULT 'pending',
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    activated_at timestamptz,
    disabled_at timestamptz,
    last_login_at timestamptz,
    CONSTRAINT control_admin_users_login_name_format CHECK (
        login_name ~ '^[a-z0-9._-]{3,64}$'
    ),
    CONSTRAINT control_admin_users_display_name_format CHECK (
        display_name = btrim(display_name)
        AND char_length(display_name) BETWEEN 1 AND 100
    ),
    CONSTRAINT control_admin_users_auth_source_fixed CHECK (auth_source = 'local'),
    CONSTRAINT control_admin_users_role_fixed CHECK (role = 'super_admin'),
    CONSTRAINT control_admin_users_status_valid CHECK (status IN ('pending', 'enabled', 'disabled')),
    CONSTRAINT control_admin_users_timestamps_valid CHECK (
        updated_at >= created_at
        AND (activated_at IS NULL OR activated_at >= created_at)
        AND (disabled_at IS NULL OR disabled_at >= created_at)
        AND (last_login_at IS NULL OR last_login_at >= created_at)
        AND (status <> 'enabled' OR (activated_at IS NOT NULL AND disabled_at IS NULL))
        AND (status <> 'disabled' OR disabled_at IS NOT NULL)
    )
);

CREATE UNIQUE INDEX control_admin_users_login_name_unique
    ON control_admin_users (login_name);
CREATE INDEX control_admin_users_enabled_idx
    ON control_admin_users (admin_id)
    WHERE status = 'enabled';

CREATE TABLE control_admin_safety_guard (
    singleton_id smallint PRIMARY KEY DEFAULT 1 CHECK (singleton_id = 1)
);
INSERT INTO control_admin_safety_guard (singleton_id) VALUES (1);

-- +goose StatementBegin
CREATE FUNCTION public.control_protect_enabled_admin() RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
BEGIN
    IF (TG_OP = 'DELETE' AND OLD.status = 'enabled')
       OR (TG_OP = 'UPDATE' AND OLD.status = 'enabled' AND NEW.status <> 'enabled') THEN
        PERFORM singleton_id
        FROM public.control_admin_safety_guard
        WHERE singleton_id = 1
        FOR UPDATE;

        IF (SELECT count(*) FROM public.control_admin_users
            WHERE status = 'enabled' AND admin_id <> OLD.admin_id) = 0 THEN
            RAISE EXCEPTION 'cannot remove the last enabled super_admin'
                USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER control_admin_users_protect_last_enabled
BEFORE UPDATE OF status OR DELETE ON control_admin_users
FOR EACH ROW EXECUTE FUNCTION control_protect_enabled_admin();

CREATE TABLE control_bootstrap_state (
    singleton_id smallint PRIMARY KEY DEFAULT 1 CHECK (singleton_id = 1),
    state text NOT NULL DEFAULT 'required',
    pending_admin_id uuid REFERENCES control_admin_users(admin_id) ON DELETE RESTRICT,
    started_at timestamptz,
    completed_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT control_bootstrap_state_valid CHECK (state IN ('required', 'in_progress', 'completed')),
    CONSTRAINT control_bootstrap_state_shape CHECK (
        (state = 'required' AND pending_admin_id IS NULL AND started_at IS NULL AND completed_at IS NULL)
        OR (state = 'in_progress' AND pending_admin_id IS NOT NULL AND started_at IS NOT NULL AND completed_at IS NULL)
        OR (state = 'completed' AND pending_admin_id IS NULL AND started_at IS NOT NULL AND completed_at IS NOT NULL)
    ),
    CONSTRAINT control_bootstrap_state_timestamps_valid CHECK (
        (started_at IS NULL OR started_at <= CURRENT_TIMESTAMP)
        AND (completed_at IS NULL OR (started_at IS NOT NULL AND completed_at >= started_at))
        AND updated_at <= CURRENT_TIMESTAMP
    )
);
INSERT INTO control_bootstrap_state (singleton_id, state) VALUES (1, 'required');

-- +goose StatementBegin
CREATE FUNCTION control_enforce_bootstrap_transition() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'bootstrap state singleton cannot be deleted' USING ERRCODE = '23514';
    END IF;
    IF OLD.state = 'completed' THEN
        IF NEW IS DISTINCT FROM OLD THEN
            RAISE EXCEPTION 'completed bootstrap state is immutable' USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;
    IF NOT (
        (OLD.state = 'required' AND NEW.state IN ('required', 'in_progress'))
        OR (OLD.state = 'in_progress' AND NEW.state IN ('required', 'in_progress', 'completed'))
    ) THEN
        RAISE EXCEPTION 'invalid bootstrap state transition' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER control_bootstrap_state_transition_guard
BEFORE UPDATE OR DELETE ON control_bootstrap_state
FOR EACH ROW EXECUTE FUNCTION control_enforce_bootstrap_transition();

CREATE TABLE control_admin_passwords (
    admin_id uuid PRIMARY KEY REFERENCES control_admin_users(admin_id) ON DELETE CASCADE,
    password_phc text NOT NULL,
    parameter_version integer NOT NULL,
    changed_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT control_admin_passwords_phc_valid CHECK (
        password_phc LIKE '$argon2id$%' AND octet_length(password_phc) BETWEEN 32 AND 512
    ),
    CONSTRAINT control_admin_passwords_parameter_version_valid CHECK (parameter_version > 0)
);

CREATE TABLE control_admin_totp (
    admin_id uuid PRIMARY KEY REFERENCES control_admin_users(admin_id) ON DELETE CASCADE,
    encrypted_secret bytea NOT NULL,
    nonce bytea NOT NULL,
    key_version integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    confirmed_at timestamptz,
    last_used_step bigint,
    CONSTRAINT control_admin_totp_ciphertext_valid CHECK (octet_length(encrypted_secret) >= 16),
    CONSTRAINT control_admin_totp_nonce_valid CHECK (octet_length(nonce) = 12),
    CONSTRAINT control_admin_totp_key_version_valid CHECK (key_version > 0),
    CONSTRAINT control_admin_totp_timestamps_valid CHECK (
        (confirmed_at IS NULL OR confirmed_at >= created_at)
        AND (last_used_step IS NULL OR (confirmed_at IS NOT NULL AND last_used_step >= 0))
    )
);

CREATE TABLE control_admin_recovery_codes (
    recovery_code_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_id uuid NOT NULL REFERENCES control_admin_users(admin_id) ON DELETE CASCADE,
    batch_id uuid NOT NULL,
    code_digest bytea NOT NULL,
    key_version integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    consumed_at timestamptz,
    revoked_at timestamptz,
    CONSTRAINT control_admin_recovery_codes_digest_valid CHECK (octet_length(code_digest) = 32),
    CONSTRAINT control_admin_recovery_codes_key_version_valid CHECK (key_version > 0),
    CONSTRAINT control_admin_recovery_codes_state_valid CHECK (
        NOT (consumed_at IS NOT NULL AND revoked_at IS NOT NULL)
        AND (consumed_at IS NULL OR consumed_at >= created_at)
        AND (revoked_at IS NULL OR revoked_at >= created_at)
    ),
    UNIQUE (key_version, code_digest)
);
CREATE INDEX control_admin_recovery_codes_available_idx
    ON control_admin_recovery_codes (admin_id, batch_id, recovery_code_id)
    WHERE consumed_at IS NULL AND revoked_at IS NULL;

CREATE TABLE control_admin_activation_tokens (
    activation_token_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_id uuid NOT NULL REFERENCES control_admin_users(admin_id) ON DELETE CASCADE,
    created_by_admin_id uuid NOT NULL REFERENCES control_admin_users(admin_id) ON DELETE RESTRICT,
    token_digest bytea NOT NULL,
    key_version integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    revoked_at timestamptz,
    CONSTRAINT control_admin_activation_tokens_digest_valid CHECK (octet_length(token_digest) = 32),
    CONSTRAINT control_admin_activation_tokens_key_version_valid CHECK (key_version > 0),
    CONSTRAINT control_admin_activation_tokens_expiry_valid CHECK (
        expires_at > created_at AND expires_at <= created_at + interval '24 hours'
    ),
    CONSTRAINT control_admin_activation_tokens_state_valid CHECK (
        NOT (consumed_at IS NOT NULL AND revoked_at IS NOT NULL)
        AND (consumed_at IS NULL OR consumed_at >= created_at)
        AND (revoked_at IS NULL OR revoked_at >= created_at)
    ),
    UNIQUE (key_version, token_digest)
);
CREATE UNIQUE INDEX control_admin_activation_tokens_one_open_idx
    ON control_admin_activation_tokens (admin_id)
    WHERE consumed_at IS NULL AND revoked_at IS NULL;
CREATE INDEX control_admin_activation_tokens_lookup_idx
    ON control_admin_activation_tokens (key_version, token_digest, expires_at)
    WHERE consumed_at IS NULL AND revoked_at IS NULL;

CREATE TABLE control_auth_challenges (
    challenge_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_id uuid NOT NULL REFERENCES control_admin_users(admin_id) ON DELETE CASCADE,
    token_digest bytea NOT NULL,
    key_version integer NOT NULL,
    source_fingerprint bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    revoked_at timestamptz,
    CONSTRAINT control_auth_challenges_token_digest_valid CHECK (octet_length(token_digest) = 32),
    CONSTRAINT control_auth_challenges_source_fingerprint_valid CHECK (octet_length(source_fingerprint) = 32),
    CONSTRAINT control_auth_challenges_key_version_valid CHECK (key_version > 0),
    CONSTRAINT control_auth_challenges_expiry_valid CHECK (
        expires_at > created_at AND expires_at <= created_at + interval '5 minutes'
    ),
    CONSTRAINT control_auth_challenges_state_valid CHECK (
        NOT (consumed_at IS NOT NULL AND revoked_at IS NOT NULL)
        AND (consumed_at IS NULL OR consumed_at >= created_at)
        AND (revoked_at IS NULL OR revoked_at >= created_at)
    ),
    UNIQUE (key_version, token_digest)
);
CREATE INDEX control_auth_challenges_lookup_idx
    ON control_auth_challenges (key_version, token_digest, expires_at)
    WHERE consumed_at IS NULL AND revoked_at IS NULL;
CREATE INDEX control_auth_challenges_admin_idx
    ON control_auth_challenges (admin_id, expires_at)
    WHERE consumed_at IS NULL AND revoked_at IS NULL;

CREATE TABLE control_admin_sessions (
    session_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_id uuid NOT NULL REFERENCES control_admin_users(admin_id) ON DELETE RESTRICT,
    token_digest bytea NOT NULL,
    csrf_digest bytea NOT NULL,
    key_version integer NOT NULL,
    mfa_method text NOT NULL,
    mfa_completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_activity_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    absolute_expires_at timestamptz NOT NULL,
    reauthenticated_at timestamptz,
    revoked_at timestamptz,
    revoke_reason text,
    CONSTRAINT control_admin_sessions_token_digest_valid CHECK (octet_length(token_digest) = 32),
    CONSTRAINT control_admin_sessions_csrf_digest_valid CHECK (octet_length(csrf_digest) = 32),
    CONSTRAINT control_admin_sessions_key_version_valid CHECK (key_version > 0),
    CONSTRAINT control_admin_sessions_mfa_method_valid CHECK (mfa_method IN ('none', 'totp', 'recovery_code')),
    CONSTRAINT control_admin_sessions_mfa_state_valid CHECK (
        (mfa_method = 'none' AND mfa_completed_at IS NULL)
        OR (mfa_method <> 'none' AND mfa_completed_at IS NOT NULL)
    ),
    CONSTRAINT control_admin_sessions_expiry_valid CHECK (
        last_activity_at >= created_at
        AND absolute_expires_at > created_at
        AND absolute_expires_at <= created_at + interval '12 hours'
        AND (mfa_completed_at IS NULL OR mfa_completed_at >= created_at)
        AND (reauthenticated_at IS NULL OR reauthenticated_at >= created_at)
        AND (revoked_at IS NULL OR revoked_at >= created_at)
    ),
    CONSTRAINT control_admin_sessions_revoke_state_valid CHECK (
        (revoked_at IS NULL AND revoke_reason IS NULL)
        OR (revoked_at IS NOT NULL AND revoke_reason IN (
            'logout', 'idle_expired', 'absolute_expired', 'administrator_disabled',
            'password_changed', 'mfa_changed', 'rotated', 'operator_revoked'
        ))
    ),
    UNIQUE (key_version, token_digest),
    UNIQUE (key_version, csrf_digest)
);
CREATE INDEX control_admin_sessions_active_lookup_idx
    ON control_admin_sessions (key_version, token_digest, absolute_expires_at)
    WHERE revoked_at IS NULL;
CREATE INDEX control_admin_sessions_admin_active_idx
    ON control_admin_sessions (admin_id, absolute_expires_at)
    WHERE revoked_at IS NULL;
CREATE INDEX control_admin_sessions_idle_expiry_idx
    ON control_admin_sessions (last_activity_at, absolute_expires_at)
    WHERE revoked_at IS NULL;

CREATE TABLE control_auth_failure_windows (
    dimension text NOT NULL,
    key_version integer NOT NULL,
    subject_fingerprint bytea NOT NULL,
    blocked_until timestamptz,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (dimension, key_version, subject_fingerprint),
    CONSTRAINT control_auth_failure_windows_dimension_valid CHECK (dimension IN ('account', 'source')),
    CONSTRAINT control_auth_failure_windows_key_version_valid CHECK (key_version > 0),
    CONSTRAINT control_auth_failure_windows_fingerprint_valid CHECK (octet_length(subject_fingerprint) = 32),
    CONSTRAINT control_auth_failure_windows_block_valid CHECK (
        blocked_until IS NULL OR blocked_until >= updated_at
    )
);
CREATE INDEX control_auth_failure_windows_expiry_idx
    ON control_auth_failure_windows (blocked_until, updated_at);

CREATE TABLE control_auth_failure_events (
    failure_event_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    dimension text NOT NULL,
    key_version integer NOT NULL,
    subject_fingerprint bytea NOT NULL,
    occurred_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT control_auth_failure_events_dimension_valid CHECK (dimension IN ('account', 'source')),
    CONSTRAINT control_auth_failure_events_key_version_valid CHECK (key_version > 0),
    CONSTRAINT control_auth_failure_events_fingerprint_valid CHECK (octet_length(subject_fingerprint) = 32),
    CONSTRAINT control_auth_failure_events_subject_fk FOREIGN KEY
        (dimension, key_version, subject_fingerprint)
        REFERENCES control_auth_failure_windows (dimension, key_version, subject_fingerprint)
        ON DELETE CASCADE
);
CREATE INDEX control_auth_failure_events_rolling_idx
    ON control_auth_failure_events
        (dimension, key_version, subject_fingerprint, occurred_at DESC);
CREATE INDEX control_auth_failure_events_expiry_idx
    ON control_auth_failure_events (occurred_at);

-- Serialize one rate-limit subject before inserting and counting its rolling
-- failure events.  A VOLATILE PL/pgSQL function uses a fresh command snapshot
-- for each statement at READ COMMITTED, so a waiter observes events committed
-- by the transaction that previously held the advisory lock.  Serializable
-- callers may still receive SQLSTATE 40001 and must retry the whole transaction.
CREATE TYPE control_auth_failure_result AS (
    failure_count integer,
    blocked_until timestamptz
);

-- +goose StatementBegin
CREATE FUNCTION control_record_auth_failure(
    p_dimension text,
    p_key_version integer,
    p_subject_fingerprint bytea,
    p_failure_threshold integer
) RETURNS control_auth_failure_result
LANGUAGE plpgsql
VOLATILE
AS $$
DECLARE
    recorded_at timestamptz;
    counted_failures integer;
    resulting_blocked_until timestamptz;
BEGIN
    IF p_failure_threshold <= 0 THEN
        RAISE EXCEPTION 'failure threshold must be positive' USING ERRCODE = '22023';
    END IF;

    PERFORM pg_advisory_xact_lock(hashtextextended(
        p_dimension || ':' || p_key_version::text || ':' || encode(p_subject_fingerprint, 'hex'),
        0
    ));

    -- transaction_timestamp() can predate a committed waiter predecessor when
    -- transactions begin concurrently. Capture the database wall clock only
    -- after acquiring the subject lock so event and block times are monotonic.
    recorded_at := clock_timestamp();

    INSERT INTO control_auth_failure_windows
        (dimension, key_version, subject_fingerprint, blocked_until, updated_at)
    VALUES
        (p_dimension, p_key_version, p_subject_fingerprint, NULL, recorded_at)
    ON CONFLICT (dimension, key_version, subject_fingerprint) DO UPDATE
    SET updated_at = recorded_at,
        blocked_until = CASE
            WHEN control_auth_failure_windows.blocked_until > recorded_at
                THEN control_auth_failure_windows.blocked_until
            ELSE NULL
        END;

    INSERT INTO control_auth_failure_events
        (dimension, key_version, subject_fingerprint, occurred_at)
    VALUES
        (p_dimension, p_key_version, p_subject_fingerprint, recorded_at);

    SELECT count(*)::integer
    INTO counted_failures
    FROM control_auth_failure_events AS events
    WHERE events.dimension = p_dimension
      AND events.key_version = p_key_version
      AND events.subject_fingerprint = p_subject_fingerprint
      AND events.occurred_at > recorded_at - interval '15 minutes'
      AND events.occurred_at <= recorded_at;

    UPDATE control_auth_failure_windows AS windows
    SET blocked_until = CASE
            WHEN counted_failures >= p_failure_threshold
                 AND (windows.blocked_until IS NULL OR windows.blocked_until <= recorded_at)
                THEN recorded_at + interval '15 minutes'
            ELSE windows.blocked_until
        END,
        updated_at = recorded_at
    WHERE windows.dimension = p_dimension
      AND windows.key_version = p_key_version
      AND windows.subject_fingerprint = p_subject_fingerprint
    RETURNING windows.blocked_until INTO resulting_blocked_until;

    RETURN ROW(counted_failures, resulting_blocked_until)::control_auth_failure_result;
END;
$$;
-- +goose StatementEnd

CREATE TABLE audit_logs (
    audit_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    occurred_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    category text NOT NULL,
    action text NOT NULL,
    result text NOT NULL,
    actor_admin_id uuid,
    target_admin_id uuid,
    actor_fingerprint bytea,
    source_fingerprint bytea,
    reason text,
    request_id text NOT NULL,
    details jsonb NOT NULL DEFAULT '{}'::jsonb,
    CONSTRAINT audit_logs_category_valid CHECK (
        category IN ('bootstrap', 'administrator', 'password', 'mfa', 'session', 'reauthentication', 'authorization', 'rate_limit')
    ),
    CONSTRAINT audit_logs_action_valid CHECK (
        action IN (
            'bootstrap.start', 'bootstrap.complete', 'bootstrap.reset',
            'auth.login_password', 'auth.login_mfa', 'auth.logout',
            'auth.password_change', 'auth.mfa_enroll', 'auth.mfa_reset',
            'auth.recovery_code_use', 'auth.recovery_codes_regenerate',
            'session.create', 'session.revoke', 'auth.reauthenticate',
            'administrator.create', 'administrator.activate', 'administrator.disable',
            'administrator.activation_token_generate', 'authorization.check',
            'auth.rate_limit', 'auth.csrf'
        )
    ),
    CONSTRAINT audit_logs_result_valid CHECK (result IN ('success', 'failure', 'denied', 'rate_limited')),
    CONSTRAINT audit_logs_actor_fingerprint_valid CHECK (
        actor_fingerprint IS NULL OR octet_length(actor_fingerprint) = 32
    ),
    CONSTRAINT audit_logs_source_fingerprint_valid CHECK (
        source_fingerprint IS NULL OR octet_length(source_fingerprint) = 32
    ),
    CONSTRAINT audit_logs_reason_valid CHECK (
        reason IS NULL OR (reason = btrim(reason) AND char_length(reason) BETWEEN 10 AND 500)
    ),
    CONSTRAINT audit_logs_request_id_valid CHECK (
        request_id = btrim(request_id) AND char_length(request_id) BETWEEN 1 AND 128
    ),
    CONSTRAINT audit_logs_details_valid CHECK (
        jsonb_typeof(details) = 'object'
        AND octet_length(details::text) <= 4096
        AND NOT (details ?| ARRAY[
            'password', 'password_phc', 'totp_secret', 'totp_code', 'recovery_code',
            'session_token', 'csrf_token', 'activation_token', 'bootstrap_secret', 'cookie',
            'login_name', 'display_name', 'ip', 'source_ip'
        ])
    )
);
CREATE INDEX audit_logs_occurred_at_idx ON audit_logs (occurred_at DESC);
CREATE INDEX audit_logs_actor_idx ON audit_logs (actor_admin_id, occurred_at DESC)
    WHERE actor_admin_id IS NOT NULL;
CREATE INDEX audit_logs_target_idx ON audit_logs (target_admin_id, occurred_at DESC)
    WHERE target_admin_id IS NOT NULL;
CREATE INDEX audit_logs_request_id_idx ON audit_logs (request_id);

-- +goose StatementBegin
CREATE FUNCTION control_reject_audit_mutation() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'audit_logs are immutable' USING ERRCODE = '42501';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER audit_logs_reject_update_delete
BEFORE UPDATE OR DELETE ON audit_logs
FOR EACH ROW EXECUTE FUNCTION control_reject_audit_mutation();
CREATE TRIGGER audit_logs_reject_truncate
BEFORE TRUNCATE ON audit_logs
FOR EACH STATEMENT EXECUTE FUNCTION control_reject_audit_mutation();

-- The cluster administrator provisions this fixed NOLOGIN capability role and
-- grants it to the environment-specific product login before running migrations.
-- Keeping role creation outside the migration avoids requiring CREATEROLE in the
-- migration account while still making a missing privilege boundary fail closed.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'relay_control_runtime') THEN
        RAISE EXCEPTION 'database role relay_control_runtime must be provisioned before migration'
            USING ERRCODE = '42704';
    END IF;
END;
$$;
-- +goose StatementEnd

REVOKE ALL ON TABLE
    environments,
    control_admin_users,
    control_admin_safety_guard,
    control_bootstrap_state,
    control_admin_passwords,
    control_admin_totp,
    control_admin_recovery_codes,
    control_admin_activation_tokens,
    control_auth_challenges,
    control_admin_sessions,
    control_auth_failure_windows,
    control_auth_failure_events,
    audit_logs
FROM relay_control_runtime;

GRANT USAGE ON SCHEMA public TO relay_control_runtime;
GRANT SELECT, INSERT ON TABLE environments TO relay_control_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE control_admin_users TO relay_control_runtime;
GRANT SELECT, UPDATE ON TABLE control_bootstrap_state TO relay_control_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE
    control_admin_passwords,
    control_admin_totp,
    control_admin_recovery_codes,
    control_admin_activation_tokens,
    control_auth_challenges,
    control_auth_failure_windows,
    control_auth_failure_events
TO relay_control_runtime;
GRANT SELECT, INSERT, UPDATE ON TABLE control_admin_sessions TO relay_control_runtime;
GRANT SELECT, INSERT ON TABLE audit_logs TO relay_control_runtime;
GRANT USAGE, SELECT ON SEQUENCE control_auth_failure_events_failure_event_id_seq
    TO relay_control_runtime;

REVOKE EXECUTE ON FUNCTION control_protect_enabled_admin() FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION control_enforce_bootstrap_transition() FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION control_record_auth_failure(text, integer, bytea, integer) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION control_reject_audit_mutation() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION control_protect_enabled_admin() TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION control_enforce_bootstrap_transition() TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION control_record_auth_failure(text, integer, bytea, integer)
    TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION control_reject_audit_mutation() TO relay_control_runtime;

REVOKE UPDATE, DELETE, TRUNCATE ON audit_logs FROM PUBLIC;
REVOKE UPDATE, DELETE, TRUNCATE ON audit_logs FROM relay_control_runtime;

-- +goose Down
REVOKE ALL ON TABLE
    environments,
    control_admin_users,
    control_admin_safety_guard,
    control_bootstrap_state,
    control_admin_passwords,
    control_admin_totp,
    control_admin_recovery_codes,
    control_admin_activation_tokens,
    control_auth_challenges,
    control_admin_sessions,
    control_auth_failure_windows,
    control_auth_failure_events,
    audit_logs
FROM relay_control_runtime;
REVOKE USAGE, SELECT ON SEQUENCE control_auth_failure_events_failure_event_id_seq
    FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION control_protect_enabled_admin() FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION control_enforce_bootstrap_transition() FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION control_record_auth_failure(text, integer, bytea, integer)
    FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION control_reject_audit_mutation() FROM relay_control_runtime;
DROP TRIGGER audit_logs_reject_truncate ON audit_logs;
DROP TRIGGER audit_logs_reject_update_delete ON audit_logs;
DROP FUNCTION control_reject_audit_mutation();
DROP TABLE audit_logs;
DROP FUNCTION control_record_auth_failure(text, integer, bytea, integer);
DROP TYPE control_auth_failure_result;
DROP TABLE control_auth_failure_events;
DROP TABLE control_auth_failure_windows;
DROP TABLE control_admin_sessions;
DROP TABLE control_auth_challenges;
DROP TABLE control_admin_activation_tokens;
DROP TABLE control_admin_recovery_codes;
DROP TABLE control_admin_totp;
DROP TABLE control_admin_passwords;
DROP TRIGGER control_bootstrap_state_transition_guard ON control_bootstrap_state;
DROP FUNCTION control_enforce_bootstrap_transition();
DROP TABLE control_bootstrap_state;
DROP TRIGGER control_admin_users_protect_last_enabled ON control_admin_users;
DROP FUNCTION control_protect_enabled_admin();
DROP TABLE control_admin_safety_guard;
DROP TABLE control_admin_users;
