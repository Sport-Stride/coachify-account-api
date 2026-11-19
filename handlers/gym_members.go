package handlers

import (
	"net/http"

	"coachify-account-api/services"

	"github.com/gin-gonic/gin"
)

// maxMemberPage mirrors repositories.MaxMemberPage: deeper pages are clamped.
const maxMemberPage = 10000

var validMemberRoles = map[string]bool{"owner": true, "head_coach": true, "coach": true}

type inviteCoachRequest struct {
	Email        string `json:"email"`
	SupervisorID string `json:"supervisor_id"`
}

type coachJoinLinkRequest struct {
	SupervisorID string `json:"supervisor_id"`
}

// ListGymMembers lists the members of a gym the caller may see (owner: all, head coach: self and supervisees).
func ListGymMembers(svc services.GymMembersService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetString("userID")
		if id == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		page, size := pagination(c)
		if page > maxMemberPage {
			page = maxMemberPage
		}
		role := c.Query("role")
		if role != "" && !validMemberRoles[role] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "role must be one of owner, head_coach, coach"})
			return
		}
		rows, total, apiErr := svc.ListMembers(c.Request.Context(), id, c.Param("gym_id"), role, page, size)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"members": rows, "total": total, "page": page, "size": size})
	}
}

// PutGymMemberSupervisor sets or clears the supervisor of a coach (owner only). The key supervisor_id is required;
// null clears it.
func PutGymMemberSupervisor(svc services.GymMembersService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetString("userID")
		if id == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		var req map[string]*string
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "supervisor_id is required (a user id or null)"})
			return
		}
		sup, ok := req["supervisor_id"]
		if !ok || (sup != nil && *sup == "") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "supervisor_id is required (a user id or null)"})
			return
		}
		supervisorID := ""
		if sup != nil {
			supervisorID = *sup
		}
		v, apiErr := svc.SetSupervisor(c.Request.Context(), id, c.Param("gym_id"), c.Param("user_id"), supervisorID)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, v)
	}
}

// InviteGymCoach emails a coach invitation (member.invite).
func InviteGymCoach(svc services.GymService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetString("userID")
		if id == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		var req inviteCoachRequest
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "email is required"})
			return
		}
		res, apiErr := svc.InviteCoach(c.Request.Context(), id, c.Param("gym_id"), req.Email, req.SupervisorID)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusAccepted, res)
	}
}

// CreateGymCoachJoinLink creates a single-use coach join link (member.invite).
func CreateGymCoachJoinLink(svc services.GymService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetString("userID")
		if id == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		var req coachJoinLinkRequest
		if c.Request.ContentLength > 0 && c.ShouldBindJSON(&req) != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
		res, apiErr := svc.CreateCoachJoinLink(c.Request.Context(), id, c.Param("gym_id"), req.SupervisorID)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusCreated, res)
	}
}
