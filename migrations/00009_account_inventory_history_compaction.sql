-- +goose Up

-- PostgreSQL 18 exposes sha256(bytea) in core.  Keep this assertion separate
-- from any history row so an unsupported server fails before DDL is installed.
-- +goose StatementBegin
DO $$
BEGIN
    IF encode(sha256(convert_to('relay-control-history-sha256-v1', 'UTF8')), 'hex')
       <> '013899e7d1a7277e0d3d41477fb40856bcdc5bf4150fe4cf9ee870cdd45ce12c' THEN
        RAISE EXCEPTION 'PostgreSQL core sha256(bytea) is unavailable or incompatible'
            USING ERRCODE = '55000';
    END IF;
END;
$$;
-- +goose StatementEnd

-- Canonical checksum row v1 is deliberately byte-oriented and independent of
-- database locale: RSH1, big-endian field count, then big-endian length and
-- bytes for every field.  NULL uses the reserved 0xffffffff length.
-- +goose StatementBegin
CREATE FUNCTION public.control_history_canonical_row_v1(VARIADIC fields bytea[])
RETURNS bytea
LANGUAGE plpgsql
IMMUTABLE
SET search_path = pg_catalog
AS $$
DECLARE
    encoded bytea;
    field bytea;
BEGIN
    IF fields IS NULL OR cardinality(fields) > 1024 THEN
        RAISE EXCEPTION 'invalid history checksum field set' USING ERRCODE = '22023';
    END IF;
    encoded := convert_to('RSH1', 'UTF8') || int4send(cardinality(fields));
    FOREACH field IN ARRAY fields LOOP
        IF field IS NULL THEN
            encoded := encoded || decode('ffffffff', 'hex');
        ELSE
            IF octet_length(field) > 1073741824 THEN
                RAISE EXCEPTION 'history checksum field is too large' USING ERRCODE = '22023';
            END IF;
            encoded := encoded || int4send(octet_length(field)) || field;
        END IF;
    END LOOP;
    RETURN encoded;
END;
$$;
-- +goose StatementEnd

-- H0 is zero32; every supplied value must already be a canonical RSH1 row.
-- Ri=sha256(row), Hi=sha256(Hi-1 || Ri).
-- +goose StatementBegin
CREATE FUNCTION public.control_history_checksum_chain_v1(canonical_rows bytea[])
RETURNS bytea
LANGUAGE plpgsql
IMMUTABLE
SET search_path = pg_catalog
AS $$
DECLARE
    digest bytea := decode(repeat('00', 32), 'hex');
    canonical_row bytea;
BEGIN
    IF canonical_rows IS NULL THEN
        RAISE EXCEPTION 'invalid history checksum row set' USING ERRCODE = '22023';
    END IF;
    FOREACH canonical_row IN ARRAY canonical_rows LOOP
        IF canonical_row IS NULL OR substring(canonical_row FROM 1 FOR 4) <> convert_to('RSH1', 'UTF8') THEN
            RAISE EXCEPTION 'invalid history checksum canonical row' USING ERRCODE = '22023';
        END IF;
        digest := sha256(digest || sha256(canonical_row));
    END LOOP;
    RETURN digest;
END;
$$;
-- +goose StatementEnd

-- Compare fixed-size proof digests without an early exit.  This is not a
-- cryptographic primitive for secrets, but avoids turning checksum mismatch
-- position into observable control flow in the completion API.
-- +goose StatementBegin
CREATE FUNCTION public.control_history_bytea_equal_v1(left_value bytea, right_value bytea)
RETURNS boolean
LANGUAGE plpgsql
IMMUTABLE
SET search_path = pg_catalog
AS $$
DECLARE
    difference integer := 0;
    position integer;
