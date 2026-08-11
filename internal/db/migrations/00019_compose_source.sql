-- +goose Up
-- The "compose" app source: the user's repository supplies its own compose
-- file and Cargo layers networking, Traefik labels, and limits over it.
--
-- compose_path is the file's path within the repo; compose_service names the
-- service that receives HTTP traffic (the one the app's domain routes to and
-- that the health gate probes).
ALTER TABLE applications
    ADD COLUMN compose_path    TEXT NOT NULL DEFAULT '',
    ADD COLUMN compose_service TEXT NOT NULL DEFAULT '';

ALTER TABLE applications DROP CONSTRAINT applications_source_type_check;
ALTER TABLE applications ADD CONSTRAINT applications_source_type_check
    CHECK (source_type IN ('git', 'image', 'compose'));

-- +goose Down
ALTER TABLE applications DROP CONSTRAINT applications_source_type_check;
ALTER TABLE applications ADD CONSTRAINT applications_source_type_check
    CHECK (source_type IN ('git', 'image'));
ALTER TABLE applications
    DROP COLUMN compose_path,
    DROP COLUMN compose_service;
