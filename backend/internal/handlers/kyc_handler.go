package handlers

import (
	"errors"
	"lending-app/backend/internal/services"
	"net/http"
)

type KYCHandler struct {
	kycService   *services.KYCService
	auditService *services.AuditService
	trustProxy   bool
}

func NewKYCHandler(k *services.KYCService, a *services.AuditService) *KYCHandler {
	return &KYCHandler{kycService: k, auditService: a}
}

func (h *KYCHandler) SetTrustProxy(v bool) { h.trustProxy = v }

// POST /api/v1/kyc/submit — registry check for BVN/NIN, human review after.
func (h *KYCHandler) Submit(w http.ResponseWriter, r *http.Request) {
	userID, ok := ctxUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing user context")
		return
	}
	var req services.KYCSubmitRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	doc, err := h.kycService.Submit(r.Context(), userID, &req)
	if err != nil {
		// Sentinel errors carry borrower-safe messages (validation,
		// mismatch reasons, conflicts, provider outages). Anything else
		// is infrastructure failure — never leak driver details.
		switch {
		case errors.Is(err, services.ErrKYCValidation),
			errors.Is(err, services.ErrKYCMismatch),
			errors.Is(err, services.ErrKYCConflict),
			errors.Is(err, services.ErrKYCProvider):
			writeError(w, http.StatusBadRequest, "KYC_SUBMIT_FAILED", err.Error())
		default:
			internalError(w, "KYC_SUBMIT_FAILED")
		}
		return
	}
	ip, ua, reqID := auditMeta(r, h.trustProxy)
	_ = h.auditService.Log(r.Context(), &userID, &userID, services.ActionKYCSubmitted,
		"KYCDocument", &doc.ID,
		map[string]string{"documentType": doc.DocumentType, "status": doc.Status},
		ip, ua, reqID)
	writeSuccess(w, http.StatusCreated, doc, "kyc submitted")
}

// GET /api/v1/kyc — own documents (numbers decrypted for the owner).
func (h *KYCHandler) ListMine(w http.ResponseWriter, r *http.Request) {
	userID, ok := ctxUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing user context")
		return
	}
	docs, err := h.kycService.ListMine(r.Context(), userID)
	if err != nil {
		internalError(w, "FETCH_FAILED")
		return
	}
	writeSuccess(w, http.StatusOK, docs, "")
}
