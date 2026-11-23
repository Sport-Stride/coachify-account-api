package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/handlers"
	"coachify-account-api/models"
	"coachify-account-api/models/api"
)

type fakeCoachProfile struct {
	got     string
	profile *api.CoachPublicProfile
	err     *models.ApiError
}

func (f *fakeCoachProfile) GetPublicProfile(_ context.Context, coachID string) (*api.CoachPublicProfile, *models.ApiError) {
	f.got = coachID
	return f.profile, f.err
}

func coachProfileRouter(svc *fakeCoachProfile) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/coaches/:coach_id/profile", handlers.GetCoachPublicProfile(svc))
	return r
}

func TestGetCoachPublicProfile(t *testing.T) {
	t.Run("not found", func(t *testing.T) {
		svc := &fakeCoachProfile{err: &models.ApiError{Code: http.StatusNotFound, Error: models.ErrCoachNotFound}}
		w := httptest.NewRecorder()
		coachProfileRouter(svc).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/coaches/c1/profile", nil))
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.JSONEq(t, `{"error":"coach not found"}`, w.Body.String())
		assert.Equal(t, "c1", svc.got)
	})

	t.Run("success has no contact details", func(t *testing.T) {
		n := 3
		svc := &fakeCoachProfile{profile: &api.CoachPublicProfile{
			ExternalID: "c1", Firstname: "Prénom", Lastname: "Nom", Role: "coach", Specialties: []string{"yoga"}, Clients: &n,
		}}
		w := httptest.NewRecorder()
		coachProfileRouter(svc).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/coaches/c1/profile", nil))
		require.Equal(t, http.StatusOK, w.Code)
		var body map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		assert.Equal(t, float64(3), body["clients"])
		assert.Equal(t, []any{"yoga"}, body["specialties"])
		for _, k := range []string{"email", "phone_number", "personal", "metadata", "address"} {
			assert.NotContains(t, body, k)
		}
	})
}
