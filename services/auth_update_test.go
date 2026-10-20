package services

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
)

func updateReq(role string, masks ...string) *api.RequestUpdateUser {
	return &api.RequestUpdateUser{User: api.UserRequest{UserRole: role}, UpdateMasks: masks}
}

func TestAuthorizeUpdateMasks(t *testing.T) {
	t.Run("escalation to admin is blocked", func(t *testing.T) {
		current := &db.User{UserRole: "coach", UserStatus: db.Active}
		_, apiErr := authorizeUpdateMasks(current, updateReq("admin", "role"), false)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusForbidden, apiErr.Code)
	})

	t.Run("escalation to admin blocked even during registration", func(t *testing.T) {
		current := &db.User{UserRole: "coach", UserStatus: db.ComReg1}
		_, apiErr := authorizeUpdateMasks(current, updateReq("admin", "role"), false)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusForbidden, apiErr.Code)
	})

	t.Run("client cannot become coach during registration", func(t *testing.T) {
		current := &db.User{UserRole: "client", UserStatus: db.ComReg1}
		_, apiErr := authorizeUpdateMasks(current, updateReq("coach", "role"), false)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusForbidden, apiErr.Code)
	})

	t.Run("coach to nutritionist allowed during registration", func(t *testing.T) {
		for _, st := range []db.UserStatus{db.ComReg1, db.ComReg2, db.ComReg3} {
			current := &db.User{UserRole: "coach", UserStatus: st}
			allowed, apiErr := authorizeUpdateMasks(current, updateReq("nutritionist", "role", "firstname"), false)
			require.Nil(t, apiErr)
			assert.Equal(t, []string{"role", "firstname"}, allowed)
		}
	})

	t.Run("role change after Active is blocked", func(t *testing.T) {
		current := &db.User{UserRole: "coach", UserStatus: db.Active}
		_, apiErr := authorizeUpdateMasks(current, updateReq("nutritionist", "role"), false)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusForbidden, apiErr.Code)
	})

	t.Run("unchanged role is a harmless no-op", func(t *testing.T) {
		current := &db.User{UserRole: "coach", UserStatus: db.Active}
		allowed, apiErr := authorizeUpdateMasks(current, updateReq("coach", "role", "firstname"), false)
		require.Nil(t, apiErr)
		assert.Equal(t, []string{"firstname"}, allowed)
	})

	t.Run("status mask is always dropped", func(t *testing.T) {
		for _, st := range []db.UserStatus{db.Blocked, db.Suspended, db.ToConfirm, db.ComReg3} {
			current := &db.User{UserRole: "coach", UserStatus: st}
			req := updateReq("coach", "status", "lastname")
			req.User.UserStatus = string(db.Active)
			allowed, apiErr := authorizeUpdateMasks(current, req, false)
			require.Nil(t, apiErr)
			assert.Equal(t, []string{"lastname"}, allowed)
		}
	})

	t.Run("head coach cannot change role", func(t *testing.T) {
		current := &db.User{UserRole: "coach", UserStatus: db.ComReg1, IsHeadCoach: true}
		_, apiErr := authorizeUpdateMasks(current, updateReq("nutritionist", "role"), false)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusForbidden, apiErr.Code)
		assert.EqualError(t, apiErr.Error, "role change not allowed")
	})

	t.Run("team member cannot change role", func(t *testing.T) {
		current := &db.User{UserRole: "coach", UserStatus: db.ComReg1}
		_, apiErr := authorizeUpdateMasks(current, updateReq("nutritionist", "role"), true)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusForbidden, apiErr.Code)
	})
}

