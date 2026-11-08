package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// Link tokens in the URL path are credentials and must not reach the logs.
func TestRequestLoggerRedactsTokenPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	core, logs := observer.New(zapcore.DebugLevel)
	restore := zap.ReplaceGlobals(zap.New(core))
	defer restore()

	const secret = "SECRET-JOIN-TOKEN-123"
	r := gin.New()
	r.Use(requestLogger())
	r.GET("/gym-join/:token", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/health/:id", func(c *gin.Context) { c.Status(http.StatusOK) })

	for _, p := range []string{"/gym-join/" + secret, "/health/42"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
	}

	entries := logs.All()
	require.Len(t, entries, 2)
	for _, e := range entries {
		for _, f := range e.Context {
			assert.NotContains(t, f.String, secret)
		}
	}
	assert.Equal(t, "/gym-join/:token", entries[0].ContextMap()["path"])
	assert.Equal(t, "/health/42", entries[1].ContextMap()["path"], "other routes keep the real path")
}
