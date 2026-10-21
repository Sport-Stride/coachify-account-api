package handlers

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/services"

	"github.com/gin-gonic/gin"
)

// respondAdminError never leaks 5xx details.
func respondAdminError(c *gin.Context, apiErr *models.ApiError) {
	if apiErr.Code >= http.StatusInternalServerError {
		c.JSON(apiErr.Code, gin.H{"error": "internal error"})
		return
	}
	respondApiError(c, apiErr)
}

// csvIDs splits a comma separated list, trimming blanks.
func csvIDs(v string) []string {
	var ids []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			ids = append(ids, p)
		}
	}
	return ids
}

// ListAdminUsers returns a filtered, sorted, paginated list of users (admin-safe fields only).
func ListAdminUsers(adminService services.AdminService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		page, size := pagination(c)
		q := db.AdminUserQuery{
			Q:      searchQuery(c),
			Role:   c.Query("role"),
			Status: c.Query("status"),
			IDs:    csvIDs(c.Query("ids")),
			Page:   page,
			Size:   size,
			Sort:   c.Query("sort"),
			Order:  c.Query("order"),
		}
		list, apiErr := adminService.ListUsers(c.Request.Context(), q)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, list)
	}
}

// GetAdminUser returns one user with coach / team / clients-count details.
func GetAdminUser(adminService services.AdminService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		id := c.Param("id")
		if id == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing user id"})
			return
		}
		user, apiErr := adminService.GetUser(c.Request.Context(), id)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"user": user})
	}
}

// GetAdminUserStats returns user base counters and the signup series of the last `days` (7..90, default 30).
func GetAdminUserStats(adminService services.AdminService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		days, err := strconv.Atoi(c.DefaultQuery("days", "30"))
		if err != nil {
			days = 30
		}
		if days < 7 {
			days = 7
		}
		if days > 90 {
			days = 90
		}
		stats, apiErr := adminService.UserStats(c.Request.Context(), days)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, stats)
	}
}

type changeStatusRequest struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// ChangeAdminUserStatus sets a user status; the actor comes from the token, never the body.
func ChangeAdminUserStatus(adminService services.AdminService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		id := c.Param("id")
		if id == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing user id"})
			return
		}
		var req changeStatusRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		row, apiErr := adminService.UpdateUserStatus(c.Request.Context(), c.GetString("userID"), id, req.Status, req.Reason)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"user": row})
	}
}

// ResendAdminUserConfirmation re-sends the confirmation email of a user still in "To Confirm".
func ResendAdminUserConfirmation(adminService services.AdminService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		id := c.Param("id")
		if id == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing user id"})
			return
		}
		if apiErr := adminService.ResendConfirmation(c.Request.Context(), c.GetString("userID"), id); apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"message": "confirmation email sent"})
	}
}

// SendAdminUserPasswordReset emails a password reset code to the user; the code is never returned.
func SendAdminUserPasswordReset(adminService services.AdminService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		id := c.Param("id")
		if id == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing user id"})
			return
		}
		if apiErr := adminService.SendPasswordReset(c.Request.Context(), c.GetString("userID"), id); apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"message": "password reset email sent"})
	}
}

type createGymOwnerRequest struct {
	Firstname string `json:"firstname"`
	Lastname  string `json:"lastname"`
	Email     string `json:"email"`
}

// CreateAdminGymOwner creates an Active gym_owner and emails it a set-password code; the code is never returned.
func CreateAdminGymOwner(adminService services.AdminService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		var req createGymOwnerRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		row, apiErr := adminService.CreateGymOwner(c.Request.Context(), c.GetString("userID"), req.Firstname, req.Lastname, req.Email)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusCreated, gin.H{"user": row})
	}
}

// ListAdminAudit returns the admin audit trail, newest first. from is inclusive, to exclusive (RFC3339).
func ListAdminAudit(adminService services.AdminService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		f := db.AdminAuditFilter{
			ActorID:  c.Query("actor_id"),
			TargetID: c.Query("target_id"),
			Action:   c.Query("action"),
		}
		var err error
		if v := c.Query("from"); v != "" {
			if f.From, err = time.Parse(time.RFC3339, v); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid from"})
				return
			}
		}
		if v := c.Query("to"); v != "" {
			if f.To, err = time.Parse(time.RFC3339, v); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid to"})
				return
			}
		}
		page, size := pagination(c)
		list, apiErr := adminService.ListAudit(c.Request.Context(), f, page, size)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, list)
	}
}
