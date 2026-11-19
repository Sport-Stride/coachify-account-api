package handlers

import (
	"net/http"

	"coachify-account-api/services"

	"github.com/gin-gonic/gin"
)

type putSeatRequest struct {
	Assigned *bool `json:"assigned"`
}

// GetGymSeats returns the seat overview of a gym (owner only): purchased, used, free, overflow and every coach with
// his seat state. 404 for a foreign gym and while GYM_SEATS_ENABLED is off.
func GetGymSeats(svc services.SeatService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetString("userID")
		if id == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		ov, apiErr := svc.Overview(c.Request.Context(), id, c.Param("gym_id"))
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, ov)
	}
}

// PutGymMemberSeat assigns or releases the seat of a coach or head coach (owner only). 409 SEATS_EXHAUSTED when no
// seat is free.
func PutGymMemberSeat(svc services.SeatService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetString("userID")
		if id == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		var req putSeatRequest
		if c.ShouldBindJSON(&req) != nil || req.Assigned == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "assigned (true or false) is required"})
			return
		}
		v, apiErr := svc.SetSeat(c.Request.Context(), id, c.Param("gym_id"), c.Param("user_id"), *req.Assigned)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, v)
	}
}
