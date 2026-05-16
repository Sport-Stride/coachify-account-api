package services

import (
	"coachify-account-api/core"
	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
	"coachify-account-api/models/mapping"
	"coachify-account-api/models/masks"
	"coachify-account-api/oauth2"
	"coachify-account-api/pkg/chat"
	"coachify-account-api/pkg/identifier"
	"coachify-account-api/pkg/invitation"
	"coachify-account-api/pkg/notification"
	"coachify-account-api/pkg/payments"
	"context"
	"fmt"
	"strings"

	jwt "github.com/appleboy/gin-jwt/v2"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"coachify-account-api/repositories"
	"coachify-account-api/utils"
	"net/http"
	"time"
)

type AuthService interface {
	GetUserById(ctx context.Context, userId string) (*api.ApiUser, *models.ApiError)
	GetUserByEmail(ctx context.Context, userEmail string) (*api.ApiUser, *models.ApiError)
	CheckEmail(ctx context.Context, userEmail string) (*api.ApiUser, *models.ApiError)
	Register(ctx context.Context, req *api.CreateUserRequest) (*api.RegisterResponse, *models.ApiError)
	Confirm(ctx context.Context, req *api.ConfirmUserRequest) *models.ApiError
	ResendConfirmEmail(ctx context.Context, email string) *models.ApiError
	TryToConnect(ctx context.Context, request api.LoginRequest) (*api.LoginResponse, *models.ApiError)
	InitResetPassword(ctx context.Context, request *api.ResetPasswordRequest) *models.ApiError
	VerifyResetPasswordCode(ctx context.Context, request *api.VerifyResetPasswordCodeRequest) *models.ApiError
	ConfirmResetPassword(ctx *gin.Context, request *api.ConfirmResetPasswordRequest) *models.ApiError
	GetUserByExternalId(ctx context.Context, userId string) (*api.ApiUserResponse, *models.ApiError)
	RefreshToken(ctx context.Context, username string, oldRefreshToken string) (string, *models.ApiError)
	UpdateUser(ctx context.Context, req api.RequestUpdateUser) (*api.ApiUser, *models.ApiError)
	GetAllUsersPag(api.SearchUser) ([]*api.ApiUserResponse, int, *models.ApiError)
	DeleteUser(ctx *gin.Context, id string) *models.ApiError
	AddUser(ctx *gin.Context, req *api.CreateUserRequest) (*api.RegisterResponse, *models.ApiError)
	GetOAuth2LoginURL(providerType string, state string) string
	HandleOAuthLogin(ctx context.Context, providerType string, oauth db.GoogleLoginRequest) (*api.OAuthResponse, *models.ApiError)
	RegisterViaLink(ctx context.Context, token string, req *api.CreateUserRequest) (*api.RegisterResponse, *models.ApiError)
	SubmitMatriculeFiscale(ctx context.Context, externalID string, matricule string) *models.ApiError
	WithdrawTaxRegistration(ctx context.Context, externalID string) *models.ApiError
	GetMatriculeFiscaleApplications(ctx context.Context, status string, page, limit int) ([]api.MatriculeFiscaleApplication, int, *models.ApiError)
	ApproveMatriculeFiscale(ctx context.Context, targetExternalID string, adminExternalID string) *models.ApiError
	RejectMatriculeFiscale(ctx context.Context, targetExternalID string, adminExternalID string) *models.ApiError
}

type AuthServiceImpl struct {
	userRepository       repositories.UserRepository
	coachService         CoachService
	passwordChecker      core.PasswordChecker
	activationManager    core.ActivationManager
	middleware           *jwt.GinJWTMiddleware
	identifier           *identifier.IdentifierClient
	invitation           *invitation.InvitationClient
	notificationClient   *notification.NotificationClient
	providers            map[oauth2.ProviderType]oauth2.Provider
	payment              *payments.PaymentClient
	registrationLinkRepo *repositories.RegistrationLinkRepository
	chatClient           *chat.ChatClient
}

func NewAuthService(
	userRepository *repositories.UserRepository,
	coachService CoachService,
	pwChecker core.PasswordChecker,
	middleware *jwt.GinJWTMiddleware,
	activationManager core.ActivationManager,
	identifier *identifier.IdentifierClient,
	notificationClient *notification.NotificationClient,
	invitation *invitation.InvitationClient,
	providers map[oauth2.ProviderType]oauth2.Provider,
	payment *payments.PaymentClient,
	registrationLinkRepo *repositories.RegistrationLinkRepository,
	chatClient *chat.ChatClient,
) *AuthServiceImpl {
	return &AuthServiceImpl{
		passwordChecker:      pwChecker,
		userRepository:       *userRepository,
		coachService:         coachService,
		middleware:           middleware,
		activationManager:    activationManager,
		identifier:           identifier,
		invitation:           invitation,
		notificationClient:   notificationClient,
		providers:            providers,
		payment:              payment,
		registrationLinkRepo: registrationLinkRepo,
		chatClient:           chatClient,
	}
}

// Common methods

// TokenData contains all tokens needed for authentication
type TokenData struct {
	AccessToken  string
	RefreshToken string
	IsNewAccess  bool
	IsNewRefresh bool
}

