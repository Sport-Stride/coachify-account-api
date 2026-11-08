package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"coachify-account-api/handlers"
	"coachify-account-api/models"
	"coachify-account-api/services"
	"coachify-account-api/services/mocks"
)

// inviteRouter mirrors the invite routes of router.initializeRoutes; the fake auth middleware sets the caller's role.
func inviteRouter(svc *mocks.GymOwnerInviteService, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/gym-owner-invites/:token", handlers.LookupGymOwnerInvite(svc))
	admin := r.Group("/admin", func(c *gin.Context) {
		c.Set("userID", "admin1")
		c.Set("userRole", role)
	})
	admin.POST("/gym-owner-invites", handlers.CreateGymOwnerInvite(svc))
	admin.GET("/gym-owner-invites", handlers.ListGymOwnerInvites(svc))
	admin.DELETE("/gym-owner-invites/:id", handlers.RevokeGymOwnerInvite(svc))
	admin.POST("/gym-owner-invites/:id/resend", handlers.ResendGymOwnerInvite(svc))
	return r
}

func doInvite(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func errBody(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var b map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &b))
	require.Contains(t, b, "error")
	return b["error"]
}

func TestInviteAdminRoutesRequireAdmin(t *testing.T) {
	svc := &mocks.GymOwnerInviteService{}
	r := inviteRouter(svc, "client")
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/admin/gym-owner-invites", `{"firstname":"A","lastname":"L","email":"a@b.co"}`},
		{http.MethodGet, "/admin/gym-owner-invites", ""},
		{http.MethodDelete, "/admin/gym-owner-invites/abc", ""},
		{http.MethodPost, "/admin/gym-owner-invites/abc/resend", ""},
	} {
		w := doInvite(r, tc.method, tc.path, tc.body)
		assert.Equal(t, http.StatusForbidden, w.Code, tc.method+" "+tc.path)
		assert.NotEmpty(t, errBody(t, w))
	}
	svc.AssertNotCalled(t, "Create", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestInviteCreate(t *testing.T) {
	t.Run("201 with the invite", func(t *testing.T) {
		svc := &mocks.GymOwnerInviteService{}
		sent := true
		svc.On("Create", mock.Anything, "admin1", "A", "L", "a@b.co").
			Return(&services.GymOwnerInviteView{ID: "i1", Email: "a@b.co", Status: "pending", EmailSent: &sent}, nil)
		w := doInvite(inviteRouter(svc, "admin"), http.MethodPost, "/admin/gym-owner-invites", `{"firstname":"A","lastname":"L","email":"a@b.co"}`)
		require.Equal(t, http.StatusCreated, w.Code)
		var got map[string]interface{}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
		assert.Equal(t, "i1", got["id"])
		assert.Equal(t, true, got["email_sent"])
		assert.NotContains(t, got, "token_hash")
	})
	t.Run("bad body is 400 with error body", func(t *testing.T) {
		w := doInvite(inviteRouter(&mocks.GymOwnerInviteService{}, "admin"), http.MethodPost, "/admin/gym-owner-invites", `{`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.NotEmpty(t, errBody(t, w))
	})
	t.Run("service 409 and 500", func(t *testing.T) {
		svc := &mocks.GymOwnerInviteService{}
		svc.On("Create", mock.Anything, "admin1", "A", "L", "dup@b.co").Return(nil, apiErr(http.StatusConflict, "email already exists"))
		svc.On("Create", mock.Anything, "admin1", "A", "L", "boom@b.co").Return(nil, apiErr(http.StatusInternalServerError, "mongo: secret detail"))
		r := inviteRouter(svc, "admin")

		w := doInvite(r, http.MethodPost, "/admin/gym-owner-invites", `{"firstname":"A","lastname":"L","email":"dup@b.co"}`)
		assert.Equal(t, http.StatusConflict, w.Code)
		assert.Equal(t, "email already exists", errBody(t, w))

		w = doInvite(r, http.MethodPost, "/admin/gym-owner-invites", `{"firstname":"A","lastname":"L","email":"boom@b.co"}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Equal(t, "internal error", errBody(t, w))
	})
}

func TestInviteListRevokeResend(t *testing.T) {
	svc := &mocks.GymOwnerInviteService{}
	svc.On("List", mock.Anything, "pending", 2, 5).Return(&services.GymOwnerInviteList{Invites: []services.GymOwnerInviteView{}, Total: 0, Page: 2, Size: 5}, nil)
	svc.On("Revoke", mock.Anything, "admin1", "ok").Return(nil)
	svc.On("Revoke", mock.Anything, "admin1", "used").Return(apiErr(http.StatusConflict, "invitation can no longer be used"))
	svc.On("Resend", mock.Anything, "admin1", "ok").Return(&services.GymOwnerInviteView{ID: "ok", Status: "pending"}, nil)
	svc.On("Resend", mock.Anything, "admin1", "used").Return(nil, apiErr(http.StatusConflict, "invitation can no longer be used"))
	r := inviteRouter(svc, "admin")

	w := doInvite(r, http.MethodGet, "/admin/gym-owner-invites?status=pending&page=2&size=5", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"invites":[]`)

	w = doInvite(r, http.MethodDelete, "/admin/gym-owner-invites/ok", "")
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, w.Body.String())

	w = doInvite(r, http.MethodDelete, "/admin/gym-owner-invites/used", "")
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.NotEmpty(t, errBody(t, w))

	w = doInvite(r, http.MethodPost, "/admin/gym-owner-invites/ok/resend", "")
	assert.Equal(t, http.StatusOK, w.Code)

	w = doInvite(r, http.MethodPost, "/admin/gym-owner-invites/used/resend", "")
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestInvitePublicLookupSameBodyForEveryFailure(t *testing.T) {
	svc := &mocks.GymOwnerInviteService{}
	svc.On("Lookup", mock.Anything, "good").Return(&services.GymOwnerInviteLookup{Email: "a@b.co", Firstname: "A", Lastname: "L"}, nil)
	for _, tok := range []string{"expired", "revoked", "used", "unknown"} {
		svc.On("Lookup", mock.Anything, tok).Return(nil, &models.ApiError{Code: http.StatusNotFound, Error: models.ErrInviteNotFound})
	}
	r := inviteRouter(svc, "")

	w := doInvite(r, http.MethodGet, "/gym-owner-invites/good", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"email":"a@b.co"`)

	var first string
	for _, tok := range []string{"expired", "revoked", "used", "unknown"} {
		w := doInvite(r, http.MethodGet, "/gym-owner-invites/"+tok, "")
		assert.Equal(t, http.StatusNotFound, w.Code)
		if first == "" {
			first = w.Body.String()
		}
		assert.Equal(t, first, w.Body.String())
	}
	assert.JSONEq(t, `{"error":"invitation not found or expired"}`, first)
}
