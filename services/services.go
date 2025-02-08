package services

import (
	"coachify-account-api/core"
	"coachify-account-api/oauth2"
	"coachify-account-api/pkg/identifier"
	"coachify-account-api/pkg/notification"
	"coachify-account-api/repositories"
	"coachify-account-api/utils"

	jwt "github.com/appleboy/gin-jwt/v2"
)

type Services struct {
	AuthService AuthService
}

func InitServices(config utils.AppConfig,
	middleware *jwt.GinJWTMiddleware,
	userRepo repositories.UserRepository,
	pwChecker core.PasswordChecker,
	activationManager core.ActivationManager,
	identfier *identifier.IdentifierClient,
	notification *notification.NotificationClient,
	providers map[oauth2.ProviderType]oauth2.Provider,
) *Services {

	// Initialisez AuthServiceImpl avec les dépendances
	authService := NewAuthService(
		userRepo,
		pwChecker,
		middleware,
		activationManager,
		identfier,
		notification,
		providers,
	)
	return &Services{
		AuthService: authService,
	}

}
