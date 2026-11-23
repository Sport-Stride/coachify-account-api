package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"coachify-account-api/models/api"
	"coachify-account-api/services"
)

// GetNotificationPrefs serves GET /user/notification-prefs: the caller's push preferences (defaults if never saved).
func GetNotificationPrefs(svc services.NotificationPrefsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("userID")
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
			return
		}
		prefs, apiErr := svc.Get(c.Request.Context(), userID)
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, prefs)
	}
}

// UpdateNotificationPrefs serves PUT /user/notification-prefs: saves the fields sent, returns the result.
func UpdateNotificationPrefs(svc services.NotificationPrefsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("userID")
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
			return
		}
		var req api.UpdateNotificationPrefsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload"})
			return
		}
		prefs, apiErr := svc.Update(c.Request.Context(), userID, req)
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, prefs)
	}
}

// LookupInternalNotificationPrefs serves POST /internal/notification-prefs (x-api-key) to the notification senders.
func LookupInternalNotificationPrefs(svc services.NotificationPrefsService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req api.NotificationPrefsLookupRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload"})
			return
		}
		prefs, apiErr := svc.Lookup(c.Request.Context(), req.UserIDs)
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, api.NotificationPrefsLookupResponse{Prefs: prefs})
	}
}
