package services

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
)

const (
	// maxAddonResolveCoaches caps the coach ids of one resolve call.
	maxAddonResolveCoaches = 50
	// addonPurchasedType is the data.type of the in-app notification of a coaching add-on purchase.
	addonPurchasedType = "gym_addon_purchased"
	// addonClientsURL is where the notification of a purchase leads (the clients tab of the gym dashboard).
	addonClientsURL = "/dashboards/gym-owner?tab=clients"
)

var (
	errAddonPurchase       = errors.New("gym_id, client_id, coach_id and subscription_id are required")
	errAddonTooManyCoaches = errors.New("at most 50 coach ids per request")
)

// GymAddonCoach is the answer of ResolveCoaches for one coach id. Eligible coaches are active owner, head coach or
// coach members of the gym that can receive clients (coaching role and, when the gym enforces seats, a seat).
type GymAddonCoach struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	ProfilePicture string `json:"profile_picture,omitempty"`
	Role           string `json:"role,omitempty"`
	Eligible       bool   `json:"eligible"`
}

// GymAddonPurchase is a paid (or free) coaching add-on purchase reported by payments-api.
type GymAddonPurchase struct {
	GymID          string
	ClientID       string
	CoachID        string
	PlanID         string
	PlanName       string
	SubscriptionID string
}

// GymAddonService backs the internal coaching add-on endpoints used by payments-api (GYM_MEMBERSHIP_PLANS_ENABLED).
type GymAddonService interface {
	// ResolveCoaches answers name, picture, role and eligibility of up to 50 coach ids in one gym.
	ResolveCoaches(ctx context.Context, gymID string, coachIDs []string) ([]GymAddonCoach, *models.ApiError)
	// OnPurchased assigns the chosen coach to the client and notifies the coach and the gym owner. Idempotent: a repeat
	// finds the coach already assigned. Assignment and notice failures are logged, never returned.
	OnPurchased(ctx context.Context, in GymAddonPurchase) *models.ApiError
}

// AddonAssigner assigns the purchased coach; *GymDispatch satisfies it.
type AddonAssigner interface {
	AssignByPurchase(ctx context.Context, gymID, clientID, coachID string) (*DispatchResult, *models.ApiError)
}

var _ AddonAssigner = (*GymDispatch)(nil)

// AddonNotifier creates an in-app notification; *tracker.TrackerClient satisfies it.
type AddonNotifier interface {
	Notify(ctx context.Context, userID, title, message, entityID, url string, data map[string]any) error
}

// GymAddonDeps are the collaborators of the add-on service. Assigner, Notifier, Mailer and Gyms are optional: a nil
// one skips that step.
type GymAddonDeps struct {
	Members  GymMembershipReader
	Seats    GymSeatStanding
	Users    AddonUsers
	Gyms     GymOwnerLookup
	Assigner AddonAssigner
	Notifier AddonNotifier
	Mailer   DispatchMailer
}

// AddonUsers is the user lookup of the add-on service; *repositories.UserRepository satisfies it.
type AddonUsers interface {
	AddonUserNames
	DispatchUsers
}

type gymAddonService struct{ d GymAddonDeps }

// NewGymAddonService builds the add-on service.
func NewGymAddonService(d GymAddonDeps) GymAddonService { return &gymAddonService{d: d} }

