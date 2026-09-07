-- +goose Up
-- Indexes for lookups that were reaching these tables without one.

-- app_metrics is the highest-volume table in the schema: one row per app per
-- 15s tick, ~5,760 per app per day. Its only index was (app_id, created_at),
-- which serves the read path but cannot be used by the daily purge, since that
-- filters on created_at alone — so housekeeping sequentially scanned the whole
-- table every night.
CREATE INDEX app_metrics_time_idx ON app_metrics (created_at);

-- The GitHub webhook resolves apps by branch on every push.
CREATE INDEX applications_branch_idx ON applications (source_type, git_branch);

-- memberships is keyed (org_id, user_id), so user_id is not an index prefix:
-- listing a user's organizations, and deleting a user (FK cascade), both
-- scanned the table.
CREATE INDEX memberships_user_idx ON memberships (user_id);

-- Same shape: the UNIQUE is (instance_id, app_id), so a lookup by app alone
-- had no index. ListAttachmentsByApp runs on every deploy to rebuild the
-- injected database URLs.
CREATE INDEX database_attachments_app_idx ON database_attachments (app_id);

-- +goose Down
DROP INDEX database_attachments_app_idx;
DROP INDEX memberships_user_idx;
DROP INDEX applications_branch_idx;
DROP INDEX app_metrics_time_idx;
