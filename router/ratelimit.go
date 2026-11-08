package router

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// globalBucketFactor scales the per-route backstop relative to the per-key limit, so spread-out
// callers (for example behind a proxy that hides client IPs) cannot bypass the limiter.
const globalBucketFactor = 20

type rateWindow struct {
	count int
	reset time.Time
}

type rateLimiter struct {
	mu      sync.Mutex
	perKey  int
	global  int
	window  time.Duration
	keys    map[string]*rateWindow
	total   rateWindow
	lastGC  time.Time
	nowFunc func() time.Time
}

func (l *rateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.nowFunc()

	// Periodic cleanup of expired windows keeps the map bounded.
	if now.Sub(l.lastGC) >= l.window {
		for k, w := range l.keys {
			if !now.Before(w.reset) {
				delete(l.keys, k)
			}
		}
		l.lastGC = now
	}

	if !now.Before(l.total.reset) {
		l.total = rateWindow{reset: now.Add(l.window)}
	}
	w, ok := l.keys[key]
	if !ok || !now.Before(w.reset) {
		w = &rateWindow{reset: now.Add(l.window)}
		l.keys[key] = w
	}
	if w.count >= l.perKey || l.total.count >= l.global {
		return false
	}
	w.count++
	l.total.count++
	return true
}

// RateLimit allows perKey requests per window for each key, plus a per-route global bucket as a backstop.
// State is in memory and per instance. Over the limit it answers 429 {"error":"too many requests"}.
func RateLimit(perKey int, window time.Duration, key func(*gin.Context) string) gin.HandlerFunc {
	return newRateLimit(perKey, window, key, time.Now)
}

func newRateLimit(perKey int, window time.Duration, key func(*gin.Context) string, now func() time.Time) gin.HandlerFunc {
	l := &rateLimiter{
		perKey:  perKey,
		global:  perKey * globalBucketFactor,
		window:  window,
		keys:    map[string]*rateWindow{},
		lastGC:  now(),
		nowFunc: now,
	}
	return func(c *gin.Context) {
		if !l.allow(key(c)) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "too many requests"})
			return
		}
		c.Next()
	}
}

// clientIPKey keys the limiter by client IP.
func clientIPKey(c *gin.Context) string { return c.ClientIP() }
