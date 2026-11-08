package services

import (
	"context"
	"net/http"
	"time"

	"go.uber.org/zap"

	"coachify-account-api/core"
	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
	"coachify-account-api/models/mapping"
	"coachify-account-api/pkg/identifier"
)

// invitedOwnerStore is the user persistence invited gym owner signup and OAuth login need; *repositories.UserRepository satisfies it.
type invitedOwnerStore interface {
	EmailExists(ctx context.Context, email string) (bool, error)
	CreateUser(ctx context.Context, dbUser *db.User) (*db.UserResponse, *models.ApiError)
	GetByEmailCheck(ctx context.Context, email string) (*db.User, *models.ApiError)
}

// ownerIDGenerator issues user ids; *identifier.IdentifierClient satisfies it.
type ownerIDGenerator interface {
	GenerateId(ctx context.Context, label string) (identifier.Entity, *models.ApiError)
}

func (s *AuthServiceImpl) ownerStore() invitedOwnerStore {
	if s.ownerUsers != nil {
		return s.ownerUsers
	}
	return &s.userRepository
}

func (s *AuthServiceImpl) ownerIDGen() ownerIDGenerator {
	if s.ownerIDs != nil {
		return s.ownerIDs
	}
	return s.identifier
}

// markInviteAccepted consumes the invite after the account exists; a failure is logged because the account is already created.
func (s *AuthServiceImpl) markInviteAccepted(ctx context.Context, inviteID, userID string) {
	if apiErr := s.gymOwnerInvites.MarkAccepted(ctx, inviteID, userID); apiErr != nil {
		zap.L().Error("gym owner invite: mark accepted failed after signup",
			zap.String("component", "auth"),
			zap.String("invite_id", inviteID),
			zap.String("user_id", userID))
	}
}

// registerInvitedGymOwner creates an Active, verified gym_owner from an admin invite: no confirmation code,
// no trial subscription and no invitation-service calls.
func (s AuthServiceImpl) registerInvitedGymOwner(ctx context.Context, req *api.CreateUserRequest) (*api.RegisterResponse, *models.ApiError) {
	if s.gymOwnerInvites == nil {
		return nil, inviteNotFound()
	}
	inv, apiErr := s.gymOwnerInvites.Redeem(ctx, req.GymOwnerInviteToken, req.Email)
	if apiErr != nil {
		return nil, apiErr
	}
	exists, err := s.ownerStore().EmailExists(ctx, inv.Email)
	if err != nil {
		zap.L().Error("invited gym owner signup: email check failed", zap.String("component", "auth"), zap.Error(err))
		return nil, &models.ApiError{Code: http.StatusInternalServerError, Error: models.ErrInternalError}
	}
	if exists {
		return nil, inviteNotUsable()
	}
	if !core.ValidatePassword(req.Password) {
		return nil, &models.ApiError{Code: http.StatusBadRequest, Error: models.ErrInvalidPassword}
	}
	hash, apiErr := s.passwordChecker.HashPassword(req.Password)
	if apiErr != nil {
		return nil, apiErr
	}
	firstname, lastname := req.FirstName, req.LastName
	if firstname == "" {
		firstname = inv.Firstname
	}
	if lastname == "" {
		lastname = inv.Lastname
	}
	created, apiErr := s.insertGymOwner(ctx, firstname, lastname, inv.Email, hash)
	if apiErr != nil {
		return nil, apiErr
	}
	s.markInviteAccepted(ctx, inv.ID.Hex(), created.ExternalID)
	return signupResponse(created), nil
}

// createInvitedGymOwnerOAuth creates an Active gym_owner from a verified Google identity and an admin invite.
func (s *AuthServiceImpl) createInvitedGymOwnerOAuth(ctx context.Context, oauthUser db.GoogleLoginRequest) (*api.OAuthResponse, *models.ApiError) {
	if s.gymOwnerInvites == nil {
		return nil, inviteNotFound()
	}
	inv, apiErr := s.gymOwnerInvites.Redeem(ctx, oauthUser.GymOwnerInviteToken, oauthUser.Profile.Email)
	if apiErr != nil {
		return nil, apiErr
	}
	id, apiErr := s.ownerIDGen().GenerateId(ctx, "user")
	if apiErr != nil {
		return nil, apiErr
	}

	newUser := mapping.ToDbUserFromGoogleProfile(oauthUser, id.Code)
	newUser.UserLastLogin = time.Now()
	if newUser.UserFirstname == "" {
		newUser.UserFirstname = inv.Firstname
	}
	if newUser.UserLastname == "" {
		newUser.UserLastname = inv.Lastname
	}
	newUser.UserRole = db.RoleGymOwner
	newUser.UserStatus = db.Active

	accessToken, refreshToken, apiErr := s.generateTokens(newUser)
	if apiErr != nil {
		return nil, apiErr
	}
	newUser.Token = &accessToken
	newUser.UserRefreshToken = &refreshToken

	if _, apiErr := s.ownerStore().CreateUser(ctx, newUser); apiErr != nil {
		return nil, apiErr
	}
	s.markInviteAccepted(ctx, inv.ID.Hex(), newUser.ExternalID)

	if s.notificationClient != nil {
		if err := s.notificationClient.SendWelcomeEmail(ctx, newUser); err != nil {
			zap.L().Warn("failed to send welcome email",
				zap.String("component", "auth"),
				zap.Error(err),
			)
		}
	}
	return &api.OAuthResponse{User: mapping.ToApiUser(newUser)}, nil
}
