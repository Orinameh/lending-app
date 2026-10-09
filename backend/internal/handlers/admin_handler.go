package handlers

import (
	"lending-app/backend/internal/services"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

type AdminHandler struct {
	adminService *services.AdminService
	auditService *services.AuditService
	trustProxy   bool
}

func NewAdminHandler(a *services.AdminService, au *services.AuditService) *AdminHandler {
	return &AdminHandler{adminService: a, auditService: au}
}

func (h *AdminHandler) SetTrustProxy(v bool) { h.trustProxy = v }

func (h *AdminHandler) GetDashboardStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.adminService.GetDashboardStats(r.Context())
	if err != nil {
		internalError(w, "STATS_FAILED")
		return
	}
	writeSuccess(w, http.StatusOK, stats, "")
}

func (h *AdminHandler) GetUsers(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	users, total, err := h.adminService.ListUsers(r.Context(), page, pageSize)
	if err != nil {
		internalError(w, "FETCH_FAILED")
		return
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	// Return profiles only (never ciphertext/PII blobs).
	profiles := make([]interface{}, 0, len(users))
	for _, u := range users {
		profiles = append(profiles, u.Profile())
	}
	writeJSON(w, http.StatusOK, PaginatedResponse{
		Data: profiles, Page: page, PageSize: pageSize, Total: total,
		TotalPages: (total + pageSize - 1) / pageSize,
	})
}

// PUT /api/v1/admin/users/{id}/status
func (h *AdminHandler) UpdateUserStatus(w http.ResponseWriter, r *http.Request) {
	actorID, _ := ctxUserID(r)
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/admin/users/")
	idStr = strings.TrimSuffix(idStr, "/status")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid user id")
		return
	}
	if id == actorID {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "cannot change your own status")
		return
	}
	var req struct {
		IsActive bool `json:"isActive"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := h.adminService.UpdateUserStatus(r.Context(), id, req.IsActive); err != nil {
		internalError(w, "UPDATE_FAILED")
		return
	}
	ip, ua, reqID := auditMeta(r, h.trustProxy)
	_ = h.auditService.Log(r.Context(), &id, &actorID, services.ActionUserStatusChange,
		"User", &id, map[string]bool{"isActive": req.IsActive},
		ip, ua, reqID)
	writeSuccess(w, http.StatusOK, nil, "user status updated")
}

// PUT /api/v1/admin/users/{id}/role
func (h *AdminHandler) UpdateUserRole(w http.ResponseWriter, r *http.Request) {
	actorID, _ := ctxUserID(r)
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/admin/users/")
	idStr = strings.TrimSuffix(idStr, "/role")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid user id")
		return
	}
	if id == actorID {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "cannot change your own role")
		return
	}
	var req struct {
		Role string `json:"role"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Role != "user" && req.Role != "agent" && req.Role != "admin" {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid role")
		return
	}
	if err := h.adminService.UpdateUserRole(r.Context(), id, req.Role); err != nil {
		writeError(w, http.StatusBadRequest, "UPDATE_FAILED", err.Error())
		return
	}
	ip, ua, reqID := auditMeta(r, h.trustProxy)
	_ = h.auditService.Log(r.Context(), &id, &actorID, services.ActionUserRoleChange,
		"User", &id, map[string]string{"role": req.Role},
		ip, ua, reqID)
	writeSuccess(w, http.StatusOK, nil, "user role updated")
}

// POST /api/v1/admin/users/{id}/kyc/approve
func (h *AdminHandler) ApproveKYC(w http.ResponseWriter, r *http.Request) {
	actorID, _ := ctxUserID(r)
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/admin/users/")
	idStr = strings.TrimSuffix(idStr, "/kyc/approve")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid user id")
		return
	}
	if err := h.adminService.UpdateKYCStatus(r.Context(), id, "verified"); err != nil {
		writeError(w, http.StatusBadRequest, "UPDATE_FAILED", err.Error())
		return
	}
	ip, ua, reqID := auditMeta(r, h.trustProxy)
	_ = h.auditService.Log(r.Context(), &id, &actorID, services.ActionKYCApprove,
		"User", &id, map[string]string{"status": "verified"},
		ip, ua, reqID)
	writeSuccess(w, http.StatusOK, nil, "KYC approved")
}

// POST /api/v1/admin/users/{id}/kyc/reject
func (h *AdminHandler) RejectKYC(w http.ResponseWriter, r *http.Request) {
	actorID, _ := ctxUserID(r)
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/admin/users/")
	idStr = strings.TrimSuffix(idStr, "/kyc/reject")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid user id")
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	_ = decodeJSON(w, r, &req)
	if len(req.Reason) > 500 {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "reason too long")
		return
	}
	if err := h.adminService.UpdateKYCStatus(r.Context(), id, "rejected"); err != nil {
		writeError(w, http.StatusBadRequest, "UPDATE_FAILED", err.Error())
		return
	}
	ip, ua, reqID := auditMeta(r, h.trustProxy)
	_ = h.auditService.Log(r.Context(), &id, &actorID, services.ActionKYCReject,
		"User", &id, map[string]string{"status": "rejected", "reason": req.Reason},
		ip, ua, reqID)
	writeSuccess(w, http.StatusOK, nil, "KYC rejected")
}
