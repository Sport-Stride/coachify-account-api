package handlers

import (
"net/http"
"strconv"

"coachify-account-api/models/db"
"coachify-account-api/services"

"github.com/gin-gonic/gin"
"go.uber.org/zap"
)

// ListAdminCoaches returns a paginated list of coaches (user data only).
// Subscription enrichment is done by the frontend via payments-api directly.
func ListAdminCoaches(adminService services.AdminService) gin.HandlerFunc {
return func(c *gin.Context) {
if c.GetString("userRole") != "admin" {
c.JSON(http.StatusForbidden, gin.H{"error": "Admin access required"})
return
}

page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
if page < 1 {
page = 1
}
size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
if size < 1 || size > 100 {
size = 20
}

var filter db.AdminCoachFilter
if v := c.Query("head_coach"); v != "" {
b, perr := strconv.ParseBool(v)
if perr != nil {
c.JSON(http.StatusBadRequest, gin.H{"error": "invalid head_coach filter"})
return
}
filter.HeadCoach = &b
}
if v := c.Query("unassigned"); v != "" {
b, perr := strconv.ParseBool(v)
if perr != nil {
c.JSON(http.StatusBadRequest, gin.H{"error": "invalid unassigned filter"})
return
}
filter.Unassigned = b
}

coaches, total, err := adminService.ListCoaches(c.Request.Context(), page, size, filter)
if err != nil {
zap.L().Error("admin list coaches failed", zap.String("component", "admin"), zap.Error(err))
c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
return
}
c.JSON(http.StatusOK, gin.H{
"coaches": coaches,
"total":   total,
"page":    page,
"size":    size,
})
}
}

// ListAdminCoachClients returns all clients of a coach (user data only).
func ListAdminCoachClients(adminService services.AdminService) gin.HandlerFunc {
return func(c *gin.Context) {
if c.GetString("userRole") != "admin" {
c.JSON(http.StatusForbidden, gin.H{"error": "Admin access required"})
return
}

coachID := c.Param("id")
if coachID == "" {
c.JSON(http.StatusBadRequest, gin.H{"error": "Missing coach id"})
return
}

clients, err := adminService.ListCoachClients(c.Request.Context(), coachID)
if err != nil {
zap.L().Error("admin list coach clients failed", zap.String("component", "admin"), zap.Error(err))
c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
return
}
c.JSON(http.StatusOK, gin.H{"clients": clients, "total": len(clients)})
}
}
