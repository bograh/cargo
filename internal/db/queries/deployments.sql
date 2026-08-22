-- name: CreateDeployment :one
INSERT INTO deployments (app_id, trigger, actor, image_tag, commit_sha)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetDeployment :one
SELECT * FROM deployments WHERE id = $1;

-- name: ListDeploymentsForApp :many
SELECT * FROM deployments WHERE app_id = $1 ORDER BY created_at DESC LIMIT 50;

-- name: SetDeploymentBuildInfo :exec
UPDATE deployments SET commit_sha = $2, image_tag = $3 WHERE id = $1;

-- name: SetDeploymentHost :exec
UPDATE deployments SET host_id = $2 WHERE id = $1;

-- name: MarkDeploymentStatus :exec
UPDATE deployments SET status = $2,
    started_at = COALESCE(started_at, now())
WHERE id = $1;

-- name: FinishDeployment :exec
UPDATE deployments SET status = $2, error = $3, finished_at = now() WHERE id = $1;

-- name: SupersedePriorLiveDeployments :exec
UPDATE deployments SET status = 'superseded'
WHERE app_id = $1 AND id <> $2 AND status = 'live';

-- name: FailStaleDeployments :execrows
UPDATE deployments SET status = 'failed', error = $1, finished_at = now()
WHERE status IN ('queued', 'building', 'deploying') AND created_at < $2;

-- name: ListPrunableDeployments :many
SELECT * FROM deployments WHERE app_id = $1 ORDER BY created_at DESC OFFSET $2;

-- name: DeleteDeployment :exec
DELETE FROM deployments WHERE id = $1;

-- name: ListRetainedImageTags :many
-- Image tags that must survive a prune: those of the newest `keep` deployments
-- plus any that is still serving. The second clause matters for blue/green and
-- for rollbacks, where a container runs an image whose own deployment row may
-- already have aged out of the keep window.
SELECT DISTINCT d.image_tag FROM deployments d
WHERE d.app_id = $1 AND d.image_tag <> '' AND (
    d.status = 'live'
    OR d.id IN (
        SELECT k.id FROM deployments k WHERE k.app_id = $1
        ORDER BY k.created_at DESC LIMIT sqlc.arg('keep')
    )
);
