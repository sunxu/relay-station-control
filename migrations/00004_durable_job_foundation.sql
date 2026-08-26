-- +goose Up

CREATE TABLE async_job_kinds (
    job_kind text NOT NULL,
    payload_schema_version integer NOT NULL,
    default_timeout_seconds integer NOT NULL,
    lease_seconds integer NOT NULL,
    heartbeat_interval_seconds integer NOT NULL,
    default_max_attempts integer NOT NULL,
    default_max_verification_attempts integer NOT NULL,
    replay_safe boolean NOT NULL DEFAULT false,
    rollback_allowed boolean NOT NULL DEFAULT false,
    lifecycle_status text NOT NULL DEFAULT 'active',
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (job_kind, payload_schema_version),
    CONSTRAINT async_job_kinds_name_valid CHECK (
        octet_length(job_kind) BETWEEN 1 AND 64
        AND job_kind ~ '^[a-z][a-z0-9._-]*$'
    ),
    CONSTRAINT async_job_kinds_schema_version_valid CHECK (payload_schema_version BETWEEN 1 AND 2147483647),
    CONSTRAINT async_job_kinds_timeout_valid CHECK (default_timeout_seconds BETWEEN 1 AND 86400),
    CONSTRAINT async_job_kinds_lease_valid CHECK (lease_seconds BETWEEN 5 AND 3600),
    CONSTRAINT async_job_kinds_heartbeat_valid CHECK (
        heartbeat_interval_seconds BETWEEN 1 AND lease_seconds - 1
    ),
    CONSTRAINT async_job_kinds_attempts_valid CHECK (default_max_attempts BETWEEN 1 AND 100),
    CONSTRAINT async_job_kinds_verification_attempts_valid CHECK (
        default_max_verification_attempts BETWEEN 1 AND 100
    ),
    CONSTRAINT async_job_kinds_lifecycle_valid CHECK (lifecycle_status IN ('active', 'retired'))
);

-- A database-level second line of defence. The Go registry remains responsible
-- for schema-specific field validation; this rejects credential-shaped keys at
-- every nesting level and lets CHECK constraints fail closed on malformed data.
-- +goose StatementBegin
CREATE FUNCTION public.control_job_payload_is_safe(candidate jsonb) RETURNS boolean
LANGUAGE plpgsql
IMMUTABLE STRICT
SET search_path = pg_catalog
AS $$
DECLARE
    member record;
    element jsonb;
BEGIN
    IF jsonb_typeof(candidate) = 'object' THEN
        FOR member IN SELECT key, value FROM jsonb_each(candidate) LOOP
            IF lower(member.key) ~ '(password|passwd|secret|token|credential|authorization|cookie|private[_-]?key|api[_-]?key|raw[_-]?response|command)'
               OR NOT public.control_job_payload_is_safe(member.value) THEN
                RETURN false;
            END IF;
        END LOOP;
    ELSIF jsonb_typeof(candidate) = 'array' THEN
        FOR element IN SELECT value FROM jsonb_array_elements(candidate) LOOP
            IF NOT public.control_job_payload_is_safe(element) THEN
                RETURN false;
            END IF;
        END LOOP;
    END IF;
    RETURN true;
END;
$$;
-- +goose StatementEnd

-- This is the database counterpart of Go's deterministic encoding for the
-- deliberately small payload type system (objects, arrays, strings, booleans,
-- signed integers and null). It also mirrors encoding/json HTML escaping.
-- +goose StatementBegin
CREATE FUNCTION public.control_job_payload_canonical(candidate jsonb) RETURNS text
LANGUAGE plpgsql
IMMUTABLE STRICT
SET search_path = pg_catalog
AS $$
DECLARE
    result text;
BEGIN
    CASE jsonb_typeof(candidate)
    WHEN 'object' THEN
        SELECT '{' || COALESCE(string_agg(
            replace(replace(replace(to_jsonb(member.key)::text, '<', '\u003c'), '>', '\u003e'), '&', '\u0026')
            || ':' || public.control_job_payload_canonical(member.value),
            ',' ORDER BY member.key COLLATE "C"
        ), '') || '}' INTO result
        FROM jsonb_each(candidate) AS member;
    WHEN 'array' THEN
        SELECT '[' || COALESCE(string_agg(
            public.control_job_payload_canonical(element.value),
            ',' ORDER BY element.ordinality
        ), '') || ']' INTO result
        FROM jsonb_array_elements(candidate) WITH ORDINALITY AS element(value, ordinality);
    WHEN 'string' THEN
        result := replace(replace(replace(candidate::text, '<', '\u003c'), '>', '\u003e'), '&', '\u0026');
        result := replace(replace(result, chr(8232), '\u2028'), chr(8233), '\u2029');
    ELSE
        result := candidate::text;
    END CASE;
    RETURN result;
END;
$$;
-- +goose StatementEnd

