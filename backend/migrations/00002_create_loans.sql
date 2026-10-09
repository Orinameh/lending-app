-- +goose Up
-- +goose StatementBegin
CREATE TABLE loans (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    amount NUMERIC(15,2) NOT NULL CHECK (amount > 0),
    interest_rate NUMERIC(7,4) NOT NULL CHECK (interest_rate >= 0 AND interest_rate <= 100),
    effective_apr NUMERIC(7,4) CHECK (effective_apr IS NULL OR (effective_apr >= 0 AND effective_apr <= 100)),
    term_months INTEGER NOT NULL CHECK (term_months BETWEEN 1 AND 60),
    purpose TEXT CHECK (purpose IS NULL OR char_length(purpose) <= 500),
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','under_review','approved','disbursed','repaid','defaulted','declined')),
    currency CHAR(3) NOT NULL DEFAULT 'NGN' CHECK (currency ~ '^[A-Z]{3}$'),
    idempotency_key TEXT UNIQUE,
    application_date TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    approval_date TIMESTAMPTZ,
    disbursement_date TIMESTAMPTZ,
    maturity_date TIMESTAMPTZ NOT NULL,
    monthly_payment NUMERIC(15,2) NOT NULL CHECK (monthly_payment >= 0),
    total_payment NUMERIC(15,2) NOT NULL CHECK (total_payment >= 0),
    total_interest NUMERIC(15,2) NOT NULL CHECK (total_interest >= 0),
    risk_score INTEGER CHECK (risk_score IS NULL OR (risk_score BETWEEN 300 AND 850)),
    credit_decision TEXT CHECK (credit_decision IS NULL OR credit_decision IN ('approved','pending_review','declined')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    CHECK (maturity_date > application_date),
    CHECK (disbursement_date IS NULL OR disbursement_date >= application_date),
    CHECK (approval_date IS NULL OR approval_date >= application_date)
);

CREATE INDEX idx_loans_user_status ON loans(user_id, status) WHERE deleted_at IS NULL;
CREATE INDEX idx_loans_status_maturity ON loans(status, maturity_date) WHERE deleted_at IS NULL;
CREATE INDEX idx_loans_maturity ON loans(maturity_date) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX idx_loans_idem ON loans(idempotency_key) WHERE idempotency_key IS NOT NULL;

DROP TRIGGER IF EXISTS trg_loans_updated_at ON loans;
CREATE TRIGGER trg_loans_updated_at
    BEFORE UPDATE ON loans
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_loans_updated_at ON loans;
DROP TABLE IF EXISTS loans;
-- +goose StatementEnd
