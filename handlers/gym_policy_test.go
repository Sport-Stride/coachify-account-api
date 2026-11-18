package handlers_test

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"coachify-account-api/handlers"
	"coachify-account-api/services"
	"coachify-account-api/services/mocks"
)

// newPolicyRouter mirrors the gym capability routes of router.initializeRoutes behind a fake auth middleware.
func newPolicyRouter(svc *mocks.GymPolicyService, userID string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	p := r.Group("/")
	p.Use(func(c *gin.Context) {
		if userID != "" {
			c.Set("userID", userID)
			c.Set("userRole", "coach")
		}
		c.Next()
	})
	p.GET("/me/gym-context", handlers.GetMyGymContext(svc))
	p.GET("/gyms/:gym_id/policies/:role", handlers.GetGymRolePolicy(svc))
	p.PUT("/gyms/:gym_id/policies/:role", handlers.PutGymRolePolicy(svc))
	p.GET("/gyms/:gym_id/members/:user_id/grants", handlers.GetGymMemberGrants(svc))
	p.PUT("/gyms/:gym_id/members/:user_id/grants", handlers.PutGymMemberGrants(svc))
	r.POST("/internal/authz/check", handlers.CheckInternalAuthz(svc))
	return r
}

func TestGymPolicyHandlers(t *testing.T) {
	t.Run("unauthenticated is 401", func(t *testing.T) {
		svc := &mocks.GymPolicyService{}
		r := newPolicyRouter(svc, "")
		for _, c := range []struct{ method, path, body string }{
			{http.MethodGet, "/me/gym-context", ""},
			{http.MethodGet, "/gyms/g1/policies/coach", ""},
			{http.MethodPut, "/gyms/g1/policies/coach", `{"capabilities":{},"version":0}`},
			{http.MethodGet, "/gyms/g1/members/u1/grants", ""},
			{http.MethodPut, "/gyms/g1/members/u1/grants", `{"grants":[]}`},
		} {
			w := do(r, c.method, c.path, c.body)
			assert.Equal(t, http.StatusUnauthorized, w.Code, c.path)
			assert.Equal(t, "Unauthorized", decode(t, w)["error"])
		}
		svc.AssertNotCalled(t, "GetRolePolicy")
	})

	t.Run("non owner member is 403", func(t *testing.T) {
		svc := &mocks.GymPolicyService{}
		svc.On("GetRolePolicy", mock.Anything, "head1", "g1", "coach").Return(nil, apiErr(http.StatusForbidden, "Gym owner access required"))
		svc.On("PutRolePolicy", mock.Anything, "head1", "g1", "coach", mock.Anything, int64(0)).Return(nil, apiErr(http.StatusForbidden, "Gym owner access required"))
		svc.On("PutGrants", mock.Anything, "head1", "g1", "u1", mock.Anything).Return(nil, apiErr(http.StatusForbidden, "Gym owner access required"))
		r := newPolicyRouter(svc, "head1")

		w := get(r, "/gyms/g1/policies/coach")
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Equal(t, "Gym owner access required", decode(t, w)["error"])
		assert.Equal(t, http.StatusForbidden, do(r, http.MethodPut, "/gyms/g1/policies/coach", `{"capabilities":{},"version":0}`).Code)
		assert.Equal(t, http.StatusForbidden, do(r, http.MethodPut, "/gyms/g1/members/u1/grants", `{"grants":[]}`).Code)
	})

	t.Run("foreign gym is 404", func(t *testing.T) {
		svc := &mocks.GymPolicyService{}
		svc.On("GetGrants", mock.Anything, "owner2", "g1", "u1").Return(nil, apiErr(http.StatusNotFound, "gym not found"))
		svc.On("GymContext", mock.Anything, "owner2").Return(nil, apiErr(http.StatusNotFound, "gym not found"))
		r := newPolicyRouter(svc, "owner2")

		w := get(r, "/gyms/g1/members/u1/grants")
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Equal(t, "gym not found", decode(t, w)["error"])
		assert.Equal(t, http.StatusNotFound, get(r, "/me/gym-context").Code)
	})

	t.Run("owner success and 409", func(t *testing.T) {
		svc := &mocks.GymPolicyService{}
		svc.On("GetRolePolicy", mock.Anything, "owner1", "g1", "coach").
			Return(&services.RolePolicyView{GymID: "g1", Role: "coach", Capabilities: map[string]bool{"library.read": true}, Version: 2}, nil)
		svc.On("PutRolePolicy", mock.Anything, "owner1", "g1", "coach", map[string]bool{"workout.write": true}, int64(2)).
			Return(&services.RolePolicyView{GymID: "g1", Role: "coach", Version: 3}, nil)
		svc.On("PutRolePolicy", mock.Anything, "owner1", "g1", "coach", mock.Anything, int64(1)).
			Return(nil, apiErr(http.StatusConflict, "policy was changed by someone else, reload and retry"))
		r := newPolicyRouter(svc, "owner1")

		w := get(r, "/gyms/g1/policies/coach")
		require.Equal(t, http.StatusOK, w.Code)
		assert.EqualValues(t, 2, decode(t, w)["version"])

		w = do(r, http.MethodPut, "/gyms/g1/policies/coach", `{"capabilities":{"workout.write":true},"version":2}`)
		require.Equal(t, http.StatusOK, w.Code)
		assert.EqualValues(t, 3, decode(t, w)["version"])

		w = do(r, http.MethodPut, "/gyms/g1/policies/coach", `{"capabilities":{},"version":1}`)
		assert.Equal(t, http.StatusConflict, w.Code)
	})

	t.Run("bad body is 400 and a 500 hides the cause", func(t *testing.T) {
		svc := &mocks.GymPolicyService{}
		svc.On("GymContext", mock.Anything, "u1").Return(nil, apiErr(http.StatusInternalServerError, "mongo: boom"))
		r := newPolicyRouter(svc, "u1")

		assert.Equal(t, http.StatusBadRequest, do(r, http.MethodPut, "/gyms/g1/policies/coach", `{`).Code)
		assert.Equal(t, http.StatusBadRequest, do(r, http.MethodPut, "/gyms/g1/policies/coach", `{"capabilities":{},"version":-1}`).Code)
		assert.Equal(t, http.StatusBadRequest, do(r, http.MethodPut, "/gyms/g1/members/u2/grants", `nope`).Code)

		w := get(r, "/me/gym-context")
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Equal(t, "internal error", decode(t, w)["error"])
	})
}

