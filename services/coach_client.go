package services

import (
	"coachify-account-api/core"
	"coachify-account-api/models/db"
	"coachify-account-api/pkg/identifier"
	"coachify-account-api/pkg/notification"
	"coachify-account-api/repositories"
	"context"
)

// services/coach_service.go
type CoachService interface {
	ListCoachClients(ctx context.Context, query db.CoachClientListQuery) ([]db.CoachClient, int, error)
	DissociateCoachClient(ctx context.Context, coachID, clientID string) error
	AddCoachClient(ctx context.Context, coachID, clientID string) error
}

type CoachServiceImpl struct {
	coachRepo          *repositories.CoachRepository
	notificationClient *notification.NotificationClient
	identifier         *identifier.IdentifierClient
	activationManager  core.ActivationManager
	baseURL            string
}

func NewCoachService(
	cr *repositories.CoachRepository,

	nc *notification.NotificationClient,
	id *identifier.IdentifierClient,
	ac core.ActivationManager,
	baseURL string) *CoachServiceImpl {
	return &CoachServiceImpl{
		coachRepo: cr,

		notificationClient: nc,
		identifier:         id,
		activationManager:  ac,
		baseURL:            baseURL,
	}
}

func (s *CoachServiceImpl) ListCoachClients(ctx context.Context, query db.CoachClientListQuery) ([]db.CoachClient, int, error) {
	return s.coachRepo.ListCoachClients(ctx, query)
}

func (s *CoachServiceImpl) DissociateCoachClient(ctx context.Context, coachID, clientID string) error {
	return s.coachRepo.DissociateCoachClient(ctx, coachID, clientID)
}
func (s *CoachServiceImpl) AddCoachClient(ctx context.Context, coachID, clientID string) error {
	return s.coachRepo.AddCoachClient(ctx, coachID, clientID)
}
