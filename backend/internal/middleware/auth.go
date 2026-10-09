package middleware

import (
	"context"
	"lending-app/backend/internal/services"
	"net"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

type ctxKey string

const (
	CtxUserID ctxKey = "userID"
	CtxRole   ctxKey = "userRole"
	CtxReqID  ctxKey = "requestID"
)

type AuthMiddleware struct {
	authService *services.AuthService
}

func NewAuthMiddleware(a *services.AuthService) *AuthMiddleware {
	return &AuthMiddleware{authService: a}
}

func (m *AuthMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			writeJSONError(w, http.StatusUnauthorized, "missing authorization header")
			return
		}
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			writeJSONError(w, http.StatusUnauthorized, "invalid authorization header")
			return
		}
		// Active + role revalidated against DB (stale-role/disabled-account safe).
		userID, role, err := m.authService.ValidateTokenActive(r.Context(), parts[1])
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}
		ctx := context.WithValue(r.Context(), CtxUserID, userID)
		ctx = context.WithValue(ctx, CtxRole, role)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type AdminMiddleware struct{}

func NewAdminMiddleware() *AdminMiddleware { return &AdminMiddleware{} }

func (m *AdminMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role, ok := r.Context().Value(CtxRole).(string)
		if !ok || role != "admin" {
			writeJSONError(w, http.StatusForbidden, "admin access required")
			return
		}
		next.ServeHTTP(w, r.WithContext(r.Context()))
	})
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":"` + msg + `"}`))
}

// UserIDFromContext is a helper
func UserIDFromContext(ctx context.Context) (uuid.UUID, bool) {
	v, ok := ctx.Value(CtxUserID).(uuid.UUID)
	return v, ok
}

func RoleFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(CtxRole).(string)
	return v, ok
}

// ClientIP returns the origin IP: RemoteAddr host portion. X-Forwarded-For is
// only trusted when TrustProxy is enabled (see config); otherwise it is
// ignored to prevent spoofing.
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			if ip := strings.TrimSpace(strings.Split(fwd, ",")[0]); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
