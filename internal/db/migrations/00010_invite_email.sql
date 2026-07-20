-- +goose Up
-- Optional recipient email for an invite. NULL = a shareable link invite.
ALTER TABLE invites ADD COLUMN email TEXT;

-- +goose Down
ALTER TABLE invites DROP COLUMN email;
