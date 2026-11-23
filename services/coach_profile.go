package services

import (
	"context"
	"net/http"
	"time"

	"go.uber.org/zap"

	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
)

// CoachProfileService serves the public profile of a coach.
type CoachProfileService interface {
	GetPublicProfile(ctx context.Context, coachID string) (*api.CoachPublicProfile, *models.ApiError)
}

// CoachProfileUsers reads a user by external id; *repositories.UserRepository satisfies it.
type CoachProfileUsers interface {
	GetUserByExternalIdUpdate(ctx context.Context, userID string) (*db.User, *models.ApiError)
}

// CoachProfileClients counts the clients a coach follows; *repositories.CoachRepository satisfies it.
type CoachProfileClients interface {
	CountFollowedClients(ctx context.Context, coachID string, withGym bool) (int, error)
}

type coachProfileService struct {
	users   CoachProfileUsers
	clients CoachProfileClients
	withGym bool // CLIENT_GYM_RELATIONS: also count the gym clients assigned to the coach
}

// NewCoachProfileService builds the public coach profile service.
func NewCoachProfileService(users CoachProfileUsers, clients CoachProfileClients, withGym bool) CoachProfileService {
	return &coachProfileService{users: users, clients: clients, withGym: withGym}
}

var errPublicCoachNotFound = &models.ApiError{Code: http.StatusNotFound, Error: models.ErrCoachNotFound}

// GetPublicProfile returns the coach's public profile. Anyone who is not a coaching role, or whose account is
// inactive or unconfirmed, is reported as not found, so the route cannot be used to probe other accounts.
// A failed client count leaves clients out rather than failing the profile.
func (s *coachProfileService) GetPublicProfile(ctx context.Context, coachID string) (*api.CoachPublicProfile, *models.ApiError) {
	if coachID == "" {
		return nil, errPublicCoachNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	user, apiErr := s.users.GetUserByExternalIdUpdate(ctx, coachID)
	if apiErr != nil {
		if apiErr.Code == http.StatusNotFound {
			return nil, errPublicCoachNotFound
		}
		return nil, apiErr
	}
	if !db.IsMatriculeRole(user.UserRole) || user.UserStatus.IsInactive() || user.UserStatus == db.ToConfirm {
		return nil, errPublicCoachNotFound
	}

	profile := &api.CoachPublicProfile{
		ExternalID:     user.ExternalID,
		Firstname:      user.UserFirstname,
		Lastname:       user.UserLastname,
		Role:           user.UserRole,
		ProfilePicture: user.UserProfilePicture,
		Bio:            user.UserDescription,
		Specialties:    []string{},
	}
	if user.UserAddress.City != nil {
		profile.City = *user.UserAddress.City
	}
	if user.Metadata != nil && len(user.Metadata.Specialties) > 0 {
		profile.Specialties = user.Metadata.Specialties
	}

	if n, err := s.clients.CountFollowedClients(ctx, coachID, s.withGym); err != nil {
		zap.L().Error("coach public profile: client count unavailable",
			zap.String("component", "coach_profile"),
			zap.String("coach_id", coachID),
			zap.Error(err),
		)
	} else {
		profile.Clients = &n
	}
	return profile, nil
}
