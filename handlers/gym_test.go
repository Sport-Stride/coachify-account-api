package handlers_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/handlers"
	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/services"
)

// fakeGym answers the few service calls the handler tests use; any other call panics on the nil embedded interface.
type fakeGym struct {
	services.GymService
	err        *models.ApiError
	lastEmail  string
	lastClient db.GymClientQuery
	lastStatus string
	lastGymID  string
}

func (f *fakeGym) Overview(context.Context, string, string) (*db.GymOverview, *models.ApiError) {
	if f.err != nil {
		return nil, f.err
	}
	return &db.GymOverview{HeadCoaches: 2, Coaches: 5}, nil
}

func (f *fakeGym) Invite(_ context.Context, _, _, email string) (*services.GymInviteResult, *models.ApiError) {
	f.lastEmail = email
	if f.err != nil {
		return nil, f.err
	}
	return &services.GymInviteResult{InvitationID: "inv1", Status: "pending"}, nil
}

func (f *fakeGym) RemoveHeadCoach(context.Context, string, string, string) *models.ApiError {
	return f.err
}

func (f *fakeGym) ListClients(_ context.Context, _, _ string, q db.GymClientQuery) ([]db.GymClientRow, int, *models.ApiError) {
	f.lastClient = q
	if f.err != nil {
		return nil, 0, f.err
	}
	return []db.GymClientRow{{ExternalID: "cl1", Firstname: "A", Email: "a@x.tn", CoachID: "c1", HeadCoachID: "h1"}}, 1, nil
}

func (f *fakeGym) AcceptInvitation(context.Context, string, string) (*services.GymJoined, *models.ApiError) {
	if f.err != nil {
		return nil, f.err
	}
	return &services.GymJoined{GymID: "g1", Name: "Iron"}, nil
}

func (f *fakeGym) MyGym(context.Context, string) (*services.MyGymInfo, *models.ApiError) {
	return nil, f.err
}

func (f *fakeGym) AdminCreateGym(context.Context, string, string, string) (*services.GymView, *models.ApiError) {
	if f.err != nil {
		return nil, f.err
	}
	return &services.GymView{GymID: "g1", Name: "Iron"}, nil
}

func (f *fakeGym) AdminAddHeadCoach(context.Context, string, string, string) *models.ApiError {
	return f.err
}

func newGymRouter(svc services.GymService, userID, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if userID != "" {
			c.Set("userID", userID)
			c.Set("userRole", role)
		}
		c.Next()
	})
	o := r.Group("/gym-owner")
	o.GET("/me", handlers.GetGymOwnerMe(svc))
	o.GET("/gyms/:gym_id/overview", handlers.GetGymOverview(svc))
	o.PATCH("/gyms/:gym_id", handlers.RenameGym(svc))
	o.GET("/gyms/:gym_id/head-coaches", handlers.ListGymHeadCoaches(svc))
	o.DELETE("/gyms/:gym_id/head-coaches/:id", handlers.RemoveGymHeadCoach(svc))
	o.GET("/gyms/:gym_id/coaches", handlers.ListGymCoaches(svc))
	o.GET("/gyms/:gym_id/clients", handlers.ListGymClients(svc))
	o.POST("/gyms/:gym_id/invitations", handlers.InviteGymHeadCoach(svc))
	o.GET("/gyms/:gym_id/invitations", handlers.ListGymInvitations(svc))
	o.DELETE("/gyms/:gym_id/invitations/:id", handlers.RevokeGymInvitation(svc))

	r.GET("/coach/gym-invitations", handlers.ListMyGymInvitations(svc))
	r.POST("/coach/gym-invitations/:id/accept", handlers.AcceptGymInvitation(svc))
	r.POST("/coach/gym-invitations/:id/decline", handlers.DeclineGymInvitation(svc))
	r.GET("/head-coach/gym", handlers.GetMyGym(svc))
	r.DELETE("/head-coach/gym", handlers.LeaveMyGym(svc))

	a := r.Group("/admin")
	a.GET("/gyms", handlers.ListAdminGyms(svc))
	a.POST("/gyms", handlers.CreateAdminGym(svc))
	a.PATCH("/gyms/:id", handlers.UpdateAdminGym(svc))
	a.PUT("/gyms/:id/head-coaches/:hcid", handlers.AddAdminGymHeadCoach(svc))
	a.DELETE("/gyms/:id/head-coaches/:hcid", handlers.RemoveAdminGymHeadCoach(svc))
	a.GET("/gyms/:id/head-coaches", handlers.ListAdminGymHeadCoaches(svc))
	a.GET("/gyms/:id/audit", handlers.ListAdminGymAudit(svc))
	return r
}

