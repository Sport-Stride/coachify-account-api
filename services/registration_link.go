package services

import (
	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
	"coachify-account-api/utils"
	"context"
	"net/http"

	"go.uber.org/zap"
)

type RegistrationLinkService interface {
	GetOrCreateLink(ctx context.Context, coachID string) (*db.RegistrationLink, *models.ApiError)
	ValidateToken(ctx context.Context, token string) (*db.RegistrationLink, string, *models.ApiError)
}

type RegistrationLinkServiceImpl struct {
	linkRepo *repositories.RegistrationLinkRepository
	userRepo *repositories.UserRepository
}

func NewRegistrationLinkService(
	linkRepo *repositories.RegistrationLinkRepository,
	userRepo *repositories.UserRepository,
) *RegistrationLinkServiceImpl {
	return &RegistrationLinkServiceImpl{
		linkRepo: linkRepo,
		userRepo: userRepo,
	}
}

// GetOrCreateLink returns the existing registration link for a coach or creates a new one.
func (s *RegistrationLinkServiceImpl) GetOrCreateLink(ctx context.Context, coachID string) (*db.RegistrationLink, *models.ApiError) {
	existing, err := s.linkRepo.GetByCoachID(ctx, coachID)
	if err != nil {
		utils.Logger.Error("failed to get registration link by coach ID", zap.Error(err))
		return nil, &models.ApiError{Code: http.StatusInternalServerError, Error: models.ErrInternalError}
	}
	if existing != nil {
		return existing, nil
	}

	token, apiErr := utils.GenerateRandomString(32)
	if apiErr != nil {
		return nil, apiErr
	}

	link := &db.RegistrationLink{
		Token:   token,
		CoachID: coachID,
	}

	if err := s.linkRepo.Create(ctx, link); err != nil {
		utils.Logger.Error("failed to create registration link", zap.Error(err))
		return nil, &models.ApiError{Code: http.StatusInternalServerError, Error: models.ErrInternalError}
	}

	return link, nil
}

// ValidateToken validates a registration link token and returns the link and coach name.
func (s *RegistrationLinkServiceImpl) ValidateToken(ctx context.Context, token string) (*db.RegistrationLink, string, *models.ApiError) {
	link, err := s.linkRepo.GetByToken(ctx, token)
	if err != nil {
		utils.Logger.Error("failed to get registration link by token", zap.Error(err))
		return nil, "", &models.ApiError{Code: http.StatusInternalServerError, Error: models.ErrInternalError}
	}
	if link == nil {
		return nil, "", &models.ApiError{Code: http.StatusNotFound, Error: models.ErrRegistrationLinkNotFound}
	}

	coachName, apiErr := s.userRepo.GetUserNameByExternalID(ctx, link.CoachID)
	if apiErr != nil {
		return nil, "", apiErr
	}

	return link, coachName, nil
}
