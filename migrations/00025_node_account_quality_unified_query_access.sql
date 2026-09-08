-- +goose Up
-- Unified read model: Inventory supplies the account set; existing quality
-- function supplies the aggregate; recent requests are bounded evidence.
-- +goose StatementBegin
CREATE FUNCTION public.control_query_node_account_quality_v3(
    target_instance_id uuid, target_provider text, target_lifecycle text,
    target_basic_status text, target_email text, target_quality text,
    after_account_key text, window_size interval, page_limit integer
) RETURNS TABLE (
    account_key text, provider text, normalized_email text, quality text,
    request_count bigint, success_count bigint, failure_count bigint,
    success_rate double precision, p95_latency_ms double precision,
    last_success_at timestamptz, last_failure_at timestamptz,
    last_failure_class text, inventory jsonb, recent_requests jsonb
) LANGUAGE plpgsql SECURITY DEFINER STABLE SET search_path = pg_catalog AS $$
DECLARE
    cursor_key text := after_account_key;
    inv record;
    stats record;
    category text;
    emitted integer := 0;
    scanned integer := 0;
BEGIN
    IF target_instance_id IS NULL OR target_provider IS NULL OR target_lifecycle IS NULL
       OR target_basic_status IS NULL OR target_email IS NULL OR target_quality IS NULL
       OR cursor_key IS NULL OR window_size IS NULL OR page_limit IS NULL
       OR (target_provider <> '' AND (octet_length(target_provider) NOT BETWEEN 1 AND 64 OR target_provider !~ '^[a-z0-9][a-z0-9._-]*$'))
       OR (target_lifecycle <> '' AND target_lifecycle NOT IN ('present','suspected_missing','missing','out_of_scope'))
       OR (target_basic_status <> '' AND target_basic_status NOT IN ('reported_active','disabled','unavailable','error','unknown'))
       OR (target_email <> '' AND (octet_length(target_email) NOT BETWEEN 1 AND 320 OR target_email <> lower(btrim(target_email)) OR target_email ~ '[[:cntrl:]]'))
       OR target_quality NOT IN ('','good','degraded','bad','unknown')
       OR window_size NOT IN (interval '15 minutes', interval '1 hour')
       OR octet_length(cursor_key) > 385 OR page_limit NOT BETWEEN 1 AND 100 THEN
        RAISE EXCEPTION 'invalid node account quality query' USING ERRCODE = '22023';
    END IF;
    LOOP
        FOR inv IN SELECT * FROM public.control_query_current_account_inventory_v1(
            target_instance_id, target_provider, target_lifecycle, target_basic_status,
            target_email, cursor_key, 101
        ) LOOP
            scanned := scanned + 1;
            cursor_key := inv.account_key;
            SELECT * INTO stats FROM public.control_query_account_request_quality_v1(
                target_instance_id, inv.account_key, NULL, window_size
            );
            category := CASE WHEN stats.request_count = 0 THEN 'unknown'
                WHEN stats.success_rate >= 0.95 THEN 'good'
                WHEN stats.success_rate >= 0.80 THEN 'degraded' ELSE 'bad' END;
            IF target_quality = '' OR target_quality = category THEN
                account_key := inv.account_key;
                provider := inv.provider;
                normalized_email := inv.normalized_email;
                quality := category;
                request_count := stats.request_count;
                success_count := stats.success_count;
                failure_count := stats.failure_count;
                success_rate := stats.success_rate;
                p95_latency_ms := stats.p95_latency_ms;
                last_success_at := stats.last_success_at;
                last_failure_at := stats.last_failure_at;
                last_failure_class := stats.last_failure_class;
                inventory := jsonb_build_object(
                    'instance_id', inv.instance_id, 'provider', inv.provider,
                    'email', inv.normalized_email, 'basic_status', inv.basic_status,
                    'lifecycle', inv.lifecycle, 'consecutive_missing_count', inv.consecutive_missing_count,
                    'first_seen_at', inv.first_seen_at, 'last_seen_at', inv.last_seen_at,
                    'missing_since', inv.missing_since, 'out_of_scope_since', inv.out_of_scope_since,
                    'last_refresh_at', inv.last_refresh_at, 'next_retry_at', inv.next_retry_at,
                    'source_updated_at', inv.source_updated_at,
                    'provider_last_complete_at', inv.provider_last_complete_at,
                    'provider_degraded', inv.provider_degraded, 'snapshot_freshness', inv.snapshot_freshness
                );
                SELECT COALESCE(jsonb_agg(jsonb_build_object(
                    'occurred_at', e.occurred_at, 'model', e.model, 'success', e.success,
                    'failure_class', e.failure_class, 'duration_ms', e.duration_ms,
                    'request_id', e.request_id
                ) ORDER BY e.occurred_at DESC, e.event_hash DESC), '[]'::jsonb)
                INTO recent_requests
                FROM (
                    SELECT event_hash, occurred_at, model, success, failure_class, duration_ms, request_id
                    FROM public.account_request_quality_events AS e
                    WHERE e.account_key = inv.account_key
                      AND e.node_id = target_instance_id
                      AND e.occurred_at >= statement_timestamp() - interval '7 days'
                      AND e.occurred_at <= statement_timestamp()
                    ORDER BY occurred_at DESC, event_hash DESC
                    LIMIT 10
                ) e;
                RETURN NEXT;
                emitted := emitted + 1;
                IF emitted >= page_limit + 1 THEN RETURN; END IF;
            END IF;
        END LOOP;
        IF scanned < 101 THEN RETURN; END IF;
        scanned := 0;
    END LOOP;
END;
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_query_node_account_quality_v3(uuid,text,text,text,text,text,text,interval,integer) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_query_node_account_quality_v3(uuid,text,text,text,text,text,text,interval,integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_query_node_account_quality_v3(uuid,text,text,text,text,text,text,interval,integer) TO relay_control_runtime;

-- +goose Down
DROP FUNCTION public.control_query_node_account_quality_v3(uuid,text,text,text,text,text,text,interval,integer);
