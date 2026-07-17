-- name: UpsertEnvVar :exec
INSERT INTO env_vars (app_id, key, value_enc)
VALUES ($1, $2, $3)
ON CONFLICT (app_id, key) DO UPDATE SET value_enc = EXCLUDED.value_enc, updated_at = now();

-- name: ListEnvVars :many
SELECT * FROM env_vars WHERE app_id = $1 ORDER BY key;

-- name: DeleteEnvVar :exec
DELETE FROM env_vars WHERE app_id = $1 AND key = $2;