CREATE TABLE async_jobs (
    job_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    idempotency_key text NOT NULL UNIQUE,
    job_kind text NOT NULL,
    payload_schema_version integer NOT NULL,
    operation_id uuid NOT NULL,
    payload jsonb NOT NULL,
    payload_hash bytea NOT NULL,
    status text NOT NULL DEFAULT 'pending',
    priority smallint NOT NULL DEFAULT 50,
    attempt_count integer NOT NULL DEFAULT 0,
    verification_attempt integer NOT NULL DEFAULT 0,
    max_attempts integer NOT NULL,
    max_verification_attempts integer NOT NULL,
    timeout_seconds integer NOT NULL,
    lease_seconds integer NOT NULL,
    heartbeat_interval_seconds integer NOT NULL,
    replay_safe boolean NOT NULL,
    rollback_allowed boolean NOT NULL,
    available_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deadline_at timestamptz NOT NULL,
    started_at timestamptz,
    completed_at timestamptz,
    cancel_requested_at timestamptz,
    error_code text,
    error_summary text,
    lease_owner text,
    lease_fencing_token uuid,
    lease_expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT async_jobs_kind_fk FOREIGN KEY (job_kind, payload_schema_version)
        REFERENCES async_job_kinds (job_kind, payload_schema_version) ON DELETE RESTRICT,
    CONSTRAINT async_jobs_idempotency_key_valid CHECK (
        octet_length(idempotency_key) BETWEEN 1 AND 255
        AND idempotency_key = btrim(idempotency_key)
        AND idempotency_key !~ '[[:cntrl:]]'
    ),
    CONSTRAINT async_jobs_payload_valid CHECK (
        jsonb_typeof(payload) = 'object'
        AND octet_length(public.control_job_payload_canonical(payload)) <= 65536
        AND public.control_job_payload_is_safe(payload)
    ),
    CONSTRAINT async_jobs_payload_hash_valid CHECK (
        octet_length(payload_hash) = 32
        AND payload_hash = sha256(convert_to(public.control_job_payload_canonical(payload), 'UTF8'))
    ),
    CONSTRAINT async_jobs_status_valid CHECK (status IN (
        'pending', 'running', 'verifying', 'retry_wait', 'rolling_back',
        'succeeded', 'failed', 'rolled_back', 'cancelled'
    )),
    CONSTRAINT async_jobs_priority_valid CHECK (priority BETWEEN 0 AND 100),
    CONSTRAINT async_jobs_attempts_valid CHECK (
        max_attempts BETWEEN 1 AND 100 AND attempt_count BETWEEN 0 AND max_attempts
        AND max_verification_attempts BETWEEN 1 AND 100
        AND verification_attempt BETWEEN 0 AND max_verification_attempts
    ),
    CONSTRAINT async_jobs_execution_policy_valid CHECK (
        timeout_seconds BETWEEN 1 AND 86400 AND lease_seconds BETWEEN 5 AND 3600
        AND heartbeat_interval_seconds BETWEEN 1 AND lease_seconds - 1
    ),
    CONSTRAINT async_jobs_timestamps_valid CHECK (
        available_at >= created_at
        AND deadline_at > created_at
        AND deadline_at <= created_at + interval '30 days'
        AND updated_at >= created_at
        AND (started_at IS NULL OR started_at >= created_at)
        AND (completed_at IS NULL OR completed_at >= created_at)
        AND (cancel_requested_at IS NULL OR cancel_requested_at >= created_at)
    ),
    CONSTRAINT async_jobs_started_state_valid CHECK (
        (attempt_count = 0 AND started_at IS NULL)
        OR (attempt_count > 0 AND started_at IS NOT NULL)
    ),
    CONSTRAINT async_jobs_terminal_state_valid CHECK (
        (status IN ('succeeded', 'failed', 'rolled_back', 'cancelled') AND completed_at IS NOT NULL)
        OR (status NOT IN ('succeeded', 'failed', 'rolled_back', 'cancelled') AND completed_at IS NULL)
    ),
    CONSTRAINT async_jobs_lease_state_valid CHECK (
        (status = 'running'
            AND lease_owner IS NOT NULL
            AND lease_fencing_token IS NOT NULL
            AND lease_expires_at IS NOT NULL)
        OR (status IN ('verifying', 'rolling_back')
            AND (
                (lease_owner IS NOT NULL AND lease_fencing_token IS NOT NULL AND lease_expires_at IS NOT NULL)
                OR (lease_owner IS NULL AND lease_fencing_token IS NULL AND lease_expires_at IS NULL)
            ))
        OR (status NOT IN ('running', 'verifying', 'rolling_back')
            AND lease_owner IS NULL
            AND lease_fencing_token IS NULL
            AND lease_expires_at IS NULL)
    ),
    CONSTRAINT async_jobs_lease_owner_valid CHECK (
        lease_owner IS NULL OR (
            octet_length(lease_owner) BETWEEN 1 AND 128
            AND lease_owner ~ '^[A-Za-z0-9][A-Za-z0-9._:/-]*$'
        )
    ),
    CONSTRAINT async_jobs_error_valid CHECK (
        (error_code IS NULL OR (
            octet_length(error_code) BETWEEN 1 AND 64
            AND error_code ~ '^[a-z][a-z0-9._-]*$'
        ))
        AND (error_summary IS NULL OR (
            octet_length(error_summary) BETWEEN 1 AND 512
            AND error_summary !~ '[[:cntrl:]]'
        ))
    )
);

CREATE INDEX async_jobs_runnable_idx
    ON async_jobs (priority DESC, available_at, created_at, job_id)
    WHERE status IN ('pending', 'retry_wait') AND cancel_requested_at IS NULL;
CREATE INDEX async_jobs_expired_lease_idx
    ON async_jobs (lease_expires_at, created_at, job_id)
    WHERE status IN ('running', 'verifying', 'rolling_back');
CREATE INDEX async_jobs_list_idx ON async_jobs (created_at DESC, job_id DESC);
CREATE INDEX async_jobs_status_list_idx ON async_jobs (status, created_at DESC, job_id DESC);
CREATE INDEX async_jobs_kind_list_idx ON async_jobs (job_kind, created_at DESC, job_id DESC);

CREATE TABLE async_job_events (
    job_id uuid NOT NULL REFERENCES async_jobs(job_id) ON DELETE RESTRICT,
    sequence bigint NOT NULL,
    event_type text NOT NULL,
    from_status text,
    to_status text NOT NULL,
    attempt integer NOT NULL,
    reason_code text,
    error_code text,
    actor_type text NOT NULL,
    occurred_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (job_id, sequence),
    CONSTRAINT async_job_events_sequence_valid CHECK (sequence > 0),
    CONSTRAINT async_job_events_type_valid CHECK (event_type IN (
        'enqueued', 'claimed', 'retry_scheduled', 'verification_started',
        'rollback_started', 'succeeded', 'failed', 'rolled_back',
        'cancelled', 'cancel_requested'
    )),
    CONSTRAINT async_job_events_from_status_valid CHECK (
        from_status IS NULL OR from_status IN (
            'pending', 'running', 'verifying', 'retry_wait', 'rolling_back',
            'succeeded', 'failed', 'rolled_back', 'cancelled'
        )
    ),
    CONSTRAINT async_job_events_to_status_valid CHECK (to_status IN (
        'pending', 'running', 'verifying', 'retry_wait', 'rolling_back',
        'succeeded', 'failed', 'rolled_back', 'cancelled'
    )),
    CONSTRAINT async_job_events_attempt_valid CHECK (attempt BETWEEN 0 AND 100),
    CONSTRAINT async_job_events_reason_valid CHECK (
        reason_code IS NULL OR (
            octet_length(reason_code) BETWEEN 1 AND 64
            AND reason_code ~ '^[a-z][a-z0-9._-]*$'
        )
    ),
    CONSTRAINT async_job_events_error_valid CHECK (
        error_code IS NULL OR (
            octet_length(error_code) BETWEEN 1 AND 64
            AND error_code ~ '^[a-z][a-z0-9._-]*$'
        )
    ),
    CONSTRAINT async_job_events_actor_valid CHECK (actor_type IN ('service', 'worker', 'reconciler', 'system')),
    CONSTRAINT async_job_events_transition_valid CHECK (
        (event_type = 'enqueued' AND from_status IS NULL AND to_status = 'pending'
            AND attempt = 0 AND actor_type = 'service')
        OR (event_type = 'claimed' AND from_status IN ('pending', 'retry_wait')
            AND to_status = 'running' AND attempt > 0 AND actor_type = 'worker')
        OR (event_type = 'retry_scheduled' AND from_status IN ('running', 'verifying')
            AND to_status = 'retry_wait' AND actor_type IN ('worker', 'reconciler'))
        OR (event_type = 'verification_started' AND from_status IN ('running', 'verifying')
            AND to_status = 'verifying' AND actor_type IN ('worker', 'reconciler'))
        OR (event_type = 'rollback_started' AND from_status IN ('verifying', 'rolling_back')
            AND to_status = 'rolling_back' AND actor_type = 'reconciler')
        OR (event_type = 'succeeded' AND from_status = 'verifying'
            AND to_status = 'succeeded' AND actor_type = 'reconciler')
        OR (event_type = 'failed' AND from_status IN ('pending', 'retry_wait', 'running', 'verifying', 'rolling_back')
            AND to_status = 'failed' AND (
                actor_type = 'system'
                OR (actor_type = 'worker' AND from_status = 'running')
                OR (actor_type = 'reconciler' AND from_status IN ('verifying', 'rolling_back'))
            ))
        OR (event_type = 'rolled_back' AND from_status = 'rolling_back'
            AND to_status = 'rolled_back' AND actor_type = 'reconciler')
        OR (event_type = 'cancelled' AND to_status = 'cancelled' AND (
            (actor_type = 'service' AND from_status IN ('pending', 'retry_wait'))
            OR (actor_type = 'reconciler' AND from_status = 'verifying')
        ))
        OR (event_type = 'cancel_requested' AND from_status IN ('running', 'verifying', 'rolling_back')
            AND to_status = from_status AND actor_type = 'service')
    )
);

