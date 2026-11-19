package handlers_test

import (
	"net/http"
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

func newInviteRouter(svc *mocks.InviteLinkService, userID string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/invite-links/:token", handlers.LookupInviteLink(svc))
	p := r.Group("/")
	p.Use(func(c *gin.Context) {
		if userID != "" {
			c.Set("userID", userID)
			c.Set("userRole", "coach")
		}
		c.Next()
	})
	p.POST("/invite-links", handlers.CreateInviteLink(svc))
	p.GET("/invite-links", handlers.ListInviteLinks(svc))
	p.DELETE("/invite-links/:token", handlers.RevokeInviteLink(svc))
	p.POST("/invite-links/:token/redeem", handlers.RedeemInviteLink(svc))
	p.GET("/me/invites", handlers.ListMyInvites(svc))
	p.POST("/me/invites/:id/accept", handlers.AcceptMyInvite(svc))
	p.POST("/me/invites/:id/decline", handlers.DeclineMyInvite(svc))
	return r
}

func TestInviteLinkHandlers(t *testing.T) {
	t.Run("create passes the actor and returns 201 with the link", func(t *testing.T) {
		svc := &mocks.InviteLinkService{}
		svc.On("Create", mock.Anything, services.Actor{ID: "u1", Role: "coach"}, services.CreateInvite{Kind: "client_gym", GymID: "g1"}).
			Return(&services.InviteCreated{ID: "i1", Link: "https://app/invite/raw"}, nil)
		w := do(newInviteRouter(svc, "u1"), http.MethodPost, "/invite-links", `{"kind":"client_gym","gym_id":"g1"}`)
		require.Equal(t, http.StatusCreated, w.Code)
		body := decode(t, w)
		assert.Equal(t, "i1", body["id"])
		assert.Equal(t, "https://app/invite/raw", body["link"])
	})
	t.Run("create validates the body and needs a caller", func(t *testing.T) {
		svc := &mocks.InviteLinkService{}
		assert.Equal(t, http.StatusBadRequest, do(newInviteRouter(svc, "u1"), http.MethodPost, "/invite-links", `{}`).Code)
		assert.Equal(t, http.StatusUnauthorized, do(newInviteRouter(svc, ""), http.MethodPost, "/invite-links", `{"kind":"x"}`).Code)
		svc.AssertNotCalled(t, "Create")
	})
	t.Run("service error keeps the {error} body", func(t *testing.T) {
		svc := &mocks.InviteLinkService{}
		svc.On("Create", mock.Anything, mock.Anything, mock.Anything).Return(nil, apiErr(http.StatusForbidden, "no"))
		w := do(newInviteRouter(svc, "u1"), http.MethodPost, "/invite-links", `{"kind":"client_gym"}`)
		require.Equal(t, http.StatusForbidden, w.Code)
		assert.Equal(t, "no", decode(t, w)["error"])
	})
	t.Run("public lookup 200 and uniform 404", func(t *testing.T) {
		svc := &mocks.InviteLinkService{}
		svc.On("Lookup", mock.Anything, "good").Return(&services.InviteLookup{Kind: "client_gym", GymName: "G", EmailBound: true}, nil)
		svc.On("Lookup", mock.Anything, "bad").Return(nil, &models.ApiError{Code: http.StatusNotFound, Error: models.ErrInviteNotFound})
		r := newInviteRouter(svc, "")
		w := do(r, http.MethodGet, "/invite-links/good", "")
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, true, decode(t, w)["email_bound"])
		assert.Equal(t, http.StatusNotFound, do(r, http.MethodGet, "/invite-links/bad", "").Code)
	})
	t.Run("redeem answers 200, or 202 for a pending acceptance", func(t *testing.T) {
		svc := &mocks.InviteLinkService{}
		svc.On("Redeem", mock.Anything, "u1", "t1").Return(&services.RedeemResult{Kind: "client_gym", Status: services.RedeemJoined}, nil)
		svc.On("Redeem", mock.Anything, "u1", "t2").Return(&services.RedeemResult{Kind: "client_gym", Status: services.RedeemPendingAcceptance, InviteID: "i2"}, nil)
		r := newInviteRouter(svc, "u1")
		assert.Equal(t, http.StatusOK, do(r, http.MethodPost, "/invite-links/t1/redeem", "").Code)
		w := do(r, http.MethodPost, "/invite-links/t2/redeem", "")
		assert.Equal(t, http.StatusAccepted, w.Code)
		assert.Equal(t, "pending_acceptance", decode(t, w)["status"])
	})
	t.Run("list, revoke, my invites, accept, decline", func(t *testing.T) {
		svc := &mocks.InviteLinkService{}
		svc.On("List", mock.Anything, "u1", "g1", "active").Return([]services.InviteLinkView{{ID: "i1"}}, nil)
		svc.On("Revoke", mock.Anything, "u1", "i1").Return(nil)
		svc.On("ListMine", mock.Anything, "u1").Return([]services.MyInvite{{ID: "i2", GymID: "g1"}}, nil)
		svc.On("Accept", mock.Anything, "u1", "i2").Return(&services.RedeemResult{Status: services.RedeemJoined}, nil)
		svc.On("Decline", mock.Anything, "u1", "i2").Return(nil)
		r := newInviteRouter(svc, "u1")
		w := do(r, http.MethodGet, "/invite-links?gym_id=g1&status=active", "")
		require.Equal(t, http.StatusOK, w.Code)
		assert.Len(t, decode(t, w)["links"], 1)
		assert.Equal(t, http.StatusNoContent, do(r, http.MethodDelete, "/invite-links/i1", "").Code)
		w = do(r, http.MethodGet, "/me/invites", "")
		require.Equal(t, http.StatusOK, w.Code)
		assert.Len(t, decode(t, w)["invites"], 1)
		assert.Equal(t, http.StatusOK, do(r, http.MethodPost, "/me/invites/i2/accept", "").Code)
		assert.Equal(t, http.StatusNoContent, do(r, http.MethodPost, "/me/invites/i2/decline", "").Code)
	})
}
