-- +goose Up

-- Migration 38 accidentally omitted the asset lifecycle categories that are
-- still emitted by the Gateway and Relay Node paths.  Keep the taxonomy
-- bounded while restoring those categories alongside account_admin.
ALTER TABLE public.audit_logs DROP CONSTRAINT audit_logs_category_valid;
ALTER TABLE public.audit_logs ADD CONSTRAINT audit_logs_category_valid CHECK (
    category IN ('bootstrap','administrator','password','mfa','session',
                 'reauthentication','authorization','rate_limit',
                 'account_inventory','account_inventory_history',
                 'relay_binding','asset','asset_gateway','asset_node',
                 'account_admin')
);

-- +goose Down
DO $$ BEGIN
    RAISE EXCEPTION 'migration 48 is forward-only' USING ERRCODE='55000';
END $$;
