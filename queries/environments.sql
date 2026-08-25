-- name: GetEnvironment :one
SELECT singleton_id, environment_id, name, environment_type, created_at, updated_at
FROM environments
WHERE singleton_id = 1;

-- name: CreateEnvironment :one
INSERT INTO environments (environment_id, name, environment_type)
VALUES ($1, $2, $3)
RETURNING singleton_id, environment_id, name, environment_type, created_at, updated_at;
