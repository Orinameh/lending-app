package middleware

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type RateLimiter struct {
	redis      *redis.Client
	trustProxy bool
}

func NewRateLimiter(r *redis.Client) *RateLimiter {
	return &RateLimiter{redis: r}
}

// SetTrustProxy enables X-Forwarded-For handling (only behind a trusted LB).
func (rl *RateLimiter) SetTrustProxy(v bool) { rl.trustProxy = v }

// fixed-window atomic increment via Lua: INCR + EXPIRE only on first hit.
var fixedWindow = redis.NewScript(`
local c = redis.call('INCR', KEYS[1])
if c == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end
return c
`)

func (rl *RateLimiter) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := ClientIP(r, rl.trustProxy)
		limit, window := limitsFor(r.URL.Path, r.Method)

		key := fmt.Sprintf("rl:%s:%s:%s", ip, r.Method, r.URL.Path)
		ctx := r.Context()

		count, err := fixedWindow.Run(ctx, rl.redis, []string{key}, int64(window.Milliseconds())).Int()
		if err != nil {
			// fail-open if redis is down (availability over strictness)
			next.ServeHTTP(w, r)
			return
		}
		if count > limit {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", fmt.Sprintf("%d", int(window.Seconds())))
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error":       "rate limit exceeded",
				"retry_after": int(window.Seconds()),
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func limitsFor(path, method string) (int, time.Duration) {
	switch {
	case path == "/api/v1/auth/login":
		return 5, 15 * time.Minute
	case path == "/api/v1/auth/register":
		return 3, 24 * time.Hour
	case path == "/api/v1/auth/refresh" || path == "/api/v1/auth/forgot-password" || path == "/api/v1/auth/reset-password":
		return 5, 15 * time.Minute
	case path == "/api/v1/auth/verify-email":
		return 10, 15 * time.Minute
	case path == "/api/v1/auth/request-email-verification":
		return 5, time.Hour
	case path == "/api/v1/auth/request-phone-otp":
		return 3, 15 * time.Minute
	case path == "/api/v1/auth/verify-phone":
		return 10, 15 * time.Minute
	case path == "/api/v1/kyc/submit":
		return 10, time.Hour
	case strings.HasPrefix(path, "/api/v1/admin"):
		return 60, time.Minute
	case method == "POST" && strings.Contains(path, "/repay"):
		return 10, time.Minute
	default:
		return 120, time.Minute
	}
}
