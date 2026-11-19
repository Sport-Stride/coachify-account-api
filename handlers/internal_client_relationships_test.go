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

func TestInternalClientRelationships(t *testing.T) {
	gin.SetMode(gin.TestMode)
	build := func(svc *mocks.GymClientService) *gin.Engine {
		r := gin.New()
		r.POST("/internal/clients/relationships", handlers.InternalClientRelationships(svc))
		return r
	}
	path := "/internal/clients/relationships"

	t.Run("success", func(t *testing.T) {
		svc := &mocks.GymClientService{}
		svc.On("Relationships", mock.Anything, "a", "c").Return(&services.ClientRelationships{
			Personal: true, Gyms: []services.GymRelation{{GymID: "g1", ActorRole: "coach", AssignedToActor: true}},
		}, nil)
		w := do(build(svc), http.MethodPost, path, `{"actor_id":"a","client_id":"c"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.JSONEq(t, `{"personal":true,"gyms":[{"gym_id":"g1","actor_role":"coach","assigned_to_actor":true}]}`, w.Body.String())
	})
	t.Run("bad body is 400", func(t *testing.T) {
		for _, b := range []string{`nope`, `{"actor_id":"a"}`, `{"client_id":"c"}`} {
			w := do(build(&mocks.GymClientService{}), http.MethodPost, path, b)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), `"error"`)
		}
	})
	t.Run("service error hides detail", func(t *testing.T) {
		svc := &mocks.GymClientService{}
		svc.On("Relationships", mock.Anything, "a", "c").Return(nil, models.NewApiError(http.StatusInternalServerError, errors.New("secret")))
		w := do(build(svc), http.MethodPost, path, `{"actor_id":"a","client_id":"c"}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.JSONEq(t, `{"error":"internal error"}`, w.Body.String())
	})
}
