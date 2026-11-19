package handlers

import (
	"net/http"

	"coachify-account-api/services"

	"github.com/gin-gonic/gin"
)

// GetInternalUserEntitlements answers GET /internal/users/:id/entitlements?context=personal|gym&gym_id= for WMS and
// invitation-api (x-api-key route): {context, features[], seat_only, seat_state, source}. A nil svc (seats off) is 404.
func GetInternalUserEntitlements(svc services.ContextEntitlementResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		if svc == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		kind := c.DefaultQuery("context", services.EntContextPersonal)
		res, apiErr := svc.Resolve(c.Request.Context(), c.Param("id"), services.EntContext{Kind: kind, GymID: c.Query("gym_id")})
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, res)
	}
}
