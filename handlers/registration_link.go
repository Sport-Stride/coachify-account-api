package handlers

import (
	"coachify-account-api/models/api"
	"coachify-account-api/services"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// GenerateRegistrationLink generates or retrieves the coach's public registration link.
func GenerateRegistrationLink(service services.RegistrationLinkService) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenUserID, exists := c.Get("userID")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found in token"})
			return
		}
		coachID, ok := tokenUserID.(string)
		if !ok || coachID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User ID not found in token"})
			return
		}

		link, apiErr := service.GetOrCreateLink(c.Request.Context(), coachID)
		if apiErr != nil {
			c.JSON(apiErr.Code, gin.H{"error": apiErr.Error.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"token":      link.Token,
			"coach_id":   link.CoachID,
			"created_at": link.CreatedAt,
		})
	}
}

// ValidateRegistrationLink validates a registration link token and returns coach info.
func ValidateRegistrationLink(service services.RegistrationLinkService) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.Param("token")
		if token == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing token"})
			return
		}

		link, coachName, apiErr := service.ValidateToken(c.Request.Context(), token)
		if apiErr != nil {
			c.JSON(apiErr.Code, gin.H{"error": apiErr.Error.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"coach_id":   link.CoachID,
			"coach_name": coachName,
		})
	}
}

// RegisterViaLink registers a new client through a coach's public registration link.
func RegisterViaLink(authService services.AuthService, regLinkService services.RegistrationLinkService) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.Param("token")
		if token == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing token"})
			return
		}

		var req api.CreateUserRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload: " + err.Error()})
			return
		}

		resp, apiErr := authService.RegisterViaLink(c.Request.Context(), token, &req)
		zap.S().Infof("RegisterViaLink response: %+v, apiErr: %+v", resp, apiErr)
		if apiErr != nil {
			c.JSON(apiErr.Code, gin.H{"error": apiErr.Error.Error()})
			return
		}

		c.JSON(http.StatusCreated, gin.H{"message": "User registered successfully", "user": resp})
	}
}
