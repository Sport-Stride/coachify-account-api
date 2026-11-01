package services_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/handlers"
	"coachify-account-api/models/db"
)

// curl-style check through the real handler and resolver: a user without grants has every
// applicable feature enabled, source "default", unlimited.
func TestInternalEntitlementsEndpointDefaultsForUserWithNoGrants(t *testing.T) {
	gin.SetMode(gin.TestMode)
	e := newEntEnv()
	r := gin.New()
	r.GET("/internal/entitlements/:user_id", handlers.GetInternalEntitlements(e.svc))

	req := httptest.NewRequest(http.MethodGet, "/internal/entitlements/c1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		UserID     string `json:"user_id"`
		Role       string `json:"role"`
		Plan       any    `json:"plan"`
		TTLSeconds int    `json:"ttl_seconds"`
		Features   map[string]struct {
			Enabled bool                `json:"enabled"`
			Source  string              `json:"source"`
			Limits  map[string]*float64 `json:"limits"`
		} `json:"features"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "c1", body.UserID)
	assert.Equal(t, "coach", body.Role)
	assert.Nil(t, body.Plan)
	assert.Equal(t, 60, body.TTLSeconds)
	require.Len(t, body.Features, 5)
	for key, f := range body.Features {
		assert.True(t, f.Enabled, key)
		assert.Equal(t, "default", f.Source, key)
		for l, v := range f.Limits {
			assert.Nil(t, v, "%s.%s is unlimited", key, l)
		}
	}
	assert.Contains(t, w.Body.String(), `"max_bytes":null`)
	assert.Contains(t, w.Body.String(), `"max_clients":null`)

	// an override flips only its feature; the rest stays default
	e.store.grants["c1"] = &db.EntitlementGrant{SubjectID: "c1", Features: map[string]db.FeatureGrant{"chat": {Enabled: bptr(false)}}}
	e2 := newEntEnv()
	e2.store.grants = e.store.grants
	r2 := gin.New()
	r2.GET("/internal/entitlements/:user_id", handlers.GetInternalEntitlements(e2.svc))
	w = httptest.NewRecorder()
	r2.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/internal/entitlements/c1", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.False(t, body.Features["chat"].Enabled)
	assert.Equal(t, "override", body.Features["chat"].Source)
	assert.Equal(t, "default", body.Features["workouts"].Source)

	// unknown user
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/internal/entitlements/ghost", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
}
