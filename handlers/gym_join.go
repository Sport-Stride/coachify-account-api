package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"coachify-account-api/services"
)

// CreateGymJoinLink lets a gym owner generate a single-use open join link.
func CreateGymJoinLink(svc services.GymService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := gymOwnerCaller(c)
		if !ok {
			return
		}
		res, apiErr := svc.CreateJoinLink(c.Request.Context(), id, c.Param("gym_id"))
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusCreated, res)
	}
}

// LookupGymJoin is the public join link lookup; every unusable token gives the same 404.
func LookupGymJoin(svc services.GymService) gin.HandlerFunc {
	return func(c *gin.Context) {
		res, apiErr := svc.LookupJoin(c.Request.Context(), c.Param("token"))
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, res)
	}
}

// AcceptGymJoin lets the signed-in coach join the gym behind a join link token.
func AcceptGymJoin(svc services.GymService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := gymCoachCaller(c)
		if !ok {
			return
		}
		joined, apiErr := svc.AcceptByToken(c.Request.Context(), id, c.Param("token"))
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"gym": joined})
	}
}
