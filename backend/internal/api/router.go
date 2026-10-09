// Package api owns HTTP routing: one declarative table mapping
// method + path → handler + access tier, with middleware composed by tier.
// Path params use the stdlib wildcard syntax and are read by handlers via
// r.PathValue("id") — no manual TrimPrefix/Split parsing anywhere.
package api

import (
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
	handler http.HandlerFunc
}

func table(d Deps) []route {
	return []route{
		// Public.
		{"GET /health", Public, d.Health.Check},
		{"POST /api/v1/auth/register", Public, d.Auth.Register},
		{"POST /api/v1/auth/login", Public, d.Auth.Login},
		{"POST /api/v1/auth/refresh", Public, d.Auth.RefreshToken},
		{"POST /api/v1/auth/logout", Authenticated, d.Auth.Logout},
		{"POST /api/v1/auth/forgot-password", Public, d.Auth.ForgotPassword},
		{"POST /api/v1/auth/reset-password", Public, d.Auth.ResetPassword},

		// Authenticated.
		{"GET /api/v1/profile", Authenticated, d.Auth.GetProfile},
		{"PUT /api/v1/profile", Authenticated, d.Auth.UpdateProfile},
		{"POST /api/v1/loans", Authenticated, d.Loan.CreateLoan},
		{"GET /api/v1/loans", Authenticated, d.Loan.GetUserLoans},
		{"GET /api/v1/loans/{id}", Authenticated, d.Loan.GetLoanDetails},
		{"POST /api/v1/loans/{id}/repay", Authenticated, d.Repayment.MakeRepayment},
		{"GET /api/v1/loans/{id}/repayments", Authenticated, d.Repayment.GetRepaymentHistory},
		{"GET /api/v1/credit/score", Authenticated, d.Credit.GetCreditScore},
		{"GET /api/v1/credit/report", Authenticated, d.Credit.GetCreditReport},
		{"POST /api/v1/credit/refresh", Authenticated, d.Credit.RefreshCreditReport},
		{"GET /api/v1/collections", Authenticated, d.Collection.GetUserCollections},
		{"POST /api/v1/collections/{id}/payment-arrangement", Authenticated, d.Collection.CreatePaymentArrangement},

		// Admin (auth + admin check).
		{"GET /api/v1/admin/dashboard", Admin, d.Admin.GetDashboardStats},
		{"GET /api/v1/admin/users", Admin, d.Admin.GetUsers},
		{"PUT /api/v1/admin/users/{id}/status", Admin, d.Admin.UpdateUserStatus},
		{"PUT /api/v1/admin/users/{id}/role", Admin, d.Admin.UpdateUserRole},
		{"POST /api/v1/admin/users/{id}/kyc/approve", Admin, d.Admin.ApproveKYC},
		{"POST /api/v1/admin/users/{id}/kyc/reject", Admin, d.Admin.RejectKYC},
		{"GET /api/v1/admin/loans", Admin, d.Loan.AdminListLoans},
		{"PUT /api/v1/admin/loans/{id}/status", Admin, d.Loan.AdminUpdateStatus},
		{"POST /api/v1/admin/loans/{id}/disburse", Admin, d.Loan.AdminDisburse},
		{"GET /api/v1/admin/collections", Admin, d.Collection.AdminList},
		{"PUT /api/v1/admin/collections/{id}/status", Admin, d.Collection.AdminUpdateStatus},
		{"POST /api/v1/admin/collections/{id}/assign", Admin, d.Collection.AdminAssign},
		{"POST /api/v1/admin/collections/run-overdue-scan", Admin, d.Collection.AdminRunOverdueScan},
	}
}

type trustProxySetter interface{ SetTrustProxy(bool) }

// NewRouter builds the mux. Middleware order per tier:
// requestID → logging → security → rate limit → auth → admin.
func NewRouter(d Deps, mw MW, trustProxy bool) *http.ServeMux {
	for _, h := range []trustProxySetter{d.Auth, d.Loan, d.Credit, d.Repayment, d.Collection, d.Admin} {
		h.SetTrustProxy(trustProxy)
	}
	mw.RateLimiter.SetTrustProxy(trustProxy)

	mux := http.NewServeMux()
	for _, r := range table(d) {
		var h http.Handler = r.handler
		switch r.tier {
		case Admin:
			h = mw.Admin.Wrap(h)
			fallthrough
		case Authenticated:
			h = mw.Auth.Wrap(h)
			fallthrough
		case Public:
			h = mw.RateLimiter.Wrap(h)
			h = mw.Security.Wrap(h)
			h = mw.Logger.Wrap(h)
			h = middleware.RequestID(h)
		}
		mux.Handle(r.pattern, h)
	}
	return mux
}
