-- +goose Up
CREATE TABLE environments (
    singleton_id smallint PRIMARY KEY DEFAULT 1 CHECK (singleton_id = 1),
    environment_id text NOT NULL UNIQUE CHECK (btrim(environment_id) <> ''),
    name text NOT NULL CHECK (btrim(name) <> ''),
    environment_type text NOT NULL CHECK (environment_type IN ('dev', 'staging', 'production')),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);

COMMENT ON TABLE environments IS
    'Singleton identity for this Control deployment and its independent database.';

-- +goose Down
DROP TABLE environments;
