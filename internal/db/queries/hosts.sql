-- name: CreateHost :one
INSERT INTO hosts (
    name, address, port, private_key_enc, key_version,
    apps_domain_suffix, letsencrypt_email
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListHosts :many
SELECT * FROM hosts ORDER BY created_at;

-- name: GetHost :one
SELECT * FROM hosts WHERE id = $1;

-- name: UpdateHostStatus :exec
UPDATE hosts SET status = $2, engine_version = $3,
                 cpu_count = $4, mem_total_mb = $5
WHERE id = $1;

-- name: PinHostFingerprint :exec
UPDATE hosts SET host_key_fingerprint = $2 WHERE id = $1;

-- name: SetHostKey :exec
UPDATE hosts SET private_key_enc = $2, key_version = $3 WHERE id = $1;

-- name: DeleteHost :exec
DELETE FROM hosts WHERE id = $1;
