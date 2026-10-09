-- +goose Up
-- +goose StatementBegin
CREATE TABLE kyc_documents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    document_type TEXT NOT NULL
        CHECK (document_type IN ('bvn','nin','passport','drivers_license','voters_card','utility_bill','bank_statement','other')),
    document_number TEXT,
    issuing_authority TEXT CHECK (issuing_authority IS NULL OR char_length(issuing_authority) <= 120),
    expiry_date DATE,
    file_path TEXT CHECK (file_path IS NULL OR char_length(file_path) <= 500),
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','submitted','verified','rejected','expired')),
    rejection_reason TEXT CHECK (rejection_reason IS NULL OR char_length(rejection_reason) <= 500),
    verified_by UUID REFERENCES users(id) ON DELETE SET NULL,
    verified_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (verified_at IS NULL OR verified_by IS NOT NULL),
    CHECK (expiry_date IS NULL OR expiry_date > CURRENT_DATE - INTERVAL '100 years')
);

CREATE UNIQUE INDEX idx_kyc_user_type ON kyc_documents(user_id, document_type);
CREATE UNIQUE INDEX idx_kyc_doc_number ON kyc_documents(document_number) WHERE document_number IS NOT NULL AND document_number <> '';
CREATE INDEX idx_kyc_status ON kyc_documents(status);

DROP TRIGGER IF EXISTS trg_kyc_updated_at ON kyc_documents;
CREATE TRIGGER trg_kyc_updated_at
    BEFORE UPDATE ON kyc_documents
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_kyc_updated_at ON kyc_documents;
DROP TABLE IF EXISTS kyc_documents;
-- +goose StatementEnd
