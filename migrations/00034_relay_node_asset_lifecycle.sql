-- +goose Up

-- A class-1 runtime cannot interpret retired Node rows or cancellation and
-- dispatch-authorization evidence.  Raise the external startup floor before
-- exposing any class-2 schema in this transaction.
ALTER TABLE public.control_runtime_compatibility
    DROP CONSTRAINT control_runtime_compatibility_floor_check,
    ADD CONSTRAINT control_runtime_compatibility_floor_check
        CHECK (phase6_evidence_floor IN (0, 1, 2));
UPDATE public.control_runtime_compatibility
SET phase6_evidence_floor = 2, updated_at = clock_timestamp()
WHERE singleton_id = 1 AND phase6_evidence_floor < 2;

LOCK TABLE public.relay_node_assets,
    public.relay_node_inventory_monitoring_activations,
    public.relay_node_gateway_account_bindings,
    public.account_inventory_poll_runs
IN ACCESS EXCLUSIVE MODE;

ALTER TABLE public.relay_node_assets
    ADD COLUMN lifecycle_status text,
    ADD COLUMN revision bigint,
    ADD COLUMN retired_at timestamptz,
    ADD COLUMN retired_by uuid,
    ADD COLUMN retire_reason text;

UPDATE public.relay_node_assets
SET lifecycle_status = 'active', revision = 1
WHERE lifecycle_status IS NULL OR revision IS NULL;

