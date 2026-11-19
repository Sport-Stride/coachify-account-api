package handlers

import (
	"net/http"

	"coachify-account-api/services"

	"github.com/gin-gonic/gin"
)

// maxGymClientPage mirrors the member list: deeper pages are clamped.
const maxGymClientPage = 10000

var gymClientSorts = map[string]bool{"": true, "name": true, "linked_at": true}

// assignedFilters maps the ?assigned= value to the filter; nil means no filter.
var assignedFilters = map[string]func() *bool{
	"":      func() *bool { return nil },
	"true":  func() *bool { v := true; return &v },
	"false": func() *bool { v := false; return &v },
}

// ListGymRelationClients lists the gym's own clients. client.view_all sees all (the pool included); others see their own and
// supervised clients. Personal coach_clients are never part of it.
func ListGymRelationClients(svc services.GymClientService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetString("userID")
		if id == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		mk, ok := assignedFilters[c.Query("assigned")]
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "assigned must be true or false"})
			return
		}
		sort := c.Query("sort")
		if !gymClientSorts[sort] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "sort must be name or linked_at"})
			return
		}
		page, size := pagination(c)
		if page > maxGymClientPage {
			page = maxGymClientPage
		}
		rows, total, apiErr := svc.ListClients(c.Request.Context(), id, c.Param("gym_id"), services.GymClientListQuery{
			Assigned: mk(), CoachID: c.Query("coach_id"), Q: c.Query("q"), Sort: sort, Page: page, Size: size,
		})
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"clients": rows, "total": total, "page": page, "size": size})
	}
}

// AssignGymClient sets or replaces the coach of a gym client (client.assign).
func AssignGymClient(svc services.GymClientService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetString("userID")
		if id == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		var req struct {
			CoachID string `json:"coach_id"`
		}
		if c.ShouldBindJSON(&req) != nil || req.CoachID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "coach_id is required"})
			return
		}
		res, apiErr := svc.Assign(c.Request.Context(), id, c.Param("gym_id"), c.Param("client_id"), req.CoachID)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, res)
	}
}

// UnassignGymClient returns a gym client to the pool (client.unassign).
func UnassignGymClient(svc services.GymClientService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetString("userID")
		if id == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		res, apiErr := svc.Unassign(c.Request.Context(), id, c.Param("gym_id"), c.Param("client_id"))
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, res)
	}
}

// RemoveGymClient ends the gym relationship of a client (owner only).
func RemoveGymClient(svc services.GymClientService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetString("userID")
		if id == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		if apiErr := svc.Remove(c.Request.Context(), id, c.Param("gym_id"), c.Param("client_id")); apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "Client removed from the gym"})
	}
}
