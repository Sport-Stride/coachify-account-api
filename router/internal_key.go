package router

import (
	"crypto/subtle"
	"net/http"

	"coachify-account-api/utils"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// InternalAPIKeyMiddleware guards service-to-service endpoints with a shared secret (env INTERNAL_API_KEY)
// sent in the x-api-key header. An unset key rejects every request. With INTERNAL_API_KEY_REQUIRED=false
// (rollout mode) a missing or wrong key is logged and the request is allowed.
func InternalAPIKeyMiddleware() gin.HandlerFunc {
	return internalAPIKeyMiddleware(func() (string, bool) {
		cfg := utils.LoadConfig()
		return cfg.InternalAPIKey, cfg.InternalAPIKeyRequired
	})
}

// NewInternalAPIKeyMiddleware is InternalAPIKeyMiddleware with explicit settings (used by tests).
func NewInternalAPIKeyMiddleware(settings func() (key string, required bool)) gin.HandlerFunc {
	return internalAPIKeyMiddleware(settings)
}

func internalAPIKeyMiddleware(settings func() (key string, required bool)) gin.HandlerFunc {
	return func(c *gin.Context) {
		expected, required := settings()
		provided := c.GetHeader("x-api-key")
		if expected != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1 {
			c.Next()
			return
		}
		if !required {
			zap.L().Warn("internal_key_missing",
				zap.String("path", loggedPath(c)),
				zap.String("ip", c.ClientIP()),
			)
			c.Next()
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or missing API key"})
		c.Abort()
	}
}
