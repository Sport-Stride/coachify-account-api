package handlers_test

import (
	"errors"
	"net/http"
	"net/http/httptest"

	"github.com/gin-gonic/gin"

	"coachify-account-api/handlers"
	"coachify-account-api/models"
	"coachify-account-api/services/mocks"
)

// apiSvcs groups the mocked services behind the account API routes.
type apiSvcs struct {
	auth  *mocks.AuthService
	coach *mocks.CoachService
	admin *mocks.AdminService
	reg   *mocks.RegistrationLinkService
}

func newSvcs() *apiSvcs {
	return &apiSvcs{
		auth:  &mocks.AuthService{},
		coach: &mocks.CoachService{},
		admin: &mocks.AdminService{},
		reg:   &mocks.RegistrationLinkService{},
	}
}

func apiErr(code int, msg string) *models.ApiError {
	return &models.ApiError{Code: code, Error: errors.New(msg)}
}

// newAPIRouter mirrors router.initializeRoutes. Protected routes sit behind a fake auth
// middleware; an empty userID simulates a context without token claims.
func newAPIRouter(s *apiSvcs, userID, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	r.GET("/health", handlers.GetHealth)
	r.GET("/oauth/:provider/login", handlers.OAuth2Login(s.auth))
	r.POST("/oauth/:provider/auth/me", handlers.OAuth2ServerSideCallback(s.auth))

	u := r.Group("/user")
	u.POST("/signup", handlers.Register(s.auth))
	u.POST("/confirm", handlers.Confirm(s.auth))
	u.POST("/resend-confirm", handlers.ResendConfirmEmail(s.auth))
	u.POST("/login", handlers.Login(s.auth))
	u.POST("/reset-password/init", handlers.InitResetPassword(s.auth))
	u.POST("/reset-password/verify", handlers.VerifyResetPasswordCode(s.auth))
	u.POST("/reset-password/confirm", handlers.ConfirmResetPassword(s.auth))
	u.POST("/refresh-token", handlers.RefreshToken(s.auth))
	u.GET("/get-user-by-email/:prefix", handlers.GetUserByEmail(s.auth))
	u.GET("/check-email/:prefix", handlers.GetUserByEmail(s.auth))
	u.GET("/get-user-by-id/:prefix", handlers.GetUserById(s.auth))
	u.GET("/get-user/:prefix", handlers.GetUserByExternalId(s.auth))

	rl := r.Group("/coach/registration-link")
	rl.GET("/:token", handlers.ValidateRegistrationLink(s.reg))
	rl.POST("/:token/register", handlers.RegisterViaLink(s.auth, s.reg))

	p := r.Group("/")
	p.Use(func(c *gin.Context) {
		if userID != "" {
			c.Set("userID", userID)
			c.Set("userRole", role)
		}
		c.Next()
	})
	pu := p.Group("/user")
	pu.GET("", handlers.GetAllUsersPag(s.auth))
	pu.PUT("/update-user", handlers.UpdateUser(s.auth))
	pu.DELETE("", handlers.DeleteAuthenticatedUser(s.auth))
	pu.PATCH("/matricule-fiscale", handlers.SubmitMatriculeFiscale(s.auth))
	pu.DELETE("/tax-registration", handlers.WithdrawTaxRegistration(s.auth))

	pc := p.Group("/coach")
	pc.GET("/clients", handlers.ListCoachClients(s.coach))
	pc.GET("/clients/summary", handlers.ListCoachClientsSummary(s.coach))
	pc.GET("/client", handlers.GetCoachIDByClientID(s.coach))
	pc.DELETE("/client/:client_id", handlers.DissociateCoachClient(s.coach))
	pc.POST("/registration-link", handlers.GenerateRegistrationLink(s.reg))

	pa := p.Group("/admin")
	pa.GET("/coaches", handlers.ListAdminCoaches(s.admin))
	pa.GET("/users", handlers.ListAdminUsers(s.admin))
	pa.GET("/users/:id", handlers.GetAdminUser(s.admin))
	pa.PATCH("/users/:id/status", handlers.ChangeAdminUserStatus(s.admin))
	pa.POST("/users/:id/resend-confirmation", handlers.ResendAdminUserConfirmation(s.admin))
	pa.POST("/users/:id/password-reset", handlers.SendAdminUserPasswordReset(s.admin))
	pa.POST("/gym-owners", handlers.CreateAdminGymOwner(s.admin))
	pa.GET("/audit", handlers.ListAdminAudit(s.admin))
	pa.GET("/stats/users", handlers.GetAdminUserStats(s.admin))
	pa.GET("/coaches/:id/clients", handlers.ListAdminCoachClients(s.admin))
	pa.GET("/matricule-fiscale", handlers.GetMatriculeFiscaleApplications(s.auth))
	pa.POST("/matricule-fiscale/:userId/approve", handlers.ApproveMatriculeFiscale(s.auth))
	pa.POST("/matricule-fiscale/:userId/reject", handlers.RejectMatriculeFiscale(s.auth))

	r.NoRoute(handlers.NoRoute)
	return r
}

func get(r *gin.Engine, path string) *httptest.ResponseRecorder {
	return do(r, http.MethodGet, path, "")
}
