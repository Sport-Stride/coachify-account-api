package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// requestLogger must never write request bodies (passwords, tokens) to the logs.
func TestRequestLoggerDoesNotLogBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	core, logs := observer.New(zapcore.DebugLevel)
	restore := zap.ReplaceGlobals(zap.New(core))
	defer restore()

	const sentinel = "SENTINEL-PASSWORD-9f3a"
	var handlerSaw string
	r := gin.New()
	r.Use(requestLogger())
	r.POST("/user/login", func(c *gin.Context) {
		var in map[string]string
		require.NoError(t, c.ShouldBindJSON(&in))
		handlerSaw = in["password"]
		c.Status(http.StatusOK)
	})

	body := fmt.Sprintf(`{"email":"a@b.c","password":"%s","token":"%s","csrfToken":"%s"}`, sentinel, sentinel, sentinel)
	req := httptest.NewRequest(http.MethodPost, "/user/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+sentinel)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, sentinel, handlerSaw, "handler must still receive the body")

	entries := logs.All()
	require.NotEmpty(t, entries)
	for _, e := range entries {
		assert.NotContains(t, e.Message, sentinel)
		for _, f := range e.Context {
			assert.NotContains(t, fmt.Sprintf("%+v", f), sentinel)
		}
	}
}
