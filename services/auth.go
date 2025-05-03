package services

import (
	"coachify-account-api/core"
	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
	"coachify-account-api/models/mapping"
	"coachify-account-api/models/masks"
	"coachify-account-api/oauth2"
	"coachify-account-api/pkg/identifier"
	"coachify-account-api/pkg/invitation"
	"coachify-account-api/pkg/notification"
	"context"
	"fmt"
	"log"

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
	ConfirmResetPassword(ctx *gin.Context, request *api.ConfirmResetPasswordRequest) *models.ApiError
	GetUserByExternalId(ctx context.Context, userId string) (*api.ApiUserResponse, *models.ApiError)
	RefreshToken(ctx context.Context, username string, oldRefreshToken string) (string, *models.ApiError)
	UpdateUser(ctx context.Context, req api.RequestUpdateUser) (*api.ApiUser, *models.ApiError)
	GetAllUsersPag(api.SearchUser) ([]*api.ApiUserResponse, int, *models.ApiError)
	DeleteUser(ctx *gin.Context, id string) *models.ApiError
	AddUser(ctx *gin.Context, req *api.CreateUserRequest) (*api.RegisterResponse, *models.ApiError)
	GetOAuth2LoginURL(providerType string, state string) string
	HandleOAuthLogin(ctx context.Context, providerType string, oauth db.GoogleLoginRequest) (*api.OAuthResponse, *models.ApiError)
}

type AuthServiceImpl struct {
	userRepository     repositories.UserRepository
	passwordChecker    core.PasswordChecker
	activationManager  core.ActivationManager
	middleware         *jwt.GinJWTMiddleware
	identifier         *identifier.IdentifierClient
	invitation         *invitation.InvitationClient
	notificationClient *notification.NotificationClient
	providers          map[oauth2.ProviderType]oauth2.Provider
}

