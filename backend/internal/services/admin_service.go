package services

import (
	"context"
	"errors"
	"lending-app/backend/internal/domain/entities"
	"lending-app/backend/internal/repositories"

	"github.com/google/uuid"
)

type AdminService struct {
	loanRepo       *repositories.LoanRepository
	userRepo       *repositories.UserRepository
	collectionRepo *repositories.CollectionRepository
}

func NewAdminService(lr *repositories.LoanRepository, ur *repositories.UserRepository, cr *repositories.CollectionRepository) *AdminService {
	return &AdminService{loanRepo: lr, userRepo: ur, collectionRepo: cr}
}

type DashboardStats struct {
	TotalUsers        int     `json:"totalUsers"`
	TotalLoans        int     `json:"totalLoans"`
	PendingLoans      int     `json:"pendingLoans"`
	ApprovedLoans     int     `json:"approvedLoans"`
	DisbursedLoans    int     `json:"disbursedLoans"`
	DefaultedLoans    int     `json:"defaultedLoans"`
	ActiveCollections int     `json:"activeCollections"`
	TotalDisbursedAmt string  `json:"totalDisbursedAmount"`
	DefaultRate       float64 `json:"defaultRate"`
}

func (s *AdminService) GetDashboardStats(ctx context.Context) (*DashboardStats, error) {
	users, totalUsers, err := s.userRepo.List(ctx, 1, 0)
	if err != nil {
		return nil, err
	}
	_ = users

	_, totalLoans, err := s.loanRepo.List(ctx, "", 1, 0)
	if err != nil {
		return nil, err
	}
	_, pending, _ := s.loanRepo.List(ctx, "pending", 1, 0)
	_, approved, _ := s.loanRepo.List(ctx, "approved", 1, 0)
	_, disbursed, _ := s.loanRepo.List(ctx, "disbursed", 1, 0)
	_, defaulted, _ := s.loanRepo.List(ctx, "defaulted", 1, 0)

	activeColls, _, _ := s.collectionRepo.List(ctx, nil, "active", 1, 0)

	stats := &DashboardStats{
		TotalUsers:        totalUsers,
		TotalLoans:        totalLoans,
		PendingLoans:      pending,
		ApprovedLoans:     approved,
		DisbursedLoans:    disbursed,
		DefaultedLoans:    defaulted,
		ActiveCollections: len(activeColls),
	}
	if totalLoans > 0 {
		stats.DefaultRate = float64(defaulted) / float64(totalLoans)
	}
	return stats, nil
}

func (s *AdminService) ListUsers(ctx context.Context, page, pageSize int) ([]*entities.User, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	return s.userRepo.List(ctx, pageSize, (page-1)*pageSize)
}

func (s *AdminService) UpdateUserStatus(ctx context.Context, id uuid.UUID, active bool) error {
	return s.userRepo.UpdateStatus(ctx, id, active)
}

func (s *AdminService) UpdateUserRole(ctx context.Context, id uuid.UUID, role string) error {
	if !entities.ValidRoles[role] {
		return errors.New("invalid role (must be user, agent, or admin)")
	}
	if role == "admin" {
		// Privilege grants should require a second control in production
		// (approval ticket / MFA). Enforced at the handler via audit.
	}
	return s.userRepo.UpdateRole(ctx, id, role)
}

func (s *AdminService) UpdateKYCStatus(ctx context.Context, id uuid.UUID, status string) error {
	if !entities.ValidKYCStatuses[status] {
		return errors.New("invalid kyc status")
	}
	if status == "pending" {
		return errors.New("cannot revert kyc to pending")
	}
	return s.userRepo.UpdateKYCStatus(ctx, id, status)
}
