-- +goose Up
-- +goose StatementBegin
-- Uniqueness is per (document, type) among ACTIVE reviews only:
-- rejected/expired rows must allow resubmission, and a BVN digit-string
-- must never block the same digits submitted as a different type.
DROP INDEX IF EXISTS idx_kyc_doc_hmac;
CREATE UNIQUE INDEX idx_kyc_doc_hmac_active ON kyc_documents(document_hmac, document_type)
    WHERE document_hmac IS NOT NULL AND document_hmac <> ''
      AND status NOT IN ('rejected', 'expired');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_kyc_doc_hmac_active;
CREATE UNIQUE INDEX idx_kyc_doc_hmac ON kyc_documents(document_hmac)
    WHERE document_hmac IS NOT NULL AND document_hmac <> '';
-- +goose StatementEnd
