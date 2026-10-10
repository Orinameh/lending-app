-- +goose Up
-- +goose StatementBegin
CREATE TABLE email_verifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (expires_at > created_at)
);

CREATE INDEX idx_emailver_user ON email_verifications(user_id);

CREATE TABLE phone_otps (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts INTEGER NOT NULL DEFAULT 5 CHECK (max_attempts BETWEEN 1 AND 10),
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (expires_at > created_at)
);

CREATE INDEX idx_phoneotp_user ON phone_otps(user_id, created_at DESC);

ALTER TABLE kyc_documents
    ADD COLUMN IF NOT EXISTS provider_ref TEXT,
    ADD COLUMN IF NOT EXISTS provider_response JSONB;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE kyc_documents
    DROP COLUMN IF EXISTS provider_response,
    DROP COLUMN IF EXISTS provider_ref;
DROP TABLE IF EXISTS phone_otps;
DROP TABLE IF EXISTS email_verifications;
-- +goose StatementEnd
