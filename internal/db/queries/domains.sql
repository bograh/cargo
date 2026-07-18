-- name: CreateDomain :one
INSERT INTO domains (app_id, hostname) VALUES ($1, $2) RETURNING *;

-- name: ListDomainsForApp :many
SELECT * FROM domains WHERE app_id = $1 ORDER BY created_at;

-- name: DeleteDomain :exec
DELETE FROM domains WHERE id = $1 AND app_id = $2;

-- name: ListAllDomains :many
SELECT * FROM domains ORDER BY created_at;

-- name: UpdateDomainStatus :exec
UPDATE domains SET status = $2, last_checked_at = now() WHERE id = $1;
