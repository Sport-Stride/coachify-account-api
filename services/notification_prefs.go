package services

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata" // IANA zones even on images without /usr/share/zoneinfo

	"go.uber.org/zap"

	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
)

// MaxNotificationPrefsLookup bounds the users of one internal lookup.
const MaxNotificationPrefsLookup = 500

// NotificationPrefsStore is the persistence seam; *repositories.NotificationPrefsRepository satisfies it.
type NotificationPrefsStore interface {
	Get(ctx context.Context, userID string) (*db.NotificationPrefs, error)
	Patch(ctx context.Context, userID string, p db.NotificationPrefsPatch) (db.NotificationPrefs, error)
	ListByIDs(ctx context.Context, userIDs []string) ([]db.NotificationPrefs, error)
}

// NotificationPrefsService serves the caller's push preferences and the batch lookup of the senders.
type NotificationPrefsService interface {
	Get(ctx context.Context, userID string) (db.NotificationPrefs, *models.ApiError)
	Update(ctx context.Context, userID string, req api.UpdateNotificationPrefsRequest) (db.NotificationPrefs, *models.ApiError)
	Lookup(ctx context.Context, userIDs []string) (map[string]db.NotificationPrefs, *models.ApiError)
}

type notificationPrefsService struct {
	store NotificationPrefsStore
}

// NewNotificationPrefsService builds the notification preferences service.
func NewNotificationPrefsService(store NotificationPrefsStore) NotificationPrefsService {
	return &notificationPrefsService{store: store}
}

var (
	reminderClock = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
	prefsUserID   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

	errPrefsEmpty    = &models.ApiError{Code: http.StatusBadRequest, Error: errors.New("nothing to update")}
	errPrefsTime     = &models.ApiError{Code: http.StatusBadRequest, Error: errors.New("invalid reminder_time: expected HH:MM")}
	errPrefsTimezone = &models.ApiError{Code: http.StatusBadRequest, Error: errors.New("invalid timezone: expected an IANA time zone")}
	errPrefsUsers    = &models.ApiError{Code: http.StatusBadRequest, Error: errors.New("user_ids must hold 1 to 500 valid ids")}
)

func prefsStoreFailed(op string, err error) *models.ApiError {
	zap.L().Error("notification prefs: store failed", zap.String("component", "notification_prefs"), zap.String("op", op), zap.Error(err))
	return &models.ApiError{Code: http.StatusInternalServerError, Error: models.ErrInternalError}
}

// validTimezone accepts IANA names only; "Local" (the server zone) is refused.
func validTimezone(tz string) bool {
	if tz == "" || len(tz) > 64 || tz == "Local" || strings.ContainsAny(tz, " \\") {
		return false
	}
	_, err := time.LoadLocation(tz)
	return err == nil
}

// Get returns the user's preferences, or the defaults when they never saved any.
func (s *notificationPrefsService) Get(ctx context.Context, userID string) (db.NotificationPrefs, *models.ApiError) {
	saved, err := s.store.Get(ctx, userID)
	if err != nil {
		return db.NotificationPrefs{}, prefsStoreFailed("get", err)
	}
	if saved == nil {
		return db.DefaultNotificationPrefs(userID), nil
	}
	return *saved, nil
}

// Update saves the provided fields and returns the resulting preferences.
func (s *notificationPrefsService) Update(ctx context.Context, userID string, req api.UpdateNotificationPrefsRequest) (db.NotificationPrefs, *models.ApiError) {
	if req.Messages == nil && req.Reminders == nil && req.ReminderTime == nil && req.Timezone == nil {
		return db.NotificationPrefs{}, errPrefsEmpty
	}
	if req.ReminderTime != nil && !reminderClock.MatchString(*req.ReminderTime) {
		return db.NotificationPrefs{}, errPrefsTime
	}
	if req.Timezone != nil && !validTimezone(*req.Timezone) {
		return db.NotificationPrefs{}, errPrefsTimezone
	}
	out, err := s.store.Patch(ctx, userID, db.NotificationPrefsPatch{
		Messages: req.Messages, Reminders: req.Reminders, ReminderTime: req.ReminderTime, Timezone: req.Timezone,
	})
	if err != nil {
		return db.NotificationPrefs{}, prefsStoreFailed("patch", err)
	}
	return out, nil
}

// Lookup returns the preferences of every requested user, the defaults for those who never saved any.
func (s *notificationPrefsService) Lookup(ctx context.Context, userIDs []string) (map[string]db.NotificationPrefs, *models.ApiError) {
	if len(userIDs) == 0 || len(userIDs) > MaxNotificationPrefsLookup {
		return nil, errPrefsUsers
	}
	ids := make([]string, 0, len(userIDs))
	seen := make(map[string]bool, len(userIDs))
	for _, id := range userIDs {
		if !prefsUserID.MatchString(id) {
			return nil, errPrefsUsers
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	saved, err := s.store.ListByIDs(ctx, ids)
	if err != nil {
		return nil, prefsStoreFailed("list", err)
	}
	out := make(map[string]db.NotificationPrefs, len(ids))
	for _, id := range ids {
		out[id] = db.DefaultNotificationPrefs(id)
	}
	for _, p := range saved {
		if seen[p.UserID] {
			out[p.UserID] = p
		}
	}
	return out, nil
}
