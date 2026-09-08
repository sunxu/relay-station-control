-- +goose Up
-- Bounded composition read: inventory remains the source of account rows and
-- the existing quality function remains the source of all statistics.
-- +goose StatementBegin
CREATE FUNCTION public.control_query_node_account_quality_v1(
    target_instance_id uuid, target_provider text, target_quality text,
    after_account_key text, window_size interval, page_limit integer
) RETURNS TABLE (
    account_key text, normalized_email text, provider text, quality text,
    request_count bigint, success_count bigint, failure_count bigint,
    success_rate double precision,
    p95_latency_ms double precision, last_success_at timestamptz,
    last_failure_at timestamptz, last_failure_class text
) LANGUAGE plpgsql SECURITY DEFINER STABLE SET search_path = pg_catalog AS $$
DECLARE
    cursor_key text := after_account_key;
    inv record;
    stats record;
    category text;
    emitted integer := 0;
    scanned integer := 0;
BEGIN
    IF target_instance_id IS NULL OR target_provider IS NULL OR target_quality IS NULL
       OR cursor_key IS NULL OR window_size IS NULL OR page_limit IS NULL
       OR (target_provider <> '' AND (octet_length(target_provider) NOT BETWEEN 1 AND 64 OR target_provider !~ '^[a-z0-9][a-z0-9._-]*$'))
       OR target_quality NOT IN ('','good','degraded','bad','unknown')
       OR window_size NOT IN (interval '15 minutes', interval '1 hour')
       OR octet_length(cursor_key) > 385 OR page_limit NOT BETWEEN 1 AND 100 THEN
        RAISE EXCEPTION 'invalid node account quality query' USING ERRCODE = '22023';
    END IF;
    LOOP
        FOR inv IN SELECT * FROM public.control_query_current_account_inventory_v1(
            target_instance_id, target_provider, '', '', '', cursor_key, 101
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
                provider := inv.provider; account_key := inv.account_key; normalized_email := inv.normalized_email; quality := category;
                request_count := stats.request_count; success_count := stats.success_count; failure_count := stats.failure_count;
                success_rate := stats.success_rate;
                p95_latency_ms := stats.p95_latency_ms; last_success_at := stats.last_success_at;
                last_failure_at := stats.last_failure_at; last_failure_class := stats.last_failure_class;
                RETURN NEXT;
                emitted := emitted + 1;
                IF emitted >= page_limit + 1 THEN RETURN; END IF;
            END IF;
        END LOOP;
        IF scanned < 101 THEN RETURN; END IF;
        scanned := 0;
        -- A full chunk advances the cursor; quality filtering may require
        -- scanning more inventory chunks before the response page is full.
    END LOOP;
END;
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_query_node_account_quality_v1(uuid,text,text,text,interval,integer) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_query_node_account_quality_v1(uuid,text,text,text,interval,integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_query_node_account_quality_v1(uuid,text,text,text,interval,integer) TO relay_control_runtime;

-- +goose Down
DROP FUNCTION public.control_query_node_account_quality_v1(uuid,text,text,text,interval,integer);
