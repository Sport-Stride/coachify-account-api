package handlers

import (
	"net/http"

	"coachify-account-api/services"

	"github.com/gin-gonic/gin"
)

type internalRelationshipsRequest struct {
	ActorID  string `json:"actor_id"`
	ClientID string `json:"client_id"`
}

// InternalClientRelationships tells tracker and WMS how an actor reaches a client: personally, through which gyms,
// and in which role (x-api-key route).
func InternalClientRelationships(svc services.GymClientService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req internalRelationshipsRequest
		if c.ShouldBindJSON(&req) != nil || req.ActorID == "" || req.ClientID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "actor_id and client_id are required"})
			return
		}
		res, apiErr := svc.Relationships(c.Request.Context(), req.ActorID, req.ClientID)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, res)
	}
}
