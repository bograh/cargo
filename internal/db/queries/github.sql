-- name: UpsertGithubInstallation :one
INSERT INTO github_installations (org_id, installation_id, account_login)
VALUES ($1, $2, $3)
ON CONFLICT (org_id) DO UPDATE
    SET installation_id = EXCLUDED.installation_id, account_login = EXCLUDED.account_login
RETURNING *;

-- name: GetGithubInstallation :one
SELECT * FROM github_installations WHERE org_id = $1;

-- name: GetGithubInstallationByInstallationID :one
SELECT * FROM github_installations WHERE installation_id = $1;

-- name: DeleteGithubInstallation :exec
DELETE FROM github_installations WHERE org_id = $1;

-- name: CreateGithubSetupState :exec
INSERT INTO github_setup_states (nonce, org_id, user_id, expires_at)
VALUES ($1, $2, $3, $4);

-- name: ConsumeGithubSetupState :one
DELETE FROM github_setup_states
WHERE nonce = $1 AND user_id = $2 AND expires_at > now()
RETURNING org_id;

-- name: DeleteExpiredGithubSetupStates :exec
DELETE FROM github_setup_states WHERE expires_at <= now();
