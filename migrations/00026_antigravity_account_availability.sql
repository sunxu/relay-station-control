-- +goose Up

ALTER TABLE public.account_inventory_snapshot_items
    ADD COLUMN availability_runtime_evidence text,
    ADD COLUMN auth_failure_reason text;
ALTER TABLE public.account_inventory
    ADD COLUMN availability_runtime_evidence text,
    ADD COLUMN auth_failure_reason text;
ALTER TABLE public.account_request_quality_events
    ADD COLUMN auth_failure_reason text;

ALTER TABLE public.account_inventory_snapshot_items
    ADD CONSTRAINT account_inventory_snapshot_availability_evidence_fixed CHECK (
        availability_runtime_evidence IS NULL OR (provider = 'antigravity' AND availability_runtime_evidence IN
        ('file_active','file_disabled','file_error','file_unavailable','file_unknown'))
    ),
    ADD CONSTRAINT account_inventory_snapshot_auth_failure_reason_fixed CHECK (
        auth_failure_reason IS NULL OR (provider = 'antigravity' AND auth_failure_reason IN
        ('token_invalid','account_blocked','forbidden','other'))
    );
ALTER TABLE public.account_inventory
    ADD CONSTRAINT account_inventory_availability_evidence_fixed CHECK (
        availability_runtime_evidence IS NULL OR (provider = 'antigravity' AND availability_runtime_evidence IN
        ('file_active','file_disabled','file_error','file_unavailable','file_unknown'))
    ),
    ADD CONSTRAINT account_inventory_auth_failure_reason_fixed CHECK (
        auth_failure_reason IS NULL OR (provider = 'antigravity' AND auth_failure_reason IN
        ('token_invalid','account_blocked','forbidden','other'))
    );
ALTER TABLE public.account_request_quality_events
    ADD CONSTRAINT account_request_quality_auth_failure_reason_fixed CHECK (
        auth_failure_reason IS NULL OR (provider = 'antigravity' AND auth_failure_reason IN
        ('token_invalid','account_blocked','forbidden','other'))
    );

CREATE TABLE public.account_availability_checkpoints (
    node_id uuid NOT NULL,
    account_key text NOT NULL,
    state text NOT NULL DEFAULT 'UNKNOWN',
    reason text,
    since timestamptz,
    last_runtime_source_id uuid,
    last_runtime_source_at timestamptz,
    last_runtime_slot timestamptz,
    consecutive_healthy_sources smallint NOT NULL DEFAULT 0,
    last_failure_at timestamptz,
    last_success_at timestamptz,
    recovery_watermark timestamptz,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (node_id, account_key),
    CONSTRAINT account_availability_checkpoint_state_fixed CHECK
        (state IN ('AVAILABLE','TOKEN_INVALID','ACCOUNT_BLOCKED','FORBIDDEN','UNKNOWN','DISABLED')),
    CONSTRAINT account_availability_checkpoint_reason_fixed CHECK
        (reason IS NULL OR reason IN ('available','disabled','token_invalid','account_blocked','forbidden','stale','incomplete','node_collection_failed','not_present','unsupported_mode','unproven','pending_confirmation','runtime_unavailable','retry_wait','conflicting_evidence')),
    CONSTRAINT account_availability_checkpoint_healthy_bounded CHECK
        (consecutive_healthy_sources BETWEEN 0 AND 2),
    CONSTRAINT account_availability_checkpoint_key_valid CHECK
        (account_key = btrim(account_key) AND account_key <> '' AND octet_length(account_key) <= 385),
    FOREIGN KEY (node_id, account_key) REFERENCES public.account_inventory(instance_id, account_key)
        ON UPDATE RESTRICT ON DELETE CASCADE
);

CREATE TABLE public.account_availability_occurrences (
    occurrence_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    node_id uuid NOT NULL,
    account_key text NOT NULL,
    reason text NOT NULL,
    severity text NOT NULL,
    status text NOT NULL DEFAULT 'ACTIVE',
    first_seen_at timestamptz NOT NULL,
    last_failure_at timestamptz NOT NULL,
    confirmed_at timestamptz NOT NULL,
    resolved_at timestamptz,
    confirmation_request_id_1 text,
    confirmation_request_id_2 text,
    confirmation_event_hash_1 text,
    confirmation_event_hash_2 text,
    confirmation_source_id uuid,
    confirmation_source_at timestamptz,
    recovery_source_id uuid,
    recovery_source_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT account_availability_occurrence_reason_fixed CHECK
        (reason IN ('token_invalid','account_blocked','forbidden')),
    CONSTRAINT account_availability_occurrence_severity_fixed CHECK
        ((reason IN ('token_invalid','account_blocked') AND severity = 'Critical') OR (reason = 'forbidden' AND severity = 'Warning')),
    CONSTRAINT account_availability_occurrence_status_fixed CHECK (status IN ('ACTIVE','RESOLVED')),
    CONSTRAINT account_availability_occurrence_times_ordered CHECK
        (first_seen_at <= last_failure_at AND confirmed_at >= first_seen_at AND (resolved_at IS NULL OR resolved_at >= confirmed_at)),
    CONSTRAINT account_availability_occurrence_account_key_valid CHECK
        (account_key = btrim(account_key) AND account_key <> '' AND octet_length(account_key) <= 385)
);
ALTER TABLE public.account_availability_checkpoints OWNER TO relay_control_migrator;
ALTER TABLE public.account_availability_occurrences OWNER TO relay_control_migrator;
ALTER TABLE public.account_request_quality_events ADD CONSTRAINT account_request_quality_auth_success_null CHECK (auth_failure_reason IS NULL OR NOT success);
CREATE UNIQUE INDEX account_availability_occurrence_active_unique
    ON public.account_availability_occurrences(node_id, account_key, reason)
    WHERE status = 'ACTIVE';
CREATE INDEX account_availability_occurrence_read_idx
    ON public.account_availability_occurrences(node_id, confirmed_at DESC, occurrence_id DESC);
CREATE INDEX account_availability_occurrence_account_idx
    ON public.account_availability_occurrences(node_id, account_key, confirmed_at DESC, occurrence_id DESC);

