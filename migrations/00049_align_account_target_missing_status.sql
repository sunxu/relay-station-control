-- +goose Up

-- Keep the database-owned failure mapping aligned with the public API
-- contract.  Historical operations and receipts are intentionally unchanged.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.control_account_failure_http_status_v1(failure_code text)
RETURNS smallint
LANGUAGE sql IMMUTABLE STRICT SET search_path = pg_catalog AS $$
    SELECT CASE failure_code
        WHEN 'invalid_request' THEN 400
        WHEN 'upload_too_large' THEN 413
        WHEN 'upload_invalid' THEN 400
        WHEN 'identity_mismatch' THEN 400
        WHEN 'node_not_found' THEN 404
        WHEN 'unsupported_provider' THEN 409
        WHEN 'node_retired' THEN 409
        WHEN 'node_monitoring_ineligible' THEN 409
        WHEN 'account_target_not_found' THEN 404
        WHEN 'account_target_ambiguous' THEN 409
        WHEN 'account_target_exists' THEN 409
        WHEN 'account_filename_conflict' THEN 409
        WHEN 'account_operation_in_progress' THEN 409
        WHEN 'unsupported_node_version' THEN 503
        WHEN 'node_management_unavailable' THEN 503
        WHEN 'service_unavailable' THEN 503
        ELSE NULL
    END::smallint
$$;
-- +goose StatementEnd

-- +goose Down
DO $$ BEGIN RAISE EXCEPTION 'migration 49 is forward-only' USING ERRCODE='55000'; END $$;
