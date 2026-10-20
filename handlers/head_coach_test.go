package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"coachify-account-api/handlers"
	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/services"
	"coachify-account-api/services/mocks"
)

// newRouter wires the handlers behind a fake auth middleware that sets the token claims.
func newRouter(svc services.TeamService, userID, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("userID", userID)
		c.Set("userRole", role)
		c.Next()
	})
	r.GET("/admin/head-coaches", handlers.ListHeadCoaches(svc))
	r.POST("/admin/head-coaches/:id", handlers.PromoteHeadCoach(svc))
	r.DELETE("/admin/head-coaches/:id", handlers.DemoteHeadCoach(svc))
	r.PUT("/admin/coaches/:id/head-coach", handlers.AssignCoachToHeadCoach(svc))
	r.DELETE("/admin/coaches/:id/head-coach", handlers.UnassignCoach(svc))
	r.GET("/head-coach/me", handlers.GetHeadCoachMe(svc))
	r.GET("/head-coach/overview", handlers.GetTeamOverview(svc))
	r.GET("/head-coach/coach-ids", handlers.GetMyTeamCoachIDs(svc))
	r.GET("/head-coach/coaches", handlers.ListMyTeamCoaches(svc))
	r.GET("/head-coach/coaches/:id", handlers.GetMyTeamCoach(svc))
	r.DELETE("/head-coach/coaches/:id", handlers.RemoveMyTeamCoach(svc))
	r.GET("/coach/team", handlers.GetMyHeadCoach(svc))
	return r
}

func do(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m))
	return m
}

func TestAdminEndpointsRequireAdmin(t *testing.T) {
	svc := &mocks.TeamService{}
	r := newRouter(svc, "c1", "coach")

	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/admin/head-coaches", ""},
		{"POST", "/admin/head-coaches/c2", ""},
		{"DELETE", "/admin/head-coaches/c2", ""},
		{"PUT", "/admin/coaches/c2/head-coach", `{"head_coach_id":"h1"}`},
		{"DELETE", "/admin/coaches/c2/head-coach", ""},
	} {
		w := do(r, tc.method, tc.path, tc.body)
		assert.Equal(t, http.StatusForbidden, w.Code, tc.method+" "+tc.path)
		assert.Equal(t, "Admin access required", decode(t, w)["error"])
	}
	svc.AssertExpectations(t)
}

