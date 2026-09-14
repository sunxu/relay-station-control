-- +goose Up

-- The atomic admission function in migration 42 is the only permitted path
-- for a prepared no-op. The earlier terminalizer could bypass same-account
-- blocker admission, so it must not remain callable by the runtime role.
REVOKE EXECUTE ON FUNCTION public.control_terminalize_account_operation_noop_v1(uuid,text)
FROM relay_control_runtime;

-- +goose Down
DO $$
BEGIN
    RAISE EXCEPTION 'migration 45 is forward-only' USING ERRCODE='55000';
END;
$$;
