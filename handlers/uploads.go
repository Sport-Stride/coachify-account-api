package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"coachify-account-api/models/api"
	"coachify-account-api/services"
)

// SignUpload serves POST /uploads/sign: signed fields for a direct image upload to Cloudinary,
// into the caller's own folder (the identity comes from the JWT only).
func SignUpload(svc services.UploadService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("userID")
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
			return
		}
		var req api.UploadSignRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload"})
			return
		}
		sig, apiErr := svc.Sign(c.Request.Context(), userID, req)
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, sig)
	}
}
