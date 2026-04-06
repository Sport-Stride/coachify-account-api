package services

import (
	"context"
	"time"

	"coachify-account-api/repositories"
)

// AdminCoachRow is the coach record returned by the admin list endpoint.
// Subscription data is NOT included here; the frontend fetches it directly
// from payments-api to avoid synchronous cross-service coupling.
type AdminCoachRow struct {
	ExternalID     string    `json:"external_id"`
	Firstname      string    `json:"firstname"`
	Lastname       string    `json:"lastname"`
	Email          string    `json:"email"`
	Status         string    `json:"status"`
	ProfilePicture string    `json:"profile_picture,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// AdminClientRow is the client record returned by the admin coach-clients endpoint.
type AdminClientRow struct {
	ExternalID     string    `json:"external_id"`
	Firstname      string    `json:"firstname"`
	Lastname       string    `json:"lastname"`
	Email          string    `json:"email"`
	Status         string    `json:"status"`
	ProfilePicture string    `json:"profile_picture,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// AdminService exposes read-only admin views over coaches and their clients.
type AdminService interface {
	ListCoaches(ctx context.Context, page, size int) ([]AdminCoachRow, int, error)
	ListCoachClients(ctx context.Context, coachID string) ([]AdminClientRow, error)
}

type adminServiceImpl struct {
	userRepo  *repositories.UserRepository
	coachRepo *repositories.CoachRepository
}

func NewAdminService(
	userRepo *repositories.UserRepository,
	coachRepo *repositories.CoachRepository,
) AdminService {
	return &adminServiceImpl{
		userRepo:  userRepo,
		coachRepo: coachRepo,
	}
}

func (s *adminServiceImpl) ListCoaches(ctx context.Context, page, size int) ([]AdminCoachRow, int, error) {
	users, total, err := s.userRepo.GetCoachesByPage(ctx, page, size)
	if err != nil {
		return nil, 0, err
	}

	rows := make([]AdminCoachRow, 0, len(users))
	for _, u := range users {
		rows = append(rows, AdminCoachRow{
			ExternalID:     u.ExternalID,
			Firstname:      u.Firstname,
			Lastname:       u.Lastname,
			Email:          u.Email,
			Status:         u.Status,
			ProfilePicture: u.ProfilePicture,
			UpdatedAt:      u.UpdatedAt,
		})
	}
	return rows, total, nil
}

func (s *adminServiceImpl) ListCoachClients(ctx context.Context, coachID string) ([]AdminClientRow, error) {
	clientIDs, err := s.coachRepo.GetClientIDsByCoach(ctx, coachID)
	if err != nil {
		return nil, err
	}
	if len(clientIDs) == 0 {
		return []AdminClientRow{}, nil
	}

	users, err := s.userRepo.GetClientsByExternalIDs(ctx, clientIDs)
	if err != nil {
		return nil, err
	}

	rows := make([]AdminClientRow, 0, len(users))
	for _, u := range users {
		rows = append(rows, AdminClientRow{
			ExternalID:     u.ExternalID,
			Firstname:      u.Firstname,
			Lastname:       u.Lastname,
			Email:          u.Email,
			Status:         u.Status,
			ProfilePicture: u.ProfilePicture,
			UpdatedAt:      u.UpdatedAt,
		})
	}
	return rows, nil
}
