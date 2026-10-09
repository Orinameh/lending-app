-- +goose Up
-- +goose StatementBegin
CREATE TABLE collections (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    loan_id UUID NOT NULL REFERENCES loans(id) ON DELETE RESTRICT,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    amount_outstanding NUMERIC(15,2) NOT NULL CHECK (amount_outstanding >= 0),
    days_past_due INTEGER NOT NULL DEFAULT 0 CHECK (days_past_due >= 0),
    status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active','resolved','legal','written_off')),
    assigned_to UUID REFERENCES users(id) ON DELETE SET NULL,
    last_contact_date TIMESTAMPTZ,
    next_contact_date TIMESTAMPTZ,
    notes TEXT CHECK (notes IS NULL OR char_length(notes) <= 2000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (next_contact_date IS NULL OR last_contact_date IS NULL OR next_contact_date >= last_contact_date)
);

CREATE INDEX idx_collections_loan ON collections(loan_id);
CREATE INDEX idx_collections_user ON collections(user_id);
CREATE INDEX idx_collections_status_dpd ON collections(status, days_past_due);
CREATE UNIQUE INDEX idx_collections_active_loan ON collections(loan_id) WHERE status = 'active';

DROP TRIGGER IF EXISTS trg_collections_updated_at ON collections;
CREATE TRIGGER trg_collections_updated_at
    BEFORE UPDATE ON collections
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_collections_updated_at ON collections;
DROP TABLE IF EXISTS collections;
-- +goose StatementEnd