// ValidateAndRefreshTokens checks and refreshes tokens as needed
func (s *AuthServiceImpl) ValidateAndRefreshTokens(user *db.User) (*TokenData, *models.ApiError) {
	result := &TokenData{
		IsNewAccess:  false,
		IsNewRefresh: false,
	}

	refreshTokenData := mapping.ToRefreshToken(user)
	accessTokenExpired := true

	// Check if the existing access token is valid
	if user.Token != nil {
		expired, err := utils.IsTokenExpired(*user.Token)
		if err == nil && !expired {
			result.AccessToken = *user.Token
			accessTokenExpired = false
			claims, _ := utils.GetTokenClaims(*user.Token)
			if claims != nil {
				utils.Logger.Info("Valid token for user",
					zap.String("userId", fmt.Sprintf("%v", user.ID)),
					zap.String("userId in token", fmt.Sprintf("%v", claims["id"])),
					zap.String("email", user.UserEmail),
					zap.String("tokenType", fmt.Sprintf("%v", claims["token_type"])))
			}
		}
	}

	// If the access token is expired or missing, attempt to refresh it
	if accessTokenExpired {
		refreshTokenValid := false

		// Check if the refresh token exists and is valid
		if user.UserRefreshToken != nil {
			rExpired, err := utils.IsTokenExpired(*user.UserRefreshToken)
			if err == nil && !rExpired {
				refreshTokenValid = true
				result.RefreshToken = *user.UserRefreshToken
			}
		}

		if refreshTokenValid {
			// Refresh token is valid: generate a new access token
			newAccessToken, err := utils.CreateToken(utils.CreateTokenParams{
				User: refreshTokenData,
				Type: "access",
			})
			if err != nil {
				return nil, err
			}
			result.AccessToken = newAccessToken
			result.IsNewAccess = true
		} else {
			// Both tokens are invalid or missing: generate both new tokens
			newAccessToken, err := utils.CreateToken(utils.CreateTokenParams{
				User: refreshTokenData,
				Type: "access",
			})
			if err != nil {
				return nil, err
			}
			result.AccessToken = newAccessToken
			result.IsNewAccess = true

			newRefreshToken, err := utils.CreateToken(utils.CreateTokenParams{
				User: refreshTokenData,
				Type: "refresh",
			})
			if err != nil {
				return nil, err
			}
			result.RefreshToken = newRefreshToken
			result.IsNewRefresh = true
		}
	} else if user.UserRefreshToken != nil {
		result.RefreshToken = *user.UserRefreshToken
	}

	return result, nil
}

func (s *AuthServiceImpl) generateTokens(user *db.User) (string, string, *models.ApiError) {
	// Prepare user data for token generation
	refreshTokenData := mapping.ToRefreshToken(user)

	// Generate access token
	accessToken, err := utils.CreateToken(utils.CreateTokenParams{
		User: refreshTokenData,
		Type: "access",
	})
	if err != nil {
		return "", "", err
	}

	// Generate refresh token
	refreshToken, err := utils.CreateToken(utils.CreateTokenParams{
		User: refreshTokenData,
		Type: "refresh",
	})
	if err != nil {
		return "", "", err
	}

	return accessToken, refreshToken, nil
}

func (s *AuthServiceImpl) generateAndSaveTokens(ctx context.Context, user *db.User) (*api.OAuthResponse, *models.ApiError) {

	accessToken, refreshToken, apiErr := s.generateTokens(user)
	if apiErr != nil {
		return nil, apiErr
	}

	user.Token = &accessToken
	user.UserRefreshToken = &refreshToken

	if err := s.userRepository.UpdateUserProviders(ctx, user); err != nil {
		return nil, err
	}

	return &api.OAuthResponse{
		User: mapping.ToApiUser(user),
	}, nil
}

func (s AuthServiceImpl) GetUserById(ctx context.Context, userId string) (*api.ApiUser, *models.ApiError) {
	user, err := s.userRepository.GetUserById(ctx, userId)
	if err != nil {

		return nil, err
	}

	// Convert the user to the API model
	apiUser := mapping.ToApiUser(user)

	// Return the converted API user and no error
	return &apiUser, nil
}

func (s AuthServiceImpl) CheckEmail(ctx context.Context, userEmail string) (*api.ApiUser, *models.ApiError) {
	user, err := s.userRepository.CheckEmail(ctx, userEmail)
	if err != nil {

		return nil, err
	}

	// Convert the user to the API model
	apiUser := mapping.ToApiUser(user)

	// Return the converted API user and no error
	return &apiUser, nil
}
func (s AuthServiceImpl) GetUserByEmail(ctx context.Context, userEmail string) (*api.ApiUser, *models.ApiError) {
	user, err := s.userRepository.GetByEmail(ctx, userEmail)
	if err != nil {

		return nil, err
	}

	// Convert the user to the API model
	apiUser := mapping.ToApiUser(user)

	// Return the converted API user and no error
	return &apiUser, nil
}

// Oauth signin Service
func (s *AuthServiceImpl) GetOAuth2LoginURL(providerType string, state string) string {
	provider, ok := s.providers[oauth2.ProviderType(providerType)]
	if !ok {
		return ""
	}
	return provider.GetLoginURL(state)
}

// func (s *AuthServiceImpl) HandleOAuthLogin(ctx context.Context, providerType, code string) (*api.OAuthResponse, *models.ApiError) {
// 	provider, ok := s.providers[oauth2.ProviderType(providerType)]
// 	if !ok {
// 		return nil, &models.ApiError{
// 			Code:  http.StatusBadRequest,
// 			Error: models.ErrProviderNotFound,
// 		}
// 	}

// 	// Exchange code for token
// 	token, err := provider.ExchangeCode(ctx, code)
// 	if err != nil {
// 		return nil, err
// 	}

// 	// Fetch user info
// 	oauthUser, err := provider.GetUserInfo(ctx, token)
// 	if err != nil {
// 		return nil, err
// 	}
// 	// Check if provider is already linked to another account
// 	user, err := s.userRepository.GetByEmailCheck(ctx, oauthUser.Email)
// 	if err != nil {
// 		return nil, err
// 	}

// 	if user != nil {
// 		// log in existing user
// 		return s.linkExistingUser(ctx, user, oauthUser)
// 	} else {
// 		// Create new user with OAuth provider
// 		return s.createNewOAuthUser(ctx, oauthUser)
// 	}
// 	// Link provider to existing account
// 	//log.Printf("IBL: OAuth response from Facebook: %+v", oauthUser)

// 	// Link OAuth provider with user account
// 	// user, apiErr := s.LinkOAuthProvider(ctx, *oauthUser)
// 	// if apiErr != nil {
// 	// 	return nil, apiErr
// 	// }