CREATE TABLE operation_outbox (
    event_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_key text NOT NULL UNIQUE,
    job_id uuid NOT NULL REFERENCES async_jobs(job_id) ON DELETE RESTRICT,
    operation_id uuid NOT NULL,
    topic text NOT NULL,
    envelope jsonb NOT NULL,
    status text NOT NULL,
    attempt_count integer NOT NULL DEFAULT 0,
    max_attempts integer NOT NULL DEFAULT 5,
    available_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    sent_at timestamptz,
    error_code text,
    lease_owner text,
    lease_fencing_token uuid,
    lease_expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT operation_outbox_event_key_valid CHECK (
        octet_length(event_key) BETWEEN 1 AND 255
        AND event_key = btrim(event_key)
        AND event_key !~ '[[:cntrl:]]'
    ),
    CONSTRAINT operation_outbox_topic_valid CHECK (topic = 'async_job_wake'),
    CONSTRAINT operation_outbox_envelope_valid CHECK (
        envelope = jsonb_build_object(
            'schema_version', 1,
            'event_id', event_id,
            'job_id', job_id,
            'operation_id', operation_id,
            'topic', topic
        )
    ),
    CONSTRAINT operation_outbox_status_valid CHECK (status IN ('pending', 'publishing', 'retry_wait', 'sent', 'failed', 'suppressed')),
    CONSTRAINT operation_outbox_attempts_valid CHECK (
        max_attempts BETWEEN 1 AND 100 AND attempt_count BETWEEN 0 AND max_attempts
    ),
    CONSTRAINT operation_outbox_timestamps_valid CHECK (
        available_at >= created_at
        AND updated_at >= created_at
        AND (sent_at IS NULL OR sent_at >= created_at)
    ),
    CONSTRAINT operation_outbox_terminal_valid CHECK (
        (status = 'sent' AND sent_at IS NOT NULL AND error_code IS NULL)
        OR (status <> 'sent' AND sent_at IS NULL)
    ),
    CONSTRAINT operation_outbox_lease_state_valid CHECK (
        (status = 'publishing' AND lease_owner IS NOT NULL AND lease_fencing_token IS NOT NULL AND lease_expires_at IS NOT NULL)
        OR (status <> 'publishing' AND lease_owner IS NULL AND lease_fencing_token IS NULL AND lease_expires_at IS NULL)
    ),
    CONSTRAINT operation_outbox_suppressed_valid CHECK (
        status <> 'suppressed' OR error_code = 'publisher_disabled'
    ),
    CONSTRAINT operation_outbox_error_valid CHECK (
        error_code IS NULL OR (
            octet_length(error_code) BETWEEN 1 AND 64
            AND error_code ~ '^[a-z][a-z0-9._-]*$'
        )
    ),
    CONSTRAINT operation_outbox_lease_owner_valid CHECK (
        lease_owner IS NULL OR (
            octet_length(lease_owner) BETWEEN 1 AND 128
            AND lease_owner ~ '^[A-Za-z0-9][A-Za-z0-9._:/-]*$'
        )
    )
);

CREATE INDEX operation_outbox_pending_idx
    ON operation_outbox (available_at, created_at, event_id)
    WHERE status IN ('pending', 'retry_wait');
CREATE INDEX operation_outbox_expired_lease_idx
    ON operation_outbox (lease_expires_at, created_at, event_id)
    WHERE status = 'publishing';
CREATE INDEX operation_outbox_job_idx ON operation_outbox (job_id, created_at, event_id);

-- +goose StatementBegin
CREATE FUNCTION public.control_reject_async_job_kind_mutation() RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
BEGIN
    RAISE EXCEPTION 'async job kind catalog is immutable' USING ERRCODE = '23514';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER async_job_kinds_immutable
BEFORE UPDATE OR DELETE ON async_job_kinds
FOR EACH ROW EXECUTE FUNCTION public.control_reject_async_job_kind_mutation();
CREATE TRIGGER async_job_kinds_truncate_immutable
BEFORE TRUNCATE ON async_job_kinds
FOR EACH STATEMENT EXECUTE FUNCTION public.control_reject_async_job_kind_mutation();

-- +goose StatementBegin
CREATE FUNCTION public.control_reject_async_job_event_mutation() RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
BEGIN
    RAISE EXCEPTION 'async job events are immutable' USING ERRCODE = '23514';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER async_job_events_immutable
BEFORE UPDATE OR DELETE ON async_job_events
FOR EACH ROW EXECUTE FUNCTION public.control_reject_async_job_event_mutation();
CREATE TRIGGER async_job_events_truncate_immutable
BEFORE TRUNCATE ON async_job_events
FOR EACH STATEMENT EXECUTE FUNCTION public.control_reject_async_job_event_mutation();

