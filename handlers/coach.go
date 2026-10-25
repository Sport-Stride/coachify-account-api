package handlers

import (
	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/services"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/mongo"
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

// ListCoachClientsSummary returns lightweight client rows (externalid, names, picture) for the token's coach.
// Query: page (default 1), size (default 200, max 1000).
func ListCoachClientsSummary(coachService services.CoachService) gin.HandlerFunc {
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
			Page int `form:"page"`
			Size int `form:"size"`
		}
		if err := c.ShouldBindQuery(&query); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid query params"})
			return
		}
		if query.Page < 1 {
			query.Page = 1
		}
		if query.Size < 1 {
			query.Size = 200
		}
		if query.Size > 1000 {
			query.Size = 1000
		}
		clients, total, err := coachService.ListCoachClientsSummary(c.Request.Context(), coachID, query.Page, query.Size)
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
			if errors.Is(err, mongo.ErrNoDocuments) {
				c.JSON(http.StatusNotFound, gin.H{"error": "no coach-client relationship found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"coach_id": coachID})
	}
}
const maxCheckClientIDs = 200

// checkLinksRoles lists the roles allowed to query client links; admin short-circuits to all true.
var checkLinksRoles = map[string]bool{"coach": true, "nutritionist": true, "head_coach": true, "admin": true}

// CheckClientLinks reports, for each requested client id, whether the token's user is linked to it.
// Used by sibling services to authorize coach access to client data.
func CheckClientLinks(coachService services.CoachService) gin.HandlerFunc {
	return func(c *gin.Context) {
		actorID := c.GetString("userID")
		if actorID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User ID not found in token"})
			return
		}
		role := c.GetString("userRole")
		if !checkLinksRoles[role] {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		var req struct {
			ClientIDs []string `json:"client_ids"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
			return
		}
		if len(req.ClientIDs) == 0 || len(req.ClientIDs) > maxCheckClientIDs {
			c.JSON(http.StatusBadRequest, gin.H{"error": "client_ids must contain between 1 and 200 ids"})
			return
		}
		seen := make(map[string]struct{}, len(req.ClientIDs))
		for _, id := range req.ClientIDs {
			if strings.TrimSpace(id) == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "client_ids must not contain blank ids"})
				return
			}
			if _, dup := seen[id]; dup {
				c.JSON(http.StatusBadRequest, gin.H{"error": "client_ids must be unique"})
				return
			}
			seen[id] = struct{}{}
		}

		var links map[string]bool
		if role == "admin" {
			links = make(map[string]bool, len(req.ClientIDs))
			for _, id := range req.ClientIDs {
				links[id] = true
			}
		} else {
			var apiErr *models.ApiError
			links, apiErr = coachService.CheckClientLinks(c.Request.Context(), actorID, req.ClientIDs)
			if apiErr != nil {
				c.JSON(apiErr.Code, gin.H{"error": apiErr.Error.Error()})
				return
			}
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{"coach_id": actorID, "links": links})
	}
}
