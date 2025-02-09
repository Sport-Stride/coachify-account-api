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
	GetOAuth2LoginURL(providerType string) string
	HandleOAuthLogin(ctx context.Context, providerType, code string) (*api.OAuthResponse, *models.ApiError)
	LinkOAuthProvider(ctx context.Context, oauthUser db.OAuthUser) (*api.LoginResponse, *models.ApiError)
}

type AuthServiceImpl struct {
	userRepository     repositories.UserRepository
	passwordChecker    core.PasswordChecker
	activationManager  core.ActivationManager
	middleware         *jwt.GinJWTMiddleware
	identifier         *identifier.IdentifierClient
	notificationClient *notification.NotificationClient
	providers          map[oauth2.ProviderType]oauth2.Provider
}

func NewAuthService(
	userRepository repositories.UserRepository,
	pwChecker core.PasswordChecker,
	middleware *jwt.GinJWTMiddleware,
	activationManager core.ActivationManager,
	identifier *identifier.IdentifierClient,
	notificationClient *notification.NotificationClient,
	providers map[oauth2.ProviderType]oauth2.Provider,

) *AuthServiceImpl {
	return &AuthServiceImpl{
		passwordChecker:    pwChecker,
		userRepository:     userRepository,
		middleware:         middleware,
		activationManager:  activationManager,
		identifier:         identifier,
		notificationClient: notificationClient,
		providers:          providers,
	}
}

func (s *AuthServiceImpl) GetOAuth2LoginURL(providerType string) string {
	provider, ok := s.providers[oauth2.ProviderType(providerType)]
	if !ok {
		return ""
	}
	return provider.GetLoginURL("state")
}

func (s *AuthServiceImpl) HandleOAuthLogin(ctx context.Context, providerType, code string) (*api.OAuthResponse, *models.ApiError) {
	provider, ok := s.providers[oauth2.ProviderType(providerType)]
	if !ok {
		return nil, &models.ApiError{
			Code:  http.StatusBadRequest,
			Error: models.ErrProviderNotFound,
		}
	}

	// Exchange code for token
	token, err := provider.ExchangeCode(ctx, code)
	if err != nil {
		return nil, err
	}

	// Fetch user info
	oauthUser, err := provider.GetUserInfo(ctx, token)
	if err != nil {
		return nil, err
	}

	//log.Printf("IBL: OAuth response from Facebook: %+v", oauthUser)

	// Link OAuth provider with user account
	user, apiErr := s.LinkOAuthProvider(ctx, *oauthUser)
	if apiErr != nil {
		return nil, apiErr
	}

	// Return response struct
	return &api.OAuthResponse{
		User: &user.User,
	}, nil
}

// LinkOAuthProvider links or creates a user based on OAuth login
func (s *AuthServiceImpl) LinkOAuthProvider(ctx context.Context, oauthUser db.OAuthUser) (*api.LoginResponse, *models.ApiError) {
	// Find existing user by email
	user, err := s.userRepository.GetByEmailCheck(ctx, oauthUser.Email)
	if err != nil {
		return nil, err
	}

	if user == nil {
		// Create new user
		// Generate external ID for new user
		id, apiErr := s.identifier.GenerateId(ctx, "user")
		if apiErr != nil {
			return nil, apiErr
		}

		user = mapping.ToDbUserFromOAuth(oauthUser, id.Code)
		accessToken, refreshToken, apiErr := s.generateTokens(user)
		user.Token = &accessToken
		user.UserRefreshToken = &refreshToken
		// Save new user in DB
		_, err := s.userRepository.CreateUser(ctx, user)
		if err != nil {
			return nil, err
		}
		// Send confirmation email
		dynamicData := map[string]string{
			"message":  "Thank you for registering!",
			"username": user.UserFirstname + " " + user.UserLastname,
			"subject":  "Thank you for Your Registration",
		}
		notificationData := notification.Request{
			To:          user.UserEmail,
			DynamicData: dynamicData,
		}

		if _, err := s.notificationClient.Send(ctx, notificationData); err != nil {
			fmt.Printf("Failed to send email: %v\n", err)
		}

		return &api.LoginResponse{
			User: mapping.ToApiUser(user), // Dereference the apiUser pointer

		}, nil
	} else {
		if _, exists := user.Providers[oauthUser.ProviderType]; exists {
			// Provider already exists, just log in the user
			accessToken, refreshToken, apiErr := s.generateTokens(user)
			user.Token = &accessToken
			user.UserRefreshToken = &refreshToken
			if apiErr != nil {
				return nil, apiErr
			}
			if err := s.userRepository.Update(ctx, user.UserEmail, user); err != nil {
				return nil, err
			}
			return &api.LoginResponse{
				User: mapping.ToApiUser(user), // Dereference the apiUser pointer

			}, nil
		}

		// Generate access and refresh tokens after update
		accessToken, refreshToken, apiErr := s.generateTokens(user)
		user.Token = &accessToken
		user.UserRefreshToken = &refreshToken

		if apiErr != nil {
			return nil, apiErr
		}
		if err := s.userRepository.UpdateUserProviders(ctx, user.UserEmail, oauthUser.ProviderType, oauthUser.ProviderID); err != nil {
			return nil, err
		}
		return &api.LoginResponse{
			User: mapping.ToApiUser(user), // Dereference the apiUser pointer

		}, nil
	}
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

func (s AuthServiceImpl) Register(ctx context.Context, req *api.CreateUserRequest) (*api.RegisterResponse, *models.ApiError) {

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
	dbUser.UserStatus = "ToConfirm"

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
	refreshTokenData := mapping.ToRefreshToken(user)
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

	user.Token = &token
	user.UserRefreshToken = &refreshToken
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

	u.UserStatus = "Active"
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
	externalID, err := s.userRepository.GetExternalIDByEmail(ctx, req.User.UserEmail)
	if err != nil {
		log.Printf("UpdateUser: error fetching user ExternalID - %v", err)
		return nil, err
	}

	// Apply update masks to the user data
	updateFields, err := masks.UpdateUserMasks(&req)
	if err != nil {
		log.Printf("UpdateUser: error applying masks to user - %v", err)
		return nil, err
	}

	// Update user data in the repository
	updatedUser, err := s.userRepository.UpdateUserByMask(ctx, externalID, updateFields)
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
	dbUser.UserStatus = "ToConfirm"

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
