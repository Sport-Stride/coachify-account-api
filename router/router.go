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

	// Public gym owner invite lookup (no auth required)
	r.GET("/gym-owner-invites/:token", RateLimit(120, time.Minute, clientIPKey), handlers.LookupGymOwnerInvite(services.GymOwnerInviteService))

	// Public gym join link lookup (no auth required)
	r.GET("/gym-join/:token", RateLimit(120, time.Minute, clientIPKey), handlers.LookupGymJoin(services.GymService))

	// Unified invite links (INVITE_LINKS_V2): the service answers 404 while the flag is off.
	r.GET("/invite-links/:token", RateLimit(120, time.Minute, clientIPKey), handlers.LookupInviteLink(services.InviteLinks))
	r.GET("/invite-links/:token/lookup", RateLimit(120, time.Minute, clientIPKey), handlers.LookupInviteLink(services.InviteLinks))

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
	userGroup.GET("/get-user-by-email/:prefix", handlers.GetUserByEmail(services.AuthService))
	userGroup.GET("/check-email/:prefix", handlers.GetUserByEmail(services.AuthService))
	userGroup.GET("/get-user-by-id/:prefix", handlers.GetUserById(services.AuthService))
	userGroup.GET("/get-user/:prefix", handlers.GetUserByExternalId(services.AuthService))

	// Protected routes: Endpoints that require a valid JWT token.
	protected := r.Group("/")
	protected.Use(AuthMiddlewareWithStatus(NewStatusGate(services.UserStatus, 30*time.Second)))
	{
		// Protected user endpoints.
		userProtectedGroup := protected.Group("/user")
		{
			// The update endpoint ignores any client-provided external ID.
			// Admin-only user listing (handler enforces role)
			userProtectedGroup.GET("", handlers.GetAllUsersPag(services.AuthService))
			userProtectedGroup.PUT("/update-user", handlers.UpdateUser(services.AuthService))
			// Own profile, including the private personal details the lookup routes never return
			userProtectedGroup.GET("/me", handlers.GetMe(services.AuthService))
			// Change own password: 403 on wrong current password; rate limited per user against brute force
			userProtectedGroup.POST("/change-password", RateLimit(5, 15*time.Minute, userOrIPKey), handlers.ChangePassword(services.AuthService))
			// Delete authenticated user account
			userProtectedGroup.DELETE("", handlers.DeleteAuthenticatedUser(services.AuthService))
			// Tax registration (coach/nutritionist only)
			userProtectedGroup.PATCH("/matricule-fiscale", handlers.SubmitMatriculeFiscale(services.AuthService))
			userProtectedGroup.DELETE("/tax-registration", handlers.WithdrawTaxRegistration(services.AuthService))

		}
		coachProtectedGroup := protected.Group("/coach")
		{

			coachProtectedGroup.GET("/clients", handlers.ListCoachClients(services.CoachService))
			coachProtectedGroup.GET("/clients/summary", handlers.ListCoachClientsSummary(services.CoachService))
			coachProtectedGroup.GET("/clients/access", handlers.ListClientAccess(services.EntitlementService))
			coachProtectedGroup.GET("/clients/:client_id/access", handlers.GetClientAccess(services.EntitlementService))
			coachProtectedGroup.PUT("/clients/:client_id/access", handlers.UpdateClientAccess(services.EntitlementService))
			coachProtectedGroup.GET("/client", handlers.GetCoachIDByClientID(services.CoachService))
			coachProtectedGroup.POST("/clients/check", handlers.CheckClientLinks(services.CoachService))
			coachProtectedGroup.DELETE("/client/:client_id", handlers.DissociateCoachClient(services.CoachService))
			coachProtectedGroup.POST("/registration-link", handlers.GenerateRegistrationLink(services.RegistrationLinkService))
			coachProtectedGroup.GET("/team", handlers.GetMyHeadCoach(services.TeamService))
			coachProtectedGroup.GET("/gym-invitations", handlers.ListMyGymInvitations(services.GymService))
			coachProtectedGroup.POST("/gym-invitations/:id/accept", handlers.AcceptGymInvitation(services.GymService))
			coachProtectedGroup.POST("/gym-invitations/:id/decline", handlers.DeclineGymInvitation(services.GymService))
			coachProtectedGroup.POST("/gym-join/:token/accept", RateLimit(30, time.Minute, userOrIPKey), handlers.AcceptGymJoin(services.GymService))
		}

		// Own effective feature access: any authenticated role.
		protected.GET("/entitlements/me", handlers.GetMyEntitlements(services.EntitlementService))

		// Analytics scope: any authenticated role, the service authorizes from the database.
		analyticsGroup := protected.Group("/analytics")
		{
			analyticsGroup.GET("/scope", handlers.GetAnalyticsScope(services.AnalyticsScopeService))
			analyticsGroup.POST("/clients/lookup", handlers.LookupAnalyticsClients(services.AnalyticsScopeService))
		}

		headCoachGroup := protected.Group("/head-coach")
		{
			headCoachGroup.GET("/clients", handlers.ListHeadCoachClients(services.AnalyticsScopeService))
			headCoachGroup.GET("/me", handlers.GetHeadCoachMe(services.TeamService))
			headCoachGroup.GET("/overview", handlers.GetTeamOverview(services.TeamService))
			headCoachGroup.GET("/coach-ids", handlers.GetMyTeamCoachIDs(services.TeamService))
			headCoachGroup.GET("/coaches", handlers.ListMyTeamCoaches(services.TeamService))
			headCoachGroup.GET("/coaches/:id", handlers.GetMyTeamCoach(services.TeamService))
			headCoachGroup.DELETE("/coaches/:id", handlers.RemoveMyTeamCoach(services.TeamService))
			headCoachGroup.GET("/gym", handlers.GetMyGym(services.GymService))
			headCoachGroup.DELETE("/gym", handlers.LeaveMyGym(services.GymService))
		}

		gymOwnerGroup := protected.Group("/gym-owner")
		{
			gymOwnerGroup.GET("/me", handlers.GetGymOwnerMe(services.GymService))
			gymOwnerGroup.GET("/gyms/:gym_id/overview", handlers.GetGymOverview(services.GymService))
			gymOwnerGroup.PATCH("/gyms/:gym_id", handlers.RenameGym(services.GymService))
			gymOwnerGroup.GET("/gyms/:gym_id/head-coaches", handlers.ListGymHeadCoaches(services.GymService))
			gymOwnerGroup.DELETE("/gyms/:gym_id/head-coaches/:id", handlers.RemoveGymHeadCoach(services.GymService))
			gymOwnerGroup.GET("/gyms/:gym_id/coaches", handlers.ListGymCoaches(services.GymService))
			gymOwnerGroup.GET("/gyms/:gym_id/clients", handlers.ListGymClients(services.GymService))
			gymOwnerGroup.POST("/gyms/:gym_id/invitations", handlers.InviteGymHeadCoach(services.GymService))
			gymOwnerGroup.GET("/gyms/:gym_id/invitations", handlers.ListGymInvitations(services.GymService))
			gymOwnerGroup.POST("/gyms/:gym_id/join-links", handlers.CreateGymJoinLink(services.GymService))
			gymOwnerGroup.DELETE("/gyms/:gym_id/invitations/:id", handlers.RevokeGymInvitation(services.GymService))
		}

		// Gym capabilities: the caller's gym context (any role) and owner-only policies and member overrides.
		// Invite links. The :token wildcard of DELETE carries the link id (one wildcard name per path position).
		protected.POST("/invite-links", handlers.CreateInviteLink(services.InviteLinks))
		protected.GET("/invite-links", handlers.ListInviteLinks(services.InviteLinks))
		protected.DELETE("/invite-links/:token", handlers.RevokeInviteLink(services.InviteLinks))
		protected.POST("/invite-links/:token/redeem", RateLimit(30, time.Minute, userOrIPKey), handlers.RedeemInviteLink(services.InviteLinks))
		protected.GET("/me/invites", handlers.ListMyInvites(services.InviteLinks))
		protected.POST("/me/invites/:id/accept", handlers.AcceptMyInvite(services.InviteLinks))
		protected.POST("/me/invites/:id/decline", handlers.DeclineMyInvite(services.InviteLinks))
		protected.GET("/me/gym-context", handlers.GetMyGymContext(services.GymPolicy))
		protected.GET("/gyms/:gym_id/policies/:role", handlers.GetGymRolePolicy(services.GymPolicy))
		protected.PUT("/gyms/:gym_id/policies/:role", handlers.PutGymRolePolicy(services.GymPolicy))
		protected.GET("/gyms/:gym_id/members/:user_id/grants", handlers.GetGymMemberGrants(services.GymPolicy))
		protected.PUT("/gyms/:gym_id/members/:user_id/grants", handlers.PutGymMemberGrants(services.GymPolicy))
		// Flat hierarchy (GYM_FLAT_HIERARCHY): the member list, supervisor changes and coach invitations answer 404 while the flag is off.
		protected.GET("/gyms/:gym_id/members", handlers.ListGymMembers(services.GymMembers))
		// Gym seats (GYM_SEATS_ENABLED): owner only; 404 while the flag is off.
		protected.GET("/gyms/:gym_id/seats", handlers.GetGymSeats(services.Seats))
		protected.PUT("/gyms/:gym_id/members/:user_id/seat", handlers.PutGymMemberSeat(services.Seats))
		protected.PUT("/gyms/:gym_id/members/:user_id/supervisor", handlers.PutGymMemberSupervisor(services.GymMembers))
		protected.POST("/gyms/:gym_id/coach-invitations", handlers.InviteGymCoach(services.GymService))
		protected.POST("/gyms/:gym_id/coach-join-links", handlers.CreateGymCoachJoinLink(services.GymService))
		// Gym client relationships (CLIENT_GYM_RELATIONS): the service answers 404 while the flag is off.
		protected.GET("/gyms/:gym_id/clients", handlers.ListGymRelationClients(services.GymClients))
		protected.POST("/gyms/:gym_id/clients/:client_id/assign", handlers.AssignGymClient(services.GymClients))
		protected.POST("/gyms/:gym_id/clients/:client_id/unassign", handlers.UnassignGymClient(services.GymClients))
		protected.DELETE("/gyms/:gym_id/clients/:client_id", handlers.RemoveGymClient(services.GymClients))
		// Signed direct image upload to Cloudinary, into the caller's own folder
		protected.POST("/uploads/sign", RateLimit(30, time.Minute, userOrIPKey), handlers.SignUpload(services.Uploads))
		protected.GET("/me/gyms", handlers.ListMyGyms(services.GymClients))
		// Client leaves a gym (GYM_DISPATCH_ENABLED); not registered while the flag is off.
		if services.GymLeaver != nil {
			protected.POST("/me/gyms/:gym_id/leave", handlers.LeaveMyGymAsClient(services.GymLeaver))
		}

		adminGroup := protected.Group("/admin")
		{
			adminGroup.GET("/coaches", handlers.ListAdminCoaches(services.AdminService))
			// Entitlements: static routes before the :id ones.
			adminGroup.GET("/coaches/access", handlers.ListCoachAccess(services.EntitlementService))
			adminGroup.GET("/coaches/:id/access", handlers.GetCoachAccess(services.EntitlementService))
			adminGroup.PUT("/coaches/:id/access", handlers.UpdateCoachAccess(services.EntitlementService))
			adminGroup.GET("/entitlements/features", handlers.ListEntitlementFeatures(services.EntitlementService))
			adminGroup.GET("/entitlements/plan-defaults", handlers.ListPlanDefaults(services.EntitlementService))
			adminGroup.PUT("/entitlements/plan-defaults/:plan_id", handlers.UpdatePlanDefault(services.EntitlementService))
			adminGroup.GET("/entitlements/audit", handlers.ListEntitlementAudit(services.EntitlementService))
			adminGroup.GET("/users", handlers.ListAdminUsers(services.AdminService))
			adminGroup.GET("/users/:id", handlers.GetAdminUser(services.AdminService))
			adminGroup.PATCH("/users/:id/status", handlers.ChangeAdminUserStatus(services.AdminService))
			adminGroup.POST("/users/:id/resend-confirmation", handlers.ResendAdminUserConfirmation(services.AdminService))
			adminGroup.POST("/users/:id/password-reset", handlers.SendAdminUserPasswordReset(services.AdminService))
			adminGroup.POST("/gym-owners", handlers.CreateAdminGymOwner(services.AdminService))
			adminGroup.POST("/gym-owner-invites", handlers.CreateGymOwnerInvite(services.GymOwnerInviteService))
			adminGroup.GET("/gym-owner-invites", handlers.ListGymOwnerInvites(services.GymOwnerInviteService))
			adminGroup.DELETE("/gym-owner-invites/:id", handlers.RevokeGymOwnerInvite(services.GymOwnerInviteService))
			adminGroup.POST("/gym-owner-invites/:id/resend", handlers.ResendGymOwnerInvite(services.GymOwnerInviteService))
			adminGroup.GET("/audit", handlers.ListAdminAudit(services.AdminService))
			adminGroup.GET("/stats/users", handlers.GetAdminUserStats(services.AdminService))
			adminGroup.GET("/coaches/:id/clients", handlers.ListAdminCoachClients(services.AdminService))
			adminGroup.PUT("/coaches/:id/head-coach", handlers.AssignCoachToHeadCoach(services.TeamService))
			adminGroup.DELETE("/coaches/:id/head-coach", handlers.UnassignCoach(services.TeamService))
			adminGroup.GET("/head-coaches", handlers.ListHeadCoaches(services.TeamService))
			adminGroup.POST("/head-coaches/:id", handlers.PromoteHeadCoach(services.TeamService))
			adminGroup.DELETE("/head-coaches/:id", handlers.DemoteHeadCoach(services.TeamService))
			adminGroup.GET("/head-coaches/:id/coaches", handlers.ListHeadCoachTeam(services.TeamService))
			adminGroup.GET("/team-audit", handlers.ListTeamAudit(services.TeamService))
			adminGroup.GET("/gyms", handlers.ListAdminGyms(services.GymService))
			adminGroup.POST("/gyms", handlers.CreateAdminGym(services.GymService))
			adminGroup.PATCH("/gyms/:id", handlers.UpdateAdminGym(services.GymService))
			adminGroup.GET("/gyms/:id/head-coaches", handlers.ListAdminGymHeadCoaches(services.GymService))
			adminGroup.PUT("/gyms/:id/head-coaches/:hcid", handlers.AddAdminGymHeadCoach(services.GymService))
			adminGroup.DELETE("/gyms/:id/head-coaches/:hcid", handlers.RemoveAdminGymHeadCoach(services.GymService))
			adminGroup.GET("/gyms/:id/audit", handlers.ListAdminGymAudit(services.GymService))
			// Matricule fiscale admin review queue
			adminGroup.GET("/matricule-fiscale", handlers.GetMatriculeFiscaleApplications(services.AuthService))
			adminGroup.POST("/matricule-fiscale/:userId/approve", handlers.ApproveMatriculeFiscale(services.AuthService))
			adminGroup.POST("/matricule-fiscale/:userId/reject", handlers.RejectMatriculeFiscale(services.AuthService))
		}
	}

	// Service-to-service entitlement endpoints: x-api-key instead of a JWT.
	internal := r.Group("/internal")
	internal.Use(InternalAPIKeyMiddleware())
	{
		internal.GET("/entitlements/:user_id", handlers.GetInternalEntitlements(services.EntitlementService))
		internal.POST("/entitlements/resolve", handlers.ResolveInternalEntitlements(services.EntitlementService))
		internal.POST("/entitlements/:user_id/consume", handlers.ConsumeInternalUsage(services.EntitlementService))
		internal.POST("/entitlements/:user_id/release", handlers.ReleaseInternalUsage(services.EntitlementService))
		internal.POST("/entitlements/:user_id/check-quota", handlers.CheckInternalQuota(services.EntitlementService))
		internal.GET("/users/:id/entitlements", handlers.GetInternalUserEntitlements(services.ContextEntitlements))
		internal.POST("/authz/check",handlers.CheckInternalAuthz(services.GymPolicy))
		internal.POST("/clients/relationships", handlers.InternalClientRelationships(services.GymClients))
		internal.POST("/gym-access", handlers.InternalGymAccess(services.GymClients))
		internal.GET("/gyms/:gym_id", handlers.GetInternalGym(services.GymService))
		// Coaching add-ons (GYM_MEMBERSHIP_PLANS_ENABLED): called by payments-api.
		if services.GymAddons != nil {
			internal.POST("/gyms/:gym_id/coaches/resolve", handlers.ResolveInternalGymCoaches(services.GymAddons))
			internal.POST("/gym-addons/purchased", handlers.InternalGymAddonPurchased(services.GymAddons))
		}
	}

	// fallback
	r.NoRoute(handlers.NoRoute)
}
