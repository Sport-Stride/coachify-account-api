package handlers

import (
	"net/http"

	"coachify-account-api/models/db"
	"coachify-account-api/services"

	"github.com/gin-gonic/gin"
)

// roleCaller returns the caller id when the token role matches. The services re-check role, status and ownership in the database.
func roleCaller(c *gin.Context, role, denied string) (string, bool) {
	id := c.GetString("userID")
	if id == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return "", false
	}
	if c.GetString("userRole") != role {
		c.JSON(http.StatusForbidden, gin.H{"error": denied})
		return "", false
	}
	return id, true
}

func gymOwnerCaller(c *gin.Context) (string, bool) {
	return roleCaller(c, db.RoleGymOwner, "Gym owner access required")
}

func gymCoachCaller(c *gin.Context) (string, bool) {
	return roleCaller(c, "coach", "Coach access required")
}

func headCoachCaller(c *gin.Context) (string, bool) {
	return roleCaller(c, "coach", "Head coach access required")
}

var validGymClientSorts = map[string]bool{"name": true, "linked_at": true}

// ---------- Gym owner ----------

func GetGymOwnerMe(svc services.GymService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := gymOwnerCaller(c)
		if !ok {
			return
		}
		me, apiErr := svc.OwnerMe(c.Request.Context(), id)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, me)
	}
}

func GetGymOverview(svc services.GymService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := gymOwnerCaller(c)
		if !ok {
			return
		}
		o, apiErr := svc.Overview(c.Request.Context(), id, c.Param("gym_id"))
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, o)
	}
}

type renameGymRequest struct {
	Name string `json:"name" binding:"required"`
}

func RenameGym(svc services.GymService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := gymOwnerCaller(c)
		if !ok {
			return
		}
		var req renameGymRequest
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
			return
		}
		g, apiErr := svc.RenameGym(c.Request.Context(), id, c.Param("gym_id"), req.Name)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"gym": g})
	}
}

func ListGymHeadCoaches(svc services.GymService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := gymOwnerCaller(c)
		if !ok {
			return
		}
		page, size := pagination(c)
		rows, total, apiErr := svc.ListHeadCoaches(c.Request.Context(), id, c.Param("gym_id"), searchQuery(c), page, size)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"head_coaches": rows, "total": total, "page": page, "size": size})
	}
}

func RemoveGymHeadCoach(svc services.GymService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := gymOwnerCaller(c)
		if !ok {
			return
		}
		if apiErr := svc.RemoveHeadCoach(c.Request.Context(), id, c.Param("gym_id"), c.Param("id")); apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func ListGymCoaches(svc services.GymService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := gymOwnerCaller(c)
		if !ok {
			return
		}
		tq, valid := teamListQuery(c)
		if !valid {
			return
		}
		q := db.GymCoachQuery{
			HeadCoachID: c.Query("head_coach_id"), Page: tq.Page, Size: tq.Size,
			Q: tq.Q, Status: tq.Status, Sort: tq.Sort,
		}
		rows, total, apiErr := svc.ListCoaches(c.Request.Context(), id, c.Param("gym_id"), q)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"coaches": rows, "total": total, "page": q.Page, "size": q.Size})
	}
}

func ListGymClients(svc services.GymService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := gymOwnerCaller(c)
		if !ok {
			return
		}
		sort := c.Query("sort")
		if sort != "" && !validGymClientSorts[sort] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid sort"})
			return
		}
		page, size := pagination(c)
		q := db.GymClientQuery{
			HeadCoachID: c.Query("head_coach_id"), CoachID: c.Query("coach_id"),
			Page: page, Size: size, Q: searchQuery(c), Sort: sort,
		}
		rows, total, apiErr := svc.ListClients(c.Request.Context(), id, c.Param("gym_id"), q)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"items": rows, "total": total, "page": page, "size": size})
	}
}

type inviteHeadCoachRequest struct {
	Email string `json:"email" binding:"required"`
}

func InviteGymHeadCoach(svc services.GymService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := gymOwnerCaller(c)
		if !ok {
			return
		}
		var req inviteHeadCoachRequest
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "email is required"})
			return
		}
		res, apiErr := svc.Invite(c.Request.Context(), id, c.Param("gym_id"), req.Email)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusAccepted, res)
	}
}

