package handlers_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
)

func TestGetMe(t *testing.T) {
	t.Run("no token claims", func(t *testing.T) {
		s := newSvcs()
		w := do(newAPIRouter(s, "", ""), "GET", "/user/me", "")
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		s.auth.AssertNotCalled(t, "GetMe", mock.Anything, mock.Anything)
	})

	t.Run("service error is propagated", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("GetMe", mock.Anything, "u1").Return(nil, models.NewApiError(http.StatusNotFound, models.ErrUserNotFound))
		w := do(newAPIRouter(s, "u1", "client"), "GET", "/user/me", "")
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Equal(t, "user not found", decode(t, w)["error"])
	})

	t.Run("returns the caller's profile with personal details", func(t *testing.T) {
		s := newSvcs()
		me := &api.MeUser{
			PublicUser: api.PublicUser{ExternalID: "u1", Firstname: "Prénom"},
			Personal:   &db.PersonalInfo{BirthDate: "1995-04-12", HeightCm: 172},
		}
		s.auth.On("GetMe", mock.Anything, "u1").Return(me, nil)
		w := do(newAPIRouter(s, "u1", "client"), "GET", "/user/me", "")
		assert.Equal(t, http.StatusOK, w.Code)
		body := decode(t, w)
		assert.Equal(t, "u1", body["external_id"], "public fields are flattened at the top level")
		assert.Equal(t, map[string]any{"birth_date": "1995-04-12", "height_cm": float64(172)}, body["personal"])
		s.auth.AssertExpectations(t)
	})
}
