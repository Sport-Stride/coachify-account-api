package services

import (
	"coachify-account-api/core"
	"coachify-account-api/models/db"
	"coachify-account-api/pkg/identifier"
	"coachify-account-api/pkg/notification"
	"coachify-account-api/repositories"
	"context"
	"encoding/json"
	"log"
)

// services/coach_service.go
// Update CoachService interface to match new enriched return type
// ListCoachClients returns enriched client details as []map[string]interface{}
type CoachService interface {
	ListCoachClients(ctx context.Context, query db.CoachClientListQuery) ([]map[string]interface{}, int, error)
	DissociateCoachClient(ctx context.Context, coachID, clientID string) error
	AddCoachClient(ctx context.Context, coachID, clientID string) error
	GetCoachIDByClientID(ctx context.Context, clientID string) (string, error)
	IsClientOfCoach(ctx context.Context, coachID, clientID string) (bool, error)
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

// Update CoachServiceImpl to match new return type
func (s *CoachServiceImpl) ListCoachClients(ctx context.Context, query db.CoachClientListQuery) ([]map[string]interface{}, int, error) {
	clients, total, err := s.coachRepo.ListCoachClients(ctx, query)
	if err != nil {
		return nil, 0, err
	}
	if raw, jsonErr := json.Marshal(clients); jsonErr == nil {
		log.Printf("ListCoachClients service raw result: %s", string(raw))
	}
	return clients, total, nil
}

func (s *CoachServiceImpl) DissociateCoachClient(ctx context.Context, coachID, clientID string) error {
	return s.coachRepo.DissociateCoachClient(ctx, coachID, clientID)
}
func (s *CoachServiceImpl) AddCoachClient(ctx context.Context, coachID, clientID string) error {
	return s.coachRepo.AddCoachClient(ctx, coachID, clientID)
}
func (s *CoachServiceImpl) GetCoachIDByClientID(ctx context.Context, clientID string) (string, error) {
	return s.coachRepo.GetCoachIDByClientID(ctx, clientID)
}

func (s *CoachServiceImpl) IsClientOfCoach(ctx context.Context, coachID, clientID string) (bool, error) {
	return s.coachRepo.IsClientOfCoach(ctx, coachID, clientID)
}
