package services

import (
	"coachify-account-api/core"
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
	RegistrationLinkService RegistrationLinkService
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
	registrationLinkService := NewRegistrationLinkService(
		registrationLinkRepo,
		userRepo,
	)

	return &Services{
		AuthService:             authService,
		CoachService:            coachService,
		AdminService:            NewAdminService(userRepo, coachRepo),
		TeamService:             NewTeamService(userRepo, teamRepo),
		RegistrationLinkService: registrationLinkService,
	}
}
