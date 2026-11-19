package router_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"coachify-account-api/handlers"
	"coachify-account-api/models"
	"coachify-account-api/router"
	"coachify-account-api/services"
)

type okResolver struct{}

func (okResolver) Resolve(context.Context, string, services.EntContext) (*services.ContextEntitlements, *models.ApiError) {
	return &services.ContextEntitlements{Context: "personal", Features: []string{}}, nil
}

// The internal entitlements endpoint sits behind the x-api-key middleware, like the rest of /internal.
func TestInternalUserEntitlementsRequiresKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/internal")
	g.Use(router.NewInternalAPIKeyMiddleware(func() (string, bool) { return "secret", true }))
	g.GET("/users/:id/entitlements", handlers.GetInternalUserEntitlements(okResolver{}))

	do := func(key string) int {
		req := httptest.NewRequest(http.MethodGet, "/internal/users/u1/entitlements?context=personal", nil)
		if key != "" {
			req.Header.Set("x-api-key", key)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	assert.Equal(t, http.StatusUnauthorized, do(""))
	assert.Equal(t, http.StatusUnauthorized, do("wrong"))
	assert.Equal(t, http.StatusOK, do("secret"))
}
