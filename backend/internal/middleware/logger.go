package middleware

import (
	"lending-app/backend/internal/infrastructure/logger"
	"net/http"
	"time"
)

type LoggerMiddleware struct {
	logger *logger.Logger
}

func NewLoggerMiddleware(l *logger.Logger) *LoggerMiddleware {
	return &LoggerMiddleware{logger: l}
}

func (m *LoggerMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		// Query excluded (may carry tokens); path UUIDs retained for tracing.
		m.logger.Info("http_request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"ip", ClientIP(r, false),
		)
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}
