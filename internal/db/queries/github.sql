-- name: UpsertGithubInstallation :one
INSERT INTO github_installations (org_id, installation_id, account_login)
VALUES ($1, $2, $3)
ON CONFLICT (org_id) DO UPDATE
    SET installation_id = EXCLUDED.installation_id, account_login = EXCLUDED.account_login
RETURNING *;

-- name: GetGithubInstallation :one
SELECT * FROM github_installations WHERE org_id = $1;

-- name: DeleteGithubInstallation :exec
DELETE FROM github_installations WHERE org_id = $1;
