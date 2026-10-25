package router

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	jwt "github.com/golang-jwt/jwt/v4"
	ginprometheus "github.com/zsais/go-gin-prometheus"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/services"
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

// AuthMiddleware validates the JWT only; use AuthMiddlewareWithStatus to also re-check the account status.
func AuthMiddleware() gin.HandlerFunc {
	return AuthMiddlewareWithStatus(nil)
}

// UserStatusLookup is the account status source for the middleware.
type UserStatusLookup = services.UserStatusLookup

// StatusGate rejects inactive accounts, caching each user's status for a short TTL so the
// database is not hit on every request.
type StatusGate struct {
	lookup UserStatusLookup
	ttl    time.Duration
	mu     sync.Mutex
	cache  map[string]statusEntry
}

type statusEntry struct {
	status  db.UserStatus
	expires time.Time
}

// NewStatusGate returns nil when lookup is nil, which disables the check.
func NewStatusGate(lookup UserStatusLookup, ttl time.Duration) *StatusGate {
	if lookup == nil {
		return nil
	}
	return &StatusGate{lookup: lookup, ttl: ttl, cache: make(map[string]statusEntry)}
}

func (g *StatusGate) status(ctx context.Context, userID string) (db.UserStatus, error) {
	now := time.Now()
	g.mu.Lock()
	if e, ok := g.cache[userID]; ok && now.Before(e.expires) {
		g.mu.Unlock()
		return e.status, nil
	}
	g.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	st, err := g.lookup.GetStatusByExternalID(ctx, userID)
	if err != nil {
		return "", err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.cache) > 10000 { // bound memory: drop everything rather than track LRU
		g.cache = make(map[string]statusEntry)
	}
	g.cache[userID] = statusEntry{status: st, expires: now.Add(g.ttl)}
	return st, nil
}

// allow aborts the request and returns false when the account may not authenticate.
func (g *StatusGate) allow(c *gin.Context) bool {
	userID, _ := c.Get("userID")
	id, _ := userID.(string)
	st, err := g.status(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, models.ErrUserNotFound) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired token"})
		} else {
			zap.L().Error("auth status lookup failed", zap.String("component", "auth"), zap.Error(err))
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Service temporarily unavailable"})
		}
		c.Abort()
		return false
	}
	if st.IsInactive() {
		msg := models.ErrAccountSuspended.Error()
		if st == db.Blocked {
			msg = models.ErrUserBlocked.Error()
		}
		c.JSON(http.StatusForbidden, gin.H{"error": msg})
		c.Abort()
		return false
	}
	return true
}

// AuthMiddlewareWithStatus validates the JWT token and, when gate is set, the account status.
func AuthMiddlewareWithStatus(gate *StatusGate) gin.HandlerFunc {
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
		// Re-check the account status so a blocked or deactivated user is cut off before the JWT expires.
		if gate != nil && !gate.allow(c) {
			return
		}

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
