package handlers

import (
	"net/http"
	"strconv"

	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
	"coachify-account-api/services"

	"github.com/gin-gonic/gin"
)

// GetMyEntitlements returns the caller's effective feature access with usage (any authenticated role).
func GetMyEntitlements(svc services.EntitlementService) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("userID")
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "User not found in token"})
			return
		}
		eff, apiErr := svc.Effective(c.Request.Context(), userID)
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, eff)
	}
}

// ListEntitlementFeatures returns the feature registry (admin).
func ListEntitlementFeatures(svc services.EntitlementService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		c.JSON(http.StatusOK, gin.H{"features": svc.Features()})
	}
}

// ListCoachAccess returns one page of coaches with their effective access (admin).
func ListCoachAccess(svc services.EntitlementService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		page, size := pagination(c)
		q := db.CoachAccessQuery{Page: page, Size: size, Q: searchQuery(c), Status: c.Query("status"), Feature: c.Query("feature")}
		if v := c.Query("disabled"); v != "" {
			b, err := strconv.ParseBool(v)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid disabled filter"})
				return
			}
			q.Disabled = b
		}
		list, apiErr := svc.ListCoachAccess(c.Request.Context(), q)
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, list)
	}
}

// GetCoachAccess returns one coach's access, raw overrides and recent audit entries (admin).
func GetCoachAccess(svc services.EntitlementService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		detail, apiErr := svc.GetCoachAccess(c.Request.Context(), c.Param("id"))
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, detail)
	}
}

// UpdateCoachAccess sets or clears a coach's feature overrides (admin).
func UpdateCoachAccess(svc services.EntitlementService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		var req api.UpdateAccessRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload"})
			return
		}
		detail, apiErr := svc.SetCoachAccess(c.Request.Context(), c.GetString("userID"), c.Param("id"), req)
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, detail)
	}
}

// ListPlanDefaults returns every plan default (admin).
func ListPlanDefaults(svc services.EntitlementService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		list, apiErr := svc.ListPlanDefaults(c.Request.Context())
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"plan_defaults": list})
	}
}

// UpdatePlanDefault sets or clears the defaults of a plan, or of every coach/client (admin).
func UpdatePlanDefault(svc services.EntitlementService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		var req api.PlanDefaultRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload"})
			return
		}
		pd, apiErr := svc.SetPlanDefault(c.Request.Context(), c.GetString("userID"), c.Param("plan_id"), req)
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, pd)
	}
}

// ListEntitlementAudit returns the entitlement audit trail (admin).
func ListEntitlementAudit(svc services.EntitlementService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		f := db.AdminAuditFilter{ActorID: c.Query("actor_id"), TargetID: c.Query("target_id")}
		page, size := pagination(c)
		list, apiErr := svc.ListAudit(c.Request.Context(), f, page, size)
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, list)
	}
}

// accessCoachID resolves whose clients the caller manages: coaches and nutritionists their own,
// admins the coach given in ?coach_id. It writes the error response and returns false otherwise.
func accessCoachID(c *gin.Context) (string, bool) {
	userID := c.GetString("userID")
	switch c.GetString("userRole") {
	case "coach", "nutritionist":
		if userID != "" {
			return userID, true
		}
	case "admin":
		if id := c.Query("coach_id"); id != "" {
			return id, true
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "coach_id is required"})
		return "", false
	}
	c.JSON(http.StatusForbidden, gin.H{"error": "Coach access required"})
	return "", false
}

// ListClientAccess returns one page of the coach's clients with their effective access.
func ListClientAccess(svc services.EntitlementService) gin.HandlerFunc {
	return func(c *gin.Context) {
		coachID, ok := accessCoachID(c)
		if !ok {
			return
		}
		page, size := pagination(c)
		list, apiErr := svc.ListClientAccess(c.Request.Context(), coachID, page, size, searchQuery(c))
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, list)
	}
}

// GetClientAccess returns one linked client's access (403 when the client is not linked to the coach).
func GetClientAccess(svc services.EntitlementService) gin.HandlerFunc {
	return func(c *gin.Context) {
		coachID, ok := accessCoachID(c)
		if !ok {
			return
		}
		detail, apiErr := svc.GetClientAccess(c.Request.Context(), coachID, c.Param("client_id"))
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, detail)
	}
}

// UpdateClientAccess sets or clears a linked client's feature overrides, capped by the coach's own access.
func UpdateClientAccess(svc services.EntitlementService) gin.HandlerFunc {
	return func(c *gin.Context) {
		coachID, ok := accessCoachID(c)
		if !ok {
			return
		}
		var req api.UpdateAccessRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload"})
			return
		}
		detail, apiErr := svc.SetClientAccess(c.Request.Context(), c.GetString("userID"), c.GetString("userRole"), coachID, c.Param("client_id"), req)
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, detail)
	}
}

// GetInternalEntitlements returns a user's effective access (service-to-service).
func GetInternalEntitlements(svc services.EntitlementService) gin.HandlerFunc {
	return func(c *gin.Context) {
		eff, apiErr := svc.Effective(c.Request.Context(), c.Param("user_id"))
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, eff)
	}
}

// ResolveInternalEntitlements returns the effective access of up to 100 users (service-to-service).
func ResolveInternalEntitlements(svc services.EntitlementService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req api.ResolveRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload"})
			return
		}
		res, apiErr := svc.Resolve(c.Request.Context(), req.UserIDs)
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"entitlements": res})
	}
}

// ConsumeInternalUsage atomically adds to a user's usage counters unless a limit would be exceeded.
func ConsumeInternalUsage(svc services.EntitlementService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req api.UsageDeltaRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload"})
			return
		}
		usage, apiErr := svc.Consume(c.Request.Context(), c.Param("user_id"), req)
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"usage": usage})
	}
}

// ReleaseInternalUsage subtracts from a user's usage counters (never below zero).
func ReleaseInternalUsage(svc services.EntitlementService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req api.UsageDeltaRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload"})
			return
		}
		if apiErr := svc.Release(c.Request.Context(), c.Param("user_id"), req); apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "released"})
	}
}

// CheckInternalQuota reports whether a coach may take on another client (service-to-service).
func CheckInternalQuota(svc services.EntitlementService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req api.CheckQuotaRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload"})
			return
		}
		res, apiErr := svc.CheckQuota(c.Request.Context(), c.Param("user_id"), req)
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, res)
	}
}
