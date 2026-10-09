package repositories

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"lending-app/backend/internal/domain/entities"
	"lending-app/backend/pkg/crypto"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var ErrNotFound = errors.New("not found")

type UserRepository struct {
	db        *sql.DB
	encryptor *crypto.EncryptionService
}

func NewUserRepository(db *sql.DB, encryptor *crypto.EncryptionService) *UserRepository {
	return &UserRepository{db: db, encryptor: encryptor}
}

func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func (r *UserRepository) encField(plain string) (sql.NullString, error) {
	if plain == "" {
		return sql.NullString{}, nil
	}
	enc, err := r.encryptor.Encrypt(plain)
	if err != nil {
		return sql.NullString{}, fmt.Errorf("encrypt field: %w", err)
	}
	return sql.NullString{String: enc, Valid: true}, nil
}

func (r *UserRepository) decField(ns sql.NullString) (string, error) {
	if !ns.Valid || ns.String == "" {
		return "", nil
	}
	s, err := r.encryptor.Decrypt(ns.String)
	if err != nil {
		return "", fmt.Errorf("decrypt field: %w", err)
	}
	return s, nil
}

func (r *UserRepository) Create(ctx context.Context, u *entities.User) error {
	emailNorm := NormalizeEmail(u.Email.Plain)
	email, err := r.encField(emailNorm)
	if err != nil {
		return err
	}
	emailHMAC := r.encryptor.HMAC(emailNorm)
	fn, err := r.encField(strings.TrimSpace(u.FirstName.Plain))
	if err != nil {
		return err
	}
	ln, err := r.encField(strings.TrimSpace(u.LastName.Plain))
	if err != nil {
		return err
	}
	ph, err := r.encField(strings.TrimSpace(u.Phone.Plain))
	if err != nil {
		return err
	}
	bvn, err := r.encField(strings.TrimSpace(u.BVN.Plain))
	if err != nil {
		return err
	}
	var bvnHMAC sql.NullString
	if strings.TrimSpace(u.BVN.Plain) != "" {
		bvnHMAC = sql.NullString{String: r.encryptor.HMAC(strings.TrimSpace(u.BVN.Plain)), Valid: true}
	}
	nin, err := r.encField(strings.TrimSpace(u.NIN.Plain))
	if err != nil {
		return err
	}
	var ninHMAC sql.NullString
	if strings.TrimSpace(u.NIN.Plain) != "" {
		ninHMAC = sql.NullString{String: r.encryptor.HMAC(strings.TrimSpace(u.NIN.Plain)), Valid: true}
	}
	addr, err := r.encField(u.Address.Plain)
	if err != nil {
		return err
	}
	city, err := r.encField(u.City.Plain)
	if err != nil {
		return err
	}
	state, err := r.encField(u.State.Plain)
	if err != nil {
		return err
	}
	var phoneHMAC sql.NullString
	if strings.TrimSpace(u.Phone.Plain) != "" {
		phoneHMAC = sql.NullString{String: r.encryptor.HMAC(strings.TrimSpace(u.Phone.Plain)), Valid: true}
	}

	const q = `
        INSERT INTO users (
            id, email, email_hmac, password_hash, first_name, last_name, phone, phone_hmac,
            bvn, bvn_hmac, nin, nin_hmac,
            address, city, state, date_of_birth, country, currency, employment_type,
            annual_income, role, email_verified, phone_verified, kyc_status,
            is_active, created_at, updated_at
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27)
    `
	_, err = runner(ctx, r.db).ExecContext(ctx, q,
		u.ID, email, emailHMAC, u.PasswordHash, fn, ln, ph, phoneHMAC,
		bvn, bvnHMAC, nin, ninHMAC,
		addr, city, state, u.DateOfBirth, u.Country, u.Currency, u.EmploymentType,
		u.AnnualIncome.String(), u.Role, u.EmailVerified, u.PhoneVerified,
		u.KYCStatus, u.IsActive, u.CreatedAt, u.UpdatedAt,
	)
	return err
}

const userCols = `
    id, email, password_hash, first_name, last_name, phone, bvn, nin,
    address, city, state, date_of_birth, country, currency, employment_type,
    annual_income, role, email_verified, phone_verified, kyc_status,
    is_active, created_at, updated_at, deleted_at
`