-- +goose StatementBegin
CREATE FUNCTION public.control_guard_async_job_mutation() RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
BEGIN
    IF OLD.status IN ('succeeded', 'failed', 'rolled_back', 'cancelled') THEN
        RAISE EXCEPTION 'terminal async job cannot be changed' USING ERRCODE = '23514';
    END IF;
    IF NEW.job_id IS DISTINCT FROM OLD.job_id
       OR NEW.idempotency_key IS DISTINCT FROM OLD.idempotency_key
       OR NEW.job_kind IS DISTINCT FROM OLD.job_kind
       OR NEW.payload_schema_version IS DISTINCT FROM OLD.payload_schema_version
       OR NEW.operation_id IS DISTINCT FROM OLD.operation_id
       OR NEW.payload IS DISTINCT FROM OLD.payload
       OR NEW.payload_hash IS DISTINCT FROM OLD.payload_hash
       OR NEW.priority IS DISTINCT FROM OLD.priority
       OR NEW.max_attempts IS DISTINCT FROM OLD.max_attempts
       OR NEW.max_verification_attempts IS DISTINCT FROM OLD.max_verification_attempts
       OR NEW.timeout_seconds IS DISTINCT FROM OLD.timeout_seconds
       OR NEW.lease_seconds IS DISTINCT FROM OLD.lease_seconds
       OR NEW.heartbeat_interval_seconds IS DISTINCT FROM OLD.heartbeat_interval_seconds
       OR NEW.replay_safe IS DISTINCT FROM OLD.replay_safe
       OR NEW.rollback_allowed IS DISTINCT FROM OLD.rollback_allowed
       OR NEW.deadline_at IS DISTINCT FROM OLD.deadline_at
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'async job identity and policy are immutable' USING ERRCODE = '23514';
    END IF;
    IF NOT (
        (OLD.status = 'pending' AND NEW.status IN ('running', 'failed', 'cancelled'))
        OR (OLD.status = 'retry_wait' AND NEW.status IN ('running', 'failed', 'cancelled'))
        OR (OLD.status = 'running' AND NEW.status IN ('running', 'verifying', 'retry_wait', 'failed'))
        OR (OLD.status = 'verifying' AND NEW.status IN ('verifying', 'retry_wait', 'rolling_back', 'succeeded', 'failed', 'cancelled'))
        OR (OLD.status = 'rolling_back' AND NEW.status IN ('rolling_back', 'rolled_back', 'failed'))
    ) THEN
        RAISE EXCEPTION 'async job state transition is invalid' USING ERRCODE = '23514';
    END IF;
    NEW.updated_at := clock_timestamp();
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER async_jobs_mutation_guard
BEFORE UPDATE ON async_jobs
FOR EACH ROW EXECUTE FUNCTION public.control_guard_async_job_mutation();

-- +goose StatementBegin
CREATE FUNCTION public.control_guard_operation_outbox_mutation() RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
BEGIN
    IF OLD.status IN ('sent', 'failed', 'suppressed') THEN
        RAISE EXCEPTION 'terminal outbox event cannot be changed' USING ERRCODE = '23514';
    END IF;
    IF NEW.event_id IS DISTINCT FROM OLD.event_id
       OR NEW.event_key IS DISTINCT FROM OLD.event_key
       OR NEW.job_id IS DISTINCT FROM OLD.job_id
       OR NEW.operation_id IS DISTINCT FROM OLD.operation_id
       OR NEW.topic IS DISTINCT FROM OLD.topic
       OR NEW.envelope IS DISTINCT FROM OLD.envelope
       OR NEW.max_attempts IS DISTINCT FROM OLD.max_attempts
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'outbox event identity is immutable' USING ERRCODE = '23514';
    END IF;
    IF NOT (
        (OLD.status IN ('pending', 'retry_wait') AND NEW.status IN ('publishing', 'failed'))
        OR (OLD.status = 'publishing' AND NEW.status IN ('publishing', 'retry_wait', 'sent', 'failed'))
    ) THEN
        RAISE EXCEPTION 'outbox state transition is invalid' USING ERRCODE = '23514';
    END IF;
    NEW.updated_at := clock_timestamp();
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER operation_outbox_mutation_guard
BEFORE UPDATE ON operation_outbox
FOR EACH ROW EXECUTE FUNCTION public.control_guard_operation_outbox_mutation();

-- No job kind is registered here. A future reviewed business migration must add
-- the matching catalog row and code registry entry together.

-- +goose StatementBegin
CREATE FUNCTION public.control_enqueue_async_job(
    enqueue_job_id uuid,
    enqueue_idempotency_key text,
    enqueue_job_kind text,
    enqueue_payload_schema_version integer,
    enqueue_operation_id uuid,
    enqueue_payload jsonb,
    enqueue_payload_hash bytea,
    enqueue_priority smallint,
    enqueue_outbox_event_id uuid,
    enqueue_outbox_event_key text,
    publisher_enabled boolean
) RETURNS SETOF public.async_jobs
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    kind_record public.async_job_kinds%ROWTYPE;
    job_record public.async_jobs%ROWTYPE;
    outbox_event_id uuid;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended(enqueue_idempotency_key, 724441091));
    SELECT * INTO kind_record
    FROM public.async_job_kinds
    WHERE job_kind = enqueue_job_kind
      AND payload_schema_version = enqueue_payload_schema_version
      AND lifecycle_status = 'active';
    IF NOT FOUND THEN
        RAISE EXCEPTION 'async job kind is not registered' USING ERRCODE = '22023';
    END IF;
    IF enqueue_job_id IS NULL OR enqueue_operation_id IS NULL
       OR enqueue_outbox_event_id IS NULL OR enqueue_outbox_event_key IS NULL
       OR publisher_enabled IS NULL THEN
        RAISE EXCEPTION 'async job enqueue input is invalid' USING ERRCODE = '22023';
    END IF;

    SELECT * INTO job_record
    FROM public.async_jobs
    WHERE idempotency_key = enqueue_idempotency_key
    FOR UPDATE;
    IF FOUND THEN
        IF job_record.job_kind IS DISTINCT FROM enqueue_job_kind
           OR job_record.payload_schema_version IS DISTINCT FROM enqueue_payload_schema_version
           OR job_record.operation_id IS DISTINCT FROM enqueue_operation_id
           OR job_record.payload_hash IS DISTINCT FROM enqueue_payload_hash
           OR job_record.payload IS DISTINCT FROM enqueue_payload
           OR job_record.priority IS DISTINCT FROM enqueue_priority
           OR job_record.max_attempts IS DISTINCT FROM kind_record.default_max_attempts
           OR job_record.max_verification_attempts IS DISTINCT FROM kind_record.default_max_verification_attempts
           OR job_record.timeout_seconds IS DISTINCT FROM kind_record.default_timeout_seconds
           OR job_record.lease_seconds IS DISTINCT FROM kind_record.lease_seconds
           OR job_record.heartbeat_interval_seconds IS DISTINCT FROM kind_record.heartbeat_interval_seconds
           OR job_record.replay_safe IS DISTINCT FROM kind_record.replay_safe
           OR job_record.rollback_allowed IS DISTINCT FROM kind_record.rollback_allowed THEN
            RAISE EXCEPTION 'async job idempotency conflict' USING ERRCODE = '23505';
        END IF;
        RETURN NEXT job_record;
        RETURN;
    END IF;

    INSERT INTO public.async_jobs (
        job_id, idempotency_key, job_kind, payload_schema_version, operation_id,
        payload, payload_hash, priority, max_attempts, max_verification_attempts,
        timeout_seconds, lease_seconds, heartbeat_interval_seconds,
        replay_safe, rollback_allowed, deadline_at
    ) VALUES (
        enqueue_job_id, enqueue_idempotency_key, enqueue_job_kind, enqueue_payload_schema_version,
        enqueue_operation_id, enqueue_payload, enqueue_payload_hash,
        enqueue_priority, kind_record.default_max_attempts,
        kind_record.default_max_verification_attempts,
        kind_record.default_timeout_seconds, kind_record.lease_seconds,
        kind_record.heartbeat_interval_seconds,
        kind_record.replay_safe, kind_record.rollback_allowed,
        CURRENT_TIMESTAMP + interval '30 days'
    ) RETURNING * INTO job_record;

    INSERT INTO public.async_job_events (
        job_id, sequence, event_type, from_status, to_status, attempt,
        reason_code, actor_type
    ) VALUES (
        job_record.job_id, 1, 'enqueued', NULL, 'pending', 0,
        'job_enqueued', 'service'
    );

    outbox_event_id := enqueue_outbox_event_id;
    INSERT INTO public.operation_outbox (
        event_id, event_key, job_id, operation_id, topic, envelope,
        status, error_code
    ) VALUES (
        outbox_event_id, enqueue_outbox_event_key,
        job_record.job_id, job_record.operation_id, 'async_job_wake',
        jsonb_build_object(
            'schema_version', 1,
            'event_id', outbox_event_id,
            'job_id', job_record.job_id,
            'operation_id', job_record.operation_id,
            'topic', 'async_job_wake'
        ),
        CASE WHEN publisher_enabled THEN 'pending' ELSE 'suppressed' END,
        CASE WHEN publisher_enabled THEN NULL ELSE 'publisher_disabled' END
    );

    RETURN NEXT job_record;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_get_async_job_by_idempotency_key(lookup_key text)
