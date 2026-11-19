package handlers_test

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"coachify-account-api/handlers"
	"coachify-account-api/models"
	"coachify-account-api/services/mocks"
)

func newLeaveRouter(svc *mocks.GymClientLeaver, userID string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	p := r.Group("/")
	p.Use(func(c *gin.Context) {
		if userID != "" {
			c.Set("userID", userID)
		}
		c.Next()
	})
	p.POST("/me/gyms/:gym_id/leave", handlers.LeaveMyGymAsClient(svc))
	return r
}

func TestLeaveMyGymAsClient(t *testing.T) {
	t.Run("unauthenticated is 401", func(t *testing.T) {
		w := do(newLeaveRouter(&mocks.GymClientLeaver{}, ""), http.MethodPost, "/me/gyms/g1/leave", "")
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
	t.Run("success is 200", func(t *testing.T) {
		svc := &mocks.GymClientLeaver{}
		svc.On("LeaveGym", mock.Anything, "cl1", "g1").Return((*models.ApiError)(nil))
		w := do(newLeaveRouter(svc, "cl1"), http.MethodPost, "/me/gyms/g1/leave", "")
		assert.Equal(t, http.StatusOK, w.Code)
		svc.AssertExpectations(t)
	})
	t.Run("service error is surfaced as json error", func(t *testing.T) {
		svc := &mocks.GymClientLeaver{}
		svc.On("LeaveGym", mock.Anything, "cl1", "g1").Return(&models.ApiError{Code: http.StatusInternalServerError, Error: assert.AnError})
		w := do(newLeaveRouter(svc, "cl1"), http.MethodPost, "/me/gyms/g1/leave", "")
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Contains(t, decode(t, w), "error")
	})
}