-- Runtime reads use these wrappers; the base tables remain inaccessible.
-- The versioned event writer preserves the v1 insert contract while carrying
-- the nullable Antigravity authentication refinement.
-- +goose StatementBegin
CREATE FUNCTION public.control_insert_account_request_quality_events_v2(events jsonb)
RETURNS bigint LANGUAGE sql SECURITY DEFINER VOLATILE SET search_path = pg_catalog AS $$
    WITH inserted AS (
        INSERT INTO public.account_request_quality_events
            (event_hash, request_id, node_id, provider, account_key, model, occurred_at, duration_ms, success, failure_class, auth_failure_reason)
        SELECT event_hash, COALESCE(request_id,''), node_id, provider, account_key, COALESCE(model,''), occurred_at,
               duration_ms, success, failure_class, auth_failure_reason
        FROM jsonb_to_recordset(events) AS e(
            event_hash text, request_id text, node_id uuid, provider text, account_key text,
            model text, occurred_at timestamptz, duration_ms bigint, success boolean,
            failure_class text, auth_failure_reason text)
        ON CONFLICT (node_id, event_hash) DO NOTHING
        RETURNING 1
    ) SELECT count(*) FROM inserted;
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_insert_account_request_quality_events_v2(jsonb) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_insert_account_request_quality_events_v2(jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_insert_account_request_quality_events_v2(jsonb) TO relay_control_runtime;

-- The source projection is shared by reconciliation and reads so worker stalls
-- cannot leave a stale AVAILABLE badge. It never grants direct table access.
-- +goose StatementBegin
CREATE FUNCTION public.control_account_availability_source_v1(target_node uuid, target_accounts text[])
RETURNS TABLE(account_key text, runtime_evidence text, auth_reason text, source_id uuid,
 source_at timestamptz, source_slot timestamptz, retry_at timestamptz, gate_reason text, gate_since timestamptz)
LANGUAGE sql SECURITY DEFINER STABLE SET search_path=pg_catalog AS $$
 SELECT i.account_key,i.availability_runtime_evidence,i.auth_failure_reason,i.current_poll_run_id,
 i.source_observed_at,i.current_scheduled_at,i.next_retry_at,
 CASE WHEN i.lifecycle <> 'present' THEN 'not_present'
 WHEN NOT EXISTS (SELECT 1 FROM public.relay_node_inventory_monitoring_activations m WHERE m.instance_id=i.instance_id AND m.active_range @> statement_timestamp()) OR p.monitoring_status IS DISTINCT FROM 'active' THEN 'not_present'
 WHEN p.instance_id IS NULL OR p.state IS DISTINCT FROM 'current' THEN 'incomplete'
 WHEN p.health_degraded OR p.health_reason IS DISTINCT FROM 'none' THEN CASE WHEN p.health_reason IN ('transport_failed','contract_invalid') THEN 'node_collection_failed' ELSE 'incomplete' END
 WHEN p.last_complete_at > statement_timestamp() OR statement_timestamp()-p.last_complete_at > interval '15 minutes' THEN 'stale'
 WHEN i.current_scheduled_at IS DISTINCT FROM p.current_scheduled_at THEN 'incomplete'
 WHEN i.availability_runtime_evidence IS NULL THEN 'unsupported_mode'
 ELSE NULL END,
 CASE WHEN statement_timestamp()-p.last_complete_at > interval '15 minutes' THEN p.last_complete_at+interval '15 minutes' ELSE COALESCE(p.health_scheduled_at,i.source_observed_at) END
 FROM public.account_inventory i LEFT JOIN public.account_inventory_provider_states p ON p.instance_id=i.instance_id AND p.provider=i.provider
 WHERE i.instance_id=target_node AND i.provider='antigravity' AND i.account_key=ANY(target_accounts);
$$;
-- +goose StatementEnd

-- Reconcile an ordered bounded page in the caller's SERIALIZABLE transaction.
-- A checkpoint lock, all evidence reads, and occurrence writes share one snapshot.
-- +goose StatementBegin
CREATE FUNCTION public.control_reconcile_account_availability_v1(after_node uuid, after_account text)
RETURNS TABLE(node_id uuid, account_key text)
LANGUAGE plpgsql SECURITY DEFINER VOLATILE SET search_path=pg_catalog AS $$
DECLARE k record; src record; cp public.account_availability_checkpoints%ROWTYPE;
 occ public.account_availability_occurrences%ROWTYPE;
 moment timestamptz:=statement_timestamp(); rsn text; new_state text; new_reason text;
 events jsonb; candidates jsonb; request_ids text[]; hashes text[]; request_times timestamptz[];
 failed_at timestamptz; first_at timestamptz; success_at timestamptz; latest_failure timestamptz;
 recovery_at timestamptz; runtime_error boolean; healthy boolean; pending boolean; conflict boolean;
 healthy_count integer; new_source boolean;
BEGIN
 IF (after_node IS NULL) <> (after_account IS NULL) THEN RAISE EXCEPTION 'invalid availability cursor' USING ERRCODE='22023'; END IF;
 FOR k IN SELECT i.instance_id,i.account_key FROM public.account_inventory i
   WHERE i.provider='antigravity' AND (after_node IS NULL OR (i.instance_id,i.account_key)>(after_node,after_account))
   ORDER BY i.instance_id,i.account_key LIMIT 100
 LOOP
  INSERT INTO public.account_availability_checkpoints(node_id,account_key) VALUES(k.instance_id,k.account_key) ON CONFLICT DO NOTHING;
  SELECT * INTO cp FROM public.account_availability_checkpoints c WHERE c.node_id=k.instance_id AND c.account_key=k.account_key FOR UPDATE;
  SELECT * INTO STRICT src FROM public.control_account_availability_source_v1(k.instance_id,ARRAY[k.account_key]);
  runtime_error:=src.gate_reason IS NULL AND src.runtime_evidence IN ('file_error','file_unavailable');
  healthy:=src.gate_reason IS NULL AND src.runtime_evidence='file_active' AND COALESCE(src.auth_reason,'other')='other';
  SELECT max(e.occurred_at) INTO success_at FROM public.account_request_quality_events e WHERE e.node_id=k.instance_id AND e.account_key=k.account_key AND e.provider='antigravity' AND e.success AND e.occurred_at BETWEEN moment-interval '7 days' AND moment;
  success_at:=greatest(success_at,cp.last_success_at);
  -- Group request IDs before checking reason. A contradictory result or reason
  -- is never resolved by first/latest wins. Hash only chooses a diagnostic ref.
  WITH scoped AS MATERIALIZED (
   SELECT e.* FROM public.account_request_quality_events e WHERE e.node_id=k.instance_id AND e.account_key=k.account_key AND e.provider='antigravity' AND e.occurred_at BETWEEN moment-interval '7 days' AND moment
  ), usable AS (
   SELECT e.* FROM scoped e WHERE NOT e.success AND e.auth_failure_reason IN ('token_invalid','account_blocked','forbidden')
    AND e.occurred_at >= moment-interval '15 minutes'
    AND e.occurred_at > COALESCE(cp.recovery_watermark,'-infinity'::timestamptz)
    AND e.occurred_at > COALESCE(success_at,'-infinity'::timestamptz)
    AND (e.request_id='' OR NOT EXISTS (SELECT 1 FROM scoped x WHERE x.request_id=e.request_id AND (x.success OR x.auth_failure_reason IS DISTINCT FROM e.auth_failure_reason)))
    AND (e.request_id='' OR NOT EXISTS (SELECT 1 FROM public.account_availability_occurrences o WHERE o.node_id=k.instance_id AND o.account_key=k.account_key AND e.request_id IN(o.confirmation_request_id_1,o.confirmation_request_id_2)))
  ) SELECT COALESCE(jsonb_agg(jsonb_build_object('request_id',u.request_id,'event_hash',u.event_hash,'reason',u.auth_failure_reason,'at',u.occurred_at) ORDER BY u.occurred_at,u.event_hash),'[]'::jsonb) INTO events FROM usable u;
  SELECT EXISTS(SELECT 1 FROM public.account_request_quality_events e JOIN public.account_request_quality_events x ON x.node_id=e.node_id AND x.account_key=e.account_key AND x.request_id=e.request_id
   WHERE e.node_id=k.instance_id AND e.account_key=k.account_key AND e.request_id<>'' AND e.provider='antigravity' AND e.occurred_at BETWEEN moment-interval '15 minutes' AND moment AND e.occurred_at>COALESCE(success_at,'-infinity'::timestamptz)
   AND x.occurred_at BETWEEN moment-interval '7 days' AND moment AND NOT e.success AND e.auth_failure_reason IN ('token_invalid','account_blocked','forbidden') AND (x.success OR x.auth_failure_reason IS DISTINCT FROM e.auth_failure_reason)) INTO conflict;
  -- Equal-time failures are excluded from confirmation, but remain pending
  -- evidence so a concurrent success/failure cannot be presented as healthy.
  pending:=jsonb_array_length(events)>0 OR conflict OR EXISTS(
   SELECT 1 FROM public.account_request_quality_events e
   WHERE e.node_id=k.instance_id AND e.account_key=k.account_key AND e.provider='antigravity'
     AND NOT e.success AND e.auth_failure_reason IN ('token_invalid','account_blocked','forbidden')
     AND e.occurred_at BETWEEN moment-interval '15 minutes' AND moment
     AND e.occurred_at = success_at);
  SELECT max((e->>'at')::timestamptz) INTO latest_failure FROM jsonb_array_elements(events)e;
  latest_failure:=greatest(cp.last_failure_at,latest_failure);
  new_source:=cp.last_runtime_source_at IS NULL OR
   (src.source_at>cp.last_runtime_source_at AND src.source_slot>cp.last_runtime_slot
    AND (src.source_id IS NULL OR cp.last_runtime_source_id IS NULL OR src.source_id<>cp.last_runtime_source_id));
  healthy_count:=cp.consecutive_healthy_sources;
  IF latest_failure > COALESCE(cp.last_failure_at,'-infinity'::timestamptz) AND latest_failure >= cp.last_runtime_source_at THEN healthy_count:=0; END IF;
  IF NOT healthy OR conflict OR (latest_failure IS NOT NULL AND latest_failure >= src.source_at) THEN healthy_count:=0;
  ELSIF new_source THEN
   IF src.source_at > COALESCE(latest_failure,'-infinity'::timestamptz) AND src.source_at > COALESCE(cp.last_runtime_source_at,'-infinity'::timestamptz) THEN
    healthy_count:=CASE WHEN healthy_count>0 AND src.source_slot=cp.last_runtime_slot+interval '5 minutes' THEN least(cp.consecutive_healthy_sources+1,2) ELSE 1 END;
   ELSE healthy_count:=0; END IF;
  END IF;
  recovery_at:=cp.recovery_watermark;
  FOREACH rsn IN ARRAY ARRAY['account_blocked','token_invalid','forbidden'] LOOP
   SELECT * INTO occ FROM public.account_availability_occurrences o WHERE o.node_id=k.instance_id AND o.account_key=k.account_key AND o.reason=rsn AND o.status='ACTIVE' FOR UPDATE;
   SELECT COALESCE(jsonb_agg(e ORDER BY (e->>'at')::timestamptz,e->>'event_hash'),'[]'::jsonb),min((e->>'at')::timestamptz),max((e->>'at')::timestamptz)
    INTO candidates,first_at,failed_at FROM jsonb_array_elements(events)e WHERE e->>'reason'=rsn;
   -- Equal-time success cannot establish causality, and cannot confirm a fault.
   IF failed_at IS NOT NULL AND failed_at <= COALESCE(success_at,'-infinity'::timestamptz) THEN candidates:='[]'::jsonb; END IF;
   IF occ.occurrence_id IS NOT NULL THEN
    failed_at:=greatest(occ.last_failure_at,failed_at);
    IF runtime_error AND (src.auth_reason=rsn OR COALESCE(src.auth_reason,'other')='other') THEN failed_at:=greatest(failed_at,src.source_at); END IF;
    IF failed_at>occ.last_failure_at THEN UPDATE public.account_availability_occurrences SET last_failure_at=failed_at WHERE occurrence_id=occ.occurrence_id; END IF;
    IF NOT conflict AND ((success_at>failed_at AND NOT (runtime_error AND src.source_at>=success_at)) OR (healthy AND healthy_count>=2 AND src.source_at>failed_at)) THEN
     UPDATE public.account_availability_occurrences SET status='RESOLVED',resolved_at=moment,recovery_source_id=CASE WHEN healthy AND healthy_count>=2 THEN src.source_id ELSE NULL END,recovery_source_at=CASE WHEN success_at>failed_at THEN success_at ELSE src.source_at END WHERE occurrence_id=occ.occurrence_id;
     recovery_at:=greatest(recovery_at,CASE WHEN success_at>failed_at THEN success_at ELSE src.source_at END);
    END IF;
   ELSIF src.gate_reason IS NULL AND src.runtime_evidence <> 'file_disabled' AND NOT conflict AND jsonb_array_length(candidates)>0 THEN
    SELECT array_agg(d.id ORDER BY d.at,d.id),array_agg(d.hash ORDER BY d.at,d.id),array_agg(d.at ORDER BY d.at,d.id) INTO request_ids,hashes,request_times
    FROM (SELECT e->>'request_id' id,min(e->>'event_hash') hash,min((e->>'at')::timestamptz) at FROM jsonb_array_elements(candidates)e WHERE e->>'request_id'<>'' GROUP BY e->>'request_id' ORDER BY at,id LIMIT 2)d;
    IF (rsn<>'forbidden' AND cardinality(request_ids)>=2) OR (runtime_error AND src.source_at BETWEEN moment-interval '15 minutes' AND moment AND src.source_at>COALESCE(success_at,'-infinity'::timestamptz) AND (src.auth_reason IS NULL OR src.auth_reason IN ('other',rsn))) THEN
     IF rsn='forbidden' OR COALESCE(cardinality(request_ids),0)<2 THEN
      request_ids:=ARRAY[NULLIF(candidates->0->>'request_id','')];hashes:=ARRAY[candidates->0->>'event_hash'];
     END IF;
     INSERT INTO public.account_availability_occurrences(node_id,account_key,reason,severity,status,first_seen_at,last_failure_at,confirmed_at,confirmation_request_id_1,confirmation_request_id_2,confirmation_event_hash_1,confirmation_event_hash_2,confirmation_source_id,confirmation_source_at)
      VALUES(k.instance_id,k.account_key,rsn,CASE WHEN rsn='forbidden' THEN 'Warning' ELSE 'Critical' END,'ACTIVE',first_at,greatest(failed_at,CASE WHEN runtime_error THEN src.source_at ELSE NULL END),moment,request_ids[1],request_ids[2],hashes[1],hashes[2],CASE WHEN runtime_error THEN src.source_id ELSE NULL END,CASE WHEN runtime_error THEN src.source_at ELSE NULL END);
     healthy_count:=0;
    END IF;
   END IF;
  END LOOP;
  new_state:='UNKNOWN';new_reason:=COALESCE(src.gate_reason,'unproven');
  IF src.gate_reason IS NULL THEN
   IF src.runtime_evidence='file_disabled' THEN new_state:='DISABLED';new_reason:='disabled';
   ELSE
    SELECT o.reason INTO new_reason FROM public.account_availability_occurrences o WHERE o.node_id=k.instance_id AND o.account_key=k.account_key AND o.status='ACTIVE' AND (o.reason<>'forbidden' OR runtime_error)
    ORDER BY CASE o.reason WHEN 'account_blocked' THEN 1 WHEN 'token_invalid' THEN 2 ELSE 3 END LIMIT 1;
    IF new_reason IS NOT NULL THEN new_state:=upper(new_reason);
    ELSIF conflict OR pending OR EXISTS(SELECT 1 FROM public.account_availability_occurrences o WHERE o.node_id=k.instance_id AND o.account_key=k.account_key AND o.status='ACTIVE') THEN new_reason:=CASE WHEN conflict THEN 'conflicting_evidence' ELSE 'pending_confirmation' END;
    ELSIF runtime_error THEN new_reason:=CASE WHEN src.auth_reason IN ('token_invalid','account_blocked','forbidden') THEN 'pending_confirmation' ELSE 'runtime_unavailable' END;
    ELSIF src.runtime_evidence='file_active' AND COALESCE(src.auth_reason,'other')='other' THEN new_state:='AVAILABLE';new_reason:='available';
    ELSE new_reason:=CASE WHEN src.retry_at>moment THEN 'retry_wait' WHEN src.auth_reason IN ('token_invalid','account_blocked','forbidden') THEN 'pending_confirmation' ELSE 'unproven' END; END IF;
   END IF;
  END IF;
  UPDATE public.account_availability_checkpoints SET state=new_state,reason=new_reason,
   since=CASE WHEN cp.state IS DISTINCT FROM new_state OR cp.reason IS DISTINCT FROM new_reason OR cp.since IS NULL THEN CASE WHEN src.gate_reason='stale' THEN src.gate_since ELSE moment END ELSE cp.since END,
   last_runtime_source_id=COALESCE(src.source_id,cp.last_runtime_source_id),last_runtime_source_at=src.source_at,last_runtime_slot=src.source_slot,
   consecutive_healthy_sources=healthy_count,last_failure_at=latest_failure,last_success_at=success_at,recovery_watermark=recovery_at,updated_at=moment
   WHERE account_availability_checkpoints.node_id=k.instance_id AND account_availability_checkpoints.account_key=k.account_key;
  node_id:=k.instance_id;account_key:=k.account_key;RETURN NEXT;
 END LOOP;
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_account_availability_source_v1(uuid,text[]) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_account_availability_source_v1(uuid,text[]) FROM PUBLIC;
ALTER FUNCTION public.control_reconcile_account_availability_v1(uuid,text) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_reconcile_account_availability_v1(uuid,text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_reconcile_account_availability_v1(uuid,text) TO relay_control_runtime;

-- +goose StatementBegin
CREATE FUNCTION public.control_query_account_availability_v1(target_node uuid, target_accounts text[])
RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER STABLE SET search_path=pg_catalog AS $$
BEGIN
 IF target_node IS NULL OR target_accounts IS NULL OR cardinality(target_accounts)>100 THEN
  RAISE EXCEPTION 'invalid availability query' USING ERRCODE='22023'; END IF;
 IF NOT EXISTS(SELECT 1 FROM public.relay_node_assets WHERE instance_id=target_node) THEN
  RAISE EXCEPTION 'node not found' USING ERRCODE='P0404'; END IF;
 RETURN (SELECT COALESCE(jsonb_agg(jsonb_build_object('account_key',src.account_key,
  'state',CASE WHEN src.gate_reason IS NOT NULL THEN 'UNKNOWN'
    WHEN src.runtime_evidence='file_disabled' THEN 'DISABLED'
    WHEN c.last_runtime_source_at IS DISTINCT FROM src.source_at THEN 'UNKNOWN'
    WHEN c.state='FORBIDDEN' AND src.runtime_evidence NOT IN ('file_error','file_unavailable') THEN 'UNKNOWN'
    ELSE COALESCE(c.state,'UNKNOWN') END,
  'reason',CASE WHEN src.gate_reason IS NOT NULL THEN src.gate_reason
    WHEN src.runtime_evidence='file_disabled' THEN 'disabled'
    WHEN c.last_runtime_source_at IS DISTINCT FROM src.source_at THEN 'unproven'
    WHEN c.state='FORBIDDEN' AND src.runtime_evidence NOT IN ('file_error','file_unavailable') THEN 'pending_confirmation'
    ELSE COALESCE(c.reason,'unproven') END,
  'since',CASE WHEN src.gate_reason='stale' THEN src.gate_since
    WHEN c.last_runtime_source_at IS DISTINCT FROM src.source_at OR c.state IS NULL THEN NULL ELSE c.since END
 ) ORDER BY src.account_key),'[]'::jsonb)
 FROM public.control_account_availability_source_v1(target_node,target_accounts) src
 LEFT JOIN public.account_availability_checkpoints c ON c.node_id=target_node AND c.account_key=src.account_key);
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_query_account_availability_occurrences_v1(
    target_node uuid, target_account text, target_status text,
    after_confirmed_at timestamptz, after_occurrence_id uuid, page_limit integer
) RETURNS SETOF jsonb LANGUAGE plpgsql SECURITY DEFINER STABLE SET search_path = pg_catalog AS $$
DECLARE r record;
BEGIN
    IF target_node IS NULL OR target_status NOT IN ('ACTIVE','RESOLVED')
       OR page_limit IS NULL OR page_limit NOT BETWEEN 1 AND 100
       OR (target_account IS NOT NULL AND (target_account = '' OR octet_length(target_account) > 385))
       OR ((after_confirmed_at IS NULL) <> (after_occurrence_id IS NULL)) THEN
        RAISE EXCEPTION 'invalid account availability occurrence query' USING ERRCODE = '22023';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM public.relay_node_assets WHERE instance_id=target_node) THEN
        RAISE EXCEPTION 'node not found' USING ERRCODE='P0404';
    END IF;
    FOR r IN
        SELECT occurrence_id, node_id, account_key, reason, severity, status,
               first_seen_at, last_failure_at, confirmed_at, resolved_at
        FROM public.account_availability_occurrences
        WHERE node_id = target_node AND status = target_status
          AND (target_account IS NULL OR account_key = target_account)
          AND (after_confirmed_at IS NULL OR (confirmed_at, occurrence_id) < (after_confirmed_at, after_occurrence_id))
        ORDER BY confirmed_at DESC, occurrence_id DESC
        LIMIT page_limit + 1
    LOOP
        RETURN NEXT jsonb_build_object(
            'occurrence_id', r.occurrence_id, 'node_id', r.node_id,
            'account_key', r.account_key, 'reason', r.reason, 'severity', r.severity,
            'status', r.status, 'first_seen_at', r.first_seen_at,
            'last_failure_at', r.last_failure_at, 'confirmed_at', r.confirmed_at,
            'resolved_at', r.resolved_at
        );
    END LOOP;
END;
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_query_account_availability_v1(uuid,text[]) OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_query_account_availability_occurrences_v1(uuid,text,text,timestamptz,uuid,integer) OWNER TO relay_control_migrator;
REVOKE ALL ON TABLE public.account_availability_checkpoints, public.account_availability_occurrences FROM PUBLIC, relay_control_runtime;
REVOKE ALL ON FUNCTION public.control_query_account_availability_v1(uuid,text[]) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.control_query_account_availability_occurrences_v1(uuid,text,text,timestamptz,uuid,integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_query_account_availability_v1(uuid,text[]) TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_query_account_availability_occurrences_v1(uuid,text,text,timestamptz,uuid,integer) TO relay_control_runtime;

-- +goose StatementBegin
CREATE FUNCTION public.control_finalize_account_inventory_poll_run_v2(
    target_poll_run_id uuid,
    expected_fencing_token uuid,
    result_transport_success boolean,
    result_response_shape_valid boolean,
    result_contract_valid boolean,
    result_inventory_mode text,
    result_node_identity_complete boolean,
    result_snapshot_complete boolean,
    result_degraded boolean,
    result_result text,
    result_reason text,
    result_source_record_count integer,
    result_identifiable_record_count integer,
    result_unidentified_record_count integer,
    result_unsupported_provider_count integer,
    result_out_of_scope_provider_count integer,
    result_node_version text,
    result_node_commit text,
    provider_results jsonb,
    snapshot_items jsonb,
    duplicate_evidence jsonb
) RETURNS SETOF public.account_inventory_poll_runs
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    database_now timestamptz := clock_timestamp();
    observation_time timestamptz;
    run_record public.account_inventory_poll_runs%ROWTYPE;
    expected_providers text[];
    supplied_providers text[];
    current_policy_version uuid;
    policy_matches boolean;
    provider_item jsonb;
    snapshot_item jsonb;
    duplicate_item jsonb;
    provider_name text;
    provider_identifiable integer;
    provider_missing integer;
    provider_duplicate integer;
    provider_identity_complete boolean;
    provider_snapshot_complete boolean;
    provider_degraded boolean;
    provider_reason text;
    provider_promotion_applied boolean;
    provider_skip_reason text;
    account_key_value text;
    email_value text;
    status_value text;
    success_value bigint;
    failed_value bigint;
    recent_value bigint;
    last_refresh_value bigint;
    next_retry_value bigint;
    updated_at_value bigint;
    availability_runtime_evidence_value text;
    auth_failure_reason_value text;
    occurrence_value integer;
    supplied_item_count bigint;
    supplied_duplicate_count bigint;
    supplied_occurrences bigint;
    supplied_total bigint;
    supplied_distinct bigint;
    summed_identifiable bigint := 0;
    summed_missing bigint := 0;
    all_snapshot_complete boolean := true;
BEGIN
    SELECT * INTO run_record
    FROM public.account_inventory_poll_runs AS run
    WHERE run.poll_run_id = target_poll_run_id
      AND run.status = 'running'
      AND run.lease_fencing_token = expected_fencing_token
      AND run.lease_expires_at > database_now
    FOR UPDATE;
    IF NOT FOUND THEN
        RETURN;
    END IF;

    SELECT policy.active_providers INTO expected_providers
    FROM public.provider_inventory_policy_versions AS policy
    WHERE policy.policy_version_id = run_record.provider_policy_version
      AND policy.node_type = run_record.node_type
      AND policy.driver_contract_version = run_record.driver_contract_version;

    IF jsonb_typeof(provider_results) IS DISTINCT FROM 'array'
       OR jsonb_array_length(provider_results) <> cardinality(expected_providers)
       OR jsonb_typeof(snapshot_items) IS DISTINCT FROM 'array'
       OR jsonb_array_length(snapshot_items) > 1000
       OR jsonb_typeof(duplicate_evidence) IS DISTINCT FROM 'array'
       OR jsonb_array_length(duplicate_evidence) > 1000 THEN
        RAISE EXCEPTION 'account inventory snapshot finalize set is invalid'
            USING ERRCODE = '23514';
    END IF;
    SELECT array_agg(value->>'provider' ORDER BY value->>'provider')
    INTO supplied_providers
    FROM jsonb_array_elements(provider_results) AS item(value);
    IF supplied_providers IS DISTINCT FROM expected_providers THEN
        RAISE EXCEPTION 'account inventory provider result set does not match pinned policy'
            USING ERRCODE = '23514';
    END IF;

    SELECT count(*), count(DISTINCT (value->>'provider', value->>'account_key'))
    INTO supplied_total, supplied_distinct
    FROM jsonb_array_elements(snapshot_items) AS item(value);
    IF supplied_total <> supplied_distinct THEN
        RAISE EXCEPTION 'duplicate account inventory snapshot candidate'
            USING ERRCODE = '23514';
    END IF;
    SELECT count(*), count(DISTINCT (value->>'provider', value->>'account_key'))
    INTO supplied_total, supplied_distinct
    FROM jsonb_array_elements(duplicate_evidence) AS item(value);
    IF supplied_total <> supplied_distinct OR EXISTS (
        SELECT 1
        FROM jsonb_array_elements(snapshot_items) AS candidate(value)
        JOIN jsonb_array_elements(duplicate_evidence) AS duplicate(value)
          ON duplicate.value->>'provider' = candidate.value->>'provider'
         AND duplicate.value->>'account_key' = candidate.value->>'account_key'
    ) THEN
        RAISE EXCEPTION 'duplicate account inventory evidence key'
            USING ERRCODE = '23514';
    END IF;

    FOR duplicate_item IN SELECT value FROM jsonb_array_elements(duplicate_evidence) AS item(value)
    LOOP
        IF jsonb_typeof(duplicate_item) IS DISTINCT FROM 'object'
           OR NOT (duplicate_item ?& ARRAY['provider','account_key','occurrence_count'])
           OR duplicate_item - ARRAY['provider','account_key','occurrence_count'] <> '{}'::jsonb
           OR jsonb_typeof(duplicate_item->'provider') IS DISTINCT FROM 'string'
           OR jsonb_typeof(duplicate_item->'account_key') IS DISTINCT FROM 'string'
           OR jsonb_typeof(duplicate_item->'occurrence_count') IS DISTINCT FROM 'number' THEN
            RAISE EXCEPTION 'invalid account inventory duplicate evidence shape'
                USING ERRCODE = '23514';
        END IF;
        BEGIN
            provider_name := duplicate_item->>'provider';
            account_key_value := duplicate_item->>'account_key';
            occurrence_value := (duplicate_item->>'occurrence_count')::integer;
        EXCEPTION WHEN invalid_text_representation OR numeric_value_out_of_range THEN
            RAISE EXCEPTION 'invalid account inventory duplicate evidence value'
                USING ERRCODE = '23514';
        END;
        IF provider_name <> ALL(expected_providers)
           OR account_key_value IS NULL
           OR octet_length(account_key_value) NOT BETWEEN 3 AND 385
           OR account_key_value NOT LIKE provider_name || ':%'
           OR substring(account_key_value FROM octet_length(provider_name) + 2)
                IS DISTINCT FROM lower(btrim(substring(
                    account_key_value FROM octet_length(provider_name) + 2
                )))
           OR substring(account_key_value FROM octet_length(provider_name) + 2) ~ '[[:cntrl:]]'
           OR octet_length(substring(account_key_value FROM octet_length(provider_name) + 2))
                NOT BETWEEN 1 AND 320
           OR occurrence_value NOT BETWEEN 2 AND 1000 THEN
            RAISE EXCEPTION 'invalid account inventory duplicate evidence value'
                USING ERRCODE = '23514';
        END IF;
    END LOOP;

    PERFORM 1
    FROM public.provider_inventory_policy_bindings AS binding
    WHERE binding.node_type = run_record.node_type
      AND binding.driver_contract_version = run_record.driver_contract_version
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'account inventory policy binding is unavailable'
            USING ERRCODE = '23514';
    END IF;
    database_now := clock_timestamp();
    IF run_record.lease_expires_at <= database_now THEN
        RAISE EXCEPTION 'account inventory poll lease expired during finalize'
            USING ERRCODE = 'P0002';
    END IF;
    observation_time := database_now;
    SELECT activation.policy_version_id INTO current_policy_version
    FROM public.provider_inventory_policy_activations AS activation
    WHERE activation.node_type = run_record.node_type
      AND activation.driver_contract_version = run_record.driver_contract_version
      AND activation.active_range @> database_now;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'account inventory active policy is unavailable'
            USING ERRCODE = '23514';
    END IF;
    policy_matches := current_policy_version = run_record.provider_policy_version;

    FOR provider_item IN SELECT value FROM jsonb_array_elements(provider_results) AS item(value)
    LOOP
        IF jsonb_typeof(provider_item) IS DISTINCT FROM 'object'
           OR NOT (provider_item ?& ARRAY[
               'provider','identifiable_count','missing_identity_count',
               'duplicate_identity_count','identity_complete','snapshot_complete',
               'degraded','reason'
           ])
           OR provider_item - ARRAY[
               'provider','identifiable_count','missing_identity_count',
               'duplicate_identity_count','identity_complete','snapshot_complete',
               'degraded','reason'
           ] <> '{}'::jsonb
           OR jsonb_typeof(provider_item->'provider') IS DISTINCT FROM 'string'
           OR jsonb_typeof(provider_item->'identifiable_count') IS DISTINCT FROM 'number'
           OR jsonb_typeof(provider_item->'missing_identity_count') IS DISTINCT FROM 'number'
           OR jsonb_typeof(provider_item->'duplicate_identity_count') IS DISTINCT FROM 'number'
           OR jsonb_typeof(provider_item->'identity_complete') IS DISTINCT FROM 'boolean'
           OR jsonb_typeof(provider_item->'snapshot_complete') IS DISTINCT FROM 'boolean'
           OR jsonb_typeof(provider_item->'degraded') IS DISTINCT FROM 'boolean'
           OR jsonb_typeof(provider_item->'reason') IS DISTINCT FROM 'string' THEN
            RAISE EXCEPTION 'invalid account inventory provider result shape'
                USING ERRCODE = '23514';
        END IF;
        BEGIN
            provider_name := provider_item->>'provider';
            provider_identifiable := (provider_item->>'identifiable_count')::integer;
            provider_missing := (provider_item->>'missing_identity_count')::integer;
            provider_duplicate := (provider_item->>'duplicate_identity_count')::integer;
            provider_identity_complete := (provider_item->>'identity_complete')::boolean;
            provider_snapshot_complete := (provider_item->>'snapshot_complete')::boolean;
            provider_degraded := (provider_item->>'degraded')::boolean;
            provider_reason := provider_item->>'reason';
        EXCEPTION WHEN invalid_text_representation OR numeric_value_out_of_range THEN
            RAISE EXCEPTION 'invalid account inventory provider result value'
                USING ERRCODE = '23514';
        END;
        IF NOT result_node_identity_complete AND provider_snapshot_complete THEN
            RAISE EXCEPTION 'account inventory provider completeness contradicts node identity'
                USING ERRCODE = '23514';
        END IF;
        summed_identifiable := summed_identifiable + provider_identifiable;
        summed_missing := summed_missing + provider_missing;
        all_snapshot_complete := all_snapshot_complete AND provider_snapshot_complete;

        SELECT count(*), coalesce(sum((value->>'occurrence_count')::integer), 0)
        INTO supplied_duplicate_count, supplied_occurrences
        FROM jsonb_array_elements(duplicate_evidence) AS duplicate(value)
        WHERE value->>'provider' = provider_name;
        SELECT count(*) INTO supplied_item_count
        FROM jsonb_array_elements(snapshot_items) AS candidate(value)
        WHERE value->>'provider' = provider_name;
        IF supplied_duplicate_count <> provider_duplicate
           OR supplied_item_count + supplied_occurrences <> provider_identifiable
           OR (provider_snapshot_complete AND supplied_duplicate_count <> 0) THEN
            RAISE EXCEPTION 'account inventory provider account counts are inconsistent'
                USING ERRCODE = '23514';
        END IF;

        IF NOT policy_matches THEN
            provider_promotion_applied := false;
            provider_skip_reason := 'policy_changed';
        ELSIF NOT result_transport_success THEN
            provider_promotion_applied := false;
            provider_skip_reason := 'transport_failed';
        ELSIF NOT result_contract_valid THEN
            provider_promotion_applied := false;
            provider_skip_reason := 'contract_invalid';
        ELSIF result_inventory_mode = 'disk_fallback' THEN
            provider_promotion_applied := false;
            provider_skip_reason := 'disk_fallback';
        ELSIF NOT result_node_identity_complete THEN
            provider_promotion_applied := false;
            provider_skip_reason := 'provider_identity_incomplete';
        ELSIF provider_duplicate > 0 THEN
            provider_promotion_applied := false;
            provider_skip_reason := 'provider_duplicate';
        ELSIF NOT provider_identity_complete OR NOT provider_snapshot_complete THEN
            provider_promotion_applied := false;
            provider_skip_reason := 'provider_identity_incomplete';
        ELSIF EXISTS (
            SELECT 1 FROM public.account_inventory_provider_states AS state
            WHERE state.instance_id = run_record.instance_id
              AND state.provider = provider_name
              AND state.current_scheduled_at >= run_record.scheduled_at
        ) THEN
            provider_promotion_applied := false;
            provider_skip_reason := 'stale_poll';
        ELSE
            provider_promotion_applied := true;
            provider_skip_reason := NULL;
        END IF;

        INSERT INTO public.account_inventory_poll_provider_results (
            poll_run_id, provider, identifiable_count, missing_identity_count,
            duplicate_identity_count, identity_complete, snapshot_complete,
            degraded, reason, promotion_applied, promotion_skipped_reason
        ) VALUES (
            target_poll_run_id, provider_name, provider_identifiable, provider_missing,
            provider_duplicate, provider_identity_complete, provider_snapshot_complete,
            provider_degraded, provider_reason, provider_promotion_applied, provider_skip_reason
        );

    END LOOP;

    FOR snapshot_item IN SELECT value FROM jsonb_array_elements(snapshot_items) AS item(value)
    LOOP
        IF jsonb_typeof(snapshot_item) IS DISTINCT FROM 'object'
           OR NOT (snapshot_item ?& ARRAY[
               'provider','account_key','email','basic_status','success_count',
               'failed_count','recent_request_count','last_refresh_unix',
               'next_retry_unix','updated_at_unix'
           ])
           OR snapshot_item - ARRAY[
               'provider','account_key','email','basic_status','success_count',
               'failed_count','recent_request_count','last_refresh_unix',
               'next_retry_unix','updated_at_unix','availability_runtime_evidence','auth_failure_reason'
           ] <> '{}'::jsonb
           OR jsonb_typeof(snapshot_item->'provider') IS DISTINCT FROM 'string'
           OR jsonb_typeof(snapshot_item->'account_key') IS DISTINCT FROM 'string'
           OR jsonb_typeof(snapshot_item->'email') IS DISTINCT FROM 'string'
           OR jsonb_typeof(snapshot_item->'basic_status') IS DISTINCT FROM 'string'
           OR jsonb_typeof(snapshot_item->'success_count') IS DISTINCT FROM 'number'
           OR jsonb_typeof(snapshot_item->'failed_count') IS DISTINCT FROM 'number'
           OR jsonb_typeof(snapshot_item->'recent_request_count') IS DISTINCT FROM 'number'
           OR jsonb_typeof(snapshot_item->'last_refresh_unix') NOT IN ('number','null')
           OR jsonb_typeof(snapshot_item->'next_retry_unix') NOT IN ('number','null')
           OR jsonb_typeof(snapshot_item->'updated_at_unix') NOT IN ('number','null')
           OR jsonb_typeof(snapshot_item->'availability_runtime_evidence') NOT IN ('string','null')
           OR jsonb_typeof(snapshot_item->'auth_failure_reason') NOT IN ('string','null') THEN
            RAISE EXCEPTION 'invalid account inventory snapshot item shape'
                USING ERRCODE = '23514';
        END IF;
        BEGIN
            provider_name := snapshot_item->>'provider';
            account_key_value := snapshot_item->>'account_key';
            email_value := snapshot_item->>'email';
            status_value := snapshot_item->>'basic_status';
            success_value := (snapshot_item->>'success_count')::bigint;
            failed_value := (snapshot_item->>'failed_count')::bigint;
            recent_value := (snapshot_item->>'recent_request_count')::bigint;
            last_refresh_value := CASE WHEN snapshot_item->'last_refresh_unix' = 'null'::jsonb
                THEN NULL ELSE (snapshot_item->>'last_refresh_unix')::bigint END;
            next_retry_value := CASE WHEN snapshot_item->'next_retry_unix' = 'null'::jsonb
                THEN NULL ELSE (snapshot_item->>'next_retry_unix')::bigint END;
            updated_at_value := CASE WHEN snapshot_item->'updated_at_unix' = 'null'::jsonb
                THEN NULL ELSE (snapshot_item->>'updated_at_unix')::bigint END;
            availability_runtime_evidence_value := NULLIF(snapshot_item->>'availability_runtime_evidence', '');
            auth_failure_reason_value := NULLIF(snapshot_item->>'auth_failure_reason', '');
        EXCEPTION WHEN invalid_text_representation OR numeric_value_out_of_range THEN
            RAISE EXCEPTION 'invalid account inventory snapshot item value'
                USING ERRCODE = '23514';
        END;
        IF provider_name <> ALL(expected_providers)
           OR account_key_value IS DISTINCT FROM provider_name || ':' || email_value
           OR email_value IS NULL OR octet_length(email_value) NOT BETWEEN 1 AND 320
           OR email_value IS DISTINCT FROM lower(btrim(email_value))
           OR email_value ~ '[[:cntrl:]]'
           OR status_value NOT IN ('disabled','unavailable','error','active','unknown')
           OR success_value < 0 OR failed_value < 0 OR recent_value NOT BETWEEN 0 AND 1000
           OR (last_refresh_value IS NOT NULL AND last_refresh_value NOT BETWEEN 1 AND 253402300799)
           OR (next_retry_value IS NOT NULL AND next_retry_value NOT BETWEEN 1 AND 253402300799)
           OR (updated_at_value IS NOT NULL AND updated_at_value NOT BETWEEN 1 AND 253402300799)
           OR (availability_runtime_evidence_value IS NOT NULL AND availability_runtime_evidence_value NOT IN ('file_active','file_disabled','file_error','file_unavailable','file_unknown'))
           OR (auth_failure_reason_value IS NOT NULL AND auth_failure_reason_value NOT IN ('token_invalid','account_blocked','forbidden','other')) THEN
            RAISE EXCEPTION 'invalid account inventory snapshot item value'
                USING ERRCODE = '23514';
        END IF;
        IF EXISTS (
            SELECT 1 FROM public.account_inventory_poll_provider_results AS result
            WHERE result.poll_run_id = target_poll_run_id
              AND result.provider = provider_name
              AND result.promotion_applied
        ) THEN
            INSERT INTO public.account_inventory_snapshot_items (
                poll_run_id, instance_id, provider, account_key, normalized_email,
                basic_status, success_count, failed_count, recent_request_count,
                last_refresh_at, next_retry_at, source_updated_at, observed_at,
                availability_runtime_evidence, auth_failure_reason
            ) VALUES (
                target_poll_run_id, run_record.instance_id, provider_name,
                account_key_value, email_value, status_value, success_value,
                failed_value, recent_value,
                CASE WHEN last_refresh_value IS NULL THEN NULL ELSE to_timestamp(last_refresh_value) END,
                CASE WHEN next_retry_value IS NULL THEN NULL ELSE to_timestamp(next_retry_value) END,
                CASE WHEN updated_at_value IS NULL THEN NULL ELSE to_timestamp(updated_at_value) END,
                observation_time, availability_runtime_evidence_value, auth_failure_reason_value
            );
        END IF;
    END LOOP;

    FOR duplicate_item IN SELECT value FROM jsonb_array_elements(duplicate_evidence) AS item(value)
    LOOP
        IF jsonb_typeof(duplicate_item) IS DISTINCT FROM 'object'
           OR NOT (duplicate_item ?& ARRAY['provider','account_key','occurrence_count'])
           OR duplicate_item - ARRAY['provider','account_key','occurrence_count'] <> '{}'::jsonb
           OR jsonb_typeof(duplicate_item->'provider') IS DISTINCT FROM 'string'
           OR jsonb_typeof(duplicate_item->'account_key') IS DISTINCT FROM 'string'
           OR jsonb_typeof(duplicate_item->'occurrence_count') IS DISTINCT FROM 'number' THEN
            RAISE EXCEPTION 'invalid account inventory duplicate evidence shape'
                USING ERRCODE = '23514';
        END IF;
        BEGIN
            provider_name := duplicate_item->>'provider';
            account_key_value := duplicate_item->>'account_key';
            occurrence_value := (duplicate_item->>'occurrence_count')::integer;
        EXCEPTION WHEN invalid_text_representation OR numeric_value_out_of_range THEN
            RAISE EXCEPTION 'invalid account inventory duplicate evidence value'
                USING ERRCODE = '23514';
        END;
        IF provider_name <> ALL(expected_providers)
           OR account_key_value IS NULL
           OR octet_length(account_key_value) NOT BETWEEN 3 AND 385
           OR account_key_value NOT LIKE provider_name || ':%'
           OR substring(account_key_value FROM octet_length(provider_name) + 2)
                IS DISTINCT FROM lower(btrim(substring(
                    account_key_value FROM octet_length(provider_name) + 2
                )))
           OR substring(account_key_value FROM octet_length(provider_name) + 2) ~ '[[:cntrl:]]'
           OR octet_length(substring(account_key_value FROM octet_length(provider_name) + 2))
                NOT BETWEEN 1 AND 320
           OR occurrence_value NOT BETWEEN 2 AND 1000 THEN
            RAISE EXCEPTION 'invalid account inventory duplicate evidence value'
                USING ERRCODE = '23514';
        END IF;
        INSERT INTO public.account_inventory_poll_duplicates (
            poll_run_id, instance_id, provider, account_key,
            occurrence_count, observed_at
        ) VALUES (
            target_poll_run_id, run_record.instance_id, provider_name,
            account_key_value, occurrence_value, observation_time
        );
    END LOOP;

    IF summed_identifiable <> result_identifiable_record_count
       OR summed_missing > result_unidentified_record_count
       OR result_snapshot_complete <> (result_node_identity_complete AND all_snapshot_complete)
       OR result_source_record_count <> result_identifiable_record_count
            + result_unidentified_record_count
            + result_unsupported_provider_count
            + result_out_of_scope_provider_count THEN
        RAISE EXCEPTION 'account inventory aggregate result is inconsistent'
            USING ERRCODE = '23514';
    END IF;

    database_now := clock_timestamp();
    IF run_record.lease_expires_at <= database_now THEN
        RAISE EXCEPTION 'account inventory poll lease expired during finalize'
            USING ERRCODE = 'P0002';
    END IF;
    INSERT INTO public.account_inventory_provider_states (
        instance_id, provider, current_poll_run_id, current_scheduled_at,
        last_complete_at, source_observed_at, source_node_version,
        source_node_commit, state, updated_at
    )
    SELECT run_record.instance_id, result.provider, target_poll_run_id,
           run_record.scheduled_at, observation_time, observation_time,
           result_node_version, result_node_commit, 'current', database_now
    FROM public.account_inventory_poll_provider_results AS result
    WHERE result.poll_run_id = target_poll_run_id
      AND result.promotion_applied
    ON CONFLICT (instance_id, provider) DO UPDATE
    SET current_poll_run_id = EXCLUDED.current_poll_run_id,
        current_scheduled_at = EXCLUDED.current_scheduled_at,
        last_complete_at = EXCLUDED.last_complete_at,
        source_observed_at = EXCLUDED.source_observed_at,
        source_node_version = EXCLUDED.source_node_version,
        source_node_commit = EXCLUDED.source_node_commit,
        state = EXCLUDED.state,
        updated_at = EXCLUDED.updated_at;

    database_now := clock_timestamp();
    IF run_record.lease_expires_at <= database_now THEN
        RAISE EXCEPTION 'account inventory poll lease expired during finalize'
            USING ERRCODE = 'P0002';
    END IF;

    RETURN QUERY
    UPDATE public.account_inventory_poll_runs AS run
    SET status = 'finalized', finalized_at = database_now, observed_at = observation_time,
        lease_expires_at = NULL, lease_fencing_token = NULL,
        transport_success = result_transport_success,
        response_shape_valid = result_response_shape_valid,
        contract_valid = result_contract_valid,
        inventory_mode = result_inventory_mode,
        node_identity_complete = result_node_identity_complete,
        snapshot_complete = result_snapshot_complete,
        degraded = result_degraded,
        result = result_result, reason = result_reason,
        source_record_count = result_source_record_count,
        identifiable_record_count = result_identifiable_record_count,
        unidentified_record_count = result_unidentified_record_count,
        unsupported_provider_count = result_unsupported_provider_count,
        out_of_scope_provider_count = result_out_of_scope_provider_count,
        node_version = result_node_version, node_commit = result_node_commit,
        promotion_skipped_reason = CASE WHEN policy_matches THEN NULL ELSE 'policy_changed' END
    WHERE run.poll_run_id = target_poll_run_id
      AND run.status = 'running'
      AND run.lease_fencing_token = expected_fencing_token
    RETURNING run.*;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public.control_finalize_account_inventory_poll_run_with_lifecycle_v2(
    target_poll_run_id uuid,
    expected_fencing_token uuid,
    result_transport_success boolean,
    result_response_shape_valid boolean,
    result_contract_valid boolean,
    result_inventory_mode text,
    result_node_identity_complete boolean,
    result_snapshot_complete boolean,
    result_degraded boolean,
    result_result text,
    result_reason text,
    result_source_record_count integer,
    result_identifiable_record_count integer,
    result_unidentified_record_count integer,
    result_unsupported_provider_count integer,
    result_out_of_scope_provider_count integer,
    result_node_version text,
    result_node_commit text,
    provider_results jsonb,
    snapshot_items jsonb,
    duplicate_evidence jsonb
) RETURNS SETOF public.account_inventory_poll_runs
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    database_now timestamptz := clock_timestamp();
    run_record public.account_inventory_poll_runs%ROWTYPE;
    finalized_record public.account_inventory_poll_runs%ROWTYPE;
    expected_providers text[];
    promoted_provider text;
BEGIN
    SELECT * INTO run_record
    FROM public.account_inventory_poll_runs AS run
    WHERE run.poll_run_id = target_poll_run_id
      AND run.status = 'running'
      AND run.lease_fencing_token = expected_fencing_token
      AND run.lease_expires_at > database_now
    FOR UPDATE;
    IF NOT FOUND THEN
        RETURN;
    END IF;
    SELECT policy.active_providers INTO expected_providers
    FROM public.provider_inventory_policy_versions AS policy
    WHERE policy.policy_version_id = run_record.provider_policy_version
      AND policy.node_type = run_record.node_type
      AND policy.driver_contract_version = run_record.driver_contract_version;
    PERFORM 1
    FROM public.provider_inventory_policy_bindings AS binding
    WHERE binding.node_type = run_record.node_type
      AND binding.driver_contract_version = run_record.driver_contract_version
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'account inventory policy binding is unavailable'
            USING ERRCODE = '23514';
    END IF;
    PERFORM 1
    FROM public.account_inventory_provider_states AS state
    WHERE state.instance_id = run_record.instance_id
      AND state.provider = ANY(expected_providers)
    ORDER BY state.provider
    FOR UPDATE;
    PERFORM 1
    FROM public.account_inventory AS account
    WHERE account.instance_id = run_record.instance_id
      AND account.provider = ANY(expected_providers)
    ORDER BY account.provider, account.account_key
    FOR UPDATE;

    PERFORM set_config('relay_control.lifecycle_write', 'finalize', true);
    SELECT * INTO finalized_record
    FROM public.control_finalize_account_inventory_poll_run_v2(
        target_poll_run_id, expected_fencing_token,
        result_transport_success, result_response_shape_valid, result_contract_valid,
        result_inventory_mode, result_node_identity_complete, result_snapshot_complete,
        result_degraded, result_result, result_reason, result_source_record_count,
        result_identifiable_record_count, result_unidentified_record_count,
        result_unsupported_provider_count, result_out_of_scope_provider_count,
        result_node_version, result_node_commit, provider_results, snapshot_items,
        duplicate_evidence
    );
    IF NOT FOUND THEN
        RETURN;
    END IF;

    FOR promoted_provider IN
        SELECT result.provider
        FROM public.account_inventory_poll_provider_results AS result
        WHERE result.poll_run_id = target_poll_run_id
          AND result.promotion_applied
        ORDER BY result.provider
    LOOP
        IF NOT EXISTS (
            SELECT 1 FROM public.account_inventory_provider_states AS state
            WHERE state.instance_id = finalized_record.instance_id
              AND state.provider = promoted_provider
              AND state.monitoring_status = 'active'
        ) THEN
            RAISE EXCEPTION 'promoted Provider is not actively monitored'
                USING ERRCODE = '23514';
        END IF;

        UPDATE public.account_inventory AS account
        SET lifecycle = CASE account.lifecycle
                WHEN 'present' THEN 'suspected_missing'
                WHEN 'suspected_missing' THEN 'missing'
                ELSE account.lifecycle
            END,
            consecutive_missing_count = CASE account.lifecycle
                WHEN 'present' THEN 1
                WHEN 'suspected_missing' THEN 2
                ELSE account.consecutive_missing_count
            END,
            missing_since = CASE account.lifecycle
                WHEN 'suspected_missing' THEN finalized_record.observed_at
                ELSE account.missing_since
            END,
            updated_at = CASE account.lifecycle
                WHEN 'present' THEN finalized_record.observed_at
                WHEN 'suspected_missing' THEN finalized_record.observed_at
                ELSE account.updated_at
            END
        WHERE account.instance_id = finalized_record.instance_id
          AND account.provider = promoted_provider
          AND account.lifecycle <> 'out_of_scope'
          AND NOT EXISTS (
              SELECT 1 FROM public.account_inventory_snapshot_items AS item
              WHERE item.poll_run_id = target_poll_run_id
                AND item.instance_id = account.instance_id
                AND item.provider = account.provider
                AND item.account_key = account.account_key
          );

        INSERT INTO public.account_inventory (
            instance_id, provider, account_key, normalized_email, basic_status,
            success_count, failed_count, recent_request_count, last_refresh_at,
            next_retry_at, source_updated_at, lifecycle,
            consecutive_missing_count, missing_since, out_of_scope_since,
            first_seen_at, last_seen_at, current_poll_run_id,
            current_scheduled_at, source_observed_at, source_node_version,
            source_node_commit, availability_runtime_evidence, auth_failure_reason, updated_at
        )
        SELECT item.instance_id, item.provider, item.account_key,
               item.normalized_email, item.basic_status, item.success_count,
               item.failed_count, item.recent_request_count,
               item.last_refresh_at, item.next_retry_at, item.source_updated_at,
               'present', 0, NULL, NULL, finalized_record.observed_at,
               finalized_record.observed_at, target_poll_run_id,
               finalized_record.scheduled_at, finalized_record.observed_at,
               finalized_record.node_version, finalized_record.node_commit,
               item.availability_runtime_evidence, item.auth_failure_reason,
               finalized_record.observed_at
        FROM public.account_inventory_snapshot_items AS item
        WHERE item.poll_run_id = target_poll_run_id
          AND item.instance_id = finalized_record.instance_id
          AND item.provider = promoted_provider
        ORDER BY item.account_key
        ON CONFLICT (instance_id, account_key) DO UPDATE
        SET basic_status = EXCLUDED.basic_status,
            success_count = EXCLUDED.success_count,
            failed_count = EXCLUDED.failed_count,
            recent_request_count = EXCLUDED.recent_request_count,
            last_refresh_at = EXCLUDED.last_refresh_at,
            next_retry_at = EXCLUDED.next_retry_at,
            source_updated_at = EXCLUDED.source_updated_at,
            availability_runtime_evidence = EXCLUDED.availability_runtime_evidence,
            auth_failure_reason = EXCLUDED.auth_failure_reason,
            lifecycle = 'present', consecutive_missing_count = 0,
            missing_since = NULL, out_of_scope_since = NULL,
            last_seen_at = EXCLUDED.last_seen_at,
            current_poll_run_id = EXCLUDED.current_poll_run_id,
            current_scheduled_at = EXCLUDED.current_scheduled_at,
            source_observed_at = EXCLUDED.source_observed_at,
            source_node_version = EXCLUDED.source_node_version,
            source_node_commit = EXCLUDED.source_node_commit,
            updated_at = EXCLUDED.updated_at;
    END LOOP;

    RETURN NEXT finalized_record;
END;
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_finalize_account_inventory_poll_run_v2(uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_finalize_account_inventory_poll_run_v2(uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_finalize_account_inventory_poll_run_v2(uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb) TO relay_control_runtime;

ALTER FUNCTION public.control_finalize_account_inventory_poll_run_with_lifecycle_v2(uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_finalize_account_inventory_poll_run_with_lifecycle_v2(uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_finalize_account_inventory_poll_run_with_lifecycle_v2(uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb) TO relay_control_runtime;

-- Preserve the v1 writer contract while ensuring a rollback to an older
-- Control cannot inherit v2-only availability metadata. The legacy bodies are
-- retained under private names and wrapped with the same public signatures.
DO $$ BEGIN EXECUTE 'ALTER FUNCTION public.control_finalize_account_inventory_poll_run(
    uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,
    text,text,integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb
) RENAME TO control_finalize_account_inventory_poll_run_v1_legacy'; END $$;
DO $$ BEGIN EXECUTE 'ALTER FUNCTION public.control_finalize_account_inventory_poll_run_with_lifecycle(
    uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,
    text,text,integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb
) RENAME TO control_finalize_account_inventory_poll_run_with_lifecycle_v1_legacy'; END $$;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_finalize_account_inventory_poll_run(
    target_poll_run_id uuid, expected_fencing_token uuid,
    result_transport_success boolean, result_response_shape_valid boolean,
    result_contract_valid boolean, result_inventory_mode text,
    result_node_identity_complete boolean, result_snapshot_complete boolean,
    result_degraded boolean, result_result text, result_reason text,
    result_source_record_count integer, result_identifiable_record_count integer,
    result_unidentified_record_count integer, result_unsupported_provider_count integer,
    result_out_of_scope_provider_count integer, result_node_version text,
    result_node_commit text, provider_results jsonb, snapshot_items jsonb,
    duplicate_evidence jsonb
) RETURNS SETOF public.account_inventory_poll_runs
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE
    finalized_record public.account_inventory_poll_runs%ROWTYPE;
BEGIN
    SELECT * INTO finalized_record FROM public.control_finalize_account_inventory_poll_run_v1_legacy(
        target_poll_run_id, expected_fencing_token, result_transport_success,
        result_response_shape_valid, result_contract_valid, result_inventory_mode,
        result_node_identity_complete, result_snapshot_complete, result_degraded,
        result_result, result_reason, result_source_record_count,
        result_identifiable_record_count, result_unidentified_record_count,
        result_unsupported_provider_count, result_out_of_scope_provider_count,
        result_node_version, result_node_commit, provider_results, snapshot_items,
        duplicate_evidence);
    IF NOT FOUND THEN
        RETURN;
    END IF;
    RETURN NEXT finalized_record;
    UPDATE public.account_inventory AS account
    SET availability_runtime_evidence = NULL, auth_failure_reason = NULL
    WHERE account.instance_id = finalized_record.instance_id
      AND account.current_poll_run_id = target_poll_run_id
      AND account.account_key IN (SELECT value->>'account_key' FROM jsonb_array_elements(snapshot_items) AS item(value));
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_finalize_account_inventory_poll_run_with_lifecycle(
    target_poll_run_id uuid, expected_fencing_token uuid,
    result_transport_success boolean, result_response_shape_valid boolean,
    result_contract_valid boolean, result_inventory_mode text,
    result_node_identity_complete boolean, result_snapshot_complete boolean,
    result_degraded boolean, result_result text, result_reason text,
    result_source_record_count integer, result_identifiable_record_count integer,
    result_unidentified_record_count integer, result_unsupported_provider_count integer,
    result_out_of_scope_provider_count integer, result_node_version text,
    result_node_commit text, provider_results jsonb, snapshot_items jsonb,
    duplicate_evidence jsonb
) RETURNS SETOF public.account_inventory_poll_runs
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE
    finalized_record public.account_inventory_poll_runs%ROWTYPE;
BEGIN
    SELECT * INTO finalized_record FROM public.control_finalize_account_inventory_poll_run_with_lifecycle_v1_legacy(
        target_poll_run_id, expected_fencing_token, result_transport_success,
        result_response_shape_valid, result_contract_valid, result_inventory_mode,
        result_node_identity_complete, result_snapshot_complete, result_degraded,
        result_result, result_reason, result_source_record_count,
        result_identifiable_record_count, result_unidentified_record_count,
        result_unsupported_provider_count, result_out_of_scope_provider_count,
        result_node_version, result_node_commit, provider_results, snapshot_items,
        duplicate_evidence);
    IF NOT FOUND THEN
        RETURN;
    END IF;
    RETURN NEXT finalized_record;
    UPDATE public.account_inventory AS account
    SET availability_runtime_evidence = NULL, auth_failure_reason = NULL
    WHERE account.instance_id = finalized_record.instance_id
      AND account.current_poll_run_id = target_poll_run_id
      AND account.account_key IN (SELECT value->>'account_key' FROM jsonb_array_elements(snapshot_items) AS item(value));
END;
$$;
-- +goose StatementEnd

ALTER FUNCTION public.control_finalize_account_inventory_poll_run(uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb) OWNER TO relay_control_migrator;
ALTER FUNCTION public.control_finalize_account_inventory_poll_run_with_lifecycle(uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_finalize_account_inventory_poll_run(uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb) FROM PUBLIC;
REVOKE ALL ON FUNCTION public.control_finalize_account_inventory_poll_run_with_lifecycle(uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_finalize_account_inventory_poll_run(uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb) TO relay_control_runtime;
GRANT EXECUTE ON FUNCTION public.control_finalize_account_inventory_poll_run_with_lifecycle(uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb) TO relay_control_runtime;
REVOKE ALL ON FUNCTION public.control_finalize_account_inventory_poll_run(uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb) FROM relay_control_runtime;
REVOKE ALL ON FUNCTION public.control_finalize_account_inventory_poll_run_v1_legacy(uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb) FROM PUBLIC, relay_control_runtime;
REVOKE ALL ON FUNCTION public.control_finalize_account_inventory_poll_run_with_lifecycle_v1_legacy(uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb) FROM PUBLIC, relay_control_runtime;
-- +goose Down
DROP FUNCTION public.control_query_account_availability_occurrences_v1(uuid,text,text,timestamptz,uuid,integer);
DROP FUNCTION public.control_query_account_availability_v1(uuid,text[]);
DROP FUNCTION public.control_finalize_account_inventory_poll_run_v2(uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb);
DROP FUNCTION public.control_finalize_account_inventory_poll_run_with_lifecycle_v2(uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb);
DROP FUNCTION public.control_finalize_account_inventory_poll_run(uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb);
DROP FUNCTION public.control_finalize_account_inventory_poll_run_with_lifecycle(uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb);
ALTER FUNCTION public.control_finalize_account_inventory_poll_run_v1_legacy(uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb) RENAME TO control_finalize_account_inventory_poll_run;
ALTER FUNCTION public.control_finalize_account_inventory_poll_run_with_lifecycle_v1_legacy(uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb) RENAME TO control_finalize_account_inventory_poll_run_with_lifecycle;
REVOKE ALL ON FUNCTION public.control_finalize_account_inventory_poll_run(uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb) FROM PUBLIC, relay_control_runtime;
REVOKE ALL ON FUNCTION public.control_finalize_account_inventory_poll_run_with_lifecycle(uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_finalize_account_inventory_poll_run_with_lifecycle(uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb) TO relay_control_runtime;
DROP FUNCTION public.control_insert_account_request_quality_events_v2(jsonb);
DROP FUNCTION public.control_reconcile_account_availability_v1(uuid,text);
DROP FUNCTION public.control_account_availability_source_v1(uuid,text[]);
DROP TABLE public.account_availability_occurrences;
DROP TABLE public.account_availability_checkpoints;
ALTER TABLE public.account_request_quality_events DROP CONSTRAINT account_request_quality_auth_success_null, DROP CONSTRAINT account_request_quality_auth_failure_reason_fixed, DROP COLUMN auth_failure_reason;
ALTER TABLE public.account_inventory DROP CONSTRAINT account_inventory_auth_failure_reason_fixed, DROP CONSTRAINT account_inventory_availability_evidence_fixed, DROP COLUMN auth_failure_reason, DROP COLUMN availability_runtime_evidence;
ALTER TABLE public.account_inventory_snapshot_items DROP CONSTRAINT account_inventory_snapshot_auth_failure_reason_fixed, DROP CONSTRAINT account_inventory_snapshot_availability_evidence_fixed, DROP COLUMN auth_failure_reason, DROP COLUMN availability_runtime_evidence;