//		// // Return response struct
//		// return &api.OAuthResponse{
//		// 	User: &user.User,
//		// }, nil
//	}
func (s *AuthServiceImpl) HandleOAuthLogin(ctx context.Context, providerType string, oauth db.GoogleLoginRequest) (*api.OAuthResponse, *models.ApiError) {
	provider, ok := s.providers[oauth2.ProviderType(providerType)]
	if !ok {
		return nil, &models.ApiError{
			Code:  http.StatusBadRequest,
			Error: models.ErrProviderNotFound,
		}
	}

	// Fetch user info
	_, err := provider.ValidateToken(ctx, oauth.Account.IdToken)
	if err != nil {
		return nil, err
	}

	// Check if a user with this email already exists
	user, err := s.userRepository.GetByEmailCheck(ctx, oauth.Profile.Email)
	if err != nil {
		return nil, err
	}
	zap.L().Debug("oauth user lookup",
		zap.String("component", "auth"),
		zap.Bool("user_found", user != nil),
	)
	oauth.Profile.ProviderType = providerType
	if user != nil {
		// Existing user: link provider details or log them in
		return s.linkExistingUser(ctx, user, oauth)
	}

	// No user exists, create a new one using the OAuth user info
	return s.createNewOAuthUser(ctx, oauth)
}
func (s *AuthServiceImpl) createNewOAuthUser(ctx context.Context, oauthUser db.GoogleLoginRequest) (*api.OAuthResponse, *models.ApiError) {
	if oauthUser.InvitedEmail != "" && !strings.EqualFold(oauthUser.Profile.Email, oauthUser.InvitedEmail) {
		zap.L().Warn("oauth email mismatch",
			zap.String("component", "auth"),
			zap.String("invited_email", oauthUser.InvitedEmail),
			zap.String("oauth_email", oauthUser.Profile.Email),
		)
		return nil, &models.ApiError{
			Code:  http.StatusForbidden,
			Error: fmt.Errorf("%w: expected %s but got %s", models.ErrOAuthEmailMismatch, oauthUser.InvitedEmail, oauthUser.Profile.Email),
		}
	}

	id, apiErr := s.identifier.GenerateId(ctx, "user")
	if apiErr != nil {
		return nil, apiErr
	}

	newUser := mapping.ToDbUserFromGoogleProfile(oauthUser, id.Code)
	// First successful OAuth sign-in is a real login event.
	newUser.UserLastLogin = time.Now()

	// Determine role and coach association.
	// If a registration link token is present, resolve the coach from it directly
	// rather than checking the invitation service.
	var role, coachId string
	if oauthUser.RegistrationToken != "" {
		link, err := s.registrationLinkRepo.GetByToken(ctx, oauthUser.RegistrationToken)
		if err != nil {
			return nil, &models.ApiError{Code: http.StatusInternalServerError, Error: models.ErrInternalError}
		}
		if link == nil {
			return nil, &models.ApiError{Code: http.StatusNotFound, Error: models.ErrRegistrationLinkNotFound}
		}
		role = "client"
		coachId = link.CoachID
		s.coachService.AddCoachClient(ctx, coachId, newUser.ExternalID)
		if s.chatClient != nil {
			coachUser, coachUserErr := s.userRepository.GetUserByExternalIdUpdate(ctx, coachId)
			var coachToken string
			if coachUserErr == nil && coachUser != nil && coachUser.Token != nil {
				coachToken = *coachUser.Token
			}
			if convErr := s.chatClient.CreateConversation(ctx, coachId, newUser.ExternalID, coachToken); convErr != nil {
				zap.L().Warn("createNewOAuthUser: failed to create coach-client conversation",
					zap.String("component", "auth"),
					zap.Any("error", convErr),
				)
			}
		}
	} else {
		var Rolerr error
		role, coachId, Rolerr = s.setUserRoleByInvitation(ctx, oauthUser.Profile.Email)
		if role == "client" {
			s.coachService.AddCoachClient(ctx, coachId, newUser.ExternalID)
		}
		if Rolerr != nil {
			return nil, &models.ApiError{Code: http.StatusInternalServerError, Error: Rolerr}
		}
	}
	newUser.UserRole = role

	// Generate tokens
	accessToken, refreshToken, apiErr := s.generateTokens(newUser)
	if apiErr != nil {
		return nil, apiErr
	}
	newUser.Token = &accessToken
	newUser.UserRefreshToken = &refreshToken
	zap.L().Debug("subscribing new user to trial", zap.String("component", "auth"), zap.String("role", newUser.UserRole))
	_, er := s.payment.SubscribeWithTrial(ctx, newUser.UserRole, refreshToken)
	if er != nil {
		zap.L().Error("trial subscription failed",
			zap.String("component", "auth"),
			zap.Any("error", er),
		)

	}
	if newUser.UserRole == "coach" {
		newUser.UserStatus = db.ComReg1
	} else {
		newUser.UserStatus = db.Active
		// Only call AcceptInvitation for the invitation flow, not for the registration link flow
		if oauthUser.RegistrationToken == "" {
			zap.L().Debug("accepting client invitation",
				zap.String("component", "auth"),
				zap.String("external_id", newUser.ExternalID),
			)
			_, err := s.invitation.AcceptInvitation(ctx, newUser.ExternalID, newUser.UserEmail, *newUser.UserRefreshToken)
			if err != nil {
				return nil, &models.ApiError{
					Code:  http.StatusInternalServerError,
					Error: models.ErrInvitationNotAccepted,
				}
			}
		}
	}

	// Create User
	_, err := s.userRepository.CreateUser(ctx, newUser)
	if err != nil {
		return nil, err
	}
	// Get the user from the database using email
	user, err := s.userRepository.GetByEmail(ctx, newUser.UserEmail)
	if err != nil {
		return nil, err
	}
	if err := s.notificationClient.SendWelcomeEmail(ctx, user); err != nil {
		zap.L().Warn("failed to send welcome email",
			zap.String("component", "auth"),
			zap.Error(err),
		)
	}
	return &api.OAuthResponse{
		User: mapping.ToApiUser(newUser),
	}, nil
}

