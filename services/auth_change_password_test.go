package services

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/core"
	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
)

type fakePasswordStore struct {
	user      *db.User
	getErr    *models.ApiError
	updateErr *models.ApiError
	savedID   string
	savedHash string
	saves     int
}

func (f *fakePasswordStore) GetUserByExternalIdUpdate(context.Context, string) (*db.User, *models.ApiError) {
	return f.user, f.getErr
}

func (f *fakePasswordStore) UpdatePasswordByExternalID(_ context.Context, id, hash string) *models.ApiError {
	f.saves++
	f.savedID, f.savedHash = id, hash
	return f.updateErr
}

func newChangePwSvc(t *testing.T, current string, store *fakePasswordStore) *AuthServiceImpl {
	t.Helper()
	pc := core.NewPasswordChecker()
	if current != "" {
		hash, err := pc.HashPassword(current)
		require.Nil(t, err)
		store.user = &db.User{ExternalID: "u1", UserPassword: hash}
	}
	return &AuthServiceImpl{passwordChecker: pc, passwordUsers: store}
}

func TestChangePassword(t *testing.T) {
	ctx := context.Background()

	t.Run("success stores a new bcrypt hash", func(t *testing.T) {
		store := &fakePasswordStore{}
		s := newChangePwSvc(t, "Old!pass1", store)
		err := s.ChangePassword(ctx, "u1", &api.ChangePasswordRequest{CurrentPassword: "Old!pass1", NewPassword: "New!pass1"})
		require.Nil(t, err)
		assert.Equal(t, "u1", store.savedID)
		assert.NotEqual(t, "New!pass1", store.savedHash)
		assert.Nil(t, core.NewPasswordChecker().VerifyPassword("New!pass1", store.savedHash))
	})

	t.Run("wrong current password is 403", func(t *testing.T) {
		store := &fakePasswordStore{}
		s := newChangePwSvc(t, "Old!pass1", store)
		err := s.ChangePassword(ctx, "u1", &api.ChangePasswordRequest{CurrentPassword: "Wrong!pass1", NewPassword: "New!pass1"})
		require.NotNil(t, err)
		assert.Equal(t, http.StatusForbidden, err.Code)
		assert.ErrorIs(t, err.Error, models.ErrCurrentPasswordIncorrect)
		assert.Zero(t, store.saves)
	})

	t.Run("weak new password is 400 before any lookup", func(t *testing.T) {
		for _, pw := range []string{"short1!", "alllower1!", "ALLUPPER1!", "NoSymbol123"} {
			store := &fakePasswordStore{getErr: models.NewApiError(500, models.ErrInternalError)}
			s := newChangePwSvc(t, "", store)
			err := s.ChangePassword(ctx, "u1", &api.ChangePasswordRequest{CurrentPassword: "Old!pass1", NewPassword: pw})
			require.NotNil(t, err, pw)
			assert.Equal(t, http.StatusBadRequest, err.Code, pw)
			assert.ErrorIs(t, err.Error, models.ErrInvalidPassword)
		}
	})

	t.Run("same password is 400", func(t *testing.T) {
		store := &fakePasswordStore{}
		s := newChangePwSvc(t, "Old!pass1", store)
		err := s.ChangePassword(ctx, "u1", &api.ChangePasswordRequest{CurrentPassword: "Old!pass1", NewPassword: "Old!pass1"})
		require.NotNil(t, err)
		assert.Equal(t, http.StatusBadRequest, err.Code)
		assert.ErrorIs(t, err.Error, models.ErrNewPasswordSameAsCurrent)
		assert.Zero(t, store.saves)
	})

	t.Run("oauth-only account without a password is 400", func(t *testing.T) {
		store := &fakePasswordStore{user: &db.User{ExternalID: "u1"}}
		s := newChangePwSvc(t, "", store)
		err := s.ChangePassword(ctx, "u1", &api.ChangePasswordRequest{CurrentPassword: "Old!pass1", NewPassword: "New!pass1"})
		require.NotNil(t, err)
		assert.Equal(t, http.StatusBadRequest, err.Code)
		assert.ErrorIs(t, err.Error, models.ErrNoLocalPassword)
	})

	t.Run("user lookup failure is propagated", func(t *testing.T) {
		store := &fakePasswordStore{getErr: models.NewApiError(http.StatusNotFound, models.ErrUserNotFound)}
		s := newChangePwSvc(t, "", store)
		err := s.ChangePassword(ctx, "u1", &api.ChangePasswordRequest{CurrentPassword: "Old!pass1", NewPassword: "New!pass1"})
		require.NotNil(t, err)
		assert.Equal(t, http.StatusNotFound, err.Code)
	})

	t.Run("repository update failure is propagated", func(t *testing.T) {
		store := &fakePasswordStore{updateErr: models.NewApiError(http.StatusInternalServerError, models.ErrInternalError)}
		s := newChangePwSvc(t, "Old!pass1", store)
		err := s.ChangePassword(ctx, "u1", &api.ChangePasswordRequest{CurrentPassword: "Old!pass1", NewPassword: "New!pass1"})
		require.NotNil(t, err)
		assert.Equal(t, http.StatusInternalServerError, err.Code)
	})

	t.Run("empty caller id is rejected", func(t *testing.T) {
		store := &fakePasswordStore{}
		s := newChangePwSvc(t, "Old!pass1", store)
		err := s.ChangePassword(ctx, "", &api.ChangePasswordRequest{CurrentPassword: "Old!pass1", NewPassword: "New!pass1"})
		require.NotNil(t, err)
		assert.Equal(t, http.StatusBadRequest, err.Code)
	})
}