func (r *UserRepository) scan(row interface{ Scan(...interface{}) error }) (*entities.User, error) {
	var u entities.User
	var email, fn, ln, ph, bvn, nin, addr, city, state sql.NullString
	var annualIncomeStr sql.NullString
	var deletedAt sql.NullTime
	err := row.Scan(
		&u.ID, &email, &u.PasswordHash, &fn, &ln, &ph, &bvn, &nin,
		&addr, &city, &state, &u.DateOfBirth, &u.Country, &u.Currency, &u.EmploymentType,
		&annualIncomeStr, &u.Role, &u.EmailVerified, &u.PhoneVerified,
		&u.KYCStatus, &u.IsActive, &u.CreatedAt, &u.UpdatedAt, &deletedAt,
	)
	if err != nil {
		return nil, err
	}
	set := func(ns sql.NullString, dst *entities.EncryptedString) error {
		if !ns.Valid || ns.String == "" {
			*dst = entities.EncryptedString{}
			return nil
		}
		plain, err := r.decField(ns)
		if err != nil {
			return err
		}
		*dst = entities.EncryptedString{Data: ns.String, Plain: plain}
		return nil
	}
	if err := set(email, &u.Email); err != nil {
		return nil, err
	}
	if err := set(fn, &u.FirstName); err != nil {
		return nil, err
	}
	if err := set(ln, &u.LastName); err != nil {
		return nil, err
	}
	if err := set(ph, &u.Phone); err != nil {
		return nil, err
	}
	if err := set(bvn, &u.BVN); err != nil {
		return nil, err
	}
	if err := set(nin, &u.NIN); err != nil {
		return nil, err
	}
	if err := set(addr, &u.Address); err != nil {
		return nil, err
	}
	if err := set(city, &u.City); err != nil {
		return nil, err
	}
	if err := set(state, &u.State); err != nil {
		return nil, err
	}
	if annualIncomeStr.Valid && annualIncomeStr.String != "" {
		d, err := decimal.NewFromString(annualIncomeStr.String)
		if err != nil {
			return nil, fmt.Errorf("parse annual_income: %w", err)
		}
		u.AnnualIncome = d
	}
	if deletedAt.Valid {
		u.DeletedAt = &deletedAt.Time
	}
	return &u, nil
}