RETURNS SETOF public.async_jobs
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended(lookup_key, 724441091));
    RETURN QUERY
    SELECT job.* FROM public.async_jobs AS job WHERE job.idempotency_key = lookup_key;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_lock_async_job(lookup_job_id uuid)
RETURNS SETOF public.async_jobs
LANGUAGE sql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
    SELECT job.* FROM public.async_jobs AS job
    WHERE job.job_id = lookup_job_id
    FOR UPDATE
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_request_async_job_cancel(
    requested_job_id uuid,
    cancel_reason_code text
) RETURNS SETOF public.async_jobs
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    before_record public.async_jobs%ROWTYPE;
    after_record public.async_jobs%ROWTYPE;
    next_sequence bigint;
BEGIN
    SELECT * INTO before_record FROM public.async_jobs
    WHERE job_id = requested_job_id FOR UPDATE;
    IF NOT FOUND THEN
        RETURN;
    END IF;
    IF before_record.status IN ('succeeded', 'failed', 'rolled_back', 'cancelled') THEN
        RAISE EXCEPTION 'terminal async job cannot be cancelled' USING ERRCODE = '23514';
    END IF;
    SELECT COALESCE(max(sequence), 0) + 1 INTO next_sequence
    FROM public.async_job_events WHERE job_id = requested_job_id;
    IF before_record.status IN ('pending', 'retry_wait') THEN
        UPDATE public.async_jobs SET
            status = 'cancelled', cancel_requested_at = clock_timestamp(),
            completed_at = clock_timestamp(), lease_owner = NULL,
            lease_fencing_token = NULL, lease_expires_at = NULL
        WHERE job_id = requested_job_id RETURNING * INTO after_record;
        INSERT INTO public.async_job_events (
            job_id, sequence, event_type, from_status, to_status, attempt,
            reason_code, actor_type
        ) VALUES (
            requested_job_id, next_sequence, 'cancelled', before_record.status,
            'cancelled', before_record.attempt_count, cancel_reason_code, 'service'
        );
    ELSE
        UPDATE public.async_jobs SET
            cancel_requested_at = COALESCE(cancel_requested_at, clock_timestamp())
        WHERE job_id = requested_job_id RETURNING * INTO after_record;
        IF before_record.cancel_requested_at IS NULL THEN
            INSERT INTO public.async_job_events (
                job_id, sequence, event_type, from_status, to_status, attempt,
                reason_code, actor_type
            ) VALUES (
                requested_job_id, next_sequence, 'cancel_requested', before_record.status,
                before_record.status, before_record.attempt_count, cancel_reason_code, 'service'
            );
        END IF;
    END IF;
    RETURN NEXT after_record;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_claim_async_job(claim_owner text, claim_fencing_token uuid)
RETURNS SETOF public.async_jobs
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    job_record public.async_jobs%ROWTYPE;
    prior_status text;
    next_sequence bigint;
BEGIN
    SELECT job.* INTO job_record
    FROM public.async_jobs AS job
    JOIN public.async_job_kinds AS kind
      ON kind.job_kind = job.job_kind
     AND kind.payload_schema_version = job.payload_schema_version
    WHERE job.status IN ('pending', 'retry_wait')
      AND kind.lifecycle_status <> 'active'
    ORDER BY job.created_at, job.job_id
    FOR UPDATE OF job SKIP LOCKED LIMIT 1;
    IF FOUND THEN
        prior_status := job_record.status;
        SELECT COALESCE(max(sequence), 0) + 1 INTO next_sequence
        FROM public.async_job_events WHERE job_id = job_record.job_id;
        UPDATE public.async_jobs SET
            status = 'failed', completed_at = clock_timestamp(),
            error_code = 'job_kind_inactive', error_summary = NULL,
            lease_owner = NULL, lease_fencing_token = NULL, lease_expires_at = NULL
        WHERE job_id = job_record.job_id RETURNING * INTO job_record;
        INSERT INTO public.async_job_events (
            job_id, sequence, event_type, from_status, to_status, attempt,
            reason_code, error_code, actor_type
        ) VALUES (
            job_record.job_id, next_sequence, 'failed', prior_status, 'failed',
            job_record.attempt_count, 'job_kind_inactive', 'job_kind_inactive', 'system'
        );
    END IF;

    SELECT job.* INTO job_record
    FROM public.async_jobs AS job
    WHERE job.status IN ('pending', 'retry_wait')
      AND (job.deadline_at <= clock_timestamp() OR job.attempt_count >= job.max_attempts)
    ORDER BY job.deadline_at, job.created_at, job.job_id
    FOR UPDATE SKIP LOCKED LIMIT 1;
    IF FOUND THEN
        prior_status := job_record.status;
        SELECT COALESCE(max(sequence), 0) + 1 INTO next_sequence
        FROM public.async_job_events WHERE job_id = job_record.job_id;
        UPDATE public.async_jobs SET
            status = 'failed', completed_at = clock_timestamp(),
            error_code = CASE WHEN deadline_at <= clock_timestamp()
                THEN 'deadline_exceeded' ELSE 'max_attempts_exhausted' END,
            error_summary = NULL,
            lease_owner = NULL, lease_fencing_token = NULL, lease_expires_at = NULL
        WHERE job_id = job_record.job_id RETURNING * INTO job_record;
        INSERT INTO public.async_job_events (
            job_id, sequence, event_type, from_status, to_status, attempt,
            reason_code, error_code, actor_type
        ) VALUES (
            job_record.job_id, next_sequence, 'failed', prior_status, 'failed',
            job_record.attempt_count, job_record.error_code, job_record.error_code, 'system'
        );
    END IF;

    SELECT job.* INTO job_record
    FROM public.async_jobs AS job
    JOIN public.async_job_kinds AS kind
      ON kind.job_kind = job.job_kind
     AND kind.payload_schema_version = job.payload_schema_version
     AND kind.lifecycle_status = 'active'
    WHERE job.status IN ('pending', 'retry_wait')
      AND job.cancel_requested_at IS NULL
      AND job.available_at <= clock_timestamp()
      AND job.deadline_at > clock_timestamp()
      AND job.attempt_count < job.max_attempts
    ORDER BY job.priority DESC, job.available_at, job.created_at, job.job_id
    FOR UPDATE OF job SKIP LOCKED
    LIMIT 1;
    IF NOT FOUND THEN
        RETURN;
    END IF;
    prior_status := job_record.status;
    SELECT COALESCE(max(sequence), 0) + 1 INTO next_sequence
    FROM public.async_job_events WHERE job_id = job_record.job_id;
    UPDATE public.async_jobs SET
        status = 'running', attempt_count = attempt_count + 1,
        started_at = COALESCE(started_at, clock_timestamp()),
        lease_owner = claim_owner, lease_fencing_token = claim_fencing_token,
        lease_expires_at = clock_timestamp() + make_interval(secs => lease_seconds),
        error_code = NULL, error_summary = NULL
    WHERE job_id = job_record.job_id RETURNING * INTO job_record;
    INSERT INTO public.async_job_events (
        job_id, sequence, event_type, from_status, to_status, attempt,
        reason_code, actor_type
    ) VALUES (
        job_record.job_id, next_sequence, 'claimed',
        prior_status,
        'running', job_record.attempt_count, 'worker_claimed', 'worker'
    );
    RETURN NEXT job_record;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_renew_async_job_lease(
    renew_job_id uuid,
    renew_fencing_token uuid,
    renew_lease_seconds integer
) RETURNS SETOF public.async_jobs
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    job_record public.async_jobs%ROWTYPE;
    next_sequence bigint;
