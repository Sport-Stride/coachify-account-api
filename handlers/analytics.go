package handlers

import (
	"net/http"

	"coachify-account-api/services"

	"github.com/gin-gonic/gin"
)

// authedCaller returns the caller id from the token; the service authorizes the role from the database.
func authedCaller(c *gin.Context) (string, bool) {
	id := c.GetString("userID")
	if id == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return "", false
	}
	return id, true
}

// GetAnalyticsScope answers who the caller may see (consumed by payments with the caller's token).
func GetAnalyticsScope(svc services.AnalyticsScopeService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := authedCaller(c)
		if !ok {
			return
		}
		req := services.ScopeRequest{
			Scope:       c.Query("scope"),
			GymID:       c.Query("gym_id"),
			HeadCoachID: c.Query("head_coach_id"),
			CoachID:     c.Query("coach_id"),
		}
		scope, apiErr := svc.Scope(c.Request.Context(), id, req)
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, scope)
	}
}

type lookupClientsRequest struct {
	services.ScopeRequest
	IDs []string `json:"ids"`
}

// LookupAnalyticsClients returns the identity of the given clients that belong to current in-scope coaches.
func LookupAnalyticsClients(svc services.AnalyticsScopeService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := authedCaller(c)
		if !ok {
			return
		}
		var req lookupClientsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
			return
		}
		if len(req.IDs) == 0 || len(req.IDs) > services.MaxLookupIDs {
			c.JSON(http.StatusBadRequest, gin.H{"error": "ids are required (max 100)"})
			return
		}
		items, apiErr := svc.LookupClients(c.Request.Context(), id, req.ScopeRequest, req.IDs)
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": items})
	}
}

// ListHeadCoachClients lists the clients of the head coach and of the current coaches of the team.
func ListHeadCoachClients(svc services.AnalyticsScopeService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := headCoachCaller(c)
		if !ok {
			return
		}
		sort := c.Query("sort")
		if sort != "" && !validGymClientSorts[sort] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid sort"})
			return
		}
		page, size := pagination(c)
		rows, total, apiErr := svc.ListHeadClients(c.Request.Context(), id, services.HeadClientsQuery{
			CoachID: c.Query("coach_id"), Q: searchQuery(c), Sort: sort, Page: page, Size: size,
		})
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": rows, "total": total, "page": page, "size": size})
	}
}
