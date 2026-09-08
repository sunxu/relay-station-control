-- +goose Up

-- History is available only for an account currently proven by Inventory.
-- Events never become a second account membership source.
-- +goose StatementBegin
CREATE FUNCTION public.control_query_account_request_history_v1(
    target_instance_id uuid,
    target_account_key text,
    after_occurred_at timestamptz,
    after_event_hash text,
    page_limit integer
) RETURNS TABLE (
    event_hash text,
    request_id text,
    model text,
    occurred_at timestamptz,
    duration_ms bigint,
    success boolean,
    failure_class text
)
LANGUAGE plpgsql
SECURITY DEFINER
STABLE
SET search_path = pg_catalog
AS $$
DECLARE
    separator integer;
    target_provider text;
    target_email text;
    current_account_exists boolean;
BEGIN
    IF target_instance_id IS NULL OR target_account_key IS NULL OR btrim(target_account_key) = ''
       OR after_occurred_at IS NULL AND after_event_hash IS NOT NULL
       OR after_occurred_at IS NOT NULL AND (after_event_hash IS NULL OR octet_length(after_event_hash) NOT BETWEEN 1 AND 256)
       OR page_limit IS NULL OR page_limit NOT BETWEEN 1 AND 100 THEN
        RAISE EXCEPTION 'invalid account request history query' USING ERRCODE = '22023';
    END IF;
    separator := strpos(target_account_key, ':');
    IF separator < 2 OR separator >= octet_length(target_account_key) THEN
        RAISE EXCEPTION 'invalid account request history query' USING ERRCODE = '22023';
    END IF;
    target_provider := substr(target_account_key, 1, separator - 1);
    target_email := substr(target_account_key, separator + 1);

    SELECT EXISTS (
        SELECT 1
        FROM public.control_query_current_account_inventory_v1(
            target_instance_id, target_provider, '', '', target_email, '', 1
        ) AS current_inventory
        WHERE current_inventory.account_key IS NOT NULL
          AND current_inventory.account_key = target_account_key
    ) INTO current_account_exists;
    IF NOT current_account_exists THEN
        RAISE EXCEPTION 'account inventory account is not registered' USING ERRCODE = 'P0404';
    END IF;

    RETURN QUERY
    SELECT event.event_hash, event.request_id, event.model, event.occurred_at,
           event.duration_ms, event.success, event.failure_class
    FROM public.account_request_quality_events AS event
    WHERE event.node_id = target_instance_id
      AND event.account_key = target_account_key
      AND event.occurred_at >= statement_timestamp() - interval '7 days'
      AND event.occurred_at <= statement_timestamp()
      AND (
          after_occurred_at IS NULL
          OR (event.occurred_at, event.event_hash) < (after_occurred_at, after_event_hash)
      )
    ORDER BY event.occurred_at DESC, event.event_hash DESC
    LIMIT page_limit + 1;
END;
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_query_account_request_history_v1(uuid,text,timestamptz,text,integer)
    OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_query_account_request_history_v1(uuid,text,timestamptz,text,integer)
    FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_query_account_request_history_v1(uuid,text,timestamptz,text,integer)
    TO relay_control_runtime;

-- +goose Down
DROP FUNCTION public.control_query_account_request_history_v1(uuid,text,timestamptz,text,integer);