BEGIN
    IF renew_lease_seconds NOT BETWEEN 5 AND 3600 THEN
        RAISE EXCEPTION 'async job lease is invalid' USING ERRCODE = '22023';
    END IF;
    UPDATE public.async_jobs SET
        lease_expires_at = clock_timestamp() + make_interval(secs => renew_lease_seconds)
    WHERE job_id = renew_job_id
      AND status IN ('running', 'verifying', 'rolling_back')
      AND lease_fencing_token = renew_fencing_token
      AND lease_expires_at > clock_timestamp()
      AND lease_seconds = renew_lease_seconds
    RETURNING * INTO job_record;
    IF NOT FOUND THEN RETURN; END IF;
    RETURN NEXT job_record;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_transition_async_job_fenced(
    transition_job_id uuid,
    expected_status text,
    transition_fencing_token uuid,
    target_status text,
    transition_event_type text,
    retry_delay_seconds integer,
    transition_reason_code text,
    transition_error_code text,
    transition_error_summary text,
    transition_actor_type text,
    transition_release_lease boolean
) RETURNS SETOF public.async_jobs
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    before_record public.async_jobs%ROWTYPE;
    after_record public.async_jobs%ROWTYPE;
    next_sequence bigint;
    target_is_leased boolean;
    target_is_terminal boolean;
BEGIN
    SELECT * INTO before_record FROM public.async_jobs
    WHERE job_id = transition_job_id
      AND status = expected_status
      AND lease_fencing_token = transition_fencing_token
      AND lease_expires_at > clock_timestamp()
    FOR UPDATE;
    IF NOT FOUND THEN RETURN; END IF;
    IF transition_actor_type NOT IN ('worker', 'reconciler')
       OR retry_delay_seconds IS NULL OR retry_delay_seconds NOT BETWEEN 0 AND 86400
       OR transition_release_lease IS NULL THEN
        RAISE EXCEPTION 'async job transition input is invalid' USING ERRCODE = '22023';
    END IF;
    IF NOT (
        (transition_actor_type = 'worker'
            AND expected_status = 'running'
            AND target_status IN ('verifying', 'retry_wait', 'failed'))
        OR (transition_actor_type = 'reconciler'
            AND expected_status = 'verifying'
            AND target_status IN ('verifying', 'retry_wait', 'rolling_back', 'succeeded', 'failed', 'cancelled'))
        OR (transition_actor_type = 'reconciler'
            AND expected_status = 'rolling_back'
            AND target_status IN ('rolling_back', 'rolled_back', 'failed'))
    ) THEN
        RAISE EXCEPTION 'async job actor transition is invalid' USING ERRCODE = '23514';
    END IF;
    IF target_status = 'rolling_back' AND NOT before_record.rollback_allowed THEN
        RAISE EXCEPTION 'async job rollback is not permitted' USING ERRCODE = '23514';
    END IF;
    IF expected_status = 'verifying' AND target_status = 'retry_wait' AND NOT before_record.replay_safe THEN
        RAISE EXCEPTION 'async job replay is not permitted' USING ERRCODE = '23514';
    END IF;
    target_is_leased := target_status IN ('running', 'verifying', 'rolling_back') AND NOT transition_release_lease;
    target_is_terminal := target_status IN ('succeeded', 'failed', 'rolled_back', 'cancelled');
    SELECT COALESCE(max(sequence), 0) + 1 INTO next_sequence
    FROM public.async_job_events WHERE job_id = transition_job_id;
    UPDATE public.async_jobs SET
        status = target_status,
        available_at = CASE WHEN target_status IN ('retry_wait', 'verifying', 'rolling_back')
            THEN clock_timestamp() + make_interval(secs => retry_delay_seconds)
            ELSE available_at END,
        completed_at = CASE WHEN target_is_terminal THEN clock_timestamp() ELSE NULL END,
        error_code = transition_error_code,
        error_summary = transition_error_summary,
        lease_owner = CASE WHEN target_is_leased THEN lease_owner ELSE NULL END,
        lease_fencing_token = CASE WHEN target_is_leased THEN lease_fencing_token ELSE NULL END,
        lease_expires_at = CASE WHEN target_is_leased THEN lease_expires_at ELSE NULL END
    WHERE job_id = transition_job_id RETURNING * INTO after_record;
    INSERT INTO public.async_job_events (
        job_id, sequence, event_type, from_status, to_status, attempt,
        reason_code, error_code, actor_type
    ) VALUES (
        transition_job_id, next_sequence, transition_event_type, expected_status,
        target_status, after_record.attempt_count, transition_reason_code,
        transition_error_code, transition_actor_type
    );
    RETURN NEXT after_record;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_claim_expired_async_job(reconcile_owner text, reconcile_fencing_token uuid)
RETURNS SETOF public.async_jobs
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    job_record public.async_jobs%ROWTYPE;
    prior_status text;
    next_sequence bigint;
