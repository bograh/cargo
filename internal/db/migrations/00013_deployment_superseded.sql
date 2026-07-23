-- +goose Up
-- 'live' previously meant both "currently serving" and "rollback-eligible", so
-- every past success stayed live and multiple rows piled up as live. Add a
-- 'superseded' status: when a new deploy goes live, the app's prior live row(s)
-- are demoted to 'superseded'. The single newest 'live' row is what's serving;
-- 'superseded' rows keep their image and remain valid rollback targets.
ALTER TABLE deployments DROP CONSTRAINT deployments_status_check;
ALTER TABLE deployments ADD CONSTRAINT deployments_status_check
    CHECK (status IN ('queued', 'building', 'deploying', 'live', 'superseded', 'failed', 'cancelled'));

-- +goose Down
UPDATE deployments SET status = 'live' WHERE status = 'superseded';
ALTER TABLE deployments DROP CONSTRAINT deployments_status_check;
ALTER TABLE deployments ADD CONSTRAINT deployments_status_check
    CHECK (status IN ('queued', 'building', 'deploying', 'live', 'failed', 'cancelled'));