func (s *AuthServiceImpl) linkExistingUser(ctx context.Context, user *db.User, oauthUser db.GoogleLoginRequest) (*api.OAuthResponse, *models.ApiError) {
	// Use the refactored token validation
	tokenData, err := s.ValidateAndRefreshTokens(user)
	if err != nil {
		return nil, err
	}
	// Update user with new tokens if needed
	if tokenData.IsNewAccess {
		user.Token = &tokenData.AccessToken
	}

	if tokenData.IsNewRefresh {
		user.UserRefreshToken = &tokenData.RefreshToken
	}

	// Check if the provider already exists in the user's providers map.
	// Provider exists: refresh the provider token.
	// providerInstance, ok := s.providers[oauth2.ProviderType(oauthUser.ProviderType)]
	// if !ok {
	// 	return nil, &models.ApiError{
	// 		Code:  http.StatusBadRequest,
	// 		Error: models.ErrProviderNotFound,
	// 	}
	// }

	// refreshedDetails, err := providerInstance.RefreshToken(ctx, providerData.RefreshToken)
	// if err != nil {
	// 	return nil, err
	// }
	// Update the provider details in the map with the refreshed token.
	//user.Providers[oauthUser.ProviderType] = *refreshedDetails
	// Provider does not exist in the user's providers map.
	// Ensure the map is initialized.
	if user.Providers == nil {
		user.Providers = make(map[string]db.OAuthProviderDetails)
	}
	expiryTime := time.Unix(oauthUser.Account.ExpiresAt, 0)
	// Create new provider details from the oauthUser information.
	newProviderDetails := db.OAuthProviderDetails{
		ProviderID:     oauthUser.Profile.Sub,
		Email:          oauthUser.Profile.Email,
		FirstName:      oauthUser.Profile.GivenName,
		LastName:       oauthUser.Profile.FamilyName,
		ProfilePicture: oauthUser.Profile.Picture,
		AccessToken:    oauthUser.Account.AccessToken,  // Use the provided access token
		RefreshToken:   oauthUser.Account.RefreshToken, // Use the provided refresh token
		Expiry:         expiryTime,                     // Converted expiry time
	}
	user.Providers[oauthUser.Profile.ProviderType] = newProviderDetails

	// Update the user's last login time.
	user.UserLastLogin = time.Now()

	// Update the provider information in the database.
	if err := s.userRepository.UpdateUserProviders(ctx, user); err != nil {
		return nil, err
	}

	return &api.OAuthResponse{
		User: mapping.ToApiUser(user),
	}, nil
}

// Auth signin Service
func (s AuthServiceImpl) Register(ctx context.Context, req *api.CreateUserRequest) (*api.RegisterResponse, *models.ApiError) {

	userExists, errEmail := s.userRepository.EmailExists(ctx, req.Email)

	if errEmail != nil {
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: errEmail,
		}
	}

	if userExists {
		// Check if user is in ToConfirm status, if so, regenerate code and resend
		userToConfirm, err := s.userRepository.GetByEmailToConfirm(ctx, req.Email)
		if err != nil {
			return nil, &models.ApiError{
				Code:  http.StatusInternalServerError,
				Error: err.Error,
			}
		}
		if userToConfirm.UserStatus == db.ToConfirm {
			confirmCode := &db.UserConfirmCode{
				Code:           s.activationManager.GenerateCode(),
				ExpirationDate: time.Now().Add(24 * time.Hour),
			}
			userToConfirm.UserConfirmCode = confirmCode
			userToConfirm.UserUpdatedAt = time.Now()
			err := s.userRepository.UpdateConfirmationCode(ctx, userToConfirm)
			if err != nil {
				return nil, &models.ApiError{
					Code:  http.StatusInternalServerError,
					Error: err.Error,
				}
			}
			// Send confirmation email
			// Get the full user for email sending
			userFull, err := s.userRepository.GetByEmail(ctx, req.Email)
			if err == nil {
				if err := s.notificationClient.SendConfirmationEmail(ctx, userFull); err != nil {
					zap.L().Warn("failed to send confirmation email",
						zap.String("component", "auth"),
						zap.Error(err),
					)
				}
			}
			// Return a response indicating code was resent (tokens empty)
			userResp := mapping.ToUserResponse(userFull)
			return &api.RegisterResponse{
				User:        mapping.ToApiUserResponse(&userResp),
				AuthToken:   "",
				RereshToken: "",
			}, nil
		}
		// User exists but not in ToConfirm, return error
		return nil, &models.ApiError{
			Code:  http.StatusConflict,
			Error: models.ErrUserAlreadyExists,
		}
	}

	// Password validation
	if !core.ValidatePassword(req.Password) {
		return nil, &models.ApiError{
			Code:  http.StatusBadRequest,
			Error: models.ErrInvalidPassword,
		}
	}

	encrypted, err := s.passwordChecker.HashPassword(req.Password)
	if err != nil {
		return nil, err
	}

	id, apiErr := s.identifier.GenerateId(ctx, "user")
	if apiErr != nil {
		return nil, apiErr
	}

	confirmCode := &db.UserConfirmCode{
		Code:           s.activationManager.GenerateCode(), // Générer un code
		ExpirationDate: time.Now().Add(24 * time.Hour),
	}

	dbUser := mapping.CreateToDbUser(req, encrypted, id.Code, *confirmCode)
	refreshTokenData := mapping.ToRefreshToken(dbUser)
	token, err := utils.CreateToken(utils.CreateTokenParams{
		User: refreshTokenData,
		Type: "access",
	})

	if err != nil {

		return nil, err
	}

	refreshToken, err := utils.CreateToken(utils.CreateTokenParams{
		User: refreshTokenData,
		Type: "refresh",
	})

	if err != nil {
		return nil, err

	}

	dbUser.Token = &token
	dbUser.UserRefreshToken = &refreshToken
	dbUser.UserStatus = db.ToConfirm
	role, coachId, Rolerr := s.setUserRoleByInvitation(ctx, dbUser.UserEmail)

	if role == "client" {
		s.coachService.AddCoachClient(ctx, coachId, dbUser.ExternalID)
	}

	if Rolerr != nil {
		return nil, &models.ApiError{Code: http.StatusInternalServerError, Error: Rolerr}
	}

	dbUser.UserRole = role
	inserted, err := s.userRepository.CreateUser(ctx, dbUser)
	if err != nil {

		return nil, err
	}

	// Send confirmation email
	go func() {

		bgCtx := context.Background()
		er := s.notificationClient.SendMail(bgCtx, dbUser)
		if er != nil {
			zap.L().Warn("failed to send registration email",
				zap.String("component", "auth"),
				zap.Any("error", er),
			)
		}
	}()
	// Send the notification email

	resp := &api.RegisterResponse{
		User:        mapping.ToApiUserResponse(inserted),
		AuthToken:   token, // Inclure le token dans la réponse
		RereshToken: refreshToken,
	}

	if req.Autologin {
		token, err := utils.CreateToken(utils.CreateTokenParams{
			User: refreshTokenData,
			Type: "access",
		})
		if err != nil {
			return nil, err
		}
		resp.AuthToken = token
	}

	return resp, nil
}

