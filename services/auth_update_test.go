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

	t.Run("gym_owner role change is refused in every direction and status", func(t *testing.T) {
		for _, st := range []db.UserStatus{db.Active, db.ComReg1, db.ComReg2, db.ComReg3} {
			for _, tc := range []struct{ from, to string }{
				{"gym_owner", "coach"}, {"gym_owner", "nutritionist"}, {"gym_owner", "admin"}, {"gym_owner", "client"},
				{"coach", "gym_owner"}, {"nutritionist", "gym_owner"}, {"client", "gym_owner"},
			} {
				current := &db.User{UserRole: tc.from, UserStatus: st}
				_, apiErr := authorizeUpdateMasks(current, updateReq(tc.to, "role"), false)
				require.NotNil(t, apiErr, "%s->%s in %s", tc.from, tc.to, st)
				assert.Equal(t, http.StatusForbidden, apiErr.Code)
			}
		}
	})

	t.Run("gym_owner unchanged role is a no-op", func(t *testing.T) {
		current := &db.User{UserRole: "gym_owner", UserStatus: db.Active}
		allowed, apiErr := authorizeUpdateMasks(current, updateReq("gym_owner", "role", "firstname"), false)
		require.Nil(t, apiErr)
		assert.Equal(t, []string{"firstname"}, allowed)
	})

	t.Run("team member cannot change role", func(t *testing.T) {
		current := &db.User{UserRole: "coach", UserStatus: db.ComReg1}
		_, apiErr := authorizeUpdateMasks(current, updateReq("nutritionist", "role"), true)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusForbidden, apiErr.Code)
	})
}

func TestIsAdminProvisionedRole(t *testing.T) {
	assert.True(t, isAdminProvisionedRole("gym_owner"))
	for _, r := range []string{"", "client", "coach", "nutritionist", "admin"} {
		assert.False(t, isAdminProvisionedRole(r), r)
	}
}
