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
	"time"

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

// CoachListStore is the persistence seam for the personal client list.
type CoachListStore interface {
	ListCoachClients(ctx context.Context, query db.CoachClientListQuery) ([]map[string]interface{}, int, error)
}

// CoachGymStore is the gym leg of the coach client reads, enabled by CLIENT_GYM_RELATIONS.
type CoachGymStore interface {
	ListCoachGymClients(ctx context.Context, query db.CoachClientListQuery) ([]map[string]interface{}, int, error)
	LinkedClientIDsWithGym(ctx context.Context, coachID string, clientIDs []string) ([]string, error)
	// ActiveGymClientsByClients returns the active gym_clients rows of the clients, whoever they are assigned to
	// (one query); the service keeps only the rows visible to the caller.
	ActiveGymClientsByClients(ctx context.Context, clientIDs []string) ([]db.GymClient, error)
}

var _ CoachGymStore = (*repositories.CoachRepository)(nil)

type CoachServiceImpl struct {
	coachRepo          *repositories.CoachRepository
	links              CoachLinkStore
	list               CoachListStore
	gym                CoachGymStore // nil while CLIENT_GYM_RELATIONS is off
	members            GymMembershipReader
	premium            PremiumAnnotator // nil: no premium fields
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
		list:      cr,

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

// WithGymRelations enables the gym leg (kind filter, link check and relationships). Nil keeps the legacy behavior.
// members resolves the caller's gym roles for the visibility of gym rows; nil leaves the gym relationships out.
func (s *CoachServiceImpl) WithGymRelations(g CoachGymStore, members GymMembershipReader) *CoachServiceImpl {
	s.gym = g
	s.members = members
	return s
}

// Update CoachServiceImpl to match new return type. Kind "" and "personal" list the personal clients, kind=gym the gym
// clients (an empty list while the gym relations are off). kind "" is not a merge of personal and gym. Every row
// carries relationships, one entry per link of the calling coach ({"kind":"personal"}, {"kind":"gym","gym_id":...}),
// resolved with one batched lookup per page.
func (s *CoachServiceImpl) ListCoachClients(ctx context.Context, query db.CoachClientListQuery) ([]map[string]interface{}, int, error) {
	if s.gym == nil && query.Kind != "" && query.Kind != ClientKindPersonal {
		return []map[string]interface{}{}, 0, nil
	}
	var rows []map[string]interface{}
	var total int
	var err error
	if query.Kind == ClientKindGym {
		rows, total, err = s.gym.ListCoachGymClients(ctx, query)
	} else {
		if s.list == nil {
			return nil, 0, errors.New("coach client list store is not configured")
		}
		rows, total, err = s.list.ListCoachClients(ctx, query)
	}
	if err != nil {
		return nil, 0, err
	}
	// With the gym relations off the payload stays exactly the legacy one (no relationships key).
	if s.gym == nil {
		return rows, total, nil
	}
	if err := s.annotateRelationships(ctx, query, rows); err != nil {
		return nil, 0, err
	}
	if query.Kind == ClientKindGym {
		s.annotatePremium(ctx, rows)
	}
	return rows, total, nil
}

// WithPremium marks the gym rows of the coach client list with the live coaching add-ons of their clients; nil skips it.
func (s *CoachServiceImpl) WithPremium(p PremiumAnnotator) *CoachServiceImpl {
	s.premium = p
	return s
}

// annotatePremium sets row["premium"], row["premium_since"] and row["premium_addons"] on the gym rows whose client
// holds a live coaching add-on: one lookup per gym of the page, fail-open (the fields are then left out).
func (s *CoachServiceImpl) annotatePremium(ctx context.Context, rows []map[string]interface{}) {
	if s.premium == nil || len(rows) == 0 {
		return
	}
	byGym := map[string][]string{}
	for _, row := range rows {
		gymID, _ := row["gym_id"].(string)
		id, _ := row["externalid"].(string)
		if gymID != "" && id != "" {
			byGym[gymID] = append(byGym[gymID], id)
		}
	}
	info := map[string]map[string]PremiumInfo{}
	for gymID, ids := range byGym {
		info[gymID] = s.premium.Lookup(ctx, gymID, ids)
	}
	for _, row := range rows {
		gymID, _ := row["gym_id"].(string)
		id, _ := row["externalid"].(string)
		pi, ok := info[gymID][id]
		if !ok {
			continue
		}
		row["premium"] = true
		if pi.Since != nil {
			row["premium_since"] = pi.Since
		}
		row["premium_addons"] = pi.Addons
	}
}

// annotateRelationships sets row["relationships"] for the page. Personal rows are personal by construction; gym rows
// need one coach_clients lookup. With the gym relations on, one gym_clients lookup and one membership read give the
// gym entries, limited to the rows the coach may see as a gym relation (visibleGymRelations), so other coaches'
// links never show.
func (s *CoachServiceImpl) annotateRelationships(ctx context.Context, query db.CoachClientListQuery, rows []map[string]interface{}) error {
	if len(rows) == 0 {
		return nil
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if id, ok := row["externalid"].(string); ok && id != "" {
			ids = append(ids, id)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	personal := map[string]bool{}
	if query.Kind == ClientKindGym {
		if s.links != nil && len(ids) > 0 {
			linked, err := s.links.LinkedClientIDs(ctx, query.CoachID, ids)
			if err != nil {
				return err
			}
			for _, id := range linked {
				personal[id] = true
			}
		}
	} else {
		for _, id := range ids {
			personal[id] = true
		}
	}

	// gyms: visible gym ids per client, sorted by gym id (visibleGymRows order).
	gyms := map[string][]string{}
	if s.members != nil && len(ids) > 0 {
		gymRows, err := s.gym.ActiveGymClientsByClients(ctx, ids)
		if err != nil {
			return err
		}
		if len(gymRows) > 0 {
			memberships, err := s.members.MembershipsOf(ctx, query.CoachID)
			if err != nil {
				// The roles are only needed for the gym entries: degrade to the personal ones, do not fail the list.
				zap.L().Error("coach client list: gym memberships unavailable", zap.String("component", "coach_client"), zap.Error(err))
			} else {
				for _, v := range visibleGymRows(query.CoachID, gymRows, memberships) {
					gyms[v.Row.ClientID] = append(gyms[v.Row.ClientID], v.Row.GymID)
				}
			}
		}
	}

	for _, row := range rows {
		id, _ := row["externalid"].(string)
		rels := []map[string]string{}
		seen := map[string]bool{}
		// A gym list row keeps its own gym entry first, as the list already reported it; the other gym entries
		// follow sorted by gym_id.
		if gymID, ok := row["gym_id"].(string); ok && query.Kind == ClientKindGym && gymID != "" {
			rels = append(rels, map[string]string{"kind": ClientKindGym, "gym_id": gymID})
			seen[gymID] = true
		}
		if personal[id] {
			rels = append(rels, map[string]string{"kind": ClientKindPersonal})
		}
		for _, gymID := range gyms[id] {
			if !seen[gymID] {
				seen[gymID] = true
				rels = append(rels, map[string]string{"kind": ClientKindGym, "gym_id": gymID})
			}
		}
		row["relationships"] = rels
	}
	return nil
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

// WithListStore replaces the personal list store (test seam).
func (s *CoachServiceImpl) WithListStore(l CoachListStore) *CoachServiceImpl {
	s.list = l
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
