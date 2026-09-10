-- +goose Up

-- Slice E: register the production DingTalk delivery job kind.  The catalog
-- row is immutable under the durable-job foundation trigger; changing this
-- contract requires a new forward migration.
INSERT INTO public.async_job_kinds (
    job_kind,
    payload_schema_version,
    default_timeout_seconds,
    lease_seconds,
    heartbeat_interval_seconds,
    default_max_attempts,
    default_max_verification_attempts,
    replay_safe,
    allow_unknown_effect_replay,
    allow_direct_success,
    rollback_allowed
) VALUES (
    'dingtalk_alert_delivery',
    1,
    10,
    30,
    5,
    5,
    1,
    true,
    true,
    true,
    false
);

-- +goose Down
-- Production durable-job catalog rows and evidence are forward-only.  A
-- rollback stops new behavior without deleting the registered contract.
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'DingTalk alert delivery migration is forward-only';
END $$;
-- +goose StatementEnd