func ListGymInvitations(svc services.GymService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := gymOwnerCaller(c)
		if !ok {
			return
		}
		status := c.Query("status")
		if status != "" && !services.IsValidGymInviteStatus(status) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid status"})
			return
		}
		page, size := pagination(c)
		rows, total, apiErr := svc.ListInvitations(c.Request.Context(), id, c.Param("gym_id"), status, page, size)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"invitations": rows, "total": total, "page": page, "size": size})
	}
}

func RevokeGymInvitation(svc services.GymService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := gymOwnerCaller(c)
		if !ok {
			return
		}
		if apiErr := svc.RevokeInvitation(c.Request.Context(), id, c.Param("gym_id"), c.Param("id")); apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

// ---------- Coach self-service ----------

func ListMyGymInvitations(svc services.GymService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := gymCoachCaller(c)
		if !ok {
			return
		}
		invs, apiErr := svc.ListMyInvitations(c.Request.Context(), id)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"invitations": invs})
	}
}

func AcceptGymInvitation(svc services.GymService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := gymCoachCaller(c)
		if !ok {
			return
		}
		joined, apiErr := svc.AcceptInvitation(c.Request.Context(), id, c.Param("id"))
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"gym": joined})
	}
}

func DeclineGymInvitation(svc services.GymService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := gymCoachCaller(c)
		if !ok {
			return
		}
		if apiErr := svc.DeclineInvitation(c.Request.Context(), id, c.Param("id")); apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "declined"})
	}
}

// ---------- Head coach ----------

// GetMyGym returns the gym of the calling head coach, or null.
func GetMyGym(svc services.GymService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := headCoachCaller(c)
		if !ok {
			return
		}
		gym, apiErr := svc.MyGym(c.Request.Context(), id)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"gym": gym})
	}
}

func LeaveMyGym(svc services.GymService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := headCoachCaller(c)
		if !ok {
			return
		}
		if apiErr := svc.LeaveGym(c.Request.Context(), id); apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

// ---------- Admin ----------

func ListAdminGyms(svc services.GymService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		status := c.Query("status")
		if status != "" && status != db.GymStatusActive && status != db.GymStatusArchived {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid status"})
			return
		}
		page, size := pagination(c)
		rows, total, apiErr := svc.AdminListGyms(c.Request.Context(), page, size, searchQuery(c), status)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"gyms": rows, "total": total, "page": page, "size": size})
	}
}

func ListAdminGymHeadCoaches(svc services.GymService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		rows, apiErr := svc.AdminListGymHeadCoaches(c.Request.Context(), c.Param("id"))
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"head_coaches": rows, "total": len(rows)})
	}
}

type createGymRequest struct {
	Name    string `json:"name" binding:"required"`
	OwnerID string `json:"owner_id" binding:"required"`
}

func CreateAdminGym(svc services.GymService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		var req createGymRequest
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "name and owner_id are required"})
			return
		}
		g, apiErr := svc.AdminCreateGym(c.Request.Context(), c.GetString("userID"), req.Name, req.OwnerID)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusCreated, gin.H{"gym": g})
	}
}

type updateGymRequest struct {
	Name    *string `json:"name"`
	Status  *string `json:"status"`
	OwnerID *string `json:"owner_id"`
}

func UpdateAdminGym(svc services.GymService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		var req updateGymRequest
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
		g, apiErr := svc.AdminUpdateGym(c.Request.Context(), c.GetString("userID"), c.Param("id"),
			services.GymAdminUpdate{Name: req.Name, Status: req.Status, OwnerID: req.OwnerID})
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"gym": g})
	}
}

func AddAdminGymHeadCoach(svc services.GymService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		if apiErr := svc.AdminAddHeadCoach(c.Request.Context(), c.GetString("userID"), c.Param("id"), c.Param("hcid")); apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func RemoveAdminGymHeadCoach(svc services.GymService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		if apiErr := svc.AdminRemoveHeadCoach(c.Request.Context(), c.GetString("userID"), c.Param("id"), c.Param("hcid")); apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func ListAdminGymAudit(svc services.GymService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !adminOnly(c) {
			return
		}
		page, size := pagination(c)
		entries, total, apiErr := svc.AdminAudit(c.Request.Context(), c.Param("id"), page, size)
		if apiErr != nil {
			respondAdminError(c, apiErr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"entries": entries, "total": total, "page": page, "size": size})
	}
}