BEGIN
    SELECT job.* INTO job_record
    FROM public.async_jobs AS job
    WHERE job.verification_attempt >= job.max_verification_attempts
      AND (
          (job.status = 'running' AND job.lease_expires_at <= clock_timestamp())
          OR (job.status IN ('verifying', 'rolling_back') AND (
              job.lease_expires_at <= clock_timestamp()
              OR (job.lease_expires_at IS NULL AND job.available_at <= clock_timestamp())
          ))
      )
    ORDER BY job.lease_expires_at NULLS FIRST, job.available_at, job.created_at, job.job_id
    FOR UPDATE SKIP LOCKED LIMIT 1;
    IF FOUND THEN
        prior_status := job_record.status;
        SELECT COALESCE(max(sequence), 0) + 1 INTO next_sequence
        FROM public.async_job_events WHERE job_id = job_record.job_id;
        UPDATE public.async_jobs SET
            status = 'failed', completed_at = clock_timestamp(),
            error_code = CASE WHEN status = 'rolling_back'
                THEN 'rollback_exhausted' ELSE 'verification_exhausted' END,
            error_summary = NULL,
            lease_owner = NULL, lease_fencing_token = NULL, lease_expires_at = NULL
        WHERE job_id = job_record.job_id RETURNING * INTO job_record;
        INSERT INTO public.async_job_events (
            job_id, sequence, event_type, from_status, to_status, attempt,
            reason_code, error_code, actor_type
        ) VALUES (
            job_record.job_id, next_sequence, 'failed', prior_status, 'failed',
            job_record.attempt_count, job_record.error_code, job_record.error_code, 'system'
        );
    END IF;

    SELECT job.* INTO job_record
    FROM public.async_jobs AS job
    WHERE (
        (job.status = 'running' AND job.lease_expires_at <= clock_timestamp())
        OR (job.status IN ('verifying', 'rolling_back') AND (
            job.lease_expires_at <= clock_timestamp()
            OR (job.lease_expires_at IS NULL AND job.available_at <= clock_timestamp())
        ))
    ) AND job.verification_attempt < job.max_verification_attempts
    ORDER BY job.lease_expires_at NULLS FIRST, job.available_at, job.created_at, job.job_id
    FOR UPDATE SKIP LOCKED LIMIT 1;
    IF NOT FOUND THEN RETURN; END IF;
    prior_status := job_record.status;
    SELECT COALESCE(max(sequence), 0) + 1 INTO next_sequence
    FROM public.async_job_events WHERE job_id = job_record.job_id;
    UPDATE public.async_jobs SET
        status = CASE WHEN status = 'running' THEN 'verifying' ELSE status END,
        verification_attempt = verification_attempt + 1,
        lease_owner = reconcile_owner,
        lease_fencing_token = reconcile_fencing_token,
        lease_expires_at = clock_timestamp() + make_interval(secs => lease_seconds)
    WHERE job_id = job_record.job_id RETURNING * INTO job_record;
    INSERT INTO public.async_job_events (
        job_id, sequence, event_type, from_status, to_status, attempt,
        reason_code, actor_type
    ) VALUES (
        job_record.job_id, next_sequence,
        CASE WHEN prior_status = 'rolling_back' THEN 'rollback_started' ELSE 'verification_started' END,
        prior_status, job_record.status, job_record.attempt_count,
        'lease_expired', 'reconciler'
    );
    RETURN NEXT job_record;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_claim_outbox_event(dispatch_owner text, dispatch_fencing_token uuid, dispatch_lease_seconds integer)
RETURNS SETOF public.operation_outbox
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    event_record public.operation_outbox%ROWTYPE;
BEGIN
    IF dispatch_lease_seconds NOT BETWEEN 5 AND 3600 THEN
        RAISE EXCEPTION 'outbox lease is invalid' USING ERRCODE = '22023';
    END IF;
    SELECT event.* INTO event_record FROM public.operation_outbox AS event
    WHERE event.status IN ('pending', 'retry_wait', 'publishing')
      AND event.attempt_count >= event.max_attempts
      AND (event.status <> 'publishing' OR event.lease_expires_at <= clock_timestamp())
    ORDER BY event.available_at, event.created_at, event.event_id
    FOR UPDATE SKIP LOCKED LIMIT 1;
    IF FOUND THEN
        UPDATE public.operation_outbox SET
            status = 'failed', error_code = 'publish_attempts_exhausted',
            lease_owner = NULL, lease_fencing_token = NULL, lease_expires_at = NULL
        WHERE event_id = event_record.event_id;
    END IF;

    SELECT event.* INTO event_record FROM public.operation_outbox AS event
    WHERE (
        (event.status IN ('pending', 'retry_wait') AND event.available_at <= clock_timestamp())
        OR (event.status = 'publishing' AND event.lease_expires_at <= clock_timestamp())
    )
      AND event.attempt_count < event.max_attempts
    ORDER BY event.available_at, event.created_at, event.event_id
    FOR UPDATE SKIP LOCKED LIMIT 1;
    IF NOT FOUND THEN RETURN; END IF;
    UPDATE public.operation_outbox SET
        status = 'publishing', attempt_count = attempt_count + 1,
        lease_owner = dispatch_owner, lease_fencing_token = dispatch_fencing_token,
        lease_expires_at = clock_timestamp() + make_interval(secs => dispatch_lease_seconds),
        error_code = NULL
    WHERE event_id = event_record.event_id RETURNING * INTO event_record;
    RETURN NEXT event_record;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_transition_outbox_fenced(
    transition_event_id uuid,
    transition_fencing_token uuid,
    target_status text,
    retry_delay_seconds integer,
    transition_error_code text
) RETURNS SETOF public.operation_outbox
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    event_record public.operation_outbox%ROWTYPE;
BEGIN
    IF target_status NOT IN ('retry_wait', 'sent', 'failed')
       OR retry_delay_seconds IS NULL OR retry_delay_seconds NOT BETWEEN 0 AND 86400 THEN
        RAISE EXCEPTION 'outbox transition input is invalid' USING ERRCODE = '22023';
    END IF;
    UPDATE public.operation_outbox SET
        status = target_status,
        available_at = CASE WHEN target_status = 'retry_wait'
            THEN clock_timestamp() + make_interval(secs => retry_delay_seconds)
            ELSE available_at END,
        sent_at = CASE WHEN target_status = 'sent' THEN clock_timestamp() ELSE NULL END,
        error_code = transition_error_code,
        lease_owner = NULL, lease_fencing_token = NULL, lease_expires_at = NULL
    WHERE event_id = transition_event_id
      AND status = 'publishing'
      AND lease_fencing_token = transition_fencing_token
      AND lease_expires_at > clock_timestamp()
    RETURNING * INTO event_record;
    IF FOUND THEN RETURN NEXT event_record; END IF;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_renew_outbox_lease(
    renew_event_id uuid,
    renew_fencing_token uuid,
    renew_lease_seconds integer
) RETURNS SETOF public.operation_outbox
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    event_record public.operation_outbox%ROWTYPE;
BEGIN
    IF renew_lease_seconds NOT BETWEEN 5 AND 3600 THEN
        RAISE EXCEPTION 'outbox lease is invalid' USING ERRCODE = '22023';
    END IF;
    UPDATE public.operation_outbox SET
        lease_expires_at = clock_timestamp() + make_interval(secs => renew_lease_seconds)
    WHERE event_id = renew_event_id
      AND status = 'publishing'
      AND lease_fencing_token = renew_fencing_token
      AND lease_expires_at > clock_timestamp()
    RETURNING * INTO event_record;
    IF FOUND THEN RETURN NEXT event_record; END IF;
