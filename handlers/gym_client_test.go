package handlers_test

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"coachify-account-api/handlers"
	"coachify-account-api/models/db"
	"coachify-account-api/services"
	"coachify-account-api/services/mocks"
)

func newGymClientRouter(svc *mocks.GymClientService, userID string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	p := r.Group("/")
	p.Use(func(c *gin.Context) {
		if userID != "" {
			c.Set("userID", userID)
		}
		c.Next()
	})
	p.GET("/gyms/:gym_id/clients", handlers.ListGymRelationClients(svc))
	p.POST("/gyms/:gym_id/clients/:client_id/assign", handlers.AssignGymClient(svc))
	p.POST("/gyms/:gym_id/clients/:client_id/unassign", handlers.UnassignGymClient(svc))
	p.DELETE("/gyms/:gym_id/clients/:client_id", handlers.RemoveGymClient(svc))
	return r
}

func TestGymClientHandlers(t *testing.T) {
	t.Run("unauthenticated is 401", func(t *testing.T) {
		r := newGymClientRouter(&mocks.GymClientService{}, "")
		assert.Equal(t, http.StatusUnauthorized, do(r, http.MethodGet, "/gyms/g1/clients", "").Code)
		assert.Equal(t, http.StatusUnauthorized, do(r, http.MethodPost, "/gyms/g1/clients/c1/assign", `{"coach_id":"x"}`).Code)
		assert.Equal(t, http.StatusUnauthorized, do(r, http.MethodPost, "/gyms/g1/clients/c1/unassign", "").Code)
		assert.Equal(t, http.StatusUnauthorized, do(r, http.MethodDelete, "/gyms/g1/clients/c1", "").Code)
	})

	t.Run("list forwards filters and returns the safe row", func(t *testing.T) {
		svc := &mocks.GymClientService{}
		no := false
		svc.On("ListClients", mock.Anything, "o1", "g1", services.GymClientListQuery{
			Assigned: &no, CoachID: "c1", Q: "al", Sort: "linked_at", Page: 2, Size: 5,
		}).Return([]db.GymClientRow{{ExternalID: "cl1", Firstname: "A", Email: "a@x.tn", CoachID: "c1"}}, 7, nil)
		w := do(newGymClientRouter(svc, "o1"), http.MethodGet, "/gyms/g1/clients?assigned=false&coach_id=c1&q=al&sort=linked_at&page=2&size=5", "")
		require.Equal(t, http.StatusOK, w.Code)
		body := decode(t, w)
		assert.EqualValues(t, 7, body["total"])
		rows := body["clients"].([]interface{})
		require.Len(t, rows, 1)
		assert.NotContains(t, rows[0], "phone_number")
		svc.AssertExpectations(t)
	})

	t.Run("invalid assigned or sort is 400", func(t *testing.T) {
		svc := &mocks.GymClientService{}
		r := newGymClientRouter(svc, "o1")
		assert.Equal(t, http.StatusBadRequest, do(r, http.MethodGet, "/gyms/g1/clients?assigned=maybe", "").Code)
		assert.Equal(t, http.StatusBadRequest, do(r, http.MethodGet, "/gyms/g1/clients?sort=email", "").Code)
		svc.AssertNotCalled(t, "ListClients", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("service errors keep their status and body shape", func(t *testing.T) {
		svc := &mocks.GymClientService{}
		svc.On("ListClients", mock.Anything, "o1", "g404", mock.Anything).Return(nil, 0, apiErr(http.StatusNotFound, "gym not found"))
		w := do(newGymClientRouter(svc, "o1"), http.MethodGet, "/gyms/g404/clients", "")
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Equal(t, "gym not found", decode(t, w)["error"])
	})

	t.Run("assign requires coach_id and returns the dispatch result", func(t *testing.T) {
		svc := &mocks.GymClientService{}
		svc.On("Assign", mock.Anything, "o1", "g1", "cl1", "c2").Return(&services.DispatchResult{ClientID: "cl1", CoachID: "c2", PreviousCoachID: "c1"}, nil)
		r := newGymClientRouter(svc, "o1")
		assert.Equal(t, http.StatusBadRequest, do(r, http.MethodPost, "/gyms/g1/clients/cl1/assign", `{}`).Code)
		assert.Equal(t, http.StatusBadRequest, do(r, http.MethodPost, "/gyms/g1/clients/cl1/assign", `nope`).Code)
		w := do(r, http.MethodPost, "/gyms/g1/clients/cl1/assign", `{"coach_id":"c2"}`)
		require.Equal(t, http.StatusOK, w.Code)
		body := decode(t, w)
		assert.Equal(t, "c2", body["coach_id"])
		assert.Equal(t, "c1", body["previous_coach_id"])
		assert.Contains(t, body, "assigned_at")
	})

	t.Run("403 without the capability", func(t *testing.T) {
		svc := &mocks.GymClientService{}
		svc.On("Unassign", mock.Anything, "c1", "g1", "cl1").Return(nil, apiErr(http.StatusForbidden, "forbidden"))
		svc.On("Remove", mock.Anything, "c1", "g1", "cl1").Return(apiErr(http.StatusForbidden, "forbidden"))
		r := newGymClientRouter(svc, "c1")
		assert.Equal(t, http.StatusForbidden, do(r, http.MethodPost, "/gyms/g1/clients/cl1/unassign", "").Code)
		assert.Equal(t, http.StatusForbidden, do(r, http.MethodDelete, "/gyms/g1/clients/cl1", "").Code)
	})

	t.Run("remove answers a message", func(t *testing.T) {
		svc := &mocks.GymClientService{}
		svc.On("Remove", mock.Anything, "o1", "g1", "cl1").Return(nil)
		w := do(newGymClientRouter(svc, "o1"), http.MethodDelete, "/gyms/g1/clients/cl1", "")
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, decode(t, w), "message")
	})
}

func TestListCoachClientsKind(t *testing.T) {
	for _, kind := range []string{"personal", "gym"} {
		s := newSvcs()
		s.coach.On("ListCoachClients", mock.Anything, db.CoachClientListQuery{CoachID: "coach1", Kind: kind}).Return(nil, 0, nil)
		assert.Equal(t, http.StatusOK, get(newAPIRouter(s, "coach1", "coach"), "/coach/clients?kind="+kind).Code, kind)
		s.coach.AssertExpectations(t)
	}
	for _, kind := range []string{"all", "Gym", "x"} {
		s := newSvcs()
		w := get(newAPIRouter(s, "coach1", "coach"), "/coach/clients?kind="+kind)
		assert.Equal(t, http.StatusBadRequest, w.Code, kind)
		assert.Contains(t, decode(t, w)["error"], "kind")
		s.coach.AssertNotCalled(t, "ListCoachClients", mock.Anything, mock.Anything)
	}
}
