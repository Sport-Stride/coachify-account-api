package handlers_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"coachify-account-api/handlers"
	"coachify-account-api/services"
	"coachify-account-api/services/mocks"
)

func newMembersRouter(svc *mocks.GymMembersService, userID string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	p := r.Group("/")
	p.Use(func(c *gin.Context) {
		if userID != "" {
			c.Set("userID", userID)
		}
		c.Next()
	})
	p.GET("/gyms/:gym_id/members", handlers.ListGymMembers(svc))
	p.PUT("/gyms/:gym_id/members/:user_id/supervisor", handlers.PutGymMemberSupervisor(svc))
	return r
}

func TestGymMembersHandlers(t *testing.T) {
	t.Run("unauthenticated is 401", func(t *testing.T) {
		r := newMembersRouter(&mocks.GymMembersService{}, "")
		assert.Equal(t, http.StatusUnauthorized, do(r, http.MethodGet, "/gyms/g1/members", "").Code)
		assert.Equal(t, http.StatusUnauthorized, do(r, http.MethodPut, "/gyms/g1/members/u1/supervisor", `{"supervisor_id":null}`).Code)
	})

	t.Run("list returns members with pagination", func(t *testing.T) {
		svc := &mocks.GymMembersService{}
		sup := "h1"
		svc.On("ListMembers", mock.Anything, "o1", "g1", "", 1, 20).Return(
			[]services.GymMemberView{{UserID: "c1", Firstname: "Cora", Lastname: "One", Role: "coach", SupervisorID: &sup, Status: "active", JoinedAt: time.Unix(0, 0)}}, int64(1), nil)
		w := do(newMembersRouter(svc, "o1"), http.MethodGet, "/gyms/g1/members", "")
		assert.Equal(t, http.StatusOK, w.Code)
		body := decode(t, w)
		assert.EqualValues(t, 1, body["total"])
		members := body["members"].([]interface{})
		require.Len(t, members, 1)
		m := members[0].(map[string]interface{})
		assert.Equal(t, "Cora", m["firstname"])
		assert.Equal(t, "One", m["lastname"])
		assert.Equal(t, "h1", m["supervisor_id"])
		assert.NotContains(t, m, "email")
		assert.NotContains(t, m, "phone")
		assert.NotContains(t, m, "profile_picture", "omitted when empty")
	})

	t.Run("supervisor body: key required, empty string rejected, null clears", func(t *testing.T) {
		svc := &mocks.GymMembersService{}
		svc.On("SetSupervisor", mock.Anything, "o1", "g1", "c1", "").Return(&services.GymMemberView{UserID: "c1"}, nil)
		svc.On("SetSupervisor", mock.Anything, "o1", "g1", "c1", "h1").Return(&services.GymMemberView{UserID: "c1"}, nil)
		r := newMembersRouter(svc, "o1")
		for _, body := range []string{`{}`, `{"supervisor_id":""}`, `[]`} {
			assert.Equal(t, http.StatusBadRequest, do(r, http.MethodPut, "/gyms/g1/members/c1/supervisor", body).Code, body)
		}
		assert.Equal(t, http.StatusOK, do(r, http.MethodPut, "/gyms/g1/members/c1/supervisor", `{"supervisor_id":null}`).Code)
		assert.Equal(t, http.StatusOK, do(r, http.MethodPut, "/gyms/g1/members/c1/supervisor", `{"supervisor_id":"h1"}`).Code)
	})

	t.Run("service errors keep the error body", func(t *testing.T) {
		svc := &mocks.GymMembersService{}
		svc.On("SetSupervisor", mock.Anything, "h1", "g1", "c1", "h1").Return(nil, apiErr(http.StatusForbidden, "Gym owner access required"))
		w := do(newMembersRouter(svc, "h1"), http.MethodPut, "/gyms/g1/members/c1/supervisor", `{"supervisor_id":"h1"}`)
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Equal(t, "Gym owner access required", decode(t, w)["error"])
	})
}

func TestListGymMembersRoleFilter(t *testing.T) {
	svc := &mocks.GymMembersService{}
	svc.On("ListMembers", mock.Anything, "o1", "g1", "coach", 1, 20).Return([]services.GymMemberView{}, int64(0), nil)
	r := newMembersRouter(svc, "o1")
	assert.Equal(t, http.StatusOK, do(r, http.MethodGet, "/gyms/g1/members?role=coach", "").Code)
	assert.Equal(t, http.StatusBadRequest, do(r, http.MethodGet, "/gyms/g1/members?role=admin", "").Code)
	svc.AssertNumberOfCalls(t, "ListMembers", 1)
}
