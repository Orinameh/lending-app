-- +goose Up
-- +goose StatementBegin
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email TEXT NOT NULL,
    email_hmac TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    first_name TEXT,
    last_name TEXT,
    phone TEXT,
    phone_hmac TEXT,
    bvn TEXT,
    bvn_hmac TEXT UNIQUE,
    nin TEXT,
    nin_hmac TEXT UNIQUE,
    address TEXT,
    city TEXT,
    state TEXT,
    date_of_birth DATE NOT NULL CHECK (date_of_birth <= CURRENT_DATE - INTERVAL '18 years'),
    country CHAR(2) NOT NULL DEFAULT 'NG' CHECK (country ~ '^[A-Z]{2}$'),
    currency CHAR(3) NOT NULL DEFAULT 'NGN' CHECK (currency ~ '^[A-Z]{3}$'),
    employment_type TEXT NOT NULL DEFAULT 'other'
        CHECK (employment_type IN ('employed','self_employed','unemployed','student','retired','other')),
    annual_income NUMERIC(15,2) CHECK (annual_income IS NULL OR annual_income >= 0),
    role TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('user','admin','agent')),
    email_verified BOOLEAN NOT NULL DEFAULT FALSE,
    phone_verified BOOLEAN NOT NULL DEFAULT FALSE,
    kyc_status TEXT NOT NULL DEFAULT 'pending'
        CHECK (kyc_status IN ('pending','submitted','verified','rejected','expired')),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);

CREATE INDEX idx_users_role ON users(role) WHERE deleted_at IS NULL;
CREATE INDEX idx_users_kyc ON users(kyc_status) WHERE deleted_at IS NULL;
CREATE INDEX idx_users_created ON users(created_at);
CREATE INDEX idx_users_active ON users(deleted_at) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX idx_users_phone_hmac ON users(phone_hmac) WHERE phone_hmac IS NOT NULL;

DROP TRIGGER IF EXISTS trg_users_updated_at ON users;
CREATE TRIGGER trg_users_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_users_updated_at ON users;
DROP TABLE IF EXISTS users;
-- +goose StatementEnd
