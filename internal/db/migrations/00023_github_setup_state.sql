-- +goose Up
-- The GitHub post-install redirect used to carry the org's UUID as `state`,
-- which anyone could forge: an attacker who admins any org could bind someone
-- else's installation_id to it (and read their private repos), and a crafted
-- link could bind an attacker's installation to a victim org (CSRF over GET).
-- `state` is now a random single-use nonce bound to the initiating user and
-- org, with a short expiry.
CREATE TABLE github_setup_states (
    nonce      TEXT PRIMARY KEY,
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX github_setup_states_expires_idx ON github_setup_states (expires_at);

-- One installation belongs to one org. Pre-existing duplicates can only have
-- come from the forgeable callback above; keep the earliest binding.
DELETE FROM github_installations g
USING github_installations older
WHERE g.installation_id = older.installation_id
  AND (older.created_at, older.org_id) < (g.created_at, g.org_id);
ALTER TABLE github_installations
    ADD CONSTRAINT github_installations_installation_id_key UNIQUE (installation_id);

-- +goose Down
ALTER TABLE github_installations DROP CONSTRAINT github_installations_installation_id_key;
DROP TABLE github_setup_states;
