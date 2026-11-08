package services

import (
	"context"
	"time"

	"coachify-account-api/core"
	"coachify-account-api/models/db"
	"coachify-account-api/oauth2"
	"coachify-account-api/pkg/chat"
	"coachify-account-api/pkg/identifier"
	"coachify-account-api/pkg/invitation"
	"coachify-account-api/pkg/notification"
	"coachify-account-api/pkg/payments"
	"coachify-account-api/repositories"
	"coachify-account-api/utils"

	jwt "github.com/appleboy/gin-jwt/v2"
)

type Services struct {
	AuthService             AuthService
	CoachService            CoachService
	AdminService            AdminService
	TeamService             TeamService
	GymService              GymService
	AnalyticsScopeService   AnalyticsScopeService
	RegistrationLinkService RegistrationLinkService
	UserStatus              UserStatusLookup
	EntitlementService      EntitlementService
	GymOwnerInviteService   GymOwnerInviteService
}

func InitServices(config utils.AppConfig,
	middleware *jwt.GinJWTMiddleware,
	userRepo *repositories.UserRepository,
	coachRepo *repositories.CoachRepository,
	registrationLinkRepo *repositories.RegistrationLinkRepository,
	pwChecker core.PasswordChecker,
	activationManager core.ActivationManager,
	identfier *identifier.IdentifierClient,
	notification *notification.NotificationClient,
	invitation *invitation.InvitationClient,
	providers map[oauth2.ProviderType]oauth2.Provider,
	payment *payments.PaymentClient,
	chatClient *chat.ChatClient,
	teamRepo *repositories.TeamRepository,
	adminAuditRepo *repositories.AdminAuditRepository,
	gymRepo *repositories.GymRepository,
	entitlementRepo *repositories.EntitlementRepository,
	entitlementAuditRepo *repositories.AdminAuditRepository,
	gymOwnerInviteRepo *repositories.GymOwnerInviteRepository,
) *Services {
	coachService := NewCoachService(
		coachRepo,
		notification,
		identfier,
		activationManager,
		config.BaseURL.URL,
	)
	authService := NewAuthService(
		userRepo,
		coachService,
		pwChecker,
		middleware,
		activationManager,
		identfier,
		notification,
		invitation,
		providers,
		payment,
		registrationLinkRepo,
		chatClient,
		teamRepo,
	)
	// Plans come from payments-api; a nil client (tests) means every user resolves without a plan.
	var planSource EntitlementPlanSource
	if payment != nil {
		planSource = payment
	}
	entitlementService := NewEntitlementService(entitlementRepo, userRepo, coachService, planSource, entitlementAuditRepo)
	authService.WithEntitlements(entitlementService)
	gymOwnerInviteService := NewGymOwnerInviteService(
		gymOwnerInviteRepo,
		userRepo,
		adminAuditRepo,
		notification,
		time.Duration(config.Gym.OwnerInviteTTLHours)*time.Hour,
		config.Gym.ClientAppURL,
	)
	authService.WithGymOwnerInvites(gymOwnerInviteService)
	var gymJoinMailer GymJoinMailer
	if notification != nil {
		gymJoinMailer = notification
	}
	// Owner chats need the chat client; a nil client (tests) disables them.
	var gymChat *GymChatProvisioner
	if chatClient != nil {
		gymChat = NewGymChatProvisioner(NewGymChatStore(gymRepo, teamRepo), chatClient, nil)
	}
	gymService := NewGymService(userRepo, gymRepo, GymSettings{
		InviteTTL:     time.Duration(config.Gym.InviteTTLHours) * time.Hour,
		InvitesPerDay: config.Gym.InvitesPerDay,
		ClientURL:     config.Gym.ClientAppURL,
		Mailer:        gymJoinMailer,
		Chat:          gymChat,
	})
	authService.WithGymJoins(gymService)
	registrationLinkService := NewRegistrationLinkService(
		registrationLinkRepo,
		userRepo,
	).WithEntitlements(entitlementService)

	return &Services{
		AuthService:             authService,
		CoachService:            coachService,
		AdminService:            NewAdminService(userRepo, coachRepo, adminAuditRepo, authService),
		TeamService:             NewTeamService(userRepo, teamRepo, WithGymMembership(gymRepo), WithGymChat(gymChat), WithReadCache(15*time.Second)),
		GymService:              gymService,
		AnalyticsScopeService:   NewAnalyticsScopeService(userRepo, repositories.NewAnalyticsRepository(teamRepo, gymRepo), config.Analytics.ScopeMaxCoaches),
		RegistrationLinkService: registrationLinkService,
		UserStatus:              userRepo,
		EntitlementService:      entitlementService,
		GymOwnerInviteService:   gymOwnerInviteService,
	}
}

// UserStatusLookup returns an account's current status; the auth middleware uses it to
// reject blocked accounts whose JWT is still valid.
type UserStatusLookup interface {
	GetStatusByExternalID(ctx context.Context, externalID string) (db.UserStatus, error)
}