END;
$$;
-- +goose StatementEnd

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

REVOKE ALL ON TABLE async_job_kinds, async_jobs, async_job_events, operation_outbox
FROM PUBLIC, relay_control_runtime;
GRANT SELECT ON TABLE async_job_kinds TO relay_control_runtime;
GRANT SELECT (
    job_id, operation_id, job_kind, status, attempt_count, max_attempts,
    available_at, started_at, completed_at, cancel_requested_at, error_code,
    lease_expires_at, created_at, updated_at
) ON async_jobs TO relay_control_runtime;
GRANT SELECT (
    job_id, sequence, event_type, from_status, to_status, attempt,
    reason_code, error_code, actor_type, occurred_at
) ON async_job_events TO relay_control_runtime;
GRANT SELECT (event_id, job_id, status, attempt_count, max_attempts, available_at, sent_at, error_code, created_at, updated_at)
ON operation_outbox TO relay_control_runtime;

REVOKE EXECUTE ON FUNCTION public.control_job_payload_is_safe(jsonb) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_job_payload_canonical(jsonb) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_reject_async_job_kind_mutation() FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_reject_async_job_event_mutation() FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_guard_async_job_mutation() FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_guard_operation_outbox_mutation() FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_enqueue_async_job(uuid, text, text, integer, uuid, jsonb, bytea, smallint, uuid, text, boolean) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_get_async_job_by_idempotency_key(text) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_lock_async_job(uuid) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_request_async_job_cancel(uuid, text) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_claim_async_job(text, uuid) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_renew_async_job_lease(uuid, uuid, integer) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_transition_async_job_fenced(uuid, text, uuid, text, text, integer, text, text, text, text, boolean) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_claim_expired_async_job(text, uuid) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_claim_outbox_event(text, uuid, integer) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_transition_outbox_fenced(uuid, uuid, text, integer, text) FROM PUBLIC;
REVOKE EXECUTE ON FUNCTION public.control_renew_outbox_lease(uuid, uuid, integer) FROM PUBLIC;

GRANT EXECUTE ON FUNCTION public.control_enqueue_async_job(uuid, text, text, integer, uuid, jsonb, bytea, smallint, uuid, text, boolean) TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_get_async_job_by_idempotency_key(text) TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_lock_async_job(uuid) TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_request_async_job_cancel(uuid, text) TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_claim_async_job(text, uuid) TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_renew_async_job_lease(uuid, uuid, integer) TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_transition_async_job_fenced(uuid, text, uuid, text, text, integer, text, text, text, text, boolean) TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_claim_expired_async_job(text, uuid) TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_claim_outbox_event(text, uuid, integer) TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_transition_outbox_fenced(uuid, uuid, text, integer, text) TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_renew_outbox_lease(uuid, uuid, integer) TO relay_control_runtime;

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    LOCK TABLE public.async_job_kinds, public.async_jobs,
        public.async_job_events, public.operation_outbox
        IN ACCESS EXCLUSIVE MODE NOWAIT;
    IF EXISTS (SELECT 1 FROM public.async_job_kinds)
       OR EXISTS (SELECT 1 FROM public.async_jobs)
       OR EXISTS (SELECT 1 FROM public.async_job_events)
       OR EXISTS (SELECT 1 FROM public.operation_outbox) THEN
        RAISE EXCEPTION 'durable job evidence exists; refusing migration down'
            USING ERRCODE = '55000';
    END IF;
END;
$$;
-- +goose StatementEnd

REVOKE ALL ON TABLE async_job_kinds, async_jobs, async_job_events, operation_outbox
FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION public.control_job_payload_is_safe(jsonb) FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION public.control_job_payload_canonical(jsonb) FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION public.control_reject_async_job_kind_mutation() FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION public.control_reject_async_job_event_mutation() FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION public.control_guard_async_job_mutation() FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION public.control_guard_operation_outbox_mutation() FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION public.control_enqueue_async_job(uuid, text, text, integer, uuid, jsonb, bytea, smallint, uuid, text, boolean) FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION public.control_get_async_job_by_idempotency_key(text) FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION public.control_lock_async_job(uuid) FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION public.control_request_async_job_cancel(uuid, text) FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION public.control_claim_async_job(text, uuid) FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION public.control_renew_async_job_lease(uuid, uuid, integer) FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION public.control_transition_async_job_fenced(uuid, text, uuid, text, text, integer, text, text, text, text, boolean) FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION public.control_claim_expired_async_job(text, uuid) FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION public.control_claim_outbox_event(text, uuid, integer) FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION public.control_transition_outbox_fenced(uuid, uuid, text, integer, text) FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION public.control_renew_outbox_lease(uuid, uuid, integer) FROM relay_control_runtime;

DROP FUNCTION public.control_renew_outbox_lease(uuid, uuid, integer);
DROP FUNCTION public.control_transition_outbox_fenced(uuid, uuid, text, integer, text);
DROP FUNCTION public.control_claim_outbox_event(text, uuid, integer);
DROP FUNCTION public.control_claim_expired_async_job(text, uuid);
DROP FUNCTION public.control_transition_async_job_fenced(uuid, text, uuid, text, text, integer, text, text, text, text, boolean);
DROP FUNCTION public.control_renew_async_job_lease(uuid, uuid, integer);
DROP FUNCTION public.control_claim_async_job(text, uuid);
DROP FUNCTION public.control_request_async_job_cancel(uuid, text);
DROP FUNCTION public.control_lock_async_job(uuid);
DROP FUNCTION public.control_get_async_job_by_idempotency_key(text);
DROP FUNCTION public.control_enqueue_async_job(uuid, text, text, integer, uuid, jsonb, bytea, smallint, uuid, text, boolean);
DROP TRIGGER operation_outbox_mutation_guard ON operation_outbox;
DROP FUNCTION public.control_guard_operation_outbox_mutation();
DROP TRIGGER async_jobs_mutation_guard ON async_jobs;
DROP FUNCTION public.control_guard_async_job_mutation();
DROP TRIGGER async_job_events_truncate_immutable ON async_job_events;
DROP TRIGGER async_job_events_immutable ON async_job_events;
DROP FUNCTION public.control_reject_async_job_event_mutation();
DROP TRIGGER async_job_kinds_truncate_immutable ON async_job_kinds;
DROP TRIGGER async_job_kinds_immutable ON async_job_kinds;
DROP FUNCTION public.control_reject_async_job_kind_mutation();
DROP TABLE operation_outbox;
DROP TABLE async_job_events;
DROP TABLE async_jobs;
DROP TABLE async_job_kinds;
DROP FUNCTION public.control_job_payload_canonical(jsonb);
DROP FUNCTION public.control_job_payload_is_safe(jsonb);
