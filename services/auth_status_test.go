package services

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
)

func TestInactiveAccountError(t *testing.T) {
	for _, st := range []db.UserStatus{db.Suspended, db.Deactivated} {
		apiErr := inactiveAccountError(st)
		require.NotNil(t, apiErr, st)
		assert.Equal(t, http.StatusForbidden, apiErr.Code)
		assert.Equal(t, "account suspended", apiErr.Error.Error())
	}
	apiErr := inactiveAccountError(db.Blocked)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusForbidden, apiErr.Code)
	assert.Equal(t, models.ErrUserBlocked, apiErr.Error)
	assert.Nil(t, inactiveAccountError(db.Active))
}

func TestCheckRefreshToken(t *testing.T) {
	tok := "good"
	t.Run("nil stored token is 401 without panic", func(t *testing.T) {
		apiErr := checkRefreshToken(&api.RefreshToken{UserStatus: string(db.Active)}, "good")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusUnauthorized, apiErr.Code)
	})
	t.Run("nil user", func(t *testing.T) {
		require.NotNil(t, checkRefreshToken(nil, "good"))
	})
	t.Run("mismatch", func(t *testing.T) {
		apiErr := checkRefreshToken(&api.RefreshToken{RefreshToken: &tok, UserStatus: string(db.Active)}, "bad")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusUnauthorized, apiErr.Code)
	})
	t.Run("inactive account rejected", func(t *testing.T) {
		for _, st := range []db.UserStatus{db.Blocked, db.Suspended, db.Deactivated} {
			apiErr := checkRefreshToken(&api.RefreshToken{RefreshToken: &tok, UserStatus: string(st)}, "good")
			require.NotNil(t, apiErr, st)
			assert.Equal(t, http.StatusForbidden, apiErr.Code)
		}
	})
	t.Run("valid", func(t *testing.T) {
		assert.Nil(t, checkRefreshToken(&api.RefreshToken{RefreshToken: &tok, UserStatus: string(db.Active)}, "good"))
	})
}
