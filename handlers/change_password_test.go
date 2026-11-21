package handlers_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"coachify-account-api/models"
	"coachify-account-api/models/api"
)

func TestChangePassword(t *testing.T) {
	const body = `{"current_password":"Old!pass1","new_password":"New!pass1"}`

	t.Run("no token claims", func(t *testing.T) {
		s := newSvcs()
		w := do(newAPIRouter(s, "", ""), "POST", "/user/change-password", body)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		s.auth.AssertNotCalled(t, "ChangePassword", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("bind errors", func(t *testing.T) {
		for _, b := range []string{`{`, `{}`, `{"current_password":"x"}`, `{"new_password":"x"}`} {
			s := newSvcs()
			w := do(newAPIRouter(s, "u1", "client"), "POST", "/user/change-password", b)
			assert.Equal(t, http.StatusBadRequest, w.Code, b)
			assert.Contains(t, decode(t, w), "error")
			s.auth.AssertNotCalled(t, "ChangePassword", mock.Anything, mock.Anything, mock.Anything)
		}
	})

	t.Run("wrong current password is propagated as 403", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("ChangePassword", mock.Anything, "u1", mock.Anything).Return(models.NewApiError(http.StatusForbidden, models.ErrCurrentPasswordIncorrect))
		w := do(newAPIRouter(s, "u1", "client"), "POST", "/user/change-password", body)
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Equal(t, "current password is incorrect", decode(t, w)["error"])
	})

	t.Run("success uses the caller id from the token", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("ChangePassword", mock.Anything, "u1", &api.ChangePasswordRequest{CurrentPassword: "Old!pass1", NewPassword: "New!pass1"}).Return(nil)
		w := do(newAPIRouter(s, "u1", "client"), "POST", "/user/change-password", body)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, true, decode(t, w)["success"])
		s.auth.AssertExpectations(t)
	})
}
