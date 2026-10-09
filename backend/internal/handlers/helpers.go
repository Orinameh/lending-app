package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"lending-app/backend/internal/middleware"

	"github.com/google/uuid"
)

const maxBodyBytes = 1 << 20 // 1 MiB

// decodeJSON enforces a body cap and strict content type.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst interface{}) bool {
	if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "INVALID_CONTENT_TYPE", "content-type must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "empty request body")
		} else {
			writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		}
		return false
	}
	return true
}

func ctxUserID(r *http.Request) (uuid.UUID, bool) {
	return middleware.UserIDFromContext(r.Context())
}

func auditMeta(r *http.Request, trustProxy bool) (ip, ua, reqID string) {
	return middleware.ClientIP(r, trustProxy), r.UserAgent(), middleware.RequestIDFromContext(r.Context())
}

// internalError hides storage/driver details from clients.
func internalError(w http.ResponseWriter, code string) {
	writeError(w, http.StatusInternalServerError, code, "internal error")
}
