package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
)

// RequestID attaches a UUID to every request (X-Request-ID out, CtxReqID in)
// for log/audit correlation.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if _, err := uuid.Parse(id); err != nil {
			id = uuid.NewString()
		}
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), CtxReqID, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func RequestIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(CtxReqID).(string); ok {
		return v
	}
	return ""
}
