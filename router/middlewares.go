package router

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"log"
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
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Allow specific origins (replace "*" with your frontend URL in production)
		allowedOrigin := "http://localhost:3000"
		if origin := c.Request.Header.Get("Origin"); origin != "" {
			allowedOrigin = origin // Allow the requesting origin
		}
		log.Printf("IBL: allowed origin is %s", allowedOrigin)
		c.Writer.Header().Set("Access-Control-Allow-Origin", allowedOrigin)

		// Allow specific HTTP methods
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, GET, PUT, DELETE, OPTIONS, PATCH")

		// Allow specific headers
		c.Writer.Header().Set("Access-Control-Allow-Headers", strings.Join([]string{
			"Origin",
			"X-Requested-With",
			"Content-Type",
			"Accept",
			"Authorization",
			"Referrer",
			"User-Agent",
		}, ", "))

		// Allow credentials (e.g., cookies, authorization headers)
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")

		// Expose specific headers to the client
		c.Writer.Header().Set("Access-Control-Expose-Headers", "Content-Length, Content-Type, Authorization")

		// Set security headers
		c.Header("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; font-src 'self'; connect-src 'self'; frame-src 'self';")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-XSS-Protection", "1; mode=block")

		// Handle preflight requests
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent) // 204 No Content
			return
		}

		// Pass to the next middleware/handler
		c.Next()
	}
}
func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		buf, _ := ioutil.ReadAll(c.Request.Body)
		rdr1 := ioutil.NopCloser(bytes.NewBuffer(buf))
		rdr2 := ioutil.NopCloser(bytes.NewBuffer(buf)) //We have to create a new Buffer, because rdr1 will be read.

		utils.Logger.Logger.Debug(readBody(rdr1)) // Print request body

		c.Request.Body = rdr2
		c.Next()
	}
}

// util
func readBody(reader io.Reader) string {
	buf := new(bytes.Buffer)
	buf.ReadFrom(reader)

	s := buf.String()
	return s
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

				httpRequest, _ := httputil.DumpRequest(c.Request, false)
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
			log.Printf("Token decryption failed: %v", err)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token encryption"})
			c.Abort()
			return
		}
		// Validate the token
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			// Check if the signing method is correct
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return secretKey, nil
		})

		if err != nil || !token.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired token"})
			c.Abort()
			return
		}
		c.Set("AuthorizationToken", tokenString) // Set the token in context
		log.Printf("Token set in context: %s", tokenString)

		// Extract claims and set them in context (do not rely on client payload)
		if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
			// Ensure the 'id' claim exists and is a string
			// Print out all claims
			for key, value := range claims {
				log.Printf("Token Claim - %s: %v", key, value)
			}

			// Alternative method to get all claims as a map
			allClaims := map[string]interface{}(claims)
			log.Printf("All Token Claims: %+v", allClaims)
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
