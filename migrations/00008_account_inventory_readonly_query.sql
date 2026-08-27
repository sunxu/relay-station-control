-- +goose Up

-- Product queries are bounded by one instance and sort by the immutable
-- account key.  The primary key already serves unfiltered/provider keyset
-- reads; these two narrow indexes cover the remaining exact filters without
-- duplicating identity or current-state data.
CREATE INDEX account_inventory_normalized_email_read_idx
    ON account_inventory (instance_id, normalized_email, account_key);
CREATE INDEX account_inventory_basic_status_read_idx
    ON account_inventory (instance_id, basic_status, account_key);

-- +goose StatementBegin
CREATE FUNCTION public.control_query_current_account_inventory_v1(
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
AS $$
DECLARE
    database_now timestamptz := clock_timestamp();
BEGIN
    IF target_instance_id IS NULL
       OR target_provider IS NULL
       OR target_lifecycle IS NULL
       OR target_basic_status IS NULL
       OR target_normalized_email IS NULL
       OR after_account_key IS NULL
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
        RAISE EXCEPTION 'invalid account inventory query'
            USING ERRCODE = '22023';
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

    -- A matching lifecycle row without its exact current Provider source is a
    -- database inconsistency, not an empty/default product result.
    IF EXISTS (
        SELECT 1
        FROM public.account_inventory AS account
        LEFT JOIN public.account_inventory_provider_states AS state
          ON state.instance_id = account.instance_id
         AND state.provider = account.provider
        LEFT JOIN public.account_inventory_poll_provider_results AS source
          ON source.poll_run_id = state.current_poll_run_id
         AND source.provider = state.provider
        WHERE account.instance_id = target_instance_id
          AND (target_provider = '' OR account.provider = target_provider)
          AND (target_lifecycle = '' OR account.lifecycle = target_lifecycle)
          AND (target_basic_status = '' OR account.basic_status =
              CASE target_basic_status WHEN 'reported_active' THEN 'active' ELSE target_basic_status END)
          AND (target_normalized_email = '' OR account.normalized_email = target_normalized_email)
          AND account.account_key > after_account_key
          AND (
              state.instance_id IS NULL
              OR state.state <> 'current'
              OR state.last_complete_at IS NULL
              OR state.current_poll_run_id IS NULL
              OR source.poll_run_id IS NULL
              OR (account.lifecycle = 'out_of_scope') <> (state.monitoring_status = 'out_of_scope')
          )
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
           source.degraded,
           CASE
               WHEN account.lifecycle = 'out_of_scope' THEN 'out_of_scope'
               WHEN database_now - state.last_complete_at > interval '15 minutes' THEN 'stale'
               ELSE 'fresh'
           END
    FROM public.account_inventory AS account
    JOIN public.account_inventory_provider_states AS state
      ON state.instance_id = account.instance_id
     AND state.provider = account.provider
    JOIN public.account_inventory_poll_provider_results AS source
      ON source.poll_run_id = state.current_poll_run_id
     AND source.provider = state.provider
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
REVOKE EXECUTE ON FUNCTION public.control_query_current_account_inventory_v1(
    uuid, text, text, text, text, text, integer
) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_query_current_account_inventory_v1(
    uuid, text, text, text, text, text, integer
) TO relay_control_runtime;

-- The view audit is an additive member of the existing immutable audit log.
-- Detail-key denial is defense in depth on top of the application allowlist.
ALTER TABLE audit_logs DROP CONSTRAINT audit_logs_category_valid;
ALTER TABLE audit_logs ADD CONSTRAINT audit_logs_category_valid CHECK (
    category IN ('bootstrap', 'administrator', 'password', 'mfa', 'session',
                 'reauthentication', 'authorization', 'rate_limit', 'account_inventory')
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
        'auth.rate_limit', 'auth.csrf', 'account_inventory.view'
    )
);
ALTER TABLE audit_logs DROP CONSTRAINT audit_logs_details_valid;
ALTER TABLE audit_logs ADD CONSTRAINT audit_logs_details_valid CHECK (
    jsonb_typeof(details) = 'object'
    AND octet_length(details::text) <= 4096
    AND NOT (details ?| ARRAY[
        'password', 'password_phc', 'totp_secret', 'totp_code', 'recovery_code',
        'session_token', 'csrf_token', 'activation_token', 'bootstrap_secret', 'cookie',
        'login_name', 'display_name', 'ip', 'source_ip', 'email', 'account_key',
        'cursor', 'filter_value', 'filter_hash', 'result_identity'
    ])
);

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM public.audit_logs
        WHERE category = 'account_inventory' OR action = 'account_inventory.view'
    ) THEN
        RAISE EXCEPTION 'account inventory readonly query down preserves existing audit rows'
            USING ERRCODE = '55000';
    END IF;
END;
$$;
-- +goose StatementEnd

ALTER TABLE audit_logs DROP CONSTRAINT audit_logs_details_valid;
ALTER TABLE audit_logs ADD CONSTRAINT audit_logs_details_valid CHECK (
    jsonb_typeof(details) = 'object'
    AND octet_length(details::text) <= 4096
    AND NOT (details ?| ARRAY[
        'password', 'password_phc', 'totp_secret', 'totp_code', 'recovery_code',
        'session_token', 'csrf_token', 'activation_token', 'bootstrap_secret', 'cookie',
        'login_name', 'display_name', 'ip', 'source_ip'
    ])
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
        'auth.rate_limit', 'auth.csrf'
    )
);
ALTER TABLE audit_logs DROP CONSTRAINT audit_logs_category_valid;
ALTER TABLE audit_logs ADD CONSTRAINT audit_logs_category_valid CHECK (
    category IN ('bootstrap', 'administrator', 'password', 'mfa', 'session',
                 'reauthentication', 'authorization', 'rate_limit')
);

REVOKE EXECUTE ON FUNCTION public.control_query_current_account_inventory_v1(
    uuid, text, text, text, text, text, integer
) FROM relay_control_runtime;
DROP FUNCTION public.control_query_current_account_inventory_v1(
    uuid, text, text, text, text, text, integer
);
DROP INDEX account_inventory_basic_status_read_idx;
DROP INDEX account_inventory_normalized_email_read_idx;
