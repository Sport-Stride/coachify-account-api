package router

import (
	"time"

	"coachify-account-api/handlers"
	"coachify-account-api/services"
	"coachify-account-api/utils"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func InitializeRouter(services *services.Services) *gin.Engine {
	// Set the default gin router
	r := gin.New()
	r.Use(cors.New(cors.Config{
		// Only allow specific origins in production
		AllowOrigins: []string{"*"},
		AllowMethods: []string{"GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"},
		AllowHeaders: []string{
			"Origin",
			"Content-Type",
			"Accept",
			"Authorization",
			"X-Requested-With",
			"X-Auth-Token",
			"x-api-key",
		},
		ExposeHeaders:    []string{"Content-Length", "Authorization"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// r.Use(
	// 	cors.New(cors.Config{
	// 		AllowAllOrigins: true,
	// 		AllowMethods:    []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
	// 		AllowHeaders:    []string{"Origin", "Content-Type", "Content-Length", "X-Requested-With", "Accept", "Referrer", "User-Agent", "X-Auth-Token", "x-api-key"},
	// 	}),
	// )

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
	//oauthGroup.GET("/:provider/callback", handlers.OAuth2Callback(services.AuthService))
	oauthGroup.POST("/:provider/auth/me", handlers.OAuth2ServerSideCallback(services.AuthService))

	//auth endpoints
	userGroup := r.Group("/user")
	userGroup.POST("/signup", handlers.Register(services.AuthService))

	// Public registration link routes (no auth required)
	regLinkGroup := r.Group("/coach/registration-link")
	regLinkGroup.GET("/:token", handlers.ValidateRegistrationLink(services.RegistrationLinkService))
	regLinkGroup.POST("/:token/register", handlers.RegisterViaLink(services.AuthService, services.RegistrationLinkService))
	userGroup.POST("/confirm", handlers.Confirm(services.AuthService))
	userGroup.POST("/resend-confirm", handlers.ResendConfirmEmail(services.AuthService))
	userGroup.POST("/login", handlers.Login(services.AuthService))
	userGroup.POST("/reset-password/init", handlers.InitResetPassword(services.AuthService))
	userGroup.POST("/reset-password/verify", handlers.VerifyResetPasswordCode(services.AuthService))
	userGroup.POST("/reset-password/confirm", handlers.ConfirmResetPassword(services.AuthService))
	userGroup.POST("/refresh-token", handlers.RefreshToken(services.AuthService))
	//userGroup.PUT("/update-user", handlers.UpdateUser(services.AuthService))
	userGroup.GET("/", handlers.GetAllUsersPag(services.AuthService))
	userGroup.POST("/", handlers.AddUser(services.AuthService))
	userGroup.GET("/get-user-by-email/:prefix", handlers.GetUserByEmail(services.AuthService))
	userGroup.GET("/check-email/:prefix", handlers.GetUserByEmail(services.AuthService))
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
			// Delete authenticated user account
			userProtectedGroup.DELETE("", handlers.DeleteAuthenticatedUser(services.AuthService))
			// Matricule fiscale submission (coach/nutritionist only)
			userProtectedGroup.PATCH("/matricule-fiscale", handlers.SubmitMatriculeFiscale(services.AuthService))

		}
		coachProtectedGroup := protected.Group("/coach")
		{

			coachProtectedGroup.GET("/clients", handlers.ListCoachClients(services.CoachService))
			coachProtectedGroup.GET("/client", handlers.GetCoachIDByClientID(services.CoachService))
			coachProtectedGroup.DELETE("/client/:client_id", handlers.DissociateCoachClient(services.CoachService))
			coachProtectedGroup.POST("/registration-link", handlers.GenerateRegistrationLink(services.RegistrationLinkService))
		}

		adminGroup := protected.Group("/admin")
		{
			adminGroup.GET("/coaches", handlers.ListAdminCoaches(services.AdminService))
			adminGroup.GET("/coaches/:id/clients", handlers.ListAdminCoachClients(services.AdminService))
			// Matricule fiscale admin review queue
			adminGroup.GET("/matricule-fiscale", handlers.GetMatriculeFiscaleApplications(services.AuthService))
			adminGroup.POST("/matricule-fiscale/:userId/approve", handlers.ApproveMatriculeFiscale(services.AuthService))
			adminGroup.POST("/matricule-fiscale/:userId/reject", handlers.RejectMatriculeFiscale(services.AuthService))
		}
	}

	// fallback
	r.NoRoute(handlers.NoRoute)
}
