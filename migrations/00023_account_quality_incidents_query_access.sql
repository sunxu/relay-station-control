-- +goose Up
-- Active incidents are a read projection over current Inventory membership and
-- retained request events. No incident state is persisted.
-- +goose StatementBegin
CREATE FUNCTION public.control_query_node_account_quality_incidents_v1(
    target_instance_id uuid, target_provider text, target_failure_class text,
    after_last_seen timestamptz, after_account_key text, after_failure_class text,
    page_limit integer
) RETURNS TABLE (
    node_id uuid, account_key text, provider text, failure_class text,
    status text, first_seen timestamptz, last_seen timestamptz, hit_count bigint,
    last_success_at timestamptz
)
LANGUAGE plpgsql SECURITY DEFINER STABLE SET search_path = pg_catalog AS $$
DECLARE
    cursor_key text := '';
    inventory record;
    inventory_keys text[] := ARRAY[]::text[];
    scanned integer := 0;
BEGIN
    IF target_instance_id IS NULL OR target_provider IS NULL OR target_failure_class IS NULL
       OR after_account_key IS NULL OR page_limit IS NULL OR page_limit NOT BETWEEN 1 AND 100
       OR after_failure_class IS NULL
       OR (after_last_seen IS NULL AND (after_account_key <> '' OR after_failure_class <> ''))
       OR (after_last_seen IS NOT NULL AND (after_account_key = '' OR after_failure_class = ''))
       OR target_failure_class NOT IN ('','auth','quota','rate_limit','upstream')
       OR (target_provider <> '' AND (octet_length(target_provider) NOT BETWEEN 1 AND 64 OR target_provider !~ '^[a-z0-9][a-z0-9._-]*$'))
       OR octet_length(after_account_key) > 385 OR octet_length(after_failure_class) > 32
       OR (after_failure_class <> '' AND after_failure_class NOT IN ('auth','quota','rate_limit','upstream')) THEN
        RAISE EXCEPTION 'invalid account quality incidents query' USING ERRCODE = '22023';
    END IF;
    LOOP
        FOR inventory IN SELECT * FROM public.control_query_current_account_inventory_v1(
            target_instance_id, target_provider, '', '', '', cursor_key, 101
        ) LOOP
            scanned := scanned + 1;
            IF inventory.account_key IS NOT NULL THEN
                inventory_keys := array_append(inventory_keys, inventory.account_key);
            END IF;
            cursor_key := inventory.account_key;
        END LOOP;
        IF scanned < 101 THEN EXIT; END IF;
        scanned := 0;
    END LOOP;
    -- An empty current Inventory is a valid empty result after the existing
    -- Inventory function has performed its Node/capability consistency gates.
    RETURN QUERY
    WITH grouped AS (
        SELECT event.account_key, split_part(event.account_key, ':', 1) AS event_provider,
               event.failure_class, min(event.occurred_at) AS group_first,
               max(event.occurred_at) AS group_last, count(*) AS group_hits
        FROM public.account_request_quality_events AS event
        WHERE event.node_id = target_instance_id
          AND event.account_key = ANY(inventory_keys)
          AND event.account_key IS NOT NULL
          AND NOT event.success
          AND event.failure_class IN ('auth','quota','rate_limit','upstream')
          AND (target_failure_class = '' OR event.failure_class = target_failure_class)
          AND event.occurred_at >= statement_timestamp() - interval '15 minutes'
          AND event.occurred_at <= statement_timestamp()
        GROUP BY event.account_key, event.failure_class
        HAVING count(*) >= 3
    )
    SELECT target_instance_id, grouped.account_key, grouped.event_provider,
           grouped.failure_class, 'active', grouped.group_first, grouped.group_last,
           grouped.group_hits,
           (SELECT max(success_event.occurred_at)
            FROM public.account_request_quality_events AS success_event
            WHERE success_event.node_id = target_instance_id
              AND success_event.account_key = grouped.account_key
              AND success_event.success
              AND success_event.occurred_at >= statement_timestamp() - interval '7 days'
              AND success_event.occurred_at <= statement_timestamp())
    FROM grouped
    WHERE after_last_seen IS NULL
       OR grouped.group_last < after_last_seen
       OR (grouped.group_last = after_last_seen AND (grouped.account_key, grouped.failure_class) > (after_account_key, after_failure_class))
    ORDER BY grouped.group_last DESC, grouped.account_key ASC, grouped.failure_class ASC
    LIMIT page_limit + 1;
END;
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_query_node_account_quality_incidents_v1(uuid,text,text,timestamptz,text,text,integer)
    OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_query_node_account_quality_incidents_v1(uuid,text,text,timestamptz,text,text,integer)
    FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_query_node_account_quality_incidents_v1(uuid,text,text,timestamptz,text,text,integer)
    TO relay_control_runtime;

-- +goose Down
DROP FUNCTION public.control_query_node_account_quality_incidents_v1(uuid,text,text,timestamptz,text,text,integer);
