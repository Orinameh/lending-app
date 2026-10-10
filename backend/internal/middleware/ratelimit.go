package middleware

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-redis/redis_rate/v10"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// RateLimiter is a sliding-window limiter (redis_rate, Lua-backed) with
// budgets declared per route in the router table — not matched by path
// strings here. Public routes key on client IP; authenticated routes key on
// user ID (abuse attribution survives IP rotation), so auth runs BEFORE the
// limiter on those tiers (see api.NewRouter).
//
// Redis outage degrades fail-open: availability over strictness, and auth
// itself still holds. Standard X-RateLimit-* headers are always emitted.
type RateLimiter struct {
	limiter    *redis_rate.Limiter
	trustProxy bool
}

func NewRateLimiter(r *redis.Client) *RateLimiter {
	return &RateLimiter{limiter: redis_rate.NewLimiter(r)}
}

// SetTrustProxy enables X-Forwarded-For handling (only behind a trusted LB).
func (rl *RateLimiter) SetTrustProxy(v bool) { rl.trustProxy = v }

// Per builds a no-burst sliding-window budget: n events per window.
func Per(n int, window time.Duration) redis_rate.Limit {
	return redis_rate.Limit{Rate: n, Burst: n, Period: window}
}

// LimitByIP applies budget keyed on the client IP (public routes).
func (rl *RateLimiter) LimitByIP(next http.Handler, budget redis_rate.Limit) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rl.serve(w, r, next, "ip:"+ClientIP(r, rl.trustProxy), budget)
	})
}

// LimitByUser applies budget keyed on the authenticated user ID. Must run
// inside the auth middleware; without a user in context it denies closed
// (fail-closed is correct here — it means wiring is broken, not Redis).
func (rl *RateLimiter) LimitByUser(next http.Handler, budget redis_rate.Limit) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, ok := UserIDFromContext(r.Context())
		if !ok || uid == uuid.Nil {
			writeJSONError(w, http.StatusUnauthorized, "missing user context")
			return
		}
		rl.serve(w, r, next, "user:"+uid.String(), budget)
	})
}

func (rl *RateLimiter) serve(w http.ResponseWriter, r *http.Request, next http.Handler, key string, budget redis_rate.Limit) {
	res, err := rl.limiter.Allow(r.Context(), key+":"+r.Method+":"+r.URL.Path, budget)
	if err != nil {
		next.ServeHTTP(w, r) // fail-open on Redis outage
		return
	}
	// Informational headers on every response (success and 429 alike).
	h := w.Header()
	h.Set("X-RateLimit-Limit", strconv.Itoa(budget.Rate))
	h.Set("X-RateLimit-Remaining", strconv.Itoa(res.Remaining))
	h.Set("X-RateLimit-Reset", strconv.FormatInt(int64(res.ResetAfter/time.Second), 10))
	if res.Allowed == 0 {
		h.Set("Content-Type", "application/json")
		h.Set("Retry-After", strconv.FormatInt(int64(res.RetryAfter/time.Second), 10))
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error":       "rate limit exceeded",
			"retry_after": int64(res.RetryAfter / time.Second),
		})
		return
	}
	next.ServeHTTP(w, r)
}
