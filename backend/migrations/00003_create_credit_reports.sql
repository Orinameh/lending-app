-- +goose Up
-- +goose StatementBegin
CREATE TABLE credit_reports (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    score INTEGER NOT NULL CHECK (score BETWEEN 300 AND 850),
    rating TEXT CHECK (rating IS NULL OR rating IN ('Poor','Fair','Good','Very Good','Exceptional')),
    total_debt NUMERIC(15,2) CHECK (total_debt IS NULL OR total_debt >= 0),
    available_credit NUMERIC(15,2) CHECK (available_credit IS NULL OR available_credit >= 0),
    credit_utilization NUMERIC(5,2) CHECK (credit_utilization IS NULL OR (credit_utilization >= 0 AND credit_utilization <= 100)),
    payment_history NUMERIC(5,2) CHECK (payment_history IS NULL OR (payment_history >= 0 AND payment_history <= 100)),
    credit_age_months INTEGER CHECK (credit_age_months IS NULL OR credit_age_months >= 0),
    num_accounts INTEGER CHECK (num_accounts IS NULL OR num_accounts >= 0),
    hard_inquiries INTEGER CHECK (hard_inquiries IS NULL OR hard_inquiries >= 0),
    delinquent_accounts INTEGER CHECK (delinquent_accounts IS NULL OR delinquent_accounts >= 0),
    report_date TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_credit_user_date ON credit_reports(user_id, report_date DESC);

DROP TRIGGER IF EXISTS trg_credit_updated_at ON credit_reports;
CREATE TRIGGER trg_credit_updated_at
    BEFORE UPDATE ON credit_reports
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_credit_updated_at ON credit_reports;
DROP TABLE IF EXISTS credit_reports;
-- +goose StatementEnd
