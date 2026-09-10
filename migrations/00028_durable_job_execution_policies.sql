-- +goose Up
-- Phase 5 Slice A: independent, default-off, immutable execution-policy snapshots.
-- Existing jobs retain false/false; no production job kind is registered here.
ALTER TABLE public.async_job_kinds
    ADD COLUMN allow_unknown_effect_replay boolean NOT NULL DEFAULT false,
    ADD COLUMN allow_direct_success boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT async_job_kinds_unknown_replay_safe
        CHECK (NOT allow_unknown_effect_replay OR replay_safe);
ALTER TABLE public.async_jobs
    ADD COLUMN allow_unknown_effect_replay boolean NOT NULL DEFAULT false,
    ADD COLUMN allow_direct_success boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT async_jobs_unknown_replay_safe
        CHECK (NOT allow_unknown_effect_replay OR replay_safe);

ALTER TABLE public.async_job_events DROP CONSTRAINT async_job_events_transition_valid;
ALTER TABLE public.async_job_events
ADD CONSTRAINT async_job_events_transition_valid CHECK (
        (event_type = 'enqueued' AND from_status IS NULL AND to_status = 'pending'
            AND attempt = 0 AND actor_type = 'service')
        OR (event_type = 'claimed' AND from_status IN ('pending', 'retry_wait')
            AND to_status = 'running' AND attempt > 0 AND actor_type = 'worker')
        OR (event_type = 'claimed' AND from_status = 'running'
            AND to_status = 'running' AND attempt > 0 AND actor_type = 'reconciler')
        OR (event_type = 'retry_scheduled' AND from_status IN ('running', 'verifying')
            AND to_status = 'retry_wait' AND actor_type IN ('worker', 'reconciler'))
        OR (event_type = 'verification_started' AND from_status IN ('running', 'verifying')
            AND to_status = 'verifying' AND actor_type IN ('worker', 'reconciler'))
        OR (event_type = 'rollback_started' AND from_status IN ('verifying', 'rolling_back')
            AND to_status = 'rolling_back' AND actor_type = 'reconciler')
        OR (event_type = 'succeeded' AND from_status = 'verifying'
            AND to_status = 'succeeded' AND actor_type = 'reconciler')
        OR (event_type = 'succeeded' AND from_status = 'running'
            AND to_status = 'succeeded' AND actor_type = 'worker')
        OR (event_type = 'failed' AND from_status IN ('pending', 'retry_wait', 'running', 'verifying', 'rolling_back')
            AND to_status = 'failed' AND (
                actor_type = 'system'
                OR (actor_type = 'worker' AND from_status = 'running')
                OR (actor_type = 'service' AND from_status = 'retry_wait')
                OR (actor_type = 'reconciler' AND from_status IN ('running', 'verifying', 'rolling_back'))
            ))
        OR (event_type = 'rolled_back' AND from_status = 'rolling_back'
            AND to_status = 'rolled_back' AND actor_type = 'reconciler')
        OR (event_type = 'cancelled' AND to_status = 'cancelled' AND (
            (actor_type = 'service' AND from_status IN ('pending', 'retry_wait'))
            OR (actor_type = 'reconciler' AND from_status = 'verifying')
            OR (actor_type = 'worker' AND from_status = 'running'
                AND reason_code IS NOT DISTINCT FROM 'execute_retryable_no_effect'
                AND error_code IS NOT DISTINCT FROM 'cancel_verified_safe')
        ))
        OR (event_type = 'cancel_requested' AND from_status IN ('running', 'verifying', 'rolling_back')
            AND to_status = from_status AND actor_type = 'service')
    );

