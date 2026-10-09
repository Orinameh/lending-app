package services

import (
	"testing"

	"lending-app/backend/internal/domain/entities"

	"github.com/shopspring/decimal"
)

func TestCalculateLoanTermsInvariants(t *testing.T) {
	amount := decimal.NewFromInt(1_000_000)
	rate := decimal.NewFromFloat(12.0)
	monthly, total, interest := calculateLoanTerms(amount, rate, 12)

	if monthly.LessThanOrEqual(decimal.Zero) {
		t.Fatalf("monthly must be positive, got %s", monthly)
	}
	if !total.Equal(monthly.Mul(decimal.NewFromInt(12))) {
		t.Fatalf("total must equal monthly*n: %s vs %s", total, monthly.Mul(decimal.NewFromInt(12)))
	}
	if !interest.Equal(total.Sub(amount)) {
		t.Fatalf("interest must equal total-amount: %s", interest)
	}
	// Known PMT value: 1,000,000 @ 12% nominal × 12 ≈ 88,848.79.
	want := decimal.NewFromFloat(88848.79)
	if monthly.Sub(want).Abs().GreaterThan(decimal.NewFromFloat(0.05)) {
		t.Fatalf("monthly = %s, want ≈ %s", monthly, want)
	}
}

func TestCalculateLoanTermsZeroRate(t *testing.T) {
	monthly, total, interest := calculateLoanTerms(decimal.NewFromInt(120_000), decimal.Zero, 12)
	if !monthly.Equal(decimal.NewFromInt(10_000)) {
		t.Fatalf("zero-rate monthly = %s, want 10000", monthly)
	}
	if !total.Equal(decimal.NewFromInt(120_000)) || !interest.IsZero() {
		t.Fatalf("zero-rate total/interest = %s/%s", total, interest)
	}
}

func TestEffectiveAnnualRate(t *testing.T) {
	// (1+0.01)^12 - 1 ≈ 12.6825%.
	eff := effectiveAnnualRate(decimal.NewFromFloat(12.0))
	want := decimal.NewFromFloat(12.6825)
	if eff.Sub(want).Abs().GreaterThan(decimal.NewFromFloat(0.001)) {
		t.Fatalf("effective APR = %s, want ≈ %s", eff, want)
	}
	if !effectiveAnnualRate(decimal.Zero).IsZero() {
		t.Fatal("zero nominal must give zero effective APR")
	}
}

func TestLoanStateMachine(t *testing.T) {
	ok := []struct{ from, to entities.LoanStatus }{
		{entities.LoanStatusPending, entities.LoanStatusApproved},
		{entities.LoanStatusPending, entities.LoanStatusDeclined},
		{entities.LoanStatusApproved, entities.LoanStatusDisbursed},
		{entities.LoanStatusDisbursed, entities.LoanStatusRepaid},
		{entities.LoanStatusDisbursed, entities.LoanStatusDefaulted},
	}
	for _, c := range ok {
		if !entities.CanTransitionLoan(c.from, c.to) {
			t.Fatalf("expected %s → %s to be allowed", c.from, c.to)
		}
	}
	bad := []struct{ from, to entities.LoanStatus }{
		{entities.LoanStatusPending, entities.LoanStatusRepaid},
		{entities.LoanStatusPending, entities.LoanStatusDisbursed},
		{entities.LoanStatusDeclined, entities.LoanStatusApproved},
		{entities.LoanStatusRepaid, entities.LoanStatusDisbursed},
		{entities.LoanStatusApproved, entities.LoanStatusRepaid},
	}
	for _, c := range bad {
		if entities.CanTransitionLoan(c.from, c.to) {
			t.Fatalf("expected %s → %s to be rejected", c.from, c.to)
		}
	}
}

func TestRepaymentBalanceDue(t *testing.T) {
	p := &entities.Repayment{
		Amount:     decimal.NewFromInt(10000),
		LateFee:    decimal.NewFromInt(500),
		AmountPaid: decimal.NewFromInt(4000),
	}
	if !p.BalanceDue().Equal(decimal.NewFromInt(6500)) {
		t.Fatalf("balance = %s, want 6500", p.BalanceDue())
	}
	p.AmountPaid = decimal.NewFromInt(20000) // over-application guard
	if !p.BalanceDue().IsZero() {
		t.Fatalf("balance must floor at zero, got %s", p.BalanceDue())
	}
}

func TestServerSidePricingBands(t *testing.T) {
	if !PricedRate(760).Equal(decimal.NewFromFloat(12.0)) {
		t.Fatal("top band must price at 12%")
	}
	if !PricedRate(500).Equal(decimal.NewFromFloat(30.0)) {
		t.Fatal("bottom band must price at 30%")
	}
}
