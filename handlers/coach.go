package handlers

import (
	"coachify-account-api/models/api"
	"coachify-account-api/services"

	"net/http"

	"github.com/gin-gonic/gin"
)

func GetClientsPaginated(coachService services.CoachService) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Retrieve coachID from token claims set by AuthMiddleware.
		coachID, exists := c.Get("userID")
		if !exists || coachID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: coach not found in token"})
			return
		}

		// Bind query parameters into the SearchClient struct.
		var searchReq api.SearchClient
		if err := c.ShouldBindQuery(&searchReq); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid query parameters: " + err.Error()})
			return
		}

		// Call the service method to fetch paginated clients.
		clients, total, apiErr := coachService.GetClientsPaginated(c.Request.Context(), coachID.(string), searchReq)
		if apiErr != nil {
			c.JSON(apiErr.Code, gin.H{"error": apiErr.Error.Error()})
			return
		}

		// Return the paginated response.
		c.JSON(http.StatusOK, api.PaginatedClientResponse{
			Clients: clients,
			Total:   total,
		})
	}
}

func InviteClient(coachService services.CoachService) gin.HandlerFunc {
	return func(c *gin.Context) {
		coachID, exists := c.Get("userID")
		if !exists || coachID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		var createUserReq api.CreateUserRequest
		if err := c.ShouldBindJSON(&createUserReq); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}

		// Call the updated InviteClient method which now accepts *api.CreateUserRequest.
		resp, apiErr := coachService.InviteClient(c.Request.Context(), &createUserReq, coachID.(string))
		if apiErr != nil {
			c.JSON(apiErr.Code, gin.H{"error": apiErr.Error.Error()})
			return
		}

		c.JSON(http.StatusCreated, resp)
	}
}
