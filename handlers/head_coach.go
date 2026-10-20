package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/services"

	"github.com/gin-gonic/gin"
)

// pagination reads page (default 1) and size (default 20, max 100).
func pagination(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if size < 1 || size > 100 {
		size = 20
	}
	return page, size
}

func respondApiError(c *gin.Context, apiErr *models.ApiError) {
	msg := "internal error"
	if apiErr.Error != nil {
		msg = apiErr.Error.Error()
	}
	c.JSON(apiErr.Code, gin.H{"error": msg})
}

// adminOnly aborts with 403 unless the caller's token role is admin.
func adminOnly(c *gin.Context) bool {
	if c.GetString("userRole") != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Admin access required"})
		return false
	}
	return true
}

// coachCaller returns the caller id when the token role is coach; the service re-checks the head flag in the DB.
func coachCaller(c *gin.Context) (string, bool) {
	id := c.GetString("userID")
	if c.GetString("userRole") != "coach" || id == "" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Head coach access required"})
		return "", false
	}
	return id, true
}

const maxSearchLen = 64

var (
	validTeamStatuses = map[string]bool{
		"Active": true, "Inactive": true, "Suspended": true,
		"Deactivated": true, "To Confirm": true, "Blocked": true,
	}
	validTeamSorts = map[string]bool{"name": true, "clients": true, "last_login": true}
)

// searchQuery trims the q parameter and caps it at maxSearchLen characters.
func searchQuery(c *gin.Context) string {
	q := strings.TrimSpace(c.Query("q"))
	if r := []rune(q); len(r) > maxSearchLen {
		q = string(r[:maxSearchLen])
	}
	return q
}

// teamListQuery parses and validates the list filters; it writes a 400 and returns false on unknown values.
func teamListQuery(c *gin.Context) (db.TeamListQuery, bool) {
	page, size := pagination(c)
	status, sort := c.Query("status"), c.Query("sort")
	if status != "" && !validTeamStatuses[status] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid status"})
		return db.TeamListQuery{}, false
	}
	if sort != "" && !validTeamSorts[sort] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid sort"})
		return db.TeamListQuery{}, false
	}
	return db.TeamListQuery{Page: page, Size: size, Q: searchQuery(c), Status: status, Sort: sort}, true
}

// ---------- Admin ----------

func ListHeadCoaches(svc services.TeamService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		page, size := pagination(c)
		rows, total, apiErr := svc.ListHeadCoaches(c.Request.Context(), page, size, searchQuery(c))
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"head_coaches": rows, "total": total, "page": page, "size": size})
	}
}

func PromoteHeadCoach(svc services.TeamService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		id := c.Param("id")
		if id == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing coach id"})
			return
		}
		row, apiErr := svc.Promote(c.Request.Context(), c.GetString("userID"), id)
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"head_coach": row})
	}
}

func DemoteHeadCoach(svc services.TeamService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		id := c.Param("id")
		if id == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing coach id"})
			return
		}
		released, apiErr := svc.Demote(c.Request.Context(), c.GetString("userID"), id)
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"released": released})
	}
}

func ListHeadCoachTeam(svc services.TeamService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		id := c.Param("id")
		if id == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing head coach id"})
			return
		}
		q, ok := teamListQuery(c)
		if !ok {
			return
		}
		rows, total, apiErr := svc.ListTeam(c.Request.Context(), id, q)
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"coaches": rows, "total": total, "page": q.Page, "size": q.Size})
	}
}

type assignHeadCoachRequest struct {
	HeadCoachID string `json:"head_coach_id" binding:"required"`
}

func AssignCoachToHeadCoach(svc services.TeamService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		id := c.Param("id")
		var req assignHeadCoachRequest
		if id == "" || c.ShouldBindJSON(&req) != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "head_coach_id is required"})
			return
		}
		m, apiErr := svc.Assign(c.Request.Context(), c.GetString("userID"), id, req.HeadCoachID)
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"membership": gin.H{
			"coach_id":      m.CoachID,
			"head_coach_id": m.HeadCoachID,
			"assigned_at":   m.AssignedAt,
		}})
	}
}

func UnassignCoach(svc services.TeamService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		id := c.Param("id")
		if id == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing coach id"})
			return
		}
		if apiErr := svc.Unassign(c.Request.Context(), c.GetString("userID"), id); apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "unassigned"})
	}
}

func ListTeamAudit(svc services.TeamService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		page, size := pagination(c)
		entries, total, apiErr := svc.ListAudit(c.Request.Context(), c.Query("head_coach_id"), c.Query("coach_id"), page, size)
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"entries": entries, "total": total, "page": page, "size": size})
	}
}

// ---------- Head coach ----------

// GetHeadCoachMe answers 200 for any coach; is_head_coach is false for non-head coaches.
func GetHeadCoachMe(svc services.TeamService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := coachCaller(c)
		if !ok {
			return
		}
		me, apiErr := svc.Me(c.Request.Context(), id)
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, me)
	}
}

func GetTeamOverview(svc services.TeamService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := coachCaller(c)
		if !ok {
			return
		}
		o, apiErr := svc.Overview(c.Request.Context(), id)
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, o)
	}
}

func ListMyTeamCoaches(svc services.TeamService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := coachCaller(c)
		if !ok {
			return
		}
		q, valid := teamListQuery(c)
		if !valid {
			return
		}
		rows, total, apiErr := svc.ListMyCoaches(c.Request.Context(), id, q)
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"coaches": rows, "total": total, "page": q.Page, "size": q.Size})
	}
}

func GetMyTeamCoach(svc services.TeamService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := coachCaller(c)
		if !ok {
			return
		}
		coachID := c.Param("id")
		if coachID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing coach id"})
			return
		}
		row, apiErr := svc.GetMyCoach(c.Request.Context(), id, coachID)
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"coach": row})
	}
}

func RemoveMyTeamCoach(svc services.TeamService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := coachCaller(c)
		if !ok {
			return
		}
		coachID := c.Param("id")
		if coachID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing coach id"})
			return
		}
		if apiErr := svc.RemoveMyCoach(c.Request.Context(), id, coachID); apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "removed"})
	}
}

// GetMyTeamCoachIDs lists the coach ids of the caller's team (consumed by payments).
func GetMyTeamCoachIDs(svc services.TeamService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := coachCaller(c)
		if !ok {
			return
		}
		ids, apiErr := svc.MyCoachIDs(c.Request.Context(), id)
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"coach_ids": ids})
	}
}

// GetMyHeadCoach returns the head coach of the calling coach, or null.
func GetMyHeadCoach(svc services.TeamService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := coachCaller(c)
		if !ok {
			return
		}
		head, apiErr := svc.MyHeadCoach(c.Request.Context(), id)
		if apiErr != nil {
			respondApiError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"head_coach": head})
	}
}