func NewAuthService(
	userRepository *repositories.UserRepository,
	pwChecker core.PasswordChecker,
	middleware *jwt.GinJWTMiddleware,
	activationManager core.ActivationManager,
	identifier *identifier.IdentifierClient,
	notificationClient *notification.NotificationClient,
	invitation *invitation.InvitationClient,
	providers map[oauth2.ProviderType]oauth2.Provider,

) *AuthServiceImpl {
	return &AuthServiceImpl{
		passwordChecker:    pwChecker,
		userRepository:     *userRepository,
		middleware:         middleware,
		activationManager:  activationManager,
		identifier:         identifier,
		invitation:         invitation,
		notificationClient: notificationClient,
		providers:          providers,
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
	log.Printf("IBL: user is %v", user == nil)
	oauth.Profile.ProviderType = providerType
	if user != nil {
		// Existing user: link provider details or log them in
		return s.linkExistingUser(ctx, user, oauth)
	}

	// No user exists, create a new one using the OAuth user info
	return s.createNewOAuthUser(ctx, oauth)
}
func (s *AuthServiceImpl) createNewOAuthUser(ctx context.Context, oauthUser db.GoogleLoginRequest) (*api.OAuthResponse, *models.ApiError) {
	id, apiErr := s.identifier.GenerateId(ctx, "user")
	if apiErr != nil {
		return nil, apiErr
	}

	newUser := mapping.ToDbUserFromGoogleProfile(oauthUser, id.Code)

	// Generate tokens
	accessToken, refreshToken, apiErr := s.generateTokens(newUser)
	if apiErr != nil {
		return nil, apiErr
	}
	newUser.Token = &accessToken
	newUser.UserRefreshToken = &refreshToken

	if newUser.UserRole == "coach" {
		newUser.UserStatus = db.ComReg1

	} else {
		newUser.UserStatus = db.Active
		log.Printf("Invitation Id and email , %s , %s", newUser.ExternalID, newUser.UserEmail)
		_, err := s.invitation.AcceptInvitation(ctx, newUser.ExternalID, newUser.UserEmail)
		if err != nil {
			return nil, &models.ApiError{
				Code:  http.StatusInternalServerError,
				Error: models.ErrInvitationNotAccepted,
			}
		}
	}
	newUser.UserStatus = db.ComReg1
	// Create User
	_, err := s.userRepository.CreateUser(ctx, newUser)
	if err != nil {
		return nil, err
	}
	// Send confirmation email
	dynamicData := map[string]string{
		"message":  "Thank you for registering!",
		"username": newUser.UserFirstname + " " + newUser.UserLastname,
		"subject":  "Thank you for Your Registration",
	}
	notificationData := notification.Request{
		To:          newUser.UserEmail,
		DynamicData: dynamicData,
	}

	if _, err := s.notificationClient.Send(ctx, notificationData); err != nil {
		return nil, err
	}

	return &api.OAuthResponse{
		User: mapping.ToApiUser(newUser),
	}, nil
}

func (s *AuthServiceImpl) linkExistingUser(ctx context.Context, user *db.User, oauthUser db.GoogleLoginRequest) (*api.OAuthResponse, *models.ApiError) {
	// Generate new access and refresh tokens for the user.
	log.Printf("IBL: linkExistingUser")
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
		// User already exists, return an appropriate error
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

	inserted, err := s.userRepository.CreateUser(ctx, dbUser)
	if err != nil {

		return nil, err
	}

	// Prepare dynamic data for the email
	dynamicData := map[string]string{
		"message":  "Thank you for registering! Here is your confirmation code: " + dbUser.UserConfirmCode.Code,
		"username": dbUser.UserFirstname + " " + dbUser.UserLastname,
		"subject":  "Confirmation of Your Registration",
	}

	// Create the notification request
	data := notification.Request{
		To:          dbUser.UserEmail, // Recipient's email
		DynamicData: dynamicData,      // Dynamic content
	}

	// Send the notification email
	res, er := s.notificationClient.Send(ctx, data)
	log.Printf("%v", data)
	if er != nil {
		// Log error if sending fails
		fmt.Printf("Failed to send email: %v\n", er.Error)

	} else {
		// Log success response
		fmt.Println("Email sent successfully!", res)
	}

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
		return err
	}

	fmt.Printf("Code: %s\n", u.UserConfirmCode.Code)

	if u.UserConfirmCode == nil || u.UserConfirmCode.ExpirationDate.Before(time.Now()) {
		confirmCode := &db.UserConfirmCode{
			Code:           s.activationManager.GenerateCode(),
			ExpirationDate: time.Now().Add(24 * time.Hour),
		}

		u.UserConfirmCode = confirmCode
		u.UserUpdatedAt = time.Now()
		err := s.userRepository.UpdateConfirmationCode(ctx, u)
		if err != nil {
			return err
		}

		dynamicData := map[string]string{
			"message": "Thank you for registering! Here is your confirmation code: " + u.UserConfirmCode.Code,
			"subject": "Confirmation of Your Registration",
		}

		// Create the notification request
		data := notification.Request{
			To:          u.UserEmail, // Recipient's email
			DynamicData: dynamicData, // Dynamic content
		}

		// Send the notification email
		res, er := s.notificationClient.Send(ctx, data)
		log.Printf("%v", data)
		if er != nil {
			// Log error if sending fails
			fmt.Printf("Failed to send email: %v\n", er.Error)
		}

		// Log success response
		fmt.Println("Email sent successfully!", res)
		return &models.ApiError{
			Code:  http.StatusUnauthorized,
			Error: models.ErrInvalidConfirmationCode,
		}
	}

	if req.ConfirmCode != u.UserConfirmCode.Code {
		return &models.ApiError{
			Code:  http.StatusUnauthorized,
			Error: models.ErrInvalidConfirmationCode,
		}
	}
	if u.UserRole == "coach" {
		u.UserStatus = db.ComReg1

	} else {
		u.UserStatus = db.Active
		log.Printf("Invitation Id and email , %s , %s", u.UserExternalID, u.UserEmail)
		_, err := s.invitation.AcceptInvitation(ctx, u.UserExternalID, u.UserEmail)
		if err != nil {
			return err
		}
	}

	u.UserVerificationStatus = true
	err = s.userRepository.UpdateConfirmationCode(ctx, u)
	if err != nil {
		return err
	}

	dynamicData := map[string]string{
		"message": "Welcome to our platform. We are excited to have you on board!",
		// "username": u.UserFirstname + " " + u.UserLastname,
		"subject": "Welcome to Our Service!",
	}

	// Create the notification request
	data := notification.Request{
		To:          u.UserEmail, // Recipient's email
		DynamicData: dynamicData, // Dynamic content
	}

	// Send the notification email
	res, er := s.notificationClient.Send(ctx, data)
	log.Printf("%v", data)
	if er != nil {
		// Log error if sending fails
		fmt.Printf("Failed to send email: %v\n", er.Error)
	}

	// Log success response
	fmt.Println("Email sent successfully!", res)

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

	dynamicData := map[string]string{
		"message": "Thank you for registering! Here is your confirmation code: " + u.UserConfirmCode.Code,
		//"username": u.UserFirstname + " " + u.UserLastname,
		"subject": "Confirmation of Your Registration",
	}

	// Create the notification request
	data := notification.Request{
		To:          u.UserEmail, // Recipient's email
		DynamicData: dynamicData, // Dynamic content
	}

	// Send the notification email
	res, er := s.notificationClient.Send(ctx, data)
	log.Printf("%v", data)
	if er != nil {
		// Log error if sending fails
		fmt.Printf("Failed to send email: %v\n", er.Error)

	}

	// Log success response
	fmt.Println("Email sent successfully!", res)

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

	dynamicData := map[string]string{
		"message": "Please use the following code to reset your password: " + user.UserResetPasswordCode.Code + " Best regards,The Support Team.",
		//"username": user.UserFirstname + " " + user.UserLastname,
		"subject": "Password Reset Request",
	}

	// Create the notification request
	data := notification.Request{
		To:          user.UserEmail, // Recipient's email
		DynamicData: dynamicData,    // Dynamic content
	}
	// Send the notification email
	res, er := s.notificationClient.Send(ctx, data, "forgotpassword")
	log.Printf("%v", data)
	if er != nil {
		// Log error if sending fails
		fmt.Printf("Failed to send email: %v\n", er.Error)

	}

	// Log success response
	fmt.Println("Email sent successfully!", res)

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
	fmt.Printf("Code: %s\n", user.UserResetPasswordCode.Code)
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

	dynamicData := map[string]string{
		"message": "Your password has been successfully reset.",
		//"username": user.UserFirstname + " " + user.UserLastname,
		"subject": "Password Reset Confirmation",
	}

	// Create the notification request
	data := notification.Request{
		To:          user.UserEmail, // Recipient's email
		DynamicData: dynamicData,    // Dynamic content
	}

	// Send the notification email
	res, er := s.notificationClient.Send(ctx, data)
	log.Printf("%v", data)
	if er != nil {
		// Log error if sending fails
		fmt.Printf("Failed to send email: %v\n", er.Error)

	}

	// Log success response
	fmt.Println("Email sent successfully!", res)

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
	fmt.Printf("UserRefreshToken : %s  , oldRefreshToken : %s \n", *user.RefreshToken, oldRefreshToken)
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
		log.Printf("UpdateUser: error applying masks to user - %v", err)
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
		log.Printf("UpdateUser: error updating user in database - %v", err)
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
	dynamicData := map[string]string{
		"message":  "Thank you for registering! Here is your confirmation code: " + dbUser.UserConfirmCode.Code,
		"username": dbUser.UserFirstname + " " + dbUser.UserLastname,
		"subject":  "Confirmation of Your Registration",
	}

	// Create the notification request
	data := notification.Request{
		To:          dbUser.UserEmail, // Recipient's email
		DynamicData: dynamicData,      // Dynamic content
	}

	// Send the notification email
	res, er := s.notificationClient.Send(ctx, data)
	log.Printf("%v", data)
	if er != nil {
		// Log error if sending fails
		fmt.Printf("Failed to send email: %v\n", er.Error)

	}

	// Log success response
	fmt.Println("Email sent successfully!", res)

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
