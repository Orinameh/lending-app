-- +goose Up
-- +goose StatementBegin
CREATE TABLE audit_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    actor_id UUID REFERENCES users(id) ON DELETE SET NULL,
    action TEXT NOT NULL CHECK (char_length(action) BETWEEN 1 AND 64),
    resource_type TEXT CHECK (resource_type IS NULL OR char_length(resource_type) <= 64),
    resource_id UUID,
    details JSONB,
    ip_address TEXT,
    user_agent TEXT CHECK (user_agent IS NULL OR char_length(user_agent) <= 500),
    request_id TEXT CHECK (request_id IS NULL OR char_length(request_id) <= 64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_audit_user_created ON audit_logs(user_id, created_at DESC);
CREATE INDEX idx_audit_action ON audit_logs(action);
CREATE INDEX idx_audit_resource ON audit_logs(resource_type, resource_id);
CREATE INDEX idx_audit_created ON audit_logs(created_at DESC);

CREATE OR REPLACE FUNCTION prevent_audit_mutation()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'audit_logs is append-only';
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_audit_no_update ON audit_logs;
CREATE TRIGGER trg_audit_no_update
    BEFORE UPDATE OR DELETE ON audit_logs
    FOR EACH ROW EXECUTE FUNCTION prevent_audit_mutation();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_audit_no_update ON audit_logs;
DROP FUNCTION IF EXISTS prevent_audit_mutation();
DROP TABLE IF EXISTS audit_logs;
-- +goose StatementEnd
