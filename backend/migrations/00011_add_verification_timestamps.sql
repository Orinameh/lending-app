-- +goose Up
-- +goose StatementBegin
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS email_verified_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS phone_verified_at TIMESTAMPTZ;

-- Backfill: previously verified accounts inherit their last update time.
-- (Fresh installs have no rows; this is for existing deployments.)
UPDATE users SET email_verified_at = updated_at
    WHERE email_verified = TRUE AND email_verified_at IS NULL;
UPDATE users SET phone_verified_at = updated_at
    WHERE phone_verified = TRUE AND phone_verified_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users
    DROP COLUMN IF EXISTS phone_verified_at,
    DROP COLUMN IF EXISTS email_verified_at;
-- +goose StatementEnd