func (r *UserRepository) GetByID(ctx context.Context, id uuid.UUID) (*entities.User, error) {
	q := `SELECT ` + userCols + ` FROM users WHERE id = $1 AND deleted_at IS NULL`
	u, err := r.scan(runner(ctx, r.db).QueryRowContext(ctx, q, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

func (r *UserRepository) GetByEmail(ctx context.Context, email string) (*entities.User, error) {
	h := r.encryptor.HMAC(NormalizeEmail(email))
	q := `SELECT ` + userCols + ` FROM users WHERE email_hmac = $1 AND deleted_at IS NULL`
	u, err := r.scan(runner(ctx, r.db).QueryRowContext(ctx, q, h))
	if err == nil {
		return u, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	// Rotation window: retry under the previous HMAC key.
	if prev, ok := r.encryptor.HMACPrevious(NormalizeEmail(email)); ok {
		u, err := r.scan(runner(ctx, r.db).QueryRowContext(ctx, q, prev))
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return u, err
	}
	return nil, ErrNotFound
}

// UpdatePII rewrites every encrypted PII column + HMAC for key rotation.
// The caller decrypts with the key ring (old keys still work) and the
// plaintext fields on u are re-encrypted here under the PRIMARY key.
func (r *UserRepository) UpdatePII(ctx context.Context, u *entities.User) error {
	emailNorm := NormalizeEmail(u.Email.Plain)
	email, err := r.encField(emailNorm)
	if err != nil {
		return err
	}
	fn, err := r.encField(strings.TrimSpace(u.FirstName.Plain))
	if err != nil {
		return err
	}
	ln, err := r.encField(strings.TrimSpace(u.LastName.Plain))
	if err != nil {
		return err
	}
	ph, err := r.encField(strings.TrimSpace(u.Phone.Plain))
	if err != nil {
		return err
	}
	bvn, err := r.encField(strings.TrimSpace(u.BVN.Plain))
	if err != nil {
		return err
	}
	nin, err := r.encField(strings.TrimSpace(u.NIN.Plain))
	if err != nil {
		return err
	}
	addr, err := r.encField(u.Address.Plain)
	if err != nil {
		return err
	}
	city, err := r.encField(u.City.Plain)
	if err != nil {
		return err
	}
	state, err := r.encField(u.State.Plain)
	if err != nil {
		return err
	}
	hmacOf := func(s string) sql.NullString {
		if strings.TrimSpace(s) == "" {
			return sql.NullString{}
		}
		return sql.NullString{String: r.encryptor.HMAC(strings.TrimSpace(s)), Valid: true}
	}
	var bvnHMAC, ninHMAC sql.NullString
	if strings.TrimSpace(u.BVN.Plain) != "" {
		bvnHMAC = sql.NullString{String: r.encryptor.HMAC(strings.TrimSpace(u.BVN.Plain)), Valid: true}
	}
	if strings.TrimSpace(u.NIN.Plain) != "" {
		ninHMAC = sql.NullString{String: r.encryptor.HMAC(strings.TrimSpace(u.NIN.Plain)), Valid: true}
	}
	_, err = runner(ctx, r.db).ExecContext(ctx, `
        UPDATE users SET email=$1, email_hmac=$2, first_name=$3, last_name=$4,
            phone=$5, phone_hmac=$6, bvn=$7, bvn_hmac=$8, nin=$9, nin_hmac=$10,
            address=$11, city=$12, state=$13, updated_at=$14
        WHERE id=$15 AND deleted_at IS NULL`,
		email, r.encryptor.HMAC(emailNorm), fn, ln, ph, hmacOf(u.Phone.Plain),
		bvn, bvnHMAC, nin, ninHMAC, addr, city, state,
		time.Now().UTC(), u.ID)
	return err
}

func (r *UserRepository) Update(ctx context.Context, u *entities.User) error {
	ph, err := r.encField(strings.TrimSpace(u.Phone.Plain))
	if err != nil {
		return err
	}
	addr, err := r.encField(u.Address.Plain)
	if err != nil {
		return err
	}
	city, err := r.encField(u.City.Plain)
	if err != nil {
		return err
	}
	state, err := r.encField(u.State.Plain)
	if err != nil {
		return err
	}
	var phoneHMAC sql.NullString
	if strings.TrimSpace(u.Phone.Plain) != "" {
		phoneHMAC = sql.NullString{String: r.encryptor.HMAC(strings.TrimSpace(u.Phone.Plain)), Valid: true}
	}
	_, err = runner(ctx, r.db).ExecContext(ctx, `
        UPDATE users SET phone=$1, phone_hmac=$2, address=$3, city=$4, state=$5,
            employment_type=$6, annual_income=$7, updated_at=$8
        WHERE id=$9 AND deleted_at IS NULL`,
		ph, phoneHMAC, addr, city, state, u.EmploymentType, u.AnnualIncome.String(),
		time.Now().UTC(), u.ID)
	return err
}

func (r *UserRepository) UpdatePassword(ctx context.Context, id uuid.UUID, hash string) error {
	_, err := runner(ctx, r.db).ExecContext(ctx,
		`UPDATE users SET password_hash=$1, updated_at=$2 WHERE id=$3 AND deleted_at IS NULL`,
		hash, time.Now().UTC(), id)
	return err
}

func (r *UserRepository) UpdateKYCStatus(ctx context.Context, id uuid.UUID, status string) error {
	if !entities.ValidKYCStatuses[status] {
		return fmt.Errorf("invalid kyc status: %s", status)
	}
	_, err := runner(ctx, r.db).ExecContext(ctx,
		`UPDATE users SET kyc_status=$1, updated_at=$2 WHERE id=$3 AND deleted_at IS NULL`,
		status, time.Now().UTC(), id)
	return err
}

func (r *UserRepository) UpdateStatus(ctx context.Context, id uuid.UUID, active bool) error {
	_, err := runner(ctx, r.db).ExecContext(ctx,
		`UPDATE users SET is_active=$1, updated_at=$2 WHERE id=$3 AND deleted_at IS NULL`,
		active, time.Now().UTC(), id)
	return err
}

func (r *UserRepository) UpdateRole(ctx context.Context, id uuid.UUID, role string) error {
	if !entities.ValidRoles[role] {
		return fmt.Errorf("invalid role: %s", role)
	}
	_, err := runner(ctx, r.db).ExecContext(ctx,
		`UPDATE users SET role=$1, updated_at=$2 WHERE id=$3 AND deleted_at IS NULL`,
		role, time.Now().UTC(), id)
	return err
}

func (r *UserRepository) List(ctx context.Context, limit, offset int) ([]*entities.User, int, error) {
	var total int
	if err := runner(ctx, r.db).QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE deleted_at IS NULL`).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := runner(ctx, r.db).QueryContext(ctx,
		`SELECT `+userCols+` FROM users WHERE deleted_at IS NULL
         ORDER BY created_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var users []*entities.User
	for rows.Next() {
		u, err := r.scan(rows)
		if err != nil {
			return nil, 0, err
		}
		users = append(users, u)
	}
	return users, total, nil
}