func (s AuthServiceImpl) TryToConnect(ctx context.Context, request api.LoginRequest) (*api.LoginResponse, *models.ApiError) {
	user, err := s.userRepository.GetByEmail(ctx, request.Email)

	if err != nil {

		return nil, err
	}

	if user.UserStatus == db.Blocked {
		return nil, &models.ApiError{
			Code:  http.StatusUnauthorized,
			Error: models.ErrUserBlocked,
		}
	}

	if user.UserStatus == db.ToConfirm {
		return nil, &models.ApiError{
			Code:  http.StatusForbidden,
			Error: models.ErrAccountNotConfirmed,
		}
	}

	e := s.passwordChecker.VerifyPassword(request.Password, user.UserPassword)
	if e != nil {
		return nil, e
	}

	user.UserLastLogin = time.Now()
	// Use the refactored token validation
	tokenData, err := s.ValidateAndRefreshTokens(user)
	if err != nil {
		return nil, err
	}

	// Update user with new tokens if needed
	if tokenData.IsNewAccess {
		user.Token = &tokenData.AccessToken
	}

	if tokenData.IsNewRefresh {
		user.UserRefreshToken = &tokenData.RefreshToken
	}

	if request.Autologin {
		user.Autologin = true
	}

	if err := s.userRepository.Update(ctx, request.Email, user); err != nil {
		return nil, err
	}

	// Return the LoginResponse with the token, refreshToken, and apiUser
	return &api.LoginResponse{
		User: mapping.ToApiUser(user), // Dereference the apiUser pointer

	}, nil

}

func (s AuthServiceImpl) Confirm(ctx context.Context, req *api.ConfirmUserRequest) *models.ApiError {
	u, err := s.userRepository.GetByEmailToConfirm(ctx, req.Email)
	if err != nil {
		zap.L().Error("confirm: failed to fetch user",
			zap.String("component", "auth"),
			zap.String("email", req.Email),
			zap.Any("error", err),
		)
		return err
	}

	// Check if the confirmation code is expired or missing
	if u.UserConfirmCode == nil || u.UserConfirmCode.ExpirationDate.Before(time.Now()) {
		confirmCode := &db.UserConfirmCode{
			Code:           s.activationManager.GenerateCode(),
			ExpirationDate: time.Now().Add(24 * time.Hour),
		}

		u.UserConfirmCode = confirmCode
		u.UserUpdatedAt = time.Now()
		err := s.userRepository.UpdateConfirmationCode(ctx, u)
		if err != nil {
			zap.L().Error("confirm: failed to update confirmation code",
				zap.String("component", "auth"),
				zap.Any("error", err),
			)
			return err
		}

		// Get the user from the database using email
		user, err := s.userRepository.GetByEmail(ctx, u.UserEmail)
		if err != nil {
			zap.L().Error("confirm: failed to fetch user by email",
				zap.String("component", "auth"),
				zap.Any("error", err),
			)
			return err
		}
		// Send confirmation email using notification client
		if err := s.notificationClient.SendConfirmationEmail(ctx, user); err != nil {
			zap.L().Warn("confirm: failed to send confirmation email",
				zap.String("component", "auth"),
				zap.Error(err),
			)
		}

		return &models.ApiError{
			Code:  http.StatusUnauthorized,
			Error: models.ErrInvalidConfirmationCode,
		}
	}

	if req.ConfirmCode != u.UserConfirmCode.Code {
		zap.L().Warn("confirm: code mismatch",
			zap.String("component", "auth"),
		)
		return &models.ApiError{
			Code:  http.StatusUnauthorized,
			Error: models.ErrInvalidConfirmationCode,
		}
	}
	s.payment.SubscribeWithTrial(ctx, u.UserRole, u.UserRefreshToken)
	if u.UserRole == "coach" {
		u.UserStatus = db.ComReg1
	} else {
		u.UserStatus = db.Active
		_, err := s.invitation.AcceptInvitation(ctx, u.UserExternalID, u.UserEmail, u.UserRefreshToken)
		if err != nil {
			if err.Code == http.StatusNotFound {
				// No invitation exists — user registered via public link. Create the conversation now.
				zap.L().Debug("confirm: no invitation to accept (registration link flow)",
					zap.String("component", "auth"),
					zap.String("external_id", u.UserExternalID),
				)
				coachID, coachErr := s.coachService.GetCoachIDByClientID(ctx, u.UserExternalID)
				if coachErr != nil {
					zap.L().Warn("confirm: could not resolve coach for conversation creation",
						zap.String("component", "auth"),
						zap.String("external_id", u.UserExternalID),
						zap.Error(coachErr),
					)
				} else if s.chatClient != nil {
					coachUser, coachUserErr := s.userRepository.GetUserByExternalIdUpdate(ctx, coachID)
					var coachToken string
					if coachUserErr == nil && coachUser != nil && coachUser.Token != nil {
						coachToken = *coachUser.Token
					}
					if convErr := s.chatClient.CreateConversation(ctx, coachID, u.UserExternalID, coachToken); convErr != nil {
						zap.L().Warn("confirm: failed to create coach-client conversation",
							zap.String("component", "auth"),
							zap.Any("error", convErr),
						)
					}
				}
			} else {
				zap.L().Error("confirm: failed to accept invitation",
					zap.String("component", "auth"),
					zap.Any("error", err),
				)
				return err
			}
		}
	}

	u.UserVerificationStatus = true
	err = s.userRepository.UpdateConfirmationCode(ctx, u)
	if err != nil {
		zap.L().Error("confirm: failed to finalize verification",
			zap.String("component", "auth"),
			zap.Any("error", err),
		)
		return err
	}

	// After updating confirmation code and user status, send welcome email
	user, err := s.userRepository.GetByEmail(ctx, u.UserEmail)
	if err != nil {
		zap.L().Error("confirm: failed to fetch user for welcome email",
			zap.String("component", "auth"),
			zap.Any("error", err),
		)
		return err
	}
	if err := s.notificationClient.SendWelcomeEmail(ctx, user); err != nil {
		zap.L().Warn("confirm: failed to send welcome email",
			zap.String("component", "auth"),
			zap.Error(err),
		)
	}

	// Map the user object to the API response
	//confirmResponse := mapping.ToConfirmResponse(u)
	return nil
}

