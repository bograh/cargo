-- +goose Up
-- When an email-targeted invite was used. NULL means unused.
--
-- A link invite (email IS NULL) is shareable by design and stays multi-use
-- until it expires or is revoked. An invite addressed to a person is not: it
-- was previously reusable by anyone holding the link, forever, and was never
-- checked against the accepting account's address.
ALTER TABLE invites ADD COLUMN accepted_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE invites DROP COLUMN accepted_at;
