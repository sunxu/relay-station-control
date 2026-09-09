-- +goose Up
-- Tighten Antigravity availability recovery evidence without modifying 00026.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_reconcile_account_availability_v1(after_node uuid, after_account text)
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
    IF failed_at>occ.last_failure_at THEN UPDATE public.account_availability_occurrences SET last_failure_at=failed_at WHERE occurrence_id=occ.occurrence_id; END IF;
    IF NOT conflict AND success_at>failed_at THEN
     UPDATE public.account_availability_occurrences SET status='RESOLVED',resolved_at=moment,recovery_source_id=NULL,recovery_source_at=success_at WHERE occurrence_id=occ.occurrence_id;
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
ALTER FUNCTION public.control_reconcile_account_availability_v1(uuid,text) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_reconcile_account_availability_v1(uuid,text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_reconcile_account_availability_v1(uuid,text) TO relay_control_runtime;

-- +goose Down
-- Restore the 00026 implementation for isolated migration rollback tests only.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_reconcile_account_availability_v1(after_node uuid, after_account text)
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
ALTER FUNCTION public.control_reconcile_account_availability_v1(uuid,text) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_reconcile_account_availability_v1(uuid,text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_reconcile_account_availability_v1(uuid,text) TO relay_control_runtime;