func (s AuthServiceImpl) ResendConfirmEmail(ctx context.Context, email string) *models.ApiError {
	u, err := s.userRepository.GetByEmailToConfirm(ctx, email)
	if err != nil {
		return err
	}
	confirmCode := &db.UserConfirmCode{
		Code:           s.activationManager.GenerateCode(),
		ExpirationDate: time.Now().Add(24 * time.Hour),
	}

	u.UserConfirmCode = confirmCode
	err = s.userRepository.UpdateConfirmationCode(ctx, u)
	if err != nil {
		return err
	}

	// Get the user from the database using email
	user, err := s.userRepository.GetByEmail(ctx, email)
	if err != nil {
		return err
	}
	// Send resend confirmation email using notification client
	if err := s.notificationClient.SendResendConfirmationEmail(ctx, user); err != nil {
		zap.L().Warn("failed to send resend confirmation email",
			zap.String("component", "auth"),
			zap.Error(err),
		)
	}

	return nil
}

func (s AuthServiceImpl) InitResetPassword(ctx context.Context, request *api.ResetPasswordRequest) *models.ApiError {
	user, err := s.userRepository.GetByEmailToResetPassword(ctx, request.Email)

	if err != nil {

		return err
	}

	user.UserResetPasswordCode = db.UserResetPasswordCode{
		Code:           s.activationManager.GenerateCode(),
		ExpirationDate: time.Now().Add(24 * time.Hour),
	}
	err = s.userRepository.UpdateResetPasswordCode(ctx, request.Email, &user.UserResetPasswordCode)
	if err != nil {
		return err
	}

	// Get the user from the database using email

	// Send reset password email using notification client
	if err := s.notificationClient.SendResetPasswordEmail(ctx, user); err != nil {
		zap.L().Warn("failed to send reset password email",
			zap.String("component", "auth"),
			zap.Error(err),
		)
	}

	return nil
}

func (s AuthServiceImpl) VerifyResetPasswordCode(ctx context.Context, request *api.VerifyResetPasswordCodeRequest) *models.ApiError {
	user, err := s.userRepository.GetByEmailToResetPassword(ctx, request.Email)
	if err != nil {
		return err
	}

	resetCode := user.UserResetPasswordCode
	if resetCode.Code == "" {
		return &models.ApiError{
			Code:  http.StatusUnauthorized,
			Error: models.ErrInvalidResetPasswordCode,
		}
	}
	if resetCode.ExpirationDate.Before(time.Now()) || resetCode.Code != request.Code {
		return &models.ApiError{
			Code:  http.StatusUnauthorized,
			Error: models.ErrInvalidResetPasswordCode,
		}
	}

	return nil
}

func (s AuthServiceImpl) ConfirmResetPassword(ctx *gin.Context, request *api.ConfirmResetPasswordRequest) *models.ApiError {
	// Password validation
	if !core.ValidatePassword(request.NewPassword) {
		return &models.ApiError{
			Code:  http.StatusBadRequest,
			Error: models.ErrInvalidPassword,
		}
	}
	user, err := s.userRepository.GetByEmailToResetPassword(ctx, request.Email)
	if err != nil {

		return err
	}
	resetPasswordCode := user.UserResetPasswordCode
	if resetPasswordCode.ExpirationDate.Before(time.Now()) || resetPasswordCode.Code != request.Code {
		return &models.ApiError{
			Code:  http.StatusUnauthorized,
			Error: models.ErrInvalidResetPasswordCode,
		}
	}

	encrypted, err := s.passwordChecker.HashPassword(request.NewPassword)
	if err != nil {
		return err
	}

	err = s.userRepository.UpdatePasswordAndClearResetCode(ctx, request.Email, encrypted)
	if err != nil {
		return err
	}

	er := s.notificationClient.SendConfirmResetPasswordEmail(ctx, user)
	if er != nil {
		zap.L().Warn("failed to send confirm reset password email",
			zap.String("component", "auth"),
			zap.Any("error", er),
		)
	}

	return nil
}

func (s AuthServiceImpl) GetUserByExternalId(ctx context.Context, userId string) (*api.ApiUserResponse, *models.ApiError) {
	user, err := s.userRepository.GetUserByExternalId(ctx, userId)
	if err != nil {
		return nil, err
	}
	// Convert the user to the API model
	apiUser := mapping.ToApiUserResponse(user)

	return &apiUser, nil
}

func (s AuthServiceImpl) RefreshToken(ctx context.Context, email string, oldRefreshToken string) (string, *models.ApiError) {
	user, err := s.userRepository.GetRefreshToken(ctx, email)
	if err != nil {
		return "", err
	}
	if *user.RefreshToken != oldRefreshToken {
		return "", &models.ApiError{
			Code:  http.StatusUnauthorized,
			Error: models.ErrInvalidRefreshToken,
		}
	}

	accessToken, e := utils.CreateToken(utils.CreateTokenParams{
		User: *user,
		Type: "access",
	})
	if e != nil {
		return "", err
	}
	//user.UserRefreshToken = &newRefreshToken

	if err := s.userRepository.UpdateToken(ctx, user.ExternalID, accessToken); err != nil {
		return "", err
	}

	return accessToken, nil
}

func (s *AuthServiceImpl) UpdateUser(ctx context.Context, req api.RequestUpdateUser) (*api.ApiUser, *models.ApiError) {
	// Fetch only the necessary fields (e.g., ExternalID) to verify the user exists
	// externalID, err := s.userRepository.GetExternalIDByEmail(ctx, req.User.UserEmail)
	// if err != nil {
	// 	log.Printf("UpdateUser: error fetching user ExternalID - %v", err)
	// 	return nil, err
	// }

	// Apply update masks to the user data
	updateFields, err := masks.UpdateUserMasks(&req)
	if err != nil {
		zap.L().Error("update user: failed to apply masks",
			zap.String("component", "auth"),
			zap.Any("error", err),
		)
		return nil, err
	}

	switch req.User.UserStatus {
	case "Complete-registration-1":
		updateFields["status"] = "Complete-registration-2"
	case "Complete-registration-2":
		updateFields["status"] = "Complete-registration-3"
	case "Complete-registration-3":
		updateFields["status"] = "Active"
	}
	// Update user data in the repository
	updatedUser, err := s.userRepository.UpdateUserByMask(ctx, req.User.ExternalID, updateFields)
	if err != nil {
		zap.L().Error("update user: failed to persist",
			zap.String("component", "auth"),
			zap.Any("error", err),
		)
		return nil, err
	}

	// Convert updated data to API user response format
	dataResp := mapping.ToApiUser(updatedUser)

	return &dataResp, nil
}

