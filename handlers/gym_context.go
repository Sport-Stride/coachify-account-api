package handlers

import (
	"net/http"

	"coachify-account-api/services"

	"github.com/gin-gonic/gin"
)

// GetMyGymContext returns the caller's gym memberships with resolved capabilities (any authenticated role).
// A caller in no gym is in personal context: empty memberships, and no gym policy gates his personal clients.
func GetMyGymContext(svc services.GymPolicyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetString("userID")
		if id == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		v, apiErr := svc.GymContext(c.Request.Context(), id)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, v)
	}
}
