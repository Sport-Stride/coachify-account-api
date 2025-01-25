package services

import (
	"mbv-common-template-api/core"
	"mbv-common-template-api/pkg/identifier"
	"mbv-common-template-api/pkg/notification"
	"mbv-common-template-api/repositories"
	"mbv-common-template-api/utils"

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
) *Services {

	// Initialisez AuthServiceImpl avec les dépendances
	authService := NewAuthService(
		userRepo,
		pwChecker,
		middleware,
		activationManager,
		identfier,
		notification,
	)
	return &Services{
		AuthService: authService,
	}

}