var gymRoutes = []struct{ method, path, body string }{
	{"GET", "/gym-owner/me", ""},
	{"GET", "/gym-owner/gyms/g1/overview", ""},
	{"PATCH", "/gym-owner/gyms/g1", `{"name":"x"}`},
	{"GET", "/gym-owner/gyms/g1/head-coaches", ""},
	{"DELETE", "/gym-owner/gyms/g1/head-coaches/h1", ""},
	{"GET", "/gym-owner/gyms/g1/coaches", ""},
	{"GET", "/gym-owner/gyms/g1/clients", ""},
	{"POST", "/gym-owner/gyms/g1/invitations", `{"email":"a@x.tn"}`},
	{"GET", "/gym-owner/gyms/g1/invitations", ""},
	{"DELETE", "/gym-owner/gyms/g1/invitations/i1", ""},
}

func TestGymOwnerEndpointsRequireOwnerRole(t *testing.T) {
	for _, role := range []string{"coach", "client", "admin"} {
		r := newGymRouter(&fakeGym{}, "u1", role)
		for _, tc := range gymRoutes {
			w := do(r, tc.method, tc.path, tc.body)
			assert.Equal(t, http.StatusForbidden, w.Code, role+" "+tc.method+" "+tc.path)
			assert.Equal(t, "Gym owner access required", decode(t, w)["error"])
		}
	}
}

func TestGymEndpointsRequireAuthentication(t *testing.T) {
	r := newGymRouter(&fakeGym{}, "", "")
	for _, tc := range append(gymRoutes,
		struct{ method, path, body string }{"GET", "/coach/gym-invitations", ""},
		struct{ method, path, body string }{"POST", "/coach/gym-invitations/i1/accept", ""},
		struct{ method, path, body string }{"GET", "/head-coach/gym", ""},
		struct{ method, path, body string }{"DELETE", "/head-coach/gym", ""},
	) {
		w := do(r, tc.method, tc.path, tc.body)
		assert.Equal(t, http.StatusUnauthorized, w.Code, tc.method+" "+tc.path)
		assert.Equal(t, "Unauthorized", decode(t, w)["error"])
	}
}

func TestGymCoachEndpointsRequireCoachRole(t *testing.T) {
	r := newGymRouter(&fakeGym{}, "u1", db.RoleGymOwner)
	for _, tc := range []struct{ method, path, msg string }{
		{"GET", "/coach/gym-invitations", "Coach access required"},
		{"POST", "/coach/gym-invitations/i1/accept", "Coach access required"},
		{"POST", "/coach/gym-invitations/i1/decline", "Coach access required"},
		{"GET", "/head-coach/gym", "Head coach access required"},
		{"DELETE", "/head-coach/gym", "Head coach access required"},
	} {
		w := do(r, tc.method, tc.path, "")
		assert.Equal(t, http.StatusForbidden, w.Code, tc.method+" "+tc.path)
		assert.Equal(t, tc.msg, decode(t, w)["error"])
	}
}

func TestGymAdminEndpointsRequireAdmin(t *testing.T) {
	r := newGymRouter(&fakeGym{}, "u1", db.RoleGymOwner)
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/admin/gyms", ""},
		{"POST", "/admin/gyms", `{"name":"x","owner_id":"o1"}`},
		{"PATCH", "/admin/gyms/g1", `{"name":"x"}`},
		{"PUT", "/admin/gyms/g1/head-coaches/h1", ""},
		{"DELETE", "/admin/gyms/g1/head-coaches/h1", ""},
		{"GET", "/admin/gyms/g1/audit", ""},
		{"GET", "/admin/gyms/g1/head-coaches", ""},
	} {
		w := do(r, tc.method, tc.path, tc.body)
		assert.Equal(t, http.StatusForbidden, w.Code, tc.method+" "+tc.path)
		assert.Equal(t, "Admin access required", decode(t, w)["error"])
	}
}

