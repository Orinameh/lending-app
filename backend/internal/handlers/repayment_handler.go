package handlers

import (
	"lending-app/backend/internal/services"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type RepaymentHandler struct {
	repaymentService *services.RepaymentService
	auditService     *services.AuditService
	trustProxy       bool
}

func NewRepaymentHandler(r *services.RepaymentService, a *services.AuditService) *RepaymentHandler {
	return &RepaymentHandler{repaymentService: r, auditService: a}
}

func (h *RepaymentHandler) SetTrustProxy(v bool) { h.trustProxy = v }

// POST /api/v1/loans/{id}/repay
func (h *RepaymentHandler) MakeRepayment(w http.ResponseWriter, r *http.Request) {
	userID, _ := ctxUserID(r)
	loanID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid loan id")
		return
	}

	var raw struct {
		Amount        string `json:"amount"`
		PaymentMethod string `json:"paymentMethod"`
		TransactionID string `json:"transactionId"`
	}
	if !decodeJSON(w, r, &raw) {
		return
	}
	amount, err := decimal.NewFromString(strings.TrimSpace(raw.Amount))
	if err != nil || amount.LessThanOrEqual(decimal.Zero) {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "amount must be a positive number")
		return
	}
	req := services.RepaymentRequest{
		Amount:        amount,
		PaymentMethod: strings.TrimSpace(raw.PaymentMethod),
		TransactionID: strings.TrimSpace(raw.TransactionID),
	}
	// Idempotency-Key header fallback.
	if req.TransactionID == "" {
		req.TransactionID = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	res, err := h.repaymentService.MakeRepayment(r.Context(), userID, loanID, &req)
	if err != nil {
		msg := err.Error()
		switch msg {
		case "forbidden", "duplicate transaction id":
			writeError(w, http.StatusConflict, "REPAYMENT_FAILED", msg)
		case "loan already fully repaid", "loan is not in a repayable state",
			"amount must be greater than zero", "payment method is required",
			"transaction id is required for idempotency", "no pending repayments to apply this amount to":
			writeError(w, http.StatusBadRequest, "REPAYMENT_FAILED", msg)
		default:
			writeError(w, http.StatusBadRequest, "REPAYMENT_FAILED", msg)
		}
		ip, ua, reqID := auditMeta(r, h.trustProxy)
		_ = h.auditService.Log(r.Context(), &userID, &userID, services.ActionRepaymentFailed,
			"Loan", &loanID, map[string]string{"reason": msg}, ip, ua, reqID)
		return
	}
	ip, ua, reqID := auditMeta(r, h.trustProxy)
	_ = h.auditService.Log(r.Context(), &userID, &userID, services.ActionRepaymentMade,
		"Loan", &loanID, map[string]string{"applied": res.Applied.String(), "overpayment": res.Overpayment.String()},
		ip, ua, reqID)
	writeSuccess(w, http.StatusOK, res, "repayment recorded")
}

// GET /api/v1/loans/{id}/repayments
func (h *RepaymentHandler) GetRepaymentHistory(w http.ResponseWriter, r *http.Request) {
	userID, _ := ctxUserID(r)
	loanID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid loan id")
		return
	}
	schedule, err := h.repaymentService.GetSchedule(r.Context(), userID, loanID)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "not found")
		return
	}
	writeSuccess(w, http.StatusOK, schedule, "")
}
