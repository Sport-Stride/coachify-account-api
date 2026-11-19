package handlers

import (
	"net/http"

	"coachify-account-api/services"

	"github.com/gin-gonic/gin"
)

type internalAuthzRequest struct {
	ActorID   string                `json:"actor_id"`
	ActorRole string                `json:"actor_role"`
	Checks    []services.AuthzCheck `json:"checks"`
}

// CheckInternalAuthz answers a batch of capability checks for WMS and tracker (x-api-key route).
// With GYM_AUTHZ_ENFORCE off it runs in report mode: every decision is allowed and denials carry would_deny.
func CheckInternalAuthz(svc services.GymPolicyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req internalAuthzRequest
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		res, apiErr := svc.Check(c.Request.Context(), services.Actor{ID: req.ActorID, Role: req.ActorRole}, req.Checks)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, res)
	}
}
