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

// CoachGymStore is the gym leg of the coach client reads, enabled by CLIENT_GYM_RELATIONS.
type CoachGymStore interface {
	ListCoachGymClients(ctx context.Context, query db.CoachClientListQuery) ([]map[string]interface{}, int, error)
	LinkedClientIDsWithGym(ctx context.Context, coachID string, clientIDs []string) ([]string, error)
}

var _ CoachGymStore = (*repositories.CoachRepository)(nil)

type CoachServiceImpl struct {
	coachRepo          *repositories.CoachRepository
	links              CoachLinkStore
	gym                CoachGymStore // nil while CLIENT_GYM_RELATIONS is off
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
// Client list kinds of GET /coach/clients. The empty kind is the legacy personal list, unchanged.
const (
	ClientKindPersonal = "personal"
	ClientKindGym      = "gym"
)

// IsValidClientKind reports whether kind is accepted by the client list ("" is the default).
func IsValidClientKind(kind string) bool {
	return kind == "" || kind == ClientKindPersonal || kind == ClientKindGym
}

// WithGymRelations enables the gym leg (kind filter and link check). Nil keeps the legacy behavior.
func (s *CoachServiceImpl) WithGymRelations(g CoachGymStore) *CoachServiceImpl {
	s.gym = g
	return s
}

// Update CoachServiceImpl to match new return type. Kind "" keeps the legacy personal list; with the gym relations
// off, every other kind answers an empty list and the personal list stays untouched.
func (s *CoachServiceImpl) ListCoachClients(ctx context.Context, query db.CoachClientListQuery) ([]map[string]interface{}, int, error) {
	// kind "" stays the personal-only list (unchanged legacy behavior, not a merge of personal and gym); gym clients
	// are reached with kind=gym. With the gym relations off, kind=gym is a valid request that answers 200 and an empty list.
	if query.Kind == "" || s.gym == nil && query.Kind == ClientKindPersonal {
		return s.coachRepo.ListCoachClients(ctx, query)
	}
	if s.gym == nil {
		return []map[string]interface{}{}, 0, nil
	}
	if query.Kind == ClientKindGym {
		return s.gym.ListCoachGymClients(ctx, query)
	}
	rows, total, err := s.coachRepo.ListCoachClients(ctx, query)
	if err != nil {
		return nil, 0, err
	}
	for _, row := range rows {
		row["relationships"] = []map[string]string{{"kind": ClientKindPersonal}}
	}
	return rows, total, nil
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
	linkedIDs := s.links.LinkedClientIDs
	if s.gym != nil {
		// One $unionWith aggregation: personal coach_clients plus the gym clients assigned to the coach.
		linkedIDs = s.gym.LinkedClientIDsWithGym
	}
	linked, err := linkedIDs(ctx, coachID, clientIDs)
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