-- Effect markers are framework proofs, not executor error strings or service
-- cancellation reasons. Keep them on their existing lifecycle paths.
ALTER TABLE public.async_job_events ADD CONSTRAINT async_job_events_effect_reason_valid CHECK (
    reason_code IS NULL OR CASE reason_code
        WHEN 'effect_unknown_unverified' THEN
            from_status = 'running' AND to_status IN ('retry_wait', 'failed')
            AND actor_type IN ('worker', 'reconciler')
        WHEN 'execute_retryable_no_effect' THEN
            from_status = 'running' AND to_status IN ('retry_wait', 'failed', 'cancelled')
            AND actor_type = 'worker'
        WHEN 'effect_absent_verified' THEN
            from_status = 'verifying' AND to_status IN ('retry_wait', 'failed', 'cancelled')
            AND actor_type = 'reconciler'
        ELSE true
    END
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_guard_async_job_mutation() RETURNS trigger
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
       OR NEW.allow_unknown_effect_replay IS DISTINCT FROM OLD.allow_unknown_effect_replay
       OR NEW.allow_direct_success IS DISTINCT FROM OLD.allow_direct_success
       OR NEW.rollback_allowed IS DISTINCT FROM OLD.rollback_allowed
       OR NEW.deadline_at IS DISTINCT FROM OLD.deadline_at
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'async job identity and policy are immutable' USING ERRCODE = '23514';
    END IF;
    IF NOT (
        (OLD.status = 'pending' AND NEW.status IN ('running', 'failed', 'cancelled'))
        OR (OLD.status = 'retry_wait' AND NEW.status IN ('running', 'failed', 'cancelled'))
        OR (OLD.status = 'running' AND NEW.status IN ('running', 'verifying', 'retry_wait', 'failed'))
        OR (OLD.status = 'running' AND NEW.status = 'succeeded'
            AND OLD.allow_direct_success AND OLD.cancel_requested_at IS NULL)
        OR (OLD.status = 'running' AND NEW.status = 'cancelled'
            AND OLD.cancel_requested_at IS NOT NULL
            AND OLD.lease_expires_at IS NOT NULL
            AND OLD.lease_expires_at > clock_timestamp()
            AND NEW.error_code IS NOT DISTINCT FROM 'cancel_verified_safe'
            AND NOT EXISTS (
                SELECT 1 FROM public.async_job_events e
                WHERE e.job_id = OLD.job_id AND e.reason_code = 'effect_unknown_unverified'
                  AND e.sequence > COALESCE((
                      SELECT max(v.sequence) FROM public.async_job_events v
                      WHERE v.job_id = OLD.job_id AND v.reason_code = 'effect_absent_verified'
                  ), 0)
            ))
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

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_enqueue_async_job(
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
           OR job_record.allow_unknown_effect_replay IS DISTINCT FROM kind_record.allow_unknown_effect_replay
           OR job_record.allow_direct_success IS DISTINCT FROM kind_record.allow_direct_success
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
        replay_safe, rollback_allowed, allow_unknown_effect_replay, allow_direct_success, deadline_at
    ) VALUES (
        enqueue_job_id, enqueue_idempotency_key, enqueue_job_kind, enqueue_payload_schema_version,
        enqueue_operation_id, enqueue_payload, enqueue_payload_hash,
        enqueue_priority, kind_record.default_max_attempts,
        kind_record.default_max_verification_attempts,
        kind_record.default_timeout_seconds, kind_record.lease_seconds,
        kind_record.heartbeat_interval_seconds,
        kind_record.replay_safe, kind_record.rollback_allowed,
        kind_record.allow_unknown_effect_replay, kind_record.allow_direct_success,
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
CREATE OR REPLACE FUNCTION public.control_transition_async_job_fenced(
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
    unresolved_unknown boolean;
BEGIN
    SELECT * INTO before_record FROM public.async_jobs
    WHERE job_id = transition_job_id
      AND status = expected_status
      AND lease_fencing_token = transition_fencing_token
      AND lease_expires_at > clock_timestamp()
    FOR UPDATE;
    IF NOT FOUND THEN RETURN; END IF;
    -- A lock wait may outlive the lease without changing the tuple.
    IF before_record.lease_expires_at <= clock_timestamp() THEN RETURN; END IF;
    IF transition_actor_type NOT IN ('worker', 'reconciler')
       OR retry_delay_seconds IS NULL OR retry_delay_seconds NOT BETWEEN 0 AND 86400
       OR transition_release_lease IS NULL THEN
        RAISE EXCEPTION 'async job transition input is invalid' USING ERRCODE = '22023';
    END IF;
    IF NOT (
        (transition_actor_type = 'worker'
            AND expected_status = 'running'
            AND (target_status IN ('verifying', 'retry_wait', 'failed')
                 OR target_status = 'cancelled'
                 OR (target_status = 'succeeded' AND before_record.allow_direct_success)))
        OR (transition_actor_type = 'reconciler'
            AND expected_status = 'running'
            AND before_record.allow_unknown_effect_replay
            AND target_status IN ('retry_wait', 'failed'))
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
    SELECT EXISTS (
        SELECT 1 FROM public.async_job_events e
        WHERE e.job_id = transition_job_id
          AND e.reason_code = 'effect_unknown_unverified'
          AND e.sequence > COALESCE((
              SELECT max(v.sequence) FROM public.async_job_events v
              WHERE v.job_id = transition_job_id AND v.reason_code = 'effect_absent_verified'
          ), 0)
    ) INTO unresolved_unknown;

    -- The current unknown proof has not been appended yet. Permission alone
    -- is never evidence, and a later no-effect attempt does not reset history.
    unresolved_unknown := unresolved_unknown OR COALESCE(transition_reason_code = 'effect_unknown_unverified', false);
    IF expected_status = 'running' AND target_status = 'retry_wait'
       AND transition_reason_code = 'effect_unknown_unverified'
       AND NOT before_record.allow_unknown_effect_replay THEN
        RAISE EXCEPTION 'async job unknown replay is not permitted' USING ERRCODE = '23514';
    END IF;
    IF expected_status = 'running' AND target_status = 'cancelled' AND (
        before_record.cancel_requested_at IS NULL
        OR transition_actor_type IS DISTINCT FROM 'worker'
        OR transition_reason_code IS DISTINCT FROM 'execute_retryable_no_effect'
        OR unresolved_unknown
    ) THEN
        RAISE EXCEPTION 'async job safe cancellation proof is required' USING ERRCODE = '23514';
    END IF;

    -- Recheck cancellation after taking the row lock, never just at claim.
    IF expected_status = 'running' AND target_status = 'succeeded'
       AND before_record.cancel_requested_at IS NOT NULL THEN
        target_status := 'failed';
        transition_event_type := 'failed';
        transition_release_lease := true;
        transition_error_code := 'cancel_after_effect_applied';
        transition_error_summary := NULL;
    ELSIF expected_status = 'running'
       AND transition_actor_type = 'worker'
       AND transition_reason_code = 'execute_retryable_no_effect'
       AND before_record.cancel_requested_at IS NOT NULL THEN
        target_status := CASE WHEN unresolved_unknown THEN 'failed' ELSE 'cancelled' END;
        transition_event_type := target_status;
        transition_release_lease := true;
        transition_error_code := CASE WHEN unresolved_unknown
            THEN 'cancel_after_unknown_effect' ELSE 'cancel_verified_safe' END;
        transition_error_summary := NULL;
    ELSIF expected_status = 'running'
       AND (target_status = 'retry_wait'
            OR (target_status = 'failed' AND transition_reason_code = 'effect_unknown_unverified'))
       AND unresolved_unknown AND before_record.cancel_requested_at IS NOT NULL THEN
        target_status := 'failed';
        transition_event_type := 'failed';
        transition_release_lease := true;
        transition_error_code := 'cancel_after_unknown_effect';
        transition_error_summary := NULL;
    ELSIF expected_status = 'running' AND target_status = 'retry_wait'
       AND (before_record.deadline_at <= clock_timestamp()
            OR before_record.attempt_count >= before_record.max_attempts) THEN
        target_status := 'failed';
        transition_event_type := 'failed';
        transition_release_lease := true;
        transition_error_code := CASE
            WHEN before_record.deadline_at <= clock_timestamp() THEN 'job_deadline_exceeded'
            ELSE 'max_attempts_exhausted' END;
        transition_error_summary := NULL;
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
CREATE OR REPLACE FUNCTION public.control_claim_expired_async_job(reconcile_owner text, reconcile_fencing_token uuid)
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
      AND NOT (job.status = 'running' AND job.allow_unknown_effect_replay)
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
    ) AND (job.verification_attempt < job.max_verification_attempts
           OR (job.status = 'running' AND job.allow_unknown_effect_replay))
    ORDER BY job.lease_expires_at NULLS FIRST, job.available_at, job.created_at, job.job_id
    FOR UPDATE SKIP LOCKED LIMIT 1;
    IF NOT FOUND THEN RETURN; END IF;
    prior_status := job_record.status;
    SELECT COALESCE(max(sequence), 0) + 1 INTO next_sequence
    FROM public.async_job_events WHERE job_id = job_record.job_id;
    UPDATE public.async_jobs SET
        status = CASE WHEN status = 'running' AND NOT allow_unknown_effect_replay THEN 'verifying' ELSE status END,
        verification_attempt = verification_attempt + CASE
            WHEN status = 'running' AND allow_unknown_effect_replay THEN 0 ELSE 1 END,
        lease_owner = reconcile_owner,
        lease_fencing_token = reconcile_fencing_token,
        lease_expires_at = clock_timestamp() + make_interval(secs => lease_seconds)
    WHERE job_id = job_record.job_id RETURNING * INTO job_record;
    INSERT INTO public.async_job_events (
        job_id, sequence, event_type, from_status, to_status, attempt,
        reason_code, actor_type
    ) VALUES (
        job_record.job_id, next_sequence,
        CASE WHEN job_record.status = 'running' THEN 'claimed'
             WHEN prior_status = 'rolling_back' THEN 'rollback_started' ELSE 'verification_started' END,
        prior_status, job_record.status, job_record.attempt_count,
        'lease_expired', 'reconciler'
    );
    RETURN NEXT job_record;
END;
$$;
-- +goose StatementEnd

-- CREATE OR REPLACE preserves the existing owner and EXECUTE ACL.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_request_async_job_cancel(
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
    IF cancel_reason_code IN ('effect_unknown_unverified', 'execute_retryable_no_effect', 'effect_absent_verified') THEN
        RAISE EXCEPTION 'effect reason is reserved for the framework' USING ERRCODE = '22023';
    END IF;
    SELECT COALESCE(max(sequence), 0) + 1 INTO next_sequence
    FROM public.async_job_events WHERE job_id = requested_job_id;
    IF before_record.status = 'retry_wait'
       AND EXISTS (
           SELECT 1 FROM public.async_job_events e
           WHERE e.job_id = requested_job_id AND e.reason_code = 'effect_unknown_unverified'
             AND e.sequence > COALESCE((
                 SELECT max(v.sequence) FROM public.async_job_events v
                 WHERE v.job_id = requested_job_id AND v.reason_code = 'effect_absent_verified'
             ), 0)
       ) THEN
        -- Unknown effects are not safe cancellation, even when cancellation
        -- arrives after the retry transition committed.
        UPDATE public.async_jobs SET
            status = 'failed', cancel_requested_at = clock_timestamp(),
            completed_at = clock_timestamp(), error_code = 'cancel_after_unknown_effect',
            error_summary = NULL, lease_owner = NULL,
            lease_fencing_token = NULL, lease_expires_at = NULL
        WHERE job_id = requested_job_id RETURNING * INTO after_record;
        INSERT INTO public.async_job_events (
            job_id, sequence, event_type, from_status, to_status, attempt,
            reason_code, error_code, actor_type
        ) VALUES (
            requested_job_id, next_sequence, 'failed', before_record.status,
            'failed', before_record.attempt_count, cancel_reason_code,
            'cancel_after_unknown_effect', 'service'
        );
    ELSIF before_record.status IN ('pending', 'retry_wait') THEN
        UPDATE public.async_jobs SET
            status = 'cancelled', cancel_requested_at = clock_timestamp(),
            -- Only an actual verification marker justifies verified-safe
            -- semantics. Pending and ordinary no-effect retry cancellation
            -- retain the legacy NULL error; policy is not effect evidence.
            error_code = CASE WHEN before_record.status = 'retry_wait' AND EXISTS (
                SELECT 1 FROM public.async_job_events v
                WHERE v.job_id = requested_job_id AND v.reason_code = 'effect_absent_verified'
            ) THEN 'cancel_verified_safe' ELSE NULL END,
            error_summary = NULL,
            completed_at = clock_timestamp(), lease_owner = NULL,
            lease_fencing_token = NULL, lease_expires_at = NULL
        WHERE job_id = requested_job_id RETURNING * INTO after_record;
        INSERT INTO public.async_job_events (
            job_id, sequence, event_type, from_status, to_status, attempt,
            reason_code, error_code, actor_type
        ) VALUES (
            requested_job_id, next_sequence, 'cancelled', before_record.status,
            'cancelled', before_record.attempt_count, cancel_reason_code, after_record.error_code, 'service'
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

-- +goose Down
-- Execution permissions and evidence must not be silently rewritten on rollback.
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'execution policy migration is forward-only';
END $$;
-- +goose StatementEnd
