package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"coachify-account-api/handlers"
	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/services"
)

type fakeInternalGym struct {
	services.GymService
	gym *db.Gym
	err *models.ApiError
}

func (f *fakeInternalGym) GetGymInternal(context.Context, string) (*db.Gym, *models.ApiError) {
	return f.gym, f.err
}

func internalGymGet(svc services.GymService, id string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/internal/gyms/:gym_id", handlers.GetInternalGym(svc))
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/internal/gyms/"+id, nil)
	r.ServeHTTP(w, req)
	return w
}

func TestGetInternalGym(t *testing.T) {
	oid := primitive.NewObjectID()
	t.Run("200", func(t *testing.T) {
		w := internalGymGet(&fakeInternalGym{gym: &db.Gym{ID: oid, Name: "Iron", OwnerID: "o1", Status: "active"}}, oid.Hex())
		assert.Equal(t, http.StatusOK, w.Code)
		assert.JSONEq(t, `{"gym_id":"`+oid.Hex()+`","name":"Iron","owner_id":"o1","status":"active"}`, w.Body.String())
	})
	t.Run("404", func(t *testing.T) {
		w := internalGymGet(&fakeInternalGym{err: &models.ApiError{Code: http.StatusNotFound, Error: assert.AnError}}, oid.Hex())
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), "error")
	})
	t.Run("invalid id", func(t *testing.T) {
		w := internalGymGet(&fakeInternalGym{}, "nope")
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}
