// Package api owns HTTP routing: one declarative table mapping
// method + path → handler + access tier, with middleware composed by tier.
// Path params use the stdlib wildcard syntax and are read by handlers via
// r.PathValue("id") — no manual TrimPrefix/Split parsing anywhere.
package api

import (
	"time"

	"github.com/go-redis/redis_rate/v10"
	"lending-app/backend/internal/handlers"
	"lending-app/backend/internal/middleware"
	"net/http"
)

type Tier int

const (
	Public Tier = iota
	Authenticated
	Admin
)

type Deps struct {
	Auth       *handlers.AuthHandler
	Loan       *handlers.LoanHandler
	Credit     *handlers.CreditHandler
	Repayment  *handlers.RepaymentHandler
	Collection *handlers.CollectionHandler
	Admin      *handlers.AdminHandler
	Health     *handlers.HealthHandler
	KYC        *handlers.KYCHandler
}

type MW struct {
	Logger      *middleware.LoggerMiddleware
	Security    *middleware.SecurityMiddleware
	RateLimiter *middleware.RateLimiter
	Auth        *middleware.AuthMiddleware
	Admin       *middleware.AdminMiddleware
}

type route struct {
	pattern string // "METHOD /path", path may contain {id} wildcards
	tier    Tier
	budget  redis_rate.Limit // zero = tier default
	handler http.HandlerFunc
}

// Tier defaults: generous for product traffic, strict where abuse pays.
var (
	publicDefault = middleware.Per(120, time.Minute)
	authedDefault = middleware.Per(120, time.Minute)
	adminDefault  = middleware.Per(60, time.Minute)
)

func table(d Deps) []route {
	return []route{
		// Public.
		{"GET /health", Public, redis_rate.Limit{}, d.Health.Check},
		{"POST /api/v1/auth/register", Public, middleware.Per(3, 24*time.Hour), d.Auth.Register},
		{"POST /api/v1/auth/login", Public, middleware.Per(5, 15*time.Minute), d.Auth.Login},
		{"POST /api/v1/auth/refresh", Public, middleware.Per(5, 15*time.Minute), d.Auth.RefreshToken},
		{"POST /api/v1/auth/logout", Authenticated, redis_rate.Limit{}, d.Auth.Logout},
		{"POST /api/v1/auth/forgot-password", Public, middleware.Per(5, 15*time.Minute), d.Auth.ForgotPassword},
		{"POST /api/v1/auth/reset-password", Public, middleware.Per(5, 15*time.Minute), d.Auth.ResetPassword},
		{"POST /api/v1/auth/request-email-verification", Authenticated, middleware.Per(5, time.Hour), d.Auth.RequestEmailVerification},
		{"POST /api/v1/auth/verify-email", Public, middleware.Per(10, 15*time.Minute), d.Auth.VerifyEmail},
		{"POST /api/v1/auth/request-phone-otp", Authenticated, middleware.Per(3, 15*time.Minute), d.Auth.RequestPhoneOTP},
		{"POST /api/v1/auth/verify-phone", Authenticated, middleware.Per(10, 15*time.Minute), d.Auth.VerifyPhone},

		// Authenticated.
		{"GET /api/v1/profile", Authenticated, redis_rate.Limit{}, d.Auth.GetProfile},
		{"PUT /api/v1/profile", Authenticated, redis_rate.Limit{}, d.Auth.UpdateProfile},
		{"POST /api/v1/loans", Authenticated, redis_rate.Limit{}, d.Loan.CreateLoan},
		{"GET /api/v1/loans", Authenticated, redis_rate.Limit{}, d.Loan.GetUserLoans},
		{"GET /api/v1/loans/{id}", Authenticated, redis_rate.Limit{}, d.Loan.GetLoanDetails},
		{"POST /api/v1/loans/{id}/repay", Authenticated, middleware.Per(10, time.Minute), d.Repayment.MakeRepayment},
		{"GET /api/v1/loans/{id}/repayments", Authenticated, redis_rate.Limit{}, d.Repayment.GetRepaymentHistory},
		{"GET /api/v1/credit/score", Authenticated, redis_rate.Limit{}, d.Credit.GetCreditScore},
		{"GET /api/v1/credit/report", Authenticated, redis_rate.Limit{}, d.Credit.GetCreditReport},
		{"POST /api/v1/credit/refresh", Authenticated, redis_rate.Limit{}, d.Credit.RefreshCreditReport},
		{"GET /api/v1/collections", Authenticated, redis_rate.Limit{}, d.Collection.GetUserCollections},
		{"POST /api/v1/collections/{id}/payment-arrangement", Authenticated, redis_rate.Limit{}, d.Collection.CreatePaymentArrangement},
		{"POST /api/v1/kyc/submit", Authenticated, middleware.Per(10, time.Hour), d.KYC.Submit},
		{"GET /api/v1/kyc", Authenticated, redis_rate.Limit{}, d.KYC.ListMine},

		// Admin (auth + admin check).
		{"GET /api/v1/admin/dashboard", Admin, redis_rate.Limit{}, d.Admin.GetDashboardStats},
		{"GET /api/v1/admin/users", Admin, redis_rate.Limit{}, d.Admin.GetUsers},
		{"PUT /api/v1/admin/users/{id}/status", Admin, redis_rate.Limit{}, d.Admin.UpdateUserStatus},
		{"PUT /api/v1/admin/users/{id}/role", Admin, redis_rate.Limit{}, d.Admin.UpdateUserRole},
		{"POST /api/v1/admin/users/{id}/kyc/approve", Admin, redis_rate.Limit{}, d.Admin.ApproveKYC},
		{"POST /api/v1/admin/users/{id}/kyc/reject", Admin, redis_rate.Limit{}, d.Admin.RejectKYC},
		{"GET /api/v1/admin/loans", Admin, redis_rate.Limit{}, d.Loan.AdminListLoans},
		{"PUT /api/v1/admin/loans/{id}/status", Admin, redis_rate.Limit{}, d.Loan.AdminUpdateStatus},
		{"POST /api/v1/admin/loans/{id}/disburse", Admin, redis_rate.Limit{}, d.Loan.AdminDisburse},
		{"GET /api/v1/admin/collections", Admin, redis_rate.Limit{}, d.Collection.AdminList},
		{"PUT /api/v1/admin/collections/{id}/status", Admin, redis_rate.Limit{}, d.Collection.AdminUpdateStatus},
		{"POST /api/v1/admin/collections/{id}/assign", Admin, redis_rate.Limit{}, d.Collection.AdminAssign},
		{"POST /api/v1/admin/collections/run-overdue-scan", Admin, redis_rate.Limit{}, d.Collection.AdminRunOverdueScan},
	}
}