func TestGymErrorShapes(t *testing.T) {
	for _, tc := range []struct {
		code int
		msg  string
	}{
		{http.StatusNotFound, "gym not found"},
		{http.StatusConflict, "already invited"},
		{http.StatusTooManyRequests, "daily invitation limit reached"},
		{http.StatusForbidden, "Gym owner access required"},
	} {
		svc := &fakeGym{err: apiErr(tc.code, tc.msg)}
		r := newGymRouter(svc, "o1", db.RoleGymOwner)
		for _, rt := range []struct{ method, path, body string }{
			{"GET", "/gym-owner/gyms/g1/overview", ""},
			{"POST", "/gym-owner/gyms/g1/invitations", `{"email":"a@x.tn"}`},
		} {
			w := do(r, rt.method, rt.path, rt.body)
			assert.Equal(t, tc.code, w.Code)
			body := decode(t, w)
			assert.Equal(t, tc.msg, body["error"])
			assert.Len(t, body, 1, "error body carries only the error key")
		}
	}
}

func TestGym500HidesDetails(t *testing.T) {
	svc := &fakeGym{err: apiErr(http.StatusInternalServerError, "mongo: dial tcp 10.0.0.5:27017: user=bob@x.tn")}
	r := newGymRouter(svc, "o1", db.RoleGymOwner)
	w := get(r, "/gym-owner/gyms/g1/overview")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, `{"error":"internal error"}`, w.Body.String())

	ra := newGymRouter(svc, "a1", "admin")
	w = do(ra, "POST", "/admin/gyms", `{"name":"x","owner_id":"o1"}`)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, `{"error":"internal error"}`, w.Body.String())
}

func TestInviteHandler(t *testing.T) {
	svc := &fakeGym{}
	r := newGymRouter(svc, "o1", db.RoleGymOwner)

	w := do(r, "POST", "/gym-owner/gyms/g1/invitations", `{"email":"a@x.tn"}`)
	assert.Equal(t, http.StatusAccepted, w.Code)
	assert.Equal(t, map[string]interface{}{"invitation_id": "inv1", "status": "pending"}, decode(t, w))
	assert.Equal(t, "a@x.tn", svc.lastEmail)

	w = do(r, "POST", "/gym-owner/gyms/g1/invitations", `{}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "email is required", decode(t, w)["error"])
}

func TestGymOwnerWritesRespondNoContent(t *testing.T) {
	r := newGymRouter(&fakeGym{}, "o1", db.RoleGymOwner)
	w := do(r, "DELETE", "/gym-owner/gyms/g1/head-coaches/h1", "")
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, w.Body.String())

	ra := newGymRouter(&fakeGym{}, "a1", "admin")
	w = do(ra, "PUT", "/admin/gyms/g1/head-coaches/h1", "")
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestGymClientsHandler(t *testing.T) {
	svc := &fakeGym{}
	r := newGymRouter(svc, "o1", db.RoleGymOwner)

	w := get(r, "/gym-owner/gyms/g1/clients?head_coach_id=h1&coach_id=c1&q=%20al%20&sort=linked_at&page=2&size=5")
	require.Equal(t, http.StatusOK, w.Code)
	body := decode(t, w)
	assert.EqualValues(t, 1, body["total"])
	assert.EqualValues(t, 2, body["page"])
	assert.EqualValues(t, 5, body["size"])
	assert.Equal(t, db.GymClientQuery{HeadCoachID: "h1", CoachID: "c1", Q: "al", Sort: "linked_at", Page: 2, Size: 5}, svc.lastClient)

	// Only the whitelisted client fields are serialized.
	item := body["items"].([]interface{})[0].(map[string]interface{})
	for k := range item {
		assert.Contains(t, []string{"external_id", "firstname", "lastname", "email", "profile_picture", "status",
			"coach_id", "coach_name", "head_coach_id", "linked_at"}, k)
	}
	assert.NotContains(t, strings.ToLower(w.Body.String()), "phone")
	assert.NotContains(t, strings.ToLower(w.Body.String()), "address")

	w = get(r, "/gym-owner/gyms/g1/clients?sort=bogus")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	w = get(r, "/gym-owner/gyms/g1/coaches?status=bogus")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	w = get(r, "/gym-owner/gyms/g1/invitations?status=bogus")
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAcceptAndMyGymHandlers(t *testing.T) {
	r := newGymRouter(&fakeGym{}, "c1", "coach")
	w := do(r, "POST", "/coach/gym-invitations/i1/accept", "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, map[string]interface{}{"gym": map[string]interface{}{"gym_id": "g1", "name": "Iron"}}, decode(t, w))

	w = get(r, "/head-coach/gym")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"gym":null}`, w.Body.String())

	rc := newGymRouter(&fakeGym{err: apiErr(http.StatusConflict, "coach belongs to a team, unassign first")}, "c1", "coach")
	w = do(rc, "POST", "/coach/gym-invitations/i1/accept", "")
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "coach belongs to a team, unassign first", decode(t, w)["error"])
}

