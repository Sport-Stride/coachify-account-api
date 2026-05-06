package handlers

import (
	"coachify-account-api/services"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// SubmitMatriculeFiscale handles PATCH /user/matricule-fiscale
func SubmitMatriculeFiscale(authService services.AuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, exists := c.Get("userID")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found in token"})
			return
		}
		externalID, ok := userID.(string)
		if !ok || externalID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User ID not found in token"})
			return
		}

		userRole, _ := c.Get("userRole")
		role, _ := userRole.(string)
		if role != "coach" && role != "nutritionist" {
			c.JSON(http.StatusForbidden, gin.H{"error": "matricule fiscale is only available for coach or nutritionist roles"})
			return
		}

		var req struct {
			MatriculeFiscale string `json:"matricule_fiscale" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		apiErr := authService.SubmitMatriculeFiscale(c.Request.Context(), externalID, req.MatriculeFiscale)
		if apiErr != nil {
			c.JSON(apiErr.Code, gin.H{"error": apiErr.Error.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "matricule fiscale submitted for review"})
	}
}

// GetMatriculeFiscaleApplications handles GET /admin/matricule-fiscale
func GetMatriculeFiscaleApplications(authService services.AuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userRole, _ := c.Get("userRole")
		role, _ := userRole.(string)
		if role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": "admin access required"})
			return
		}

		status := c.Query("status")
		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))

		applications, total, apiErr := authService.GetMatriculeFiscaleApplications(c.Request.Context(), status, page, limit)
		if apiErr != nil {
			c.JSON(apiErr.Code, gin.H{"error": apiErr.Error.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"applications": applications,
			"total":        total,
			"page":         page,
			"limit":        limit,
		})
	}
}

// ApproveMatriculeFiscale handles POST /admin/matricule-fiscale/:userId/approve
func ApproveMatriculeFiscale(authService services.AuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		adminID, exists := c.Get("userID")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found in token"})
			return
		}
		adminExternalID, ok := adminID.(string)
		if !ok || adminExternalID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User ID not found in token"})
			return
		}

		userRole, _ := c.Get("userRole")
		role, _ := userRole.(string)
		if role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": "admin access required"})
			return
		}

		targetUserID := c.Param("userId")
		if targetUserID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "userId parameter required"})
			return
		}

		apiErr := authService.ApproveMatriculeFiscale(c.Request.Context(), targetUserID, adminExternalID)
		if apiErr != nil {
			c.JSON(apiErr.Code, gin.H{"error": apiErr.Error.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "matricule fiscale approved"})
	}
}

// RejectMatriculeFiscale handles POST /admin/matricule-fiscale/:userId/reject
func RejectMatriculeFiscale(authService services.AuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		adminID, exists := c.Get("userID")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found in token"})
			return
		}
		adminExternalID, ok := adminID.(string)
		if !ok || adminExternalID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User ID not found in token"})
			return
		}

		userRole, _ := c.Get("userRole")
		role, _ := userRole.(string)
		if role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": "admin access required"})
			return
		}

		targetUserID := c.Param("userId")
		if targetUserID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "userId parameter required"})
			return
		}

		apiErr := authService.RejectMatriculeFiscale(c.Request.Context(), targetUserID, adminExternalID)
		if apiErr != nil {
			c.JSON(apiErr.Code, gin.H{"error": apiErr.Error.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "matricule fiscale rejected"})
	}
}
