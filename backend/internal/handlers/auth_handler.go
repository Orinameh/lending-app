package handlers

import (
	"lending-app/backend/internal/services"
	"lending-app/backend/pkg/crypto"
	"lending-app/backend/pkg/validator"
	"net/http"
	"strings"

	"github.com/shopspring/decimal"
)

type AuthHandler struct {
	authService  *services.AuthService
	auditService *services.AuditService
	encryptor    *crypto.EncryptionService
	trustProxy   bool
}

func NewAuthHandler(a *services.AuthService, au *services.AuditService, e *crypto.EncryptionService) *AuthHandler {
	return &AuthHandler{authService: a, auditService: au, encryptor: e}
}

func (h *AuthHandler) SetTrustProxy(v bool) { h.trustProxy = v }

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req services.RegisterRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	user, err := h.authService.Register(r.Context(), &req)
	if err != nil {
		// Generic to avoid user-enumeration; validation errors stay specific.
		msg := err.Error()
		if msg == "unable to complete registration" {
			writeError(w, http.StatusConflict, "REGISTRATION_FAILED", "unable to complete registration")
			return
		}
		writeError(w, http.StatusBadRequest, "REGISTRATION_FAILED", msg)
		return
	}

	ip, ua, reqID := auditMeta(r, h.trustProxy)
	_ = h.auditService.Log(r.Context(), &user.ID, nil, services.ActionUserRegistration,
		"User", &user.ID, map[string]string{"kycStatus": user.KYCStatus},
		ip, ua, reqID)

	writeSuccess(w, http.StatusCreated, user.Profile(), "registration successful")
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req services.LoginRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	resp, err := h.authService.Login(r.Context(), &req)
	if err != nil {
		ip, ua, reqID := auditMeta(r, h.trustProxy)
		_ = h.auditService.Log(r.Context(), nil, nil, services.ActionLoginFailed,
			"User", nil, map[string]string{"reason": "invalid credentials"},
			ip, ua, reqID)
		writeError(w, http.StatusUnauthorized, "LOGIN_FAILED", "invalid credentials")
		return
	}

	ip, ua, reqID := auditMeta(r, h.trustProxy)
	_ = h.auditService.Log(r.Context(), &resp.User.ID, &resp.User.ID, services.ActionUserLogin,
		"User", &resp.User.ID, nil, ip, ua, reqID)

	writeSuccess(w, http.StatusOK, resp, "login successful")
}

func (h *AuthHandler) RefreshToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refreshToken"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.RefreshToken) == "" {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "refresh token required")
		return
	}
	resp, err := h.authService.RefreshToken(r.Context(), req.RefreshToken)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "REFRESH_FAILED", "invalid refresh token")
		return
	}
	writeSuccess(w, http.StatusOK, resp, "token refreshed")
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	userID, ok := ctxUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing user context")
		return
	}
	var req struct {
		RefreshToken string `json:"refreshToken"`
	}
	_ = decodeJSON(w, r, &req)
	if strings.TrimSpace(req.RefreshToken) != "" {
		_ = h.authService.Logout(r.Context(), req.RefreshToken)
	}
	ip, ua, reqID := auditMeta(r, h.trustProxy)
	_ = h.auditService.Log(r.Context(), &userID, &userID, services.ActionUserLogout,
		"User", &userID, nil, ip, ua, reqID)
	writeSuccess(w, http.StatusOK, nil, "logged out")
}

func (h *AuthHandler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	// Always generic (no enumeration). Token is emailed out-of-band;
	// in dev it is only in server logs, never in the response.
	raw, sent, err := h.authService.RequestPasswordReset(r.Context(), req.Email)
	if err != nil {
		internalError(w, "RESET_FAILED")
		return
	}
	ip, ua, reqID := auditMeta(r, h.trustProxy)
	_ = h.auditService.Log(r.Context(), nil, nil, services.ActionPasswordResetReq,
		"User", nil, map[string]string{"requested": "true"}, ip, ua, reqID)
	_ = raw
	_ = sent
	writeSuccess(w, http.StatusOK, nil, "if the email exists, a reset link has been sent")
}

func (h *AuthHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token       string `json:"token"`
		NewPassword string `json:"newPassword"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Token) == "" || req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "token and new password are required")
		return
	}
	if err := h.authService.ResetPassword(r.Context(), strings.TrimSpace(req.Token), req.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, "RESET_FAILED", err.Error())
		return
	}
	ip, ua, reqID := auditMeta(r, h.trustProxy)
	_ = h.auditService.Log(r.Context(), nil, nil, services.ActionPasswordReset,
		"User", nil, map[string]string{"reset": "true"}, ip, ua, reqID)
	writeSuccess(w, http.StatusOK, nil, "password reset successful")
}

func (h *AuthHandler) GetProfile(w http.ResponseWriter, r *http.Request) {
	userID, ok := ctxUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing user context")
		return
	}
	user, err := h.authService.GetUser(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "USER_NOT_FOUND", "user not found")
		return
	}
	writeSuccess(w, http.StatusOK, user.Profile(), "")
}

func (h *AuthHandler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	userID, ok := ctxUserID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing user context")
		return
	}
	user, err := h.authService.GetUser(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "USER_NOT_FOUND", "user not found")
		return
	}

	var req struct {
		Phone          string `json:"phone"`
		Address        string `json:"address"`
		EmploymentType string `json:"employmentType"`
		AnnualIncome   string `json:"annualIncome"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Phone != "" {
		if !validator.ValidatePhone(strings.TrimSpace(req.Phone)) {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid phone format")
			return
		}
		user.Phone.Plain = strings.TrimSpace(req.Phone)
	}
	if req.Address != "" {
		if len(req.Address) > 500 {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "address too long")
			return
		}
		user.Address.Plain = req.Address
	}
	if req.EmploymentType != "" {
		if !validEmployment(req.EmploymentType) {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid employment type")
			return
		}
		user.EmploymentType = req.EmploymentType
	}
	if req.AnnualIncome != "" {
		d, err := decimal.NewFromString(strings.TrimSpace(req.AnnualIncome))
		if err != nil || d.IsNegative() {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid annual income")
			return
		}
		user.AnnualIncome = d
	}
	if err := h.authService.UpdateProfile(r.Context(), user); err != nil {
		internalError(w, "UPDATE_FAILED")
		return
	}
	ip, ua, reqID := auditMeta(r, h.trustProxy)
	_ = h.auditService.Log(r.Context(), &userID, &userID, services.ActionUserProfileUpdate,
		"User", &userID, nil, ip, ua, reqID)
	updated, err := h.authService.GetUser(r.Context(), userID)
	if err != nil {
		writeSuccess(w, http.StatusOK, user.Profile(), "profile updated")
		return
	}
	writeSuccess(w, http.StatusOK, updated.Profile(), "profile updated")
}

func validEmployment(s string) bool {
	switch s {
	case "employed", "self_employed", "unemployed", "student", "retired", "other":
		return true
	}
	return false
}