func TestAdminCreateGymHandler(t *testing.T) {
	r := newGymRouter(&fakeGym{}, "a1", "admin")
	w := do(r, "POST", "/admin/gyms", `{"name":"Iron","owner_id":"o1"}`)
	assert.Equal(t, http.StatusCreated, w.Code)

	w = do(r, "POST", "/admin/gyms", `{"name":"Iron"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "name and owner_id are required", decode(t, w)["error"])
}

func (f *fakeGym) AdminListGyms(_ context.Context, _, _ int, _, status string) ([]db.AdminGymRow, int, *models.ApiError) {
	f.lastStatus = status
	if f.err != nil {
		return nil, 0, f.err
	}
	return []db.AdminGymRow{{GymID: "g1", Name: "Iron", OwnerEmail: "o@x.tn", OwnerPicture: "p.png"}}, 1, nil
}

func (f *fakeGym) AdminListGymHeadCoaches(_ context.Context, gymID string) ([]db.AdminGymHeadCoachRow, *models.ApiError) {
	f.lastGymID = gymID
	if f.err != nil {
		return nil, f.err
	}
	return []db.AdminGymHeadCoachRow{{ExternalID: "h1", Firstname: "A", Email: "a@x.tn", Status: "Active", AddedByRole: "admin", TeamSize: 3}}, nil
}

func TestAdminListGymsStatusHandler(t *testing.T) {
	svc := &fakeGym{}
	r := newGymRouter(svc, "a1", "admin")
	for _, s := range []string{"active", "archived"} {
		w := get(r, "/admin/gyms?status="+s)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, s, svc.lastStatus)
	}
	w := get(r, "/admin/gyms")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "", svc.lastStatus)
	row := decode(t, w)["gyms"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, "o@x.tn", row["owner_email"])
	assert.Equal(t, "p.png", row["owner_profile_picture"])

	w = get(r, "/admin/gyms?status=bogus")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "Invalid status", decode(t, w)["error"])
}

func TestAdminListGymHeadCoachesHandler(t *testing.T) {
	svc := &fakeGym{}
	r := newGymRouter(svc, "a1", "admin")
	w := get(r, "/admin/gyms/g1/head-coaches")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "g1", svc.lastGymID)
	body := decode(t, w)
	assert.EqualValues(t, 1, body["total"])
	hc := body["head_coaches"].([]interface{})[0].(map[string]interface{})
	for _, k := range []string{"external_id", "firstname", "lastname", "email", "status", "added_at", "added_by_role", "team_size"} {
		assert.Contains(t, hc, k)
	}
	assert.EqualValues(t, 3, hc["team_size"])

	nf := newGymRouter(&fakeGym{err: apiErr(http.StatusNotFound, "gym not found")}, "a1", "admin")
	w = get(nf, "/admin/gyms/g1/head-coaches")
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "gym not found", decode(t, w)["error"])
}
