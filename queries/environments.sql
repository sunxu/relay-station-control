-- name: GetEnvironment :one
SELECT singleton_id, environment_id, name, environment_type, created_at, updated_at
FROM environments
WHERE singleton_id = 1;
