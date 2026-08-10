-- +goose Up
-- Per-app deployment strategy. NULL means "use the instance-wide default"
-- (CARGO_DEPLOY_STRATEGY), resolved at deploy time.
--
-- 'bluegreen' brings the new version up alongside the old one, health-gates it,
-- then reaps the old color — removing the recreate gap that FR-4.7 documented as
-- a known limitation. 'recreate' keeps the original single-container behaviour
-- for apps that cannot tolerate two instances running at once (exclusive locks,
-- singleton workers, migrations on boot).
ALTER TABLE applications
    ADD COLUMN deploy_strategy TEXT;

-- +goose Down
ALTER TABLE applications
    DROP COLUMN deploy_strategy;
