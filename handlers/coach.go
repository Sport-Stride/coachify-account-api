package handlers

import (
	"coachify-account-api/models/db"
	"coachify-account-api/services"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// List and filter/paginate coach clients
func ListCoachClients(coachService services.CoachService) gin.HandlerFunc {
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
		var query struct {
			ClientID string `form:"client_id"`
			FromDate string `form:"from_date"`
			ToDate   string `form:"to_date"`
			Page     int    `form:"page"`
			Size     int    `form:"size"`
		}
		if err := c.ShouldBindQuery(&query); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid query params"})
			return
		}
		var fromDate, toDate time.Time
		var err error
		if query.FromDate != "" {
			fromDate, err = time.Parse(time.RFC3339, query.FromDate)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid from_date format"})
				return
			}
		}
		if query.ToDate != "" {
			toDate, err = time.Parse(time.RFC3339, query.ToDate)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid to_date format"})
				return
			}
		}
		listQuery := db.CoachClientListQuery{
			CoachID:  coachID,
			ClientID: query.ClientID,
			FromDate: fromDate,
			ToDate:   toDate,
			Page:     query.Page,
			Size:     query.Size,
		}
		clients, total, err := coachService.ListCoachClients(c.Request.Context(), listQuery)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"clients": clients, "total": total})
	}
}

// Dissociate a coach and a client
func DissociateCoachClient(coachService services.CoachService) gin.HandlerFunc {
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
		clientID := c.Param("client_id")
		if clientID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing client_id param"})
			return
		}
		err := coachService.DissociateCoachClient(c.Request.Context(), coachID, clientID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "Coach-client relationship removed"})
	}
}
// Dissociate a coach and a client
func GetCoachIDByClientID(coachService services.CoachService) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenUserID, exists := c.Get("userID")
		if !exists {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found in token"})
			return
		}
		clientID, ok := tokenUserID.(string)
		if !ok || clientID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User ID not found in token"})
			return
		}
		coachID, err := coachService.GetCoachIDByClientID(c.Request.Context(), clientID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"coach_id": coachID})
	}
}