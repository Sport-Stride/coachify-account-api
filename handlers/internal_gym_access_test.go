package handlers_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"coachify-account-api/handlers"
	"coachify-account-api/models"
	"coachify-account-api/services"
	"coachify-account-api/services/mocks"
)

func TestInternalGymAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	build := func(svc *mocks.GymClientService) *gin.Engine {
		r := gin.New()
		r.POST("/internal/gym-access", handlers.InternalGymAccess(svc))
		return r
	}
	path := "/internal/gym-access"
	body := `{"user_id":"u","gym_id":"g"}`

	for name, tc := range map[string]struct {
		res  services.GymAccess
		want string
	}{
		"owner":  {services.GymAccess{StaffRole: "owner"}, `{"staff_role":"owner","client_active":false}`},
		"staff":  {services.GymAccess{StaffRole: "coach"}, `{"staff_role":"coach","client_active":false}`},
		"client": {services.GymAccess{ClientActive: true}, `{"staff_role":"","client_active":true}`},
		"none":   {services.GymAccess{}, `{"staff_role":"","client_active":false}`},
	} {
		t.Run(name, func(t *testing.T) {
			svc := &mocks.GymClientService{}
			res := tc.res
			svc.On("Access", mock.Anything, "u", "g").Return(&res, nil)
			w := do(build(svc), http.MethodPost, path, body)
			assert.Equal(t, http.StatusOK, w.Code)
			assert.JSONEq(t, tc.want, w.Body.String())
		})
	}
	t.Run("bad input is 400", func(t *testing.T) {
		for _, b := range []string{`nope`, `{"user_id":"u"}`, `{"gym_id":"g"}`, `{"user_id":"","gym_id":"g"}`} {
			w := do(build(&mocks.GymClientService{}), http.MethodPost, path, b)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), `"error"`)
		}
	})
	t.Run("service error hides detail", func(t *testing.T) {
		svc := &mocks.GymClientService{}
		svc.On("Access", mock.Anything, "u", "g").Return(nil, models.NewApiError(http.StatusInternalServerError, errors.New("secret")))
		w := do(build(svc), http.MethodPost, path, body)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.JSONEq(t, `{"error":"internal error"}`, w.Body.String())
	})
}
