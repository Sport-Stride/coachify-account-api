package handlers_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models"
	"coachify-account-api/models/api"
)

func TestUpdateUser(t *testing.T) {
	t.Run("no token claims", func(t *testing.T) {
		s := newSvcs()
		w := do(newAPIRouter(s, "", ""), "PUT", "/user/update-user", `{"user":{"firstname":"A","lastname":"B"},"update_masks":["firstname"]}`)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		s.auth.AssertNotCalled(t, "UpdateUser", mock.Anything, mock.Anything)
	})

	t.Run("invalid body", func(t *testing.T) {
		w := do(newAPIRouter(newSvcs(), "u1", "client"), "PUT", "/user/update-user", `{`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, "Invalid request payload", decode(t, w)["error"])
	})

	t.Run("token id overrides body id", func(t *testing.T) {
		s := newSvcs()
		var got api.RequestUpdateUser
		s.auth.On("UpdateUser", mock.Anything, mock.Anything).
			Run(func(args mock.Arguments) { got = args.Get(1).(api.RequestUpdateUser) }).
			Return(&api.ApiUser{ExternalID: "token-user"}, nil)
		w := do(newAPIRouter(s, "token-user", "client"), "PUT", "/user/update-user",
			`{"user":{"external_id":"victim","firstname":"A","lastname":"B"},"update_masks":["firstname"]}`)
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "token-user", got.User.ExternalID)
		assert.Equal(t, []string{"firstname"}, got.UpdateMasks)
	})

	t.Run("body without id still uses token id", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("UpdateUser", mock.Anything, mock.MatchedBy(func(r api.RequestUpdateUser) bool {
			return r.User.ExternalID == "token-user"
		})).Return(&api.ApiUser{}, nil)
		w := do(newAPIRouter(s, "token-user", "client"), "PUT", "/user/update-user", `{"user":{"firstname":"A","lastname":"B"}}`)
		assert.Equal(t, http.StatusOK, w.Code)
		s.auth.AssertExpectations(t)
	})

	t.Run("role/status escalation blocked by service is surfaced as 403", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("UpdateUser", mock.Anything, mock.MatchedBy(func(r api.RequestUpdateUser) bool {
			return r.User.UserRole == "admin" && r.User.ExternalID == "u1"
		})).Return(nil, apiErr(http.StatusForbidden, "role change not allowed"))
		w := do(newAPIRouter(s, "u1", "coach"), "PUT", "/user/update-user",
			`{"user":{"firstname":"A","lastname":"B","role":"admin","status":"active"},"update_masks":["role","status"]}`)
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Equal(t, "role change not allowed", decode(t, w)["error"])
	})

	t.Run("user not found", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("UpdateUser", mock.Anything, mock.Anything).Return(nil, models.NewApiError(http.StatusNotFound, models.ErrUserNotFound))
		w := do(newAPIRouter(s, "u1", "client"), "PUT", "/user/update-user", `{"user":{"firstname":"A","lastname":"B"}}`)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

func TestDeleteAuthenticatedUser(t *testing.T) {
	t.Run("no token claims", func(t *testing.T) {
		s := newSvcs()
		w := do(newAPIRouter(s, "", ""), "DELETE", "/user", "")
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		s.auth.AssertNotCalled(t, "DeleteUser", mock.Anything, mock.Anything)
	})

	t.Run("deletes only the token user, ignoring body", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("DeleteUser", mock.Anything, "u1").Return(nil)
		w := do(newAPIRouter(s, "u1", "client"), "DELETE", "/user", `{"external_id":"someone-else"}`)
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "Account deleted successfully", decode(t, w)["message"])
		s.auth.AssertExpectations(t)
	})

	t.Run("service error", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("DeleteUser", mock.Anything, "u1").Return(models.NewApiError(http.StatusInternalServerError, models.ErrUserDeletionFailed))
		w := do(newAPIRouter(s, "u1", "client"), "DELETE", "/user", "")
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Equal(t, "user deletion failed", decode(t, w)["error"])
	})
}

func TestMatriculeFiscaleUser(t *testing.T) {
	t.Run("submit: no claims", func(t *testing.T) {
		w := do(newAPIRouter(newSvcs(), "", ""), "PATCH", "/user/matricule-fiscale", `{"matricule_fiscale":"123"}`)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
	t.Run("submit: role not allowed", func(t *testing.T) {
		s := newSvcs()
		w := do(newAPIRouter(s, "u1", "client"), "PATCH", "/user/matricule-fiscale", `{"matricule_fiscale":"123"}`)
		assert.Equal(t, http.StatusForbidden, w.Code)
		s.auth.AssertNotCalled(t, "SubmitMatriculeFiscale", mock.Anything, mock.Anything, mock.Anything)
	})
	t.Run("submit: missing field", func(t *testing.T) {
		w := do(newAPIRouter(newSvcs(), "u1", "coach"), "PATCH", "/user/matricule-fiscale", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
	t.Run("submit: already approved", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("SubmitMatriculeFiscale", mock.Anything, "u1", "123").Return(models.NewApiError(http.StatusConflict, models.ErrMatriculeFiscaleAlreadyApproved))
		w := do(newAPIRouter(s, "u1", "nutritionist"), "PATCH", "/user/matricule-fiscale", `{"matricule_fiscale":"123"}`)
		assert.Equal(t, http.StatusConflict, w.Code)
	})
	t.Run("submit: success", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("SubmitMatriculeFiscale", mock.Anything, "u1", "123").Return(nil)
		w := do(newAPIRouter(s, "u1", "coach"), "PATCH", "/user/matricule-fiscale", `{"matricule_fiscale":"123"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		s.auth.AssertExpectations(t)
	})
	t.Run("withdraw: no claims", func(t *testing.T) {
		w := do(newAPIRouter(newSvcs(), "", ""), "DELETE", "/user/tax-registration", "")
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
	t.Run("withdraw: role not allowed", func(t *testing.T) {
		w := do(newAPIRouter(newSvcs(), "u1", "admin"), "DELETE", "/user/tax-registration", "")
		assert.Equal(t, http.StatusForbidden, w.Code)
	})
	t.Run("withdraw: success", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("WithdrawTaxRegistration", mock.Anything, "u1").Return(nil)
		w := do(newAPIRouter(s, "u1", "coach"), "DELETE", "/user/tax-registration", "")
		assert.Equal(t, http.StatusOK, w.Code)
	})
	t.Run("withdraw: service error", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("WithdrawTaxRegistration", mock.Anything, "u1").Return(models.NewApiError(http.StatusBadRequest, models.ErrMatriculeFiscaleInvalidStatus))
		w := do(newAPIRouter(s, "u1", "coach"), "DELETE", "/user/tax-registration", "")
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}
