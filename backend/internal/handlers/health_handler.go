package handlers

import (
	"context"
	"database/sql"
	"lending-app/backend/pkg/crypto"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
)

var appVersion = "1.0.0"

type HealthHandler struct {
	encryptor *crypto.EncryptionService
	db        *sql.DB
	redis     *redis.Client
}

func NewHealthHandler(e *crypto.EncryptionService, db *sql.DB, r *redis.Client) *HealthHandler {
	return &HealthHandler{encryptor: e, db: db, redis: r}
}

func (h *HealthHandler) Check(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	checks := map[string]interface{}{
		"encryption": h.encryptor.HealthCheck() == nil,
		"database":   h.db.PingContext(ctx) == nil,
		"redis":      h.redis.Ping(ctx).Err() == nil,
	}
	status := http.StatusOK
	body := "ok"
	for _, v := range checks {
		if v == false {
			status = http.StatusServiceUnavailable
			body = "degraded"
			break
		}
	}
	writeJSON(w, status, map[string]interface{}{
		"status":    body,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"version":   appVersion,
		"checks":    checks,
	})
}
