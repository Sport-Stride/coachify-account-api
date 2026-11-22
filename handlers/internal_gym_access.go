package handlers

import (
	"net/http"

	"coachify-account-api/services"

	"github.com/gin-gonic/gin"
)

type internalGymAccessRequest struct {
	UserID string `json:"user_id"`
	GymID  string `json:"gym_id"`
}

// InternalGymAccess tells schedule-api a user's standing in a gym: active staff role and active client status
// (x-api-key route).
func InternalGymAccess(svc services.GymClientService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req internalGymAccessRequest
		if c.ShouldBindJSON(&req) != nil || req.UserID == "" || req.GymID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "user_id and gym_id are required"})
			return
		}
		res, apiErr := svc.Access(c.Request.Context(), req.UserID, req.GymID)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, res)
	}
}
