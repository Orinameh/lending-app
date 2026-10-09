package handlers

import (
	"context"
	"lending-app/backend/internal/domain/entities"
	"lending-app/backend/internal/services"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type CollectionHandler struct {
	collectionService *services.CollectionService
	auditService      *services.AuditService
	trustProxy        bool
}

func NewCollectionHandler(c *services.CollectionService, a *services.AuditService) *CollectionHandler {
	return &CollectionHandler{collectionService: c, auditService: a}
}

// User: get own collections
func (h *CollectionHandler) SetTrustProxy(v bool) { h.trustProxy = v }

func (h *CollectionHandler) GetUserCollections(w http.ResponseWriter, r *http.Request) {
	userID, _ := ctxUserID(r)
	list, err := h.collectionService.ListByUser(r.Context(), userID)
	if err != nil {
		internalError(w, "FETCH_FAILED")
		return
	}
	writeSuccess(w, http.StatusOK, list, "")
}

// Admin: list all
func (h *CollectionHandler) AdminList(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	list, total, err := h.collectionService.ListAll(r.Context(), status, page, pageSize)
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
		Data: list, Page: page, PageSize: pageSize, Total: total,
		TotalPages: (total + pageSize - 1) / pageSize,
	})
}

// Admin: update status
func (h *CollectionHandler) AdminUpdateStatus(w http.ResponseWriter, r *http.Request) {
	actorID, _ := ctxUserID(r)
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/admin/collections/")
	idStr = strings.TrimSuffix(idStr, "/status")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid collection id")
		return
	}
	var req struct {
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := h.collectionService.UpdateStatus(r.Context(), id, entities.CollectionStatus(req.Status), req.Note); err != nil {
		writeError(w, http.StatusBadRequest, "UPDATE_FAILED", err.Error())
		return
	}
	ip, ua, reqID := auditMeta(r, h.trustProxy)
	_ = h.auditService.Log(r.Context(), nil, &actorID, services.ActionCollectionUpdate,
		"Collection", &id, map[string]string{"status": req.Status},
		ip, ua, reqID)
	writeSuccess(w, http.StatusOK, nil, "collection updated")
}

// Admin: assign to agent
func (h *CollectionHandler) AdminAssign(w http.ResponseWriter, r *http.Request) {
	actorID, _ := ctxUserID(r)
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/admin/collections/")
	idStr = strings.TrimSuffix(idStr, "/assign")
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid collection id")
		return
	}
	var req struct {
		AgentID uuid.UUID `json:"agentId"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.AgentID == uuid.Nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "agent id required")
		return
	}
	if err := h.collectionService.Assign(r.Context(), id, req.AgentID); err != nil {
		writeError(w, http.StatusBadRequest, "ASSIGN_FAILED", "assignment failed")
		return
	}
	ip, ua, reqID := auditMeta(r, h.trustProxy)
	_ = h.auditService.Log(r.Context(), nil, &actorID, services.ActionCollectionAssign,
		"Collection", &id, map[string]string{"agentId": req.AgentID.String()},
		ip, ua, reqID)
	writeSuccess(w, http.StatusOK, nil, "collection assigned")
}

// User: create payment arrangement
func (h *CollectionHandler) CreatePaymentArrangement(w http.ResponseWriter, r *http.Request) {
	userID, _ := ctxUserID(r)
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// /api/v1/collections/{id}/payment-arrangement
	if len(parts) < 5 {
		writeError(w, http.StatusBadRequest, "INVALID_PATH", "invalid path")
		return
	}
	id, err := uuid.Parse(parts[3])
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid collection id")
		return
	}
	var req struct {
		Note string `json:"note"`
	}
	_ = decodeJSON(w, r, &req)
	if err := h.collectionService.CreatePaymentArrangement(r.Context(), userID, id, req.Note); err != nil {
		writeError(w, http.StatusBadRequest, "ARRANGEMENT_FAILED", err.Error())
		return
	}
	writeSuccess(w, http.StatusOK, nil, "payment arrangement recorded")
}

// Admin: run overdue scan (in production trigger via cron)
func (h *CollectionHandler) AdminRunOverdueScan(w http.ResponseWriter, r *http.Request) {
	// Bounded: scan must finish inside the server WriteTimeout.
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	created, err := h.collectionService.RunOverdueScan(ctx)
	if err != nil {
		internalError(w, "SCAN_FAILED")
		return
	}
	writeSuccess(w, http.StatusOK, map[string]int{"created": created}, "overdue scan complete")
}
