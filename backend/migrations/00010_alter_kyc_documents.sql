-- +goose Up
-- +goose StatementBegin
-- document_number is stored encrypted (randomized nonce), so a UNIQUE index
-- on the ciphertext can never dedupe. Replace with a deterministic HMAC
-- blind index (same pattern as users.email_hmac), and allow resubmission
-- after rejection by dropping the one-row-per-type constraint in favor of
-- a service-level "one active review per type" rule.
ALTER TABLE kyc_documents ADD COLUMN IF NOT EXISTS document_hmac TEXT;

DROP INDEX IF EXISTS idx_kyc_user_type;
DROP INDEX IF EXISTS idx_kyc_doc_number;

CREATE UNIQUE INDEX idx_kyc_doc_hmac ON kyc_documents(document_hmac)
    WHERE document_hmac IS NOT NULL AND document_hmac <> '';
CREATE INDEX idx_kyc_user_type_status ON kyc_documents(user_id, document_type, status);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_kyc_user_type_status;
DROP INDEX IF EXISTS idx_kyc_doc_hmac;
ALTER TABLE kyc_documents DROP COLUMN IF EXISTS document_hmac;
CREATE UNIQUE INDEX idx_kyc_user_type ON kyc_documents(user_id, document_type);
CREATE UNIQUE INDEX idx_kyc_doc_number ON kyc_documents(document_number)
    WHERE document_number IS NOT NULL AND document_number <> '';
-- +goose StatementEnd
