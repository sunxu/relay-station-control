-- +goose Up

-- Phase 1B persistence foundation for Cross-node Duplicate Ownership
-- (openspec/changes/add-cross-node-duplicate-ownership). This migration only
-- creates the occurrence/affected-node/evidence tables and their database-level
-- immutability guards frozen in design.md §1B.1-§1B.9. It does not implement
-- the detection worker, Phase 2 ownership query, or any API/UI/alerting.
--
-- Table creation order matters: cross_node_duplicate_occurrences is created
-- first (with no trigger yet), then the two child tables that FK to it, and
-- only afterwards is the occurrence mutation-guard function created and
-- attached -- because that function's latest_evaluation_id consistency check
-- references cross_node_duplicate_occurrence_evidence, which must already
-- exist.

-- +goose StatementBegin
CREATE FUNCTION public.control_reject_cross_node_duplicate_mutation() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    RAISE EXCEPTION '% is immutable', TG_TABLE_NAME USING ERRCODE = '42501';
END;
$$;
-- +goose StatementEnd

CREATE TABLE cross_node_duplicate_occurrences (
    occurrence_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    environment_id text NOT NULL
        REFERENCES environments(environment_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    account_key text NOT NULL,
    conflict_type text NOT NULL,
    status text NOT NULL,
    severity text NOT NULL,
    first_seen_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    resolved_at timestamptz,
    evidence_state text NOT NULL,
    last_fully_verified_at timestamptz,
    latest_evaluation_id uuid,
    -- account_key shape mirrors account_inventory_provider_valid +
    -- account_inventory_email_valid + account_inventory_key_valid
    -- (migrations/00007_account_inventory_lifecycle_foundation.sql L100-113)
    -- decomposed from a single column instead of separate provider/
    -- normalized_email columns: the provider charset below never contains
    -- ':', so the first ':' in account_key is always exactly the boundary
    -- Account Inventory itself would have placed between provider and
    -- normalized_email. This is not a second canonical shape -- it is the
    -- same shape verified by splitting the single stored column at that
    -- boundary. occurrence intentionally has no FK to account_inventory
    -- (occurrence history must survive Account Inventory retention/lifecycle
    -- churn per design.md §7 / openspec proposal Binding-independence
    -- boundary).
    CONSTRAINT cross_node_duplicate_occurrence_account_key_valid CHECK (
        octet_length(account_key) BETWEEN 3 AND 385
        AND position(':' in account_key) > 0
        AND octet_length(split_part(account_key, ':', 1)) BETWEEN 1 AND 64
        AND split_part(account_key, ':', 1) ~ '^[a-z0-9][a-z0-9._-]*$'
        AND octet_length(substr(account_key, position(':' in account_key) + 1)) BETWEEN 1 AND 320
        AND substr(account_key, position(':' in account_key) + 1)
            = lower(btrim(substr(account_key, position(':' in account_key) + 1)))
        AND substr(account_key, position(':' in account_key) + 1) !~ '[[:cntrl:]]'
    ),
    CONSTRAINT cross_node_duplicate_occurrence_conflict_type_fixed CHECK (
        conflict_type = 'cross_node_duplicate_ownership'
    ),
    CONSTRAINT cross_node_duplicate_occurrence_status_fixed CHECK (
        status IN ('ACTIVE', 'RESOLVED')
    ),
    CONSTRAINT cross_node_duplicate_occurrence_severity_fixed CHECK (
        severity = 'Critical'
    ),
    CONSTRAINT cross_node_duplicate_occurrence_evidence_state_fixed CHECK (
        evidence_state IN ('complete', 'degraded')
    ),
    CONSTRAINT cross_node_duplicate_occurrence_times_ordered CHECK (
        last_seen_at >= first_seen_at
    ),
    CONSTRAINT cross_node_duplicate_occurrence_resolved_shape CHECK (
        (status = 'RESOLVED') = (resolved_at IS NOT NULL)
    )
);

CREATE UNIQUE INDEX cross_node_duplicate_occurrence_active_uidx
    ON cross_node_duplicate_occurrences (environment_id, account_key, conflict_type)
    WHERE status = 'ACTIVE';

CREATE TABLE cross_node_duplicate_occurrence_nodes (
    occurrence_id uuid NOT NULL
        REFERENCES cross_node_duplicate_occurrences(occurrence_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    instance_id uuid NOT NULL
        REFERENCES relay_node_assets(instance_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    first_confirmed_at timestamptz NOT NULL,
    PRIMARY KEY (occurrence_id, instance_id)
);

CREATE TABLE cross_node_duplicate_occurrence_evidence (
    observation_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    occurrence_id uuid NOT NULL
        REFERENCES cross_node_duplicate_occurrences(occurrence_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    instance_id uuid NOT NULL
        REFERENCES relay_node_assets(instance_id)
        ON UPDATE RESTRICT ON DELETE RESTRICT,
    observation_kind text NOT NULL,
    -- retention-safe source pointer: deliberately no FK / ON DELETE action
    -- (design.md §1B.5) so that Account Inventory poll_run retention can
    -- proceed without ever touching this immutable table.
    source_poll_run_id uuid,
    source_provider text NOT NULL,
    source_scheduled_at timestamptz NOT NULL,
    source_completed_at timestamptz NOT NULL,
    evaluation_id uuid NOT NULL,
    evaluation_at timestamptz NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT cross_node_duplicate_evidence_kind_fixed CHECK (
        observation_kind IN ('owner_confirmed', 'absence_confirmed', 'degraded')
    ),
    CONSTRAINT cross_node_duplicate_evidence_provider_valid CHECK (
        octet_length(source_provider) BETWEEN 1 AND 64
        AND source_provider ~ '^[a-z0-9][a-z0-9._-]*$'
    ),
    CONSTRAINT cross_node_duplicate_evidence_no_future_eval CHECK (
        evaluation_at >= source_completed_at
    )
);

CREATE INDEX cross_node_duplicate_evidence_occurrence_idx
    ON cross_node_duplicate_occurrence_evidence (occurrence_id, recorded_at DESC);
-- One evaluation produces at most one observation per Node (Migration
-- Review P1 #3): a refresh/detect/resolve pass evaluates each affected Node
-- exactly once per evaluation_id. This unique index also covers the
-- (occurrence_id, evaluation_id) lookup as a leading-column prefix, so no
-- separate non-unique index is needed.
CREATE UNIQUE INDEX cross_node_duplicate_evidence_evaluation_node_uidx
    ON cross_node_duplicate_occurrence_evidence (occurrence_id, evaluation_id, instance_id);

-- +goose StatementBegin
CREATE FUNCTION public.control_enforce_cross_node_duplicate_occurrence_mutation() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'cross_node_duplicate_occurrences: DELETE is forbidden' USING ERRCODE = '42501';
    END IF;

    IF TG_OP = 'INSERT' THEN
        -- A new occurrence identity is only ever created via detect/reopen,
        -- which always starts ACTIVE; RESOLVED is only reachable through the
        -- ACTIVE -> RESOLVED UPDATE transition below, never by direct INSERT
        -- (Migration Review P1 #1).
        IF NEW.status IS DISTINCT FROM 'ACTIVE' OR NEW.resolved_at IS NOT NULL THEN
            RAISE EXCEPTION 'cross_node_duplicate_occurrences: INSERT must be ACTIVE with resolved_at NULL'
                USING ERRCODE = '23514';
        END IF;
    END IF;

    IF TG_OP = 'UPDATE' THEN
        IF NEW.occurrence_id IS DISTINCT FROM OLD.occurrence_id
           OR NEW.environment_id IS DISTINCT FROM OLD.environment_id
           OR NEW.account_key IS DISTINCT FROM OLD.account_key
           OR NEW.conflict_type IS DISTINCT FROM OLD.conflict_type
           OR NEW.severity IS DISTINCT FROM OLD.severity
           OR NEW.first_seen_at IS DISTINCT FROM OLD.first_seen_at THEN
            RAISE EXCEPTION 'cross_node_duplicate_occurrences: identity fields are immutable'
                USING ERRCODE = '23514';
        END IF;

        IF OLD.status = 'RESOLVED' THEN
            RAISE EXCEPTION 'cross_node_duplicate_occurrences: RESOLVED occurrence is immutable'
                USING ERRCODE = '23514';
        END IF;

        IF OLD.status = 'ACTIVE' AND NEW.status = 'ACTIVE' THEN
            NULL;
        ELSIF OLD.status = 'ACTIVE' AND NEW.status = 'RESOLVED' THEN
            IF NEW.resolved_at IS NULL THEN
                RAISE EXCEPTION 'cross_node_duplicate_occurrences: resolved_at MUST be set in the same UPDATE as ACTIVE -> RESOLVED'
                    USING ERRCODE = '23514';
            END IF;
        ELSE
            RAISE EXCEPTION 'cross_node_duplicate_occurrences: invalid status transition'
                USING ERRCODE = '23514';
        END IF;
    END IF;

    -- latest_evaluation_id consistency (design.md §1B.2): whenever it is
    -- non-NULL on the row being written (INSERT or changed on UPDATE), a
    -- matching evidence evaluation MUST already exist for this occurrence.
    -- Not a physical FK: evaluation_id is not unique/PK on the evidence
    -- table (one evaluation produces N per-node observation rows).
    IF NEW.latest_evaluation_id IS NOT NULL
       AND (TG_OP = 'INSERT' OR NEW.latest_evaluation_id IS DISTINCT FROM OLD.latest_evaluation_id) THEN
        IF NOT EXISTS (
            SELECT 1 FROM public.cross_node_duplicate_occurrence_evidence e
            WHERE e.occurrence_id = NEW.occurrence_id
              AND e.evaluation_id = NEW.latest_evaluation_id
        ) THEN
            RAISE EXCEPTION 'cross_node_duplicate_occurrences: latest_evaluation_id must reference an existing evidence evaluation for this occurrence'
                USING ERRCODE = '23514';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER cross_node_duplicate_occurrence_enforce_mutation
BEFORE INSERT OR UPDATE OR DELETE ON cross_node_duplicate_occurrences
FOR EACH ROW EXECUTE FUNCTION public.control_enforce_cross_node_duplicate_occurrence_mutation();

CREATE TRIGGER cross_node_duplicate_occurrence_reject_truncate
BEFORE TRUNCATE ON cross_node_duplicate_occurrences
FOR EACH STATEMENT EXECUTE FUNCTION public.control_reject_cross_node_duplicate_mutation();

-- +goose StatementBegin
CREATE FUNCTION public.control_enforce_cross_node_duplicate_occurrence_node_mutation() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE
    parent_status text;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION 'cross_node_duplicate_occurrence_nodes: UPDATE is forbidden'
            USING ERRCODE = '42501';
    END IF;

    IF TG_OP = 'INSERT' THEN
        SELECT status INTO parent_status
            FROM public.cross_node_duplicate_occurrences
            WHERE occurrence_id = NEW.occurrence_id
            FOR UPDATE;
        IF parent_status IS DISTINCT FROM 'ACTIVE' THEN
            RAISE EXCEPTION 'cross_node_duplicate_occurrence_nodes: INSERT only allowed while parent occurrence is ACTIVE'
                USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;

    IF TG_OP = 'DELETE' THEN
        SELECT status INTO parent_status
            FROM public.cross_node_duplicate_occurrences
            WHERE occurrence_id = OLD.occurrence_id
            FOR UPDATE;
        IF parent_status IS DISTINCT FROM 'ACTIVE' THEN
            RAISE EXCEPTION 'cross_node_duplicate_occurrence_nodes: DELETE only allowed while parent occurrence is ACTIVE'
                USING ERRCODE = '23514';
        END IF;
        -- Fresh-absence-evidence correctness for this DELETE is an
        -- application/transaction concern (Phase 3/4 lifecycle logic), not a
        -- generic trigger concern; this guard only enforces the parent
        -- lifecycle boundary (design.md §1B.3).
        RETURN OLD;
    END IF;

    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER cross_node_duplicate_occurrence_node_enforce_mutation
BEFORE INSERT OR UPDATE OR DELETE ON cross_node_duplicate_occurrence_nodes
FOR EACH ROW EXECUTE FUNCTION public.control_enforce_cross_node_duplicate_occurrence_node_mutation();

CREATE TRIGGER cross_node_duplicate_occurrence_node_reject_truncate
BEFORE TRUNCATE ON cross_node_duplicate_occurrence_nodes
FOR EACH STATEMENT EXECUTE FUNCTION public.control_reject_cross_node_duplicate_mutation();

-- +goose StatementBegin
CREATE FUNCTION public.control_enforce_cross_node_duplicate_evidence_insert() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE
    parent_status text;
BEGIN
    -- Evidence may only be appended while the parent occurrence is ACTIVE
    -- (Migration Review P1 #2). This is only the parent-lifecycle boundary,
    -- not freshness/owner/absence business correctness, which stays a
    -- Phase 3/4 application concern. Resolve's normal write order is still
    -- evidence-first, occurrence-RESOLVED-second: the last evidence written
    -- as part of a resolve is always written while the parent is still
    -- ACTIVE.
    SELECT status INTO parent_status
        FROM public.cross_node_duplicate_occurrences
        WHERE occurrence_id = NEW.occurrence_id
        FOR UPDATE;
    IF parent_status IS DISTINCT FROM 'ACTIVE' THEN
        RAISE EXCEPTION 'cross_node_duplicate_occurrence_evidence: INSERT only allowed while parent occurrence is ACTIVE'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER cross_node_duplicate_evidence_enforce_insert
BEFORE INSERT ON cross_node_duplicate_occurrence_evidence
FOR EACH ROW EXECUTE FUNCTION public.control_enforce_cross_node_duplicate_evidence_insert();

CREATE TRIGGER cross_node_duplicate_evidence_reject_update_delete
BEFORE UPDATE OR DELETE ON cross_node_duplicate_occurrence_evidence
FOR EACH ROW EXECUTE FUNCTION public.control_reject_cross_node_duplicate_mutation();

CREATE TRIGGER cross_node_duplicate_evidence_reject_truncate
BEFORE TRUNCATE ON cross_node_duplicate_occurrence_evidence
FOR EACH STATEMENT EXECUTE FUNCTION public.control_reject_cross_node_duplicate_mutation();

-- Least-privilege runtime grants (design.md §1B / Migration Review item 11).
-- No table gets a blanket ALL PRIVILEGES grant; each grant lists exactly the
-- verbs the frozen lifecycle (detect/refresh/resolve/reopen) needs.
REVOKE ALL ON TABLE cross_node_duplicate_occurrences
FROM PUBLIC, relay_control_runtime;
GRANT SELECT, INSERT ON TABLE cross_node_duplicate_occurrences
TO relay_control_runtime;
GRANT UPDATE (
    last_seen_at, evidence_state, last_fully_verified_at,
    latest_evaluation_id, status, resolved_at
) ON TABLE cross_node_duplicate_occurrences
TO relay_control_runtime;

REVOKE ALL ON TABLE cross_node_duplicate_occurrence_nodes
FROM PUBLIC, relay_control_runtime;
GRANT SELECT, INSERT, DELETE ON TABLE cross_node_duplicate_occurrence_nodes
TO relay_control_runtime;

REVOKE ALL ON TABLE cross_node_duplicate_occurrence_evidence
FROM PUBLIC, relay_control_runtime;
GRANT SELECT, INSERT ON TABLE cross_node_duplicate_occurrence_evidence
TO relay_control_runtime;

REVOKE EXECUTE ON FUNCTION public.control_reject_cross_node_duplicate_mutation()
FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_reject_cross_node_duplicate_mutation()
TO relay_control_runtime;

REVOKE EXECUTE ON FUNCTION public.control_enforce_cross_node_duplicate_occurrence_mutation()
FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_enforce_cross_node_duplicate_occurrence_mutation()
TO relay_control_runtime;

REVOKE EXECUTE ON FUNCTION public.control_enforce_cross_node_duplicate_occurrence_node_mutation()
FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_enforce_cross_node_duplicate_occurrence_node_mutation()
TO relay_control_runtime;

REVOKE EXECUTE ON FUNCTION public.control_enforce_cross_node_duplicate_evidence_insert()
FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.control_enforce_cross_node_duplicate_evidence_insert()
TO relay_control_runtime;

-- +goose Down

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM cross_node_duplicate_occurrences LIMIT 1)
       OR EXISTS (SELECT 1 FROM cross_node_duplicate_occurrence_nodes LIMIT 1)
       OR EXISTS (SELECT 1 FROM cross_node_duplicate_occurrence_evidence LIMIT 1) THEN
        RAISE EXCEPTION 'cannot rollback cross node duplicate ownership foundation when occurrence, affected-node, or evidence history exists'
            USING ERRCODE = '55000';
    END IF;
END;
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION public.control_enforce_cross_node_duplicate_evidence_insert()
FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION public.control_enforce_cross_node_duplicate_occurrence_node_mutation()
FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION public.control_enforce_cross_node_duplicate_occurrence_mutation()
FROM relay_control_runtime;
REVOKE EXECUTE ON FUNCTION public.control_reject_cross_node_duplicate_mutation()
FROM relay_control_runtime;

REVOKE ALL ON TABLE cross_node_duplicate_occurrence_evidence
FROM relay_control_runtime;
REVOKE ALL ON TABLE cross_node_duplicate_occurrence_nodes
FROM relay_control_runtime;
REVOKE ALL ON TABLE cross_node_duplicate_occurrences
FROM relay_control_runtime;

DROP TRIGGER cross_node_duplicate_evidence_reject_truncate
    ON cross_node_duplicate_occurrence_evidence;
DROP TRIGGER cross_node_duplicate_evidence_reject_update_delete
    ON cross_node_duplicate_occurrence_evidence;
DROP TRIGGER cross_node_duplicate_evidence_enforce_insert
    ON cross_node_duplicate_occurrence_evidence;
DROP FUNCTION public.control_enforce_cross_node_duplicate_evidence_insert();

DROP TRIGGER cross_node_duplicate_occurrence_node_reject_truncate
    ON cross_node_duplicate_occurrence_nodes;
DROP TRIGGER cross_node_duplicate_occurrence_node_enforce_mutation
    ON cross_node_duplicate_occurrence_nodes;
DROP FUNCTION public.control_enforce_cross_node_duplicate_occurrence_node_mutation();

DROP TRIGGER cross_node_duplicate_occurrence_reject_truncate
    ON cross_node_duplicate_occurrences;
DROP TRIGGER cross_node_duplicate_occurrence_enforce_mutation
    ON cross_node_duplicate_occurrences;
DROP FUNCTION public.control_enforce_cross_node_duplicate_occurrence_mutation();

DROP TABLE cross_node_duplicate_occurrence_evidence;
DROP TABLE cross_node_duplicate_occurrence_nodes;
DROP TABLE cross_node_duplicate_occurrences;

DROP FUNCTION public.control_reject_cross_node_duplicate_mutation();
