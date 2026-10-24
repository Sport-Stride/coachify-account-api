package router

import (
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"runtime/debug"
	"strings"
	"time"

	jwt "github.com/golang-jwt/jwt/v4"
	ginprometheus "github.com/zsais/go-gin-prometheus"

	"coachify-account-api/utils"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func initializeMiddlewares(r *gin.Engine) {
	// add cors headers
	r.Use(securityHeaders())
	r.Use(RecoveryWithZap(utils.Logger, true))
	p := ginprometheus.NewPrometheus("gin")
	p.ReqCntURLLabelMappingFn = func(c *gin.Context) string {
		if full := c.FullPath(); full != "" {
			return full
		}
		return "unknown"
	}
	p.Use(r) 
	// dump request in debug
	if gin.Mode() == gin.DebugMode {
		r.Use(requestLogger())
	}

}

// Add a separate middleware for security headers
func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Set security headers
		c.Header("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; font-src 'self'; connect-src 'self'; frame-src 'self';")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-XSS-Protection", "1; mode=block")

		c.Next()
	}
}
// requestLogger logs method, path, status and latency only. Request bodies
// carry credentials and tokens and must never be logged.
func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		zap.L().Debug("request",
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("latency", time.Since(start)),
		)
	}
}

func Ginzap(logger utils.LogWrapperObj, timeFormat string, utc bool, alwaysLog bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		// some evil middlewares modify this values
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery
		c.Next()

		end := time.Now()
		latency := end.Sub(start)
		if utc {
			end = end.UTC()
		}

		if len(c.Errors) > 0 {
			// Append error field if this is an erroneous request.
			for _, e := range c.Errors.Errors() {
				logger.Error(e)
			}
		} else {
			if alwaysLog || c.Writer.Status() >= 300 {
				logger.Info(path,
					zap.Int("status", c.Writer.Status()),
					zap.String("method", c.Request.Method),
					zap.String("path", path),
					zap.String("query", query),
					zap.String("ip", c.ClientIP()),
					zap.String("identifier-agent", c.Request.UserAgent()),
					zap.String("time", end.Format(timeFormat)),
					zap.Duration("latency", latency),
				)
			}
		}
	}
}

func RecoveryWithZap(logger utils.LogWrapperObj, stack bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				// Check for a broken connection, as it is not really a
				// condition that warrants a panic stack trace.
				var brokenPipe bool
				if ne, ok := err.(*net.OpError); ok {
					if se, ok := ne.Err.(*os.SyscallError); ok {
						if strings.Contains(strings.ToLower(se.Error()), "broken pipe") || strings.Contains(strings.ToLower(se.Error()), "connection reset by peer") {
							brokenPipe = true
						}
					}
				}

				httpRequest, _ := httputil.DumpRequest(redactedRequest(c.Request), false)
				if brokenPipe {
					logger.Error(c.Request.URL.Path,
						zap.Any("error", err),
						zap.String("request", string(httpRequest)),
					)
					// If the connection is dead, we can't write a status to it.
					c.Error(err.(error)) // nolint: errcheck
					c.Abort()
					return
				}

				if stack {
					logger.Error("[Recovery from panic]",
						zap.Time("time", time.Now()),
						zap.Any("error", err),
						zap.String("request", string(httpRequest)),
						zap.String("stack", string(debug.Stack())),
					)
				} else {
					logger.Error("[Recovery from panic]",
						zap.Time("time", time.Now()),
						zap.Any("error", err),
						zap.String("request", string(httpRequest)),
					)
				}
				c.AbortWithStatus(http.StatusInternalServerError)
			}
		}()
		c.Next()
	}
}

// Middleware to validate the JWT token
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		secretKey := []byte(utils.LoadConfig().CoachifySecretKey)

		// Retrieve the token from the Authorization header
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization header missing"})
			c.Abort()
			return
		}

		// The expected format is "Bearer <token>"
		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid authorization format"})
			c.Abort()
			return
		}

		// Extract the token
		encryptedTokenString := parts[1]
		// Decrypt the token using AES-GCM
		tokenString, err := utils.Decrypt(encryptedTokenString, []byte(utils.LoadConfig().CoachifyEncryptionKey))
		if err != nil {
			zap.L().Debug("token decryption failed", zap.Error(err))
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token encryption"})
			c.Abort()
			return
		}
		// Validate the token
		token, err := utils.ParseSignedToken(tokenString, secretKey)

		if err != nil || !token.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired token"})
			c.Abort()
			return
		}
		c.Set("AuthorizationToken", tokenString)

		// Extract claims and set them in context (do not rely on client payload)
		if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
			if id, ok := claims["id"].(string); ok {
				c.Set("userID", id)
			} else {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token claims: id missing"})
				c.Abort()
				return
			}
			if role, ok := claims["role"].(string); ok {
				c.Set("userRole", role)
			}
		}

		// Continue to the protected route
		c.Next()
	}
}

// redactedRequest returns a copy of r whose credential headers are masked, so
// request dumps in panic logs never contain tokens or session cookies.
func redactedRequest(r *http.Request) *http.Request {
	clone := r.Clone(r.Context())
	for _, h := range []string{"Authorization", "Cookie", "X-Auth-Token", "X-Api-Key"} {
		if clone.Header.Get(h) != "" {
			clone.Header.Set(h, "[REDACTED]")
		}
	}
	return clone
}
