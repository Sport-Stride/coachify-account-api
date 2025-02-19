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
		// Extract coachID from the context
		coachID, exists := c.Get("userID")
		if !exists || coachID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}

		// Parse the request body
		var requestBody struct {
			Email  string   `json:"email"`  // Single email
			Emails []string `json:"emails"` // Multiple emails
		}

		if err := c.ShouldBindJSON(&requestBody); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}

		// Determine if the request is for a single email or multiple emails
		if requestBody.Email != "" {
			// Single email case
			createUserReq := api.CreateUserRequest{
				Email: requestBody.Email,
			}

			// Call the InviteClient method for a single email
			resp, apiErr := coachService.InviteClient(c.Request.Context(), &createUserReq, coachID.(string))
			if apiErr != nil {
				c.JSON(apiErr.Code, gin.H{"error": apiErr.Error.Error()})
				return
			}

			c.JSON(http.StatusCreated, resp)
		} else if len(requestBody.Emails) > 0 {
			// Multiple emails case
			resp, apiErr := coachService.InviteMultipleClientsBulk(c.Request.Context(), requestBody.Emails, coachID.(string))
			if apiErr != nil {
				c.JSON(apiErr.Code, gin.H{"error": apiErr.Error.Error()})
				return
			}

			c.JSON(http.StatusCreated, resp)
		} else {
			// No valid email(s) provided
			c.JSON(http.StatusBadRequest, gin.H{"error": "no valid email(s) provided"})
			return
		}
	}
}
