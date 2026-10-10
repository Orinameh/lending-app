package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"lending-app/backend/internal/domain/entities"
	"time"

	"github.com/google/uuid"
)

type KYCRepository struct {
	db *sql.DB
}

func NewKYCRepository(db *sql.DB) *KYCRepository {
	return &KYCRepository{db: db}
}

const kycCols = `
    id, user_id, document_type, document_number, document_hmac, issuing_authority,
    expiry_date, file_path, status, rejection_reason, verified_by,
    verified_at, provider_ref, provider_response, created_at, updated_at
`

func (r *KYCRepository) scan(row interface{ Scan(...interface{}) error }) (*entities.KYCDocument, error) {
	var d entities.KYCDocument
	var docNum, docHMAC, issuer, filePath, rejection, providerRef sql.NullString
	var expiry sql.NullTime
	var verifiedBy uuid.NullUUID
	var verifiedAt sql.NullTime
	var providerResp []byte
	err := row.Scan(
		&d.ID, &d.UserID, &d.DocumentType, &docNum, &docHMAC, &issuer,
		&expiry, &filePath, &d.Status, &rejection, &verifiedBy,
		&verifiedAt, &providerRef, &providerResp, &d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if docNum.Valid {
		d.DocumentNumber = docNum.String
	}
	if docHMAC.Valid {
		d.DocumentHMAC = docHMAC.String
	}
	if issuer.Valid {
		d.IssuingAuthority = issuer.String
	}
	if expiry.Valid {
		d.ExpiryDate = &expiry.Time
	}
	if filePath.Valid {
		d.FilePath = filePath.String
	}
	if rejection.Valid {
		d.RejectionReason = rejection.String
	}
	if verifiedBy.Valid {
		d.VerifiedBy = &verifiedBy.UUID
	}
	if verifiedAt.Valid {
		d.VerifiedAt = &verifiedAt.Time
	}
	if providerRef.Valid {
		d.ProviderRef = providerRef.String
	}
	if len(providerResp) > 0 {
		d.ProviderResponse = json.RawMessage(providerResp)
	}
	return &d, nil
}

func (r *KYCRepository) Create(ctx context.Context, d *entities.KYCDocument) error {
	var providerResp []byte
	if d.ProviderResponse != nil {
		providerResp = []byte(d.ProviderResponse)
	}
	_, err := runner(ctx, r.db).ExecContext(ctx,
		`INSERT INTO kyc_documents (
            id, user_id, document_type, document_number, document_hmac, issuing_authority,
            expiry_date, file_path, status, rejection_reason, verified_by,
            verified_at, provider_ref, provider_response, created_at, updated_at
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		d.ID, d.UserID, d.DocumentType, nullStr(d.DocumentNumber), nullStr(d.DocumentHMAC), nullStr(d.IssuingAuthority),
		d.ExpiryDate, nullStr(d.FilePath), d.Status, nullStr(d.RejectionReason), d.VerifiedBy,
		d.VerifiedAt, nullStr(d.ProviderRef), providerResp, d.CreatedAt, d.UpdatedAt,
	)
	return err
}

func (r *KYCRepository) GetByUserAndType(ctx context.Context, userID uuid.UUID, docType string) (*entities.KYCDocument, error) {
	d, err := r.scan(runner(ctx, r.db).QueryRowContext(ctx,
		`SELECT `+kycCols+` FROM kyc_documents WHERE user_id=$1 AND document_type=$2
         ORDER BY created_at DESC LIMIT 1`, userID, docType))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

func (r *KYCRepository) GetActiveByDocHMAC(ctx context.Context, hmac, docType string) (*entities.KYCDocument, error) {
	if hmac == "" || docType == "" {
		return nil, ErrNotFound
	}
	d, err := r.scan(runner(ctx, r.db).QueryRowContext(ctx,
		`SELECT `+kycCols+` FROM kyc_documents
         WHERE document_hmac=$1 AND document_type=$2
           AND status NOT IN ('rejected','expired')
         ORDER BY created_at DESC LIMIT 1`, hmac, docType))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

func (r *KYCRepository) ListByUser(ctx context.Context, userID uuid.UUID) ([]*entities.KYCDocument, error) {
	rows, err := runner(ctx, r.db).QueryContext(ctx,
		`SELECT `+kycCols+` FROM kyc_documents WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*entities.KYCDocument
	for rows.Next() {
		d, err := r.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

func (r *KYCRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status, reason string, verifiedBy *uuid.UUID) error {
	if !entities.ValidKYCDocumentStatuses[status] {
		return errors.New("invalid kyc document status")
	}
	var verifiedAt *time.Time
	if status == "verified" {
		now := time.Now().UTC()
		verifiedAt = &now
	}
	_, err := runner(ctx, r.db).ExecContext(ctx,
		`UPDATE kyc_documents SET status=$1, rejection_reason=$2, verified_by=$3,
            verified_at=$4, updated_at=$5 WHERE id=$6`,
		status, nullStr(reason), verifiedBy, verifiedAt, time.Now().UTC(), id)
	return err
}
