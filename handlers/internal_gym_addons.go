package handlers

import (
	"net/http"

	"coachify-account-api/services"

	"github.com/gin-gonic/gin"
)

type internalResolveCoachesRequest struct {
	CoachIDs []string `json:"coach_ids"`
}

// ResolveInternalGymCoaches answers name, picture, role and eligibility of coach ids in a gym (x-api-key route used
// by payments-api to validate and display the coach of a coaching add-on plan).
func ResolveInternalGymCoaches(svc services.GymAddonService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req internalResolveCoachesRequest
		if c.ShouldBindJSON(&req) != nil || c.Param("gym_id") == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "coach_ids is required"})
			return
		}
		coaches, apiErr := svc.ResolveCoaches(c.Request.Context(), c.Param("gym_id"), req.CoachIDs)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"coaches": coaches})
	}
}

type internalAddonPurchasedRequest struct {
	GymID          string `json:"gym_id"`
	ClientID       string `json:"client_id"`
	CoachID        string `json:"coach_id"`
	PlanID         string `json:"plan_id"`
	PlanName       string `json:"plan_name"`
	SubscriptionID string `json:"subscription_id"`
}

// InternalGymAddonPurchased assigns the coach of a coaching add-on purchase to the client and notifies the coach and
// the gym owner (x-api-key route called by payments-api after the purchase is active).
func InternalGymAddonPurchased(svc services.GymAddonService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req internalAddonPurchasedRequest
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		apiErr := svc.OnPurchased(c.Request.Context(), services.GymAddonPurchase{
			GymID: req.GymID, ClientID: req.ClientID, CoachID: req.CoachID, PlanID: req.PlanID,
			PlanName: req.PlanName, SubscriptionID: req.SubscriptionID,
		})
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.Status(http.StatusNoContent)
	}
}