type trustProxySetter interface{ SetTrustProxy(bool) }

// NewRouter builds the mux. Middleware order per tier:
// requestID → logging → security → [auth → admin] → rate limit → handler.
// Auth runs before the limiter on authenticated tiers so budgets key on
// user ID (abuse attribution survives IP rotation); public tiers key on IP.
// devMode echoes raw email tokens / OTP codes in responses (non-prod only;
// production delivers via email/SMS vendors).
func NewRouter(d Deps, mw MW, trustProxy, devMode bool) *http.ServeMux {
	for _, h := range []trustProxySetter{d.Auth, d.Loan, d.Credit, d.Repayment, d.Collection, d.Admin, d.KYC} {
		h.SetTrustProxy(trustProxy)
	}
	d.Auth.SetDevExposeSecrets(devMode)
	mw.RateLimiter.SetTrustProxy(trustProxy)

	budgetFor := func(r route) redis_rate.Limit {
		if r.budget.Rate > 0 {
			return r.budget
		}
		switch r.tier {
		case Admin:
			return adminDefault
		case Authenticated:
			return authedDefault
		default:
			return publicDefault
		}
	}

	mux := http.NewServeMux()
	for _, r := range table(d) {
		var h http.Handler = r.handler
		switch r.tier {
		case Admin:
			h = mw.RateLimiter.LimitByUser(h, budgetFor(r))
			h = mw.Admin.Wrap(h)
			h = mw.Auth.Wrap(h)
		case Authenticated:
			h = mw.RateLimiter.LimitByUser(h, budgetFor(r))
			h = mw.Auth.Wrap(h)
		case Public:
			h = mw.RateLimiter.LimitByIP(h, budgetFor(r))
		}
		h = mw.Security.Wrap(h)
		h = mw.Logger.Wrap(h)
		h = middleware.RequestID(h)
		mux.Handle(r.pattern, h)
	}
	return mux
}
