package handlers

import (
	"lending-app/backend/internal/domain/entities"
	"lending-app/backend/internal/services"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

type LoanHandler struct {
	loanService  *services.LoanService
	auditService *services.AuditService
	trustProxy   bool
}

func NewLoanHandler(l *services.LoanService, a *services.AuditService) *LoanHandler {
	return &LoanHandler{loanService: l, auditService: a}
}

func (h *LoanHandler) SetTrustProxy(v bool) { h.trustProxy = v }

func (h *LoanHandler) CreateLoan(w http.ResponseWriter, r *http.Request) {
	userID, ok := ctxUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing user context")
		return
	}
	var req services.LoanApplicationRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	// Idempotency-Key header fallback.
	if req.IdempotencyKey == "" {
		req.IdempotencyKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	loan, err := h.loanService.ApplyForLoan(r.Context(), userID, &req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "LOAN_APPLICATION_FAILED", err.Error())
		return
	}
	ip, ua, reqID := auditMeta(r, h.trustProxy)
	_ = h.auditService.Log(r.Context(), &loan.UserID, &userID, services.ActionLoanApplication,
		"Loan", &loan.ID, map[string]interface{}{"amount": loan.Amount.String(), "status": loan.Status},
		ip, ua, reqID)
	writeSuccess(w, http.StatusCreated, loan, "loan application submitted")
}

func (h *LoanHandler) GetUserLoans(w http.ResponseWriter, r *http.Request) {
	userID, _ := ctxUserID(r)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))

	loans, total, err := h.loanService.GetUserLoans(r.Context(), userID, page, pageSize)
	if err != nil {
		internalError(w, "FETCH_FAILED")
		return
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	writeJSON(w, http.StatusOK, PaginatedResponse{
		Data:       loans,
		Page:       page,
		PageSize:   pageSize,
		Total:      total,
		TotalPages: (total + pageSize - 1) / pageSize,
	})
}

func (h *LoanHandler) GetLoanDetails(w http.ResponseWriter, r *http.Request) {
	userID, _ := ctxUserID(r)
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/loans/")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid loan id")
		return
	}
	loan, err := h.loanService.GetLoanDetails(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "LOAN_NOT_FOUND", "loan not found")
		return
	}
	if loan.UserID != userID {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "not your loan")
		return
	}
	writeSuccess(w, http.StatusOK, loan, "")
}

// Admin: list all
func (h *LoanHandler) AdminListLoans(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	loans, total, err := h.loanService.ListAll(r.Context(), status, page, pageSize)
	if err != nil {
		writeError(w, http.StatusBadRequest, "FETCH_FAILED", err.Error())
		return
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	writeJSON(w, http.StatusOK, PaginatedResponse{
		Data: loans, Page: page, PageSize: pageSize, Total: total,
		TotalPages: (total + pageSize - 1) / pageSize,
	})
}

// Admin: update status (state-machine enforced in service)
func (h *LoanHandler) AdminUpdateStatus(w http.ResponseWriter, r *http.Request) {
	actorID, _ := ctxUserID(r)
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/admin/loans/")
	idStr = strings.TrimSuffix(idStr, "/status")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid loan id")
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !validLoanStatus(req.Status) {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid loan status")
		return
	}
	if err := h.loanService.UpdateStatus(r.Context(), id, entities.LoanStatus(req.Status)); err != nil {
		writeError(w, http.StatusBadRequest, "UPDATE_FAILED", err.Error())
		return
	}
	ip, ua, reqID := auditMeta(r, h.trustProxy)
	_ = h.auditService.Log(r.Context(), &id, &actorID, services.ActionLoanStatusChange,
		"Loan", &id, map[string]string{"status": req.Status}, ip, ua, reqID)
	writeSuccess(w, http.StatusOK, nil, "loan status updated")
}

// Admin: disburse (approved → disbursed + schedule anchored at funding)
func (h *LoanHandler) AdminDisburse(w http.ResponseWriter, r *http.Request) {
	actorID, _ := ctxUserID(r)
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/admin/loans/")
	idStr = strings.TrimSuffix(idStr, "/disburse")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid loan id")
		return
	}
	loan, err := h.loanService.Disburse(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadRequest, "DISBURSE_FAILED", err.Error())
		return
	}
	ip, ua, reqID := auditMeta(r, h.trustProxy)
	_ = h.auditService.Log(r.Context(), &loan.UserID, &actorID, services.ActionLoanDisbursement,
		"Loan", &id, map[string]string{"amount": loan.Amount.String()}, ip, ua, reqID)
	writeSuccess(w, http.StatusOK, loan, "loan disbursed")
}

func validLoanStatus(s string) bool {
	switch entities.LoanStatus(s) {
	case entities.LoanStatusPending, entities.LoanStatusUnderReview,
		entities.LoanStatusApproved, entities.LoanStatusDisbursed,
		entities.LoanStatusRepaid, entities.LoanStatusDefaulted,
		entities.LoanStatusDeclined:
		return true
	}
	return false
}
