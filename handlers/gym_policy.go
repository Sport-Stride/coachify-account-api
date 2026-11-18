package handlers

import (
	"net/http"

	"coachify-account-api/services"

	"github.com/gin-gonic/gin"
)

type putRolePolicyRequest struct {
	Capabilities map[string]bool `json:"capabilities"`
	Version      int64           `json:"version"`
}

type putGrantsRequest struct {
	Grants []services.GrantInput `json:"grants"`
}

// GetGymRolePolicy returns the effective capabilities of a role in a gym (owner only).
func GetGymRolePolicy(svc services.GymPolicyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetString("userID")
		if id == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		v, apiErr := svc.GetRolePolicy(c.Request.Context(), id, c.Param("gym_id"), c.Param("role"))
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, v)
	}
}

// PutGymRolePolicy replaces the stored capability overrides of a role (owner only, optimistic version).
func PutGymRolePolicy(svc services.GymPolicyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetString("userID")
		if id == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		var req putRolePolicyRequest
		if c.ShouldBindJSON(&req) != nil || req.Version < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "capabilities and version are required"})
			return
		}
		v, apiErr := svc.PutRolePolicy(c.Request.Context(), id, c.Param("gym_id"), c.Param("role"), req.Capabilities, req.Version)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, v)
	}
}

// GetGymMemberGrants returns the overrides of a member (owner only).
func GetGymMemberGrants(svc services.GymPolicyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetString("userID")
		if id == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		v, apiErr := svc.GetGrants(c.Request.Context(), id, c.Param("gym_id"), c.Param("user_id"))
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, v)
	}
}

// PutGymMemberGrants replaces the overrides of a member (owner only; the owner cannot be targeted).
func PutGymMemberGrants(svc services.GymPolicyService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetString("userID")
		if id == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		var req putGrantsRequest
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "grants is required"})
			return
		}
		v, apiErr := svc.PutGrants(c.Request.Context(), id, c.Param("gym_id"), c.Param("user_id"), req.Grants)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, v)
	}
}
