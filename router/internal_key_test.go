package router_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"coachify-account-api/router"
)

func internalKeyRouter(key string, required bool) *gin.Engine {
	r := gin.New()
	g := r.Group("/internal")
	g.Use(router.NewInternalAPIKeyMiddleware(func() (string, bool) { return key, required }))
	g.GET("/ping", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	return r
}

func callInternal(r http.Handler, header string, set bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/internal/ping", nil)
	if set {
		req.Header.Set("x-api-key", header)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestInternalAPIKeyRequired(t *testing.T) {
	r := internalKeyRouter("s3cret", true)
	assert.Equal(t, http.StatusOK, callInternal(r, "s3cret", true).Code, "correct key")
	assert.Equal(t, http.StatusUnauthorized, callInternal(r, "", false).Code, "missing key")
	assert.Equal(t, http.StatusUnauthorized, callInternal(r, "wrong", true).Code, "wrong key")
	assert.Equal(t, http.StatusUnauthorized, callInternal(r, "s3cre", true).Code, "prefix of the key")
	w := callInternal(r, "", false)
	assert.JSONEq(t, `{"error":"Invalid or missing API key"}`, w.Body.String())
}

func TestInternalAPIKeyUnsetRejectsWhenRequired(t *testing.T) {
	r := internalKeyRouter("", true)
	assert.Equal(t, http.StatusUnauthorized, callInternal(r, "", true).Code, "empty key must never match an empty header")
}

func TestInternalAPIKeyFlagOffPasses(t *testing.T) {
	r := internalKeyRouter("s3cret", false)
	assert.Equal(t, http.StatusOK, callInternal(r, "s3cret", true).Code)
	assert.Equal(t, http.StatusOK, callInternal(r, "", false).Code, "report mode: missing key is only logged")
	assert.Equal(t, http.StatusOK, callInternal(r, "wrong", true).Code, "report mode: wrong key is only logged")
	assert.Equal(t, http.StatusOK, callInternal(internalKeyRouter("", false), "", false).Code)
}
