package services

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"time"

	"go.uber.org/zap"

	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
)

// MaxDevicesPerUser bounds the push tokens kept per user; the least recently registered go first.
const MaxDevicesPerUser = 10

// PushDeviceStore is the persistence seam; *repositories.PushDeviceRepository satisfies it.
type PushDeviceStore interface {
	Upsert(ctx context.Context, d db.PushDevice) error
	ListByUser(ctx context.Context, userID string, limit int64) ([]db.PushDevice, error)
	DeleteForUser(ctx context.Context, userID, token string) error
	TrimUser(ctx context.Context, userID string, keep int64) error
}

// PushDeviceService registers the Expo push tokens of signed-in apps and lists them for the senders.
type PushDeviceService interface {
	Register(ctx context.Context, userID string, req api.RegisterDeviceRequest) *models.ApiError
	Unregister(ctx context.Context, userID, token string) *models.ApiError
	List(ctx context.Context, userID string) ([]db.PushDevice, *models.ApiError)
}

type pushDeviceService struct {
	store PushDeviceStore
}

// NewPushDeviceService builds the push device service.
func NewPushDeviceService(store PushDeviceStore) PushDeviceService {
	return &pushDeviceService{store: store}
}

var (
	expoPushToken = regexp.MustCompile(`^Expo(nent)?PushToken\[[A-Za-z0-9_-]{10,200}\]$`)
	localeTag     = regexp.MustCompile(`^[A-Za-z]{2,3}([-_][A-Za-z0-9]{2,8}){0,3}$`)
	pushPlatforms = map[string]bool{"ios": true, "android": true}

	errPushToken    = &models.ApiError{Code: http.StatusBadRequest, Error: errors.New("invalid token: expected an Expo push token")}
	errPushPlatform = &models.ApiError{Code: http.StatusBadRequest, Error: errors.New("invalid platform: expected ios or android")}
	errPushLocale   = &models.ApiError{Code: http.StatusBadRequest, Error: errors.New("invalid locale")}
)

func pushStoreFailed(op string, err error) *models.ApiError {
	zap.L().Error("push devices: store failed", zap.String("component", "push_devices"), zap.String("op", op), zap.Error(err))
	return &models.ApiError{Code: http.StatusInternalServerError, Error: models.ErrInternalError}
}

// Register stores the token for the user and keeps at most MaxDevicesPerUser of their devices.
func (s *pushDeviceService) Register(ctx context.Context, userID string, req api.RegisterDeviceRequest) *models.ApiError {
	if !expoPushToken.MatchString(req.Token) {
		return errPushToken
	}
	if !pushPlatforms[req.Platform] {
		return errPushPlatform
	}
	if req.Locale != "" && !localeTag.MatchString(req.Locale) {
		return errPushLocale
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.store.Upsert(ctx, db.PushDevice{Token: req.Token, UserID: userID, Platform: req.Platform, Locale: req.Locale}); err != nil {
		return pushStoreFailed("upsert", err)
	}
	if err := s.store.TrimUser(ctx, userID, MaxDevicesPerUser); err != nil {
		// The token is registered; an old one lingering is harmless.
		zap.L().Warn("push devices: trim failed", zap.String("component", "push_devices"), zap.Error(err))
	}
	return nil
}

// Unregister removes the token if it is the user's; removing an unknown token is not an error.
func (s *pushDeviceService) Unregister(ctx context.Context, userID, token string) *models.ApiError {
	if !expoPushToken.MatchString(token) {
		return errPushToken
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.store.DeleteForUser(ctx, userID, token); err != nil {
		return pushStoreFailed("delete", err)
	}
	return nil
}

// List returns the user's devices, most recently registered first (for the notification senders).
func (s *pushDeviceService) List(ctx context.Context, userID string) ([]db.PushDevice, *models.ApiError) {
	if userID == "" {
		return nil, &models.ApiError{Code: http.StatusBadRequest, Error: errors.New("user id required")}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	devices, err := s.store.ListByUser(ctx, userID, MaxDevicesPerUser)
	if err != nil {
		return nil, pushStoreFailed("list", err)
	}
	return devices, nil
}
