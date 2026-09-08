-- +goose Up

CREATE TABLE account_request_quality_events (
    event_hash text NOT NULL,
    request_id text NOT NULL DEFAULT '',
    node_id uuid NOT NULL,
    provider text NOT NULL,
    account_key text,
    model text NOT NULL DEFAULT '',
    occurred_at timestamptz NOT NULL,
    duration_ms bigint,
    success boolean NOT NULL,
    failure_class text,
    PRIMARY KEY (node_id, event_hash),
    CONSTRAINT account_request_quality_event_hash_valid CHECK (octet_length(event_hash) BETWEEN 1 AND 256),
    CONSTRAINT account_request_quality_provider_valid CHECK (octet_length(provider) BETWEEN 1 AND 128),
    CONSTRAINT account_request_quality_duration_valid CHECK (duration_ms IS NULL OR duration_ms >= 0),
    CONSTRAINT account_request_quality_failure_valid CHECK (failure_class IS NULL OR failure_class IN ('auth','quota','rate_limit','upstream','unknown')),
    CONSTRAINT account_request_quality_outcome_shape CHECK ((success AND failure_class IS NULL) OR (NOT success AND failure_class IS NOT NULL)),
    CONSTRAINT account_request_quality_account_key_valid CHECK (account_key IS NULL OR (account_key = btrim(account_key) AND account_key <> '' AND octet_length(account_key) <= 512)),
    CONSTRAINT account_request_quality_provider_key_match CHECK (account_key IS NULL OR (left(account_key, octet_length(provider) + 1) = provider || ':' AND octet_length(substr(account_key, octet_length(provider) + 2)) > 0))
);
CREATE INDEX account_request_quality_account_idx ON account_request_quality_events (node_id, account_key, occurred_at DESC) WHERE account_key IS NOT NULL;
CREATE INDEX account_request_quality_provider_idx ON account_request_quality_events (node_id, provider, occurred_at DESC);
CREATE INDEX account_request_quality_retention_idx ON account_request_quality_events (occurred_at, node_id, event_hash);

-- Runtime callers interact with this append-only projection only through small
-- SECURITY DEFINER functions; the base table remains inaccessible to runtime.
-- +goose StatementBegin
CREATE FUNCTION public.control_insert_account_request_quality_events_v1(events jsonb)
RETURNS bigint LANGUAGE sql SECURITY DEFINER VOLATILE SET search_path = pg_catalog AS $$
    WITH inserted AS (
        INSERT INTO public.account_request_quality_events
            (event_hash, request_id, node_id, provider, account_key, model, occurred_at, duration_ms, success, failure_class)
        SELECT event_hash, COALESCE(request_id,''), node_id, provider, account_key, COALESCE(model,''), occurred_at,
               duration_ms, success, failure_class
        FROM jsonb_to_recordset(events) AS e(
            event_hash text, request_id text, node_id uuid, provider text, account_key text,
            model text, occurred_at timestamptz, duration_ms bigint, success boolean, failure_class text)
        ON CONFLICT (node_id, event_hash) DO NOTHING
        RETURNING 1
    ) SELECT count(*) FROM inserted;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_delete_old_account_request_quality_events_v1()
RETURNS bigint LANGUAGE sql SECURITY DEFINER VOLATILE SET search_path = pg_catalog AS $$
    WITH doomed AS (SELECT node_id, event_hash FROM public.account_request_quality_events WHERE occurred_at < statement_timestamp() - interval '7 days' ORDER BY occurred_at LIMIT 1000),
    deleted AS (DELETE FROM public.account_request_quality_events e USING doomed d WHERE e.node_id=d.node_id AND e.event_hash=d.event_hash RETURNING 1)
    SELECT count(*) FROM deleted;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_query_account_request_quality_v1(target_node uuid, target_account text, target_provider text, window_size interval)
RETURNS TABLE(request_count bigint, success_count bigint, failure_count bigint, unresolved_request_count bigint,
 success_rate double precision, p95_latency_ms double precision, last_success_at timestamptz, last_failure_at timestamptz, last_failure_class text)
