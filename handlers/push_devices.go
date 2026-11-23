package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"coachify-account-api/models/api"
	"coachify-account-api/services"
)

// RegisterPushDevice serves POST /user/devices: stores the caller's Expo push token.
func RegisterPushDevice(svc services.PushDeviceService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("userID")
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
			return
		}
		var req api.RegisterDeviceRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload"})
			return
		}
		if apiErr := svc.Register(c.Request.Context(), userID, req); apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

// UnregisterPushDevice serves DELETE /user/devices/:token (on sign-out): removes the caller's token.
func UnregisterPushDevice(svc services.PushDeviceService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("userID")
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
			return
		}
		if apiErr := svc.Unregister(c.Request.Context(), userID, c.Param("token")); apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

// ListInternalPushDevices serves GET /internal/users/:id/devices (x-api-key) to the notification senders.
func ListInternalPushDevices(svc services.PushDeviceService) gin.HandlerFunc {
	return func(c *gin.Context) {
		devices, apiErr := svc.List(c.Request.Context(), c.Param("id"))
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, api.DevicesResponse{Devices: devices})
	}
}