BEGIN
    IF left_value IS NULL OR right_value IS NULL
       OR octet_length(left_value) <> 32 OR octet_length(right_value) <> 32 THEN
        RETURN false;
    END IF;
    FOR position IN 0..31 LOOP
        difference := difference | (get_byte(left_value, position)
                                    # get_byte(right_value, position));
    END LOOP;
    RETURN difference = 0;
END;
$$;
-- +goose StatementEnd

-- PostgreSQL stores microseconds while Go's RFC3339Nano formatter trims
-- trailing fractional zeros.  This produces the identical UTC text domain.
-- +goose StatementBegin
CREATE FUNCTION public.control_history_time_text_v1(value timestamptz)
RETURNS text
LANGUAGE sql
IMMUTABLE STRICT
SET search_path = pg_catalog
AS $$
    SELECT to_char(value AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS')
        || CASE
            WHEN to_char(value AT TIME ZONE 'UTC', 'US') = '000000' THEN ''
            ELSE '.' || rtrim(to_char(value AT TIME ZONE 'UTC', 'US'), '0')
           END
        || 'Z'
$$;
-- +goose StatementEnd

CREATE TABLE account_inventory_compaction_runs (
    compaction_run_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    summary_date date NOT NULL,
    instance_id uuid NOT NULL,
    provider_policy_version uuid NOT NULL,
    status text NOT NULL DEFAULT 'pending',
    failed_from text,
    claim_owner text,
    lease_expires_at timestamptz,
    fencing_token uuid,
    attempt_count integer NOT NULL DEFAULT 0,
    checksum_version smallint,
    source_snapshot_count bigint,
    source_poll_count bigint,
    source_provider_result_count bigint,
    source_duplicate_count bigint,
    source_checksum bytea,
    deleted_snapshot_count bigint NOT NULL DEFAULT 0,
    deleted_poll_count bigint NOT NULL DEFAULT 0,
    deleted_provider_result_count bigint NOT NULL DEFAULT 0,
    deleted_duplicate_count bigint NOT NULL DEFAULT 0,
    failure_reason text,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    summarized_at timestamptz,
    deleting_at timestamptz,
    completed_at timestamptz,
    failed_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT account_inventory_compaction_date_bounded CHECK (
        summary_date BETWEEN DATE '2000-01-01' AND DATE '9999-12-30'
    ),
    CONSTRAINT account_inventory_compaction_status_fixed CHECK (
        status IN ('pending', 'summarized', 'deleting', 'completed', 'failed')
    ),
    CONSTRAINT account_inventory_compaction_failed_from_fixed CHECK (
        failed_from IS NULL OR failed_from IN ('pending', 'summarized', 'deleting')
    ),
    CONSTRAINT account_inventory_compaction_claim_shape CHECK (
        (claim_owner IS NULL AND lease_expires_at IS NULL AND fencing_token IS NULL)
        OR (claim_owner IS NOT NULL
            AND octet_length(claim_owner) BETWEEN 1 AND 128
            AND claim_owner ~ '^[A-Za-z0-9][A-Za-z0-9._:-]*$'
            AND lease_expires_at IS NOT NULL AND fencing_token IS NOT NULL
            AND status IN ('pending', 'summarized', 'deleting'))
    ),
    CONSTRAINT account_inventory_compaction_attempt_bounded CHECK (
        attempt_count BETWEEN 0 AND 1000000
        AND (attempt_count > 0 OR claim_owner IS NULL)
    ),
    CONSTRAINT account_inventory_compaction_counts_bounded CHECK (
        (source_snapshot_count IS NULL OR source_snapshot_count >= 0)
        AND (source_poll_count IS NULL OR source_poll_count >= 0)
        AND (source_provider_result_count IS NULL OR source_provider_result_count >= 0)
        AND (source_duplicate_count IS NULL OR source_duplicate_count >= 0)
        AND deleted_snapshot_count >= 0
        AND (source_snapshot_count IS NULL OR deleted_snapshot_count <= source_snapshot_count)
        AND deleted_poll_count >= 0
        AND deleted_provider_result_count >= 0
        AND deleted_duplicate_count >= 0
        AND ((source_poll_count IS NULL AND deleted_poll_count = 0)
             OR deleted_poll_count <= source_poll_count)
        AND ((source_provider_result_count IS NULL AND deleted_provider_result_count = 0)
             OR deleted_provider_result_count <= source_provider_result_count)
        AND ((source_duplicate_count IS NULL AND deleted_duplicate_count = 0)
             OR deleted_duplicate_count <= source_duplicate_count)
    ),
    CONSTRAINT account_inventory_compaction_checksum_shape CHECK (
        (checksum_version IS NULL AND source_checksum IS NULL)
        OR (checksum_version = 1 AND octet_length(source_checksum) = 32)
    ),
    CONSTRAINT account_inventory_compaction_failure_fixed CHECK (
        failure_reason IS NULL OR failure_reason IN (
            'source_day_mismatch', 'source_count_mismatch', 'source_checksum_mismatch',
            'activation_inconsistent', 'statement_timeout', 'lease_expired',
            'database_unavailable', 'internal'
        )
    ),
    CONSTRAINT account_inventory_compaction_state_shape CHECK (
        (status = 'pending'
         AND failed_from IS NULL AND checksum_version IS NULL
         AND source_snapshot_count IS NULL AND source_poll_count IS NULL
         AND source_provider_result_count IS NULL AND source_duplicate_count IS NULL
         AND source_checksum IS NULL AND deleted_snapshot_count = 0
         AND deleted_poll_count = 0 AND deleted_provider_result_count = 0
         AND deleted_duplicate_count = 0
         AND failure_reason IS NULL AND summarized_at IS NULL
         AND deleting_at IS NULL AND completed_at IS NULL AND failed_at IS NULL)
        OR (status IN ('summarized', 'deleting', 'completed')
            AND failed_from IS NULL AND checksum_version = 1
            AND source_snapshot_count IS NOT NULL AND source_poll_count IS NOT NULL
            AND source_provider_result_count IS NOT NULL AND source_duplicate_count IS NOT NULL
            AND source_checksum IS NOT NULL AND failure_reason IS NULL
            AND summarized_at IS NOT NULL AND failed_at IS NULL
            AND (status = 'summarized' AND deleting_at IS NULL AND completed_at IS NULL
                    AND deleted_poll_count = 0 AND deleted_provider_result_count = 0
                    AND deleted_duplicate_count = 0
                 OR status = 'deleting' AND deleting_at IS NOT NULL AND completed_at IS NULL
                    AND deleted_poll_count = 0 AND deleted_provider_result_count = 0
                    AND deleted_duplicate_count = 0
                 OR status = 'completed' AND deleting_at IS NOT NULL AND completed_at IS NOT NULL
                    AND deleted_snapshot_count = source_snapshot_count))
        OR (status = 'failed'
            AND failed_from IS NOT NULL AND failure_reason IS NOT NULL AND failed_at IS NOT NULL
            AND completed_at IS NULL
            AND ((failed_from = 'pending' AND summarized_at IS NULL
                  AND checksum_version IS NULL AND source_checksum IS NULL
                  AND source_snapshot_count IS NULL AND source_poll_count IS NULL
                  AND source_provider_result_count IS NULL AND source_duplicate_count IS NULL
                  AND deleted_snapshot_count = 0
                  AND deleted_poll_count = 0 AND deleted_provider_result_count = 0
                  AND deleted_duplicate_count = 0)
                 OR (failed_from IN ('summarized', 'deleting')
                     AND summarized_at IS NOT NULL AND checksum_version = 1
                     AND source_checksum IS NOT NULL AND source_snapshot_count IS NOT NULL
                     AND source_poll_count IS NOT NULL
                     AND source_provider_result_count IS NOT NULL
                     AND source_duplicate_count IS NOT NULL
                     AND deleted_poll_count = 0 AND deleted_provider_result_count = 0
                     AND deleted_duplicate_count = 0
                     AND ((failed_from = 'summarized' AND deleting_at IS NULL)
                          OR (failed_from = 'deleting' AND deleting_at IS NOT NULL)))))
    ),
    CONSTRAINT account_inventory_compaction_times_ordered CHECK (
        updated_at >= coalesce(completed_at, failed_at, deleting_at, summarized_at, created_at)
        AND (summarized_at IS NULL OR summarized_at >= created_at)
        AND (deleting_at IS NULL OR deleting_at >= summarized_at)
        AND (completed_at IS NULL OR completed_at >= deleting_at)
        AND (failed_at IS NULL
             OR failed_at >= coalesce(deleting_at, summarized_at, created_at))
    ),
    FOREIGN KEY (instance_id) REFERENCES relay_node_assets(instance_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    FOREIGN KEY (provider_policy_version)
        REFERENCES provider_inventory_policy_versions(policy_version_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    UNIQUE (summary_date, instance_id, provider_policy_version),
    UNIQUE (compaction_run_id, summary_date, instance_id, provider_policy_version)
);

CREATE INDEX account_inventory_compaction_runs_claim_idx
    ON account_inventory_compaction_runs (status, lease_expires_at, summary_date, instance_id)
    WHERE status IN ('pending', 'summarized', 'deleting', 'failed');
CREATE INDEX account_inventory_compaction_runs_completed_idx
    ON account_inventory_compaction_runs (completed_at, summary_date, instance_id)
    WHERE status = 'completed';

CREATE TABLE account_inventory_daily_rollup_runs (
    rollup_run_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    summary_date date NOT NULL,
    instance_id uuid NOT NULL,
    status text NOT NULL DEFAULT 'pending',
    claim_owner text,
    lease_expires_at timestamptz,
    fencing_token uuid,
    completed_fencing_token uuid,
    attempt_count integer NOT NULL DEFAULT 0,
    expected_segment_count integer,
    completed_segment_count integer,
    checksum_version smallint,
    segment_checksum bytea,
    failure_reason text,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    completed_at timestamptz,
    failed_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT account_inventory_rollup_run_date_bounded CHECK (
        summary_date BETWEEN DATE '2000-01-01' AND DATE '9999-12-30'
    ),
    CONSTRAINT account_inventory_rollup_run_status_fixed CHECK (
        status IN ('pending', 'completed', 'failed')
    ),
    CONSTRAINT account_inventory_rollup_run_claim_shape CHECK (
        (claim_owner IS NULL AND lease_expires_at IS NULL AND fencing_token IS NULL)
        OR (status = 'pending' AND claim_owner IS NOT NULL
            AND octet_length(claim_owner) BETWEEN 1 AND 128
            AND claim_owner ~ '^[A-Za-z0-9][A-Za-z0-9._:-]*$'
            AND lease_expires_at IS NOT NULL AND fencing_token IS NOT NULL)
    ),
    CONSTRAINT account_inventory_rollup_run_attempt_bounded CHECK (
        attempt_count BETWEEN 0 AND 1000000 AND (attempt_count > 0 OR claim_owner IS NULL)
    ),
    CONSTRAINT account_inventory_rollup_run_counts CHECK (
        (expected_segment_count IS NULL OR expected_segment_count > 0)
        AND (completed_segment_count IS NULL OR completed_segment_count >= 0)
        AND (expected_segment_count IS NULL OR completed_segment_count <= expected_segment_count)
    ),
    CONSTRAINT account_inventory_rollup_run_checksum_shape CHECK (
        (checksum_version IS NULL AND segment_checksum IS NULL)
        OR (checksum_version = 1 AND octet_length(segment_checksum) = 32)
    ),
    CONSTRAINT account_inventory_rollup_run_failure_fixed CHECK (
        failure_reason IS NULL OR failure_reason IN (
            'segment_incomplete', 'segment_count_mismatch', 'segment_checksum_mismatch',
            'activation_inconsistent', 'statement_timeout', 'lease_expired',
            'database_unavailable', 'internal'
        )
    ),
    CONSTRAINT account_inventory_rollup_run_state_shape CHECK (
        (status = 'pending' AND completed_fencing_token IS NULL
         AND expected_segment_count IS NULL
         AND completed_segment_count IS NULL AND checksum_version IS NULL
         AND segment_checksum IS NULL AND failure_reason IS NULL
         AND completed_at IS NULL AND failed_at IS NULL)
        OR (status = 'completed' AND claim_owner IS NULL AND lease_expires_at IS NULL
            AND fencing_token IS NULL AND completed_fencing_token IS NOT NULL
            AND expected_segment_count > 0
            AND completed_segment_count = expected_segment_count
            AND checksum_version = 1 AND segment_checksum IS NOT NULL
            AND failure_reason IS NULL AND completed_at IS NOT NULL AND failed_at IS NULL)
        OR (status = 'failed' AND claim_owner IS NULL AND lease_expires_at IS NULL
            AND fencing_token IS NULL AND completed_fencing_token IS NULL
            AND failure_reason IS NOT NULL
            AND expected_segment_count IS NULL AND completed_segment_count IS NULL
            AND checksum_version IS NULL AND segment_checksum IS NULL
            AND completed_at IS NULL AND failed_at IS NOT NULL)
    ),
    CONSTRAINT account_inventory_rollup_run_times_ordered CHECK (
        updated_at >= coalesce(completed_at, failed_at, created_at)
        AND (completed_at IS NULL OR completed_at >= created_at)
        AND (failed_at IS NULL OR failed_at >= created_at)
    ),
    FOREIGN KEY (instance_id) REFERENCES relay_node_assets(instance_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    UNIQUE (summary_date, instance_id),
    UNIQUE (rollup_run_id, summary_date, instance_id)
);

CREATE INDEX account_inventory_daily_rollup_runs_claim_idx
    ON account_inventory_daily_rollup_runs (status, lease_expires_at, summary_date, instance_id)
    WHERE status IN ('pending','failed');
CREATE INDEX account_inventory_daily_rollup_runs_completed_idx
    ON account_inventory_daily_rollup_runs (completed_at, summary_date, instance_id)
    WHERE status = 'completed';

CREATE TABLE account_inventory_history_retired_days (
    summary_date date NOT NULL,
    instance_id uuid NOT NULL,
    retired_at timestamptz NOT NULL,
    PRIMARY KEY (summary_date, instance_id),
    CONSTRAINT account_inventory_history_retired_day_date_bounded CHECK (
        summary_date BETWEEN DATE '2000-01-01' AND DATE '9999-12-30'
    ),
    CONSTRAINT account_inventory_history_retired_day_age CHECK (
        retired_at >= ((summary_date + 1)::timestamp AT TIME ZONE 'UTC')
                      + interval '30 days'
    ),
    FOREIGN KEY (instance_id) REFERENCES relay_node_assets(instance_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT
);

CREATE TABLE account_inventory_daily_summaries (
    summary_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    compaction_run_id uuid NOT NULL,
    summary_date date NOT NULL,
    instance_id uuid NOT NULL,
    provider text NOT NULL,
    account_key text NOT NULL,
    provider_policy_version uuid NOT NULL,
    first_scheduled_at timestamptz NOT NULL,
    last_scheduled_at timestamptz NOT NULL,
    first_observed_at timestamptz NOT NULL,
    last_observed_at timestamptz NOT NULL,
    last_basic_status text NOT NULL,
    sample_count integer NOT NULL,
    disabled_count integer NOT NULL,
    unavailable_count integer NOT NULL,
    error_count integer NOT NULL,
    active_count integer NOT NULL,
    unknown_count integer NOT NULL,
    first_success_count bigint NOT NULL,
    last_success_count bigint NOT NULL,
    success_reset_count integer NOT NULL,
    first_failed_count bigint NOT NULL,
    last_failed_count bigint NOT NULL,
    failed_reset_count integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT account_inventory_daily_summary_provider_valid CHECK (
        octet_length(provider) BETWEEN 1 AND 64
        AND provider ~ '^[a-z0-9][a-z0-9._-]*$'
    ),
    CONSTRAINT account_inventory_daily_summary_key_valid CHECK (
        octet_length(account_key) BETWEEN 3 AND 385
        AND account_key LIKE provider || ':%'
        AND substring(account_key FROM octet_length(provider) + 2)
            = lower(btrim(substring(account_key FROM octet_length(provider) + 2)))
        AND substring(account_key FROM octet_length(provider) + 2) !~ '[[:cntrl:]]'
        AND octet_length(substring(account_key FROM octet_length(provider) + 2)) BETWEEN 1 AND 320
    ),
    CONSTRAINT account_inventory_daily_summary_status_fixed CHECK (
        last_basic_status IN ('disabled', 'unavailable', 'error', 'active', 'unknown')
    ),
    CONSTRAINT account_inventory_daily_summary_samples_consistent CHECK (
        sample_count > 0
        AND disabled_count >= 0 AND unavailable_count >= 0 AND error_count >= 0
        AND active_count >= 0 AND unknown_count >= 0
        AND disabled_count + unavailable_count + error_count + active_count + unknown_count
            = sample_count
    ),
    CONSTRAINT account_inventory_daily_summary_counters_bounded CHECK (
        first_success_count >= 0 AND last_success_count >= 0
        AND first_failed_count >= 0 AND last_failed_count >= 0
        AND success_reset_count BETWEEN 0 AND sample_count - 1
        AND failed_reset_count BETWEEN 0 AND sample_count - 1
    ),
    CONSTRAINT account_inventory_daily_summary_times_ordered CHECK (
        first_scheduled_at <= last_scheduled_at
        AND first_observed_at <= last_observed_at
        AND first_scheduled_at >= (summary_date::timestamp AT TIME ZONE 'UTC')
        AND last_scheduled_at < ((summary_date + 1)::timestamp AT TIME ZONE 'UTC')
        AND first_observed_at >= (summary_date::timestamp AT TIME ZONE 'UTC')
        AND last_observed_at < ((summary_date + 1)::timestamp AT TIME ZONE 'UTC')
        AND created_at >= last_observed_at
    ),
    FOREIGN KEY (compaction_run_id, summary_date, instance_id, provider_policy_version)
        REFERENCES account_inventory_compaction_runs
            (compaction_run_id, summary_date, instance_id, provider_policy_version)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    UNIQUE (summary_date, instance_id, account_key, provider_policy_version)
);

CREATE INDEX account_inventory_daily_summaries_rollup_idx
    ON account_inventory_daily_summaries
        (summary_date, instance_id, account_key, last_scheduled_at DESC,
         provider_policy_version DESC);

CREATE TABLE account_inventory_daily_provider_summaries (
    provider_summary_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    compaction_run_id uuid NOT NULL,
    summary_date date NOT NULL,
    instance_id uuid NOT NULL,
    provider text NOT NULL,
    provider_policy_version uuid NOT NULL,
    expected_poll_count integer NOT NULL,
    transport_success_count integer NOT NULL,
    contract_valid_count integer NOT NULL,
    snapshot_complete_count integer NOT NULL,
    promotion_applied_count integer NOT NULL,
    promotion_skipped_count integer NOT NULL,
    policy_changed_count integer NOT NULL,
    abandoned_count integer NOT NULL,
    degraded_count integer NOT NULL,
    first_promotion_at timestamptz,
    last_promotion_at timestamptz,
    coverage_numerator integer NOT NULL,
    coverage_denominator integer NOT NULL,
    coverage_ratio numeric(9,8) NOT NULL,
    coverage_threshold_basis_points integer NOT NULL DEFAULT 9500,
    coverage_status text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT account_inventory_provider_summary_provider_valid CHECK (
        octet_length(provider) BETWEEN 1 AND 64
        AND provider ~ '^[a-z0-9][a-z0-9._-]*$'
    ),
    CONSTRAINT account_inventory_provider_summary_counts CHECK (
        expected_poll_count BETWEEN 1 AND 288
        AND transport_success_count BETWEEN 0 AND expected_poll_count
        AND contract_valid_count BETWEEN 0 AND transport_success_count
        AND snapshot_complete_count BETWEEN 0 AND contract_valid_count
        AND promotion_applied_count BETWEEN 0 AND snapshot_complete_count
        AND promotion_skipped_count BETWEEN 0 AND expected_poll_count
        AND policy_changed_count BETWEEN 0 AND promotion_skipped_count
        AND abandoned_count BETWEEN 0 AND expected_poll_count
        AND degraded_count BETWEEN 0 AND expected_poll_count
        AND promotion_applied_count + promotion_skipped_count <= expected_poll_count
        AND coverage_numerator = promotion_applied_count
        AND coverage_denominator = expected_poll_count
    ),
    CONSTRAINT account_inventory_provider_summary_coverage CHECK (
        coverage_threshold_basis_points = 9500
        AND coverage_ratio = round(coverage_numerator::numeric / coverage_denominator, 8)
        AND coverage_ratio BETWEEN 0 AND 1
        AND coverage_status IN ('complete', 'partial')
        AND (coverage_status = 'complete')
            = (coverage_numerator * 10000::bigint
                >= coverage_denominator * coverage_threshold_basis_points::bigint)
    ),
    CONSTRAINT account_inventory_provider_summary_promotion_times CHECK (
        (promotion_applied_count = 0
         AND first_promotion_at IS NULL AND last_promotion_at IS NULL)
        OR (promotion_applied_count > 0
            AND first_promotion_at IS NOT NULL AND last_promotion_at IS NOT NULL
            AND first_promotion_at <= last_promotion_at
            AND first_promotion_at >= (summary_date::timestamp AT TIME ZONE 'UTC')
            AND last_promotion_at < ((summary_date + 1)::timestamp AT TIME ZONE 'UTC'))
    ),
    FOREIGN KEY (compaction_run_id, summary_date, instance_id, provider_policy_version)
        REFERENCES account_inventory_compaction_runs
            (compaction_run_id, summary_date, instance_id, provider_policy_version)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    UNIQUE (summary_date, instance_id, provider, provider_policy_version)
);

CREATE INDEX account_inventory_daily_provider_summaries_rollup_idx
    ON account_inventory_daily_provider_summaries
        (summary_date, instance_id, provider, provider_policy_version);

CREATE TABLE account_inventory_daily_account_rollups (
    account_rollup_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    rollup_run_id uuid NOT NULL,
    summary_date date NOT NULL,
    instance_id uuid NOT NULL,
    provider text NOT NULL,
    account_key text NOT NULL,
    first_scheduled_at timestamptz NOT NULL,
    last_scheduled_at timestamptz NOT NULL,
    first_observed_at timestamptz NOT NULL,
    last_observed_at timestamptz NOT NULL,
    last_basic_status text NOT NULL,
    sample_count integer NOT NULL,
    disabled_count integer NOT NULL,
    unavailable_count integer NOT NULL,
    error_count integer NOT NULL,
    active_count integer NOT NULL,
    unknown_count integer NOT NULL,
    first_success_count bigint NOT NULL,
    last_success_count bigint NOT NULL,
    success_reset_count integer NOT NULL,
    first_failed_count bigint NOT NULL,
    last_failed_count bigint NOT NULL,
    failed_reset_count integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT account_inventory_account_rollup_provider_valid CHECK (
        octet_length(provider) BETWEEN 1 AND 64
        AND provider ~ '^[a-z0-9][a-z0-9._-]*$'
    ),
    CONSTRAINT account_inventory_account_rollup_key_valid CHECK (
        octet_length(account_key) BETWEEN 3 AND 385
        AND account_key LIKE provider || ':%'
        AND substring(account_key FROM octet_length(provider) + 2)
            = lower(btrim(substring(account_key FROM octet_length(provider) + 2)))
        AND substring(account_key FROM octet_length(provider) + 2) !~ '[[:cntrl:]]'
        AND octet_length(substring(account_key FROM octet_length(provider) + 2)) BETWEEN 1 AND 320
    ),
    CONSTRAINT account_inventory_account_rollup_status_fixed CHECK (
        last_basic_status IN ('disabled', 'unavailable', 'error', 'active', 'unknown')
    ),
    CONSTRAINT account_inventory_account_rollup_samples_consistent CHECK (
        sample_count > 0
        AND disabled_count >= 0 AND unavailable_count >= 0 AND error_count >= 0
        AND active_count >= 0 AND unknown_count >= 0
        AND disabled_count + unavailable_count + error_count + active_count + unknown_count
            = sample_count
        AND success_reset_count BETWEEN 0 AND sample_count - 1
        AND failed_reset_count BETWEEN 0 AND sample_count - 1
        AND first_success_count >= 0 AND last_success_count >= 0
        AND first_failed_count >= 0 AND last_failed_count >= 0
    ),
    CONSTRAINT account_inventory_account_rollup_times_ordered CHECK (
        first_scheduled_at <= last_scheduled_at
        AND first_observed_at <= last_observed_at
        AND first_scheduled_at >= (summary_date::timestamp AT TIME ZONE 'UTC')
        AND last_scheduled_at < ((summary_date + 1)::timestamp AT TIME ZONE 'UTC')
        AND first_observed_at >= (summary_date::timestamp AT TIME ZONE 'UTC')
        AND last_observed_at < ((summary_date + 1)::timestamp AT TIME ZONE 'UTC')
        AND created_at >= last_observed_at
    ),
    FOREIGN KEY (rollup_run_id, summary_date, instance_id)
        REFERENCES account_inventory_daily_rollup_runs
            (rollup_run_id, summary_date, instance_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    UNIQUE (summary_date, instance_id, account_key)
);

CREATE INDEX account_inventory_daily_account_rollups_read_idx
    ON account_inventory_daily_account_rollups (summary_date, instance_id, account_key);

CREATE TABLE account_inventory_daily_provider_rollups (
    provider_rollup_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    rollup_run_id uuid NOT NULL,
    summary_date date NOT NULL,
    instance_id uuid NOT NULL,
    provider text NOT NULL,
    expected_poll_count integer NOT NULL,
    transport_success_count integer NOT NULL,
    contract_valid_count integer NOT NULL,
    snapshot_complete_count integer NOT NULL,
    promotion_applied_count integer NOT NULL,
    promotion_skipped_count integer NOT NULL,
    policy_changed_count integer NOT NULL,
    abandoned_count integer NOT NULL,
    degraded_count integer NOT NULL,
    first_promotion_at timestamptz,
    last_promotion_at timestamptz,
    coverage_numerator integer NOT NULL,
    coverage_denominator integer NOT NULL,
    coverage_ratio numeric(9,8) NOT NULL,
    coverage_threshold_basis_points integer NOT NULL DEFAULT 9500,
    coverage_status text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT account_inventory_provider_rollup_provider_valid CHECK (
        octet_length(provider) BETWEEN 1 AND 64
        AND provider ~ '^[a-z0-9][a-z0-9._-]*$'
    ),
    CONSTRAINT account_inventory_provider_rollup_counts CHECK (
        expected_poll_count BETWEEN 1 AND 288
        AND transport_success_count BETWEEN 0 AND expected_poll_count
        AND contract_valid_count BETWEEN 0 AND transport_success_count
        AND snapshot_complete_count BETWEEN 0 AND contract_valid_count
        AND promotion_applied_count BETWEEN 0 AND snapshot_complete_count
        AND promotion_skipped_count BETWEEN 0 AND expected_poll_count
        AND policy_changed_count BETWEEN 0 AND promotion_skipped_count
        AND abandoned_count BETWEEN 0 AND expected_poll_count
        AND degraded_count BETWEEN 0 AND expected_poll_count
        AND promotion_applied_count + promotion_skipped_count <= expected_poll_count
        AND coverage_numerator = promotion_applied_count
        AND coverage_denominator = expected_poll_count
    ),
    CONSTRAINT account_inventory_provider_rollup_coverage CHECK (
        coverage_threshold_basis_points = 9500
        AND coverage_ratio = round(coverage_numerator::numeric / coverage_denominator, 8)
        AND coverage_ratio BETWEEN 0 AND 1
        AND coverage_status IN ('complete', 'partial')
        AND (coverage_status = 'complete')
            = (coverage_numerator * 10000::bigint
                >= coverage_denominator * coverage_threshold_basis_points::bigint)
    ),
    CONSTRAINT account_inventory_provider_rollup_promotion_times CHECK (
        (promotion_applied_count = 0
         AND first_promotion_at IS NULL AND last_promotion_at IS NULL)
        OR (promotion_applied_count > 0
            AND first_promotion_at IS NOT NULL AND last_promotion_at IS NOT NULL
            AND first_promotion_at <= last_promotion_at
            AND first_promotion_at >= (summary_date::timestamp AT TIME ZONE 'UTC')
            AND last_promotion_at < ((summary_date + 1)::timestamp AT TIME ZONE 'UTC'))
    ),
    FOREIGN KEY (rollup_run_id, summary_date, instance_id)
        REFERENCES account_inventory_daily_rollup_runs
            (rollup_run_id, summary_date, instance_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    UNIQUE (summary_date, instance_id, provider)
);

CREATE INDEX account_inventory_daily_provider_rollups_completed_read_idx
    ON account_inventory_daily_provider_rollups
        (instance_id, provider, summary_date DESC, provider_rollup_id);

-- Summary and final rows are append-only proof.  The later retention function
-- uses a separate exact gate; ordinary UPDATE/DELETE/TRUNCATE always fails.
-- +goose StatementBegin
CREATE FUNCTION public.control_reject_account_inventory_history_row_mutation()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE
    delete_gate text := coalesce(
        current_setting('relay_control.history_rollup_row_retention_delete', true), '');
    expected_gate text;
BEGIN
    IF TG_OP = 'DELETE' THEN
        expected_gate := TG_TABLE_NAME || ':' || CASE TG_TABLE_NAME
            WHEN 'account_inventory_daily_summaries' THEN to_jsonb(OLD)->>'summary_id'
            WHEN 'account_inventory_daily_provider_summaries' THEN to_jsonb(OLD)->>'provider_summary_id'
            WHEN 'account_inventory_daily_account_rollups' THEN to_jsonb(OLD)->>'account_rollup_id'
            WHEN 'account_inventory_daily_provider_rollups' THEN to_jsonb(OLD)->>'provider_rollup_id'
            ELSE ''
        END;
        IF current_user = 'relay_control_migrator'
           AND session_user <> current_user
           AND pg_has_role(session_user, 'relay_control_runtime', 'member')
           AND NOT pg_has_role(session_user, 'relay_control_migrator', 'member')
           AND delete_gate = expected_gate THEN
            RETURN OLD;
        END IF;
    END IF;
    RAISE EXCEPTION 'account inventory history summary is immutable'
        USING ERRCODE = '42501';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER account_inventory_daily_summaries_immutable
BEFORE UPDATE OR DELETE ON account_inventory_daily_summaries
FOR EACH ROW EXECUTE FUNCTION public.control_reject_account_inventory_history_row_mutation();
CREATE TRIGGER account_inventory_daily_summaries_truncate_immutable
BEFORE TRUNCATE ON account_inventory_daily_summaries
FOR EACH STATEMENT EXECUTE FUNCTION public.control_reject_account_inventory_history_row_mutation();
CREATE TRIGGER account_inventory_daily_provider_summaries_immutable
BEFORE UPDATE OR DELETE ON account_inventory_daily_provider_summaries
FOR EACH ROW EXECUTE FUNCTION public.control_reject_account_inventory_history_row_mutation();
CREATE TRIGGER account_inventory_daily_provider_summaries_truncate_immutable
BEFORE TRUNCATE ON account_inventory_daily_provider_summaries
FOR EACH STATEMENT EXECUTE FUNCTION public.control_reject_account_inventory_history_row_mutation();
CREATE TRIGGER account_inventory_daily_account_rollups_immutable
BEFORE UPDATE OR DELETE ON account_inventory_daily_account_rollups
FOR EACH ROW EXECUTE FUNCTION public.control_reject_account_inventory_history_row_mutation();
CREATE TRIGGER account_inventory_daily_account_rollups_truncate_immutable
BEFORE TRUNCATE ON account_inventory_daily_account_rollups
FOR EACH STATEMENT EXECUTE FUNCTION public.control_reject_account_inventory_history_row_mutation();
CREATE TRIGGER account_inventory_daily_provider_rollups_immutable
BEFORE UPDATE OR DELETE ON account_inventory_daily_provider_rollups
FOR EACH ROW EXECUTE FUNCTION public.control_reject_account_inventory_history_row_mutation();
CREATE TRIGGER account_inventory_daily_provider_rollups_truncate_immutable
BEFORE TRUNCATE ON account_inventory_daily_provider_rollups
FOR EACH STATEMENT EXECUTE FUNCTION public.control_reject_account_inventory_history_row_mutation();

-- Run identities are immutable.  State-machine functions open this
-- transaction-local gate only after locking and fencing the exact run.
-- +goose StatementBegin
CREATE FUNCTION public.control_protect_account_inventory_history_run()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE
    write_gate text := coalesce(current_setting('relay_control.history_run_write', true), '');
    retention_gate text;
BEGIN
    IF TG_OP = 'DELETE' THEN
        retention_gate := CASE TG_TABLE_NAME
            WHEN 'account_inventory_daily_rollup_runs' THEN coalesce(
                current_setting('relay_control.history_rollup_run_retention_delete', true), '')
            WHEN 'account_inventory_compaction_runs' THEN coalesce(
                current_setting('relay_control.history_compaction_run_retention_delete', true), '')
            ELSE ''
        END;
        IF current_user = 'relay_control_migrator'
           AND session_user <> current_user
           AND pg_has_role(session_user, 'relay_control_runtime', 'member')
           AND NOT pg_has_role(session_user, 'relay_control_migrator', 'member')
           AND OLD.status = 'completed'
           AND retention_gate = (CASE TG_TABLE_NAME
                WHEN 'account_inventory_daily_rollup_runs' THEN to_jsonb(OLD)->>'rollup_run_id'
                WHEN 'account_inventory_compaction_runs' THEN to_jsonb(OLD)->>'compaction_run_id'
                ELSE ''
               END) THEN
            RETURN OLD;
        END IF;
        RAISE EXCEPTION 'account inventory history run cannot be deleted directly'
            USING ERRCODE = '42501';
    END IF;
    IF TG_OP = 'TRUNCATE' THEN
        RAISE EXCEPTION 'account inventory history run cannot be deleted directly'
            USING ERRCODE = '42501';
    END IF;
    IF current_user <> 'relay_control_migrator'
       OR session_user = current_user
       OR NOT pg_has_role(session_user, 'relay_control_runtime', 'member')
       OR pg_has_role(session_user, 'relay_control_migrator', 'member')
       OR write_gate <> TG_TABLE_NAME THEN
        RAISE EXCEPTION 'account inventory history run requires controlled transition'
            USING ERRCODE = '42501';
    END IF;
    IF TG_TABLE_NAME = 'account_inventory_compaction_runs' THEN
        IF NEW.compaction_run_id <> OLD.compaction_run_id
           OR NEW.summary_date <> OLD.summary_date
           OR NEW.instance_id <> OLD.instance_id
           OR NEW.provider_policy_version <> OLD.provider_policy_version
           OR NEW.created_at <> OLD.created_at THEN
            RAISE EXCEPTION 'account inventory compaction identity is immutable'
                USING ERRCODE = '23514';
        END IF;
    ELSE
        IF NEW.rollup_run_id <> OLD.rollup_run_id
           OR NEW.summary_date <> OLD.summary_date
           OR NEW.instance_id <> OLD.instance_id
           OR NEW.created_at <> OLD.created_at THEN
            RAISE EXCEPTION 'account inventory rollup identity is immutable'
                USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER account_inventory_compaction_runs_guard
BEFORE UPDATE OR DELETE ON account_inventory_compaction_runs
FOR EACH ROW EXECUTE FUNCTION public.control_protect_account_inventory_history_run();
CREATE TRIGGER account_inventory_compaction_runs_truncate_guard
BEFORE TRUNCATE ON account_inventory_compaction_runs
FOR EACH STATEMENT EXECUTE FUNCTION public.control_protect_account_inventory_history_run();
CREATE TRIGGER account_inventory_daily_rollup_runs_guard
BEFORE UPDATE OR DELETE ON account_inventory_daily_rollup_runs
FOR EACH ROW EXECUTE FUNCTION public.control_protect_account_inventory_history_run();
CREATE TRIGGER account_inventory_daily_rollup_runs_truncate_guard
BEFORE TRUNCATE ON account_inventory_daily_rollup_runs
FOR EACH STATEMENT EXECUTE FUNCTION public.control_protect_account_inventory_history_run();

-- A retired day is a durable negative fact.  It can only be inserted by the
-- rollup-run retention function for the exact day/instance being removed.
-- +goose StatementBegin
CREATE FUNCTION public.control_protect_account_inventory_history_retired_day()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE gate text := coalesce(
    current_setting('relay_control.history_retired_day_write', true), '');
BEGIN
    IF TG_OP = 'INSERT'
       AND current_user = 'relay_control_migrator'
       AND session_user <> current_user
       AND pg_has_role(session_user, 'relay_control_runtime', 'member')
       AND NOT pg_has_role(session_user, 'relay_control_migrator', 'member')
       AND gate = NEW.summary_date::text || ':' || NEW.instance_id::text THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'account inventory retired day requires controlled insertion'
        USING ERRCODE = '42501';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER account_inventory_history_retired_days_guard
BEFORE INSERT OR UPDATE OR DELETE ON account_inventory_history_retired_days
FOR EACH ROW EXECUTE FUNCTION public.control_protect_account_inventory_history_retired_day();
CREATE TRIGGER account_inventory_history_retired_days_truncate_guard
BEFORE TRUNCATE ON account_inventory_history_retired_days
FOR EACH STATEMENT EXECUTE FUNCTION public.control_protect_account_inventory_history_retired_day();

-- Once retired, a day cannot acquire new source evidence and therefore cannot
-- become eligible for planning again.
-- +goose StatementBegin
CREATE FUNCTION public.control_reject_account_inventory_retired_day_poll()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
SET TimeZone = 'UTC'
AS $$
DECLARE
    poll_summary_date date := (NEW.scheduled_at AT TIME ZONE 'UTC')::date;
BEGIN
    PERFORM pg_advisory_xact_lock(
        hashtext(NEW.instance_id::text),
        (poll_summary_date - date '2000-01-01')::integer
    );
    IF EXISTS (
        SELECT 1 FROM public.account_inventory_history_retired_days AS retired
        WHERE retired.summary_date=poll_summary_date
          AND retired.instance_id=NEW.instance_id
    ) THEN
        RAISE EXCEPTION 'account inventory poll day is retired'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER account_inventory_poll_runs_retired_day_guard
BEFORE INSERT ON account_inventory_poll_runs
FOR EACH ROW EXECUTE FUNCTION public.control_reject_account_inventory_retired_day_poll();

-- Current product health must survive legal deletion of the historical poll
-- pointer.  Migration only copies the non-identity health projection from the
-- exact current result; it neither creates Provider states nor summary rows.
ALTER TABLE account_inventory_provider_states
    ADD COLUMN health_scheduled_at timestamptz,
    ADD COLUMN health_degraded boolean,
    ADD COLUMN health_reason text;

ALTER TABLE account_inventory_provider_states
    DISABLE TRIGGER account_inventory_provider_states_guard;
UPDATE account_inventory_provider_states AS state
SET health_scheduled_at = run.scheduled_at,
    health_degraded = result.degraded,
    health_reason = CASE WHEN result.degraded THEN result.reason ELSE 'none' END
FROM account_inventory_poll_runs AS run
JOIN account_inventory_poll_provider_results AS result
  ON result.poll_run_id = run.poll_run_id
WHERE run.poll_run_id = state.current_poll_run_id
  AND run.instance_id = state.instance_id
  AND result.provider = state.provider
  AND run.status = 'finalized'
  AND result.promotion_applied;
ALTER TABLE account_inventory_provider_states
    ENABLE TRIGGER account_inventory_provider_states_guard;

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM public.account_inventory_provider_states
        WHERE health_scheduled_at IS NULL OR health_degraded IS NULL OR health_reason IS NULL
    ) THEN
        RAISE EXCEPTION 'Provider current health backfill is incomplete'
            USING ERRCODE = '55000';
    END IF;
END;
$$;
-- +goose StatementEnd

ALTER TABLE account_inventory_provider_states
    ALTER COLUMN health_scheduled_at SET NOT NULL,
    ALTER COLUMN health_degraded SET NOT NULL,
    ALTER COLUMN health_reason SET NOT NULL,
    ADD CONSTRAINT account_inventory_provider_health_slot_aligned CHECK (
        extract(epoch FROM health_scheduled_at)::bigint % 300 = 0
        AND health_scheduled_at >= current_scheduled_at
    ),
    ADD CONSTRAINT account_inventory_provider_health_reason_fixed CHECK (
        health_reason IN (
            'none', 'transport_failed', 'contract_invalid', 'disk_fallback',
            'node_identity_incomplete', 'identity_incomplete'
        )
        AND (health_degraded = (health_reason <> 'none'))
    );

-- The guard admits three exact shapes only: FK SET NULL retention, policy
-- scope transition, and finalize-time monotonic health refresh.  Pointer
-- promotion keeps the pre-existing applied-promotion proof.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_protect_account_inventory_provider_state()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE
    write_gate text := coalesce(current_setting('relay_control.lifecycle_write', true), '');
    expected_health boolean;
    expected_reason text;
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT result.degraded,
               CASE WHEN result.degraded THEN result.reason ELSE 'none' END,
               run.scheduled_at
        INTO NEW.health_degraded, NEW.health_reason, NEW.health_scheduled_at
        FROM public.account_inventory_poll_provider_results AS result
        JOIN public.account_inventory_poll_runs AS run
          ON run.poll_run_id = result.poll_run_id
        WHERE result.poll_run_id = NEW.current_poll_run_id
          AND result.provider = NEW.provider
          AND result.promotion_applied;
        IF NEW.monitoring_status <> 'active' OR NEW.out_of_scope_since IS NOT NULL
           OR NEW.health_scheduled_at IS NULL THEN
            RAISE EXCEPTION 'account inventory provider state requires applied promotion'
                USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'account inventory provider state cannot be deleted'
            USING ERRCODE = '42501';
    END IF;
    IF NEW.instance_id <> OLD.instance_id OR NEW.provider <> OLD.provider THEN
        RAISE EXCEPTION 'account inventory provider state identity is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.current_poll_run_id IS NULL
       AND OLD.current_poll_run_id IS NOT NULL
       AND NEW.current_scheduled_at = OLD.current_scheduled_at
       AND NEW.last_complete_at = OLD.last_complete_at
       AND NEW.source_observed_at = OLD.source_observed_at
       AND NEW.source_node_version = OLD.source_node_version
       AND NEW.source_node_commit = OLD.source_node_commit
       AND NEW.state = OLD.state AND NEW.updated_at = OLD.updated_at
       AND NEW.monitoring_status = OLD.monitoring_status
       AND NEW.out_of_scope_since IS NOT DISTINCT FROM OLD.out_of_scope_since
       AND NEW.health_scheduled_at = OLD.health_scheduled_at
       AND NEW.health_degraded = OLD.health_degraded
       AND NEW.health_reason = OLD.health_reason THEN
        RETURN NEW;
    END IF;
    IF write_gate = 'policy'
       AND NEW.current_poll_run_id IS NOT DISTINCT FROM OLD.current_poll_run_id
       AND NEW.current_scheduled_at = OLD.current_scheduled_at
       AND NEW.last_complete_at = OLD.last_complete_at
       AND NEW.source_observed_at = OLD.source_observed_at
       AND NEW.source_node_version = OLD.source_node_version
       AND NEW.source_node_commit = OLD.source_node_commit
       AND NEW.state = OLD.state
       AND NEW.health_scheduled_at = OLD.health_scheduled_at
       AND NEW.health_degraded = OLD.health_degraded
       AND NEW.health_reason = OLD.health_reason
       AND ((OLD.monitoring_status = 'active' AND NEW.monitoring_status = 'out_of_scope'
             AND NEW.out_of_scope_since IS NOT NULL)
            OR (OLD.monitoring_status = 'out_of_scope' AND NEW.monitoring_status = 'active'
                AND NEW.out_of_scope_since IS NULL)) THEN
        RETURN NEW;
    END IF;
    IF write_gate = 'health'
       AND NEW.current_poll_run_id IS NOT DISTINCT FROM OLD.current_poll_run_id
       AND NEW.current_scheduled_at = OLD.current_scheduled_at
       AND NEW.last_complete_at = OLD.last_complete_at
       AND NEW.source_observed_at = OLD.source_observed_at
       AND NEW.source_node_version = OLD.source_node_version
       AND NEW.source_node_commit = OLD.source_node_commit
       AND NEW.state = OLD.state AND NEW.updated_at = OLD.updated_at
       AND NEW.monitoring_status = OLD.monitoring_status
       AND NEW.out_of_scope_since IS NOT DISTINCT FROM OLD.out_of_scope_since
       AND NEW.monitoring_status = 'active'
       AND NEW.health_scheduled_at > OLD.health_scheduled_at THEN
        SELECT result.degraded, CASE WHEN result.degraded THEN result.reason ELSE 'none' END
        INTO expected_health, expected_reason
        FROM public.account_inventory_poll_runs AS run
        JOIN public.account_inventory_poll_provider_results AS result
          ON result.poll_run_id = run.poll_run_id AND result.provider = NEW.provider
        JOIN public.provider_inventory_policy_activations AS activation
          ON activation.node_type = run.node_type
         AND activation.driver_contract_version = run.driver_contract_version
         AND activation.policy_version_id = run.provider_policy_version
         AND activation.active_range @> clock_timestamp()
        WHERE run.instance_id = NEW.instance_id
          AND run.scheduled_at = NEW.health_scheduled_at
          AND run.status = 'finalized'
          AND run.promotion_skipped_reason IS DISTINCT FROM 'policy_changed'
          AND result.promotion_skipped_reason IS DISTINCT FROM 'policy_changed'
          AND result.promotion_skipped_reason IS DISTINCT FROM 'stale_poll';
        IF FOUND AND NEW.health_degraded = expected_health AND NEW.health_reason = expected_reason THEN
            RETURN NEW;
        END IF;
    END IF;
    IF NEW.current_poll_run_id IS DISTINCT FROM OLD.current_poll_run_id
       AND NEW.current_scheduled_at > OLD.current_scheduled_at THEN
        SELECT result.degraded,
               CASE WHEN result.degraded THEN result.reason ELSE 'none' END,
               run.scheduled_at
        INTO NEW.health_degraded, NEW.health_reason, NEW.health_scheduled_at
        FROM public.account_inventory_poll_provider_results AS result
        JOIN public.account_inventory_poll_runs AS run
          ON run.poll_run_id = result.poll_run_id
        WHERE result.poll_run_id = NEW.current_poll_run_id
          AND result.provider = NEW.provider
          AND result.promotion_applied;
    END IF;
    IF NEW.monitoring_status <> 'active' OR NEW.out_of_scope_since IS NOT NULL
       OR NEW.current_scheduled_at <= OLD.current_scheduled_at
       OR NEW.health_scheduled_at <> NEW.current_scheduled_at THEN
        RAISE EXCEPTION 'account inventory provider pointer must advance while active'
            USING ERRCODE = '23514';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM public.account_inventory_poll_provider_results AS result
        WHERE result.poll_run_id = NEW.current_poll_run_id
          AND result.provider = NEW.provider AND result.promotion_applied
    ) THEN
        RAISE EXCEPTION 'account inventory provider state requires applied promotion'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_refresh_account_inventory_provider_health_v1()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
SET TimeZone = 'UTC'
AS $$
DECLARE prior_gate text := coalesce(current_setting('relay_control.lifecycle_write', true), '');
BEGIN
    IF OLD.status NOT IN ('finalized', 'abandoned') AND NEW.status = 'finalized'
       AND NEW.promotion_skipped_reason IS DISTINCT FROM 'policy_changed' THEN
        PERFORM set_config('relay_control.lifecycle_write', 'health', true);
        UPDATE public.account_inventory_provider_states AS state
        SET health_scheduled_at = NEW.scheduled_at,
            health_degraded = result.degraded,
            health_reason = CASE WHEN result.degraded THEN result.reason ELSE 'none' END
        FROM public.account_inventory_poll_provider_results AS result
        WHERE result.poll_run_id = NEW.poll_run_id
          AND result.provider = state.provider
          AND state.instance_id = NEW.instance_id
          AND state.monitoring_status = 'active'
          AND state.health_scheduled_at < NEW.scheduled_at
          AND result.promotion_skipped_reason IS DISTINCT FROM 'policy_changed'
          AND result.promotion_skipped_reason IS DISTINCT FROM 'stale_poll'
          AND EXISTS (
              SELECT 1 FROM public.provider_inventory_policy_activations AS activation
              WHERE activation.node_type = NEW.node_type
                AND activation.driver_contract_version = NEW.driver_contract_version
                AND activation.policy_version_id = NEW.provider_policy_version
                AND activation.active_range @> clock_timestamp()
          );
        PERFORM set_config('relay_control.lifecycle_write', prior_gate, true);
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_refresh_account_inventory_provider_health_v1()
    OWNER TO relay_control_migrator;
CREATE TRIGGER account_inventory_poll_runs_refresh_provider_health_v1
AFTER UPDATE ON account_inventory_poll_runs
FOR EACH ROW EXECUTE FUNCTION public.control_refresh_account_inventory_provider_health_v1();

-- Keep the v1 product signature unchanged.  A non-NULL source pointer still
-- has to resolve, while a retention-cleared pointer is valid because all
-- product health fields now live in Provider current state.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_query_current_account_inventory_v1(
    target_instance_id uuid,
    target_provider text,
    target_lifecycle text,
    target_basic_status text,
    target_normalized_email text,
    after_account_key text,
    page_limit integer
) RETURNS TABLE (
    instance_id uuid,
    provider text,
    account_key text,
    normalized_email text,
    basic_status text,
    lifecycle text,
    consecutive_missing_count integer,
    first_seen_at timestamptz,
    last_seen_at timestamptz,
    missing_since timestamptz,
    out_of_scope_since timestamptz,
    last_refresh_at timestamptz,
    next_retry_at timestamptz,
    source_updated_at timestamptz,
    provider_last_complete_at timestamptz,
    provider_degraded boolean,
    snapshot_freshness text
)
LANGUAGE plpgsql
SECURITY DEFINER
STABLE
SET search_path = pg_catalog
SET TimeZone = 'UTC'
AS $$
#variable_conflict use_variable
DECLARE
    database_now timestamptz := clock_timestamp();
BEGIN
    IF target_instance_id IS NULL
       OR target_provider IS NULL OR target_lifecycle IS NULL
       OR target_basic_status IS NULL OR target_normalized_email IS NULL
       OR after_account_key IS NULL OR page_limit IS NULL
       OR (target_provider <> '' AND (
           octet_length(target_provider) NOT BETWEEN 1 AND 64
           OR target_provider !~ '^[a-z0-9][a-z0-9._-]*$'))
       OR (target_lifecycle <> '' AND target_lifecycle NOT IN (
           'present', 'suspected_missing', 'missing', 'out_of_scope'))
       OR (target_basic_status <> '' AND target_basic_status NOT IN (
           'reported_active', 'disabled', 'unavailable', 'error', 'unknown'))
       OR (target_normalized_email <> '' AND (
           octet_length(target_normalized_email) NOT BETWEEN 1 AND 320
           OR target_normalized_email <> lower(btrim(target_normalized_email))
           OR target_normalized_email ~ '[[:cntrl:]]'))
       OR octet_length(after_account_key) > 385
       OR (after_account_key <> '' AND (
           strpos(after_account_key, ':') < 2
           OR after_account_key <> lower(btrim(after_account_key))
           OR after_account_key ~ '[[:cntrl:]]'))
       OR page_limit NOT BETWEEN 1 AND 101 THEN
        RAISE EXCEPTION 'invalid account inventory query' USING ERRCODE = '22023';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM public.relay_node_assets AS asset
        WHERE asset.instance_id = target_instance_id
    ) THEN
        RAISE EXCEPTION 'account inventory instance is not registered'
            USING ERRCODE = 'P0404';
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM public.relay_node_assets AS asset
        JOIN public.node_capabilities AS capability
          ON capability.instance_id = asset.instance_id
         AND capability.node_type = asset.node_type
         AND capability.driver_contract_version = asset.driver_contract_version
        WHERE asset.instance_id = target_instance_id
          AND capability.capability = 'management_account_inventory_read'
    ) THEN
        RAISE EXCEPTION 'account inventory capability is unavailable'
            USING ERRCODE = 'P0409';
    END IF;
    IF EXISTS (
        SELECT 1
        FROM public.account_inventory AS account
        LEFT JOIN public.account_inventory_provider_states AS state
          ON state.instance_id = account.instance_id AND state.provider = account.provider
        LEFT JOIN public.account_inventory_poll_provider_results AS source
          ON source.poll_run_id = state.current_poll_run_id AND source.provider = state.provider
        WHERE account.instance_id = target_instance_id
          AND (target_provider = '' OR account.provider = target_provider)
          AND (target_lifecycle = '' OR account.lifecycle = target_lifecycle)
          AND (target_basic_status = '' OR account.basic_status =
              CASE target_basic_status WHEN 'reported_active' THEN 'active' ELSE target_basic_status END)
          AND (target_normalized_email = '' OR account.normalized_email = target_normalized_email)
          AND account.account_key > after_account_key
          AND (state.instance_id IS NULL OR state.state <> 'current'
               OR state.last_complete_at IS NULL OR state.health_scheduled_at IS NULL
               OR state.health_degraded IS NULL OR state.health_reason IS NULL
               OR state.health_scheduled_at < state.current_scheduled_at
               OR state.health_degraded <> (state.health_reason <> 'none')
               OR (state.current_poll_run_id IS NOT NULL AND source.poll_run_id IS NULL)
               OR (account.lifecycle = 'out_of_scope')
                    <> (state.monitoring_status = 'out_of_scope'))
    ) THEN
        RAISE EXCEPTION 'account inventory current state is inconsistent'
            USING ERRCODE = 'P0503';
    END IF;

    RETURN QUERY
    SELECT account.instance_id, account.provider, account.account_key,
           account.normalized_email,
           CASE account.basic_status WHEN 'active' THEN 'reported_active'
                ELSE account.basic_status END,
           account.lifecycle, account.consecutive_missing_count,
           account.first_seen_at, account.last_seen_at,
           account.missing_since, account.out_of_scope_since,
           account.last_refresh_at, account.next_retry_at,
           account.source_updated_at, state.last_complete_at,
           state.health_degraded,
           CASE WHEN account.lifecycle = 'out_of_scope' THEN 'out_of_scope'
                WHEN database_now - state.last_complete_at > interval '15 minutes' THEN 'stale'
                ELSE 'fresh' END
    FROM public.account_inventory AS account
    JOIN public.account_inventory_provider_states AS state
      ON state.instance_id = account.instance_id AND state.provider = account.provider
    WHERE account.instance_id = target_instance_id
      AND (target_provider = '' OR account.provider = target_provider)
      AND (target_lifecycle = '' OR account.lifecycle = target_lifecycle)
      AND (target_basic_status = '' OR account.basic_status =
          CASE target_basic_status WHEN 'reported_active' THEN 'active' ELSE target_basic_status END)
      AND (target_normalized_email = '' OR account.normalized_email = target_normalized_email)
      AND account.account_key > after_account_key
    ORDER BY account.account_key
    LIMIT page_limit;
END;
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_query_current_account_inventory_v1(
    uuid, text, text, text, text, text, integer
) OWNER TO relay_control_migrator;

-- Compaction emits only fixed, non-secret operational facts.  In particular,
-- run/fence/policy identifiers and checksums never enter the audit stream.
ALTER TABLE audit_logs DROP CONSTRAINT audit_logs_category_valid;
ALTER TABLE audit_logs ADD CONSTRAINT audit_logs_category_valid CHECK (
    category IN ('bootstrap', 'administrator', 'password', 'mfa', 'session',
                 'reauthentication', 'authorization', 'rate_limit',
                 'account_inventory', 'account_inventory_history')
);
ALTER TABLE audit_logs DROP CONSTRAINT audit_logs_action_valid;
ALTER TABLE audit_logs ADD CONSTRAINT audit_logs_action_valid CHECK (
    action IN (
        'bootstrap.start', 'bootstrap.complete', 'bootstrap.reset',
        'auth.login_password', 'auth.login_mfa', 'auth.logout',
        'auth.password_change', 'auth.mfa_enroll', 'auth.mfa_reset',
        'auth.recovery_code_use', 'auth.recovery_codes_regenerate',
        'session.create', 'session.revoke', 'auth.reauthenticate',
        'administrator.create', 'administrator.activate', 'administrator.disable',
        'administrator.activation_token_generate', 'authorization.check',
        'auth.rate_limit', 'auth.csrf', 'account_inventory.view',
        'account_inventory_history.summarized',
        'account_inventory_history.snapshot_delete_batch',
        'account_inventory_history.retention_delete_batch',
        'account_inventory_history.completed',
        'account_inventory_history.failed'
    )
);
ALTER TABLE audit_logs ADD CONSTRAINT audit_logs_history_shape CHECK (
    category <> 'account_inventory_history'
    OR (
        action IN (
            'account_inventory_history.summarized',
            'account_inventory_history.snapshot_delete_batch',
            'account_inventory_history.retention_delete_batch',
            'account_inventory_history.completed',
            'account_inventory_history.failed'
        )
        AND actor_admin_id IS NULL AND target_admin_id IS NULL
        AND actor_fingerprint IS NULL AND source_fingerprint IS NULL
        AND reason IS NULL AND request_id = 'history-compaction-system'
        AND details ?& ARRAY['instance', 'summary_date', 'phase', 'row_count']
        AND details - ARRAY['instance', 'summary_date', 'phase', 'row_count'] = '{}'::jsonb
        AND jsonb_typeof(details->'instance') = 'string'
        AND jsonb_typeof(details->'summary_date') = 'string'
        AND jsonb_typeof(details->'phase') = 'string'
        AND jsonb_typeof(details->'row_count') = 'number'
        AND (details->>'instance') ~
            '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
        AND (details->>'summary_date') ~ '^2[0-9]{3}-[0-9]{2}-[0-9]{2}$'
        AND (details->>'phase') IN (
            'summarize', 'snapshot_delete', 'complete',
            'fail_pending', 'fail_summarized', 'fail_deleting',
            'rollup_complete', 'rollup_fail_pending',
            'retention_poll', 'retention_rollup_rows',
            'retention_rollup_run', 'retention_compaction_run'
        )
        AND (details->>'row_count') ~ '^(0|[1-9][0-9]{0,18})$'
        AND ((action = 'account_inventory_history.summarized'
              AND result = 'success' AND details->>'phase' = 'summarize')
          OR (action = 'account_inventory_history.snapshot_delete_batch'
              AND result = 'success' AND details->>'phase' = 'snapshot_delete')
          OR (action = 'account_inventory_history.retention_delete_batch'
              AND result = 'success'
              AND details->>'phase' IN (
                  'retention_poll','retention_rollup_rows',
                  'retention_rollup_run','retention_compaction_run'))
          OR (action = 'account_inventory_history.completed'
              AND result = 'success'
              AND details->>'phase' IN ('complete','rollup_complete'))
          OR (action = 'account_inventory_history.failed'
              AND result = 'failure'
              AND details->>'phase' IN (
                  'fail_pending','fail_summarized','fail_deleting','rollup_fail_pending')))
    )
);

-- Runtime retains the pre-existing generic audit INSERT privilege, so history
-- evidence needs an independent exact gate. Only the versioned SECURITY
-- DEFINER functions may emit these four actions.
-- +goose StatementBegin
CREATE FUNCTION public.control_protect_account_inventory_history_audit()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE
    audit_gate text := coalesce(
        current_setting('relay_control.history_audit_write', true), '');
BEGIN
    IF NEW.category <> 'account_inventory_history' THEN
        RETURN NEW;
    END IF;
    IF current_user <> 'relay_control_migrator'
       OR session_user = current_user
       OR NOT pg_has_role(session_user, 'relay_control_runtime', 'member')
       OR pg_has_role(session_user, 'relay_control_migrator', 'member')
       OR audit_gate <> NEW.action || ':' || (NEW.details->>'phase') THEN
        RAISE EXCEPTION 'account inventory history audit requires controlled insertion'
            USING ERRCODE = '42501';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER audit_logs_account_inventory_history_guard
BEFORE INSERT ON audit_logs
FOR EACH ROW EXECUTE FUNCTION public.control_protect_account_inventory_history_audit();

-- A DELETE is legal only inside the fenced compaction batch function.  The
-- gate contains the exact run id and the trigger independently proves that
-- OLD belongs to that run's immutable source day.  UPDATE/TRUNCATE and all
-- duplicate-evidence mutations remain forbidden.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_reject_account_inventory_snapshot_mutation()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE
    delete_gate text := coalesce(
        current_setting('relay_control.history_snapshot_delete', true), '');
    poll_retention_gate text := coalesce(
        current_setting('relay_control.history_poll_retention_delete', true), '');
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF current_user = 'relay_control_migrator'
           AND session_user <> current_user
           AND pg_has_role(session_user, 'relay_control_runtime', 'member')
           AND NOT pg_has_role(session_user, 'relay_control_migrator', 'member')
           AND poll_retention_gate = OLD.poll_run_id::text THEN
            RETURN OLD;
        END IF;
    END IF;
    IF TG_TABLE_NAME = 'account_inventory_snapshot_items'
       AND TG_OP = 'DELETE' THEN
        IF current_user = 'relay_control_migrator'
           AND session_user <> current_user
           AND pg_has_role(session_user, 'relay_control_runtime', 'member')
           AND NOT pg_has_role(session_user, 'relay_control_migrator', 'member')
           AND delete_gate ~
               '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
           AND EXISTS (
               SELECT 1
               FROM public.account_inventory_compaction_runs AS compaction
               JOIN public.account_inventory_poll_runs AS poll
                 ON poll.poll_run_id = OLD.poll_run_id
                AND poll.instance_id = compaction.instance_id
                AND poll.provider_policy_version = compaction.provider_policy_version
               WHERE compaction.compaction_run_id = delete_gate::uuid
                 AND compaction.status = 'deleting'
                 AND OLD.instance_id = compaction.instance_id
                 AND poll.scheduled_at >=
                     (compaction.summary_date::timestamp AT TIME ZONE 'UTC')
                 AND poll.scheduled_at <
                     ((compaction.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
                 AND OLD.observed_at >=
                     (compaction.summary_date::timestamp AT TIME ZONE 'UTC')
                 AND OLD.observed_at <
                     ((compaction.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
           ) THEN
            RETURN OLD;
        END IF;
    END IF;
    RAISE EXCEPTION 'account inventory snapshot evidence is immutable'
        USING ERRCODE = '42501';
END;
$$;
-- +goose StatementEnd

-- Terminal poll evidence is deletable only through the exact retention gate.
-- All state transitions keep Migration 5's original proof checks.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_protect_account_inventory_poll_run()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE
    expected_providers text[];
    stored_providers text[];
    retention_gate text := coalesce(
        current_setting('relay_control.history_poll_retention_delete', true), '');
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF current_user = 'relay_control_migrator'
           AND session_user <> current_user
           AND pg_has_role(session_user, 'relay_control_runtime', 'member')
           AND NOT pg_has_role(session_user, 'relay_control_migrator', 'member')
           AND OLD.status IN ('finalized', 'abandoned')
           AND retention_gate = OLD.poll_run_id::text THEN
            RETURN OLD;
        END IF;
        RAISE EXCEPTION 'terminal account inventory poll evidence is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF OLD.status IN ('finalized', 'abandoned') THEN
        RAISE EXCEPTION 'terminal account inventory poll evidence is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.poll_run_id <> OLD.poll_run_id
       OR NEW.instance_id <> OLD.instance_id
       OR NEW.node_type <> OLD.node_type
       OR NEW.driver_contract_version <> OLD.driver_contract_version
       OR NEW.scheduled_at <> OLD.scheduled_at
       OR NEW.provider_policy_version <> OLD.provider_policy_version
       OR NEW.max_attempts <> OLD.max_attempts
       OR NEW.poll_start_grace_seconds <> OLD.poll_start_grace_seconds
       OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'account inventory poll identity is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF NOT (
        (OLD.status = 'pending' AND NEW.status IN ('running', 'abandoned'))
        OR (OLD.status = 'running' AND NEW.status IN ('retry_wait', 'finalized', 'abandoned'))
        OR (OLD.status = 'retry_wait' AND NEW.status IN ('running', 'abandoned'))
    ) THEN
        RAISE EXCEPTION 'invalid account inventory poll transition'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.status = 'abandoned' AND EXISTS (
        SELECT 1 FROM public.account_inventory_poll_provider_results AS result
        WHERE result.poll_run_id = NEW.poll_run_id
    ) THEN
        RAISE EXCEPTION 'abandoned account inventory poll cannot contain provider evidence'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.status = 'finalized' THEN
        SELECT policy.active_providers INTO expected_providers
        FROM public.provider_inventory_policy_versions AS policy
        WHERE policy.policy_version_id = NEW.provider_policy_version
          AND policy.node_type = NEW.node_type
          AND policy.driver_contract_version = NEW.driver_contract_version;
        SELECT array_agg(result.provider ORDER BY result.provider)
        INTO stored_providers
        FROM public.account_inventory_poll_provider_results AS result
        WHERE result.poll_run_id = NEW.poll_run_id;
        IF stored_providers IS DISTINCT FROM expected_providers THEN
            RAISE EXCEPTION 'finalized account inventory poll provider set is incomplete'
                USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_protect_account_inventory_provider_result()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE
    parent_status text;
    retention_gate text := coalesce(
        current_setting('relay_control.history_poll_retention_delete', true), '');
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF current_user = 'relay_control_migrator'
           AND session_user <> current_user
           AND pg_has_role(session_user, 'relay_control_runtime', 'member')
           AND NOT pg_has_role(session_user, 'relay_control_migrator', 'member')
           AND retention_gate = OLD.poll_run_id::text THEN
            RETURN OLD;
        END IF;
        RAISE EXCEPTION 'account inventory provider evidence is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION 'account inventory provider evidence is immutable'
            USING ERRCODE = '23514';
    END IF;
    SELECT run.status INTO parent_status
    FROM public.account_inventory_poll_runs AS run
    WHERE run.poll_run_id = NEW.poll_run_id
    FOR KEY SHARE;
    IF parent_status <> 'running' THEN
        RAISE EXCEPTION 'provider evidence requires a running poll'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_plan_account_inventory_history_v1(schedule_limit integer)
RETURNS jsonb
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
SET TimeZone = 'UTC'
AS $$
DECLARE
    database_now timestamptz;
    eligible_last_date date;
    inserted_compactions integer := 0;
    inserted_rollups integer := 0;
BEGIN
    IF schedule_limit IS NULL OR schedule_limit NOT BETWEEN 1 AND 1000 THEN
        RAISE EXCEPTION 'invalid account inventory history planning limit'
            USING ERRCODE = '22023';
    END IF;
    PERFORM pg_advisory_xact_lock(72193847561029384);
    database_now := clock_timestamp();
    eligible_last_date := ((database_now - interval '72 hours') AT TIME ZONE 'UTC')::date - 1;

    WITH activation_intersections AS (
        SELECT asset.instance_id, policy.policy_version_id,
               greatest(policy.effective_from, monitoring.effective_from) AS lower_at,
               least(coalesce(policy.effective_to, 'infinity'::timestamptz),
                     coalesce(monitoring.effective_to, 'infinity'::timestamptz)) AS upper_at
        FROM public.provider_inventory_policy_activations AS policy
        JOIN public.relay_node_assets AS asset
          ON asset.node_type = policy.node_type
         AND asset.driver_contract_version = policy.driver_contract_version
        JOIN public.relay_node_inventory_monitoring_activations AS monitoring
          ON monitoring.instance_id = asset.instance_id
         AND policy.active_range && monitoring.active_range
        WHERE policy.effective_from <
              ((eligible_last_date + 1)::timestamp AT TIME ZONE 'UTC')
          AND monitoring.effective_from <
              ((eligible_last_date + 1)::timestamp AT TIME ZONE 'UTC')
    ), candidate_keys AS (
        SELECT DISTINCT generated.summary_date, intersection.instance_id,
               intersection.policy_version_id
        FROM activation_intersections AS intersection
        CROSS JOIN LATERAL generate_series(
            (intersection.lower_at AT TIME ZONE 'UTC')::date,
            least(((intersection.upper_at - interval '1 microsecond') AT TIME ZONE 'UTC')::date,
                  eligible_last_date),
            interval '1 day'
        ) AS generated_at
        CROSS JOIN LATERAL (
            SELECT generated_at::date AS summary_date,
                   greatest(intersection.lower_at,
                       (generated_at::date::timestamp AT TIME ZONE 'UTC')) AS segment_lower,
                   least(intersection.upper_at,
                       ((generated_at::date + 1)::timestamp AT TIME ZONE 'UTC')) AS segment_upper
        ) AS generated
        WHERE to_timestamp(ceil(extract(epoch FROM generated.segment_lower) / 300) * 300)
              < generated.segment_upper
          AND NOT EXISTS (
              SELECT 1 FROM public.account_inventory_history_retired_days AS retired
              WHERE retired.summary_date=generated.summary_date
                AND retired.instance_id=intersection.instance_id
          )
          AND NOT EXISTS (
              SELECT 1 FROM public.account_inventory_compaction_runs AS existing
              WHERE existing.summary_date=generated.summary_date
                AND existing.instance_id=intersection.instance_id
                AND existing.provider_policy_version=intersection.policy_version_id
          )
          AND (
              ((generated.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
                  > database_now - interval '30 days'
              OR EXISTS (
                  SELECT 1 FROM public.account_inventory_poll_runs AS poll
                  WHERE poll.instance_id = intersection.instance_id
                    AND poll.scheduled_at >=
                        (generated.summary_date::timestamp AT TIME ZONE 'UTC')
                    AND poll.scheduled_at <
                        ((generated.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
              )
              OR EXISTS (
                  SELECT 1 FROM public.account_inventory_compaction_runs AS existing
                  WHERE existing.summary_date=generated.summary_date
                    AND existing.instance_id=intersection.instance_id
              )
          )
        ORDER BY generated.summary_date, intersection.instance_id,
                 intersection.policy_version_id
        LIMIT schedule_limit
    ), inserted AS (
        INSERT INTO public.account_inventory_compaction_runs (
            summary_date, instance_id, provider_policy_version
        )
        SELECT summary_date, instance_id, policy_version_id FROM candidate_keys
        ON CONFLICT (summary_date, instance_id, provider_policy_version) DO NOTHING
        RETURNING 1
    ) SELECT count(*) INTO inserted_compactions FROM inserted;

    WITH activation_intersections AS (
        SELECT asset.instance_id, policy.policy_version_id,
               greatest(policy.effective_from, monitoring.effective_from) AS lower_at,
               least(coalesce(policy.effective_to, 'infinity'::timestamptz),
                     coalesce(monitoring.effective_to, 'infinity'::timestamptz)) AS upper_at
        FROM public.provider_inventory_policy_activations AS policy
        JOIN public.relay_node_assets AS asset
          ON asset.node_type = policy.node_type
         AND asset.driver_contract_version = policy.driver_contract_version
        JOIN public.relay_node_inventory_monitoring_activations AS monitoring
          ON monitoring.instance_id = asset.instance_id
         AND policy.active_range && monitoring.active_range
        WHERE policy.effective_from <
              ((eligible_last_date + 1)::timestamp AT TIME ZONE 'UTC')
          AND monitoring.effective_from <
              ((eligible_last_date + 1)::timestamp AT TIME ZONE 'UTC')
    ), expected_keys AS (
        SELECT DISTINCT generated.summary_date, intersection.instance_id,
               intersection.policy_version_id
        FROM activation_intersections AS intersection
        CROSS JOIN LATERAL generate_series(
            (intersection.lower_at AT TIME ZONE 'UTC')::date,
            least(((intersection.upper_at - interval '1 microsecond') AT TIME ZONE 'UTC')::date,
                  eligible_last_date), interval '1 day'
        ) AS generated_at
        CROSS JOIN LATERAL (
            SELECT generated_at::date AS summary_date,
                   greatest(intersection.lower_at,
                       (generated_at::date::timestamp AT TIME ZONE 'UTC')) AS segment_lower,
                   least(intersection.upper_at,
                       ((generated_at::date + 1)::timestamp AT TIME ZONE 'UTC')) AS segment_upper
        ) AS generated
        WHERE to_timestamp(ceil(extract(epoch FROM generated.segment_lower) / 300) * 300)
              < generated.segment_upper
          AND NOT EXISTS (
              SELECT 1 FROM public.account_inventory_history_retired_days AS retired
              WHERE retired.summary_date=generated.summary_date
                AND retired.instance_id=intersection.instance_id
          )
          AND (
              ((generated.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
                  > database_now - interval '30 days'
              OR EXISTS (
                  SELECT 1 FROM public.account_inventory_poll_runs AS poll
                  WHERE poll.instance_id = intersection.instance_id
                    AND poll.scheduled_at >=
                        (generated.summary_date::timestamp AT TIME ZONE 'UTC')
                    AND poll.scheduled_at <
                        ((generated.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
              )
              OR EXISTS (
                  SELECT 1 FROM public.account_inventory_compaction_runs AS existing
                  WHERE existing.summary_date=generated.summary_date
                    AND existing.instance_id=intersection.instance_id
              )
          )
    ), ready_days AS (
        SELECT expected.summary_date, expected.instance_id
        FROM expected_keys AS expected
        LEFT JOIN public.account_inventory_compaction_runs AS compaction
          ON compaction.summary_date = expected.summary_date
         AND compaction.instance_id = expected.instance_id
         AND compaction.provider_policy_version = expected.policy_version_id
        WHERE NOT EXISTS (
            SELECT 1 FROM public.account_inventory_daily_rollup_runs AS existing
            WHERE existing.summary_date=expected.summary_date
              AND existing.instance_id=expected.instance_id
        )
        GROUP BY expected.summary_date, expected.instance_id
        HAVING count(*) > 0
           AND count(compaction.compaction_run_id) = count(*)
           AND count(*) FILTER (WHERE compaction.status = 'completed') = count(*)
        ORDER BY expected.summary_date, expected.instance_id
        LIMIT schedule_limit
    ), inserted AS (
        INSERT INTO public.account_inventory_daily_rollup_runs (
            summary_date, instance_id
        )
        SELECT summary_date, instance_id FROM ready_days
        ON CONFLICT (summary_date, instance_id) DO NOTHING
        RETURNING 1
    ) SELECT count(*) INTO inserted_rollups FROM inserted;

    RETURN jsonb_build_object(
        'compaction_runs_created', inserted_compactions,
        'rollup_runs_created', inserted_rollups
    );
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_claim_account_inventory_compaction_v1(
    worker_token uuid, lease_seconds integer
) RETURNS SETOF public.account_inventory_compaction_runs
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
SET TimeZone = 'UTC'
AS $$
DECLARE
    database_now timestamptz := clock_timestamp();
BEGIN
    IF worker_token IS NULL OR lease_seconds NOT BETWEEN 5 AND 300 THEN
        RAISE EXCEPTION 'invalid account inventory compaction claim'
            USING ERRCODE = '22023';
    END IF;
    PERFORM set_config('relay_control.history_run_write',
                       'account_inventory_compaction_runs', true);
    RETURN QUERY
    WITH candidate AS (
        SELECT run.compaction_run_id
        FROM public.account_inventory_compaction_runs AS run
        WHERE (
              (run.status = 'pending' AND run.lease_expires_at IS NULL)
              OR (run.status = 'failed'
                  AND run.failure_reason IN (
                      'lease_expired', 'statement_timeout', 'database_unavailable'
                  )
                  AND run.lease_expires_at IS NULL)
          )
          AND ((run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
                <= database_now - interval '72 hours'
        ORDER BY run.summary_date, run.instance_id, run.provider_policy_version
        FOR UPDATE SKIP LOCKED LIMIT 1
    )
    UPDATE public.account_inventory_compaction_runs AS run
    SET status = CASE WHEN run.status = 'failed' THEN run.failed_from ELSE run.status END,
        failed_from = NULL,
        failure_reason = NULL,
        failed_at = NULL,
        claim_owner = worker_token::text,
        lease_expires_at = database_now + make_interval(secs => lease_seconds),
        fencing_token = gen_random_uuid(), attempt_count = run.attempt_count + 1,
        updated_at = database_now
    FROM candidate
    WHERE run.compaction_run_id = candidate.compaction_run_id
    RETURNING run.*;
    PERFORM set_config('relay_control.history_run_write', '', true);
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_renew_account_inventory_compaction_v1(
    target_run_id uuid, target_fencing_token uuid, lease_seconds integer
) RETURNS SETOF public.account_inventory_compaction_runs
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
SET TimeZone = 'UTC'
AS $$
DECLARE database_now timestamptz := clock_timestamp();
BEGIN
    IF target_run_id IS NULL OR target_fencing_token IS NULL
       OR lease_seconds NOT BETWEEN 5 AND 300 THEN
        RAISE EXCEPTION 'invalid account inventory compaction renewal'
            USING ERRCODE = '22023';
    END IF;
    PERFORM run.compaction_run_id
    FROM public.account_inventory_compaction_runs AS run
    WHERE run.compaction_run_id = target_run_id
    FOR UPDATE;
    database_now := clock_timestamp();
    PERFORM set_config('relay_control.history_run_write',
                       'account_inventory_compaction_runs', true);
    RETURN QUERY
    UPDATE public.account_inventory_compaction_runs AS run
    SET lease_expires_at = database_now + make_interval(secs => lease_seconds),
        updated_at = database_now
    WHERE run.compaction_run_id = target_run_id
      AND run.fencing_token = target_fencing_token
      AND run.status IN ('pending', 'summarized', 'deleting')
      AND run.lease_expires_at > database_now
    RETURNING run.*;
    PERFORM set_config('relay_control.history_run_write', '', true);
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_reconcile_account_inventory_compactions_v1(
    reconcile_limit integer
) RETURNS jsonb
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
SET TimeZone = 'UTC'
AS $$
DECLARE
    database_now timestamptz := clock_timestamp();
    failed_count bigint := 0;
    expired_run record;
    failed_run record;
BEGIN
    IF reconcile_limit IS NULL OR reconcile_limit NOT BETWEEN 1 AND 1000 THEN
        RAISE EXCEPTION 'invalid account inventory compaction reconcile limit'
            USING ERRCODE = '22023';
    END IF;
    FOR expired_run IN
        SELECT run.compaction_run_id, run.status
        FROM public.account_inventory_compaction_runs AS run
        WHERE run.status IN ('pending', 'summarized', 'deleting')
          AND run.lease_expires_at <= database_now
        ORDER BY run.lease_expires_at, run.compaction_run_id
        FOR UPDATE SKIP LOCKED LIMIT reconcile_limit
    LOOP
        PERFORM set_config('relay_control.history_run_write',
                           'account_inventory_compaction_runs', true);
        UPDATE public.account_inventory_compaction_runs AS run
        SET status = 'failed', failed_from = expired_run.status,
            failure_reason = 'lease_expired', failed_at = database_now,
            claim_owner = NULL, lease_expires_at = NULL, fencing_token = NULL,
            updated_at = database_now
        WHERE run.compaction_run_id = expired_run.compaction_run_id
          AND run.status = expired_run.status
          AND run.lease_expires_at <= database_now
        RETURNING run.instance_id, run.summary_date, run.failed_from INTO failed_run;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'account inventory compaction reconcile lost its lock'
                USING ERRCODE = 'P0002';
        END IF;
        PERFORM set_config('relay_control.history_run_write', '', true);
        PERFORM set_config(
            'relay_control.history_audit_write',
            'account_inventory_history.failed:fail_' || failed_run.failed_from, true);
        INSERT INTO public.audit_logs (
            occurred_at, category, action, result, request_id, details
        ) VALUES (
            database_now, 'account_inventory_history',
            'account_inventory_history.failed', 'failure',
            'history-compaction-system',
            jsonb_build_object(
                'instance', failed_run.instance_id::text,
                'summary_date', failed_run.summary_date::text,
                'phase', 'fail_' || failed_run.failed_from,
                'row_count', 0
            )
        );
        PERFORM set_config('relay_control.history_audit_write', '', true);
        failed_count := failed_count + 1;
    END LOOP;
    RETURN jsonb_build_object('failed_count', failed_count);
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_fail_account_inventory_compaction_v1(
    target_run_id uuid, target_fencing_token uuid, fixed_reason text
) RETURNS jsonb
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
SET TimeZone = 'UTC'
AS $$
DECLARE
    database_now timestamptz := clock_timestamp();
    failed_run record;
BEGIN
    IF target_run_id IS NULL OR target_fencing_token IS NULL
       OR fixed_reason IS NULL OR fixed_reason NOT IN (
           'source_day_mismatch', 'source_count_mismatch',
           'source_checksum_mismatch', 'activation_inconsistent',
           'statement_timeout', 'lease_expired', 'database_unavailable', 'internal'
       ) THEN
        RAISE EXCEPTION 'invalid account inventory compaction failure'
            USING ERRCODE = '22023';
    END IF;
    PERFORM run.compaction_run_id
    FROM public.account_inventory_compaction_runs AS run
    WHERE run.compaction_run_id = target_run_id
    FOR UPDATE;
    database_now := clock_timestamp();
    PERFORM set_config('relay_control.history_run_write',
                       'account_inventory_compaction_runs', true);
    UPDATE public.account_inventory_compaction_runs AS run
    SET status = 'failed', failed_from = run.status,
        failure_reason = fixed_reason, failed_at = database_now,
        claim_owner = NULL, lease_expires_at = NULL, fencing_token = NULL,
        updated_at = database_now
    WHERE run.compaction_run_id = target_run_id
      AND run.fencing_token = target_fencing_token
      AND run.status IN ('pending', 'summarized', 'deleting')
      AND run.lease_expires_at > database_now
    RETURNING run.instance_id, run.summary_date, run.failed_from INTO failed_run;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'account inventory compaction lease is lost'
            USING ERRCODE = 'P0002';
    END IF;
    PERFORM set_config('relay_control.history_run_write', '', true);
    PERFORM set_config(
        'relay_control.history_audit_write',
        'account_inventory_history.failed:fail_' || failed_run.failed_from, true);
    INSERT INTO public.audit_logs (
        occurred_at, category, action, result, request_id, details
    ) VALUES (
        database_now, 'account_inventory_history',
        'account_inventory_history.failed', 'failure',
        'history-compaction-system',
        jsonb_build_object(
            'instance', failed_run.instance_id::text,
            'summary_date', failed_run.summary_date::text,
            'phase', 'fail_' || failed_run.failed_from,
            'row_count', 0
        )
    );
    PERFORM set_config('relay_control.history_audit_write', '', true);
    RETURN jsonb_build_object('status', 'failed', 'failure_reason', fixed_reason);
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_summarize_account_inventory_compaction_v1(
    target_run_id uuid, target_fencing_token uuid
) RETURNS jsonb
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
SET TimeZone = 'UTC'
AS $$
#variable_conflict use_variable
DECLARE
    database_now timestamptz := clock_timestamp();
    target_run public.account_inventory_compaction_runs%ROWTYPE;
    expected_poll_count integer;
    source_snapshot_count bigint;
    source_poll_count bigint;
    source_provider_result_count bigint;
    source_duplicate_count bigint;
    source_rows bigint;
    source_checksum bytea := decode(repeat('00', 32), 'hex');
    source_record record;
    account_segment_count bigint;
    provider_segment_count bigint;
BEGIN
    IF target_run_id IS NULL OR target_fencing_token IS NULL THEN
        RAISE EXCEPTION 'invalid account inventory compaction summarize request'
            USING ERRCODE = '22023';
    END IF;
    SELECT run.* INTO target_run
    FROM public.account_inventory_compaction_runs AS run
    WHERE run.compaction_run_id = target_run_id
    FOR UPDATE;
    database_now := clock_timestamp();
    IF NOT FOUND OR target_run.fencing_token IS DISTINCT FROM target_fencing_token
       OR target_run.lease_expires_at IS NULL
       OR target_run.lease_expires_at <= database_now THEN
        RAISE EXCEPTION 'account inventory compaction lease is lost'
            USING ERRCODE = 'P0002';
    END IF;
    IF target_run.status = 'summarized' THEN
        SELECT count(*) INTO account_segment_count
        FROM public.account_inventory_daily_summaries
        WHERE compaction_run_id = target_run_id;
        SELECT count(*) INTO provider_segment_count
        FROM public.account_inventory_daily_provider_summaries
        WHERE compaction_run_id = target_run_id;
        RETURN jsonb_build_object(
            'status', 'summarized',
            'source_rows', target_run.source_snapshot_count
                + target_run.source_poll_count
                + target_run.source_provider_result_count
                + target_run.source_duplicate_count,
            'source_snapshot_count', target_run.source_snapshot_count,
            'source_checksum_hex', encode(target_run.source_checksum, 'hex'),
            'account_segment_count', account_segment_count,
            'provider_segment_count', provider_segment_count
        );
    END IF;
    IF target_run.status <> 'pending'
       OR ((target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
            > database_now - interval '72 hours' THEN
        RAISE EXCEPTION 'account inventory compaction is not eligible for summarize'
            USING ERRCODE = '23514';
    END IF;

    WITH intersections AS (
        SELECT greatest(policy.effective_from, monitoring.effective_from,
                        target_run.summary_date::timestamp AT TIME ZONE 'UTC') AS lower_at,
               least(coalesce(policy.effective_to, 'infinity'::timestamptz),
                     coalesce(monitoring.effective_to, 'infinity'::timestamptz),
                     (target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC') AS upper_at
        FROM public.relay_node_assets AS asset
        JOIN public.provider_inventory_policy_activations AS policy
          ON policy.node_type = asset.node_type
         AND policy.driver_contract_version = asset.driver_contract_version
         AND policy.policy_version_id = target_run.provider_policy_version
        JOIN public.relay_node_inventory_monitoring_activations AS monitoring
          ON monitoring.instance_id = asset.instance_id
         AND policy.active_range && monitoring.active_range
        WHERE asset.instance_id = target_run.instance_id
          AND policy.effective_from <
              ((target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
          AND coalesce(policy.effective_to, 'infinity'::timestamptz) >
              (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
          AND monitoring.effective_from <
              ((target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
          AND coalesce(monitoring.effective_to, 'infinity'::timestamptz) >
              (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
    ), slots AS (
        SELECT DISTINCT slot_at
        FROM intersections
        CROSS JOIN LATERAL generate_series(
            to_timestamp(ceil(extract(epoch FROM lower_at) / 300) * 300),
            upper_at - interval '1 microsecond', interval '5 minutes'
        ) AS generated(slot_at)
        WHERE lower_at < upper_at
    )
    SELECT count(*)::integer INTO expected_poll_count FROM slots;
    IF expected_poll_count NOT BETWEEN 1 AND 288 THEN
        RAISE EXCEPTION 'account inventory compaction activation is inconsistent'
            USING ERRCODE = 'P1004';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM public.account_inventory_poll_runs AS poll
        WHERE poll.instance_id = target_run.instance_id
          AND poll.provider_policy_version = target_run.provider_policy_version
          AND poll.scheduled_at >=
              (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
          AND poll.scheduled_at <
              ((target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
          AND poll.status NOT IN ('finalized', 'abandoned')
    ) THEN
        RAISE EXCEPTION 'account inventory compaction contains nonterminal source polls'
            USING ERRCODE = 'P1004';
    END IF;
    IF EXISTS (
        WITH intersections AS (
            SELECT greatest(policy.effective_from, monitoring.effective_from,
                            target_run.summary_date::timestamp AT TIME ZONE 'UTC') AS lower_at,
                   least(coalesce(policy.effective_to, 'infinity'::timestamptz),
                         coalesce(monitoring.effective_to, 'infinity'::timestamptz),
                         (target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC') AS upper_at
            FROM public.relay_node_assets AS asset
            JOIN public.provider_inventory_policy_activations AS policy
              ON policy.node_type = asset.node_type
             AND policy.driver_contract_version = asset.driver_contract_version
             AND policy.policy_version_id = target_run.provider_policy_version
            JOIN public.relay_node_inventory_monitoring_activations AS monitoring
              ON monitoring.instance_id = asset.instance_id
             AND policy.active_range && monitoring.active_range
            WHERE asset.instance_id = target_run.instance_id
        ), slots AS (
            SELECT DISTINCT slot_at FROM intersections
            CROSS JOIN LATERAL generate_series(
                to_timestamp(ceil(extract(epoch FROM lower_at) / 300) * 300),
                upper_at - interval '1 microsecond', interval '5 minutes'
            ) AS generated(slot_at) WHERE lower_at < upper_at
        )
        SELECT 1
        FROM public.account_inventory_poll_runs AS poll
        WHERE poll.instance_id = target_run.instance_id
          AND poll.provider_policy_version = target_run.provider_policy_version
          AND poll.scheduled_at >=
              (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
          AND poll.scheduled_at <
              ((target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
          AND NOT EXISTS (SELECT 1 FROM slots WHERE slot_at = poll.scheduled_at)
    ) THEN
        RAISE EXCEPTION 'account inventory compaction source is outside activation truth'
            USING ERRCODE = 'P1004';
    END IF;
    IF EXISTS (
        SELECT 1
        FROM public.account_inventory_poll_runs AS poll
        LEFT JOIN public.account_inventory_snapshot_items AS snapshot
          ON snapshot.poll_run_id = poll.poll_run_id
         AND snapshot.instance_id = poll.instance_id
        WHERE poll.instance_id = target_run.instance_id
          AND poll.status = 'finalized'
          AND (
              poll.scheduled_at::date = target_run.summary_date
              OR poll.observed_at::date = target_run.summary_date
              OR snapshot.observed_at::date = target_run.summary_date
          )
          AND (
              poll.observed_at::date <> poll.scheduled_at::date
              OR (snapshot.poll_run_id IS NOT NULL
                  AND snapshot.observed_at::date <> poll.scheduled_at::date)
          )
    ) THEN
        RAISE EXCEPTION 'account inventory compaction source day mismatch'
            USING ERRCODE = 'P1001';
    END IF;

    SELECT count(*) INTO source_poll_count
    FROM public.account_inventory_poll_runs AS poll
    WHERE poll.instance_id = target_run.instance_id
      AND poll.provider_policy_version = target_run.provider_policy_version
      AND poll.scheduled_at >= (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
      AND poll.scheduled_at < ((target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
      AND poll.status IN ('finalized', 'abandoned');
    SELECT count(*) INTO source_provider_result_count
    FROM public.account_inventory_poll_provider_results AS result
    JOIN public.account_inventory_poll_runs AS poll ON poll.poll_run_id = result.poll_run_id
    WHERE poll.instance_id = target_run.instance_id
      AND poll.provider_policy_version = target_run.provider_policy_version
      AND poll.scheduled_at >= (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
      AND poll.scheduled_at < ((target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
      AND poll.status IN ('finalized', 'abandoned');
    SELECT count(*) INTO source_snapshot_count
    FROM public.account_inventory_snapshot_items AS snapshot
    JOIN public.account_inventory_poll_runs AS poll ON poll.poll_run_id = snapshot.poll_run_id
    WHERE poll.instance_id = target_run.instance_id
      AND poll.provider_policy_version = target_run.provider_policy_version
      AND poll.scheduled_at >= (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
      AND poll.scheduled_at < ((target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
      AND snapshot.observed_at >= (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
      AND snapshot.observed_at < ((target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC');
    SELECT count(*) INTO source_duplicate_count
    FROM public.account_inventory_poll_duplicates AS duplicate_row
    JOIN public.account_inventory_poll_runs AS poll ON poll.poll_run_id = duplicate_row.poll_run_id
    WHERE poll.instance_id = target_run.instance_id
      AND poll.provider_policy_version = target_run.provider_policy_version
      AND poll.scheduled_at >= (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
      AND poll.scheduled_at < ((target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
      AND duplicate_row.observed_at >= (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
      AND duplicate_row.observed_at < ((target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC');
    source_rows := source_poll_count + source_provider_result_count
                   + source_snapshot_count + source_duplicate_count;

    FOR source_record IN
        SELECT source.canonical_row
        FROM (
            SELECT 1 AS kind_order, poll.scheduled_at AS order_scheduled_at,
                   poll.poll_run_id AS order_poll_run_id,
                   NULL::uuid AS order_instance_id, ''::text AS order_key,
                   public.control_history_canonical_row_v1(VARIADIC ARRAY[
                       convert_to('poll','UTF8'),
                       convert_to(public.control_history_time_text_v1(poll.scheduled_at),'UTF8'),
                       convert_to(poll.poll_run_id::text,'UTF8'),
                       convert_to(poll.status,'UTF8'),
                       CASE WHEN poll.observed_at IS NULL THEN NULL::bytea ELSE
                           convert_to(public.control_history_time_text_v1(poll.observed_at),'UTF8') END,
                       CASE WHEN poll.transport_success IS NULL THEN NULL::bytea ELSE
                           convert_to(CASE WHEN poll.transport_success THEN 't' ELSE 'f' END,'UTF8') END,
                       CASE WHEN poll.contract_valid IS NULL THEN NULL::bytea ELSE
                           convert_to(CASE WHEN poll.contract_valid THEN 't' ELSE 'f' END,'UTF8') END,
                       CASE WHEN poll.snapshot_complete IS NULL THEN NULL::bytea ELSE
                           convert_to(CASE WHEN poll.snapshot_complete THEN 't' ELSE 'f' END,'UTF8') END,
                       CASE WHEN poll.degraded IS NULL THEN NULL::bytea ELSE
                           convert_to(CASE WHEN poll.degraded THEN 't' ELSE 'f' END,'UTF8') END
                   ]::bytea[]) AS canonical_row
            FROM public.account_inventory_poll_runs AS poll
            WHERE poll.instance_id = target_run.instance_id
              AND poll.provider_policy_version = target_run.provider_policy_version
              AND poll.scheduled_at >= (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
              AND poll.scheduled_at < ((target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
              AND poll.status IN ('finalized', 'abandoned')
            UNION ALL
            SELECT 2, poll.scheduled_at, result.poll_run_id, NULL::uuid, result.provider,
                   public.control_history_canonical_row_v1(VARIADIC ARRAY[
                       convert_to('provider_result','UTF8'),
                       convert_to(public.control_history_time_text_v1(poll.scheduled_at),'UTF8'),
                       convert_to(result.poll_run_id::text,'UTF8'),
                       convert_to(result.provider,'UTF8'),
                       convert_to(CASE WHEN result.snapshot_complete THEN 't' ELSE 'f' END,'UTF8'),
                       convert_to(CASE WHEN result.promotion_applied THEN 't' ELSE 'f' END,'UTF8'),
                       CASE WHEN result.promotion_skipped_reason IS NULL THEN NULL::bytea ELSE
                           convert_to(result.promotion_skipped_reason,'UTF8') END,
                       convert_to(CASE WHEN result.degraded THEN 't' ELSE 'f' END,'UTF8'),
                       convert_to(result.reason,'UTF8')
                   ]::bytea[])
            FROM public.account_inventory_poll_provider_results AS result
            JOIN public.account_inventory_poll_runs AS poll ON poll.poll_run_id = result.poll_run_id
            WHERE poll.instance_id = target_run.instance_id
              AND poll.provider_policy_version = target_run.provider_policy_version
              AND poll.scheduled_at >= (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
              AND poll.scheduled_at < ((target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
              AND poll.status IN ('finalized', 'abandoned')
            UNION ALL
            SELECT 3, poll.scheduled_at, snapshot.poll_run_id, snapshot.instance_id,
                   snapshot.account_key,
                   public.control_history_canonical_row_v1(VARIADIC ARRAY[
                       convert_to('snapshot','UTF8'),
                       convert_to(public.control_history_time_text_v1(poll.scheduled_at),'UTF8'),
                       convert_to(snapshot.poll_run_id::text,'UTF8'),
                       convert_to(snapshot.instance_id::text,'UTF8'),
                       convert_to(snapshot.provider,'UTF8'),
                       convert_to(snapshot.account_key,'UTF8'),
                       convert_to(public.control_history_time_text_v1(snapshot.observed_at),'UTF8'),
                       convert_to(snapshot.basic_status,'UTF8'),
                       convert_to(snapshot.success_count::text,'UTF8'),
                       convert_to(snapshot.failed_count::text,'UTF8')
                   ]::bytea[])
            FROM public.account_inventory_snapshot_items AS snapshot
            JOIN public.account_inventory_poll_runs AS poll ON poll.poll_run_id = snapshot.poll_run_id
            WHERE poll.instance_id = target_run.instance_id
              AND poll.provider_policy_version = target_run.provider_policy_version
              AND poll.scheduled_at >= (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
              AND poll.scheduled_at < ((target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
              AND snapshot.observed_at >= (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
              AND snapshot.observed_at < ((target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
            UNION ALL
            SELECT 4, poll.scheduled_at, duplicate_row.poll_run_id,
                   duplicate_row.instance_id, duplicate_row.account_key,
                   public.control_history_canonical_row_v1(VARIADIC ARRAY[
                       convert_to('duplicate','UTF8'),
                       convert_to(public.control_history_time_text_v1(poll.scheduled_at),'UTF8'),
                       convert_to(duplicate_row.poll_run_id::text,'UTF8'),
                       convert_to(duplicate_row.instance_id::text,'UTF8'),
                       convert_to(duplicate_row.provider,'UTF8'),
                       convert_to(duplicate_row.account_key,'UTF8'),
                       convert_to(public.control_history_time_text_v1(duplicate_row.observed_at),'UTF8'),
                       convert_to(duplicate_row.occurrence_count::text,'UTF8')
                   ]::bytea[])
            FROM public.account_inventory_poll_duplicates AS duplicate_row
            JOIN public.account_inventory_poll_runs AS poll ON poll.poll_run_id = duplicate_row.poll_run_id
            WHERE poll.instance_id = target_run.instance_id
              AND poll.provider_policy_version = target_run.provider_policy_version
              AND poll.scheduled_at >= (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
              AND poll.scheduled_at < ((target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
              AND duplicate_row.observed_at >= (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
              AND duplicate_row.observed_at < ((target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
        ) AS source
        ORDER BY source.kind_order, source.order_scheduled_at,
                 source.order_poll_run_id, source.order_instance_id, source.order_key
    LOOP
        source_checksum := sha256(source_checksum || sha256(source_record.canonical_row));
    END LOOP;

    WITH samples AS (
        SELECT snapshot.*, poll.scheduled_at,
               lag(snapshot.success_count) OVER (
                   PARTITION BY snapshot.account_key
                   ORDER BY poll.scheduled_at, snapshot.observed_at, snapshot.poll_run_id
               ) AS previous_success_count,
               lag(snapshot.failed_count) OVER (
                   PARTITION BY snapshot.account_key
                   ORDER BY poll.scheduled_at, snapshot.observed_at, snapshot.poll_run_id
               ) AS previous_failed_count
        FROM public.account_inventory_snapshot_items AS snapshot
        JOIN public.account_inventory_poll_runs AS poll ON poll.poll_run_id = snapshot.poll_run_id
        WHERE poll.instance_id = target_run.instance_id
          AND poll.provider_policy_version = target_run.provider_policy_version
          AND poll.scheduled_at >= (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
          AND poll.scheduled_at < ((target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
          AND snapshot.observed_at >= (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
          AND snapshot.observed_at < ((target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
    ), inserted AS (
        INSERT INTO public.account_inventory_daily_summaries (
            compaction_run_id, summary_date, instance_id, provider, account_key,
            provider_policy_version, first_scheduled_at, last_scheduled_at,
            first_observed_at, last_observed_at, last_basic_status, sample_count,
            disabled_count, unavailable_count, error_count, active_count, unknown_count,
            first_success_count, last_success_count, success_reset_count,
            first_failed_count, last_failed_count, failed_reset_count, created_at
        )
        SELECT target_run_id, target_run.summary_date, target_run.instance_id,
               provider, account_key, target_run.provider_policy_version,
               min(scheduled_at), max(scheduled_at), min(observed_at), max(observed_at),
               (array_agg(basic_status ORDER BY scheduled_at DESC,
                          observed_at DESC, poll_run_id DESC))[1],
               count(*)::integer,
               count(*) FILTER (WHERE basic_status = 'disabled')::integer,
               count(*) FILTER (WHERE basic_status = 'unavailable')::integer,
               count(*) FILTER (WHERE basic_status = 'error')::integer,
               count(*) FILTER (WHERE basic_status = 'active')::integer,
               count(*) FILTER (WHERE basic_status = 'unknown')::integer,
               (array_agg(success_count ORDER BY scheduled_at, observed_at, poll_run_id))[1],
               (array_agg(success_count ORDER BY scheduled_at DESC,
                          observed_at DESC, poll_run_id DESC))[1],
               count(*) FILTER (WHERE previous_success_count IS NOT NULL
                                  AND success_count < previous_success_count)::integer,
               (array_agg(failed_count ORDER BY scheduled_at, observed_at, poll_run_id))[1],
               (array_agg(failed_count ORDER BY scheduled_at DESC,
                          observed_at DESC, poll_run_id DESC))[1],
               count(*) FILTER (WHERE previous_failed_count IS NOT NULL
                                  AND failed_count < previous_failed_count)::integer,
               database_now
        FROM samples GROUP BY provider, account_key
        RETURNING 1
    ) SELECT count(*) INTO account_segment_count FROM inserted;

    WITH active_providers AS (
        SELECT provider
        FROM public.provider_inventory_policy_versions AS policy,
             LATERAL unnest(policy.active_providers) AS active(provider)
        WHERE policy.policy_version_id = target_run.provider_policy_version
    ), terminal_polls AS (
        SELECT poll.*
        FROM public.account_inventory_poll_runs AS poll
        WHERE poll.instance_id = target_run.instance_id
          AND poll.provider_policy_version = target_run.provider_policy_version
          AND poll.scheduled_at >= (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
          AND poll.scheduled_at < ((target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
          AND poll.status IN ('finalized', 'abandoned')
    ), aggregated AS (
        SELECT active.provider,
               count(*) FILTER (WHERE poll.status = 'finalized'
                                  AND poll.transport_success)::integer AS transport_success_count,
               count(*) FILTER (WHERE poll.status = 'finalized'
                                  AND poll.contract_valid)::integer AS contract_valid_count,
               count(*) FILTER (WHERE result.snapshot_complete)::integer AS snapshot_complete_count,
               count(*) FILTER (WHERE result.promotion_applied)::integer AS promotion_applied_count,
               count(*) FILTER (WHERE result.promotion_skipped_reason IS NOT NULL)::integer
                   AS promotion_skipped_count,
               count(*) FILTER (WHERE result.promotion_skipped_reason = 'policy_changed')::integer
                   AS policy_changed_count,
               count(*) FILTER (WHERE poll.status = 'abandoned')::integer AS abandoned_count,
               count(*) FILTER (WHERE result.degraded)::integer AS degraded_count,
               min(poll.scheduled_at) FILTER (WHERE result.promotion_applied) AS first_promotion_at,
               max(poll.scheduled_at) FILTER (WHERE result.promotion_applied) AS last_promotion_at
        FROM active_providers AS active
        LEFT JOIN terminal_polls AS poll ON true
        LEFT JOIN public.account_inventory_poll_provider_results AS result
          ON result.poll_run_id = poll.poll_run_id AND result.provider = active.provider
        GROUP BY active.provider
    ), inserted AS (
        INSERT INTO public.account_inventory_daily_provider_summaries (
            compaction_run_id, summary_date, instance_id, provider,
            provider_policy_version, expected_poll_count, transport_success_count,
            contract_valid_count, snapshot_complete_count, promotion_applied_count,
            promotion_skipped_count, policy_changed_count, abandoned_count,
            degraded_count, first_promotion_at, last_promotion_at,
            coverage_numerator, coverage_denominator, coverage_ratio,
            coverage_threshold_basis_points, coverage_status, created_at
        )
        SELECT target_run_id, target_run.summary_date, target_run.instance_id,
               provider, target_run.provider_policy_version, expected_poll_count,
               transport_success_count, contract_valid_count, snapshot_complete_count,
               promotion_applied_count, promotion_skipped_count, policy_changed_count,
               abandoned_count, degraded_count, first_promotion_at, last_promotion_at,
               promotion_applied_count, expected_poll_count,
               round(promotion_applied_count::numeric / expected_poll_count, 8), 9500,
               CASE WHEN promotion_applied_count * 10000::bigint
                              >= expected_poll_count * 9500::bigint
                    THEN 'complete' ELSE 'partial' END,
               database_now
        FROM aggregated
        RETURNING 1
    ) SELECT count(*) INTO provider_segment_count FROM inserted;

    PERFORM set_config('relay_control.history_run_write',
                       'account_inventory_compaction_runs', true);
    UPDATE public.account_inventory_compaction_runs AS run
    SET status = 'summarized', checksum_version = 1,
        source_snapshot_count = source_snapshot_count,
        source_poll_count = source_poll_count,
        source_provider_result_count = source_provider_result_count,
        source_duplicate_count = source_duplicate_count,
        source_checksum = source_checksum, summarized_at = database_now,
        updated_at = database_now
    WHERE run.compaction_run_id = target_run_id
      AND run.status = 'pending'
      AND run.fencing_token = target_fencing_token
      AND run.lease_expires_at > database_now;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'account inventory compaction lease is lost'
            USING ERRCODE = 'P0002';
    END IF;
    PERFORM set_config('relay_control.history_run_write', '', true);
    PERFORM set_config(
        'relay_control.history_audit_write',
        'account_inventory_history.summarized:summarize', true);
    INSERT INTO public.audit_logs (
        occurred_at, category, action, result, request_id, details
    ) VALUES (
        database_now, 'account_inventory_history',
        'account_inventory_history.summarized', 'success',
        'history-compaction-system',
        jsonb_build_object(
            'instance', target_run.instance_id::text,
            'summary_date', target_run.summary_date::text,
            'phase', 'summarize', 'row_count', source_rows
        )
    );
    PERFORM set_config('relay_control.history_audit_write', '', true);
    RETURN jsonb_build_object(
        'status', 'summarized', 'source_rows', source_rows,
        'source_snapshot_count', source_snapshot_count,
        'source_checksum_hex', encode(source_checksum, 'hex'),
        'account_segment_count', account_segment_count,
        'provider_segment_count', provider_segment_count
    );
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_delete_account_inventory_snapshot_batch_v1(
    target_run_id uuid, target_fencing_token uuid, delete_limit integer
) RETURNS jsonb
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
SET TimeZone = 'UTC'
AS $$
DECLARE
    database_now timestamptz := clock_timestamp();
    target_run public.account_inventory_compaction_runs%ROWTYPE;
    deleted_count bigint := 0;
    total_deleted_count bigint;
    remaining_count bigint;
BEGIN
    IF target_run_id IS NULL OR target_fencing_token IS NULL
       OR delete_limit IS NULL OR delete_limit NOT BETWEEN 1 AND 5000 THEN
        RAISE EXCEPTION 'invalid account inventory snapshot delete batch'
            USING ERRCODE = '22023';
    END IF;
    SELECT run.* INTO target_run
    FROM public.account_inventory_compaction_runs AS run
    WHERE run.compaction_run_id = target_run_id
    FOR UPDATE;
    database_now := clock_timestamp();
    IF NOT FOUND OR target_run.fencing_token IS DISTINCT FROM target_fencing_token
       OR target_run.lease_expires_at IS NULL
       OR target_run.lease_expires_at <= database_now
       OR target_run.status NOT IN ('summarized', 'deleting') THEN
        RAISE EXCEPTION 'account inventory compaction lease is lost'
            USING ERRCODE = 'P0002';
    END IF;
    IF target_run.status = 'summarized' THEN
        PERFORM set_config('relay_control.history_run_write',
                           'account_inventory_compaction_runs', true);
        UPDATE public.account_inventory_compaction_runs AS run
        SET status = 'deleting', deleting_at = database_now, updated_at = database_now
        WHERE run.compaction_run_id = target_run_id
          AND run.status = 'summarized'
          AND run.fencing_token = target_fencing_token
          AND run.lease_expires_at > database_now;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'account inventory compaction lease is lost'
                USING ERRCODE = 'P0002';
        END IF;
        PERFORM set_config('relay_control.history_run_write', '', true);
    END IF;

    PERFORM set_config('relay_control.history_snapshot_delete', target_run_id::text, true);
    WITH selected AS (
        SELECT snapshot.poll_run_id, snapshot.instance_id, snapshot.account_key
        FROM public.account_inventory_snapshot_items AS snapshot
        JOIN public.account_inventory_poll_runs AS poll
          ON poll.poll_run_id = snapshot.poll_run_id
         AND poll.instance_id = snapshot.instance_id
        WHERE poll.instance_id = target_run.instance_id
          AND poll.provider_policy_version = target_run.provider_policy_version
          AND poll.scheduled_at >=
              (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
          AND poll.scheduled_at <
              ((target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
          AND snapshot.observed_at >=
              (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
          AND snapshot.observed_at <
              ((target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
        ORDER BY snapshot.poll_run_id, snapshot.instance_id, snapshot.account_key
        FOR UPDATE OF snapshot SKIP LOCKED
        LIMIT delete_limit
    ), deleted AS (
        DELETE FROM public.account_inventory_snapshot_items AS snapshot
        USING selected
        WHERE snapshot.poll_run_id = selected.poll_run_id
          AND snapshot.instance_id = selected.instance_id
          AND snapshot.account_key = selected.account_key
        RETURNING 1
    )
    SELECT count(*) INTO deleted_count FROM deleted;
    PERFORM set_config('relay_control.history_snapshot_delete', '', true);

    SELECT count(*) INTO remaining_count
    FROM public.account_inventory_snapshot_items AS snapshot
    JOIN public.account_inventory_poll_runs AS poll
      ON poll.poll_run_id = snapshot.poll_run_id
     AND poll.instance_id = snapshot.instance_id
    WHERE poll.instance_id = target_run.instance_id
      AND poll.provider_policy_version = target_run.provider_policy_version
      AND poll.scheduled_at >=
          (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
      AND poll.scheduled_at <
          ((target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
      AND snapshot.observed_at >=
          (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
      AND snapshot.observed_at <
          ((target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC');

    PERFORM set_config('relay_control.history_run_write',
                       'account_inventory_compaction_runs', true);
    UPDATE public.account_inventory_compaction_runs AS run
    SET deleted_snapshot_count = run.deleted_snapshot_count + deleted_count,
        updated_at = database_now
    WHERE run.compaction_run_id = target_run_id
      AND run.status = 'deleting'
      AND run.fencing_token = target_fencing_token
      AND run.lease_expires_at > database_now
      AND run.deleted_snapshot_count + deleted_count <= run.source_snapshot_count
    RETURNING run.deleted_snapshot_count INTO total_deleted_count;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'account inventory compaction source count mismatch'
            USING ERRCODE = 'P1002';
    END IF;
    PERFORM set_config('relay_control.history_run_write', '', true);
    PERFORM set_config(
        'relay_control.history_audit_write',
        'account_inventory_history.snapshot_delete_batch:snapshot_delete', true);
    INSERT INTO public.audit_logs (
        occurred_at, category, action, result, request_id, details
    ) VALUES (
        database_now, 'account_inventory_history',
        'account_inventory_history.snapshot_delete_batch', 'success',
        'history-compaction-system',
        jsonb_build_object(
            'instance', target_run.instance_id::text,
            'summary_date', target_run.summary_date::text,
            'phase', 'snapshot_delete', 'row_count', deleted_count
        )
    );
    PERFORM set_config('relay_control.history_audit_write', '', true);
    RETURN jsonb_build_object(
        'status', 'deleting', 'deleted_count', deleted_count,
        'total_deleted_count', total_deleted_count,
        'remaining_count', remaining_count
    );
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_complete_account_inventory_compaction_v1(
    target_run_id uuid, target_fencing_token uuid, expected_checksum bytea
) RETURNS jsonb
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
SET TimeZone = 'UTC'
AS $$
DECLARE
    database_now timestamptz := clock_timestamp();
    target_run public.account_inventory_compaction_runs%ROWTYPE;
    remaining_count bigint;
    mismatch_reason text;
BEGIN
    IF target_run_id IS NULL OR target_fencing_token IS NULL
       OR expected_checksum IS NULL OR octet_length(expected_checksum) <> 32 THEN
        RAISE EXCEPTION 'invalid account inventory compaction completion'
            USING ERRCODE = '22023';
    END IF;
    SELECT run.* INTO target_run
    FROM public.account_inventory_compaction_runs AS run
    WHERE run.compaction_run_id = target_run_id
    FOR UPDATE;
    database_now := clock_timestamp();
    IF NOT FOUND THEN
        RAISE EXCEPTION 'account inventory compaction does not exist'
            USING ERRCODE = 'P0002';
    END IF;
    IF target_run.status = 'completed' THEN
        IF NOT public.control_history_bytea_equal_v1(
            target_run.source_checksum, expected_checksum) THEN
            RAISE EXCEPTION 'account inventory compaction checksum mismatch'
                USING ERRCODE = 'P1003';
        END IF;
        RETURN jsonb_build_object(
            'status', 'completed',
            'source_checksum_hex', encode(target_run.source_checksum, 'hex'),
            'idempotent', true, 'failure_reason', NULL
        );
    END IF;
    IF target_run.status <> 'deleting'
       OR target_run.fencing_token IS DISTINCT FROM target_fencing_token
       OR target_run.lease_expires_at IS NULL
       OR target_run.lease_expires_at <= database_now THEN
        RAISE EXCEPTION 'account inventory compaction lease is lost'
            USING ERRCODE = 'P0002';
    END IF;
    IF NOT public.control_history_bytea_equal_v1(
        target_run.source_checksum, expected_checksum) THEN
        mismatch_reason := 'source_checksum_mismatch';
    END IF;
    SELECT count(*) INTO remaining_count
    FROM public.account_inventory_snapshot_items AS snapshot
    JOIN public.account_inventory_poll_runs AS poll
      ON poll.poll_run_id = snapshot.poll_run_id
     AND poll.instance_id = snapshot.instance_id
    WHERE poll.instance_id = target_run.instance_id
      AND poll.provider_policy_version = target_run.provider_policy_version
      AND poll.scheduled_at >=
          (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
      AND poll.scheduled_at <
          ((target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
      AND snapshot.observed_at >=
          (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
      AND snapshot.observed_at <
          ((target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC');
    IF mismatch_reason IS NULL AND (
        remaining_count <> 0
        OR target_run.deleted_snapshot_count <> target_run.source_snapshot_count
    ) THEN
        mismatch_reason := 'source_count_mismatch';
    END IF;
    IF mismatch_reason IS NOT NULL THEN
        PERFORM set_config('relay_control.history_run_write',
                           'account_inventory_compaction_runs', true);
        UPDATE public.account_inventory_compaction_runs AS run
        SET status = 'failed', failed_from = 'deleting',
            failure_reason = mismatch_reason, failed_at = database_now,
            claim_owner = NULL, lease_expires_at = NULL, fencing_token = NULL,
            updated_at = database_now
        WHERE run.compaction_run_id = target_run_id
          AND run.status = 'deleting'
          AND run.fencing_token = target_fencing_token
          AND run.lease_expires_at > database_now;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'account inventory compaction lease is lost'
                USING ERRCODE = 'P0002';
        END IF;
        PERFORM set_config('relay_control.history_run_write', '', true);
        PERFORM set_config(
            'relay_control.history_audit_write',
            'account_inventory_history.failed:fail_deleting', true);
        INSERT INTO public.audit_logs (
            occurred_at, category, action, result, request_id, details
        ) VALUES (
            database_now, 'account_inventory_history',
            'account_inventory_history.failed', 'failure',
            'history-compaction-system',
            jsonb_build_object(
                'instance', target_run.instance_id::text,
                'summary_date', target_run.summary_date::text,
                'phase', 'fail_deleting', 'row_count', 0
            )
        );
        PERFORM set_config('relay_control.history_audit_write', '', true);
        RETURN jsonb_build_object(
            'status', 'failed',
            'source_checksum_hex', encode(target_run.source_checksum, 'hex'),
            'idempotent', false, 'failure_reason', mismatch_reason
        );
    END IF;

    PERFORM set_config('relay_control.history_run_write',
                       'account_inventory_compaction_runs', true);
    UPDATE public.account_inventory_compaction_runs AS run
    SET status = 'completed', completed_at = database_now,
        claim_owner = NULL, lease_expires_at = NULL, fencing_token = NULL,
        updated_at = database_now
    WHERE run.compaction_run_id = target_run_id
      AND run.status = 'deleting'
      AND run.fencing_token = target_fencing_token
      AND run.lease_expires_at > database_now
      AND run.deleted_snapshot_count = run.source_snapshot_count;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'account inventory compaction lease is lost'
            USING ERRCODE = 'P0002';
    END IF;
    PERFORM set_config('relay_control.history_run_write', '', true);
    PERFORM set_config(
        'relay_control.history_audit_write',
        'account_inventory_history.completed:complete', true);
    INSERT INTO public.audit_logs (
        occurred_at, category, action, result, request_id, details
    ) VALUES (
        database_now, 'account_inventory_history',
        'account_inventory_history.completed', 'success',
        'history-compaction-system',
        jsonb_build_object(
            'instance', target_run.instance_id::text,
            'summary_date', target_run.summary_date::text,
            'phase', 'complete', 'row_count', target_run.source_snapshot_count
        )
    );
    PERFORM set_config('relay_control.history_audit_write', '', true);
    RETURN jsonb_build_object(
        'status', 'completed',
        'source_checksum_hex', encode(target_run.source_checksum, 'hex'),
        'idempotent', false, 'failure_reason', NULL
    );
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_claim_account_inventory_daily_rollup_v1(
    worker_token uuid, lease_seconds integer
) RETURNS SETOF public.account_inventory_daily_rollup_runs
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
SET TimeZone = 'UTC'
AS $$
DECLARE database_now timestamptz := clock_timestamp();
BEGIN
    IF worker_token IS NULL OR lease_seconds NOT BETWEEN 5 AND 300 THEN
        RAISE EXCEPTION 'invalid account inventory daily rollup claim'
            USING ERRCODE = '22023';
    END IF;
    PERFORM set_config('relay_control.history_run_write',
                       'account_inventory_daily_rollup_runs', true);
    RETURN QUERY
    WITH candidate AS (
        SELECT run.rollup_run_id
        FROM public.account_inventory_daily_rollup_runs AS run
        WHERE run.status IN ('pending','failed')
          AND (run.status <> 'failed' OR run.failure_reason IN (
              'segment_incomplete', 'statement_timeout',
              'lease_expired', 'database_unavailable'
          ))
          AND (run.lease_expires_at IS NULL OR run.lease_expires_at <= database_now)
          AND ((run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
                <= database_now - interval '72 hours'
        ORDER BY run.summary_date, run.instance_id
        FOR UPDATE SKIP LOCKED LIMIT 1
    )
    UPDATE public.account_inventory_daily_rollup_runs AS run
    SET status = 'pending', failure_reason = NULL, failed_at = NULL,
        claim_owner = worker_token::text,
        lease_expires_at = database_now + make_interval(secs => lease_seconds),
        fencing_token = gen_random_uuid(), attempt_count = run.attempt_count + 1,
        updated_at = database_now
    FROM candidate
    WHERE run.rollup_run_id = candidate.rollup_run_id
    RETURNING run.*;
    PERFORM set_config('relay_control.history_run_write', '', true);
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_renew_account_inventory_daily_rollup_v1(
    target_run_id uuid, target_fencing_token uuid, lease_seconds integer
) RETURNS SETOF public.account_inventory_daily_rollup_runs
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
SET TimeZone = 'UTC'
AS $$
DECLARE database_now timestamptz := clock_timestamp();
BEGIN
    IF target_run_id IS NULL OR target_fencing_token IS NULL
       OR lease_seconds NOT BETWEEN 5 AND 300 THEN
        RAISE EXCEPTION 'invalid account inventory daily rollup renewal'
            USING ERRCODE = '22023';
    END IF;
    PERFORM run.rollup_run_id
    FROM public.account_inventory_daily_rollup_runs AS run
    WHERE run.rollup_run_id = target_run_id
    FOR UPDATE;
    database_now := clock_timestamp();
    PERFORM set_config('relay_control.history_run_write',
                       'account_inventory_daily_rollup_runs', true);
    RETURN QUERY
    UPDATE public.account_inventory_daily_rollup_runs AS run
    SET lease_expires_at = database_now + make_interval(secs => lease_seconds),
        updated_at = database_now
    WHERE run.rollup_run_id = target_run_id
      AND run.status = 'pending'
      AND run.fencing_token = target_fencing_token
      AND run.lease_expires_at > database_now
    RETURNING run.*;
    PERFORM set_config('relay_control.history_run_write', '', true);
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_reconcile_account_inventory_daily_rollups_v1(
    reconcile_limit integer
) RETURNS jsonb
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
SET TimeZone = 'UTC'
AS $$
DECLARE
    database_now timestamptz := clock_timestamp();
    failed_count bigint := 0;
    expired_run record;
BEGIN
    IF reconcile_limit IS NULL OR reconcile_limit NOT BETWEEN 1 AND 1000 THEN
        RAISE EXCEPTION 'invalid account inventory daily rollup reconcile limit'
            USING ERRCODE = '22023';
    END IF;
    FOR expired_run IN
        SELECT run.rollup_run_id, run.instance_id, run.summary_date
        FROM public.account_inventory_daily_rollup_runs AS run
        WHERE run.status = 'pending' AND run.lease_expires_at <= database_now
        ORDER BY run.lease_expires_at, run.rollup_run_id
        FOR UPDATE SKIP LOCKED LIMIT reconcile_limit
    LOOP
        PERFORM set_config('relay_control.history_run_write',
                           'account_inventory_daily_rollup_runs', true);
        UPDATE public.account_inventory_daily_rollup_runs AS run
        SET status = 'failed', failure_reason = 'lease_expired',
            failed_at = database_now, claim_owner = NULL,
            lease_expires_at = NULL, fencing_token = NULL,
            updated_at = database_now
        WHERE run.rollup_run_id = expired_run.rollup_run_id
          AND run.status = 'pending' AND run.lease_expires_at <= database_now;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'account inventory daily rollup reconcile lost its lock'
                USING ERRCODE = 'P0002';
        END IF;
        PERFORM set_config('relay_control.history_run_write', '', true);
        PERFORM set_config(
            'relay_control.history_audit_write',
            'account_inventory_history.failed:rollup_fail_pending', true);
        INSERT INTO public.audit_logs (
            occurred_at, category, action, result, request_id, details
        ) VALUES (
            database_now, 'account_inventory_history',
            'account_inventory_history.failed', 'failure',
            'history-compaction-system',
            jsonb_build_object(
                'instance', expired_run.instance_id::text,
                'summary_date', expired_run.summary_date::text,
                'phase', 'rollup_fail_pending', 'row_count', 0
            )
        );
        PERFORM set_config('relay_control.history_audit_write', '', true);
        failed_count := failed_count + 1;
    END LOOP;
    RETURN jsonb_build_object('failed_count', failed_count);
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_fail_account_inventory_daily_rollup_v1(
    target_run_id uuid, target_fencing_token uuid, fixed_reason text
) RETURNS jsonb
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
SET TimeZone = 'UTC'
AS $$
DECLARE
    database_now timestamptz := clock_timestamp();
    failed_run record;
BEGIN
    IF target_run_id IS NULL OR target_fencing_token IS NULL
       OR fixed_reason IS NULL OR fixed_reason NOT IN (
           'segment_incomplete', 'segment_count_mismatch',
           'segment_checksum_mismatch', 'activation_inconsistent',
           'statement_timeout', 'lease_expired', 'database_unavailable', 'internal'
       ) THEN
        RAISE EXCEPTION 'invalid account inventory daily rollup failure'
            USING ERRCODE = '22023';
    END IF;
    PERFORM run.rollup_run_id
    FROM public.account_inventory_daily_rollup_runs AS run
    WHERE run.rollup_run_id = target_run_id
    FOR UPDATE;
    database_now := clock_timestamp();
    PERFORM set_config('relay_control.history_run_write',
                       'account_inventory_daily_rollup_runs', true);
    UPDATE public.account_inventory_daily_rollup_runs AS run
    SET status = 'failed', failure_reason = fixed_reason,
        failed_at = database_now, claim_owner = NULL,
        lease_expires_at = NULL, fencing_token = NULL,
        updated_at = database_now
    WHERE run.rollup_run_id = target_run_id
      AND run.status = 'pending'
      AND run.fencing_token = target_fencing_token
      AND run.lease_expires_at > database_now
    RETURNING run.instance_id, run.summary_date INTO failed_run;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'account inventory daily rollup lease is lost'
            USING ERRCODE = 'P0002';
    END IF;
    PERFORM set_config('relay_control.history_run_write', '', true);
    PERFORM set_config(
        'relay_control.history_audit_write',
        'account_inventory_history.failed:rollup_fail_pending', true);
    INSERT INTO public.audit_logs (
        occurred_at, category, action, result, request_id, details
    ) VALUES (
        database_now, 'account_inventory_history',
        'account_inventory_history.failed', 'failure',
        'history-compaction-system',
        jsonb_build_object(
            'instance', failed_run.instance_id::text,
            'summary_date', failed_run.summary_date::text,
            'phase', 'rollup_fail_pending', 'row_count', 0
        )
    );
    PERFORM set_config('relay_control.history_audit_write', '', true);
    RETURN jsonb_build_object('status','failed','failure_reason',fixed_reason);
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_finalize_account_inventory_daily_rollup_v1(
    target_run_id uuid, target_fencing_token uuid
) RETURNS jsonb
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
SET TimeZone = 'UTC'
AS $$
#variable_conflict use_variable
DECLARE
    database_now timestamptz := clock_timestamp();
    target_run public.account_inventory_daily_rollup_runs%ROWTYPE;
    expected_policies uuid[];
    expected_count integer := 0;
    completed_count integer := 0;
    provider_shape_invalid boolean := false;
    fixed_reason text;
    segment_checksum bytea := decode(repeat('00', 32), 'hex');
    segment_record record;
    account_rollup_count bigint := 0;
    provider_rollup_count bigint := 0;
BEGIN
    IF target_run_id IS NULL OR target_fencing_token IS NULL THEN
        RAISE EXCEPTION 'invalid account inventory daily rollup finalize request'
            USING ERRCODE = '22023';
    END IF;
    SELECT run.* INTO target_run
    FROM public.account_inventory_daily_rollup_runs AS run
    WHERE run.rollup_run_id = target_run_id
    FOR UPDATE;
    database_now := clock_timestamp();
    IF NOT FOUND THEN
        RAISE EXCEPTION 'account inventory daily rollup does not exist'
            USING ERRCODE = 'P0002';
    END IF;
    IF target_run.status = 'completed' THEN
        IF target_run.completed_fencing_token IS DISTINCT FROM target_fencing_token THEN
            RAISE EXCEPTION 'account inventory daily rollup fence is stale'
                USING ERRCODE = 'P0002';
        END IF;
    ELSIF target_run.status <> 'pending'
       OR target_run.fencing_token IS DISTINCT FROM target_fencing_token
       OR target_run.lease_expires_at IS NULL
       OR target_run.lease_expires_at <= database_now THEN
        RAISE EXCEPTION 'account inventory daily rollup lease is lost'
            USING ERRCODE = 'P0002';
    END IF;
    IF ((target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
         > database_now - interval '72 hours' THEN
        IF target_run.status = 'completed' THEN
            RAISE EXCEPTION 'account inventory daily rollup activation changed'
                USING ERRCODE = 'P1104';
        END IF;
        fixed_reason := 'activation_inconsistent';
    END IF;

    WITH intersections AS (
        SELECT policy.policy_version_id,
               greatest(policy.effective_from, monitoring.effective_from,
                        target_run.summary_date::timestamp AT TIME ZONE 'UTC') AS lower_at,
               least(coalesce(policy.effective_to, 'infinity'::timestamptz),
                     coalesce(monitoring.effective_to, 'infinity'::timestamptz),
                     (target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC') AS upper_at
        FROM public.relay_node_assets AS asset
        JOIN public.provider_inventory_policy_activations AS policy
          ON policy.node_type = asset.node_type
         AND policy.driver_contract_version = asset.driver_contract_version
        JOIN public.relay_node_inventory_monitoring_activations AS monitoring
          ON monitoring.instance_id = asset.instance_id
         AND policy.active_range && monitoring.active_range
        WHERE asset.instance_id = target_run.instance_id
          AND policy.effective_from <
              ((target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
          AND coalesce(policy.effective_to, 'infinity'::timestamptz) >
              (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
          AND monitoring.effective_from <
              ((target_run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
          AND coalesce(monitoring.effective_to, 'infinity'::timestamptz) >
              (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
    ), expected AS (
        SELECT DISTINCT policy_version_id
        FROM intersections
        WHERE lower_at < upper_at
          AND to_timestamp(ceil(extract(epoch FROM lower_at) / 300) * 300) < upper_at
    )
    SELECT count(*)::integer, array_agg(policy_version_id ORDER BY policy_version_id)
    INTO expected_count, expected_policies FROM expected;

    IF expected_count = 0 THEN
        IF target_run.status = 'completed' THEN
            RAISE EXCEPTION 'account inventory daily rollup activation changed'
                USING ERRCODE = 'P1104';
        END IF;
        fixed_reason := 'activation_inconsistent';
    ELSE
        SELECT count(*)::integer INTO completed_count
        FROM public.account_inventory_compaction_runs AS compaction
        WHERE compaction.summary_date = target_run.summary_date
          AND compaction.instance_id = target_run.instance_id
          AND compaction.provider_policy_version = ANY(expected_policies)
          AND compaction.status = 'completed';
        IF completed_count <> expected_count OR EXISTS (
            SELECT 1 FROM unnest(expected_policies) AS expected(policy_version_id)
            LEFT JOIN public.account_inventory_compaction_runs AS compaction
              ON compaction.summary_date = target_run.summary_date
             AND compaction.instance_id = target_run.instance_id
             AND compaction.provider_policy_version = expected.policy_version_id
            WHERE compaction.compaction_run_id IS NULL OR compaction.status <> 'completed'
        ) THEN
            IF target_run.status = 'completed' THEN
                RAISE EXCEPTION 'account inventory daily rollup segment count changed'
                    USING ERRCODE = 'P1102';
            END IF;
            fixed_reason := 'segment_incomplete';
        END IF;
    END IF;

    IF fixed_reason IS NULL THEN
        WITH expected_provider AS (
            SELECT policy.policy_version_id, active.provider
            FROM public.provider_inventory_policy_versions AS policy
            CROSS JOIN LATERAL unnest(policy.active_providers) AS active(provider)
            WHERE policy.policy_version_id = ANY(expected_policies)
        ), actual_provider AS (
            SELECT summary.provider_policy_version, summary.provider
            FROM public.account_inventory_daily_provider_summaries AS summary
            JOIN public.account_inventory_compaction_runs AS compaction
              ON compaction.compaction_run_id = summary.compaction_run_id
             AND compaction.summary_date = summary.summary_date
             AND compaction.instance_id = summary.instance_id
             AND compaction.provider_policy_version = summary.provider_policy_version
            WHERE summary.summary_date = target_run.summary_date
              AND summary.instance_id = target_run.instance_id
              AND summary.provider_policy_version = ANY(expected_policies)
              AND compaction.status = 'completed'
        ), differences AS (
            (SELECT * FROM expected_provider EXCEPT SELECT * FROM actual_provider)
            UNION ALL
            (SELECT * FROM actual_provider EXCEPT SELECT * FROM expected_provider)
        )
        SELECT EXISTS(SELECT 1 FROM differences) INTO provider_shape_invalid;
        IF provider_shape_invalid THEN
            IF target_run.status = 'completed' THEN
                RAISE EXCEPTION 'account inventory daily rollup segment shape changed'
                    USING ERRCODE = 'P1102';
            END IF;
            fixed_reason := 'segment_count_mismatch';
        END IF;
    END IF;

    IF fixed_reason IS NOT NULL THEN
        PERFORM set_config('relay_control.history_run_write',
                           'account_inventory_daily_rollup_runs', true);
        UPDATE public.account_inventory_daily_rollup_runs AS run
        SET status = 'failed', failure_reason = fixed_reason,
            failed_at = database_now, claim_owner = NULL,
            lease_expires_at = NULL, fencing_token = NULL,
            updated_at = database_now
        WHERE run.rollup_run_id = target_run_id
          AND run.status = 'pending'
          AND run.fencing_token = target_fencing_token
          AND run.lease_expires_at > database_now;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'account inventory daily rollup lease is lost'
                USING ERRCODE = 'P0002';
        END IF;
        PERFORM set_config('relay_control.history_run_write', '', true);
        PERFORM set_config(
            'relay_control.history_audit_write',
            'account_inventory_history.failed:rollup_fail_pending', true);
        INSERT INTO public.audit_logs (
            occurred_at, category, action, result, request_id, details
        ) VALUES (
            database_now, 'account_inventory_history',
            'account_inventory_history.failed', 'failure',
            'history-compaction-system',
            jsonb_build_object(
                'instance', target_run.instance_id::text,
                'summary_date', target_run.summary_date::text,
                'phase', 'rollup_fail_pending', 'row_count', 0
            )
        );
        PERFORM set_config('relay_control.history_audit_write', '', true);
        RETURN jsonb_build_object(
            'status','failed', 'expected_segment_count',expected_count,
            'completed_segment_count',completed_count,
            'segment_checksum_hex',NULL, 'account_rollup_count',0,
            'provider_rollup_count',0, 'idempotent',false,
            'failure_reason',fixed_reason
        );
    END IF;

    FOR segment_record IN
        SELECT source.canonical_row
        FROM (
            SELECT 1 AS kind_order, summary.summary_date AS order_date,
                   summary.instance_id AS order_instance_id,
                   summary.account_key AS order_key,
                   summary.provider_policy_version AS order_policy,
                   public.control_history_canonical_row_v1(VARIADIC ARRAY[
                       convert_to('account_segment','UTF8'),
                       convert_to(summary.summary_date::text,'UTF8'),
                       convert_to(summary.instance_id::text,'UTF8'),
                       convert_to(summary.provider,'UTF8'),
                       convert_to(summary.account_key,'UTF8'),
                       convert_to(summary.provider_policy_version::text,'UTF8'),
                       convert_to(public.control_history_time_text_v1(summary.first_scheduled_at),'UTF8'),
                       convert_to(public.control_history_time_text_v1(summary.last_scheduled_at),'UTF8'),
                       convert_to(public.control_history_time_text_v1(summary.first_observed_at),'UTF8'),
                       convert_to(public.control_history_time_text_v1(summary.last_observed_at),'UTF8'),
                       convert_to(summary.last_basic_status,'UTF8'),
                       convert_to(summary.sample_count::text,'UTF8'),
                       convert_to(summary.disabled_count::text,'UTF8'),
                       convert_to(summary.unavailable_count::text,'UTF8'),
                       convert_to(summary.error_count::text,'UTF8'),
                       convert_to(summary.active_count::text,'UTF8'),
                       convert_to(summary.unknown_count::text,'UTF8'),
                       convert_to(summary.first_success_count::text,'UTF8'),
                       convert_to(summary.last_success_count::text,'UTF8'),
                       convert_to(summary.success_reset_count::text,'UTF8'),
                       convert_to(summary.first_failed_count::text,'UTF8'),
                       convert_to(summary.last_failed_count::text,'UTF8'),
                       convert_to(summary.failed_reset_count::text,'UTF8')
                   ]::bytea[]) AS canonical_row
            FROM public.account_inventory_daily_summaries AS summary
            JOIN public.account_inventory_compaction_runs AS compaction
              ON compaction.compaction_run_id = summary.compaction_run_id
            WHERE summary.summary_date = target_run.summary_date
              AND summary.instance_id = target_run.instance_id
              AND summary.provider_policy_version = ANY(expected_policies)
              AND compaction.status = 'completed'
            UNION ALL
            SELECT 2, summary.summary_date, summary.instance_id, summary.provider,
                   summary.provider_policy_version,
                   public.control_history_canonical_row_v1(VARIADIC ARRAY[
                       convert_to('provider_segment','UTF8'),
                       convert_to(summary.summary_date::text,'UTF8'),
                       convert_to(summary.instance_id::text,'UTF8'),
                       convert_to(summary.provider,'UTF8'),
                       convert_to(summary.provider_policy_version::text,'UTF8'),
                       convert_to(summary.expected_poll_count::text,'UTF8'),
                       convert_to(summary.transport_success_count::text,'UTF8'),
                       convert_to(summary.contract_valid_count::text,'UTF8'),
                       convert_to(summary.snapshot_complete_count::text,'UTF8'),
                       convert_to(summary.promotion_applied_count::text,'UTF8'),
                       convert_to(summary.promotion_skipped_count::text,'UTF8'),
                       convert_to(summary.policy_changed_count::text,'UTF8'),
                       convert_to(summary.abandoned_count::text,'UTF8'),
                       convert_to(summary.degraded_count::text,'UTF8'),
                       CASE WHEN summary.first_promotion_at IS NULL THEN NULL::bytea
                            ELSE convert_to(public.control_history_time_text_v1(summary.first_promotion_at),'UTF8') END,
                       CASE WHEN summary.last_promotion_at IS NULL THEN NULL::bytea
                            ELSE convert_to(public.control_history_time_text_v1(summary.last_promotion_at),'UTF8') END,
                       convert_to(summary.coverage_numerator::text,'UTF8'),
                       convert_to(summary.coverage_denominator::text,'UTF8'),
                       convert_to(summary.coverage_threshold_basis_points::text,'UTF8'),
                       convert_to(summary.coverage_status,'UTF8')
                   ]::bytea[])
            FROM public.account_inventory_daily_provider_summaries AS summary
            JOIN public.account_inventory_compaction_runs AS compaction
              ON compaction.compaction_run_id = summary.compaction_run_id
            WHERE summary.summary_date = target_run.summary_date
              AND summary.instance_id = target_run.instance_id
              AND summary.provider_policy_version = ANY(expected_policies)
              AND compaction.status = 'completed'
        ) AS source
        ORDER BY source.kind_order, source.order_date, source.order_instance_id,
                 source.order_key, source.order_policy
    LOOP
        segment_checksum := sha256(
            segment_checksum || sha256(segment_record.canonical_row));
    END LOOP;

    IF target_run.status = 'completed' THEN
        IF target_run.expected_segment_count <> expected_count
           OR target_run.completed_segment_count <> completed_count THEN
            RAISE EXCEPTION 'account inventory daily rollup segment count changed'
                USING ERRCODE = 'P1102';
        END IF;
        IF NOT public.control_history_bytea_equal_v1(
            target_run.segment_checksum, segment_checksum) THEN
            RAISE EXCEPTION 'account inventory daily rollup checksum changed'
                USING ERRCODE = 'P1103';
        END IF;
        SELECT count(*) INTO account_rollup_count
        FROM public.account_inventory_daily_account_rollups
        WHERE rollup_run_id = target_run_id;
        SELECT count(*) INTO provider_rollup_count
        FROM public.account_inventory_daily_provider_rollups
        WHERE rollup_run_id = target_run_id;
        RETURN jsonb_build_object(
            'status','completed', 'expected_segment_count',expected_count,
            'completed_segment_count',completed_count,
            'segment_checksum_hex',encode(segment_checksum,'hex'),
            'account_rollup_count',account_rollup_count,
            'provider_rollup_count',provider_rollup_count,
            'idempotent',true, 'failure_reason',NULL
        );
    END IF;

    WITH segments AS (
        SELECT summary.*,
               lag(summary.last_success_count) OVER (
                   PARTITION BY summary.account_key
                   ORDER BY summary.first_scheduled_at, summary.last_scheduled_at,
                            summary.provider_policy_version
               ) AS previous_last_success_count,
               lag(summary.last_failed_count) OVER (
                   PARTITION BY summary.account_key
                   ORDER BY summary.first_scheduled_at, summary.last_scheduled_at,
                            summary.provider_policy_version
               ) AS previous_last_failed_count
        FROM public.account_inventory_daily_summaries AS summary
        JOIN public.account_inventory_compaction_runs AS compaction
          ON compaction.compaction_run_id = summary.compaction_run_id
        WHERE summary.summary_date = target_run.summary_date
          AND summary.instance_id = target_run.instance_id
          AND summary.provider_policy_version = ANY(expected_policies)
          AND compaction.status = 'completed'
    ), inserted AS (
        INSERT INTO public.account_inventory_daily_account_rollups (
            rollup_run_id,summary_date,instance_id,provider,account_key,
            first_scheduled_at,last_scheduled_at,first_observed_at,last_observed_at,
            last_basic_status,sample_count,disabled_count,unavailable_count,
            error_count,active_count,unknown_count,first_success_count,
            last_success_count,success_reset_count,first_failed_count,
            last_failed_count,failed_reset_count,created_at
        )
        SELECT target_run_id,target_run.summary_date,target_run.instance_id,
               (array_agg(provider ORDER BY first_scheduled_at,
                          provider_policy_version))[1], account_key,
               min(first_scheduled_at),max(last_scheduled_at),
               min(first_observed_at),max(last_observed_at),
               (array_agg(last_basic_status ORDER BY last_scheduled_at DESC,
                          provider_policy_version DESC))[1],
               sum(sample_count)::integer,sum(disabled_count)::integer,
               sum(unavailable_count)::integer,sum(error_count)::integer,
               sum(active_count)::integer,sum(unknown_count)::integer,
               (array_agg(first_success_count ORDER BY first_scheduled_at,
                          provider_policy_version))[1],
               (array_agg(last_success_count ORDER BY last_scheduled_at DESC,
                          provider_policy_version DESC))[1],
               (sum(success_reset_count)
                + count(*) FILTER (WHERE previous_last_success_count IS NOT NULL
                                     AND first_success_count < previous_last_success_count))::integer,
               (array_agg(first_failed_count ORDER BY first_scheduled_at,
                          provider_policy_version))[1],
               (array_agg(last_failed_count ORDER BY last_scheduled_at DESC,
                          provider_policy_version DESC))[1],
               (sum(failed_reset_count)
                + count(*) FILTER (WHERE previous_last_failed_count IS NOT NULL
                                     AND first_failed_count < previous_last_failed_count))::integer,
               database_now
        FROM segments GROUP BY account_key
        RETURNING 1
    ) SELECT count(*) INTO account_rollup_count FROM inserted;

    WITH segments AS (
        SELECT summary.*
        FROM public.account_inventory_daily_provider_summaries AS summary
        JOIN public.account_inventory_compaction_runs AS compaction
          ON compaction.compaction_run_id = summary.compaction_run_id
        WHERE summary.summary_date = target_run.summary_date
          AND summary.instance_id = target_run.instance_id
          AND summary.provider_policy_version = ANY(expected_policies)
          AND compaction.status = 'completed'
    ), aggregated AS (
        SELECT provider,sum(expected_poll_count)::integer AS expected_poll_count,
               sum(transport_success_count)::integer AS transport_success_count,
               sum(contract_valid_count)::integer AS contract_valid_count,
               sum(snapshot_complete_count)::integer AS snapshot_complete_count,
               sum(promotion_applied_count)::integer AS promotion_applied_count,
               sum(promotion_skipped_count)::integer AS promotion_skipped_count,
               sum(policy_changed_count)::integer AS policy_changed_count,
               sum(abandoned_count)::integer AS abandoned_count,
               sum(degraded_count)::integer AS degraded_count,
               min(first_promotion_at) AS first_promotion_at,
               max(last_promotion_at) AS last_promotion_at
        FROM segments GROUP BY provider HAVING sum(expected_poll_count) > 0
    ), inserted AS (
        INSERT INTO public.account_inventory_daily_provider_rollups (
            rollup_run_id,summary_date,instance_id,provider,expected_poll_count,
            transport_success_count,contract_valid_count,snapshot_complete_count,
            promotion_applied_count,promotion_skipped_count,policy_changed_count,
            abandoned_count,degraded_count,first_promotion_at,last_promotion_at,
            coverage_numerator,coverage_denominator,coverage_ratio,
            coverage_threshold_basis_points,coverage_status,created_at
        )
        SELECT target_run_id,target_run.summary_date,target_run.instance_id,provider,
               expected_poll_count,transport_success_count,contract_valid_count,
               snapshot_complete_count,promotion_applied_count,promotion_skipped_count,
               policy_changed_count,abandoned_count,degraded_count,
               first_promotion_at,last_promotion_at,promotion_applied_count,
               expected_poll_count,
               round(promotion_applied_count::numeric/expected_poll_count,8),9500,
               CASE WHEN promotion_applied_count*10000::bigint
                              >= expected_poll_count*9500::bigint
                    THEN 'complete' ELSE 'partial' END,database_now
        FROM aggregated
        RETURNING 1
    ) SELECT count(*) INTO provider_rollup_count FROM inserted;

    PERFORM set_config('relay_control.history_run_write',
                       'account_inventory_daily_rollup_runs', true);
    UPDATE public.account_inventory_daily_rollup_runs AS run
    SET status='completed',expected_segment_count=expected_count,
        completed_segment_count=completed_count,checksum_version=1,
        segment_checksum=segment_checksum,completed_fencing_token=target_fencing_token,
        completed_at=database_now,claim_owner=NULL,lease_expires_at=NULL,
        fencing_token=NULL,updated_at=database_now
    WHERE run.rollup_run_id=target_run_id AND run.status='pending'
      AND run.fencing_token=target_fencing_token
      AND run.lease_expires_at>database_now;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'account inventory daily rollup lease is lost'
            USING ERRCODE = 'P0002';
    END IF;
    PERFORM set_config('relay_control.history_run_write','',true);
    PERFORM set_config(
        'relay_control.history_audit_write',
        'account_inventory_history.completed:rollup_complete',true);
    INSERT INTO public.audit_logs(
        occurred_at,category,action,result,request_id,details
    ) VALUES(
        database_now,'account_inventory_history',
        'account_inventory_history.completed','success',
        'history-compaction-system',jsonb_build_object(
            'instance',target_run.instance_id::text,
            'summary_date',target_run.summary_date::text,
            'phase','rollup_complete',
            'row_count',account_rollup_count+provider_rollup_count
        )
    );
    PERFORM set_config('relay_control.history_audit_write','',true);
    RETURN jsonb_build_object(
        'status','completed','expected_segment_count',expected_count,
        'completed_segment_count',completed_count,
        'segment_checksum_hex',encode(segment_checksum,'hex'),
        'account_rollup_count',account_rollup_count,
        'provider_rollup_count',provider_rollup_count,
        'idempotent',false,'failure_reason',NULL
    );
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_delete_account_inventory_poll_retention_v1(
    delete_limit integer
) RETURNS jsonb
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
SET TimeZone = 'UTC'
AS $$
DECLARE
    retention_cutoff timestamptz;
    target_run public.account_inventory_compaction_runs%ROWTYPE;
    target_poll record;
    remaining_poll_count bigint;
    remaining_provider_count bigint;
    remaining_duplicate_count bigint;
    batch_poll_count bigint := 0;
    batch_provider_count bigint := 0;
    batch_duplicate_count bigint := 0;
BEGIN
    IF delete_limit IS NULL OR delete_limit NOT BETWEEN 1 AND 5000 THEN
        RAISE EXCEPTION 'invalid account inventory poll retention limit'
            USING ERRCODE = '22023';
    END IF;
    PERFORM pg_advisory_xact_lock(72193847561029384);
    retention_cutoff := clock_timestamp() - interval '30 days';
    SELECT compaction.* INTO target_run
    FROM public.account_inventory_compaction_runs AS compaction
    WHERE compaction.status='completed'
      AND compaction.deleted_snapshot_count=compaction.source_snapshot_count
      AND EXISTS (
          SELECT 1 FROM public.account_inventory_daily_rollup_runs AS rollup
          WHERE rollup.summary_date=compaction.summary_date
            AND rollup.instance_id=compaction.instance_id
            AND rollup.status='completed')
      AND EXISTS (
          SELECT 1 FROM public.account_inventory_poll_runs AS poll
          WHERE poll.instance_id=compaction.instance_id
            AND poll.provider_policy_version=compaction.provider_policy_version
            AND poll.scheduled_at >= (compaction.summary_date::timestamp AT TIME ZONE 'UTC')
            AND poll.scheduled_at < ((compaction.summary_date+1)::timestamp AT TIME ZONE 'UTC')
            AND poll.scheduled_at <= retention_cutoff
            AND poll.status IN ('finalized','abandoned'))
      AND NOT EXISTS (
          SELECT 1 FROM public.account_inventory_snapshot_items AS snapshot
          JOIN public.account_inventory_poll_runs AS poll
            ON poll.poll_run_id=snapshot.poll_run_id AND poll.instance_id=snapshot.instance_id
          WHERE poll.instance_id=compaction.instance_id
            AND poll.provider_policy_version=compaction.provider_policy_version
            AND poll.scheduled_at >= (compaction.summary_date::timestamp AT TIME ZONE 'UTC')
            AND poll.scheduled_at < ((compaction.summary_date+1)::timestamp AT TIME ZONE 'UTC'))
      AND NOT EXISTS (
          SELECT 1 FROM public.account_inventory_poll_runs AS poll
          WHERE poll.instance_id=compaction.instance_id
            AND poll.provider_policy_version=compaction.provider_policy_version
            AND poll.scheduled_at >= (compaction.summary_date::timestamp AT TIME ZONE 'UTC')
            AND poll.scheduled_at < ((compaction.summary_date+1)::timestamp AT TIME ZONE 'UTC')
            AND poll.status NOT IN ('finalized','abandoned'))
      AND compaction.deleted_poll_count
          + (SELECT count(*) FROM public.account_inventory_poll_runs AS poll
             WHERE poll.instance_id=compaction.instance_id
               AND poll.provider_policy_version=compaction.provider_policy_version
               AND poll.scheduled_at >= (compaction.summary_date::timestamp AT TIME ZONE 'UTC')
               AND poll.scheduled_at < ((compaction.summary_date+1)::timestamp AT TIME ZONE 'UTC'))
          = compaction.source_poll_count
      AND compaction.deleted_provider_result_count
          + (SELECT count(*) FROM public.account_inventory_poll_provider_results AS result
             JOIN public.account_inventory_poll_runs AS poll ON poll.poll_run_id=result.poll_run_id
             WHERE poll.instance_id=compaction.instance_id
               AND poll.provider_policy_version=compaction.provider_policy_version
               AND poll.scheduled_at >= (compaction.summary_date::timestamp AT TIME ZONE 'UTC')
               AND poll.scheduled_at < ((compaction.summary_date+1)::timestamp AT TIME ZONE 'UTC'))
          = compaction.source_provider_result_count
      AND compaction.deleted_duplicate_count
          + (SELECT count(*) FROM public.account_inventory_poll_duplicates AS duplicate_row
             JOIN public.account_inventory_poll_runs AS poll ON poll.poll_run_id=duplicate_row.poll_run_id
             WHERE poll.instance_id=compaction.instance_id
               AND poll.provider_policy_version=compaction.provider_policy_version
               AND poll.scheduled_at >= (compaction.summary_date::timestamp AT TIME ZONE 'UTC')
               AND poll.scheduled_at < ((compaction.summary_date+1)::timestamp AT TIME ZONE 'UTC'))
          = compaction.source_duplicate_count
    ORDER BY compaction.summary_date,compaction.instance_id,compaction.provider_policy_version
    FOR UPDATE SKIP LOCKED LIMIT 1;
    IF NOT FOUND THEN
        RETURN jsonb_build_object('processed_count',0,'deleted_row_count',0);
    END IF;

    FOR target_poll IN
        SELECT poll.poll_run_id,
               (SELECT count(*) FROM public.account_inventory_poll_provider_results AS result
                WHERE result.poll_run_id=poll.poll_run_id) AS provider_count,
               (SELECT count(*) FROM public.account_inventory_poll_duplicates AS duplicate_row
                WHERE duplicate_row.poll_run_id=poll.poll_run_id) AS duplicate_count
        FROM public.account_inventory_poll_runs AS poll
        WHERE poll.instance_id=target_run.instance_id
          AND poll.provider_policy_version=target_run.provider_policy_version
          AND poll.scheduled_at >= (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
          AND poll.scheduled_at < ((target_run.summary_date+1)::timestamp AT TIME ZONE 'UTC')
          AND poll.scheduled_at <= retention_cutoff
          AND poll.status IN ('finalized','abandoned')
        ORDER BY poll.scheduled_at,poll.poll_run_id
        FOR UPDATE OF poll SKIP LOCKED LIMIT delete_limit
    LOOP
        PERFORM set_config('relay_control.history_poll_retention_delete',
                           target_poll.poll_run_id::text,true);
        DELETE FROM public.account_inventory_poll_runs
        WHERE poll_run_id=target_poll.poll_run_id;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'account inventory poll retention lost its lock'
                USING ERRCODE='P0002';
        END IF;
        batch_poll_count := batch_poll_count+1;
        batch_provider_count := batch_provider_count+target_poll.provider_count;
        batch_duplicate_count := batch_duplicate_count+target_poll.duplicate_count;
    END LOOP;
    PERFORM set_config('relay_control.history_poll_retention_delete','',true);
    IF batch_poll_count=0 THEN
        RETURN jsonb_build_object('processed_count',0,'deleted_row_count',0);
    END IF;

    SELECT count(*) INTO remaining_poll_count
    FROM public.account_inventory_poll_runs AS poll
    WHERE poll.instance_id=target_run.instance_id
      AND poll.provider_policy_version=target_run.provider_policy_version
      AND poll.scheduled_at >= (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
      AND poll.scheduled_at < ((target_run.summary_date+1)::timestamp AT TIME ZONE 'UTC');
    SELECT count(*) INTO remaining_provider_count
    FROM public.account_inventory_poll_provider_results AS result
    JOIN public.account_inventory_poll_runs AS poll ON poll.poll_run_id=result.poll_run_id
    WHERE poll.instance_id=target_run.instance_id
      AND poll.provider_policy_version=target_run.provider_policy_version
      AND poll.scheduled_at >= (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
      AND poll.scheduled_at < ((target_run.summary_date+1)::timestamp AT TIME ZONE 'UTC');
    SELECT count(*) INTO remaining_duplicate_count
    FROM public.account_inventory_poll_duplicates AS duplicate_row
    JOIN public.account_inventory_poll_runs AS poll ON poll.poll_run_id=duplicate_row.poll_run_id
    WHERE poll.instance_id=target_run.instance_id
      AND poll.provider_policy_version=target_run.provider_policy_version
      AND poll.scheduled_at >= (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
      AND poll.scheduled_at < ((target_run.summary_date+1)::timestamp AT TIME ZONE 'UTC');

    PERFORM set_config('relay_control.history_run_write',
                       'account_inventory_compaction_runs',true);
    UPDATE public.account_inventory_compaction_runs AS compaction
    SET deleted_poll_count=compaction.deleted_poll_count+batch_poll_count,
        deleted_provider_result_count=
            compaction.deleted_provider_result_count+batch_provider_count,
        deleted_duplicate_count=compaction.deleted_duplicate_count+batch_duplicate_count,
        updated_at=clock_timestamp()
    WHERE compaction.compaction_run_id=target_run.compaction_run_id
      AND compaction.status='completed'
      AND compaction.deleted_poll_count+batch_poll_count+remaining_poll_count
            = compaction.source_poll_count
      AND compaction.deleted_provider_result_count+batch_provider_count
            +remaining_provider_count=compaction.source_provider_result_count
      AND compaction.deleted_duplicate_count+batch_duplicate_count
            +remaining_duplicate_count=compaction.source_duplicate_count;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'account inventory poll retention conservation mismatch'
            USING ERRCODE='23514';
    END IF;
    PERFORM set_config('relay_control.history_run_write','',true);
    PERFORM set_config('relay_control.history_audit_write',
        'account_inventory_history.retention_delete_batch:retention_poll',true);
    INSERT INTO public.audit_logs (
        occurred_at,category,action,result,request_id,details
    ) VALUES (
        clock_timestamp(),'account_inventory_history',
        'account_inventory_history.retention_delete_batch','success',
        'history-compaction-system',jsonb_build_object(
            'instance',target_run.instance_id::text,
            'summary_date',target_run.summary_date::text,
            'phase','retention_poll','row_count',
            batch_poll_count+batch_provider_count+batch_duplicate_count)
    );
    PERFORM set_config('relay_control.history_audit_write','',true);
    RETURN jsonb_build_object(
        'processed_count',batch_poll_count,
        'deleted_row_count',batch_poll_count+batch_provider_count+batch_duplicate_count);
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_delete_account_inventory_rollup_row_retention_v1(
    delete_limit integer
) RETURNS jsonb
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
SET TimeZone = 'UTC'
AS $$
DECLARE
    retention_cutoff timestamptz;
    target_run public.account_inventory_daily_rollup_runs%ROWTYPE;
    target_row record;
    remaining_budget integer := delete_limit;
    deleted_count bigint := 0;
BEGIN
    IF delete_limit IS NULL OR delete_limit NOT BETWEEN 1 AND 5000 THEN
        RAISE EXCEPTION 'invalid account inventory rollup row retention limit'
            USING ERRCODE = '22023';
    END IF;
    PERFORM pg_advisory_xact_lock(72193847561029384);
    retention_cutoff := clock_timestamp() - interval '30 days';
    SELECT rollup.* INTO target_run
    FROM public.account_inventory_daily_rollup_runs AS rollup
    WHERE rollup.status = 'completed'
      AND rollup.completed_at <= retention_cutoff
      AND ((rollup.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
            <= retention_cutoff
      AND NOT EXISTS (
          SELECT 1 FROM public.account_inventory_poll_runs AS poll
          WHERE poll.instance_id = rollup.instance_id
            AND poll.scheduled_at >=
                (rollup.summary_date::timestamp AT TIME ZONE 'UTC')
            AND poll.scheduled_at <
                ((rollup.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
      )
      AND ((SELECT count(*) FROM public.account_inventory_daily_summaries AS row
            WHERE row.summary_date=rollup.summary_date AND row.instance_id=rollup.instance_id)
         + (SELECT count(*) FROM public.account_inventory_daily_provider_summaries AS row
            WHERE row.summary_date=rollup.summary_date AND row.instance_id=rollup.instance_id)
         + (SELECT count(*) FROM public.account_inventory_daily_account_rollups AS row
            WHERE row.rollup_run_id=rollup.rollup_run_id)
         + (SELECT count(*) FROM public.account_inventory_daily_provider_rollups AS row
            WHERE row.rollup_run_id=rollup.rollup_run_id)) > 0
    ORDER BY rollup.summary_date, rollup.instance_id
    FOR UPDATE SKIP LOCKED LIMIT 1;
    IF NOT FOUND THEN
        RETURN jsonb_build_object('processed_count',0,'deleted_row_count',0);
    END IF;

    FOR target_row IN
        SELECT 'account_inventory_daily_summaries'::text AS table_name,
               row.summary_id AS row_id
        FROM public.account_inventory_daily_summaries AS row
        WHERE row.summary_date=target_run.summary_date AND row.instance_id=target_run.instance_id
        ORDER BY row.summary_id FOR UPDATE SKIP LOCKED LIMIT remaining_budget
    LOOP
        PERFORM set_config('relay_control.history_rollup_row_retention_delete',
                           target_row.table_name || ':' || target_row.row_id::text,true);
        DELETE FROM public.account_inventory_daily_summaries WHERE summary_id=target_row.row_id;
        deleted_count := deleted_count + 1;
        remaining_budget := remaining_budget - 1;
    END LOOP;
    IF EXISTS (SELECT 1 FROM public.account_inventory_daily_summaries AS row
               WHERE row.summary_date=target_run.summary_date
                 AND row.instance_id=target_run.instance_id) THEN
        remaining_budget := 0;
    END IF;
    IF remaining_budget > 0 THEN
      FOR target_row IN
        SELECT 'account_inventory_daily_provider_summaries'::text AS table_name,
               row.provider_summary_id AS row_id
        FROM public.account_inventory_daily_provider_summaries AS row
        WHERE row.summary_date=target_run.summary_date AND row.instance_id=target_run.instance_id
        ORDER BY row.provider_summary_id FOR UPDATE SKIP LOCKED LIMIT remaining_budget
    LOOP
        PERFORM set_config('relay_control.history_rollup_row_retention_delete',
                           target_row.table_name || ':' || target_row.row_id::text,true);
        DELETE FROM public.account_inventory_daily_provider_summaries
        WHERE provider_summary_id=target_row.row_id;
        deleted_count := deleted_count + 1;
        remaining_budget := remaining_budget - 1;
      END LOOP;
    END IF;
    IF EXISTS (SELECT 1 FROM public.account_inventory_daily_provider_summaries AS row
               WHERE row.summary_date=target_run.summary_date
                 AND row.instance_id=target_run.instance_id) THEN
        remaining_budget := 0;
    END IF;
    IF remaining_budget > 0 THEN
      FOR target_row IN
        SELECT 'account_inventory_daily_account_rollups'::text AS table_name,
               row.account_rollup_id AS row_id
        FROM public.account_inventory_daily_account_rollups AS row
        WHERE row.rollup_run_id=target_run.rollup_run_id
        ORDER BY row.account_rollup_id FOR UPDATE SKIP LOCKED LIMIT remaining_budget
    LOOP
        PERFORM set_config('relay_control.history_rollup_row_retention_delete',
                           target_row.table_name || ':' || target_row.row_id::text,true);
        DELETE FROM public.account_inventory_daily_account_rollups
        WHERE account_rollup_id=target_row.row_id;
        deleted_count := deleted_count + 1;
        remaining_budget := remaining_budget - 1;
      END LOOP;
    END IF;
    IF EXISTS (SELECT 1 FROM public.account_inventory_daily_account_rollups AS row
               WHERE row.rollup_run_id=target_run.rollup_run_id) THEN
        remaining_budget := 0;
    END IF;
    IF remaining_budget > 0 THEN
      FOR target_row IN
        SELECT 'account_inventory_daily_provider_rollups'::text AS table_name,
               row.provider_rollup_id AS row_id
        FROM public.account_inventory_daily_provider_rollups AS row
        WHERE row.rollup_run_id=target_run.rollup_run_id
        ORDER BY row.provider_rollup_id FOR UPDATE SKIP LOCKED LIMIT remaining_budget
    LOOP
        PERFORM set_config('relay_control.history_rollup_row_retention_delete',
                           target_row.table_name || ':' || target_row.row_id::text,true);
        DELETE FROM public.account_inventory_daily_provider_rollups
        WHERE provider_rollup_id=target_row.row_id;
        deleted_count := deleted_count + 1;
        remaining_budget := remaining_budget - 1;
      END LOOP;
    END IF;
    PERFORM set_config('relay_control.history_rollup_row_retention_delete','',true);
    IF deleted_count=0 THEN
        RETURN jsonb_build_object('processed_count',0,'deleted_row_count',0);
    END IF;
    PERFORM set_config('relay_control.history_audit_write',
        'account_inventory_history.retention_delete_batch:retention_rollup_rows',true);
    INSERT INTO public.audit_logs (
        occurred_at, category, action, result, request_id, details
    ) VALUES (
        clock_timestamp(),'account_inventory_history',
        'account_inventory_history.retention_delete_batch','success',
        'history-compaction-system',jsonb_build_object(
            'instance',target_run.instance_id::text,
            'summary_date',target_run.summary_date::text,
            'phase','retention_rollup_rows','row_count',deleted_count)
    );
    PERFORM set_config('relay_control.history_audit_write','',true);
    RETURN jsonb_build_object(
        'processed_count',deleted_count,'deleted_row_count',deleted_count);
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_delete_account_inventory_rollup_run_retention_v1(
    delete_limit integer
) RETURNS jsonb
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
SET TimeZone = 'UTC'
AS $$
DECLARE
    retention_cutoff timestamptz;
    target_run record;
    deleted_count bigint := 0;
BEGIN
    IF delete_limit IS NULL OR delete_limit NOT BETWEEN 1 AND 5000 THEN
        RAISE EXCEPTION 'invalid account inventory rollup run retention limit'
            USING ERRCODE = '22023';
    END IF;
    PERFORM pg_advisory_xact_lock(72193847561029384);
    retention_cutoff := clock_timestamp() - interval '30 days';
    FOR target_run IN
        SELECT rollup.rollup_run_id,rollup.instance_id,rollup.summary_date
        FROM public.account_inventory_daily_rollup_runs AS rollup
        WHERE rollup.status='completed' AND rollup.completed_at <= retention_cutoff
          AND ((rollup.summary_date+1)::timestamp AT TIME ZONE 'UTC') <= retention_cutoff
          AND NOT EXISTS (SELECT 1 FROM public.account_inventory_daily_account_rollups AS row
                          WHERE row.rollup_run_id=rollup.rollup_run_id)
          AND NOT EXISTS (SELECT 1 FROM public.account_inventory_daily_provider_rollups AS row
                          WHERE row.rollup_run_id=rollup.rollup_run_id)
          AND NOT EXISTS (SELECT 1 FROM public.account_inventory_daily_summaries AS row
                          WHERE row.summary_date=rollup.summary_date AND row.instance_id=rollup.instance_id)
          AND NOT EXISTS (SELECT 1 FROM public.account_inventory_daily_provider_summaries AS row
                          WHERE row.summary_date=rollup.summary_date AND row.instance_id=rollup.instance_id)
          AND NOT EXISTS (SELECT 1 FROM public.account_inventory_poll_runs AS poll
                          WHERE poll.instance_id=rollup.instance_id
                            AND poll.scheduled_at >= (rollup.summary_date::timestamp AT TIME ZONE 'UTC')
                            AND poll.scheduled_at < ((rollup.summary_date+1)::timestamp AT TIME ZONE 'UTC'))
          AND NOT EXISTS (SELECT 1 FROM public.account_inventory_history_retired_days AS retired
                          WHERE retired.summary_date=rollup.summary_date
                            AND retired.instance_id=rollup.instance_id)
        ORDER BY rollup.summary_date,rollup.instance_id
        FOR UPDATE SKIP LOCKED LIMIT delete_limit
    LOOP
        -- Poll insertion takes only this instance/day lock.  Retention takes
        -- the global history lock first, then the same day lock, so the marker
        -- and late evidence are ordered without introducing a lock cycle.
        PERFORM pg_advisory_xact_lock(
            hashtext(target_run.instance_id::text),
            (target_run.summary_date - date '2000-01-01')::integer
        );
        IF EXISTS (
            SELECT 1 FROM public.account_inventory_poll_runs AS poll
            WHERE poll.instance_id=target_run.instance_id
              AND poll.scheduled_at >=
                  (target_run.summary_date::timestamp AT TIME ZONE 'UTC')
              AND poll.scheduled_at <
                  ((target_run.summary_date+1)::timestamp AT TIME ZONE 'UTC')
        ) THEN
            CONTINUE;
        END IF;
        PERFORM set_config('relay_control.history_retired_day_write',
            target_run.summary_date::text || ':' || target_run.instance_id::text,true);
        INSERT INTO public.account_inventory_history_retired_days(
            summary_date,instance_id,retired_at
        ) VALUES(target_run.summary_date,target_run.instance_id,clock_timestamp());
        PERFORM set_config('relay_control.history_retired_day_write','',true);
        PERFORM set_config('relay_control.history_rollup_run_retention_delete',
                           target_run.rollup_run_id::text,true);
        DELETE FROM public.account_inventory_daily_rollup_runs
        WHERE rollup_run_id=target_run.rollup_run_id AND status='completed';
        IF NOT FOUND THEN
            RAISE EXCEPTION 'account inventory rollup run retention lost its lock'
                USING ERRCODE='P0002';
        END IF;
        deleted_count := deleted_count+1;
        PERFORM set_config('relay_control.history_audit_write',
            'account_inventory_history.retention_delete_batch:retention_rollup_run',true);
        INSERT INTO public.audit_logs (
            occurred_at,category,action,result,request_id,details
        ) VALUES (
            clock_timestamp(),'account_inventory_history',
            'account_inventory_history.retention_delete_batch','success',
            'history-compaction-system',jsonb_build_object(
                'instance',target_run.instance_id::text,
                'summary_date',target_run.summary_date::text,
                'phase','retention_rollup_run','row_count',1)
        );
        PERFORM set_config('relay_control.history_audit_write','',true);
    END LOOP;
    PERFORM set_config('relay_control.history_rollup_run_retention_delete','',true);
    RETURN jsonb_build_object(
        'processed_count',deleted_count,'deleted_row_count',deleted_count);
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_delete_account_inventory_compaction_run_retention_v1(
    delete_limit integer
) RETURNS jsonb
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
SET TimeZone = 'UTC'
AS $$
DECLARE
    retention_cutoff timestamptz;
    target_run record;
    deleted_count bigint := 0;
BEGIN
    IF delete_limit IS NULL OR delete_limit NOT BETWEEN 1 AND 5000 THEN
        RAISE EXCEPTION 'invalid account inventory compaction run retention limit'
            USING ERRCODE = '22023';
    END IF;
    PERFORM pg_advisory_xact_lock(72193847561029384);
    retention_cutoff := clock_timestamp() - interval '30 days';
    FOR target_run IN
        SELECT compaction.compaction_run_id,compaction.instance_id,compaction.summary_date
        FROM public.account_inventory_compaction_runs AS compaction
        WHERE compaction.status='completed' AND compaction.completed_at <= retention_cutoff
          AND ((compaction.summary_date+1)::timestamp AT TIME ZONE 'UTC') <= retention_cutoff
          AND compaction.deleted_snapshot_count=compaction.source_snapshot_count
          AND compaction.deleted_poll_count=compaction.source_poll_count
          AND compaction.deleted_provider_result_count=compaction.source_provider_result_count
          AND compaction.deleted_duplicate_count=compaction.source_duplicate_count
          AND NOT EXISTS (SELECT 1 FROM public.account_inventory_daily_summaries AS row
                          WHERE row.compaction_run_id=compaction.compaction_run_id)
          AND NOT EXISTS (SELECT 1 FROM public.account_inventory_daily_provider_summaries AS row
                          WHERE row.compaction_run_id=compaction.compaction_run_id)
          AND NOT EXISTS (SELECT 1 FROM public.account_inventory_daily_rollup_runs AS rollup
                          WHERE rollup.summary_date=compaction.summary_date
                            AND rollup.instance_id=compaction.instance_id)
          AND EXISTS (SELECT 1 FROM public.account_inventory_history_retired_days AS retired
                      WHERE retired.summary_date=compaction.summary_date
                        AND retired.instance_id=compaction.instance_id)
          AND NOT EXISTS (SELECT 1 FROM public.account_inventory_poll_runs AS poll
                          WHERE poll.instance_id=compaction.instance_id
                            AND poll.provider_policy_version=compaction.provider_policy_version
                            AND poll.scheduled_at >= (compaction.summary_date::timestamp AT TIME ZONE 'UTC')
                            AND poll.scheduled_at < ((compaction.summary_date+1)::timestamp AT TIME ZONE 'UTC'))
        ORDER BY compaction.summary_date,compaction.instance_id,
                 compaction.provider_policy_version
        FOR UPDATE SKIP LOCKED LIMIT delete_limit
    LOOP
        PERFORM set_config('relay_control.history_compaction_run_retention_delete',
                           target_run.compaction_run_id::text,true);
        DELETE FROM public.account_inventory_compaction_runs
        WHERE compaction_run_id=target_run.compaction_run_id AND status='completed';
        IF NOT FOUND THEN
            RAISE EXCEPTION 'account inventory compaction run retention lost its lock'
                USING ERRCODE='P0002';
        END IF;
        deleted_count := deleted_count+1;
        PERFORM set_config('relay_control.history_audit_write',
            'account_inventory_history.retention_delete_batch:retention_compaction_run',true);
        INSERT INTO public.audit_logs (
            occurred_at,category,action,result,request_id,details
        ) VALUES (
            clock_timestamp(),'account_inventory_history',
            'account_inventory_history.retention_delete_batch','success',
            'history-compaction-system',jsonb_build_object(
                'instance',target_run.instance_id::text,
                'summary_date',target_run.summary_date::text,
                'phase','retention_compaction_run','row_count',1)
        );
        PERFORM set_config('relay_control.history_audit_write','',true);
    END LOOP;
    PERFORM set_config('relay_control.history_compaction_run_retention_delete','',true);
    RETURN jsonb_build_object(
        'processed_count',deleted_count,'deleted_row_count',deleted_count);
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_list_account_inventory_history_metrics_v1()
RETURNS TABLE (
    instance_id uuid, provider text, coverage_ratio numeric,
    coverage_complete boolean, summary_date date
)
LANGUAGE sql
SECURITY DEFINER
STABLE
SET search_path = pg_catalog
SET TimeZone = 'UTC'
AS $$
    SELECT DISTINCT ON (rollup.instance_id, rollup.provider)
           rollup.instance_id, rollup.provider, rollup.coverage_ratio,
           rollup.coverage_status = 'complete', rollup.summary_date
    FROM public.account_inventory_daily_provider_rollups AS rollup
    JOIN public.account_inventory_daily_rollup_runs AS run
      ON run.rollup_run_id = rollup.rollup_run_id
     AND run.summary_date = rollup.summary_date
     AND run.instance_id = rollup.instance_id
    WHERE run.status = 'completed'
    ORDER BY rollup.instance_id, rollup.provider, rollup.summary_date DESC
$$;
-- +goose StatementEnd

-- The runtime collector consumes one identity-free, fixed-shape database
-- snapshot.  Process-local delete counters are merged by the Store; audit rows
-- are intentionally not repurposed as a metrics counter.
-- +goose StatementBegin
CREATE FUNCTION public.control_account_inventory_history_metrics_snapshot_v1()
RETURNS jsonb
LANGUAGE sql
SECURITY DEFINER
STABLE
SET search_path = pg_catalog
SET TimeZone = 'UTC'
AS $$
    WITH timing AS (
        SELECT statement_timestamp() AS database_now,
               statement_timestamp() - interval '30 days' AS retention_cutoff
    ),
    unfinished_days AS (
        SELECT ((run.summary_date + 1)::timestamp AT TIME ZONE 'UTC') AS day_end
        FROM public.account_inventory_compaction_runs AS run, timing
        WHERE run.status <> 'completed'
          AND ((run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
                <= timing.database_now - interval '72 hours'
        UNION ALL
        -- A completed zero-source compaction still represents unfinished work
        -- until its instance/day rollup exists and completes.
        SELECT ((run.summary_date + 1)::timestamp AT TIME ZONE 'UTC') AS day_end
        FROM public.account_inventory_compaction_runs AS run, timing
        WHERE run.status = 'completed'
          AND ((run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
                <= timing.database_now - interval '72 hours'
          AND NOT EXISTS (
              SELECT 1 FROM public.account_inventory_daily_rollup_runs AS rollup
              WHERE rollup.summary_date = run.summary_date
                AND rollup.instance_id = run.instance_id
                AND rollup.status = 'completed'
          )
          AND NOT EXISTS (
              SELECT 1 FROM public.account_inventory_history_retired_days AS retired
              WHERE retired.summary_date = run.summary_date
                AND retired.instance_id = run.instance_id
          )
        UNION ALL
        -- A default-disabled runner has no run rows yet.  Source-backed days
        -- must still make the growing eligible backlog visible before the
        -- first planner pass.
        SELECT DISTINCT
               (((poll.scheduled_at AT TIME ZONE 'UTC')::date + 1)::timestamp
                    AT TIME ZONE 'UTC') AS day_end
        FROM public.account_inventory_poll_runs AS poll, timing
        WHERE (((poll.scheduled_at AT TIME ZONE 'UTC')::date + 1)::timestamp
                    AT TIME ZONE 'UTC')
                <= timing.database_now - interval '72 hours'
          AND NOT EXISTS (
              SELECT 1
              FROM public.account_inventory_history_retired_days AS retired
              WHERE retired.summary_date = (poll.scheduled_at AT TIME ZONE 'UTC')::date
                AND retired.instance_id = poll.instance_id
          )
          AND NOT EXISTS (
              SELECT 1
              FROM public.account_inventory_compaction_runs AS existing
              WHERE existing.summary_date = (poll.scheduled_at AT TIME ZONE 'UTC')::date
                AND existing.instance_id = poll.instance_id
                AND existing.provider_policy_version = poll.provider_policy_version
          )
        UNION ALL
        -- Planner candidates also exist for monitored activation intersections
        -- with no poll evidence.  Limit generation to the fixed retained
        -- horizon so a disabled service cannot turn a long activation into an
        -- unbounded metrics query.
        SELECT DISTINCT
               ((candidate.summary_date + 1)::timestamp AT TIME ZONE 'UTC') AS day_end
        FROM (
            SELECT asset.instance_id, policy.policy_version_id,
                   greatest(policy.effective_from, monitoring.effective_from) AS lower_at,
                   least(coalesce(policy.effective_to, 'infinity'::timestamptz),
                         coalesce(monitoring.effective_to, 'infinity'::timestamptz)) AS upper_at
            FROM public.provider_inventory_policy_activations AS policy
            JOIN public.relay_node_assets AS asset
              ON asset.node_type = policy.node_type
             AND asset.driver_contract_version = policy.driver_contract_version
            JOIN public.relay_node_inventory_monitoring_activations AS monitoring
              ON monitoring.instance_id = asset.instance_id
             AND policy.active_range && monitoring.active_range
        ) AS activation
        CROSS JOIN timing
        CROSS JOIN LATERAL generate_series(
            greatest((activation.lower_at AT TIME ZONE 'UTC')::date,
                     (timing.retention_cutoff AT TIME ZONE 'UTC')::date),
            least(((activation.upper_at - interval '1 microsecond') AT TIME ZONE 'UTC')::date,
                  ((timing.database_now - interval '72 hours') AT TIME ZONE 'UTC')::date - 1),
            interval '1 day'
        ) AS generated_at
        CROSS JOIN LATERAL (
            SELECT generated_at::date AS summary_date,
                   greatest(activation.lower_at,
                       (generated_at::date::timestamp AT TIME ZONE 'UTC')) AS segment_lower,
                   least(activation.upper_at,
                       ((generated_at::date + 1)::timestamp AT TIME ZONE 'UTC')) AS segment_upper
        ) AS candidate
        WHERE to_timestamp(ceil(extract(epoch FROM candidate.segment_lower) / 300) * 300)
                < candidate.segment_upper
          AND ((candidate.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
                > timing.retention_cutoff
          AND NOT EXISTS (
              SELECT 1 FROM public.account_inventory_history_retired_days AS retired
              WHERE retired.summary_date = candidate.summary_date
                AND retired.instance_id = activation.instance_id
          )
          AND NOT EXISTS (
              SELECT 1 FROM public.account_inventory_compaction_runs AS existing
              WHERE existing.summary_date = candidate.summary_date
                AND existing.instance_id = activation.instance_id
                AND existing.provider_policy_version = activation.policy_version_id
          )
        UNION ALL
        SELECT ((run.summary_date + 1)::timestamp AT TIME ZONE 'UTC') AS day_end
        FROM public.account_inventory_daily_rollup_runs AS run, timing
        WHERE run.status <> 'completed'
          AND ((run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
                <= timing.database_now - interval '72 hours'
    ),
    eligible_compactions AS (
        SELECT run.*
        FROM public.account_inventory_compaction_runs AS run, timing
        WHERE run.status = 'completed'
          AND run.completed_at <= timing.retention_cutoff
          AND ((run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
                <= timing.retention_cutoff
    ),
    eligible_rollups AS (
        SELECT run.*
        FROM public.account_inventory_daily_rollup_runs AS run, timing
        WHERE run.status = 'completed'
          AND run.completed_at <= timing.retention_cutoff
          AND ((run.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
                <= timing.retention_cutoff
    ),
    eligible_polls AS (
        SELECT poll.poll_run_id
        FROM public.account_inventory_poll_runs AS poll
        JOIN eligible_compactions AS compaction
          ON compaction.instance_id = poll.instance_id
         AND compaction.provider_policy_version = poll.provider_policy_version
         AND poll.scheduled_at >=
                (compaction.summary_date::timestamp AT TIME ZONE 'UTC')
         AND poll.scheduled_at <
                ((compaction.summary_date + 1)::timestamp AT TIME ZONE 'UTC')
        JOIN eligible_rollups AS rollup
          ON rollup.instance_id = compaction.instance_id
         AND rollup.summary_date = compaction.summary_date
    ),
    unfinished_source_polls AS (
        SELECT poll.poll_run_id
        FROM public.account_inventory_poll_runs AS poll, timing
        WHERE (((poll.scheduled_at AT TIME ZONE 'UTC')::date + 1)::timestamp
                    AT TIME ZONE 'UTC')
                <= timing.database_now - interval '72 hours'
          AND NOT EXISTS (
              SELECT 1 FROM public.account_inventory_compaction_runs AS completed
              WHERE completed.summary_date = (poll.scheduled_at AT TIME ZONE 'UTC')::date
                AND completed.instance_id = poll.instance_id
                AND completed.provider_policy_version = poll.provider_policy_version
                AND completed.status = 'completed'
          )
    ),
    active_snapshot_backlog AS (
        SELECT count(*) AS row_count
        FROM public.account_inventory_snapshot_items AS item
        JOIN unfinished_source_polls AS poll ON poll.poll_run_id = item.poll_run_id
    ),
    retention_backlog AS (
        SELECT
            (SELECT count(*) FROM eligible_polls)
          + (SELECT count(*) FROM public.account_inventory_poll_provider_results AS result
             JOIN eligible_polls AS poll ON poll.poll_run_id = result.poll_run_id)
          + (SELECT count(*) FROM public.account_inventory_poll_duplicates AS duplicate_row
             JOIN eligible_polls AS poll ON poll.poll_run_id = duplicate_row.poll_run_id)
          + (SELECT count(*) FROM public.account_inventory_daily_summaries AS row
             JOIN eligible_rollups AS rollup
               ON rollup.summary_date = row.summary_date
              AND rollup.instance_id = row.instance_id)
          + (SELECT count(*) FROM public.account_inventory_daily_provider_summaries AS row
             JOIN eligible_rollups AS rollup
               ON rollup.summary_date = row.summary_date
              AND rollup.instance_id = row.instance_id)
          + (SELECT count(*) FROM public.account_inventory_daily_account_rollups AS row
             JOIN eligible_rollups AS rollup ON rollup.rollup_run_id = row.rollup_run_id)
          + (SELECT count(*) FROM public.account_inventory_daily_provider_rollups AS row
             JOIN eligible_rollups AS rollup ON rollup.rollup_run_id = row.rollup_run_id)
          + (SELECT count(*) FROM eligible_rollups)
          + (SELECT count(*) FROM eligible_compactions) AS row_count
    )
    SELECT jsonb_build_object(
        'compaction_runs', jsonb_build_object(
            'pending', count(*) FILTER (WHERE compaction.status = 'pending'),
            'summarized', count(*) FILTER (WHERE compaction.status = 'summarized'),
            'deleting', count(*) FILTER (WHERE compaction.status = 'deleting'),
            'completed', count(*) FILTER (WHERE compaction.status = 'completed'),
            'failed', count(*) FILTER (WHERE compaction.status = 'failed')
        ),
        'rollup_runs', jsonb_build_object(
            'pending', (SELECT count(*) FROM public.account_inventory_daily_rollup_runs WHERE status = 'pending'),
            'completed', (SELECT count(*) FROM public.account_inventory_daily_rollup_runs WHERE status = 'completed'),
            'failed', (SELECT count(*) FROM public.account_inventory_daily_rollup_runs WHERE status = 'failed')
        ),
        'oldest_eligible_unfinished_seconds', coalesce((
            SELECT greatest(0::numeric,
                            max(extract(epoch FROM timing.database_now - day.day_end)))
            FROM unfinished_days AS day, timing
        ), 0::numeric),
        'failures', jsonb_build_object(
            'pending', count(*) FILTER (WHERE compaction.status = 'failed' AND compaction.failed_from = 'pending'),
            'summarized', count(*) FILTER (WHERE compaction.status = 'failed' AND compaction.failed_from = 'summarized'),
            'deleting', count(*) FILTER (WHERE compaction.status = 'failed' AND compaction.failed_from = 'deleting')
        ),
        'delete_backlog_rows',
            (SELECT row_count FROM active_snapshot_backlog)
            + (SELECT row_count FROM retention_backlog)
    )
    FROM public.account_inventory_compaction_runs AS compaction
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_history_schema_compatibility_v1()
RETURNS jsonb
LANGUAGE plpgsql
SECURITY DEFINER
STABLE
SET search_path = pg_catalog
SET TimeZone = 'UTC'
AS $$
DECLARE
    required_tables text[] := ARRAY[
        'account_inventory_compaction_runs',
        'account_inventory_daily_rollup_runs',
        'account_inventory_history_retired_days',
        'account_inventory_daily_summaries',
        'account_inventory_daily_provider_summaries',
        'account_inventory_daily_account_rollups',
        'account_inventory_daily_provider_rollups'
    ];
    exposed_functions text[] := ARRAY[
        'public.control_plan_account_inventory_history_v1(integer)',
        'public.control_claim_account_inventory_compaction_v1(uuid,integer)',
        'public.control_renew_account_inventory_compaction_v1(uuid,uuid,integer)',
        'public.control_reconcile_account_inventory_compactions_v1(integer)',
        'public.control_summarize_account_inventory_compaction_v1(uuid,uuid)',
        'public.control_delete_account_inventory_snapshot_batch_v1(uuid,uuid,integer)',
        'public.control_complete_account_inventory_compaction_v1(uuid,uuid,bytea)',
        'public.control_fail_account_inventory_compaction_v1(uuid,uuid,text)',
        'public.control_claim_account_inventory_daily_rollup_v1(uuid,integer)',
        'public.control_renew_account_inventory_daily_rollup_v1(uuid,uuid,integer)',
        'public.control_reconcile_account_inventory_daily_rollups_v1(integer)',
        'public.control_finalize_account_inventory_daily_rollup_v1(uuid,uuid)',
        'public.control_fail_account_inventory_daily_rollup_v1(uuid,uuid,text)',
        'public.control_delete_account_inventory_poll_retention_v1(integer)',
        'public.control_delete_account_inventory_rollup_row_retention_v1(integer)',
        'public.control_delete_account_inventory_rollup_run_retention_v1(integer)',
        'public.control_delete_account_inventory_compaction_run_retention_v1(integer)',
        'public.control_list_account_inventory_history_metrics_v1()',
        'public.control_account_inventory_history_metrics_snapshot_v1()',
        'public.control_history_schema_compatibility_v1()',
        'public.control_query_current_account_inventory_v1(uuid,text,text,text,text,text,integer)'
    ];
    secured_functions text[] := ARRAY[
        'public.control_reject_account_inventory_retired_day_poll()',
        'public.control_refresh_account_inventory_provider_health_v1()',
        'public.control_plan_account_inventory_history_v1(integer)',
        'public.control_claim_account_inventory_compaction_v1(uuid,integer)',
        'public.control_renew_account_inventory_compaction_v1(uuid,uuid,integer)',
        'public.control_reconcile_account_inventory_compactions_v1(integer)',
        'public.control_summarize_account_inventory_compaction_v1(uuid,uuid)',
        'public.control_delete_account_inventory_snapshot_batch_v1(uuid,uuid,integer)',
        'public.control_complete_account_inventory_compaction_v1(uuid,uuid,bytea)',
        'public.control_fail_account_inventory_compaction_v1(uuid,uuid,text)',
        'public.control_claim_account_inventory_daily_rollup_v1(uuid,integer)',
        'public.control_renew_account_inventory_daily_rollup_v1(uuid,uuid,integer)',
        'public.control_reconcile_account_inventory_daily_rollups_v1(integer)',
        'public.control_finalize_account_inventory_daily_rollup_v1(uuid,uuid)',
        'public.control_fail_account_inventory_daily_rollup_v1(uuid,uuid,text)',
        'public.control_delete_account_inventory_poll_retention_v1(integer)',
        'public.control_delete_account_inventory_rollup_row_retention_v1(integer)',
        'public.control_delete_account_inventory_rollup_run_retention_v1(integer)',
        'public.control_delete_account_inventory_compaction_run_retention_v1(integer)',
        'public.control_list_account_inventory_history_metrics_v1()',
        'public.control_account_inventory_history_metrics_snapshot_v1()',
        'public.control_history_schema_compatibility_v1()',
        'public.control_query_current_account_inventory_v1(uuid,text,text,text,text,text,integer)'
    ];
BEGIN
    IF current_setting('TimeZone') <> 'UTC'
       OR octet_length(sha256(''::bytea)) <> 32
       OR EXISTS (
           SELECT 1 FROM unnest(required_tables) AS expected(table_name)
           LEFT JOIN pg_catalog.pg_namespace AS namespace
             ON namespace.nspname = 'public'
           LEFT JOIN pg_catalog.pg_class AS relation
             ON relation.relnamespace = namespace.oid
            AND relation.relname = expected.table_name AND relation.relkind = 'r'
           WHERE relation.oid IS NULL
              OR pg_catalog.pg_get_userbyid(relation.relowner) <> 'relay_control_migrator'
       )
       OR EXISTS (
           SELECT 1
           FROM (VALUES
               ('account_inventory_compaction_runs','status'),
               ('account_inventory_compaction_runs','failed_from'),
               ('account_inventory_compaction_runs','source_checksum'),
               ('account_inventory_compaction_runs','deleted_snapshot_count'),
               ('account_inventory_compaction_runs','deleted_poll_count'),
               ('account_inventory_compaction_runs','deleted_provider_result_count'),
               ('account_inventory_compaction_runs','deleted_duplicate_count'),
               ('account_inventory_daily_rollup_runs','expected_segment_count'),
               ('account_inventory_daily_rollup_runs','segment_checksum'),
               ('account_inventory_daily_rollup_runs','completed_fencing_token'),
               ('account_inventory_history_retired_days','retired_at'),
               ('account_inventory_daily_summaries','account_key'),
               ('account_inventory_daily_provider_summaries','policy_changed_count'),
               ('account_inventory_daily_provider_summaries','coverage_threshold_basis_points'),
               ('account_inventory_daily_account_rollups','account_key'),
               ('account_inventory_daily_provider_rollups','policy_changed_count'),
               ('account_inventory_daily_provider_rollups','coverage_threshold_basis_points'),
               ('account_inventory_provider_states','health_scheduled_at'),
               ('account_inventory_provider_states','health_degraded'),
               ('account_inventory_provider_states','health_reason')
           ) AS expected(table_name, column_name)
           LEFT JOIN pg_catalog.pg_namespace AS namespace ON namespace.nspname = 'public'
           LEFT JOIN pg_catalog.pg_class AS relation
             ON relation.relnamespace = namespace.oid AND relation.relname = expected.table_name
           LEFT JOIN pg_catalog.pg_attribute AS attribute
             ON attribute.attrelid = relation.oid AND attribute.attname = expected.column_name
            AND attribute.attnum > 0 AND NOT attribute.attisdropped
           WHERE attribute.attnum IS NULL
       )
       OR EXISTS (
           SELECT 1
           FROM (VALUES
               ('account_inventory_compaction_runs','account_inventory_compaction_state_shape'),
               ('account_inventory_compaction_runs','account_inventory_compaction_times_ordered'),
               ('account_inventory_compaction_runs','account_inventory_compaction_claim_shape'),
               ('account_inventory_daily_rollup_runs','account_inventory_rollup_run_state_shape'),
               ('account_inventory_daily_rollup_runs','account_inventory_rollup_run_times_ordered'),
               ('account_inventory_history_retired_days','account_inventory_history_retired_day_age'),
               ('account_inventory_daily_summaries','account_inventory_daily_summary_samples_consistent'),
               ('account_inventory_daily_provider_summaries','account_inventory_provider_summary_counts'),
               ('account_inventory_daily_provider_summaries','account_inventory_provider_summary_coverage'),
               ('account_inventory_daily_account_rollups','account_inventory_account_rollup_samples_consistent'),
               ('account_inventory_daily_provider_rollups','account_inventory_provider_rollup_counts'),
               ('account_inventory_daily_provider_rollups','account_inventory_provider_rollup_coverage'),
               ('account_inventory_provider_states','account_inventory_provider_health_slot_aligned'),
               ('account_inventory_provider_states','account_inventory_provider_health_reason_fixed')
              ,('audit_logs','audit_logs_history_shape')
           ) AS expected(table_name, constraint_name)
           LEFT JOIN pg_catalog.pg_namespace AS namespace ON namespace.nspname = 'public'
           LEFT JOIN pg_catalog.pg_class AS relation
             ON relation.relnamespace = namespace.oid AND relation.relname = expected.table_name
           LEFT JOIN pg_catalog.pg_constraint AS constraint_row
             ON constraint_row.conrelid = relation.oid
            AND constraint_row.conname = expected.constraint_name
           WHERE constraint_row.oid IS NULL
       )
       OR EXISTS (
           SELECT 1
           FROM (VALUES
               ('account_inventory_compaction_runs_guard',
                'account_inventory_compaction_runs','control_protect_account_inventory_history_run'),
               ('account_inventory_compaction_runs_truncate_guard',
                'account_inventory_compaction_runs','control_protect_account_inventory_history_run'),
               ('account_inventory_daily_rollup_runs_guard',
                'account_inventory_daily_rollup_runs','control_protect_account_inventory_history_run'),
               ('account_inventory_daily_rollup_runs_truncate_guard',
                'account_inventory_daily_rollup_runs','control_protect_account_inventory_history_run'),
               ('account_inventory_history_retired_days_guard',
                'account_inventory_history_retired_days','control_protect_account_inventory_history_retired_day'),
               ('account_inventory_history_retired_days_truncate_guard',
                'account_inventory_history_retired_days','control_protect_account_inventory_history_retired_day'),
               ('account_inventory_poll_runs_retired_day_guard',
                'account_inventory_poll_runs','control_reject_account_inventory_retired_day_poll'),
               ('account_inventory_daily_summaries_immutable',
                'account_inventory_daily_summaries','control_reject_account_inventory_history_row_mutation'),
               ('account_inventory_daily_summaries_truncate_immutable',
                'account_inventory_daily_summaries','control_reject_account_inventory_history_row_mutation'),
               ('account_inventory_daily_provider_summaries_immutable',
                'account_inventory_daily_provider_summaries','control_reject_account_inventory_history_row_mutation'),
               ('account_inventory_daily_provider_summaries_truncate_immutable',
                'account_inventory_daily_provider_summaries','control_reject_account_inventory_history_row_mutation'),
               ('account_inventory_daily_account_rollups_immutable',
                'account_inventory_daily_account_rollups','control_reject_account_inventory_history_row_mutation'),
               ('account_inventory_daily_account_rollups_truncate_immutable',
                'account_inventory_daily_account_rollups','control_reject_account_inventory_history_row_mutation'),
               ('account_inventory_daily_provider_rollups_immutable',
                'account_inventory_daily_provider_rollups','control_reject_account_inventory_history_row_mutation'),
               ('account_inventory_daily_provider_rollups_truncate_immutable',
                'account_inventory_daily_provider_rollups','control_reject_account_inventory_history_row_mutation'),
               ('account_inventory_snapshot_items_immutable',
                'account_inventory_snapshot_items','control_reject_account_inventory_snapshot_mutation'),
               ('account_inventory_snapshot_items_truncate_immutable',
                'account_inventory_snapshot_items','control_reject_account_inventory_snapshot_mutation'),
               ('account_inventory_poll_runs_refresh_provider_health_v1',
                'account_inventory_poll_runs','control_refresh_account_inventory_provider_health_v1'),
               ('account_inventory_provider_states_guard',
                'account_inventory_provider_states','control_protect_account_inventory_provider_state'),
               ('audit_logs_account_inventory_history_guard',
                'audit_logs','control_protect_account_inventory_history_audit')
           ) AS expected(trigger_name, table_name, function_name)
           LEFT JOIN pg_catalog.pg_namespace AS namespace
             ON namespace.nspname = 'public'
           LEFT JOIN pg_catalog.pg_class AS relation
             ON relation.relnamespace = namespace.oid
            AND relation.relname = expected.table_name
           LEFT JOIN pg_catalog.pg_proc AS trigger_function
             ON trigger_function.pronamespace = namespace.oid
            AND trigger_function.proname = expected.function_name
           LEFT JOIN pg_catalog.pg_trigger AS trigger_row
             ON trigger_row.tgname = expected.trigger_name
            AND trigger_row.tgrelid = relation.oid
            AND trigger_row.tgfoid = trigger_function.oid
            AND NOT trigger_row.tgisinternal AND trigger_row.tgenabled = 'O'
           WHERE trigger_row.oid IS NULL
              OR pg_catalog.pg_get_userbyid(trigger_function.proowner)
                    <> 'relay_control_migrator'
       )
       OR EXISTS (
           SELECT 1
           FROM pg_catalog.pg_namespace AS namespace
           JOIN pg_catalog.pg_class AS relation
             ON relation.relnamespace = namespace.oid
           JOIN pg_catalog.pg_index AS index_row
             ON index_row.indrelid = relation.oid
           JOIN pg_catalog.pg_class AS index_relation
             ON index_relation.oid = index_row.indexrelid
           WHERE namespace.nspname = 'public'
             AND relation.relname = 'account_inventory_daily_rollup_runs'
             AND index_relation.relname = 'account_inventory_daily_rollup_runs_claim_idx'
             AND NOT (
                 pg_catalog.strpos(
                     pg_catalog.pg_get_expr(index_row.indpred, index_row.indrelid),
                     '''pending''') > 0
                 AND pg_catalog.strpos(
                     pg_catalog.pg_get_expr(index_row.indpred, index_row.indrelid),
                     '''failed''') > 0
             )
       )
       OR NOT EXISTS (
           SELECT 1
           FROM pg_catalog.pg_namespace AS namespace
           JOIN pg_catalog.pg_class AS relation
             ON relation.relnamespace = namespace.oid
           JOIN pg_catalog.pg_index AS index_row
             ON index_row.indrelid = relation.oid
           JOIN pg_catalog.pg_class AS index_relation
             ON index_relation.oid = index_row.indexrelid
           WHERE namespace.nspname = 'public'
             AND relation.relname = 'account_inventory_daily_rollup_runs'
             AND index_relation.relname = 'account_inventory_daily_rollup_runs_claim_idx'
             AND index_row.indisvalid AND index_row.indisready
       )
       OR EXISTS (
           SELECT 1 FROM unnest(secured_functions) AS expected(signature)
           LEFT JOIN pg_catalog.pg_proc AS procedure_row
             ON procedure_row.oid = to_regprocedure(expected.signature)
           WHERE procedure_row.oid IS NULL
              OR NOT procedure_row.prosecdef
              OR pg_catalog.pg_get_userbyid(procedure_row.proowner) <> 'relay_control_migrator'
              OR NOT coalesce(procedure_row.proconfig, ARRAY[]::text[])
                    @> ARRAY['search_path=pg_catalog','TimeZone=UTC']
       )
       OR EXISTS (
           SELECT 1
           FROM (VALUES
               ('public.control_plan_account_inventory_history_v1(integer)','jsonb'::regtype,false),
               ('public.control_claim_account_inventory_compaction_v1(uuid,integer)',
                'public.account_inventory_compaction_runs'::regtype,true),
               ('public.control_renew_account_inventory_compaction_v1(uuid,uuid,integer)',
                'public.account_inventory_compaction_runs'::regtype,true),
               ('public.control_reconcile_account_inventory_compactions_v1(integer)','jsonb'::regtype,false),
               ('public.control_summarize_account_inventory_compaction_v1(uuid,uuid)','jsonb'::regtype,false),
               ('public.control_delete_account_inventory_snapshot_batch_v1(uuid,uuid,integer)','jsonb'::regtype,false),
               ('public.control_complete_account_inventory_compaction_v1(uuid,uuid,bytea)','jsonb'::regtype,false),
               ('public.control_fail_account_inventory_compaction_v1(uuid,uuid,text)','jsonb'::regtype,false),
               ('public.control_claim_account_inventory_daily_rollup_v1(uuid,integer)',
                'public.account_inventory_daily_rollup_runs'::regtype,true),
               ('public.control_renew_account_inventory_daily_rollup_v1(uuid,uuid,integer)',
                'public.account_inventory_daily_rollup_runs'::regtype,true),
               ('public.control_reconcile_account_inventory_daily_rollups_v1(integer)','jsonb'::regtype,false),
               ('public.control_finalize_account_inventory_daily_rollup_v1(uuid,uuid)','jsonb'::regtype,false),
               ('public.control_fail_account_inventory_daily_rollup_v1(uuid,uuid,text)','jsonb'::regtype,false),
               ('public.control_delete_account_inventory_poll_retention_v1(integer)','jsonb'::regtype,false),
               ('public.control_delete_account_inventory_rollup_row_retention_v1(integer)','jsonb'::regtype,false),
               ('public.control_delete_account_inventory_rollup_run_retention_v1(integer)','jsonb'::regtype,false),
               ('public.control_delete_account_inventory_compaction_run_retention_v1(integer)','jsonb'::regtype,false),
               ('public.control_list_account_inventory_history_metrics_v1()','record'::regtype,true),
               ('public.control_account_inventory_history_metrics_snapshot_v1()','jsonb'::regtype,false),
               ('public.control_history_schema_compatibility_v1()','jsonb'::regtype,false),
               ('public.control_query_current_account_inventory_v1(uuid,text,text,text,text,text,integer)',
                'record'::regtype,true)
           ) AS expected(signature, return_type, returns_set)
           LEFT JOIN pg_catalog.pg_proc AS procedure_row
             ON procedure_row.oid = to_regprocedure(expected.signature)
           WHERE procedure_row.oid IS NULL
              OR procedure_row.prorettype <> expected.return_type
              OR procedure_row.proretset <> expected.returns_set
       )
       OR EXISTS (
           SELECT 1 FROM unnest(exposed_functions) AS expected(signature)
           LEFT JOIN pg_catalog.pg_proc AS exposed
             ON exposed.oid = to_regprocedure(expected.signature)
           WHERE exposed.oid IS NULL
              OR NOT has_function_privilege(
                    'relay_control_runtime', exposed.oid, 'EXECUTE')
              OR exposed.proacl IS NULL
              OR exposed.proacl::text ~ '[{,]=X/'
              OR has_function_privilege(
                    'relay_control_asset_registrar', exposed.oid, 'EXECUTE')
       )
       OR EXISTS (
           SELECT 1
           FROM unnest(required_tables) AS expected(table_name)
           CROSS JOIN unnest(ARRAY[
               'SELECT','INSERT','UPDATE','DELETE','TRUNCATE','REFERENCES','TRIGGER'
           ]) AS privilege(privilege_name)
           WHERE has_table_privilege(
               'relay_control_runtime', 'public.' || expected.table_name,
               privilege.privilege_name
           ) OR has_table_privilege(
               'relay_control_asset_registrar', 'public.' || expected.table_name,
               privilege.privilege_name
           )
       )
       OR has_function_privilege(
            'relay_control_runtime',
            'public.control_history_canonical_row_v1(bytea[])', 'EXECUTE')
       OR has_function_privilege(
            'relay_control_runtime',
            'public.control_history_checksum_chain_v1(bytea[])', 'EXECUTE')
       OR has_function_privilege(
            'relay_control_runtime',
            'public.control_history_bytea_equal_v1(bytea,bytea)', 'EXECUTE')
       OR has_function_privilege(
            'relay_control_runtime',
            'public.control_history_time_text_v1(timestamp with time zone)', 'EXECUTE')
       OR to_regprocedure('public.control_history_bytea_equal_v1(bytea,bytea)') IS NULL
       OR (SELECT pg_catalog.pg_get_userbyid(proowner) <> 'relay_control_migrator'
                  OR prosecdef
                  OR proacl IS NULL OR proacl::text ~ '[{,]=X/'
                  OR NOT coalesce(proconfig, ARRAY[]::text[])
                         @> ARRAY['search_path=pg_catalog']
           FROM pg_catalog.pg_proc
           WHERE oid = to_regprocedure(
               'public.control_history_bytea_equal_v1(bytea,bytea)'))
       OR to_regprocedure(
            'public.control_history_time_text_v1(timestamp with time zone)') IS NULL
       OR (SELECT pg_catalog.pg_get_userbyid(proowner) <> 'relay_control_migrator'
                  OR prosecdef
                  OR proacl IS NULL OR proacl::text ~ '[{,]=X/'
                  OR NOT coalesce(proconfig, ARRAY[]::text[])
                         @> ARRAY['search_path=pg_catalog']
           FROM pg_catalog.pg_proc
           WHERE oid = to_regprocedure(
               'public.control_history_time_text_v1(timestamp with time zone)'))
       OR pg_has_role('relay_control_runtime', 'relay_control_migrator', 'member') THEN
        RAISE EXCEPTION 'account inventory history schema is incompatible'
            USING ERRCODE = '55000';
    END IF;

    RETURN jsonb_build_object(
        'schema_version', 1,
        'history_table_count', cardinality(required_tables),
        'core_sha256', true,
        'coverage_threshold_basis_points', 9500,
        'snapshot_minimum_age_hours', 72,
        'history_retention_days', 30
    );
END;
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_history_canonical_row_v1(VARIADIC bytea[])
    OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_history_checksum_chain_v1(bytea[])
    OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_history_bytea_equal_v1(bytea, bytea)
    OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_history_time_text_v1(timestamptz)
    OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_protect_account_inventory_history_audit()
    OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_protect_account_inventory_history_retired_day()
    OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_reject_account_inventory_retired_day_poll()
    OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_plan_account_inventory_history_v1(integer)
    OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_claim_account_inventory_compaction_v1(uuid, integer)
    OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_renew_account_inventory_compaction_v1(uuid, uuid, integer)
    OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_reconcile_account_inventory_compactions_v1(integer)
    OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_summarize_account_inventory_compaction_v1(uuid, uuid)
    OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_delete_account_inventory_snapshot_batch_v1(uuid, uuid, integer)
    OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_complete_account_inventory_compaction_v1(uuid, uuid, bytea)
    OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_fail_account_inventory_compaction_v1(uuid, uuid, text)
    OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_claim_account_inventory_daily_rollup_v1(uuid, integer)
    OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_renew_account_inventory_daily_rollup_v1(uuid, uuid, integer)
    OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_reconcile_account_inventory_daily_rollups_v1(integer)
    OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_finalize_account_inventory_daily_rollup_v1(uuid, uuid)
    OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_fail_account_inventory_daily_rollup_v1(uuid, uuid, text)
    OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_delete_account_inventory_poll_retention_v1(integer)
    OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_delete_account_inventory_rollup_row_retention_v1(integer)
    OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_delete_account_inventory_rollup_run_retention_v1(integer)
    OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_delete_account_inventory_compaction_run_retention_v1(integer)
    OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_list_account_inventory_history_metrics_v1()
    OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_account_inventory_history_metrics_snapshot_v1()
    OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_history_schema_compatibility_v1()
    OWNER TO relay_control_migrator;

REVOKE ALL ON TABLE account_inventory_compaction_runs,
    account_inventory_daily_rollup_runs,
    account_inventory_history_retired_days,
    account_inventory_daily_summaries,
    account_inventory_daily_provider_summaries,
    account_inventory_daily_account_rollups,
    account_inventory_daily_provider_rollups
FROM PUBLIC, relay_control_runtime, relay_control_asset_registrar;

REVOKE EXECUTE ON FUNCTION public.control_history_canonical_row_v1(VARIADIC bytea[]),
    public.control_history_checksum_chain_v1(bytea[]),
    public.control_history_bytea_equal_v1(bytea, bytea),
    public.control_history_time_text_v1(timestamptz),
    public.control_protect_account_inventory_history_audit(),
    public.control_protect_account_inventory_history_retired_day(),
    public.control_reject_account_inventory_retired_day_poll(),
    public.control_reject_account_inventory_history_row_mutation(),
    public.control_protect_account_inventory_history_run(),
    public.control_refresh_account_inventory_provider_health_v1(),
    public.control_plan_account_inventory_history_v1(integer),
    public.control_claim_account_inventory_compaction_v1(uuid, integer),
    public.control_renew_account_inventory_compaction_v1(uuid, uuid, integer),
    public.control_reconcile_account_inventory_compactions_v1(integer),
    public.control_summarize_account_inventory_compaction_v1(uuid, uuid),
    public.control_delete_account_inventory_snapshot_batch_v1(uuid, uuid, integer),
    public.control_complete_account_inventory_compaction_v1(uuid, uuid, bytea),
    public.control_fail_account_inventory_compaction_v1(uuid, uuid, text),
    public.control_claim_account_inventory_daily_rollup_v1(uuid, integer),
    public.control_renew_account_inventory_daily_rollup_v1(uuid, uuid, integer),
    public.control_reconcile_account_inventory_daily_rollups_v1(integer),
    public.control_finalize_account_inventory_daily_rollup_v1(uuid, uuid),
    public.control_fail_account_inventory_daily_rollup_v1(uuid, uuid, text),
    public.control_delete_account_inventory_poll_retention_v1(integer),
    public.control_delete_account_inventory_rollup_row_retention_v1(integer),
    public.control_delete_account_inventory_rollup_run_retention_v1(integer),
    public.control_delete_account_inventory_compaction_run_retention_v1(integer),
    public.control_list_account_inventory_history_metrics_v1(),
    public.control_account_inventory_history_metrics_snapshot_v1(),
    public.control_history_schema_compatibility_v1()
FROM PUBLIC;

GRANT EXECUTE ON FUNCTION public.control_plan_account_inventory_history_v1(integer),
    public.control_claim_account_inventory_compaction_v1(uuid, integer),
    public.control_renew_account_inventory_compaction_v1(uuid, uuid, integer),
    public.control_reconcile_account_inventory_compactions_v1(integer),
    public.control_summarize_account_inventory_compaction_v1(uuid, uuid),
    public.control_delete_account_inventory_snapshot_batch_v1(uuid, uuid, integer),
    public.control_complete_account_inventory_compaction_v1(uuid, uuid, bytea),
    public.control_fail_account_inventory_compaction_v1(uuid, uuid, text),
    public.control_claim_account_inventory_daily_rollup_v1(uuid, integer),
    public.control_renew_account_inventory_daily_rollup_v1(uuid, uuid, integer),
    public.control_reconcile_account_inventory_daily_rollups_v1(integer),
    public.control_finalize_account_inventory_daily_rollup_v1(uuid, uuid),
    public.control_fail_account_inventory_daily_rollup_v1(uuid, uuid, text),
    public.control_delete_account_inventory_poll_retention_v1(integer),
    public.control_delete_account_inventory_rollup_row_retention_v1(integer),
    public.control_delete_account_inventory_rollup_run_retention_v1(integer),
    public.control_delete_account_inventory_compaction_run_retention_v1(integer),
    public.control_list_account_inventory_history_metrics_v1(),
    public.control_account_inventory_history_metrics_snapshot_v1(),
    public.control_history_schema_compatibility_v1()
TO relay_control_runtime;

REVOKE EXECUTE ON FUNCTION public.control_query_current_account_inventory_v1(
    uuid, text, text, text, text, text, integer
) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_query_current_account_inventory_v1(
    uuid, text, text, text, text, text, integer
) TO relay_control_runtime;

-- +goose Down
-- +goose StatementBegin
DO $$
DECLARE history_rows bigint;
BEGIN
    LOCK TABLE account_inventory_compaction_runs,
        account_inventory_daily_rollup_runs,
        account_inventory_history_retired_days,
        account_inventory_daily_summaries,
        account_inventory_daily_provider_summaries,
        account_inventory_daily_account_rollups,
        account_inventory_daily_provider_rollups
        IN ACCESS EXCLUSIVE MODE NOWAIT;
    SELECT (SELECT count(*) FROM account_inventory_compaction_runs)
         + (SELECT count(*) FROM account_inventory_daily_rollup_runs)
         + (SELECT count(*) FROM account_inventory_history_retired_days)
         + (SELECT count(*) FROM account_inventory_daily_summaries)
         + (SELECT count(*) FROM account_inventory_daily_provider_summaries)
         + (SELECT count(*) FROM account_inventory_daily_account_rollups)
         + (SELECT count(*) FROM account_inventory_daily_provider_rollups)
         + (SELECT count(*) FROM audit_logs
            WHERE category = 'account_inventory_history')
         + (SELECT count(*) FROM account_inventory_provider_states
            WHERE current_poll_run_id IS NULL)
         + (SELECT count(*) FROM account_inventory
            WHERE current_poll_run_id IS NULL)
    INTO history_rows;
    IF history_rows <> 0 THEN
        RAISE EXCEPTION 'account inventory history migration down requires empty history state'
            USING ERRCODE = '55000';
    END IF;
END;
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION public.control_plan_account_inventory_history_v1(integer),
    public.control_claim_account_inventory_compaction_v1(uuid, integer),
    public.control_renew_account_inventory_compaction_v1(uuid, uuid, integer),
    public.control_reconcile_account_inventory_compactions_v1(integer),
    public.control_summarize_account_inventory_compaction_v1(uuid, uuid),
    public.control_delete_account_inventory_snapshot_batch_v1(uuid, uuid, integer),
    public.control_complete_account_inventory_compaction_v1(uuid, uuid, bytea),
    public.control_fail_account_inventory_compaction_v1(uuid, uuid, text),
    public.control_claim_account_inventory_daily_rollup_v1(uuid, integer),
    public.control_renew_account_inventory_daily_rollup_v1(uuid, uuid, integer),
    public.control_reconcile_account_inventory_daily_rollups_v1(integer),
    public.control_finalize_account_inventory_daily_rollup_v1(uuid, uuid),
    public.control_fail_account_inventory_daily_rollup_v1(uuid, uuid, text),
    public.control_delete_account_inventory_poll_retention_v1(integer),
    public.control_delete_account_inventory_rollup_row_retention_v1(integer),
    public.control_delete_account_inventory_rollup_run_retention_v1(integer),
    public.control_delete_account_inventory_compaction_run_retention_v1(integer),
    public.control_list_account_inventory_history_metrics_v1(),
    public.control_account_inventory_history_metrics_snapshot_v1(),
    public.control_history_schema_compatibility_v1()
FROM relay_control_runtime;

DROP FUNCTION public.control_history_schema_compatibility_v1();
DROP FUNCTION public.control_account_inventory_history_metrics_snapshot_v1();
DROP FUNCTION public.control_list_account_inventory_history_metrics_v1();
DROP FUNCTION public.control_delete_account_inventory_compaction_run_retention_v1(integer);
DROP FUNCTION public.control_delete_account_inventory_rollup_run_retention_v1(integer);
DROP FUNCTION public.control_delete_account_inventory_rollup_row_retention_v1(integer);
DROP FUNCTION public.control_delete_account_inventory_poll_retention_v1(integer);
DROP FUNCTION public.control_finalize_account_inventory_daily_rollup_v1(uuid, uuid);
DROP FUNCTION public.control_fail_account_inventory_daily_rollup_v1(uuid, uuid, text);
DROP FUNCTION public.control_reconcile_account_inventory_daily_rollups_v1(integer);
DROP FUNCTION public.control_renew_account_inventory_daily_rollup_v1(uuid, uuid, integer);
DROP FUNCTION public.control_claim_account_inventory_daily_rollup_v1(uuid, integer);
DROP FUNCTION public.control_complete_account_inventory_compaction_v1(uuid, uuid, bytea);
DROP FUNCTION public.control_delete_account_inventory_snapshot_batch_v1(uuid, uuid, integer);
DROP FUNCTION public.control_summarize_account_inventory_compaction_v1(uuid, uuid);
DROP FUNCTION public.control_fail_account_inventory_compaction_v1(uuid, uuid, text);
DROP FUNCTION public.control_reconcile_account_inventory_compactions_v1(integer);
DROP FUNCTION public.control_renew_account_inventory_compaction_v1(uuid, uuid, integer);
DROP FUNCTION public.control_claim_account_inventory_compaction_v1(uuid, integer);
DROP FUNCTION public.control_plan_account_inventory_history_v1(integer);

DROP TRIGGER audit_logs_account_inventory_history_guard ON audit_logs;
DROP FUNCTION public.control_protect_account_inventory_history_audit();
ALTER TABLE audit_logs DROP CONSTRAINT audit_logs_history_shape;
ALTER TABLE audit_logs DROP CONSTRAINT audit_logs_action_valid;
ALTER TABLE audit_logs ADD CONSTRAINT audit_logs_action_valid CHECK (
    action IN (
        'bootstrap.start', 'bootstrap.complete', 'bootstrap.reset',
        'auth.login_password', 'auth.login_mfa', 'auth.logout',
        'auth.password_change', 'auth.mfa_enroll', 'auth.mfa_reset',
        'auth.recovery_code_use', 'auth.recovery_codes_regenerate',
        'session.create', 'session.revoke', 'auth.reauthenticate',
        'administrator.create', 'administrator.activate', 'administrator.disable',
        'administrator.activation_token_generate', 'authorization.check',
        'auth.rate_limit', 'auth.csrf', 'account_inventory.view'
    )
);
ALTER TABLE audit_logs DROP CONSTRAINT audit_logs_category_valid;
ALTER TABLE audit_logs ADD CONSTRAINT audit_logs_category_valid CHECK (
    category IN ('bootstrap', 'administrator', 'password', 'mfa', 'session',
                 'reauthentication', 'authorization', 'rate_limit', 'account_inventory')
);

-- Restore Migration 5's unconditional terminal poll/provider immutability.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_protect_account_inventory_poll_run()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE
    expected_providers text[];
    stored_providers text[];
BEGIN
    IF TG_OP = 'DELETE' OR OLD.status IN ('finalized', 'abandoned') THEN
        RAISE EXCEPTION 'terminal account inventory poll evidence is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.poll_run_id <> OLD.poll_run_id
       OR NEW.instance_id <> OLD.instance_id
       OR NEW.node_type <> OLD.node_type
       OR NEW.driver_contract_version <> OLD.driver_contract_version
       OR NEW.scheduled_at <> OLD.scheduled_at
       OR NEW.provider_policy_version <> OLD.provider_policy_version
       OR NEW.max_attempts <> OLD.max_attempts
       OR NEW.poll_start_grace_seconds <> OLD.poll_start_grace_seconds
       OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'account inventory poll identity is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF NOT (
        (OLD.status = 'pending' AND NEW.status IN ('running', 'abandoned'))
        OR (OLD.status = 'running' AND NEW.status IN ('retry_wait', 'finalized', 'abandoned'))
        OR (OLD.status = 'retry_wait' AND NEW.status IN ('running', 'abandoned'))
    ) THEN
        RAISE EXCEPTION 'invalid account inventory poll transition'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.status = 'abandoned' AND EXISTS (
        SELECT 1 FROM public.account_inventory_poll_provider_results AS result
        WHERE result.poll_run_id = NEW.poll_run_id
    ) THEN
        RAISE EXCEPTION 'abandoned account inventory poll cannot contain provider evidence'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.status = 'finalized' THEN
        SELECT policy.active_providers INTO expected_providers
        FROM public.provider_inventory_policy_versions AS policy
        WHERE policy.policy_version_id = NEW.provider_policy_version
          AND policy.node_type = NEW.node_type
          AND policy.driver_contract_version = NEW.driver_contract_version;
        SELECT array_agg(result.provider ORDER BY result.provider)
        INTO stored_providers
        FROM public.account_inventory_poll_provider_results AS result
        WHERE result.poll_run_id = NEW.poll_run_id;
        IF stored_providers IS DISTINCT FROM expected_providers THEN
            RAISE EXCEPTION 'finalized account inventory poll provider set is incomplete'
                USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_protect_account_inventory_provider_result()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE parent_status text;
BEGIN
    IF TG_OP IN ('UPDATE', 'DELETE') THEN
        RAISE EXCEPTION 'account inventory provider evidence is immutable'
            USING ERRCODE = '23514';
    END IF;
    SELECT run.status INTO parent_status
    FROM public.account_inventory_poll_runs AS run
    WHERE run.poll_run_id = NEW.poll_run_id
    FOR KEY SHARE;
    IF parent_status <> 'running' THEN
        RAISE EXCEPTION 'provider evidence requires a running poll'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- Restore Migration 6's unconditional snapshot immutability gate.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_reject_account_inventory_snapshot_mutation()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    RAISE EXCEPTION 'account inventory snapshot evidence is immutable'
        USING ERRCODE = '42501';
END;
$$;
-- +goose StatementEnd

DROP TRIGGER account_inventory_poll_runs_refresh_provider_health_v1
    ON account_inventory_poll_runs;
DROP FUNCTION public.control_refresh_account_inventory_provider_health_v1();

-- Restore the Migration 7 Provider pointer guard before removing health
-- columns so an isolated down/up round trip returns the exact prior contract.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_protect_account_inventory_provider_state()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE write_gate text := coalesce(current_setting('relay_control.lifecycle_write', true), '');
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.monitoring_status <> 'active' OR NEW.out_of_scope_since IS NOT NULL OR NOT EXISTS (
            SELECT 1 FROM public.account_inventory_poll_provider_results AS result
            WHERE result.poll_run_id = NEW.current_poll_run_id
              AND result.provider = NEW.provider AND result.promotion_applied
        ) THEN
            RAISE EXCEPTION 'account inventory provider state requires applied promotion'
                USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'account inventory provider state cannot be deleted'
            USING ERRCODE = '42501';
    END IF;
    IF NEW.instance_id <> OLD.instance_id OR NEW.provider <> OLD.provider THEN
        RAISE EXCEPTION 'account inventory provider state identity is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.current_poll_run_id IS NULL
       AND OLD.current_poll_run_id IS NOT NULL
       AND NEW.current_scheduled_at = OLD.current_scheduled_at
       AND NEW.last_complete_at = OLD.last_complete_at
       AND NEW.source_observed_at = OLD.source_observed_at
       AND NEW.source_node_version = OLD.source_node_version
       AND NEW.source_node_commit = OLD.source_node_commit
       AND NEW.state = OLD.state AND NEW.updated_at = OLD.updated_at
       AND NEW.monitoring_status = OLD.monitoring_status
       AND NEW.out_of_scope_since IS NOT DISTINCT FROM OLD.out_of_scope_since THEN
        RETURN NEW;
    END IF;
    IF write_gate = 'policy'
       AND NEW.current_poll_run_id IS NOT DISTINCT FROM OLD.current_poll_run_id
       AND NEW.current_scheduled_at = OLD.current_scheduled_at
       AND NEW.last_complete_at = OLD.last_complete_at
       AND NEW.source_observed_at = OLD.source_observed_at
       AND NEW.source_node_version = OLD.source_node_version
       AND NEW.source_node_commit = OLD.source_node_commit
       AND NEW.state = OLD.state
       AND ((OLD.monitoring_status = 'active' AND NEW.monitoring_status = 'out_of_scope'
             AND NEW.out_of_scope_since IS NOT NULL)
            OR (OLD.monitoring_status = 'out_of_scope' AND NEW.monitoring_status = 'active'
                AND NEW.out_of_scope_since IS NULL)) THEN
        RETURN NEW;
    END IF;
    IF NEW.monitoring_status <> 'active' OR NEW.out_of_scope_since IS NOT NULL
       OR NEW.current_scheduled_at <= OLD.current_scheduled_at THEN
        RAISE EXCEPTION 'account inventory provider pointer must advance while active'
            USING ERRCODE = '23514';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM public.account_inventory_poll_provider_results AS result
        WHERE result.poll_run_id = NEW.current_poll_run_id
          AND result.provider = NEW.provider AND result.promotion_applied
    ) THEN
        RAISE EXCEPTION 'account inventory provider state requires applied promotion'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_query_current_account_inventory_v1(
    target_instance_id uuid,
    target_provider text,
    target_lifecycle text,
    target_basic_status text,
    target_normalized_email text,
    after_account_key text,
    page_limit integer
) RETURNS TABLE (
    instance_id uuid, provider text, account_key text, normalized_email text,
    basic_status text, lifecycle text, consecutive_missing_count integer,
    first_seen_at timestamptz, last_seen_at timestamptz,
    missing_since timestamptz, out_of_scope_since timestamptz,
    last_refresh_at timestamptz, next_retry_at timestamptz,
    source_updated_at timestamptz, provider_last_complete_at timestamptz,
    provider_degraded boolean, snapshot_freshness text
)
LANGUAGE plpgsql
SECURITY DEFINER
STABLE
SET search_path = pg_catalog
AS $$
DECLARE database_now timestamptz := clock_timestamp();
BEGIN
    IF target_instance_id IS NULL OR target_provider IS NULL
       OR target_lifecycle IS NULL OR target_basic_status IS NULL
       OR target_normalized_email IS NULL OR after_account_key IS NULL
       OR page_limit IS NULL
       OR (target_provider <> '' AND (
           octet_length(target_provider) NOT BETWEEN 1 AND 64
           OR target_provider !~ '^[a-z0-9][a-z0-9._-]*$'))
       OR (target_lifecycle <> '' AND target_lifecycle NOT IN (
           'present', 'suspected_missing', 'missing', 'out_of_scope'))
       OR (target_basic_status <> '' AND target_basic_status NOT IN (
           'reported_active', 'disabled', 'unavailable', 'error', 'unknown'))
       OR (target_normalized_email <> '' AND (
           octet_length(target_normalized_email) NOT BETWEEN 1 AND 320
           OR target_normalized_email <> lower(btrim(target_normalized_email))
           OR target_normalized_email ~ '[[:cntrl:]]'))
       OR octet_length(after_account_key) > 385
       OR (after_account_key <> '' AND (
           strpos(after_account_key, ':') < 2
           OR after_account_key <> lower(btrim(after_account_key))
           OR after_account_key ~ '[[:cntrl:]]'))
       OR page_limit NOT BETWEEN 1 AND 101 THEN
        RAISE EXCEPTION 'invalid account inventory query' USING ERRCODE = '22023';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM public.relay_node_assets AS asset
        WHERE asset.instance_id = target_instance_id
    ) THEN
        RAISE EXCEPTION 'account inventory instance is not registered'
            USING ERRCODE = 'P0404';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM public.relay_node_assets AS asset
        JOIN public.node_capabilities AS capability
          ON capability.instance_id = asset.instance_id
         AND capability.node_type = asset.node_type
         AND capability.driver_contract_version = asset.driver_contract_version
        WHERE asset.instance_id = target_instance_id
          AND capability.capability = 'management_account_inventory_read'
    ) THEN
        RAISE EXCEPTION 'account inventory capability is unavailable'
            USING ERRCODE = 'P0409';
    END IF;
    IF EXISTS (
        SELECT 1 FROM public.account_inventory AS account
        LEFT JOIN public.account_inventory_provider_states AS state
          ON state.instance_id = account.instance_id AND state.provider = account.provider
        LEFT JOIN public.account_inventory_poll_provider_results AS source
          ON source.poll_run_id = state.current_poll_run_id AND source.provider = state.provider
        WHERE account.instance_id = target_instance_id
          AND (target_provider = '' OR account.provider = target_provider)
          AND (target_lifecycle = '' OR account.lifecycle = target_lifecycle)
          AND (target_basic_status = '' OR account.basic_status =
              CASE target_basic_status WHEN 'reported_active' THEN 'active' ELSE target_basic_status END)
          AND (target_normalized_email = '' OR account.normalized_email = target_normalized_email)
          AND account.account_key > after_account_key
          AND (state.instance_id IS NULL OR state.state <> 'current'
               OR state.last_complete_at IS NULL OR state.current_poll_run_id IS NULL
               OR source.poll_run_id IS NULL
               OR (account.lifecycle = 'out_of_scope')
                    <> (state.monitoring_status = 'out_of_scope'))
    ) THEN
        RAISE EXCEPTION 'account inventory current state is inconsistent'
            USING ERRCODE = 'P0503';
    END IF;
    RETURN QUERY
    SELECT account.instance_id, account.provider, account.account_key,
           account.normalized_email,
           CASE account.basic_status WHEN 'active' THEN 'reported_active'
                ELSE account.basic_status END,
           account.lifecycle, account.consecutive_missing_count,
           account.first_seen_at, account.last_seen_at,
           account.missing_since, account.out_of_scope_since,
           account.last_refresh_at, account.next_retry_at,
           account.source_updated_at, state.last_complete_at, source.degraded,
           CASE WHEN account.lifecycle = 'out_of_scope' THEN 'out_of_scope'
                WHEN database_now - state.last_complete_at > interval '15 minutes' THEN 'stale'
                ELSE 'fresh' END
    FROM public.account_inventory AS account
    JOIN public.account_inventory_provider_states AS state
      ON state.instance_id = account.instance_id AND state.provider = account.provider
    JOIN public.account_inventory_poll_provider_results AS source
      ON source.poll_run_id = state.current_poll_run_id AND source.provider = state.provider
    WHERE account.instance_id = target_instance_id
      AND (target_provider = '' OR account.provider = target_provider)
      AND (target_lifecycle = '' OR account.lifecycle = target_lifecycle)
      AND (target_basic_status = '' OR account.basic_status =
          CASE target_basic_status WHEN 'reported_active' THEN 'active' ELSE target_basic_status END)
      AND (target_normalized_email = '' OR account.normalized_email = target_normalized_email)
      AND account.account_key > after_account_key
    ORDER BY account.account_key LIMIT page_limit;
END;
$$;
-- +goose StatementEnd

ALTER TABLE account_inventory_provider_states
    DROP CONSTRAINT account_inventory_provider_health_reason_fixed,
    DROP CONSTRAINT account_inventory_provider_health_slot_aligned,
    DROP COLUMN health_reason,
    DROP COLUMN health_degraded,
    DROP COLUMN health_scheduled_at;

DROP TRIGGER account_inventory_daily_provider_rollups_truncate_immutable
    ON account_inventory_daily_provider_rollups;
DROP TRIGGER account_inventory_daily_provider_rollups_immutable
    ON account_inventory_daily_provider_rollups;
DROP TRIGGER account_inventory_daily_account_rollups_truncate_immutable
    ON account_inventory_daily_account_rollups;
DROP TRIGGER account_inventory_daily_account_rollups_immutable
    ON account_inventory_daily_account_rollups;
DROP TRIGGER account_inventory_daily_provider_summaries_truncate_immutable
    ON account_inventory_daily_provider_summaries;
DROP TRIGGER account_inventory_daily_provider_summaries_immutable
    ON account_inventory_daily_provider_summaries;
DROP TRIGGER account_inventory_daily_summaries_truncate_immutable
    ON account_inventory_daily_summaries;
DROP TRIGGER account_inventory_daily_summaries_immutable
    ON account_inventory_daily_summaries;
DROP TRIGGER account_inventory_daily_rollup_runs_truncate_guard
    ON account_inventory_daily_rollup_runs;
DROP TRIGGER account_inventory_daily_rollup_runs_guard
    ON account_inventory_daily_rollup_runs;
DROP TRIGGER account_inventory_compaction_runs_truncate_guard
    ON account_inventory_compaction_runs;
DROP TRIGGER account_inventory_compaction_runs_guard
    ON account_inventory_compaction_runs;

DROP TRIGGER account_inventory_poll_runs_retired_day_guard
    ON account_inventory_poll_runs;
DROP FUNCTION public.control_reject_account_inventory_retired_day_poll();
DROP TRIGGER account_inventory_history_retired_days_truncate_guard
    ON account_inventory_history_retired_days;
DROP TRIGGER account_inventory_history_retired_days_guard
    ON account_inventory_history_retired_days;
DROP FUNCTION public.control_protect_account_inventory_history_retired_day();

DROP TABLE account_inventory_daily_provider_rollups;
DROP TABLE account_inventory_daily_account_rollups;
DROP TABLE account_inventory_daily_provider_summaries;
DROP TABLE account_inventory_daily_summaries;
DROP TABLE account_inventory_history_retired_days;
DROP TABLE account_inventory_daily_rollup_runs;
DROP TABLE account_inventory_compaction_runs;

DROP FUNCTION public.control_protect_account_inventory_history_run();
DROP FUNCTION public.control_reject_account_inventory_history_row_mutation();
DROP FUNCTION public.control_history_bytea_equal_v1(bytea, bytea);
DROP FUNCTION public.control_history_time_text_v1(timestamptz);
DROP FUNCTION public.control_history_checksum_chain_v1(bytea[]);
DROP FUNCTION public.control_history_canonical_row_v1(VARIADIC bytea[]);