func TestInternalAuthzHandler(t *testing.T) {
	body := `{"actor_id":"u1","actor_role":"coach","checks":[{"capability":"library.read","gym_id":"g1","resource":{"type":"gym","id":"g1"}}]}`

	t.Run("decisions", func(t *testing.T) {
		svc := &mocks.GymPolicyService{}
		svc.On("Check", mock.Anything, services.Actor{ID: "u1", Role: "coach"}, mock.MatchedBy(func(c []services.AuthzCheck) bool {
			return len(c) == 1 && c[0].Capability == "library.read" && c[0].GymID == "g1" && c[0].Resource.Type == "gym"
		})).Return(&services.AuthzCheckResult{
			Decisions:  []services.AuthzCheckDecision{{Allowed: true, Reason: "platform_preset", GymID: "g1", WouldDeny: true}},
			Version:    4,
			TTLSeconds: 30,
			Mode:       "report",
		}, nil)
		w := do(newPolicyRouter(svc, ""), http.MethodPost, "/internal/authz/check", body)
		require.Equal(t, http.StatusOK, w.Code)
		m := decode(t, w)
		assert.EqualValues(t, 30, m["ttl_seconds"])
		assert.Equal(t, "report", m["mode"])
		d := m["decisions"].([]interface{})[0].(map[string]interface{})
		assert.Equal(t, true, d["allowed"])
		assert.Equal(t, true, d["would_deny"])
	})

	t.Run("invalid body and service errors", func(t *testing.T) {
		svc := &mocks.GymPolicyService{}
		svc.On("Check", mock.Anything, mock.Anything, mock.Anything).Return(nil, apiErr(http.StatusBadRequest, "checks must contain 1 to 200 entries"))
		r := newPolicyRouter(svc, "")
		assert.Equal(t, http.StatusBadRequest, do(r, http.MethodPost, "/internal/authz/check", `{`).Code)
		w := do(r, http.MethodPost, "/internal/authz/check", `{"actor_id":"u1"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, "checks must contain 1 to 200 entries", decode(t, w)["error"])
	})
}
