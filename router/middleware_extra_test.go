package router

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"coachify-account-api/utils"
)

func observedLogger() (utils.LogWrapperObj, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	return utils.LogWrapperObj{Logger: zap.New(core).Sugar()}, logs
}

func allLogText(logs *observer.ObservedLogs) string {
	out := ""
	for _, e := range logs.All() {
		out += e.Message + " "
		for k, v := range e.ContextMap() {
			out += k + "=" + toStr(v) + " "
		}
	}
	return out
}

func toStr(v interface{}) string {
	switch s := v.(type) {
	case string:
		return s
	case error:
		return s.Error()
	}
	return ""
}

func TestSecurityHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(securityHeaders())
	r.GET("/x", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
	assert.Contains(t, w.Header().Get("Strict-Transport-Security"), "max-age=")
	assert.Contains(t, w.Header().Get("Content-Security-Policy"), "default-src 'self'")
}

func TestRecoveryWithZap_PanicReturns500WithoutLeakingDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger, logs := observedLogger()
	r := gin.New()
	r.Use(RecoveryWithZap(logger, true))
	r.GET("/boom", func(c *gin.Context) { panic("db password is hunter2") })
	r.GET("/ok", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NotContains(t, w.Body.String(), "hunter2", "panic detail must not reach the client")
	assert.NotContains(t, w.Body.String(), "goroutine", "no stack trace to the client")
	require.Equal(t, 1, logs.Len())
	assert.Equal(t, "[Recovery from panic]", logs.All()[0].Message)

	// Server keeps serving after a recovered panic.
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ok", nil))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRecoveryWithZap_DoesNotLogCredentialHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stack := range []bool{true, false} {
		logger, logs := observedLogger()
		r := gin.New()
		r.Use(RecoveryWithZap(logger, stack))
		r.GET("/boom", func(c *gin.Context) { panic("x") })

		req := httptest.NewRequest(http.MethodGet, "/boom", nil)
		req.Header.Set("Authorization", "Bearer SENTINEL-TOKEN-123")
		req.Header.Set("Cookie", "session=SENTINEL-COOKIE-456")
		req.Header.Set("X-Api-Key", "SENTINEL-KEY-789")
		r.ServeHTTP(httptest.NewRecorder(), req)

		require.Equal(t, 1, logs.Len())
		text := allLogText(logs)
		assert.NotContains(t, text, "SENTINEL-TOKEN-123")
		assert.NotContains(t, text, "SENTINEL-COOKIE-456")
		assert.NotContains(t, text, "SENTINEL-KEY-789")
		assert.Contains(t, text, "/boom", "request line is still logged")
	}
}

func TestRedactedRequestDoesNotMutateOriginal(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer abc")
	clone := redactedRequest(req)
	assert.Equal(t, "[REDACTED]", clone.Header.Get("Authorization"))
	assert.Equal(t, "Bearer abc", req.Header.Get("Authorization"))
	assert.Empty(t, clone.Header.Get("Cookie"), "absent headers are not invented")
}

func TestGinzap(t *testing.T) {
	gin.SetMode(gin.TestMode)

	run := func(alwaysLog bool, status int, withError bool) *observer.ObservedLogs {
		logger, logs := observedLogger()
		r := gin.New()
		r.Use(Ginzap(logger, time.RFC3339, true, alwaysLog))
		r.GET("/x", func(c *gin.Context) {
			if withError {
				_ = c.Error(assert.AnError)
			}
			c.Status(status)
		})
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x?q=1", nil))
		return logs
	}

	assert.Equal(t, 0, run(false, http.StatusOK, false).Len(), "2xx not logged unless alwaysLog")
	assert.Equal(t, 1, run(true, http.StatusOK, false).Len())
	assert.Equal(t, 1, run(false, http.StatusNotFound, false).Len(), ">=300 is logged")

	logs := run(false, http.StatusInternalServerError, true)
	require.Equal(t, 1, logs.Len())
	assert.Equal(t, zapcore.ErrorLevel, logs.All()[0].Level)
}
