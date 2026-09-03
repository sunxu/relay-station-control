-- +goose Up

CREATE TABLE gateway_directory_ingestion_runs (
    ingestion_run_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    gateway_instance_id uuid NOT NULL,
    scheduled_at timestamptz NOT NULL,
    status text NOT NULL DEFAULT 'pending',
    attempt_count smallint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    first_started_at timestamptz,
    last_started_at timestamptz,
    lease_expires_at timestamptz,
    lease_fencing_token uuid,
    terminal_at timestamptz,
    outcome text,
    last_failure_class text,
    source_generated_at timestamptz,
    received_at timestamptz,
    content_fingerprint bytea,
    snapshot_id uuid,
    account_count integer,
    CONSTRAINT gateway_directory_ingestion_runs_status_fixed CHECK (
        status IN ('pending', 'running', 'retry_wait', 'succeeded', 'failed')
    ),
    CONSTRAINT gateway_directory_ingestion_runs_slot_aligned CHECK (
        mod(extract(epoch FROM scheduled_at), 180) = 0
    ),
    CONSTRAINT gateway_directory_ingestion_runs_attempts_bounded CHECK (
        attempt_count BETWEEN 0 AND 2
    ),
    CONSTRAINT gateway_directory_ingestion_runs_outcome_fixed CHECK (
        outcome IS NULL OR outcome IN ('changed', 'unchanged')
    ),
    CONSTRAINT gateway_directory_ingestion_runs_failure_fixed CHECK (
        last_failure_class IS NULL OR last_failure_class IN (
            'transport',
            'timeout',
            'partial_read',
            'http_429',
            'http_5xx',
            'http_non_retryable',
            'contract_invalid',
            'source_time_invalid',
            'hard_limit',
            'secret_unavailable',
            'finalize_transient',
            'lease_lost',
            'unknown_execution',
            'start_deadline_expired'
        )
    ),
    CONSTRAINT gateway_directory_ingestion_runs_fingerprint_shape CHECK (
        content_fingerprint IS NULL OR octet_length(content_fingerprint) = 32
    ),
    CONSTRAINT gateway_directory_ingestion_runs_account_count_bounded CHECK (
        account_count IS NULL OR account_count BETWEEN 0 AND 10000
    ),
    CONSTRAINT gateway_directory_ingestion_runs_times_ordered CHECK (
        created_at >= scheduled_at
        AND (first_started_at IS NULL OR first_started_at >= created_at)
        AND (last_started_at IS NULL OR (
            first_started_at IS NOT NULL AND last_started_at >= first_started_at
        ))
        AND (lease_expires_at IS NULL OR (
            last_started_at IS NOT NULL
            AND lease_expires_at = last_started_at + interval '15 seconds'
        ))
        AND (terminal_at IS NULL OR (
            last_started_at IS NULL OR terminal_at >= last_started_at
        ))
        AND (received_at IS NULL OR (
            last_started_at IS NOT NULL AND received_at >= last_started_at
        ))
    ),
    CONSTRAINT gateway_directory_ingestion_runs_state_shape CHECK (
        (
            status = 'pending'
            AND attempt_count = 0
            AND first_started_at IS NULL
            AND last_started_at IS NULL
            AND lease_expires_at IS NULL
            AND lease_fencing_token IS NULL
            AND terminal_at IS NULL
            AND outcome IS NULL
            AND last_failure_class IS NULL
            AND source_generated_at IS NULL
            AND received_at IS NULL
            AND content_fingerprint IS NULL
            AND snapshot_id IS NULL
            AND account_count IS NULL
        )
        OR (
            status = 'running'
            AND attempt_count BETWEEN 1 AND 2
            AND first_started_at IS NOT NULL
            AND last_started_at IS NOT NULL
            AND lease_expires_at = last_started_at + interval '15 seconds'
            AND lease_fencing_token IS NOT NULL
            AND terminal_at IS NULL
            AND outcome IS NULL
            AND source_generated_at IS NULL
            AND received_at IS NULL
            AND content_fingerprint IS NULL
            AND snapshot_id IS NULL
            AND account_count IS NULL
            AND (
                (attempt_count = 1 AND last_failure_class IS NULL)
                OR (
                    attempt_count = 2
                    AND last_failure_class IN (
                        'transport', 'timeout', 'partial_read', 'http_429',
                        'http_5xx', 'finalize_transient', 'lease_lost',
                        'unknown_execution'
                    )
                )
            )
        )
        OR (
            status = 'retry_wait'
            AND attempt_count = 1
            AND first_started_at IS NOT NULL
            AND last_started_at IS NOT NULL
            AND lease_expires_at IS NULL
            AND lease_fencing_token IS NULL
            AND terminal_at IS NULL
            AND outcome IS NULL
            AND source_generated_at IS NULL
            AND received_at IS NULL
            AND content_fingerprint IS NULL
            AND snapshot_id IS NULL
            AND account_count IS NULL
            AND last_failure_class IN (
                'transport', 'timeout', 'partial_read', 'http_429',
                'http_5xx', 'finalize_transient', 'lease_lost',
                'unknown_execution'
            )
        )
        OR (
            status = 'succeeded'
            AND attempt_count BETWEEN 1 AND 2
            AND first_started_at IS NOT NULL
            AND last_started_at IS NOT NULL
            AND lease_expires_at IS NULL
            AND lease_fencing_token IS NULL
            AND terminal_at IS NOT NULL
            AND outcome IN ('changed', 'unchanged')
            AND source_generated_at IS NOT NULL
            AND received_at IS NOT NULL
            AND terminal_at = received_at
            AND content_fingerprint IS NOT NULL
            AND octet_length(content_fingerprint) = 32
            AND snapshot_id IS NOT NULL
            AND account_count BETWEEN 0 AND 10000
            AND (
                (attempt_count = 1 AND last_failure_class IS NULL)
                OR (
                    attempt_count = 2
                    AND last_failure_class IN (
                        'transport', 'timeout', 'partial_read', 'http_429',
                        'http_5xx', 'finalize_transient', 'lease_lost',
                        'unknown_execution'
                    )
                )
            )
        )
        OR (
            status = 'failed'
            AND attempt_count BETWEEN 0 AND 2
            AND lease_expires_at IS NULL
            AND lease_fencing_token IS NULL
            AND terminal_at IS NOT NULL
            AND outcome IS NULL
            AND source_generated_at IS NULL
            AND received_at IS NULL
            AND content_fingerprint IS NULL
            AND snapshot_id IS NULL
            AND account_count IS NULL
            AND last_failure_class IS NOT NULL
            AND (
                (
                    attempt_count = 0
                    AND first_started_at IS NULL
                    AND last_started_at IS NULL
                    AND last_failure_class = 'start_deadline_expired'
                )
                OR (
                    attempt_count BETWEEN 1 AND 2
                    AND first_started_at IS NOT NULL
                    AND last_started_at IS NOT NULL
                )
            )
        )
    ),
    FOREIGN KEY (gateway_instance_id)
        REFERENCES gateway_instances(instance_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    UNIQUE (ingestion_run_id, gateway_instance_id),
    UNIQUE (gateway_instance_id, scheduled_at)
);

CREATE UNIQUE INDEX gateway_directory_ingestion_runs_one_active_idx
    ON gateway_directory_ingestion_runs (gateway_instance_id)
    WHERE status IN ('pending', 'running', 'retry_wait');

CREATE INDEX gateway_directory_ingestion_runs_claim_idx
    ON gateway_directory_ingestion_runs (scheduled_at, gateway_instance_id)
    WHERE status IN ('pending', 'retry_wait');

CREATE INDEX gateway_directory_ingestion_runs_reconcile_idx
    ON gateway_directory_ingestion_runs (lease_expires_at, scheduled_at, gateway_instance_id)
    WHERE status = 'running';

CREATE INDEX gateway_directory_ingestion_runs_history_idx
    ON gateway_directory_ingestion_runs (gateway_instance_id, scheduled_at DESC);

CREATE TABLE gateway_directory_snapshots (
    snapshot_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    gateway_instance_id uuid NOT NULL,
    fingerprint bytea NOT NULL,
    fingerprint_encoding_version smallint NOT NULL DEFAULT 1,
    schema_version integer NOT NULL,
    account_count integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT gateway_directory_snapshots_fingerprint_shape CHECK (
        octet_length(fingerprint) = 32
    ),
    CONSTRAINT gateway_directory_snapshots_encoding_version_fixed CHECK (
        fingerprint_encoding_version = 1
    ),
    CONSTRAINT gateway_directory_snapshots_schema_version_fixed CHECK (
        schema_version = 1
    ),
    CONSTRAINT gateway_directory_snapshots_account_count_bounded CHECK (
        account_count BETWEEN 0 AND 10000
    ),
    FOREIGN KEY (gateway_instance_id)
        REFERENCES gateway_instances(instance_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    UNIQUE (snapshot_id, gateway_instance_id),
    UNIQUE (gateway_instance_id, fingerprint)
);

CREATE INDEX gateway_directory_snapshots_history_idx
    ON gateway_directory_snapshots (gateway_instance_id, created_at DESC);

CREATE TABLE gateway_directory_snapshot_items (
    snapshot_id uuid NOT NULL,
    account_id bigint NOT NULL,
    name text NOT NULL,
    platform text NOT NULL,
    type text NOT NULL,
    url text,
    status text NOT NULL,
    PRIMARY KEY (snapshot_id, account_id),
    CONSTRAINT gateway_directory_snapshot_items_id_positive CHECK (
        account_id > 0
    ),
    CONSTRAINT gateway_directory_snapshot_items_type_fixed CHECK (
        type IN ('apikey', 'upstream')
    ),
    CONSTRAINT gateway_directory_snapshot_items_url_bounded CHECK (
        url IS NULL OR octet_length(url) <= 4096
    ),
    FOREIGN KEY (snapshot_id)
        REFERENCES gateway_directory_snapshots(snapshot_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT
);

ALTER TABLE gateway_directory_ingestion_runs
    ADD CONSTRAINT gateway_directory_ingestion_runs_snapshot_fk
    FOREIGN KEY (snapshot_id, gateway_instance_id)
        REFERENCES gateway_directory_snapshots(snapshot_id, gateway_instance_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT;

CREATE TABLE gateway_directory_current_state (
    gateway_instance_id uuid PRIMARY KEY,
    current_snapshot_id uuid NOT NULL,
    current_content_fingerprint bytea NOT NULL,
    last_success_received_at timestamptz NOT NULL,
    last_source_generated_at timestamptz NOT NULL,
    last_success_run_id uuid NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT gateway_directory_current_state_fingerprint_shape CHECK (
        octet_length(current_content_fingerprint) = 32
    ),
    CONSTRAINT gateway_directory_current_state_time_order CHECK (
        updated_at >= last_success_received_at
    ),
    FOREIGN KEY (gateway_instance_id)
        REFERENCES gateway_instances(instance_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    FOREIGN KEY (current_snapshot_id, gateway_instance_id)
        REFERENCES gateway_directory_snapshots(snapshot_id, gateway_instance_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    FOREIGN KEY (last_success_run_id, gateway_instance_id)
        REFERENCES gateway_directory_ingestion_runs(ingestion_run_id, gateway_instance_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT
);

-- Ingestion runs stay mutable only while non-terminal.  Once succeeded/failed,
-- they become immutable evidence and only exact state-machine transitions may
-- occur before that point.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_protect_gateway_directory_ingestion_run()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF OLD.status IN ('succeeded', 'failed') THEN
            RAISE EXCEPTION 'gateway directory ingestion run is immutable once terminal'
                USING ERRCODE = '23514';
        END IF;
        RETURN OLD;
    END IF;

    IF OLD.status IN ('succeeded', 'failed') THEN
        RAISE EXCEPTION 'gateway directory ingestion run is immutable once terminal'
            USING ERRCODE = '23514';
    END IF;

    IF NEW.ingestion_run_id <> OLD.ingestion_run_id
       OR NEW.gateway_instance_id <> OLD.gateway_instance_id
       OR NEW.scheduled_at <> OLD.scheduled_at
       OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'gateway directory ingestion run identity is immutable'
            USING ERRCODE = '23514';
    END IF;

    IF NEW.status IS DISTINCT FROM OLD.status THEN
        IF NOT (
            (OLD.status = 'pending' AND NEW.status IN ('running', 'failed'))
            OR (OLD.status = 'running' AND NEW.status IN ('retry_wait', 'succeeded', 'failed'))
            OR (OLD.status = 'retry_wait' AND NEW.status IN ('running', 'failed'))
        ) THEN
            RAISE EXCEPTION 'gateway directory ingestion run status transition is invalid'
                USING ERRCODE = '23514';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER gateway_directory_ingestion_runs_guard
BEFORE UPDATE OR DELETE ON gateway_directory_ingestion_runs
FOR EACH ROW EXECUTE FUNCTION public.control_protect_gateway_directory_ingestion_run();

REVOKE ALL ON TABLE
    gateway_directory_ingestion_runs,
    gateway_directory_snapshots,
    gateway_directory_snapshot_items,
    gateway_directory_current_state
FROM PUBLIC, relay_control_runtime;

GRANT SELECT, INSERT ON TABLE gateway_directory_ingestion_runs
TO relay_control_runtime;
GRANT UPDATE (
    status,
    attempt_count,
    first_started_at,
    last_started_at,
    lease_expires_at,
    lease_fencing_token,
    terminal_at,
    outcome,
    last_failure_class,
    source_generated_at,
    received_at,
    content_fingerprint,
    snapshot_id,
    account_count
) ON gateway_directory_ingestion_runs TO relay_control_runtime;

GRANT SELECT, INSERT ON TABLE
    gateway_directory_snapshots,
    gateway_directory_snapshot_items
TO relay_control_runtime;

GRANT SELECT, INSERT ON TABLE gateway_directory_current_state
TO relay_control_runtime;
GRANT UPDATE (
    current_snapshot_id,
    current_content_fingerprint,
    last_success_received_at,
    last_source_generated_at,
    last_success_run_id,
    updated_at
) ON gateway_directory_current_state TO relay_control_runtime;

-- +goose Down

DROP TABLE gateway_directory_current_state;
DROP TABLE gateway_directory_snapshot_items;
ALTER TABLE gateway_directory_ingestion_runs
    DROP CONSTRAINT gateway_directory_ingestion_runs_snapshot_fk;
DROP TABLE gateway_directory_snapshots;
DROP TABLE gateway_directory_ingestion_runs;
