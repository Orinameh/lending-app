package handlers

import (
	"lending-app/backend/internal/services"
	"net/http"
)

type CreditHandler struct {
	creditService *services.CreditService
	auditService  *services.AuditService
	trustProxy    bool
}

func NewCreditHandler(c *services.CreditService, a *services.AuditService) *CreditHandler {
	return &CreditHandler{creditService: c, auditService: a}
}

func (h *CreditHandler) SetTrustProxy(v bool) { h.trustProxy = v }

func (h *CreditHandler) GetCreditScore(w http.ResponseWriter, r *http.Request) {
	userID, _ := ctxUserID(r)
	cr, err := h.creditService.GetLatestOrGenerate(r.Context(), userID)
	if err != nil {
		internalError(w, "CREDIT_FAILED")
		return
	}
	writeSuccess(w, http.StatusOK, map[string]interface{}{
		"score":  cr.Score,
		"rating": cr.Rating,
	}, "")
}

func (h *CreditHandler) GetCreditReport(w http.ResponseWriter, r *http.Request) {
	userID, _ := ctxUserID(r)
	cr, err := h.creditService.GetLatestOrGenerate(r.Context(), userID)
	if err != nil {
		internalError(w, "CREDIT_FAILED")
		return
	}
	writeSuccess(w, http.StatusOK, cr, "")
}

func (h *CreditHandler) RefreshCreditReport(w http.ResponseWriter, r *http.Request) {
	userID, _ := ctxUserID(r)
	cr, err := h.creditService.GenerateReport(r.Context(), userID)
	if err != nil {
		internalError(w, "REFRESH_FAILED")
		return
	}
	ip, ua, reqID := auditMeta(r, h.trustProxy)
	_ = h.auditService.Log(r.Context(), &userID, &userID, services.ActionCreditRefresh,
		"CreditReport", &cr.ID, nil, ip, ua, reqID)
	writeSuccess(w, http.StatusOK, cr, "credit report refreshed")
}
