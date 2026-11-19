package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"coachify-account-api/services"
)

// inviteCaller returns the authenticated user id or answers 401.
func inviteCaller(c *gin.Context) (string, bool) {
	id := c.GetString("userID")
	if id == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return "", false
	}
	return id, true
}

// CreateInviteLink creates an invite link of any kind; the raw token is in the returned link and nowhere else.
func CreateInviteLink(svc services.InviteLinkService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := inviteCaller(c)
		if !ok {
			return
		}
		var req services.CreateInvite
		if err := c.ShouldBindJSON(&req); err != nil || req.Kind == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "kind is required"})
			return
		}
		res, apiErr := svc.Create(c.Request.Context(), services.Actor{ID: id, Role: c.GetString("userRole")}, req)
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusCreated, res)
	}
}

// ListInviteLinks lists the caller's links, or every link of a gym for a caller holding member.invite there.
func ListInviteLinks(svc services.InviteLinkService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := inviteCaller(c)
		if !ok {
			return
		}
		rows, apiErr := svc.List(c.Request.Context(), id, c.Query("gym_id"), c.Query("status"))
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"links": rows})
	}
}

// RevokeInviteLink revokes a link. The route parameter is named token because gin requires one wildcard name per
// path position; here it carries the link id.
func RevokeInviteLink(svc services.InviteLinkService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := inviteCaller(c)
		if !ok {
			return
		}
		if apiErr := svc.Revoke(c.Request.Context(), id, c.Param("token")); apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

// LookupInviteLink is the public lookup: invalid, used, expired and revoked tokens give the same 404.
func LookupInviteLink(svc services.InviteLinkService) gin.HandlerFunc {
	return func(c *gin.Context) {
		res, apiErr := svc.Lookup(c.Request.Context(), c.Param("token"))
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, res)
	}
}

// RedeemInviteLink redeems a link for the signed-in user. An email-bound gym invite is not redeemed: 202 with
// status pending_acceptance, to be accepted through /me/invites.
func RedeemInviteLink(svc services.InviteLinkService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := inviteCaller(c)
		if !ok {
			return
		}
		res, apiErr := svc.Redeem(c.Request.Context(), id, c.Param("token"))
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		code := http.StatusOK
		if res.Status == services.RedeemPendingAcceptance {
			code = http.StatusAccepted
		}
		c.JSON(code, res)
	}
}

// ListMyInvites lists the email-bound gym invites waiting for the signed-in client.
func ListMyInvites(svc services.InviteLinkService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := inviteCaller(c)
		if !ok {
			return
		}
		rows, apiErr := svc.ListMine(c.Request.Context(), id)
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"invites": rows})
	}
}

// AcceptMyInvite accepts an email-bound gym invite (the client's consent).
func AcceptMyInvite(svc services.InviteLinkService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := inviteCaller(c)
		if !ok {
			return
		}
		res, apiErr := svc.Accept(c.Request.Context(), id, c.Param("id"))
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, res)
	}
}

// DeclineMyInvite declines an email-bound gym invite.
func DeclineMyInvite(svc services.InviteLinkService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := inviteCaller(c)
		if !ok {
			return
		}
		if apiErr := svc.Decline(c.Request.Context(), id, c.Param("id")); apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.Status(http.StatusNoContent)
	}
}
