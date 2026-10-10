package services

import (
	"context"
	"encoding/json"
	"lending-app/backend/internal/domain/entities"
	"lending-app/backend/internal/repositories"
	"strings"
	"time"

	"github.com/google/uuid"
)

type AuditAction string

const (
	ActionUserRegistration  AuditAction = "USER_REGISTRATION"
	ActionUserLogin         AuditAction = "USER_LOGIN"
	ActionUserLogout        AuditAction = "USER_LOGOUT"
	ActionLoginFailed       AuditAction = "LOGIN_FAILED"
	ActionUserProfileUpdate AuditAction = "USER_PROFILE_UPDATE"
	ActionUserStatusChange  AuditAction = "USER_STATUS_CHANGE"
	ActionUserRoleChange    AuditAction = "USER_ROLE_CHANGE"
	ActionPasswordResetReq  AuditAction = "PASSWORD_RESET_REQUEST"
	ActionPasswordReset     AuditAction = "PASSWORD_RESET"
	ActionEmailVerified     AuditAction = "EMAIL_VERIFIED"
	ActionPhoneVerified     AuditAction = "PHONE_VERIFIED"
	ActionLoanApplication   AuditAction = "LOAN_APPLICATION"
	ActionLoanApproval      AuditAction = "LOAN_APPROVAL"
	ActionLoanStatusChange  AuditAction = "LOAN_STATUS_CHANGE"
	ActionLoanDisbursement  AuditAction = "LOAN_DISBURSEMENT"
	ActionRepaymentMade     AuditAction = "REPAYMENT_MADE"
	ActionRepaymentFailed   AuditAction = "REPAYMENT_FAILED"
	ActionCollectionUpdate  AuditAction = "COLLECTION_UPDATE"
	ActionCollectionAssign  AuditAction = "COLLECTION_ASSIGN"
	ActionCreditRefresh     AuditAction = "CREDIT_REFRESH"
	ActionKYCApprove        AuditAction = "KYC_APPROVE"
	ActionKYCReject         AuditAction = "KYC_REJECT"
	ActionKYCUpdate         AuditAction = "KYC_UPDATE"
	ActionKYCSubmitted      AuditAction = "KYC_SUBMITTED"
)

type AuditService struct {
	repo *repositories.AuditRepository
}

func NewAuditService(repo *repositories.AuditRepository) *AuditService {
	return &AuditService{repo: repo}
}

func (s *AuditService) Log(
	ctx context.Context,
	subjectID *uuid.UUID,
	actorID *uuid.UUID,
	action AuditAction,
	resourceType string,
	resourceID *uuid.UUID,
	details interface{},
	ip, userAgent, requestID string,
) error {
	var raw json.RawMessage
	if details != nil {
		b, err := json.Marshal(maskPII(details))
		if err == nil {
			raw = b
		}
	}
	if len(userAgent) > 500 {
		userAgent = userAgent[:500]
	}
	return s.repo.Create(ctx, &entities.AuditLog{
		ID:           uuid.New(),
		UserID:       subjectID,
		ActorID:      actorID,
		Action:       string(action),
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Details:      raw,
		IPAddress:    ip,
		UserAgent:    userAgent,
		RequestID:    requestID,
		CreatedAt:    time.Now().UTC(),
	})
}

var sensitiveKeys = map[string]bool{
	"bvn": true, "nin": true, "password": true, "passwordhash": true,
	"password_hash": true, "email": true, "phone": true, "address": true,
	"token": true, "refreshtoken": true, "firstname": true, "first_name": true,
	"lastname": true, "last_name": true, "dateofbirth": true, "date_of_birth": true,
	"documentnumber": true, "document_number": true, "filepath": true, "file_path": true,
	"annualincome": true, "annual_income": true, "city": true, "state": true,
	"ipaddress": true, "ip_address": true, "secret": true, "authorization": true,
}

const redacted = "***REDACTED***"

// maskPII recursively redacts known PII/sensitive keys (case-insensitive,
// nested maps and slices). Non-object payloads pass through only if they are
// scalar; maps/slices are rebuilt redacted.
func maskPII(v interface{}) interface{} {
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]string{"_unserializable": redacted}
	}
	var anyV interface{}
	if err := json.Unmarshal(b, &anyV); err != nil {
		return map[string]string{"_unserializable": redacted}
	}
	return redactValue(anyV)
}

func redactValue(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, val := range t {
			if sensitiveKeys[strings.ToLower(k)] {
				out[k] = redacted
				continue
			}
			out[k] = redactValue(val)
		}
		return out
	case []interface{}:
		for i, e := range t {
			t[i] = redactValue(e)
		}
		return t
	default:
		return v
	}
}
