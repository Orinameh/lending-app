-- +goose Up
-- +goose StatementBegin
-- One row per payment ATTEMPT. Idempotency lives here (unique per payment),
-- not on installment rows: a single payment legitimately spans multiple
-- installments on spillover, so a per-row UNIQUE on transaction_id is both
-- wrong and unusable. The insert itself is the atomic dedup — no TOCTOU.
CREATE TABLE payments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    loan_id UUID NOT NULL REFERENCES loans(id) ON DELETE RESTRICT,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    amount NUMERIC(15,2) NOT NULL CHECK (amount > 0),
    applied NUMERIC(15,2) NOT NULL DEFAULT 0 CHECK (applied >= 0),
    overpayment NUMERIC(15,2) NOT NULL DEFAULT 0 CHECK (overpayment >= 0),
    payment_method TEXT NOT NULL CHECK (char_length(payment_method) BETWEEN 1 AND 50),
    transaction_id TEXT NOT NULL UNIQUE CHECK (char_length(transaction_id) BETWEEN 1 AND 100),
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','applied','failed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_payments_loan ON payments(loan_id);
CREATE INDEX idx_payments_user ON payments(user_id);

-- Retire the per-row unique index; the column stays as last-touch info.
DROP INDEX IF EXISTS idx_repayments_txn;

DROP TRIGGER IF EXISTS trg_payments_updated_at ON payments;
CREATE TRIGGER trg_payments_updated_at
    BEFORE UPDATE ON payments
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_payments_updated_at ON payments;
DROP TABLE IF EXISTS payments;
CREATE UNIQUE INDEX idx_repayments_txn ON repayments(transaction_id)
    WHERE transaction_id IS NOT NULL AND transaction_id <> '';
-- +goose StatementEnd