func (s *AuthServiceImpl) GetAllUsersPag(searchUser api.SearchUser) ([]*api.ApiUserResponse, int, *models.ApiError) {
	// Log the search query for debugging
	utils.Logger.Info("Search query received", zap.Any("searchUser", searchUser))

	// Convert the API SearchUser object to DB format
	sDB := mapping.SearchUserAPIToDB(searchUser)

	// Call the repository to retrieve users with pagination and filters
	res, count, err := s.userRepository.GetAllUsersPag(context.Background(), &sDB)
	if err != nil {
		return nil, 0, err
	}

	// Convert results from DB format to ApiUser format
	results := make([]*api.ApiUserResponse, 0, len(res))
	for _, v := range res {
		result := mapping.ToApiUserResponse(v)
		results = append(results, &result)
	}

	return results, count, nil
}

func (s *AuthServiceImpl) DeleteUser(ctx *gin.Context, id string) *models.ApiError {

	// Delete the user from the repository
	err := s.userRepository.DeleteUser(ctx, id)
	if err != nil {
		return err // Return the error if user deletion fails
	}

	return nil // Return nil if everything was successful
}

func (s AuthServiceImpl) AddUser(ctx *gin.Context, req *api.CreateUserRequest) (*api.RegisterResponse, *models.ApiError) {
	now := time.Now()
	// Password validation
	if !core.ValidatePassword(req.Password) {
		return nil, &models.ApiError{
			Code:  http.StatusBadRequest,
			Error: models.ErrInvalidPassword,
		}
	}

	encrypted, err := s.passwordChecker.HashPassword(req.Password)
	if err != nil {
		return nil, err
	}

	id, _ := s.identifier.GenerateId(ctx, "user")

	confirmCode := &db.UserConfirmCode{
		Code:           s.activationManager.GenerateCode(), // Générer un code
		ExpirationDate: time.Now().Add(24 * time.Hour),
	}
	dbUser := mapping.CreateToDbUser(req, encrypted, id.Code, *confirmCode)
	dbUser.UserCreatedAt = now
	dbUser.UserUpdatedAt = now
	refreshTokenData := mapping.ToRefreshToken(dbUser)
	token, err := utils.CreateToken(utils.CreateTokenParams{
		User: refreshTokenData,
		Type: "access",
	})
	if err != nil {

		return nil, err
	}
	refreshToken, err := utils.CreateToken(utils.CreateTokenParams{
		User: refreshTokenData,
		Type: "refresh",
	})
	if err != nil {
		return nil, err

	}

	dbUser.Token = &token
	dbUser.UserRefreshToken = &refreshToken
	dbUser.UserStatus = db.ToConfirm

	inserted, err := s.userRepository.CreateUser(ctx, dbUser)
	if err != nil {

		return nil, err
	}

	// Send the notification email
	er := s.notificationClient.SendConfirmationEmail(ctx, dbUser)
	if er != nil {
		zap.L().Warn("failed to send confirmation email",
			zap.String("component", "auth"),
			zap.Any("error", er),
		)
	}

	resp := &api.RegisterResponse{
		User:        mapping.ToApiUserResponse(inserted),
		AuthToken:   token,
		RereshToken: refreshToken,
	}

	if req.Autologin {
		token, err := utils.CreateToken(utils.CreateTokenParams{
			User: refreshTokenData,
			Type: "access",
		})
		if err != nil {
			return nil, &models.ApiError{
				Code:  http.StatusInternalServerError,
				Error: models.ErrErrorGeneratingJWTToken,
			}
		}
		resp.AuthToken = token

	}

	return resp, nil
}

// setUserRoleByInvitation checks invitation and sets the user role accordingly
func (s *AuthServiceImpl) setUserRoleByInvitation(ctx context.Context, email string) (string, string, error) {
	exists, coach_external_id, err := s.invitation.CheckInvitationByEmail(ctx, email)
	if err != nil {
		return "", "", err.Error
	}
	if exists {
		return "client", coach_external_id, nil
	}
	return "coach", coach_external_id, nil
}

// RegisterViaLink registers a new user as a client through a coach's public registration link.
func (s AuthServiceImpl) RegisterViaLink(ctx context.Context, token string, req *api.CreateUserRequest) (*api.RegisterResponse, *models.ApiError) {
	// Validate the token and resolve the coach
	link, err := s.registrationLinkRepo.GetByToken(ctx, token)
	if err != nil {
		return nil, &models.ApiError{Code: http.StatusInternalServerError, Error: models.ErrInternalError}
	}
	if link == nil {
		return nil, &models.ApiError{Code: http.StatusNotFound, Error: models.ErrRegistrationLinkNotFound}
	}

	// Check if email already exists in the system
	userExists, errEmail := s.userRepository.EmailExists(ctx, req.Email)
	if errEmail != nil {
		return nil, &models.ApiError{Code: http.StatusInternalServerError, Error: errEmail}
	}
	if userExists {
		// Check if user is in ToConfirm status — still block, they should use the original flow
		return nil, &models.ApiError{Code: http.StatusConflict, Error: models.ErrUserExistsUseLogin}
	}

	// Password validation
	if !core.ValidatePassword(req.Password) {
		return nil, &models.ApiError{Code: http.StatusBadRequest, Error: models.ErrInvalidPassword}
	}

	encrypted, hashErr := s.passwordChecker.HashPassword(req.Password)
	if hashErr != nil {
		return nil, hashErr
	}

	id, apiErr := s.identifier.GenerateId(ctx, "user")
	if apiErr != nil {
		return nil, apiErr
	}

	confirmCode := &db.UserConfirmCode{
		Code:           s.activationManager.GenerateCode(),
		ExpirationDate: time.Now().Add(24 * time.Hour),
	}

	dbUser := mapping.CreateToDbUser(req, encrypted, id.Code, *confirmCode)
	refreshTokenData := mapping.ToRefreshToken(dbUser)
	accessToken, tokenErr := utils.CreateToken(utils.CreateTokenParams{
		User: refreshTokenData,
		Type: "access",
	})
	if tokenErr != nil {
		return nil, tokenErr
	}

	refreshToken, tokenErr := utils.CreateToken(utils.CreateTokenParams{
		User: refreshTokenData,
		Type: "refresh",
	})
	if tokenErr != nil {
		return nil, tokenErr
	}

	dbUser.Token = &accessToken
	dbUser.UserRefreshToken = &refreshToken
	dbUser.UserStatus = db.ToConfirm
	dbUser.UserRole = "client"

	inserted, createErr := s.userRepository.CreateUser(ctx, dbUser)
	if createErr != nil {
		return nil, createErr
	}

	// Associate client with coach — same pattern as invitation flow
	s.coachService.AddCoachClient(ctx, link.CoachID, dbUser.ExternalID)

	// Send confirmation email
	go func() {
		bgCtx := context.Background()
		er := s.notificationClient.SendMail(bgCtx, dbUser)
		if er != nil {
			zap.L().Warn("failed to send registration email",
				zap.String("component", "auth"),
				zap.Any("error", er),
			)
		}
	}()

	resp := &api.RegisterResponse{
		User:        mapping.ToApiUserResponse(inserted),
		AuthToken:   accessToken,
		RereshToken: refreshToken,
	}

	return resp, nil
}

