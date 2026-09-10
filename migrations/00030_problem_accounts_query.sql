-- +goose Up

-- Problems is a read-only projection over the existing confirmed occurrence
-- facts.  The aggregated page is deliberately bounded before any diagnostic
-- enrichment so that the diagnostics remain page-batched rather than
-- becoming one query per account.
-- +goose StatementBegin
CREATE FUNCTION public.control_query_problem_accounts_v1(
    target_provider text DEFAULT '',
    target_node uuid DEFAULT NULL,
    target_severity text DEFAULT '',
    target_reason text DEFAULT '',
    target_email text DEFAULT '',
    after_severity text DEFAULT NULL,
    after_since timestamptz DEFAULT NULL,
    after_email text DEFAULT NULL,
    after_node uuid DEFAULT NULL,
    page_limit integer DEFAULT 25
) RETURNS TABLE (
    instance_id uuid,
    node_name text,
    account_key text,
    email text,
    provider text,
    issues jsonb,
    availability jsonb,
    token_state text,
    last_refresh_at timestamptz,
    expected_valid_until timestamptz,
    next_retry_at timestamptz,
    last_success_at timestamptz,
    last_failure_at timestamptz,
    highest_severity text,
    oldest_active_since timestamptz
)
LANGUAGE plpgsql
SECURITY DEFINER
STABLE
SET search_path = pg_catalog
AS $$
DECLARE
    provider_filter text := lower(btrim(COALESCE(target_provider, '')));
    email_filter text := lower(btrim(COALESCE(target_email, '')));
    reason_filter text := lower(btrim(COALESCE(target_reason, '')));
    cursor_severity text := NULLIF(btrim(COALESCE(after_severity, '')), '');
    cursor_email text := NULLIF(lower(btrim(COALESCE(after_email, ''))), '');
