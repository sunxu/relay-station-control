-- +goose Up

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
    'node.register','node.edit','node.retire','node.replace',
    'node.health','node.connection_test','node.monitoring_enable','node.monitoring_disable',
    'account.operation_failed','account.operation_applied','account.operation_noop'
));

-- +goose Down
DO $$
BEGIN
    RAISE EXCEPTION 'migration 44 is forward-only' USING ERRCODE='55000';
END;
$$;
