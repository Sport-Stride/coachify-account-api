package services

import (
	"coachify-account-api/core"
	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/pkg/identifier"
	"coachify-account-api/pkg/notification"
	"coachify-account-api/repositories"
	"coachify-account-api/utils"
	"context"
	"errors"
	"net/http"

	"go.uber.org/zap"
)

// services/coach_service.go
// Update CoachService interface to match new enriched return type
// ListCoachClients returns enriched client details as []map[string]interface{}
type CoachService interface {
	ListCoachClients(ctx context.Context, query db.CoachClientListQuery) ([]map[string]interface{}, int, error)
	ListCoachClientsSummary(ctx context.Context, coachID string, page, size int) ([]db.CoachClientSummary, int, error)
	DissociateCoachClient(ctx context.Context, coachID, clientID string) error
	AddCoachClient(ctx context.Context, coachID, clientID string) error
	GetCoachIDByClientID(ctx context.Context, clientID string) (string, error)
	IsClientOfCoach(ctx context.Context, coachID, clientID string) (bool, error)
	CheckClientLinks(ctx context.Context, coachID string, clientIDs []string) (map[string]bool, *models.ApiError)
}

// CoachLinkStore is the persistence seam for bulk coach-client link lookups.
type CoachLinkStore interface {
	LinkedClientIDs(ctx context.Context, coachID string, clientIDs []string) ([]string, error)
}

type CoachServiceImpl struct {
	coachRepo          *repositories.CoachRepository
	links              CoachLinkStore
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
		links:     cr,

		notificationClient: nc,
		identifier:         id,
		activationManager:  ac,
		baseURL:            baseURL,
	}
}

// Update CoachServiceImpl to match new return type
func (s *CoachServiceImpl) ListCoachClients(ctx context.Context, query db.CoachClientListQuery) ([]map[string]interface{}, int, error) {
	return s.coachRepo.ListCoachClients(ctx, query)
}

func (s *CoachServiceImpl) ListCoachClientsSummary(ctx context.Context, coachID string, page, size int) ([]db.CoachClientSummary, int, error) {
	return s.coachRepo.ListCoachClientsSummary(ctx, coachID, page, size)
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

// WithLinkStore replaces the link lookup store (test seam).
func (s *CoachServiceImpl) WithLinkStore(l CoachLinkStore) *CoachServiceImpl {
	s.links = l
	return s
}

// CheckClientLinks returns, for every requested id, whether it is linked to coachID.
func (s *CoachServiceImpl) CheckClientLinks(ctx context.Context, coachID string, clientIDs []string) (map[string]bool, *models.ApiError) {
	if coachID == "" {
		return nil, models.NewApiError(http.StatusBadRequest, errors.New("coach id is required"))
	}
	linked, err := s.links.LinkedClientIDs(ctx, coachID, clientIDs)
	if err != nil {
		utils.Logger.Error("failed to check coach client links", zap.Error(err))
		return nil, models.NewApiError(http.StatusInternalServerError, models.ErrInternalError)
	}
	links := make(map[string]bool, len(clientIDs))
	for _, id := range clientIDs {
		links[id] = false
	}
	for _, id := range linked {
		if _, requested := links[id]; requested {
			links[id] = true
		}
	}
	return links, nil
}
