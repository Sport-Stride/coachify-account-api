package router

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func limitedEngine(perKey int, window time.Duration, now func() time.Time) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	key := func(c *gin.Context) string { return c.GetHeader("X-Key") }
	r.GET("/x", newRateLimit(perKey, window, key, now), func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

func hit(r *gin.Engine, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Key", key)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestRateLimitBlocksAfterNAndResets(t *testing.T) {
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := limitedEngine(3, time.Minute, func() time.Time { return clock })

	for i := 0; i < 3; i++ {
		assert.Equal(t, http.StatusOK, hit(r, "a").Code)
	}
	w := hit(r, "a")
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.JSONEq(t, `{"error":"too many requests"}`, w.Body.String())

	// Another key has its own budget.
	assert.Equal(t, http.StatusOK, hit(r, "b").Code)

	clock = clock.Add(time.Minute + time.Second)
	assert.Equal(t, http.StatusOK, hit(r, "a").Code, "window elapsed")
}

func TestRateLimitGlobalBackstop(t *testing.T) {
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := limitedEngine(1, time.Minute, func() time.Time { return clock })

	// Each key stays within its own limit, but the route-wide bucket (perKey * factor) runs out.
	ok := 0
	for i := 0; i < globalBucketFactor+5; i++ {
		if hit(r, string(rune('A'+i))).Code == http.StatusOK {
			ok++
		}
	}
	assert.Equal(t, globalBucketFactor, ok)
}
