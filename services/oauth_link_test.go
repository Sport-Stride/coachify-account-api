package services

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models/db"
)

func TestNormalizeEmail(t *testing.T) {
	assert.Equal(t, "a@b.com", normalizeClaimEmail("  A@B.Com "))
}

func TestOAuthLinkPlan(t *testing.T) {
	for _, st := range []db.UserStatus{db.Blocked, db.Suspended, db.Deactivated} {
		_, _, err := oauthLinkPlan(&db.User{UserStatus: st})
		require.NotNil(t, err, st)
		assert.Equal(t, http.StatusForbidden, err.Code)
	}
	tk, st, err := oauthLinkPlan(&db.User{UserStatus: db.ToConfirm, UserRole: "client"})
	require.Nil(t, err)
	assert.True(t, tk)
	assert.Equal(t, db.Active, st)
	_, st, _ = oauthLinkPlan(&db.User{UserStatus: db.ToConfirm, UserRole: "coach"})
	assert.Equal(t, db.ComReg1, st)

	tk, st, _ = oauthLinkPlan(&db.User{UserStatus: db.Active, UserResetPasswordCode: &db.UserResetPasswordCode{Code: "123456"}})
	assert.True(t, tk, "pending reset code")
	assert.Equal(t, db.Active, st)
	tk, _, _ = oauthLinkPlan(&db.User{UserStatus: db.Active, UserConfirmCode: &db.UserConfirmCode{Code: "123456"}})
	assert.True(t, tk, "pending confirm code")

	tk, _, err = oauthLinkPlan(&db.User{UserStatus: db.Active, UserConfirmCode: &db.UserConfirmCode{}})
	assert.Nil(t, err)
	assert.False(t, tk, "clean confirmed account links normally")
}