BEGIN
    IF octet_length(provider_filter) > 64
       OR (provider_filter <> '' AND provider_filter !~ '^[a-z0-9][a-z0-9._-]*$')
       OR octet_length(email_filter) > 320
       OR (email_filter <> '' AND email_filter ~ '[[:cntrl:]]')
       OR octet_length(reason_filter) > 64
       OR (reason_filter <> '' AND reason_filter NOT IN
           ('token_invalid', 'account_blocked', 'forbidden',
            'cross_node_duplicate_ownership'))
       OR (target_severity IS NOT NULL AND target_severity <> ''
           AND target_severity NOT IN ('Critical', 'Warning'))
       OR page_limit IS NULL OR page_limit NOT BETWEEN 1 AND 100
       OR ((cursor_severity IS NULL) <> (after_since IS NULL))
       OR ((cursor_severity IS NULL) <> (cursor_email IS NULL))
       OR ((cursor_severity IS NULL) <> (after_node IS NULL))
       OR (cursor_severity IS NOT NULL
           AND cursor_severity NOT IN ('Critical', 'Warning'))
       OR (cursor_email IS NOT NULL
           AND (octet_length(cursor_email) > 320
                OR cursor_email ~ '[[:cntrl:]]')) THEN
        RAISE EXCEPTION 'invalid problem accounts query'
            USING ERRCODE = '22023';
    END IF;

    -- An unsupported provider is a validly shaped caller filter and therefore
    -- intentionally returns an empty result rather than a provider error.
    IF provider_filter <> '' AND provider_filter <> 'antigravity' THEN
        RETURN;
    END IF;

    RETURN QUERY
    WITH issue_rows AS MATERIALIZED (
        SELECT o.node_id AS instance_id, o.account_key, upper(o.reason) AS issue_type,
               o.reason, o.severity, o.occurrence_id, o.first_seen_at
        FROM public.account_availability_occurrences AS o
        WHERE o.status = 'ACTIVE'
          AND o.reason IN ('token_invalid', 'account_blocked', 'forbidden')
          AND split_part(o.account_key, ':', 1) = 'antigravity'
        UNION ALL
        SELECT n.instance_id, o.account_key,
               'CROSS_NODE_DUPLICATE_OWNERSHIP',
               'cross_node_duplicate_ownership', o.severity,
               o.occurrence_id, o.first_seen_at
        FROM public.cross_node_duplicate_occurrences AS o
        JOIN public.cross_node_duplicate_occurrence_nodes AS n
          ON n.occurrence_id = o.occurrence_id
        WHERE o.status = 'ACTIVE'
          AND o.conflict_type = 'cross_node_duplicate_ownership'
          AND split_part(o.account_key, ':', 1) = 'antigravity'
    ),
    grouped AS MATERIALIZED (
        SELECT i.instance_id, i.account_key,
               substr(i.account_key, position(':' IN i.account_key) + 1) AS email,
               jsonb_agg(
                   jsonb_build_object(
                       'type', i.issue_type,
                       'reason', i.reason,
                       'severity', i.severity,
                       'occurrence_id', i.occurrence_id,
                       'since', i.first_seen_at
                   )
                   ORDER BY CASE i.severity WHEN 'Critical' THEN 2 ELSE 1 END DESC,
                            i.issue_type, i.first_seen_at, i.occurrence_id
               ) AS issues,
               CASE WHEN max(CASE WHEN i.severity = 'Critical' THEN 2 ELSE 1 END) = 2
                    THEN 'Critical' ELSE 'Warning' END AS highest_severity,
               min(i.first_seen_at) AS oldest_active_since
        FROM issue_rows AS i
        GROUP BY i.instance_id, i.account_key
    ),
    filtered AS MATERIALIZED (
        SELECT g.*,
               CASE WHEN g.highest_severity = 'Critical' THEN 2 ELSE 1 END AS severity_rank
        FROM grouped AS g
        WHERE (target_node IS NULL OR g.instance_id = target_node)
          AND (target_severity IS NULL OR target_severity = ''
               OR g.highest_severity = target_severity)
          AND (reason_filter = '' OR EXISTS (
               SELECT 1 FROM jsonb_array_elements(g.issues) AS issue
               WHERE issue->>'reason' = reason_filter))
          AND (email_filter = '' OR g.email = email_filter)
          AND (cursor_severity IS NULL OR
               (-CASE WHEN g.highest_severity = 'Critical' THEN 2 ELSE 1 END,
                g.oldest_active_since, g.email COLLATE "C", g.instance_id)
               >
               (-CASE WHEN cursor_severity = 'Critical' THEN 2 ELSE 1 END,
                after_since, cursor_email COLLATE "C", after_node))
        ORDER BY severity_rank DESC, g.oldest_active_since, g.email COLLATE "C",
                 g.instance_id
        LIMIT page_limit + 1
    ),
    page AS MATERIALIZED (
        SELECT f.*, row_number() OVER (PARTITION BY f.instance_id
                                       ORDER BY f.account_key) AS node_row
        FROM filtered AS f
    ),
    node_batches AS MATERIALIZED (
        SELECT p.instance_id,
               (p.node_row - 1) / 100 AS batch_no,
               array_agg(p.account_key ORDER BY p.account_key) AS account_keys
        FROM page AS p
        JOIN public.relay_node_assets AS a ON a.instance_id = p.instance_id
        GROUP BY p.instance_id, (p.node_row - 1) / 100
    ),
    availability_diag AS MATERIALIZED (
        SELECT b.instance_id, x.value->>'account_key' AS account_key,
               x.value - 'account_key' AS availability
        FROM node_batches AS b
        CROSS JOIN LATERAL jsonb_array_elements(
            public.control_query_account_availability_v1(b.instance_id, b.account_keys)
        ) AS x(value)
    ),
    token_diag AS MATERIALIZED (
        SELECT b.instance_id, t.account_key, t.token_state,
               t.expected_valid_until
        FROM node_batches AS b
        CROSS JOIN LATERAL public.control_query_account_token_health_v1(
            b.instance_id, b.account_keys
        ) AS t
    ),
    quality_diag AS MATERIALIZED (
        SELECT p.instance_id, p.account_key,
               max(e.occurred_at) FILTER (WHERE e.success) AS last_success_at,
               max(e.occurred_at) FILTER (WHERE NOT e.success) AS last_failure_at
        FROM page AS p
        LEFT JOIN public.account_request_quality_events AS e
          ON e.node_id = p.instance_id
         AND e.account_key = p.account_key
         AND e.provider = 'antigravity'
        GROUP BY p.instance_id, p.account_key
    )
    SELECT p.instance_id,
           COALESCE(asset.display_name, ''),
           p.account_key,
           p.email,
           'antigravity'::text,
           p.issues,
           COALESCE(a.availability,
                    '{"state":"UNKNOWN","reason":"not_present","since":null}'::jsonb),
           COALESCE(t.token_state,
                    CASE WHEN EXISTS (
                        SELECT 1 FROM jsonb_array_elements(p.issues) AS issue
                        WHERE issue->>'type' = 'TOKEN_INVALID'
                    ) THEN 'INVALID' ELSE 'UNKNOWN' END),
           inventory.last_refresh_at,
           t.expected_valid_until,
           inventory.next_retry_at,
           q.last_success_at,
           q.last_failure_at,
           p.highest_severity,
           p.oldest_active_since
    FROM page AS p
    LEFT JOIN public.relay_node_assets AS asset
      ON asset.instance_id = p.instance_id
    LEFT JOIN public.account_inventory AS inventory
      ON inventory.instance_id = p.instance_id
     AND inventory.provider = 'antigravity'
     AND inventory.account_key = p.account_key
    LEFT JOIN availability_diag AS a
      ON a.instance_id = p.instance_id AND a.account_key = p.account_key
    LEFT JOIN token_diag AS t
      ON t.instance_id = p.instance_id AND t.account_key = p.account_key
    LEFT JOIN quality_diag AS q
      ON q.instance_id = p.instance_id AND q.account_key = p.account_key
    ORDER BY p.severity_rank DESC, p.oldest_active_since, p.email COLLATE "C",
             p.instance_id;
END;
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_query_problem_accounts_v1(
    text, uuid, text, text, text, text, timestamptz, text, uuid, integer
) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_query_problem_accounts_v1(
    text, uuid, text, text, text, text, timestamptz, text, uuid, integer
) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_query_problem_accounts_v1(
    text, uuid, text, text, text, text, timestamptz, text, uuid, integer
) TO relay_control_runtime;

-- +goose Down
DROP FUNCTION public.control_query_problem_accounts_v1(
    text, uuid, text, text, text, text, timestamptz, text, uuid, integer
);