func TestAssignValidatesBody(t *testing.T) {
	svc := &mocks.TeamService{}
	r := newRouter(svc, "admin1", "admin")

	w := do(r, "PUT", "/admin/coaches/c2/head-coach", `{}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, decode(t, w), "error")
}

func TestAssignOK(t *testing.T) {
	svc := &mocks.TeamService{}
	svc.On("Assign", mock.Anything, "admin1", "c2", "h1").
		Return(&db.TeamMember{CoachID: "c2", HeadCoachID: "h1"}, nil)
	r := newRouter(svc, "admin1", "admin")

	w := do(r, "PUT", "/admin/coaches/c2/head-coach", `{"head_coach_id":"h1"}`)
	require.Equal(t, http.StatusOK, w.Code)
	membership := decode(t, w)["membership"].(map[string]interface{})
	assert.Equal(t, "c2", membership["coach_id"])
	assert.Equal(t, "h1", membership["head_coach_id"])
}

func TestPromoteConflictPassesServiceError(t *testing.T) {
	svc := &mocks.TeamService{}
	svc.On("Promote", mock.Anything, "admin1", "c2").
		Return(nil, &models.ApiError{Code: http.StatusConflict, Error: assertErr("coach belongs to a team, unassign first")})
	r := newRouter(svc, "admin1", "admin")

	w := do(r, "POST", "/admin/head-coaches/c2", "")
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "coach belongs to a team, unassign first", decode(t, w)["error"])
}

func TestListFiltersAreValidated(t *testing.T) {
	svc := &mocks.TeamService{}
	r := newRouter(svc, "h1", "coach")

	for _, path := range []string{
		"/head-coach/coaches?status=Bogus",
		"/head-coach/coaches?sort=password",
	} {
		w := do(r, "GET", path, "")
		assert.Equal(t, http.StatusBadRequest, w.Code, path)
		assert.Contains(t, decode(t, w), "error")
	}
	svc.AssertNotCalled(t, "ListMyCoaches", mock.Anything, mock.Anything, mock.Anything)
}

func TestListQueryIsTrimmedAndCapped(t *testing.T) {
	svc := &mocks.TeamService{}
	long := strings.Repeat("a", 100)
	svc.On("ListMyCoaches", mock.Anything, "h1", mock.MatchedBy(func(q db.TeamListQuery) bool {
		return q.Q == strings.Repeat("a", 64) && q.Status == "To Confirm" && q.Sort == "last_login"
	})).Return([]db.TeamCoachRow{}, 0, nil)
	r := newRouter(svc, "h1", "coach")

	w := do(r, "GET", "/head-coach/coaches?status=To+Confirm&sort=last_login&q=+"+long, "")
	assert.Equal(t, http.StatusOK, w.Code)
	svc.AssertExpectations(t)
}

func TestDemoteReturnsReleased(t *testing.T) {
	svc := &mocks.TeamService{}
	svc.On("Demote", mock.Anything, "admin1", "h1").Return(4, nil)
	r := newRouter(svc, "admin1", "admin")

	w := do(r, "DELETE", "/admin/head-coaches/h1", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.EqualValues(t, 4, decode(t, w)["released"])
}

func TestHeadCoachEndpointsRejectNonCoachToken(t *testing.T) {
	svc := &mocks.TeamService{}
	for _, role := range []string{"client", "admin", "nutritionist"} {
		r := newRouter(svc, "u1", role)
		for _, p := range []string{"/head-coach/me", "/head-coach/overview", "/head-coach/coach-ids", "/head-coach/coaches", "/head-coach/coaches/c1"} {
			w := do(r, "GET", p, "")
			assert.Equal(t, http.StatusForbidden, w.Code, role+" "+p)
		}
	}
	svc.AssertExpectations(t)
}

func TestHeadCoachServiceForbidden(t *testing.T) {
	svc := &mocks.TeamService{}
	svc.On("Overview", mock.Anything, "c1").
		Return(nil, &models.ApiError{Code: http.StatusForbidden, Error: assertErr("Head coach access required")})
	r := newRouter(svc, "c1", "coach")

	w := do(r, "GET", "/head-coach/overview", "")
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, "Head coach access required", decode(t, w)["error"])
}

func TestHeadCoachListDefaultsAndCaps(t *testing.T) {
	svc := &mocks.TeamService{}
	svc.On("ListMyCoaches", mock.Anything, "h1", db.TeamListQuery{Page: 1, Size: 20, Q: "ann", Status: "Active", Sort: "clients"}).
		Return([]db.TeamCoachRow{{ExternalID: "c1"}}, 1, nil)
	r := newRouter(svc, "h1", "coach")

	w := do(r, "GET", "/head-coach/coaches?q=ann&status=Active&sort=clients&size=1000", "")
	require.Equal(t, http.StatusOK, w.Code)
	body := decode(t, w)
	assert.EqualValues(t, 1, body["total"])
	assert.EqualValues(t, 1, body["page"])
	assert.EqualValues(t, 20, body["size"])
	assert.Len(t, body["coaches"], 1)
}

func TestMeAndMyHeadCoach(t *testing.T) {
	svc := &mocks.TeamService{}
	svc.On("Me", mock.Anything, "c1").Return(&services.HeadCoachMe{}, nil)
	svc.On("MyHeadCoach", mock.Anything, "c1").Return(nil, nil)
	r := newRouter(svc, "c1", "coach")

	w := do(r, "GET", "/head-coach/me", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, false, decode(t, w)["is_head_coach"])

	w = do(r, "GET", "/coach/team", "")
	require.Equal(t, http.StatusOK, w.Code)
	body := decode(t, w)
	assert.Contains(t, body, "head_coach")
	assert.Nil(t, body["head_coach"])
}

func TestRemoveMyCoach(t *testing.T) {
	svc := &mocks.TeamService{}
	svc.On("RemoveMyCoach", mock.Anything, "h1", "c1").Return(nil)
	svc.On("RemoveMyCoach", mock.Anything, "h1", "c9").
		Return(&models.ApiError{Code: http.StatusNotFound, Error: assertErr("coach not found")})
	r := newRouter(svc, "h1", "coach")

	w := do(r, "DELETE", "/head-coach/coaches/c1", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "removed", decode(t, w)["message"])

	w = do(r, "DELETE", "/head-coach/coaches/c9", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

type assertErr string

func (e assertErr) Error() string { return string(e) }
