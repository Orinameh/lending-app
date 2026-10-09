-- +goose Up
-- +goose StatementBegin
CREATE TABLE repayments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    loan_id UUID NOT NULL REFERENCES loans(id) ON DELETE RESTRICT,
    amount NUMERIC(15,2) NOT NULL CHECK (amount > 0),
    principal_amount NUMERIC(15,2) NOT NULL CHECK (principal_amount >= 0),
    interest_amount NUMERIC(15,2) NOT NULL CHECK (interest_amount >= 0),
    amount_paid NUMERIC(15,2) NOT NULL DEFAULT 0 CHECK (amount_paid >= 0),
    principal_paid NUMERIC(15,2) NOT NULL DEFAULT 0 CHECK (principal_paid >= 0),
    interest_paid NUMERIC(15,2) NOT NULL DEFAULT 0 CHECK (interest_paid >= 0),
    fee_paid NUMERIC(15,2) NOT NULL DEFAULT 0 CHECK (fee_paid >= 0),
    payment_date TIMESTAMPTZ,
    due_date TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','partial','paid','overdue')),
    payment_method TEXT CHECK (payment_method IS NULL OR char_length(payment_method) <= 50),
    transaction_id TEXT,
    late_fee NUMERIC(15,2) NOT NULL DEFAULT 0 CHECK (late_fee >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (amount_paid <= amount + late_fee),
    CHECK (principal_paid <= principal_amount),
    CHECK (interest_paid <= interest_amount)
);

CREATE INDEX idx_repayments_loan ON repayments(loan_id);
CREATE INDEX idx_repayments_loan_status ON repayments(loan_id, status);
CREATE INDEX idx_repayments_due ON repayments(due_date) WHERE status IN ('pending','partial','overdue');
CREATE UNIQUE INDEX idx_repayments_txn ON repayments(transaction_id) WHERE transaction_id IS NOT NULL AND transaction_id <> '';

DROP TRIGGER IF EXISTS trg_repayments_updated_at ON repayments;
CREATE TRIGGER trg_repayments_updated_at
    BEFORE UPDATE ON repayments
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_repayments_updated_at ON repayments;
DROP TABLE IF EXISTS repayments;
-- +goose StatementEnd
