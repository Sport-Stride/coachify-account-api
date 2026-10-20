package handlers_test

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/mongo"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
)

func TestListCoachClients(t *testing.T) {
	t.Run("no token claims", func(t *testing.T) {
		s := newSvcs()
		w := get(newAPIRouter(s, "", ""), "/coach/clients")
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		s.coach.AssertNotCalled(t, "ListCoachClients", mock.Anything, mock.Anything)
	})

	t.Run("coach id always comes from token, query filters and pagination forwarded", func(t *testing.T) {
		s := newSvcs()
		from := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		to := time.Date(2026, 2, 2, 3, 4, 5, 0, time.UTC)
		want := db.CoachClientListQuery{CoachID: "coach1", ClientID: "cl1", FromDate: from, ToDate: to, Page: 2, Size: 5}
		s.coach.On("ListCoachClients", mock.Anything, want).Return([]map[string]interface{}{{"external_id": "cl1"}}, 11, nil)
		w := get(newAPIRouter(s, "coach1", "coach"),
			"/coach/clients?client_id=cl1&from_date=2026-01-02T03:04:05Z&to_date=2026-02-02T03:04:05Z&page=2&size=5&coach_id=attacker")
		require.Equal(t, http.StatusOK, w.Code)
		body := decode(t, w)
		assert.Equal(t, float64(11), body["total"])
		assert.Len(t, body["clients"], 1)
		s.coach.AssertExpectations(t)
	})

	t.Run("defaults when no query", func(t *testing.T) {
		s := newSvcs()
		s.coach.On("ListCoachClients", mock.Anything, db.CoachClientListQuery{CoachID: "coach1"}).Return(nil, 0, nil)
		w := get(newAPIRouter(s, "coach1", "coach"), "/coach/clients")
		assert.Equal(t, http.StatusOK, w.Code)
		s.coach.AssertExpectations(t)
	})

	for _, tc := range []struct{ name, qs string }{
		{"bad page", "?page=abc"},
		{"bad size", "?size=x"},
		{"bad from_date", "?from_date=yesterday"},
		{"bad to_date", "?to_date=2026-13-45"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newSvcs()
			w := get(newAPIRouter(s, "coach1", "coach"), "/coach/clients"+tc.qs)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, decode(t, w), "error")
			s.coach.AssertNotCalled(t, "ListCoachClients", mock.Anything, mock.Anything)
		})
	}

	t.Run("service error", func(t *testing.T) {
		s := newSvcs()
		s.coach.On("ListCoachClients", mock.Anything, mock.Anything).Return(nil, 0, errors.New("db down"))
		w := get(newAPIRouter(s, "coach1", "coach"), "/coach/clients")
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Contains(t, decode(t, w), "error")
	})
}

func TestGetCoachIDByClientID(t *testing.T) {
	t.Run("no token claims", func(t *testing.T) {
		w := get(newAPIRouter(newSvcs(), "", ""), "/coach/client")
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
	t.Run("success uses token id as client id", func(t *testing.T) {
		s := newSvcs()
		s.coach.On("GetCoachIDByClientID", mock.Anything, "cl1").Return("coach1", nil)
		w := get(newAPIRouter(s, "cl1", "client"), "/coach/client")
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "coach1", decode(t, w)["coach_id"])
	})
	t.Run("no relationship is 404", func(t *testing.T) {
		s := newSvcs()
		s.coach.On("GetCoachIDByClientID", mock.Anything, "cl1").Return("", mongo.ErrNoDocuments)
		w := get(newAPIRouter(s, "cl1", "client"), "/coach/client")
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Equal(t, "no coach-client relationship found", decode(t, w)["error"])
	})
	t.Run("other error is 500", func(t *testing.T) {
		s := newSvcs()
		s.coach.On("GetCoachIDByClientID", mock.Anything, "cl1").Return("", errors.New("boom"))
		w := get(newAPIRouter(s, "cl1", "client"), "/coach/client")
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestDissociateCoachClient(t *testing.T) {
	t.Run("no token claims", func(t *testing.T) {
		s := newSvcs()
		w := do(newAPIRouter(s, "", ""), "DELETE", "/coach/client/cl1", "")
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		s.coach.AssertNotCalled(t, "DissociateCoachClient", mock.Anything, mock.Anything, mock.Anything)
	})
	t.Run("success scoped to token coach", func(t *testing.T) {
		s := newSvcs()
		s.coach.On("DissociateCoachClient", mock.Anything, "coach1", "cl1").Return(nil)
		w := do(newAPIRouter(s, "coach1", "coach"), "DELETE", "/coach/client/cl1", "")
		assert.Equal(t, http.StatusOK, w.Code)
		s.coach.AssertExpectations(t)
	})
	t.Run("service error", func(t *testing.T) {
		s := newSvcs()
		s.coach.On("DissociateCoachClient", mock.Anything, "coach1", "cl1").Return(errors.New("boom"))
		w := do(newAPIRouter(s, "coach1", "coach"), "DELETE", "/coach/client/cl1", "")
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestRegistrationLinks(t *testing.T) {
	link := &db.RegistrationLink{Token: "tok", CoachID: "coach1", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}

	t.Run("generate: no claims", func(t *testing.T) {
		w := do(newAPIRouter(newSvcs(), "", ""), "POST", "/coach/registration-link", "")
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
	t.Run("generate: success for token coach", func(t *testing.T) {
		s := newSvcs()
		s.reg.On("GetOrCreateLink", mock.Anything, "coach1").Return(link, nil)
		w := do(newAPIRouter(s, "coach1", "coach"), "POST", "/coach/registration-link", "")
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "tok", decode(t, w)["token"])
		assert.Equal(t, "coach1", decode(t, w)["coach_id"])
	})
	t.Run("generate: service error", func(t *testing.T) {
		s := newSvcs()
		s.reg.On("GetOrCreateLink", mock.Anything, "coach1").Return(nil, models.NewApiError(http.StatusForbidden, errors.New("not a coach")))
		w := do(newAPIRouter(s, "coach1", "client"), "POST", "/coach/registration-link", "")
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Equal(t, "not a coach", decode(t, w)["error"])
	})
	t.Run("validate: success", func(t *testing.T) {
		s := newSvcs()
		s.reg.On("ValidateToken", mock.Anything, "tok").Return(link, "Coach Name", nil)
		w := get(newAPIRouter(s, "", ""), "/coach/registration-link/tok")
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "Coach Name", decode(t, w)["coach_name"])
	})
	t.Run("validate: unknown token", func(t *testing.T) {
		s := newSvcs()
		s.reg.On("ValidateToken", mock.Anything, "nope").Return(nil, "", models.NewApiError(http.StatusNotFound, models.ErrRegistrationLinkNotFound))
		w := get(newAPIRouter(s, "", ""), "/coach/registration-link/nope")
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}
