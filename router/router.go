package router

import (
	"time"

	"coachify-account-api/handlers"
	"coachify-account-api/services"
	"coachify-account-api/utils"

	"github.com/gin-gonic/gin"
)

func InitializeRouter(services *services.Services) *gin.Engine {
	// Set the default gin router
	r := gin.New()

	r.Use(gin.Recovery())

	// Initialize middlewares
	initializeMiddlewares(r)

	// Initialize routes
	initializeRoutes(r, services)

	return r
}

func initializeRoutes(r *gin.Engine, services *services.Services) {
	untracedGroup := r.Group("/")
	untracedGroup.Use(Ginzap(utils.Logger, time.RFC3339, true, false))

	// health
	untracedGroup.GET("/", handlers.GetHealth)
	untracedGroup.GET("/health", handlers.GetHealth)

	//auth with providers
	oauthGroup := untracedGroup.Group("/oauth")
	oauthGroup.GET("/:provider/login", handlers.OAuth2Login(services.AuthService))
	oauthGroup.GET("/:provider/callback", handlers.OAuth2Callback(services.AuthService))

	//auth endpoints
	userGroup := r.Group("/user")
	userGroup.POST("/signup", handlers.Register(services.AuthService))
	userGroup.POST("/confirm", handlers.Confirm(services.AuthService))
	userGroup.POST("/resend-confirm", handlers.ResendConfirmEmail(services.AuthService))
	userGroup.POST("/login", handlers.Login(services.AuthService))
	userGroup.POST("/reset-password/init", handlers.InitResetPassword(services.AuthService))
	userGroup.POST("/reset-password/confirm", handlers.ConfirmResetPassword(services.AuthService))
	userGroup.POST("/refresh-token", handlers.RefreshToken(services.AuthService))
	//userGroup.PUT("/update-user", handlers.UpdateUser(services.AuthService))
	userGroup.GET("/", handlers.GetAllUsersPag(services.AuthService))
	userGroup.DELETE("/", handlers.DeleteUser(services.AuthService))
	userGroup.POST("/", handlers.AddUser(services.AuthService))
	userGroup.GET("/get-user-by-email/:prefix", handlers.GetUserByEmail(services.AuthService))
	userGroup.GET("/get-user-by-id/:prefix", handlers.GetUserById(services.AuthService))
	userGroup.GET("/get-user/:prefix", handlers.GetUserByExternalId(services.AuthService))

	// Protected routes: Endpoints that require a valid JWT token.
	protected := r.Group("/")
	protected.Use(AuthMiddleware())
	{
		// Protected user endpoints.
		userProtectedGroup := protected.Group("/user")
		{
			// The update endpoint ignores any client-provided external ID.
			userProtectedGroup.PUT("/update-user", handlers.UpdateUser(services.AuthService))
		}
	}
	// fallback
	r.NoRoute(handlers.NoRoute)
}
