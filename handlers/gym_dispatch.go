package handlers

import (
	"net/http"

	"coachify-account-api/services"

	"github.com/gin-gonic/gin"
)

// LeaveMyGymAsClient ends the caller's gym client relationship (POST /me/gyms/:gym_id/leave). Idempotent.
func LeaveMyGymAsClient(svc services.GymClientLeaver) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetString("userID")
		if id == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		if apiErr := svc.LeaveGym(c.Request.Context(), id, c.Param("gym_id")); apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "You left the gym"})
	}
}
