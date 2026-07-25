-- +goose Up
-- Opt-in per-app notification on a successful deploy (failures always notify).
-- The outbound webhook URL is stored encrypted in instance_settings under the
-- key 'notify_webhook', so no schema change is needed for it.
ALTER TABLE applications
    ADD COLUMN notify_on_success BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE applications DROP COLUMN notify_on_success;
