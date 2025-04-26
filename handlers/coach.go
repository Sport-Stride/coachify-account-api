package handlers

import (
	"coachify-account-api/models/api"
	"coachify-account-api/services"

	"net/http"

	"github.com/gin-gonic/gin"
)

func InviteClient(coachService services.CoachService) gin.HandlerFunc {
	return func(c *gin.Context) {
		coachID, exists := c.Get("userID")
		if !exists || coachID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		var req struct {
			Email  string   `json:"email"`
			Emails []string `json:"emails"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		if req.Email != "" {
			if err := coachService.InviteClient(c.Request.Context(), req.Email, coachID.(string)); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusCreated, gin.H{"message": "Invitation sent"})
		} else if len(req.Emails) > 0 {
			success, failed := coachService.InviteMultipleClientsBulk(c.Request.Context(), req.Emails, coachID.(string))
			c.JSON(http.StatusCreated, gin.H{
				"message":       "Bulk invitations processed",
				"successful":    success,
				"failed_emails": failed,
			})
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no valid email(s) provided"})
		}
	}
}

// Handler for registration with invitation code
func RegisterWithInvitation(authService services.AuthService, coachService services.CoachService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Code string `json:"invite_code" binding:"required"`
			api.CreateUserRequest
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
			return
		}
		resp, err := coachService.RegisterClientWithInvitation(c.Request.Context(), req.Code, &req.CreateUserRequest)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, resp)
	}
}

// CRUD endpoints for invitations (list, delete)
func ListInvitations(coachService services.CoachService) gin.HandlerFunc {
	return func(c *gin.Context) {
		coachID, exists := c.Get("userID")
		if !exists || coachID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		invs, err := coachService.ListInvitations(c.Request.Context(), coachID.(string))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, invs)
	}
}
func DeleteInvitation(coachService services.CoachService) gin.HandlerFunc {
	return func(c *gin.Context) {
		code := c.Param("code")
		if code == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing code"})
			return
		}
		if err := coachService.DeleteInvitation(c.Request.Context(), code); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "Invitation deleted"})
	}
}