LANGUAGE plpgsql SECURITY DEFINER STABLE SET search_path = pg_catalog AS $$
BEGIN
    IF target_node IS NULL OR target_account = '' OR window_size IS NULL
       OR window_size NOT IN (interval '15 minutes', interval '1 hour') THEN
        RAISE EXCEPTION 'account request quality window is invalid' USING ERRCODE = '22023';
    END IF;
    RETURN QUERY
    WITH filtered AS (
        SELECT * FROM public.account_request_quality_events
        WHERE node_id=target_node AND occurred_at >= statement_timestamp()-window_size
          AND occurred_at <= statement_timestamp()
          AND (target_account IS NULL OR account_key=target_account)
          AND (target_provider IS NULL OR provider=target_provider)
    ), stats AS (
        SELECT count(*) request_count, count(*) FILTER (WHERE success) success_count,
               count(*) FILTER (WHERE NOT success) failure_count,
               count(*) FILTER (WHERE account_key IS NULL) unresolved_request_count,
               percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms) FILTER (WHERE duration_ms IS NOT NULL) p95_latency_ms,
               max(occurred_at) FILTER (WHERE success) last_success_at,
               max(occurred_at) FILTER (WHERE NOT success) last_failure_at
        FROM filtered
    ) SELECT stats.request_count, stats.success_count, stats.failure_count, stats.unresolved_request_count,
        CASE WHEN stats.request_count=0 THEN NULL ELSE stats.success_count::double precision/stats.request_count END,
        stats.p95_latency_ms, stats.last_success_at, stats.last_failure_at,
        (SELECT failure_class FROM filtered WHERE NOT success ORDER BY occurred_at DESC, event_hash DESC LIMIT 1)
    FROM stats;
END;
$$;
-- +goose StatementEnd

-- Node targets use the existing asset/capability/monitoring truth.
-- +goose StatementBegin
CREATE FUNCTION public.control_query_account_request_quality_targets_v1()
RETURNS TABLE(instance_id uuid, node_type text, driver_contract_version text, management_endpoint text, reader_secret_ref text, capabilities text[])
LANGUAGE sql SECURITY DEFINER STABLE SET search_path = pg_catalog AS $$
    SELECT a.instance_id, a.node_type, a.driver_contract_version, a.management_endpoint, a.reader_secret_ref,
           ARRAY['management_account_inventory_read']::text[]
    FROM public.relay_node_assets a
    JOIN public.node_capabilities c ON c.instance_id=a.instance_id AND c.node_type=a.node_type AND c.driver_contract_version=a.driver_contract_version AND c.capability='management_account_inventory_read'
    JOIN public.relay_node_inventory_monitoring_activations m ON m.instance_id=a.instance_id
      AND m.active_range @> statement_timestamp()
    WHERE a.node_type='cliproxyapi' AND a.driver_contract_version='cliproxyapi.auth-files.v1'
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_insert_account_request_quality_events_v1(jsonb) OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_delete_old_account_request_quality_events_v1() OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_query_account_request_quality_v1(uuid, text, text, interval) OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_query_account_request_quality_targets_v1() OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_insert_account_request_quality_events_v1(jsonb) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.control_delete_old_account_request_quality_events_v1() FROM PUBLIC;
REVOKE ALL ON TABLE public.account_request_quality_events FROM PUBLIC, relay_control_runtime;
REVOKE ALL ON FUNCTION public.control_query_account_request_quality_v1(uuid, text, text, interval) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.control_query_account_request_quality_targets_v1() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_insert_account_request_quality_events_v1(jsonb) TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_delete_old_account_request_quality_events_v1() TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_query_account_request_quality_v1(uuid, text, text, interval) TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_query_account_request_quality_targets_v1() TO relay_control_runtime;

-- +goose Down
DROP FUNCTION public.control_query_account_request_quality_targets_v1();
DROP FUNCTION public.control_query_account_request_quality_v1(uuid, text, text, interval);
DROP FUNCTION public.control_delete_old_account_request_quality_events_v1();
DROP FUNCTION public.control_insert_account_request_quality_events_v1(jsonb);
DROP TABLE public.account_request_quality_events;