// SubmitMatriculeFiscale allows a coach/nutritionist to submit their matricule fiscale for review.
func (s *AuthServiceImpl) SubmitMatriculeFiscale(ctx context.Context, externalID string, matricule string) *models.ApiError {
	matricule = strings.TrimSpace(matricule)
	if matricule == "" {
		return &models.ApiError{Code: http.StatusUnprocessableEntity, Error: models.ErrMatriculeFiscaleEmpty}
	}
	if len(matricule) > 50 {
		return &models.ApiError{Code: http.StatusUnprocessableEntity, Error: models.ErrMatriculeFiscaleTooLong}
	}

	user, apiErr := s.userRepository.GetUserById(ctx, externalID)
	if apiErr != nil {
		return apiErr
	}
	if user.UserRole != "coach" && user.UserRole != "nutritionist" {
		return &models.ApiError{Code: http.StatusForbidden, Error: models.ErrMatriculeFiscaleInvalidRole}
	}
	if user.MatriculeFiscaleStatus == db.MatriculeFiscaleApproved {
		return &models.ApiError{Code: http.StatusConflict, Error: models.ErrMatriculeFiscaleAlreadyApproved}
	}

	return s.userRepository.UpdateMatriculeFiscale(ctx, externalID, matricule)
}

// GetMatriculeFiscaleApplications returns paginated list of matricule fiscale applications for admin review.
func (s *AuthServiceImpl) GetMatriculeFiscaleApplications(ctx context.Context, status string, page, limit int) ([]api.MatriculeFiscaleApplication, int, *models.ApiError) {
	var dbStatus db.MatriculeFiscaleStatus
	if status != "" {
		dbStatus = db.MatriculeFiscaleStatus(status)
		if dbStatus != db.MatriculeFiscaleNone && dbStatus != db.MatriculeFiscalePending &&
			dbStatus != db.MatriculeFiscaleApproved && dbStatus != db.MatriculeFiscaleRejected {
			return nil, 0, &models.ApiError{Code: http.StatusBadRequest, Error: models.ErrMatriculeFiscaleInvalidStatus}
		}
	}

	users, total, apiErr := s.userRepository.FindByMatriculeFiscaleStatus(ctx, dbStatus, page, limit)
	if apiErr != nil {
		return nil, 0, apiErr
	}

	applications := make([]api.MatriculeFiscaleApplication, 0, len(users))
	for _, u := range users {
		applications = append(applications, api.MatriculeFiscaleApplication{
			UserID:           u.ExternalID,
			FullName:         u.UserFirstname + " " + u.UserLastname,
			Role:             u.UserRole,
			Email:            u.UserEmail,
			MatriculeFiscale: u.MatriculeFiscale,
			Status:           string(u.MatriculeFiscaleStatus),
			SubmittedAt:      u.MatriculeFiscaleSubmittedAt,
			ReviewedAt:       u.MatriculeFiscaleReviewedAt,
			ReviewedBy:       u.MatriculeFiscaleReviewedBy,
		})
	}

	return applications, total, nil
}

// ApproveMatriculeFiscale sets the matricule fiscale status to approved.
func (s *AuthServiceImpl) ApproveMatriculeFiscale(ctx context.Context, targetExternalID string, adminExternalID string) *models.ApiError {
	user, apiErr := s.userRepository.GetUserById(ctx, targetExternalID)
	if apiErr != nil {
		return apiErr
	}
	if user.UserRole != "coach" && user.UserRole != "nutritionist" {
		return &models.ApiError{Code: http.StatusBadRequest, Error: models.ErrMatriculeFiscaleInvalidRole}
	}

	apiErr = s.userRepository.UpdateMatriculeFiscaleStatus(ctx, targetExternalID, db.MatriculeFiscaleApproved, adminExternalID)
	if apiErr != nil {
		return apiErr
	}

	// Notify user asynchronously
	go func() {
		bgCtx := context.Background()
		_ = s.notificationClient.SendMatriculeFiscaleNotification(bgCtx, user, "approved")
	}()

	return nil
}

// RejectMatriculeFiscale sets the matricule fiscale status to rejected.
func (s *AuthServiceImpl) RejectMatriculeFiscale(ctx context.Context, targetExternalID string, adminExternalID string) *models.ApiError {
	user, apiErr := s.userRepository.GetUserById(ctx, targetExternalID)
	if apiErr != nil {
		return apiErr
	}
	if user.UserRole != "coach" && user.UserRole != "nutritionist" {
		return &models.ApiError{Code: http.StatusBadRequest, Error: models.ErrMatriculeFiscaleInvalidRole}
	}

	apiErr = s.userRepository.UpdateMatriculeFiscaleStatus(ctx, targetExternalID, db.MatriculeFiscaleRejected, adminExternalID)
	if apiErr != nil {
		return apiErr
	}

	// Notify user asynchronously
	go func() {
		bgCtx := context.Background()
		_ = s.notificationClient.SendMatriculeFiscaleNotification(bgCtx, user, "rejected")
	}()

	return nil
}

// WithdrawTaxRegistration allows a coach/nutritionist to clear their pending or rejected
// tax registration submission, resetting status back to none.
func (s *AuthServiceImpl) WithdrawTaxRegistration(ctx context.Context, externalID string) *models.ApiError {
	return s.userRepository.ClearTaxRegistration(ctx, externalID)
}