ALTER TABLE public.relay_node_assets
    ALTER COLUMN lifecycle_status SET DEFAULT 'active',
    ALTER COLUMN lifecycle_status SET NOT NULL,
    ALTER COLUMN revision SET DEFAULT 1,
    ALTER COLUMN revision SET NOT NULL,
    ADD CONSTRAINT relay_node_assets_retired_by_fkey
        FOREIGN KEY (retired_by) REFERENCES public.control_admin_users(admin_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    ADD CONSTRAINT relay_node_assets_lifecycle_fixed
        CHECK (lifecycle_status IN ('active', 'retired')) NOT VALID,
    ADD CONSTRAINT relay_node_assets_revision_positive CHECK (revision >= 1) NOT VALID,
    ADD CONSTRAINT relay_node_assets_lifecycle_shape CHECK (
        (lifecycle_status = 'active' AND retired_at IS NULL
         AND retired_by IS NULL AND retire_reason IS NULL)
        OR
        (lifecycle_status = 'retired' AND retired_at IS NOT NULL
         AND retired_by IS NOT NULL
         AND retire_reason IN ('administrator_retire', 'replacement')
         AND retired_at >= created_at)
    ) NOT VALID;

ALTER TABLE public.relay_node_assets
    VALIDATE CONSTRAINT relay_node_assets_lifecycle_fixed,
    VALIDATE CONSTRAINT relay_node_assets_revision_positive,
    VALIDATE CONSTRAINT relay_node_assets_lifecycle_shape;

-- +goose StatementBegin
CREATE FUNCTION public.control_protect_relay_node_asset_lifecycle()
RETURNS trigger LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    IF TG_OP IN ('DELETE', 'TRUNCATE') THEN
        RAISE EXCEPTION 'relay node asset history is immutable' USING ERRCODE = '42501';
    END IF;
    IF NEW.instance_id IS DISTINCT FROM OLD.instance_id
       OR NEW.node_type IS DISTINCT FROM OLD.node_type
       OR NEW.driver_contract_version IS DISTINCT FROM OLD.driver_contract_version
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'relay node asset identity is immutable' USING ERRCODE = '23514';
    END IF;
    IF OLD.lifecycle_status = 'retired' THEN
        RAISE EXCEPTION 'retired relay node asset is terminal' USING ERRCODE = '23514';
    END IF;
    IF NEW.revision <> OLD.revision + 1 THEN
        RAISE EXCEPTION 'relay node asset revision must advance exactly once' USING ERRCODE = '23514';
    END IF;
    IF NEW.lifecycle_status NOT IN ('active', 'retired')
       OR (NEW.lifecycle_status = 'active' AND (
           NEW.retired_at IS NOT NULL OR NEW.retired_by IS NOT NULL OR NEW.retire_reason IS NOT NULL
       )) THEN
        RAISE EXCEPTION 'relay node lifecycle transition is invalid' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER relay_node_assets_lifecycle_guard
BEFORE UPDATE OR DELETE ON public.relay_node_assets
FOR EACH ROW EXECUTE FUNCTION public.control_protect_relay_node_asset_lifecycle();
CREATE TRIGGER relay_node_assets_truncate_guard
BEFORE TRUNCATE ON public.relay_node_assets
FOR EACH STATEMENT EXECUTE FUNCTION public.control_protect_relay_node_asset_lifecycle();

CREATE TABLE public.relay_node_asset_replacements (
    old_instance_id uuid PRIMARY KEY REFERENCES public.relay_node_assets(instance_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    new_instance_id uuid NOT NULL UNIQUE REFERENCES public.relay_node_assets(instance_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    replaced_at timestamptz NOT NULL,
    replaced_by uuid NOT NULL REFERENCES public.control_admin_users(admin_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    command_id uuid NOT NULL UNIQUE,
    CONSTRAINT relay_node_asset_replacements_distinct CHECK (old_instance_id <> new_instance_id)
);

-- +goose StatementBegin
CREATE FUNCTION public.control_protect_relay_node_asset_replacement()
RETURNS trigger LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    IF TG_OP IN ('UPDATE', 'DELETE', 'TRUNCATE') THEN
        RAISE EXCEPTION 'relay node replacement lineage is immutable' USING ERRCODE='42501';
    END IF;
    IF EXISTS (
        WITH RECURSIVE reachable(id, path) AS (
            SELECT NEW.new_instance_id, ARRAY[NEW.new_instance_id]::uuid[]
            UNION ALL
            SELECT r.new_instance_id, reachable.path || r.new_instance_id
            FROM public.relay_node_asset_replacements r JOIN reachable ON r.old_instance_id=reachable.id
            WHERE NOT r.new_instance_id = ANY(reachable.path)
        ) SELECT 1 FROM reachable WHERE id=NEW.old_instance_id
    ) THEN
        RAISE EXCEPTION 'relay node replacement lineage cycle is forbidden' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION public.control_serialize_relay_node_asset_replacement_insert()
RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
BEGIN
    LOCK TABLE public.relay_node_asset_replacements IN SHARE ROW EXCLUSIVE MODE;
    RETURN NEW;
END $$;
-- +goose StatementEnd
ALTER FUNCTION public.control_serialize_relay_node_asset_replacement_insert()
    OWNER TO relay_control_migrator;
REVOKE EXECUTE ON FUNCTION public.control_serialize_relay_node_asset_replacement_insert()
    FROM PUBLIC, relay_control_runtime, relay_control_asset_registrar;
CREATE TRIGGER relay_node_asset_replacements_00_insert_lock
BEFORE INSERT ON public.relay_node_asset_replacements
FOR EACH ROW EXECUTE FUNCTION public.control_serialize_relay_node_asset_replacement_insert();
CREATE TRIGGER relay_node_asset_replacements_guard
BEFORE INSERT OR UPDATE OR DELETE ON public.relay_node_asset_replacements
FOR EACH ROW EXECUTE FUNCTION public.control_protect_relay_node_asset_replacement();
CREATE TRIGGER relay_node_asset_replacements_truncate_guard
BEFORE TRUNCATE ON public.relay_node_asset_replacements
FOR EACH STATEMENT EXECUTE FUNCTION public.control_protect_relay_node_asset_replacement();

ALTER TABLE public.relay_node_inventory_monitoring_activations
    ADD COLUMN cancelled_at timestamptz,
    ADD COLUMN cancelled_by uuid,
    ADD COLUMN cancel_reason text,
    ADD CONSTRAINT relay_node_monitoring_cancelled_by_fkey
        FOREIGN KEY (cancelled_by) REFERENCES public.control_admin_users(admin_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    ADD CONSTRAINT relay_node_monitoring_cancellation_shape CHECK (
        (cancelled_at IS NULL AND cancelled_by IS NULL AND cancel_reason IS NULL)
        OR
        (cancelled_at IS NOT NULL AND cancelled_by IS NOT NULL
         AND cancel_reason IN ('node_retired','node_replaced')
         AND cancelled_at <= effective_from)
    );
ALTER TABLE public.relay_node_inventory_monitoring_activations
    DROP CONSTRAINT node_monitoring_activation_end_metadata_valid,
    ADD CONSTRAINT node_monitoring_activation_end_metadata_valid CHECK (
        (effective_to IS NULL AND end_reason IS NULL AND end_actor IS NULL AND end_recorded_at IS NULL)
        OR
        (effective_to IS NOT NULL AND end_reason IN (
            'deployment_disable','scheduled_disable','reconciliation','node_retired','node_replaced')
         AND end_actor IS NOT NULL AND octet_length(end_actor) BETWEEN 1 AND 128
         AND end_actor ~ '^[A-Za-z0-9][A-Za-z0-9._@:-]*$'
         AND end_recorded_at IS NOT NULL AND end_recorded_at >= created_at)
    ),
    DROP CONSTRAINT relay_node_inventory_monitoring_a_instance_id_active_range_excl,
    DROP COLUMN active_range,
    ADD COLUMN active_range tstzrange GENERATED ALWAYS AS (
        CASE WHEN cancelled_at IS NULL
             THEN tstzrange(effective_from,effective_to,'[)')
             ELSE 'empty'::tstzrange END
    ) STORED,
    ADD CONSTRAINT relay_node_inventory_monitoring_activations_instance_id_active_range_excl
        EXCLUDE USING gist (instance_id WITH =, active_range WITH &&);

-- +goose StatementBegin
CREATE FUNCTION public.control_protect_node_monitoring_cancellation()
RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    IF TG_OP IN ('DELETE','TRUNCATE') THEN
        RAISE EXCEPTION 'node monitoring history is immutable' USING ERRCODE='42501';
    END IF;
    IF NEW.monitoring_activation_id IS DISTINCT FROM OLD.monitoring_activation_id
       OR NEW.instance_id IS DISTINCT FROM OLD.instance_id
       OR NEW.effective_from IS DISTINCT FROM OLD.effective_from
       OR NEW.reason IS DISTINCT FROM OLD.reason
       OR NEW.actor IS DISTINCT FROM OLD.actor
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'node monitoring identity is immutable' USING ERRCODE='23514';
    END IF;
    IF OLD.cancelled_at IS NOT NULL THEN
        RAISE EXCEPTION 'cancelled node monitoring history is immutable' USING ERRCODE='23514';
    END IF;
    IF OLD.effective_to IS NOT NULL AND ROW(
        NEW.effective_to, NEW.end_reason, NEW.end_actor, NEW.end_recorded_at
    ) IS DISTINCT FROM ROW(
        OLD.effective_to, OLD.end_reason, OLD.end_actor, OLD.end_recorded_at
    ) AND NOT (
        NEW.effective_to < OLD.effective_to
        AND NEW.end_reason IN ('node_retired', 'node_replaced')
        AND NEW.end_actor IS NOT NULL
        AND NEW.end_recorded_at = NEW.effective_to
    ) THEN
        RAISE EXCEPTION 'closed node monitoring history is immutable' USING ERRCODE='23514';
    END IF;
    IF NEW.cancelled_at IS NOT NULL AND (
        NEW.effective_to IS DISTINCT FROM OLD.effective_to
        OR NEW.end_reason IS DISTINCT FROM OLD.end_reason
        OR NEW.end_actor IS DISTINCT FROM OLD.end_actor
        OR NEW.end_recorded_at IS DISTINCT FROM OLD.end_recorded_at
    ) THEN
        RAISE EXCEPTION 'node monitoring cancellation cannot rewrite schedule history' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER relay_node_monitoring_cancellation_guard
BEFORE UPDATE OR DELETE ON public.relay_node_inventory_monitoring_activations
FOR EACH ROW EXECUTE FUNCTION public.control_protect_node_monitoring_cancellation();
CREATE TRIGGER relay_node_monitoring_cancellation_truncate_guard
BEFORE TRUNCATE ON public.relay_node_inventory_monitoring_activations
FOR EACH STATEMENT EXECUTE FUNCTION public.control_protect_node_monitoring_cancellation();

ALTER TABLE public.relay_node_gateway_account_bindings
    DROP CONSTRAINT relay_node_gateway_account_bindings_end_reason_fixed,
    ADD CONSTRAINT relay_node_gateway_account_bindings_end_reason_fixed CHECK (
        end_reason IS NULL OR end_reason IN (
            'administrator_unbind','administrator_rebind','gateway_retired','gateway_replaced',
            'node_retired','node_replaced'));

ALTER TABLE public.account_inventory_poll_runs
    ADD COLUMN dispatch_authorized_attempt integer,
    ADD COLUMN dispatch_authorized_at timestamptz,
    ADD COLUMN dispatch_authorized_fencing_token uuid,
    ADD CONSTRAINT account_inventory_poll_runs_dispatch_authorization_shape CHECK (
        (dispatch_authorized_attempt IS NULL AND dispatch_authorized_at IS NULL
         AND dispatch_authorized_fencing_token IS NULL)
        OR
        (status='running' AND dispatch_authorized_attempt=attempt_count
         AND dispatch_authorized_at IS NOT NULL
         AND dispatch_authorized_fencing_token=lease_fencing_token)
    ),
    DROP CONSTRAINT account_inventory_poll_runs_execution_reason_fixed,
    ADD CONSTRAINT account_inventory_poll_runs_execution_reason_fixed CHECK (
        execution_reason IS NULL OR execution_reason IN (
            'lease_expired','poll_start_grace_expired','max_attempts_exhausted',
            'node_retired','node_replaced')),
    DROP CONSTRAINT account_inventory_poll_runs_promotion_reason_fixed,
    ADD CONSTRAINT account_inventory_poll_runs_promotion_reason_fixed CHECK (
        promotion_skipped_reason IS NULL OR promotion_skipped_reason IN (
            'policy_changed','monitoring_ineligible','node_retired','node_replaced'));
ALTER TABLE public.account_inventory_poll_provider_results
    DROP CONSTRAINT account_inventory_poll_provider_promotion_reason_fixed,
    ADD CONSTRAINT account_inventory_poll_provider_promotion_reason_fixed CHECK (
        promotion_skipped_reason IS NULL OR promotion_skipped_reason IN (
            'policy_changed','transport_failed','contract_invalid','disk_fallback',
            'provider_identity_incomplete','provider_duplicate','stale_poll',
            'monitoring_ineligible','node_retired','node_replaced'));

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_refresh_account_inventory_provider_health_v1()
RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog SET TimeZone='UTC' AS $$
DECLARE prior_gate text := coalesce(current_setting('relay_control.lifecycle_write',true),'');
BEGIN
  IF OLD.status NOT IN ('finalized','abandoned') AND NEW.status='finalized'
     AND NEW.promotion_skipped_reason IS DISTINCT FROM 'policy_changed'
     AND NEW.promotion_skipped_reason IS DISTINCT FROM 'monitoring_ineligible'
     AND NEW.promotion_skipped_reason IS DISTINCT FROM 'node_retired'
     AND NEW.promotion_skipped_reason IS DISTINCT FROM 'node_replaced' THEN
    PERFORM set_config('relay_control.lifecycle_write','health',true);
    UPDATE public.account_inventory_provider_states state
    SET health_scheduled_at=NEW.scheduled_at,health_degraded=result.degraded,
        health_reason=CASE WHEN result.degraded THEN result.reason ELSE 'none' END
    FROM public.account_inventory_poll_provider_results result
    WHERE result.poll_run_id=NEW.poll_run_id AND result.provider=state.provider
      AND state.instance_id=NEW.instance_id AND state.monitoring_status='active'
      AND state.health_scheduled_at<NEW.scheduled_at
      AND result.promotion_skipped_reason IS DISTINCT FROM 'policy_changed'
      AND result.promotion_skipped_reason IS DISTINCT FROM 'stale_poll'
      AND result.promotion_skipped_reason IS DISTINCT FROM 'monitoring_ineligible'
      AND result.promotion_skipped_reason IS DISTINCT FROM 'node_retired'
      AND result.promotion_skipped_reason IS DISTINCT FROM 'node_replaced'
      AND EXISTS (SELECT 1 FROM public.provider_inventory_policy_activations a
        WHERE a.node_type=NEW.node_type AND a.driver_contract_version=NEW.driver_contract_version
          AND a.policy_version_id=NEW.provider_policy_version AND a.active_range @> clock_timestamp());
    PERFORM set_config('relay_control.lifecycle_write',prior_gate,true);
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
ALTER FUNCTION public.control_refresh_account_inventory_provider_health_v1() OWNER TO relay_control_migrator;

-- Compose the Stage-1 promotion consistency guard with the Stage-2 run-wide
-- lifecycle and monitoring fences.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_validate_account_inventory_promotion()
RETURNS trigger LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE inconsistent_count bigint;
BEGIN
    IF NEW.status <> 'finalized' THEN
        RETURN NEW;
    END IF;
    SELECT count(*) INTO inconsistent_count
    FROM public.account_inventory_poll_provider_results AS result
    WHERE result.poll_run_id = NEW.poll_run_id
      AND (
        (NOT result.promotion_applied AND result.promotion_skipped_reason IS NULL)
        OR
        (result.promotion_applied AND (
            NOT NEW.contract_valid OR NEW.inventory_mode <> 'runtime'
            OR NOT NEW.node_identity_complete
            OR NOT result.snapshot_complete
            OR NOT EXISTS (
                SELECT 1 FROM public.account_inventory_provider_states AS state
                WHERE state.instance_id = NEW.instance_id
                  AND state.provider = result.provider
                  AND state.current_poll_run_id = NEW.poll_run_id
            )
        ))
        OR (NEW.promotion_skipped_reason IN (
                'policy_changed','monitoring_ineligible','node_retired','node_replaced'
            ) AND (
                result.promotion_applied
                OR result.promotion_skipped_reason IS DISTINCT FROM NEW.promotion_skipped_reason
            ))
        OR (result.promotion_skipped_reason IN (
                'policy_changed','monitoring_ineligible','node_retired','node_replaced'
            ) AND NEW.promotion_skipped_reason IS DISTINCT FROM result.promotion_skipped_reason)
        OR (NOT result.promotion_applied AND EXISTS (
            SELECT 1 FROM public.account_inventory_snapshot_items AS item
            WHERE item.poll_run_id = NEW.poll_run_id
              AND item.provider = result.provider
        ))
      );
    IF inconsistent_count <> 0 THEN
        RAISE EXCEPTION 'account inventory promotion evidence is inconsistent'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_validate_account_inventory_promotion()
    OWNER TO relay_control_migrator;

ALTER TABLE public.account_inventory_poll_runs
    DROP CONSTRAINT account_inventory_poll_runs_state_shape,
    ADD CONSTRAINT account_inventory_poll_runs_state_shape CHECK (
      (status='pending' AND attempt_count=0 AND first_started_at IS NULL AND last_started_at IS NULL
       AND lease_expires_at IS NULL AND lease_fencing_token IS NULL AND finalized_at IS NULL
       AND abandoned_at IS NULL AND execution_reason IS NULL AND observed_at IS NULL
       AND transport_success IS NULL AND response_shape_valid IS NULL AND contract_valid IS NULL
       AND inventory_mode IS NULL AND node_identity_complete IS NULL AND snapshot_complete IS NULL
       AND degraded IS NULL AND result IS NULL AND reason IS NULL AND source_record_count IS NULL
       AND identifiable_record_count IS NULL AND unidentified_record_count IS NULL
       AND unsupported_provider_count IS NULL AND out_of_scope_provider_count IS NULL
       AND node_version IS NULL AND node_commit IS NULL)
      OR
      (status='running' AND attempt_count>0 AND first_started_at IS NOT NULL AND last_started_at IS NOT NULL
       AND lease_expires_at>last_started_at AND lease_fencing_token IS NOT NULL AND finalized_at IS NULL
       AND abandoned_at IS NULL AND execution_reason IS NULL AND observed_at IS NULL
       AND transport_success IS NULL AND response_shape_valid IS NULL AND contract_valid IS NULL
       AND inventory_mode IS NULL AND node_identity_complete IS NULL AND snapshot_complete IS NULL
       AND degraded IS NULL AND result IS NULL AND reason IS NULL AND source_record_count IS NULL
       AND identifiable_record_count IS NULL AND unidentified_record_count IS NULL
       AND unsupported_provider_count IS NULL AND out_of_scope_provider_count IS NULL
       AND node_version IS NULL AND node_commit IS NULL)
      OR
      (status='retry_wait' AND attempt_count>0 AND first_started_at IS NOT NULL AND last_started_at IS NOT NULL
       AND lease_expires_at IS NULL AND lease_fencing_token IS NULL AND finalized_at IS NULL
       AND abandoned_at IS NULL AND execution_reason='lease_expired' AND observed_at IS NULL
       AND transport_success IS NULL AND response_shape_valid IS NULL AND contract_valid IS NULL
       AND inventory_mode IS NULL AND node_identity_complete IS NULL AND snapshot_complete IS NULL
       AND degraded IS NULL AND result IS NULL AND reason IS NULL AND source_record_count IS NULL
       AND identifiable_record_count IS NULL AND unidentified_record_count IS NULL
       AND unsupported_provider_count IS NULL AND out_of_scope_provider_count IS NULL
       AND node_version IS NULL AND node_commit IS NULL)
      OR
      (status='finalized' AND attempt_count>0 AND first_started_at IS NOT NULL AND last_started_at IS NOT NULL
       AND lease_expires_at IS NULL AND lease_fencing_token IS NULL AND finalized_at IS NOT NULL
       AND abandoned_at IS NULL AND execution_reason IS NULL AND observed_at IS NOT NULL
       AND transport_success IS NOT NULL AND response_shape_valid IS NOT NULL AND contract_valid IS NOT NULL
       AND node_identity_complete IS NOT NULL AND snapshot_complete IS NOT NULL AND degraded IS NOT NULL
       AND result IS NOT NULL AND reason IS NOT NULL AND source_record_count IS NOT NULL
       AND identifiable_record_count IS NOT NULL AND unidentified_record_count IS NOT NULL
       AND unsupported_provider_count IS NOT NULL AND out_of_scope_provider_count IS NOT NULL
       AND node_version IS NOT NULL AND node_commit IS NOT NULL
       AND (contract_valid OR inventory_mode IS NULL) AND (NOT contract_valid OR inventory_mode IS NOT NULL)
       AND (NOT response_shape_valid OR transport_success) AND (NOT contract_valid OR response_shape_valid)
       AND (NOT snapshot_complete OR contract_valid) AND ((reason='none')=contract_valid)
       AND ((result IN ('success','degraded'))=contract_valid)
       AND (result<>'success' OR (snapshot_complete AND NOT degraded))
       AND (result<>'degraded' OR degraded) AND (contract_valid OR degraded))
      OR
      (status='abandoned' AND finalized_at IS NULL AND abandoned_at IS NOT NULL
       AND lease_expires_at IS NULL AND lease_fencing_token IS NULL
       AND execution_reason IN ('poll_start_grace_expired','max_attempts_exhausted','node_retired','node_replaced')
       AND observed_at IS NULL AND transport_success IS NULL AND response_shape_valid IS NULL
       AND contract_valid IS NULL AND inventory_mode IS NULL AND node_identity_complete IS NULL
       AND snapshot_complete IS NULL AND degraded IS NULL AND result IS NULL AND reason IS NULL
       AND source_record_count IS NULL AND identifiable_record_count IS NULL
       AND unidentified_record_count IS NULL AND unsupported_provider_count IS NULL
       AND out_of_scope_provider_count IS NULL AND node_version IS NULL AND node_commit IS NULL)
    );

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_protect_account_inventory_poll_run() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE
    expected_providers text[];
    stored_providers text[];
    lifecycle_reason text := nullif(
        current_setting('relay_control.node_promotion_fence', true), '');
    retention_gate text := coalesce(
        current_setting('relay_control.history_poll_retention_delete', true), '');
BEGIN
    IF TG_OP='TRUNCATE' THEN
        RAISE EXCEPTION 'terminal account inventory poll evidence is immutable' USING ERRCODE='23514';
    END IF;
    IF TG_OP='DELETE' THEN
        IF current_user='relay_control_migrator'
           AND session_user<>current_user
           AND pg_has_role(session_user,'relay_control_runtime','member')
           AND NOT pg_has_role(session_user,'relay_control_migrator','member')
           AND OLD.status IN ('finalized','abandoned')
           AND retention_gate=OLD.poll_run_id::text THEN
            RETURN OLD;
        END IF;
        RAISE EXCEPTION 'terminal account inventory poll evidence is immutable' USING ERRCODE='23514';
    END IF;
    IF OLD.status IN ('finalized','abandoned') THEN
        RAISE EXCEPTION 'terminal account inventory poll evidence is immutable' USING ERRCODE='23514';
    END IF;
    IF NEW.poll_run_id<>OLD.poll_run_id OR NEW.instance_id<>OLD.instance_id
       OR NEW.node_type<>OLD.node_type OR NEW.driver_contract_version<>OLD.driver_contract_version
       OR NEW.scheduled_at<>OLD.scheduled_at OR NEW.provider_policy_version<>OLD.provider_policy_version
       OR NEW.max_attempts<>OLD.max_attempts OR NEW.poll_start_grace_seconds<>OLD.poll_start_grace_seconds
       OR NEW.created_at<>OLD.created_at THEN
        RAISE EXCEPTION 'account inventory poll identity is immutable' USING ERRCODE='23514';
    END IF;
    IF OLD.status='running' AND NEW.status<>'running' THEN
        NEW.dispatch_authorized_attempt:=NULL; NEW.dispatch_authorized_at:=NULL;
        NEW.dispatch_authorized_fencing_token:=NULL;
    END IF;
    IF NEW.status='finalized' AND lifecycle_reason IS NULL THEN
        SELECT CASE a.retire_reason WHEN 'replacement' THEN 'node_replaced'
                   WHEN 'administrator_retire' THEN 'node_retired' END
        INTO lifecycle_reason
        FROM public.relay_node_assets a
        WHERE a.instance_id=NEW.instance_id AND a.lifecycle_status='retired';
    END IF;
    IF NEW.status='finalized' AND lifecycle_reason IS NOT NULL THEN
        NEW.promotion_skipped_reason:=lifecycle_reason;
    END IF;
    IF OLD.status=NEW.status THEN
        IF NOT (OLD.status='running' AND OLD.dispatch_authorized_attempt IS NULL
            AND NEW.dispatch_authorized_attempt=NEW.attempt_count
            AND NEW.dispatch_authorized_fencing_token=NEW.lease_fencing_token
            AND NEW.dispatch_authorized_at IS NOT NULL) THEN
            RAISE EXCEPTION 'invalid account inventory poll same-state mutation' USING ERRCODE='23514';
        END IF;
    ELSIF NOT ((OLD.status='pending' AND NEW.status IN ('running','abandoned'))
        OR (OLD.status='running' AND NEW.status IN ('retry_wait','finalized','abandoned'))
        OR (OLD.status='retry_wait' AND NEW.status IN ('running','abandoned'))) THEN
        RAISE EXCEPTION 'invalid account inventory poll transition' USING ERRCODE='23514';
    END IF;
    IF NEW.status='abandoned' AND EXISTS (SELECT 1 FROM public.account_inventory_poll_provider_results r WHERE r.poll_run_id=NEW.poll_run_id) THEN
        RAISE EXCEPTION 'abandoned account inventory poll cannot contain provider evidence' USING ERRCODE='23514';
    END IF;
    IF NEW.status='finalized' THEN
        SELECT p.active_providers INTO expected_providers FROM public.provider_inventory_policy_versions p
        WHERE p.policy_version_id=NEW.provider_policy_version AND p.node_type=NEW.node_type
          AND p.driver_contract_version=NEW.driver_contract_version;
        SELECT array_agg(r.provider ORDER BY r.provider) INTO stored_providers
        FROM public.account_inventory_poll_provider_results r WHERE r.poll_run_id=NEW.poll_run_id;
        IF stored_providers IS DISTINCT FROM expected_providers THEN
            RAISE EXCEPTION 'finalized account inventory poll provider set is incomplete' USING ERRCODE='23514';
        END IF;
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd

-- The runtime role cannot mutate poll-run state directly. This narrowly
-- scoped function establishes the one-shot outbound authorization while
-- preserving the Node-first lock order.
-- +goose StatementBegin
CREATE FUNCTION public.control_authorize_account_inventory_poll_dispatch(
    requested_poll_run_id uuid,
    requested_attempt integer,
    requested_fencing_token uuid
) RETURNS TABLE (lease_remaining_seconds double precision, grace_remaining_seconds double precision)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    database_now timestamptz;
    target_instance_id uuid;
BEGIN
    SELECT instance_id INTO target_instance_id
    FROM public.account_inventory_poll_runs
    WHERE poll_run_id = requested_poll_run_id;

    IF target_instance_id IS NULL THEN
        RETURN;
    END IF;

    PERFORM 1
    FROM public.relay_node_assets
    WHERE instance_id = target_instance_id
      AND lifecycle_status = 'active'
    FOR UPDATE;
    IF NOT FOUND THEN
        RETURN;
    END IF;

    database_now := clock_timestamp();

    RETURN QUERY
    WITH eligible AS (
        SELECT run.poll_run_id, run.attempt_count, run.lease_fencing_token,
               run.lease_expires_at, run.scheduled_at, run.poll_start_grace_seconds
        FROM public.account_inventory_poll_runs AS run
        WHERE run.poll_run_id = requested_poll_run_id
          AND run.status = 'running'
          AND run.attempt_count = requested_attempt
          AND run.lease_fencing_token = requested_fencing_token
          AND run.dispatch_authorized_attempt IS NULL
          AND run.lease_expires_at > database_now
          AND run.scheduled_at + make_interval(secs => run.poll_start_grace_seconds) > database_now
          AND EXISTS (
              SELECT 1
              FROM public.relay_node_inventory_monitoring_activations AS monitoring
              WHERE monitoring.instance_id = run.instance_id
                AND monitoring.cancelled_at IS NULL
                AND database_now <@ monitoring.active_range
          )
        FOR UPDATE
    ), changed AS (
        UPDATE public.account_inventory_poll_runs AS run
        SET dispatch_authorized_attempt = eligible.attempt_count,
            dispatch_authorized_at = database_now,
            dispatch_authorized_fencing_token = eligible.lease_fencing_token
        FROM eligible
        WHERE run.poll_run_id = eligible.poll_run_id
        RETURNING eligible.lease_expires_at, eligible.scheduled_at,
                  eligible.poll_start_grace_seconds
    )
    SELECT extract(epoch FROM (changed.lease_expires_at - database_now))::double precision,
           extract(epoch FROM (
               changed.scheduled_at
               + make_interval(secs => changed.poll_start_grace_seconds)
               - database_now
           ))::double precision
    FROM changed;
END;
$$;
-- +goose StatementEnd

-- Called only after the lifecycle command has locked the owning Node. The
-- function keeps poll-run DML behind the existing privileged database API.
-- +goose StatementBegin
CREATE FUNCTION public.control_abandon_node_inventory_poll_runs(
    requested_instance_id uuid,
    lifecycle_boundary timestamptz,
    lifecycle_reason text
) RETURNS bigint
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    changed_count bigint;
BEGIN
    IF lifecycle_reason NOT IN ('node_retired', 'node_replaced') THEN
        RAISE EXCEPTION 'invalid node lifecycle reason' USING ERRCODE = '22023';
    END IF;

    UPDATE public.account_inventory_poll_runs
    SET status = 'abandoned',
        abandoned_at = lifecycle_boundary,
        execution_reason = lifecycle_reason,
        lease_expires_at = NULL,
        lease_fencing_token = NULL,
        dispatch_authorized_attempt = NULL,
        dispatch_authorized_at = NULL,
        dispatch_authorized_fencing_token = NULL
    WHERE instance_id = requested_instance_id
      AND status IN ('pending', 'retry_wait', 'running')
      AND observed_at IS NULL
      AND NOT (
          status = 'running'
          AND dispatch_authorized_attempt = attempt_count
          AND dispatch_authorized_fencing_token = lease_fencing_token
      );
    GET DIAGNOSTICS changed_count = ROW_COUNT;
    RETURN changed_count;
END;
$$;
-- +goose StatementEnd

-- Provider evidence remains append-only, but a late authorized worker is
-- fenced from every current projection before the normal finalize path sees
-- its provider rows.
-- +goose StatementBegin
CREATE FUNCTION public.control_fence_retired_node_provider_promotion()
RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE lifecycle_reason text := nullif(
    current_setting('relay_control.node_promotion_fence', true), '');
BEGIN
  IF lifecycle_reason IS NULL THEN
    SELECT CASE a.retire_reason WHEN 'replacement' THEN 'node_replaced'
               WHEN 'administrator_retire' THEN 'node_retired' END
    INTO lifecycle_reason
    FROM public.account_inventory_poll_runs r
    JOIN public.relay_node_assets a ON a.instance_id=r.instance_id
    WHERE r.poll_run_id=NEW.poll_run_id AND a.lifecycle_status='retired';
  END IF;
  IF lifecycle_reason IS NOT NULL THEN
    NEW.promotion_applied:=false;
    NEW.promotion_skipped_reason:=lifecycle_reason;
  END IF;
  RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER account_inventory_poll_provider_results_00_node_lifecycle_fence
BEFORE INSERT ON public.account_inventory_poll_provider_results
FOR EACH ROW EXECUTE FUNCTION public.control_fence_retired_node_provider_promotion();

CREATE TABLE public.asset_registry_generations (
    singleton_id smallint PRIMARY KEY DEFAULT 1 CHECK (singleton_id=1),
    node_generation bigint NOT NULL DEFAULT 0 CHECK (node_generation >= 0)
);
INSERT INTO public.asset_registry_generations(singleton_id,node_generation) VALUES(1,0);

-- +goose StatementBegin
CREATE FUNCTION public.control_protect_asset_registry_generation()
RETURNS trigger LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    IF TG_OP IN ('DELETE','TRUNCATE') OR NEW.singleton_id IS DISTINCT FROM OLD.singleton_id
       OR NEW.node_generation < OLD.node_generation THEN
        RAISE EXCEPTION 'asset registry generation cannot move backwards' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER asset_registry_generations_guard BEFORE UPDATE OR DELETE
ON public.asset_registry_generations FOR EACH ROW
EXECUTE FUNCTION public.control_protect_asset_registry_generation();
CREATE TRIGGER asset_registry_generations_truncate_guard BEFORE TRUNCATE
ON public.asset_registry_generations FOR EACH STATEMENT
EXECUTE FUNCTION public.control_protect_asset_registry_generation();

-- +goose StatementBegin
CREATE FUNCTION public.control_advance_asset_registry_node_generation()
RETURNS bigint
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    next_generation bigint;
BEGIN
    UPDATE public.asset_registry_generations
    SET node_generation = node_generation + 1
    WHERE singleton_id = 1
      AND node_generation < 9223372036854775807
    RETURNING node_generation INTO next_generation;
    IF next_generation IS NULL THEN
        RAISE EXCEPTION 'asset registry node generation exhausted' USING ERRCODE = '22003';
    END IF;
    RETURN next_generation;
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_advance_asset_registry_node_generation()
    OWNER TO relay_control_migrator;

ALTER TABLE public.audit_logs DROP CONSTRAINT audit_logs_category_valid;
ALTER TABLE public.audit_logs ADD CONSTRAINT audit_logs_category_valid CHECK (
    category IN ('bootstrap','administrator','password','mfa','session','reauthentication',
      'authorization','rate_limit','account_inventory','account_inventory_history',
      'relay_binding','asset','asset_gateway','asset_node'));
ALTER TABLE public.audit_logs DROP CONSTRAINT audit_logs_action_valid;
ALTER TABLE public.audit_logs ADD CONSTRAINT audit_logs_action_valid CHECK (action IN (
    'bootstrap.start','bootstrap.complete','bootstrap.reset','auth.login_password',
    'auth.login_mfa','auth.logout','auth.password_change','auth.mfa_enroll','auth.mfa_reset',
    'auth.recovery_code_use','auth.recovery_codes_regenerate','session.create','session.revoke',
    'auth.reauthenticate','administrator.create','administrator.activate','administrator.disable',
    'administrator.activation_token_generate','authorization.check','auth.rate_limit','auth.csrf',
    'account_inventory.view','account_inventory_history.summarized',
    'account_inventory_history.snapshot_delete_batch','account_inventory_history.retention_delete_batch',
    'account_inventory_history.completed','account_inventory_history.failed',
    'relay_binding.bind','relay_binding.unbind','relay_binding.rebind',
    'asset.gateway_directory_reader_configured','gateway.register','gateway.edit','gateway.retire',
    'gateway.replace','gateway.health','gateway.connection_test',
    'node.register','node.edit','node.retire','node.replace'));

-- New Node identity and its initial capability declaration are one atomic,
-- narrowly scoped write. The runtime role cannot append capabilities to an
-- already committed identity.
-- +goose StatementBegin
CREATE FUNCTION public.control_create_relay_node_asset(
    requested_instance_id uuid,
    requested_display_name text,
    requested_node_type text,
    requested_driver_contract_version text,
    requested_management_endpoint text,
    requested_reader_secret_ref text,
    requested_capabilities text[]
) RETURNS uuid
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    created_instance_id uuid;
BEGIN
    IF requested_instance_id IS NULL
       OR requested_capabilities IS NULL
       OR cardinality(requested_capabilities) < 1
       OR cardinality(requested_capabilities) <> (
           SELECT count(DISTINCT capability) FROM unnest(requested_capabilities) capability
       )
       OR EXISTS (
           SELECT 1
           FROM unnest(requested_capabilities) requested(capability)
           LEFT JOIN public.driver_capabilities supported
             ON supported.node_type = requested_node_type
            AND supported.driver_contract_version = requested_driver_contract_version
            AND supported.capability = requested.capability
           WHERE supported.capability IS NULL
       ) THEN
        RAISE EXCEPTION 'invalid initial relay node capability declaration'
            USING ERRCODE = '23514';
    END IF;

    INSERT INTO public.relay_node_assets(
        instance_id, display_name, node_type, driver_contract_version,
        management_endpoint, reader_secret_ref, lifecycle_status, revision
    ) VALUES (
        requested_instance_id, requested_display_name, requested_node_type,
        requested_driver_contract_version, requested_management_endpoint,
        requested_reader_secret_ref, 'active', 1
    ) RETURNING instance_id INTO created_instance_id;

    INSERT INTO public.node_capabilities(
        instance_id, node_type, driver_contract_version, capability
    )
    SELECT created_instance_id, requested_node_type,
           requested_driver_contract_version, requested.capability
    FROM unnest(requested_capabilities) requested(capability)
    ORDER BY requested.capability;

    RETURN created_instance_id;
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_create_relay_node_asset(uuid,text,text,text,text,text,text[])
    OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_create_relay_node_asset(uuid,text,text,text,text,text,text[])
    FROM PUBLIC, relay_control_runtime, relay_control_asset_registrar;
GRANT EXECUTE ON FUNCTION public.control_create_relay_node_asset(uuid,text,text,text,text,text,text[])
    TO relay_control_runtime;

REVOKE ALL ON public.relay_node_asset_replacements, public.asset_registry_generations
FROM PUBLIC, relay_control_runtime, relay_control_asset_registrar;
GRANT SELECT,INSERT ON public.relay_node_asset_replacements TO relay_control_runtime;
GRANT SELECT ON public.asset_registry_generations TO relay_control_runtime;
REVOKE ALL ON public.relay_node_assets FROM relay_control_runtime;
GRANT SELECT (
    instance_id, display_name, node_type, driver_contract_version,
    management_endpoint, reader_secret_configured, lifecycle_status, revision,
    retired_at, retired_by, retire_reason, created_at, updated_at
) ON public.relay_node_assets TO relay_control_runtime;
GRANT UPDATE (
    display_name, management_endpoint, reader_secret_ref, lifecycle_status,
    revision, retired_at, retired_by, retire_reason, updated_at
) ON public.relay_node_assets TO relay_control_runtime;
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON public.node_capabilities FROM relay_control_runtime;
GRANT SELECT ON public.relay_node_inventory_monitoring_activations TO relay_control_runtime;
GRANT UPDATE (
    effective_to, end_reason, end_actor, end_recorded_at,
    cancelled_at, cancelled_by, cancel_reason
) ON public.relay_node_inventory_monitoring_activations TO relay_control_runtime;
REVOKE EXECUTE ON FUNCTION public.control_authorize_account_inventory_poll_dispatch(uuid, integer, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_authorize_account_inventory_poll_dispatch(uuid, integer, uuid) TO relay_control_runtime;
REVOKE EXECUTE ON FUNCTION public.control_abandon_node_inventory_poll_runs(uuid, timestamptz, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_abandon_node_inventory_poll_runs(uuid, timestamptz, text) TO relay_control_runtime;
REVOKE EXECUTE ON FUNCTION public.control_advance_asset_registry_node_generation() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_advance_asset_registry_node_generation() TO relay_control_runtime;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_schedule_account_inventory_poll_runs(
    poll_period_seconds integer,
    poll_start_grace_seconds integer,
    poll_max_attempts integer,
    schedule_limit integer
) RETURNS jsonb
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    database_now timestamptz := clock_timestamp();
    current_slot timestamptz;
    inconsistent_count bigint;
    eligible_count bigint;
    schedule_result jsonb;
BEGIN
    IF poll_period_seconds <> 300
       OR poll_start_grace_seconds < 1 OR poll_start_grace_seconds >= poll_period_seconds
       OR poll_max_attempts < 1 OR poll_max_attempts > 10
       OR schedule_limit < 1 OR schedule_limit > 1000 THEN
        RAISE EXCEPTION 'invalid account inventory poll scheduling policy'
            USING ERRCODE = '22023';
    END IF;
    current_slot := to_timestamp(
        floor(extract(epoch FROM database_now) / 300) * 300
    );
    IF current_slot + make_interval(secs => poll_start_grace_seconds) <= database_now THEN
        RETURN jsonb_build_object(
            'scheduled_at', current_slot,
            'eligible_count', 0,
            'created_count', 0
        );
    END IF;

    SELECT count(*) INTO inconsistent_count
    FROM public.relay_node_assets AS asset
    WHERE EXISTS (
        SELECT 1 FROM public.node_capabilities AS capability
        WHERE capability.instance_id = asset.instance_id
          AND capability.node_type = asset.node_type
          AND capability.driver_contract_version = asset.driver_contract_version
          AND capability.capability = 'management_account_inventory_read'
          AND asset.lifecycle_status = 'active'
    )
      AND EXISTS (
        SELECT 1 FROM public.relay_node_inventory_monitoring_activations AS monitoring
        WHERE monitoring.instance_id = asset.instance_id
          AND monitoring.active_range @> current_slot
    )
      AND (
        (SELECT count(*) FROM public.provider_inventory_policy_activations AS activation
         WHERE activation.node_type = asset.node_type
           AND activation.driver_contract_version = asset.driver_contract_version
           AND activation.active_range @> current_slot) <> 1
        OR NOT EXISTS (
            SELECT 1
            FROM public.provider_inventory_policy_activations AS activation
            JOIN public.provider_inventory_policy_bindings AS binding
              ON binding.node_type = activation.node_type
             AND binding.driver_contract_version = activation.driver_contract_version
             AND binding.policy_version_id = activation.policy_version_id
            JOIN public.provider_inventory_policy_versions AS policy
              ON policy.policy_version_id = activation.policy_version_id
             AND policy.node_type = activation.node_type
             AND policy.driver_contract_version = activation.driver_contract_version
            WHERE activation.node_type = asset.node_type
              AND activation.driver_contract_version = asset.driver_contract_version
              AND activation.active_range @> current_slot
              AND cardinality(policy.active_providers) > 0
        )
      );
    IF inconsistent_count <> 0 THEN
        RAISE EXCEPTION 'account inventory poll eligibility is inconsistent'
            USING ERRCODE = '23514';
    END IF;

    SELECT count(DISTINCT asset.instance_id) INTO eligible_count
    FROM public.relay_node_assets AS asset
    JOIN public.node_capabilities AS capability
      ON capability.instance_id = asset.instance_id
     AND capability.node_type = asset.node_type
     AND capability.driver_contract_version = asset.driver_contract_version
     AND capability.capability = 'management_account_inventory_read'
          AND asset.lifecycle_status = 'active'
    JOIN public.relay_node_inventory_monitoring_activations AS monitoring
      ON monitoring.instance_id = asset.instance_id
     AND monitoring.active_range @> current_slot
    JOIN public.provider_inventory_policy_activations AS activation
      ON activation.node_type = asset.node_type
     AND activation.driver_contract_version = asset.driver_contract_version
     AND activation.active_range @> current_slot
    JOIN public.provider_inventory_policy_bindings AS binding
      ON binding.node_type = activation.node_type
     AND binding.driver_contract_version = activation.driver_contract_version
     AND binding.policy_version_id = activation.policy_version_id
    JOIN public.provider_inventory_policy_versions AS policy
      ON policy.policy_version_id = activation.policy_version_id
     AND policy.node_type = activation.node_type
     AND policy.driver_contract_version = activation.driver_contract_version
     AND cardinality(policy.active_providers) > 0;
    IF eligible_count > schedule_limit THEN
        RAISE EXCEPTION 'account inventory poll capacity exceeded'
            USING ERRCODE = '22023';
    END IF;

    WITH eligible AS (
        SELECT asset.instance_id, asset.node_type, asset.driver_contract_version,
               activation.policy_version_id
        FROM public.relay_node_assets AS asset
        JOIN public.node_capabilities AS capability
          ON capability.instance_id = asset.instance_id
         AND capability.node_type = asset.node_type
         AND capability.driver_contract_version = asset.driver_contract_version
         AND capability.capability = 'management_account_inventory_read'
          AND asset.lifecycle_status = 'active'
        JOIN public.relay_node_inventory_monitoring_activations AS monitoring
          ON monitoring.instance_id = asset.instance_id
         AND monitoring.active_range @> current_slot
        JOIN public.provider_inventory_policy_activations AS activation
          ON activation.node_type = asset.node_type
         AND activation.driver_contract_version = asset.driver_contract_version
         AND activation.active_range @> current_slot
        JOIN public.provider_inventory_policy_bindings AS binding
          ON binding.node_type = activation.node_type
         AND binding.driver_contract_version = activation.driver_contract_version
         AND binding.policy_version_id = activation.policy_version_id
        JOIN public.provider_inventory_policy_versions AS policy
          ON policy.policy_version_id = activation.policy_version_id
         AND policy.node_type = activation.node_type
         AND policy.driver_contract_version = activation.driver_contract_version
         AND cardinality(policy.active_providers) > 0
        ORDER BY EXISTS (
            SELECT 1 FROM public.account_inventory_poll_runs AS existing
            WHERE existing.instance_id = asset.instance_id
              AND existing.scheduled_at = current_slot
        ), asset.instance_id
        LIMIT schedule_limit
        FOR UPDATE OF asset SKIP LOCKED
    ), inserted AS (
        INSERT INTO public.account_inventory_poll_runs (
            poll_run_id, instance_id, node_type, driver_contract_version,
            scheduled_at, provider_policy_version, max_attempts,
            poll_start_grace_seconds, created_at
        )
        SELECT gen_random_uuid(), eligible.instance_id, eligible.node_type,
               eligible.driver_contract_version, current_slot,
               eligible.policy_version_id, poll_max_attempts,
               poll_start_grace_seconds, database_now
        FROM eligible
        ON CONFLICT (instance_id, scheduled_at) DO NOTHING
        RETURNING 1
    )
    SELECT jsonb_build_object(
        'scheduled_at', current_slot,
        'eligible_count', eligible_count,
        'created_count', (SELECT count(*) FROM inserted)
    )
    INTO schedule_result;
    RETURN schedule_result;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_claim_account_inventory_poll_run(
    new_fencing_token uuid,
    lease_seconds integer
) RETURNS jsonb
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    database_now timestamptz;
    candidate_id uuid;
    candidate_node uuid;
    lifecycle_reason text;
    run_record public.account_inventory_poll_runs%ROWTYPE;
    claim_result jsonb;
BEGIN
    IF new_fencing_token IS NULL OR lease_seconds < 1 OR lease_seconds > 120 THEN
        RAISE EXCEPTION 'invalid account inventory poll claim policy'
            USING ERRCODE = '22023';
    END IF;
    -- Candidate discovery is deliberately non-locking.  The serialization
    -- boundary below is always Node first, then its poll run.
    SELECT run.poll_run_id, run.instance_id
    INTO candidate_id, candidate_node
    FROM public.account_inventory_poll_runs AS run
    JOIN public.relay_node_assets AS asset ON asset.instance_id=run.instance_id
    WHERE run.status IN ('pending', 'retry_wait')
      AND (
        asset.lifecycle_status <> 'active'
        OR (
          run.attempt_count < run.max_attempts
          AND run.scheduled_at + make_interval(secs => run.poll_start_grace_seconds) > clock_timestamp()
          AND EXISTS (
            SELECT 1
            FROM public.relay_node_inventory_monitoring_activations AS monitoring
            WHERE monitoring.instance_id=run.instance_id
              AND monitoring.cancelled_at IS NULL
              AND clock_timestamp() <@ monitoring.active_range
          )
        )
      )
    ORDER BY run.scheduled_at, run.instance_id
    LIMIT 1;
    IF candidate_id IS NULL THEN
        RETURN NULL;
    END IF;

    SELECT CASE asset.retire_reason
             WHEN 'replacement' THEN 'node_replaced'
             WHEN 'administrator_retire' THEN 'node_retired'
           END
    INTO lifecycle_reason
    FROM public.relay_node_assets AS asset
    WHERE asset.instance_id=candidate_node
    FOR UPDATE;

    SELECT * INTO run_record
    FROM public.account_inventory_poll_runs AS run
    WHERE run.poll_run_id=candidate_id
      AND run.status IN ('pending', 'retry_wait')
    FOR UPDATE;
    IF NOT FOUND THEN
        RETURN NULL;
    END IF;

    database_now := clock_timestamp();
    IF lifecycle_reason IS NOT NULL THEN
        UPDATE public.account_inventory_poll_runs AS run
        SET status='abandoned',
            lease_expires_at=NULL,
            lease_fencing_token=NULL,
            dispatch_authorized_attempt=NULL,
            dispatch_authorized_at=NULL,
            dispatch_authorized_fencing_token=NULL,
            abandoned_at=database_now,
            execution_reason=lifecycle_reason
        WHERE run.poll_run_id=candidate_id;
        RETURN NULL;
    END IF;

    IF run_record.attempt_count >= run_record.max_attempts
       OR run_record.scheduled_at + make_interval(secs => run_record.poll_start_grace_seconds) <= database_now
       OR NOT EXISTS (
         SELECT 1
         FROM public.relay_node_inventory_monitoring_activations AS monitoring
         WHERE monitoring.instance_id=candidate_node
           AND monitoring.cancelled_at IS NULL
           AND database_now <@ monitoring.active_range
       ) THEN
        RETURN NULL;
    END IF;

    UPDATE public.account_inventory_poll_runs AS run
    SET status = 'running',
        attempt_count = run.attempt_count + 1,
        first_started_at = coalesce(run.first_started_at, database_now),
        last_started_at = database_now,
        lease_expires_at = database_now + make_interval(secs => lease_seconds),
        lease_fencing_token = new_fencing_token,
        execution_reason = NULL
    WHERE run.poll_run_id = candidate_id;

    SELECT jsonb_build_object(
           'poll_run_id', run.poll_run_id,
           'instance_id', run.instance_id,
           'node_type', run.node_type,
           'driver_contract_version', run.driver_contract_version,
           'scheduled_at', run.scheduled_at,
           'provider_policy_version', run.provider_policy_version,
           'attempt_count', run.attempt_count,
           'max_attempts', run.max_attempts,
           'first_started_at', run.first_started_at,
           'last_started_at', run.last_started_at,
           'lease_expires_at', run.lease_expires_at,
           'lease_fencing_token', run.lease_fencing_token,
           'grace_remaining_milliseconds', greatest(0::bigint, floor(extract(epoch FROM (
               run.scheduled_at + make_interval(secs => run.poll_start_grace_seconds) - database_now
           )) * 1000)::bigint),
           'management_endpoint', asset.management_endpoint,
           'reader_secret_ref', asset.reader_secret_ref,
           'active_providers', policy.active_providers,
           'out_of_scope_providers', policy.out_of_scope_providers
    ) INTO claim_result
    FROM public.account_inventory_poll_runs AS run
    JOIN public.relay_node_assets AS asset
      ON asset.instance_id = run.instance_id
     AND asset.node_type = run.node_type
     AND asset.driver_contract_version = run.driver_contract_version
     AND asset.lifecycle_status = 'active'
    JOIN public.provider_inventory_policy_versions AS policy
      ON policy.policy_version_id = run.provider_policy_version
     AND policy.node_type = run.node_type
     AND policy.driver_contract_version = run.driver_contract_version
    WHERE run.poll_run_id = candidate_id;
    RETURN claim_result;
END;
$$;
-- +goose StatementEnd

-- Expired authorized attempts re-read Node lifecycle before choosing retry.
-- Candidate discovery is non-locking; the durable order is Node, then run.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_reconcile_account_inventory_poll_run()
RETURNS SETOF public.account_inventory_poll_runs
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    database_now timestamptz;
    discovery_now timestamptz := clock_timestamp();
    candidate_id uuid;
    candidate_node uuid;
    lifecycle_reason text;
    run_record public.account_inventory_poll_runs%ROWTYPE;
BEGIN
    SELECT run.poll_run_id, run.instance_id
    INTO candidate_id, candidate_node
    FROM public.account_inventory_poll_runs AS run
    WHERE (run.status IN ('pending', 'retry_wait')
           AND run.scheduled_at + make_interval(secs => run.poll_start_grace_seconds) <= discovery_now)
       OR (run.status = 'running' AND run.lease_expires_at <= discovery_now)
    ORDER BY run.scheduled_at, run.instance_id
    LIMIT 1;
    IF candidate_id IS NULL THEN
        RETURN;
    END IF;

    SELECT CASE asset.retire_reason
             WHEN 'replacement' THEN 'node_replaced'
             WHEN 'administrator_retire' THEN 'node_retired'
           END
    INTO lifecycle_reason
    FROM public.relay_node_assets AS asset
    WHERE asset.instance_id = candidate_node
    FOR UPDATE;

    SELECT * INTO run_record
    FROM public.account_inventory_poll_runs AS run
    WHERE run.poll_run_id = candidate_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RETURN;
    END IF;

    database_now := clock_timestamp();
    IF NOT ((run_record.status IN ('pending', 'retry_wait')
             AND run_record.scheduled_at + make_interval(secs => run_record.poll_start_grace_seconds) <= database_now)
         OR (run_record.status = 'running' AND run_record.lease_expires_at <= database_now)) THEN
        RETURN;
    END IF;

    RETURN QUERY
    UPDATE public.account_inventory_poll_runs AS run
    SET status = CASE
            WHEN lifecycle_reason IS NULL
             AND run_record.status = 'running'
             AND run_record.scheduled_at + make_interval(secs => run_record.poll_start_grace_seconds) > database_now
             AND run_record.attempt_count < run_record.max_attempts THEN 'retry_wait'
            ELSE 'abandoned'
        END,
        lease_expires_at = NULL,
        lease_fencing_token = NULL,
        dispatch_authorized_attempt = NULL,
        dispatch_authorized_at = NULL,
        dispatch_authorized_fencing_token = NULL,
        abandoned_at = CASE
            WHEN lifecycle_reason IS NULL
             AND run_record.status = 'running'
             AND run_record.scheduled_at + make_interval(secs => run_record.poll_start_grace_seconds) > database_now
             AND run_record.attempt_count < run_record.max_attempts THEN NULL
            ELSE database_now
        END,
        execution_reason = coalesce(lifecycle_reason, CASE
            WHEN run_record.status = 'running'
             AND run_record.scheduled_at + make_interval(secs => run_record.poll_start_grace_seconds) > database_now
             AND run_record.attempt_count < run_record.max_attempts THEN 'lease_expired'
            WHEN run_record.attempt_count >= run_record.max_attempts THEN 'max_attempts_exhausted'
            ELSE 'poll_start_grace_expired'
        END)
    WHERE run.poll_run_id = candidate_id
    RETURNING run.*;
END;
$$;
-- +goose StatementEnd

-- Finalization serializes on the owning Node before the existing run/provider
-- state machine.  Thus promotion-first may commit current truth, while a
-- lifecycle-first finalizer preserves evidence with the lifecycle skip reason.
-- +goose StatementBegin
DO $$ BEGIN
    EXECUTE 'ALTER FUNCTION public.control_finalize_account_inventory_poll_run_with_lifecycle_v2(uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb) RENAME TO control_finalize_account_inventory_poll_run_with_lifecycle_stage1_v2';
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_finalize_account_inventory_poll_run_with_lifecycle_v2(
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
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE
    target_instance_id uuid;
    target_lifecycle text;
    target_retire_reason text;
    database_now timestamptz;
    promotion_fence_reason text;
    prior_promotion_fence text := coalesce(
        current_setting('relay_control.node_promotion_fence', true), '');
BEGIN
    SELECT instance_id INTO target_instance_id
    FROM public.account_inventory_poll_runs
    WHERE poll_run_id=target_poll_run_id
      AND status='running'
      AND lease_fencing_token=expected_fencing_token
    FOR UPDATE;
    IF target_instance_id IS NULL THEN
        RETURN;
    END IF;

    SELECT lifecycle_status,retire_reason
    INTO target_lifecycle,target_retire_reason
    FROM public.relay_node_assets
    WHERE instance_id=target_instance_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RETURN;
    END IF;

    database_now:=clock_timestamp();
    IF target_lifecycle='retired' THEN
        promotion_fence_reason:=CASE target_retire_reason
            WHEN 'replacement' THEN 'node_replaced'
            WHEN 'administrator_retire' THEN 'node_retired'
        END;
    ELSIF target_lifecycle='active' AND NOT EXISTS (
        SELECT 1
        FROM public.relay_node_inventory_monitoring_activations monitoring
        WHERE monitoring.instance_id=target_instance_id
          AND monitoring.cancelled_at IS NULL
          AND database_now <@ monitoring.active_range
    ) THEN
        promotion_fence_reason:='monitoring_ineligible';
    END IF;

    PERFORM set_config(
        'relay_control.node_promotion_fence',
        coalesce(promotion_fence_reason,''), true);
    RETURN QUERY
    SELECT * FROM public.control_finalize_account_inventory_poll_run_with_lifecycle_stage1_v2(
        target_poll_run_id, expected_fencing_token, result_transport_success,
        result_response_shape_valid, result_contract_valid, result_inventory_mode,
        result_node_identity_complete, result_snapshot_complete, result_degraded,
        result_result, result_reason, result_source_record_count,
        result_identifiable_record_count, result_unidentified_record_count,
        result_unsupported_provider_count, result_out_of_scope_provider_count,
        result_node_version, result_node_commit, provider_results, snapshot_items,
        duplicate_evidence
    );
    PERFORM set_config(
        'relay_control.node_promotion_fence', prior_promotion_fence, true);
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_finalize_account_inventory_poll_run_with_lifecycle_v2(
    uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,
    integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb
) OWNER TO relay_control_migrator;
REVOKE ALL ON FUNCTION public.control_finalize_account_inventory_poll_run_with_lifecycle_v2(
    uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,
    integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb
) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_finalize_account_inventory_poll_run_with_lifecycle_v2(
    uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,
    integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb
) TO relay_control_runtime;
REVOKE ALL ON FUNCTION public.control_finalize_account_inventory_poll_run_with_lifecycle_stage1_v2(
    uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,
    integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb
) FROM PUBLIC, relay_control_runtime;
REVOKE ALL ON FUNCTION public.control_finalize_account_inventory_poll_run_v2(
    uuid,uuid,boolean,boolean,boolean,text,boolean,boolean,boolean,text,text,
    integer,integer,integer,integer,integer,text,text,jsonb,jsonb,jsonb
) FROM relay_control_runtime;

-- Recompose the existing operational monitoring writer with the Stage-2
-- Node-first lock graph and generation semantics.  Future scheduling remains
-- an operational capability; cancelled evidence is never reused.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_set_node_inventory_monitoring(
    monitored_instance_id uuid,
    monitoring_enabled boolean,
    requested_effective_at timestamptz,
    monitoring_reason text,
    monitoring_actor text
) RETURNS uuid
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    boundary timestamptz;
    database_now timestamptz;
    activation_id uuid;
    existing public.relay_node_inventory_monitoring_activations%ROWTYPE;
    node_active boolean;
BEGIN
    IF monitored_instance_id IS NULL OR monitoring_enabled IS NULL
       OR monitoring_reason IS NULL OR monitoring_actor IS NULL
       OR octet_length(monitoring_actor) NOT BETWEEN 1 AND 128
       OR monitoring_actor !~ '^[A-Za-z0-9][A-Za-z0-9._@:-]*$'
       OR (monitoring_enabled AND monitoring_reason NOT IN
           ('deployment_enable','scheduled_enable','reconciliation'))
       OR (NOT monitoring_enabled AND monitoring_reason NOT IN
           ('deployment_disable','scheduled_disable','reconciliation')) THEN
        RAISE EXCEPTION 'monitoring activation metadata is invalid' USING ERRCODE='22023';
    END IF;

    SELECT lifecycle_status='active' INTO node_active
    FROM public.relay_node_assets
    WHERE instance_id=monitored_instance_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'monitoring target is not registered' USING ERRCODE='23503';
    END IF;
    IF NOT node_active THEN
        RAISE EXCEPTION 'monitoring target is retired' USING ERRCODE='23514';
    END IF;

    PERFORM monitoring_activation_id
    FROM public.relay_node_inventory_monitoring_activations
    WHERE instance_id=monitored_instance_id
    ORDER BY effective_from, monitoring_activation_id
    FOR UPDATE;

    database_now := clock_timestamp();
    boundary := COALESCE(requested_effective_at, database_now);
    IF requested_effective_at IS NOT NULL AND boundary < database_now THEN
        RAISE EXCEPTION 'monitoring activation cannot be backfilled' USING ERRCODE='22023';
    END IF;
    SELECT * INTO existing
    FROM public.relay_node_inventory_monitoring_activations
    WHERE instance_id=monitored_instance_id AND cancelled_at IS NULL
      AND boundary <@ active_range
    ORDER BY effective_from, monitoring_activation_id
    LIMIT 1;

    IF monitoring_enabled THEN
        IF FOUND THEN RETURN existing.monitoring_activation_id; END IF;
        activation_id := gen_random_uuid();
        INSERT INTO public.relay_node_inventory_monitoring_activations(
            monitoring_activation_id,instance_id,effective_from,reason,actor,created_at)
        VALUES(activation_id,monitored_instance_id,boundary,monitoring_reason,
               monitoring_actor,LEAST(clock_timestamp(),boundary));
        IF boundary <= database_now THEN
            UPDATE public.asset_registry_generations
            SET node_generation=node_generation+1
            WHERE singleton_id=1 AND node_generation<9223372036854775807;
            IF NOT FOUND THEN RAISE EXCEPTION 'node generation exhausted' USING ERRCODE='22003'; END IF;
        END IF;
        RETURN activation_id;
    END IF;

    IF NOT FOUND THEN
        SELECT * INTO existing
        FROM public.relay_node_inventory_monitoring_activations
        WHERE instance_id=monitored_instance_id AND effective_to=boundary
        ORDER BY end_recorded_at DESC LIMIT 1;
        IF FOUND THEN
            IF existing.end_reason IS DISTINCT FROM monitoring_reason
               OR existing.end_actor IS DISTINCT FROM monitoring_actor THEN
                RAISE EXCEPTION 'monitoring deactivation conflicts with existing boundary' USING ERRCODE='23505';
            END IF;
            RETURN existing.monitoring_activation_id;
        END IF;
        RETURN NULL;
    END IF;
    IF existing.effective_from=boundary THEN
        RAISE EXCEPTION 'monitoring activation boundary conflicts' USING ERRCODE='23P01';
    END IF;
    UPDATE public.relay_node_inventory_monitoring_activations
    SET effective_to=boundary,end_reason=monitoring_reason,end_actor=monitoring_actor,
        end_recorded_at=clock_timestamp()
    WHERE monitoring_activation_id=existing.monitoring_activation_id;
    IF boundary <= database_now THEN
        UPDATE public.asset_registry_generations
        SET node_generation=node_generation+1
        WHERE singleton_id=1 AND node_generation<9223372036854775807;
        IF NOT FOUND THEN RAISE EXCEPTION 'node generation exhausted' USING ERRCODE='22003'; END IF;
    END IF;
    RETURN existing.monitoring_activation_id;
END;
$$;
-- +goose StatementEnd

-- Current operational projections share the same lifecycle and monitoring
-- eligibility predicate. Historical evidence functions remain unchanged.
-- +goose StatementBegin
CREATE FUNCTION public.control_node_currently_eligible(target_instance_id uuid)
RETURNS boolean
LANGUAGE sql
SECURITY DEFINER
STABLE
SET search_path = pg_catalog
AS $$
    SELECT EXISTS (
        SELECT 1
        FROM public.relay_node_assets AS asset
        WHERE asset.instance_id = target_instance_id
          AND asset.lifecycle_status = 'active'
          AND EXISTS (
              SELECT 1
              FROM public.relay_node_inventory_monitoring_activations AS monitoring
              WHERE monitoring.instance_id = asset.instance_id
                AND monitoring.cancelled_at IS NULL
                AND statement_timestamp() <@ monitoring.active_range
          )
    )
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_node_currently_eligible(uuid)
    OWNER TO relay_control_migrator;
REVOKE EXECUTE ON FUNCTION public.control_node_currently_eligible(uuid) FROM PUBLIC;

-- Keep sqlc's schema parser on the public function identity while PostgreSQL
-- preserves the Stage-1 body under a private compatibility name.
-- +goose StatementBegin
DO $$ BEGIN
    EXECUTE 'ALTER FUNCTION public.control_query_current_account_inventory_v1(uuid,text,text,text,text,text,integer) RENAME TO control_query_current_account_inventory_stage1_v1';
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_query_current_account_inventory_v1(
    target_instance_id uuid, target_provider text, target_lifecycle text,
    target_basic_status text, target_normalized_email text,
    after_account_key text, page_limit integer
) RETURNS TABLE (
    instance_id uuid, provider text, account_key text, normalized_email text,
    basic_status text, lifecycle text, consecutive_missing_count integer,
    first_seen_at timestamptz, last_seen_at timestamptz,
    missing_since timestamptz, out_of_scope_since timestamptz,
    last_refresh_at timestamptz, next_retry_at timestamptz,
    source_updated_at timestamptz, provider_last_complete_at timestamptz,
    provider_degraded boolean, snapshot_freshness text
)
LANGUAGE plpgsql SECURITY DEFINER STABLE SET search_path = pg_catalog AS $$
BEGIN
    IF NOT public.control_node_currently_eligible(target_instance_id) THEN
        RETURN;
    END IF;
    RETURN QUERY SELECT *
    FROM public.control_query_current_account_inventory_stage1_v1(
        target_instance_id, target_provider, target_lifecycle,
        target_basic_status, target_normalized_email, after_account_key, page_limit
    );
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

-- +goose StatementBegin
DO $$ BEGIN
    EXECUTE 'ALTER FUNCTION public.control_list_current_account_inventory_lifecycle(uuid,text,text,text,integer) RENAME TO control_list_current_account_inventory_lifecycle_stage1';
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_list_current_account_inventory_lifecycle(
    target_instance_id uuid, target_provider text, target_lifecycle text,
    after_account_key text, page_limit integer
) RETURNS TABLE (
    provider text, account_key text, normalized_email text, basic_status text,
    success_count bigint, failed_count bigint, recent_request_count bigint,
    last_refresh_at timestamptz, next_retry_at timestamptz,
    source_updated_at timestamptz, lifecycle text,
    consecutive_missing_count integer, missing_since timestamptz,
    out_of_scope_since timestamptz, first_seen_at timestamptz,
    last_seen_at timestamptz, current_poll_run_id uuid,
    current_scheduled_at timestamptz, source_observed_at timestamptz,
    source_node_version text, source_node_commit text, updated_at timestamptz
)
LANGUAGE plpgsql SECURITY DEFINER STABLE SET search_path = pg_catalog AS $$
BEGIN
    IF NOT public.control_node_currently_eligible(target_instance_id) THEN
        RETURN;
    END IF;
    RETURN QUERY SELECT *
    FROM public.control_list_current_account_inventory_lifecycle_stage1(
        target_instance_id, target_provider, target_lifecycle,
        after_account_key, page_limit
    );
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_list_current_account_inventory_lifecycle(
    uuid, text, text, text, integer
) OWNER TO relay_control_migrator;
REVOKE EXECUTE ON FUNCTION public.control_list_current_account_inventory_lifecycle(
    uuid, text, text, text, integer
) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_list_current_account_inventory_lifecycle(
    uuid, text, text, text, integer
) TO relay_control_runtime;

CREATE OR REPLACE FUNCTION public.control_list_account_inventory_lifecycle_metrics()
RETURNS TABLE (instance_id uuid, provider text, lifecycle text, account_count bigint)
LANGUAGE sql SECURITY DEFINER STABLE SET search_path = pg_catalog AS $$
    SELECT account.instance_id, account.provider, account.lifecycle, count(*)::bigint
    FROM public.account_inventory AS account
    WHERE public.control_node_currently_eligible(account.instance_id)
    GROUP BY account.instance_id, account.provider, account.lifecycle
    ORDER BY account.instance_id, account.provider, account.lifecycle
$$;

CREATE OR REPLACE FUNCTION public.control_query_account_request_quality_targets_v1()
RETURNS TABLE(instance_id uuid, node_type text, driver_contract_version text,
              management_endpoint text, reader_secret_ref text, capabilities text[])
LANGUAGE sql SECURITY DEFINER STABLE SET search_path = pg_catalog AS $$
    SELECT asset.instance_id, asset.node_type, asset.driver_contract_version,
           asset.management_endpoint, asset.reader_secret_ref,
           ARRAY['management_account_inventory_read']::text[]
    FROM public.relay_node_assets AS asset
    JOIN public.node_capabilities AS capability
      ON capability.instance_id = asset.instance_id
     AND capability.node_type = asset.node_type
     AND capability.driver_contract_version = asset.driver_contract_version
     AND capability.capability = 'management_account_inventory_read'
    WHERE asset.node_type = 'cliproxyapi'
      AND asset.driver_contract_version = 'cliproxyapi.auth-files.v1'
      AND public.control_node_currently_eligible(asset.instance_id)
$$;

-- +goose StatementBegin
DO $$ BEGIN
    EXECUTE 'ALTER FUNCTION public.control_query_account_availability_v1(uuid,text[]) RENAME TO control_query_account_availability_stage1_v1';
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_query_account_availability_v1(
    target_node uuid, target_accounts text[]
) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER STABLE SET search_path = pg_catalog AS $$
BEGIN
    IF NOT public.control_node_currently_eligible(target_node) THEN
        RETURN '[]'::jsonb;
    END IF;
    RETURN public.control_query_account_availability_stage1_v1(
        target_node, target_accounts
    );
END;
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_query_account_availability_v1(uuid, text[])
    OWNER TO relay_control_migrator;
REVOKE EXECUTE ON FUNCTION public.control_query_account_availability_v1(uuid, text[]) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_query_account_availability_v1(uuid, text[]) TO relay_control_runtime;

-- +goose StatementBegin
DO $$ BEGIN
    EXECUTE 'ALTER FUNCTION public.control_query_problem_accounts_v1(text,uuid,text,text,text,text,timestamptz,text,uuid,integer) RENAME TO control_query_problem_accounts_stage1_v1';
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_query_problem_accounts_v1(
    target_provider text DEFAULT '', target_node uuid DEFAULT NULL,
    target_severity text DEFAULT '', target_reason text DEFAULT '',
    target_email text DEFAULT '', after_severity text DEFAULT NULL,
    after_since timestamptz DEFAULT NULL, after_email text DEFAULT NULL,
    after_node uuid DEFAULT NULL, page_limit integer DEFAULT 25
) RETURNS TABLE (
    instance_id uuid, node_name text, account_key text, email text,
    provider text, issues jsonb, availability jsonb, token_state text,
    last_refresh_at timestamptz, expected_valid_until timestamptz,
    next_retry_at timestamptz, last_success_at timestamptz,
    last_failure_at timestamptz, highest_severity text,
    oldest_active_since timestamptz
)
LANGUAGE sql SECURITY DEFINER STABLE SET search_path = pg_catalog AS $$
    SELECT result.*
    FROM public.control_query_problem_accounts_stage1_v1(
        target_provider, target_node, target_severity, target_reason,
        target_email, after_severity, after_since, after_email,
        after_node, page_limit
    ) AS result
    WHERE public.control_node_currently_eligible(result.instance_id)
$$;
-- +goose StatementEnd
ALTER FUNCTION public.control_query_problem_accounts_v1(
    text, uuid, text, text, text, text, timestamptz, text, uuid, integer
) OWNER TO relay_control_migrator;
REVOKE EXECUTE ON FUNCTION public.control_query_problem_accounts_v1(
    text, uuid, text, text, text, text, timestamptz, text, uuid, integer
) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_query_problem_accounts_v1(
    text, uuid, text, text, text, text, timestamptz, text, uuid, integer
) TO relay_control_runtime;

-- +goose Down
-- Stage 2 durable evidence is a forward-only compatibility boundary.
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'relay node asset lifecycle migration is forward-only' USING ERRCODE='55000';
END $$;
-- +goose StatementEnd
