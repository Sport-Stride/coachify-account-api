package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"coachify-account-api/services"
)

// GetCoachPublicProfile serves GET /coaches/:coach_id/profile to any signed-in user.
func GetCoachPublicProfile(svc services.CoachProfileService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		profile, apiErr := svc.GetPublicProfile(ctx.Request.Context(), ctx.Param("coach_id"))
		if apiErr != nil {
			ctx.JSON(apiErr.Code, gin.H{"error": apiErr.Error.Error()})
			return
		}
		ctx.JSON(http.StatusOK, profile)
	}
}
