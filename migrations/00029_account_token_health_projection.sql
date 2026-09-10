-- +goose Up

-- Phase 5 Token Health is a read projection over current Inventory evidence and
-- the existing Antigravity availability gate.  It does not persist token state.
-- The statement timestamp is stable for the whole read, including the v4
-- composition below.
-- +goose StatementBegin
CREATE FUNCTION public.control_query_account_token_health_v1(
    target_node uuid,
    target_accounts text[]
) RETURNS TABLE (
    account_key text,
    token_state text,
    expected_valid_until timestamptz
)
LANGUAGE plpgsql
SECURITY DEFINER
STABLE
SET search_path = pg_catalog
AS $$
DECLARE
    database_now timestamptz := statement_timestamp();
BEGIN
    IF target_node IS NULL
       OR target_accounts IS NULL
       OR cardinality(target_accounts) > 101 THEN
        RAISE EXCEPTION 'invalid account token health query'
            USING ERRCODE = '22023';
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM public.relay_node_assets AS asset
        WHERE asset.instance_id = target_node
    ) THEN
        RAISE EXCEPTION 'node not found' USING ERRCODE = 'P0404';
    END IF;

    RETURN QUERY
    WITH source AS MATERIALIZED (
        SELECT *
        FROM public.control_account_availability_source_v1(
            target_node, target_accounts
        )
    )
    SELECT source.account_key,
           CASE
               WHEN EXISTS (
                   SELECT 1
                   FROM public.account_availability_occurrences AS occurrence
                   WHERE occurrence.node_id = target_node
                     AND occurrence.account_key = source.account_key
                     AND occurrence.reason = 'token_invalid'
                     AND occurrence.status = 'ACTIVE'
               ) THEN 'INVALID'
               WHEN source.gate_reason IS NOT NULL THEN 'UNKNOWN'
               WHEN inventory.last_refresh_at IS NULL THEN 'UNKNOWN'
               WHEN inventory.last_refresh_at > database_now THEN 'UNKNOWN'
               WHEN database_now < inventory.last_refresh_at
                    + interval '3599 seconds' THEN 'VALID'
               ELSE 'UNKNOWN'
           END AS token_state,
           inventory.last_refresh_at + interval '3599 seconds'
               AS expected_valid_until
    FROM source
    JOIN public.account_inventory AS inventory
      ON inventory.instance_id = target_node
     AND inventory.provider = 'antigravity'
     AND inventory.account_key = source.account_key
    ORDER BY source.account_key;
END;
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_query_account_token_health_v1(uuid, text[])
    OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_query_account_token_health_v1(uuid, text[])
    FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_query_account_token_health_v1(uuid, text[])
    TO relay_control_runtime;

-- v4 preserves the v3 quality algorithm and page contract, then adds one
-- set-based Token Health projection for the returned Antigravity page.
-- +goose StatementBegin
CREATE FUNCTION public.control_query_node_account_quality_v4(
    target_instance_id uuid, target_provider text, target_lifecycle text,
    target_basic_status text, target_email text, target_quality text,
    after_account_key text, window_size interval, page_limit integer
) RETURNS TABLE (
    account_key text, provider text, normalized_email text, quality text,
    request_count bigint, success_count bigint, failure_count bigint,
    success_rate double precision, p95_latency_ms double precision,
    last_success_at timestamptz, last_failure_at timestamptz,
    last_failure_class text, inventory jsonb, recent_requests jsonb,
    token_state text, expected_valid_until timestamptz
)
LANGUAGE sql
SECURITY DEFINER
STABLE
SET search_path = pg_catalog
AS $$
    WITH quality_page AS MATERIALIZED (
        SELECT *
        FROM public.control_query_node_account_quality_v3(
            target_instance_id, target_provider, target_lifecycle,
            target_basic_status, target_email, target_quality,
            after_account_key, window_size, page_limit
        )
    ),
    token_page AS MATERIALIZED (
        SELECT *
        FROM public.control_query_account_token_health_v1(
            target_instance_id,
            ARRAY(
                SELECT quality.account_key
                FROM quality_page AS quality
                WHERE quality.provider = 'antigravity'
            )
        )
    )
    SELECT quality.account_key, quality.provider, quality.normalized_email,
           quality.quality, quality.request_count, quality.success_count,
           quality.failure_count, quality.success_rate,
           quality.p95_latency_ms, quality.last_success_at,
           quality.last_failure_at, quality.last_failure_class,
           quality.inventory, quality.recent_requests,
           CASE WHEN quality.provider = 'antigravity'
                THEN token.token_state ELSE NULL END,
           CASE WHEN quality.provider = 'antigravity'
                THEN token.expected_valid_until ELSE NULL END
    FROM quality_page AS quality
    LEFT JOIN token_page AS token
      ON token.account_key = quality.account_key
    ORDER BY quality.account_key;
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_query_node_account_quality_v4(
    uuid, text, text, text, text, text, text, interval, integer
) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_query_node_account_quality_v4(
    uuid, text, text, text, text, text, text, interval, integer
) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_query_node_account_quality_v4(
    uuid, text, text, text, text, text, text, interval, integer
) TO relay_control_runtime;

-- +goose Down
DROP FUNCTION public.control_query_node_account_quality_v4(
    uuid, text, text, text, text, text, text, interval, integer
);
DROP FUNCTION public.control_query_account_token_health_v1(uuid, text[]);