func (s *gymAddonService) ResolveCoaches(ctx context.Context, gymID string, coachIDs []string) ([]GymAddonCoach, *models.ApiError) {
	if gymID == "" {
		return nil, teamErr(http.StatusBadRequest, errGymNotFound)
	}
	ids := make([]string, 0, len(coachIDs))
	seen := map[string]bool{}
	for _, id := range coachIDs {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) > maxAddonResolveCoaches {
		return nil, teamErr(http.StatusBadRequest, errAddonTooManyCoaches)
	}
	out := make([]GymAddonCoach, 0, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	ctx, cancel := context.WithTimeout(ctx, authzTimeout)
	defer cancel()
	profiles := map[string]db.UserResponse{}
	if s.d.Users != nil {
		users, apiErr := s.d.Users.GetUsersByExternalIds(ctx, ids)
		if apiErr != nil {
			return nil, apiErr
		}
		for _, u := range users {
			profiles[u.ExternalID] = u
		}
	}
	for _, id := range ids {
		m, err := s.d.Members.MembershipOf(ctx, id, gymID)
		if err != nil {
			zap.L().Error("gym addon resolve failure", zap.String("component", "gym_addon"), zap.String("gym_id", gymID), zap.Error(err))
			return nil, teamErr(http.StatusInternalServerError, errInternal)
		}
		c := GymAddonCoach{ID: id}
		if u, ok := profiles[id]; ok {
			c.Name, c.ProfilePicture = strings.TrimSpace(u.Firstname+" "+u.Lastname), u.ProfilePicture
		}
		if m != nil {
			c.Role = string(m.Role)
		}
		c.Eligible = canCoach(m) && requireCoachSeatOf(ctx, s.d.Seats, gymID, id) == nil
		out = append(out, c)
	}
	return out, nil
}

func (s *gymAddonService) OnPurchased(ctx context.Context, in GymAddonPurchase) *models.ApiError {
	if in.GymID == "" || in.ClientID == "" || in.CoachID == "" || in.SubscriptionID == "" {
		return teamErr(http.StatusBadRequest, errAddonPurchase)
	}
	if s.d.Assigner != nil {
		// A refused assignment (seat 409, coach gone) never blocks the premium or the notices: the owner sees the
		// client in the pool and can assign manually.
		if _, apiErr := s.d.Assigner.AssignByPurchase(ctx, in.GymID, in.ClientID, in.CoachID); apiErr != nil {
			zap.L().Warn("gym addon purchase assignment failed", zap.String("component", "gym_addon"),
				zap.String("gym_id", in.GymID), zap.String("client_id", in.ClientID), zap.String("coach_id", in.CoachID),
				zap.Int("status", apiErr.Code), zap.Error(apiErr.Error))
		}
	}
	ownerID := ""
	if s.d.Gyms != nil {
		gctx, cancel := context.WithTimeout(ctx, authzTimeout)
		g, err := s.d.Gyms.GetGym(gctx, in.GymID)
		cancel()
		if err != nil || g == nil {
			zap.L().Warn("gym addon purchase: gym lookup failed", zap.String("component", "gym_addon"),
				zap.String("gym_id", in.GymID), zap.Error(err))
		} else {
			ownerID = g.OwnerID
		}
	}
	recipients := []string{in.CoachID}
	if ownerID != "" && ownerID != in.CoachID {
		recipients = append(recipients, ownerID)
	}
	for _, uid := range recipients {
		s.notify(ctx, uid, in)
	}
	return nil
}

// notify sends the in-app notification and the email of a purchase to one recipient; each failure is logged alone.
func (s *gymAddonService) notify(ctx context.Context, userID string, in GymAddonPurchase) {
	title := "New coaching add-on"
	message := "A client of your gym bought a coaching add-on on Coachify."
	if userID == in.CoachID {
		message = "A client of your gym bought a coaching add-on with you as coach on Coachify."
	}
	if in.PlanName != "" {
		message += " Plan: " + in.PlanName + "."
	}
	if s.d.Notifier != nil {
		nctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := s.d.Notifier.Notify(nctx, userID, title, message, in.SubscriptionID, addonClientsURL, map[string]any{
			"type": addonPurchasedType, "gym_id": in.GymID, "client_id": in.ClientID, "coach_id": in.CoachID, "plan_id": in.PlanID,
		})
		cancel()
		if err != nil {
			zap.L().Warn("gym addon in-app notification failed", zap.String("component", "gym_addon"), zap.Error(err))
		}
	}
	if s.d.Mailer == nil || s.d.Users == nil {
		return
	}
	ectx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	u, apiErr := s.d.Users.GetUserByExternalIdUpdate(ectx, userID)
	if apiErr != nil || u == nil || u.UserEmail == "" {
		return
	}
	if err := s.d.Mailer.SendGymAssignmentEmail(ectx, u.UserEmail, title, message); err != nil {
		zap.L().Warn("gym addon email failed", zap.String("component", "gym_addon"), zap.Error(err))
	}
}
