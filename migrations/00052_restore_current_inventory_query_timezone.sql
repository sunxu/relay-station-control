-- +goose Up

ALTER FUNCTION public.control_query_current_account_inventory_v1(
    uuid,
    text,
    text,
    text,
    text,
    text,
    integer
)
SET TimeZone = 'UTC';

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'migration 52 is forward-only' USING ERRCODE = '55000';
END $$;
-- +goose StatementEnd
