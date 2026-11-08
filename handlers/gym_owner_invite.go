package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"coachify-account-api/services"
)

type createGymOwnerInviteRequest struct {
	Firstname string `json:"firstname"`
	Lastname  string `json:"lastname"`
	Email     string `json:"email"`
}

// CreateGymOwnerInvite issues a gym owner invitation and emails the signup link; the link is never returned.
func CreateGymOwnerInvite(svc services.GymOwnerInviteService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		var req createGymOwnerInviteRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		inv, apiErr := svc.Create(c.Request.Context(), c.GetString("userID"), req.Firstname, req.Lastname, req.Email)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusCreated, inv)
	}
}

// ListGymOwnerInvites returns invitations newest first, optionally filtered by status.
func ListGymOwnerInvites(svc services.GymOwnerInviteService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		page, size := pagination(c)
		list, apiErr := svc.List(c.Request.Context(), c.Query("status"), page, size)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, list)
	}
}

// RevokeGymOwnerInvite revokes a pending invitation.
func RevokeGymOwnerInvite(svc services.GymOwnerInviteService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		if apiErr := svc.Revoke(c.Request.Context(), c.GetString("userID"), c.Param("id")); apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

// ResendGymOwnerInvite rotates the token of a pending invitation and emails the new link.
func ResendGymOwnerInvite(svc services.GymOwnerInviteService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		inv, apiErr := svc.Resend(c.Request.Context(), c.GetString("userID"), c.Param("id"))
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, inv)
	}
}

// LookupGymOwnerInvite is the public invite lookup; every failure gives the same 404 body.
func LookupGymOwnerInvite(svc services.GymOwnerInviteService) gin.HandlerFunc {
	return func(c *gin.Context) {
		inv, apiErr := svc.Lookup(c.Request.Context(), c.Param("token"))
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, inv)
	}
}
