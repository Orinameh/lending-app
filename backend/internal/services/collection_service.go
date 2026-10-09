package services

import (
	"context"
	"database/sql"
	"errors"
	"lending-app/backend/internal/domain/entities"
	"lending-app/backend/internal/repositories"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type CollectionService struct {
	collectionRepo *repositories.CollectionRepository
	loanRepo       *repositories.LoanRepository
	repaymentRepo  *repositories.RepaymentRepository
	repaymentSvc   *RepaymentService
	db             *sql.DB
}

func NewCollectionService(cr *repositories.CollectionRepository, lr *repositories.LoanRepository, rr *repositories.RepaymentRepository) *CollectionService {
	return &CollectionService{collectionRepo: cr, loanRepo: lr, repaymentRepo: rr}
}

func (s *CollectionService) WithDB(db *sql.DB) { s.db = db }

func (s *CollectionService) WithRepaymentSvc(rs *RepaymentService) {
	s.repaymentSvc = rs
}

var validCollectionStatuses = map[entities.CollectionStatus]bool{
	entities.CollectionStatusActive:     true,
	entities.CollectionStatusResolved:   true,
	entities.CollectionStatusLegal:      true,
	entities.CollectionStatusWrittenOff: true,
}

// dateDiffDays truncates to calendar dates (DST-safe).
func dateDiffDays(from, to time.Time) int {
	f := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
	t := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.UTC)
	d := int(t.Sub(f).Hours() / 24)
	if d < 0 {
		return 0
	}
	return d
}

// RunOverdueScan creates/updates Collection records from PER-INSTALLMENT
// delinquency (not loan maturity). For each disbursed/approved loan with an
// unpaid installment past due: accrue late fees, compute DPD from the earliest
// overdue due_date, and upsert the active collection for that loan.
func (s *CollectionService) RunOverdueScan(ctx context.Context) (int, error) {
	// Failover/concurrency: only one replica/scanner runs at a time.
	// Others skip (return 0) instead of queueing duplicate collections.
	if s.db != nil {
		scanCtx, cancel := repositories.WithTimeout(ctx)
		defer cancel()
		got, unlock, err := repositories.TryAdvisoryLock(scanCtx, s.db, repositories.LockOverdueScan)
		if err != nil {
			return 0, err
		}
		if !got {
			return 0, nil
		}
		defer unlock()
	}
	now := time.Now().UTC()
	loans, err := s.loanRepo.GetOverdueInstallments(ctx, now, 500)
	if err != nil {
		return 0, err
	}
	touched := 0
	for _, loan := range loans {
		if s.repaymentSvc != nil {
			_, _ = s.repaymentSvc.AccrueLateFees(ctx, loan.ID, now)
		}

		payments, err := s.repaymentRepo.GetByLoanID(ctx, loan.ID)
		if err != nil {
			continue
		}
		outstanding := decimal.Zero
		var earliest *time.Time
		for _, p := range payments {
			if p.Status == entities.RepaymentStatusPaid {
				continue
			}
			if !p.DueDate.Before(now) {
				continue
			}
			outstanding = outstanding.Add(p.BalanceDue())
			if earliest == nil || p.DueDate.Before(*earliest) {
				d := p.DueDate
				earliest = &d
			}
		}
		if outstanding.IsZero() || earliest == nil {
			continue
		}
		dpd := dateDiffDays(*earliest, now)

		existing, err := s.collectionRepo.GetActiveByLoanID(ctx, loan.ID)
		if err == nil && existing != nil {
			// Refresh stale snapshot.
			existing.AmountOutstanding = outstanding.Round(2)
			existing.DaysPastDue = dpd
			if err := s.collectionRepo.Update(ctx, existing); err != nil {
				continue
			}
			touched++
			continue
		}

		c := &entities.Collection{
			ID:                uuid.New(),
			LoanID:            loan.ID,
			UserID:            loan.UserID,
			AmountOutstanding: outstanding.Round(2),
			DaysPastDue:       dpd,
			Status:            entities.CollectionStatusActive,
			Notes:             "auto-generated from overdue scan",
			CreatedAt:         now,
			UpdatedAt:         now,
		}
		if err := s.collectionRepo.Create(ctx, c); err != nil {
			continue
		}
		touched++
	}
	return touched, nil
}

func (s *CollectionService) ListByUser(ctx context.Context, userID uuid.UUID) ([]*entities.Collection, error) {
	out, _, err := s.collectionRepo.List(ctx, &userID, "", 100, 0)
	return out, err
}

func (s *CollectionService) ListAll(ctx context.Context, status string, page, pageSize int) ([]*entities.Collection, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 10
	}
	if status != "" && !validCollectionStatuses[entities.CollectionStatus(status)] {
		return nil, 0, errors.New("invalid collection status")
	}
	return s.collectionRepo.List(ctx, nil, status, pageSize, (page-1)*pageSize)
}

func (s *CollectionService) UpdateStatus(ctx context.Context, id uuid.UUID, status entities.CollectionStatus, note string) error {
	if !validCollectionStatuses[status] {
		return errors.New("invalid collection status")
	}
	c, err := s.collectionRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	c.Status = status
	if note != "" {
		if len(note) > 2000 {
			return errors.New("note too long")
		}
		c.Notes = note
	}
	return s.collectionRepo.Update(ctx, c)
}

func (s *CollectionService) Assign(ctx context.Context, id, agentID uuid.UUID) error {
	c, err := s.collectionRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	c.AssignedTo = &agentID
	// Preserve history: record assignment as the latest contact event,
	// never wipe LastContactDate.
	c.LastContactDate = &now
	return s.collectionRepo.Update(ctx, c)
}

func (s *CollectionService) CreatePaymentArrangement(ctx context.Context, userID, collectionID uuid.UUID, note string) error {
	c, err := s.collectionRepo.GetByID(ctx, collectionID)
	if err != nil {
		return err
	}
	if c.UserID != userID {
		return errors.New("forbidden")
	}
	if c.Status != entities.CollectionStatusActive {
		return errors.New("collection is not active")
	}
	if len(note) > 2000 {
		return errors.New("note too long")
	}
	now := time.Now().UTC()
	next := now.Add(7 * 24 * time.Hour)
	c.LastContactDate = &now
	c.NextContactDate = &next
	c.Notes = note
	return s.collectionRepo.Update(ctx, c)
}
