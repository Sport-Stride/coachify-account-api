package services

import (
	"coachify-account-api/core"
	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
	"coachify-account-api/models/mapping"
	"coachify-account-api/models/masks"
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
	UpdateUser(ctx context.Context, req api.RequestUpdateUser) (*api.ApiUserResponse, *models.ApiError)
	GetAllUsersPag(api.SearchUser) ([]*api.ApiUserResponse, int, error)
	DeleteUser(ctx *gin.Context, id string) *models.ApiError
	AddUser(ctx *gin.Context, req *api.CreateUserRequest) (*api.RegisterResponse, *models.ApiError)
}

type AuthServiceImpl struct {
	userRepository     repositories.UserRepository
	passwordChecker    core.PasswordChecker
	activationManager  core.ActivationManager
	middleware         *jwt.GinJWTMiddleware
	identifier         *identifier.IdentifierClient
	notificationClient *notification.NotificationClient
}

func NewAuthService(
	userRepository repositories.UserRepository,
	pwChecker core.PasswordChecker,
	middleware *jwt.GinJWTMiddleware,
	activationManager core.ActivationManager,
	identifier *identifier.IdentifierClient,
	notificationClient *notification.NotificationClient,

) *AuthServiceImpl {
	return &AuthServiceImpl{
		passwordChecker:    pwChecker,
		userRepository:     userRepository,
		middleware:         middleware,
		activationManager:  activationManager,
		identifier:         identifier,
		notificationClient: notificationClient,
	}
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

	now := time.Now()
	// Password validation
	if !core.ValidatePassword(req.Password) {
		return nil, &models.ApiError{
			Code:  http.StatusBadRequest,
			Error: models.ErrIncorrectPassword,
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
	dbUser.UserCreatedAt = now
	dbUser.UserUpdatedAt = now
	token, err := utils.CreateToken(utils.CreateTokenParams{
		User: *dbUser,
		Type: "access",
	})

	if err != nil {

		return nil, err
	}

	refreshToken, err := utils.CreateToken(utils.CreateTokenParams{
		User: *dbUser,
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
	res, err := s.notificationClient.Send(ctx, data)
	log.Printf("%v", data)
	if err != nil {
		// Log error if sending fails
		models.NewApiError(http.StatusInternalServerError, models.ErrFailedToSendEmail)
	} else {
		// Log success response
		fmt.Println("Email sent successfully!", res)
	}

	if dbUser == nil {
		return nil, &models.ApiError{
			Code:  http.StatusBadRequest,
			Error: models.ErrDbUserIsNil,
		}
	}

	resp := &api.RegisterResponse{
		User:        mapping.ToApiUserResponse(inserted),
		AuthToken:   token, // Inclure le token dans la réponse
		RereshToken: refreshToken,
	}

	if req.Autologin {
		token, err := utils.CreateToken(utils.CreateTokenParams{
			User: *dbUser,
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

	if user == nil {
		return nil, models.NewApiError(http.StatusUnauthorized, models.ErrUserNotFound)
	}

	if user.UserStatus == db.Blocked {
		return nil, models.NewApiError(http.StatusUnauthorized, models.ErrUserBlocked)
	}

	if user.UserStatus == db.ToConfirm {
		return nil, models.NewApiError(http.StatusForbidden, models.ErrAccountNotConfirmed)
	}

	checked, err := s.passwordChecker.VerifyPassword(request.Password, user.UserPassword)
	if err != nil {
		return nil, models.NewApiError(http.StatusUnauthorized, models.ErrPasswordVerificationError)
	}

	if !checked {
		return nil, models.NewApiError(http.StatusUnauthorized, models.ErrIncorrectPassword)
	}

	user.UserLastLogin = time.Now()

	token, err := utils.CreateToken(utils.CreateTokenParams{
		User: *user,
		Type: "access",
	})
	if err != nil {
		return nil, err
	}

	refreshToken, err := utils.CreateToken(utils.CreateTokenParams{
		User: *user,
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

	if err := s.userRepository.Update(ctx, user.ID, user); err != nil {
		return nil, models.NewApiError(http.StatusInternalServerError, models.ErrFailedToUpdateUser)

	}

	// Return the LoginResponse with the token, refreshToken, and apiUser
	return &api.LoginResponse{
		User: mapping.ToApiUser(user), // Dereference the apiUser pointer

	}, nil

}

func (s AuthServiceImpl) Confirm(ctx context.Context, req *api.ConfirmUserRequest) *models.ApiError {
	// Fetch confirmation details
	confirmCode, isVerified, err := s.userRepository.GetConfirmationDetails(ctx, req.Email)
	if err != nil {
		return models.NewApiError(http.StatusInternalServerError, models.ErrInternalError)
	}
	if confirmCode == nil {
		return models.NewApiError(http.StatusUnauthorized, models.ErrUnknownUser)
	}
	if isVerified {
		return models.NewApiError(http.StatusUnauthorized, models.ErrUserAlreadyVerified)
	}

	// Validate or update the confirmation code
	if confirmCode.ExpirationDate.Before(time.Now()) {
		newConfirmCode := &db.UserConfirmCode{
			Code:           s.activationManager.GenerateCode(),
			ExpirationDate: time.Now().Add(24 * time.Hour),
		}
		if err := s.userRepository.UpdateConfirmationCode(ctx, req.Email, newConfirmCode); err != nil {
			return models.NewApiError(http.StatusInternalServerError, models.ErrFailedToUpdateUser)
		}
		return models.NewApiError(http.StatusUnauthorized, models.ErrInvalidConfirmationCode)
	}

	// Mark the user as verified
	if err := s.userRepository.MarkUserAsVerified(ctx, req.Email); err != nil {
		return models.NewApiError(http.StatusInternalServerError, models.ErrFailedToUpdateUser)
	}

	return nil
}

func (s AuthServiceImpl) ResendConfirmEmail(ctx context.Context, email string) *models.ApiError {
	// Fetch only the necessary fields (verification status and user ID)
	isVerified, err := s.userRepository.GetVerificationStatusAndID(ctx, email)
	if err != nil {
		return models.NewApiError(http.StatusInternalServerError, models.ErrInternalError)
	}

	// Check if the user is already verified
	if isVerified {
		return models.NewApiError(http.StatusUnauthorized, models.ErrUserAlreadyVerified)
	}

	// Generate a new confirmation code
	confirmCode := &db.UserConfirmCode{
		Code:           s.activationManager.GenerateCode(),
		ExpirationDate: time.Now().Add(24 * time.Hour),
	}

	// Update the user's confirmation code in the database
	if err := s.userRepository.UpdateConfirmationCode(ctx, email, confirmCode); err != nil {
		return models.NewApiError(http.StatusInternalServerError, models.ErrFailedToUpdateUser)
	}

	dynamicData := map[string]string{
		"message": "Please use the following code to reset your password: " + confirmCode.Code + " Best regards,The Support Team.",

		"subject": "Password Reset Request",
	}

	// Create the notification request
	data := notification.Request{
		To:          email,       // Recipient's email
		DynamicData: dynamicData, // Dynamic content
	}
	// Send the confirmation email
	if res, err := s.notificationClient.Send(ctx, data, "forgotpassword"); err != nil {
		// Log error if sending fails
		return models.NewApiError(http.StatusInternalServerError, models.ErrFailedToSendEmail)
	} else {
		fmt.Println("Email sent successfully!", res)
	}

	return nil
}

func (s AuthServiceImpl) InitResetPassword(ctx context.Context, request *api.ResetPasswordRequest) *models.ApiError {
	// Fetch only the necessary fields (status and user ID)
	userStatus, err := s.userRepository.GetStatusAndID(ctx, request.Email)
	if err != nil {
		return models.NewApiError(http.StatusInternalServerError, models.ErrInternalError)
	}

	// Check if the user is blocked
	if userStatus == db.Blocked {
		utils.Logger.Info("unable to send reset password email, user banned",
			zap.String("email", request.Email),
			zap.String("status", string(userStatus)),
		)
		return models.NewApiError(http.StatusInternalServerError, models.ErrUnableToSendResPass)
	}

	// Generate a new reset password code
	resetCode := &db.UserResetPasswordCode{
		Code:           s.activationManager.GenerateCode(),
		ExpirationDate: time.Now().Add(24 * time.Hour),
	}

	// Update the user's reset password code in the database
	if err := s.userRepository.UpdateResetPasswordCode(ctx, request.Email, resetCode); err != nil {
		return models.NewApiError(http.StatusInternalServerError, models.ErrFailedToUpdateUser)
	}

	// Send the reset password email
	dynamicData := map[string]string{
		"message": "Please use the following code to reset your password: " + resetCode.Code + " Best regards,The Support Team.",
		"subject": "Password Reset Request",
	}

	// Create the notification request
	data := notification.Request{
		To:          request.Email, // Recipient's email
		DynamicData: dynamicData,   // Dynamic content
	}
	// Send the notification email
	res, er := s.notificationClient.Send(ctx, data, "forgotpassword")
	log.Printf("%v", data)
	if er != nil {
		// Log error if sending fails
		models.NewApiError(http.StatusInternalServerError, models.ErrFailedToSendEmail)

	}
	// Log success response
	fmt.Println("Email sent successfully!", res)
	return nil
}

func (s AuthServiceImpl) ConfirmResetPassword(ctx *gin.Context, request *api.ConfirmResetPasswordRequest) *models.ApiError {
	// Password validation
	if !core.ValidatePassword(request.NewPassword) {
		return models.NewApiError(http.StatusBadRequest, models.ErrInvalidPassword)
	}

	// Fetch only the necessary fields (status and reset password code)
	userStatus, resetPasswordCode, err := s.userRepository.GetStatusAndResetPasswordCode(ctx, request.Email)
	if err != nil {
		return models.NewApiError(http.StatusInternalServerError, models.ErrInternalError)
	}

	// Check if the user is blocked
	if userStatus == db.Blocked {
		return models.NewApiError(http.StatusUnauthorized, models.ErrUserBlocked)
	}

	// Validate the reset password code
	if resetPasswordCode == nil || resetPasswordCode.ExpirationDate.Before(time.Now()) || resetPasswordCode.Code != request.Code {
		return models.NewApiError(http.StatusUnauthorized, models.ErrInvalidResetPasswordCode)
	}

	// Hash the new password
	encrypted, er := s.passwordChecker.HashPassword(request.NewPassword)
	if er != nil {
		return models.NewApiError(http.StatusInternalServerError, models.ErrFailedToHashPassword)
	}

	// Update the user's password and clear the reset password code
	if err := s.userRepository.UpdatePasswordAndClearResetCode(ctx, request.Email, encrypted); err != nil {
		return models.NewApiError(http.StatusInternalServerError, models.ErrFailedToUpdateUser)
	}

	// Send the password reset confirmation email
	dynamicData := map[string]string{
		"message": "Your password has been successfully reset.",

		"subject": "Password Reset Confirmation",
	}

	// Create the notification request
	data := notification.Request{
		To:          request.Email, // Recipient's email
		DynamicData: dynamicData,   // Dynamic content
	}

	// Send the notification email
	res, er := s.notificationClient.Send(ctx, data)
	log.Printf("%v", data)
	if er != nil {
		// Log error if sending fails
		models.NewApiError(http.StatusInternalServerError, models.ErrFailedToSendEmail)

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

func (s AuthServiceImpl) RefreshToken(ctx context.Context, username string, oldRefreshToken string) (string, *models.ApiError) {
	user, err := s.userRepository.GetByEmail(ctx, username)
	if err != nil {
		return "", err
	}
	fmt.Printf("UserRefreshToken : %s  , oldRefreshToken : %s \n", *user.UserRefreshToken, oldRefreshToken)
	if *user.UserRefreshToken != oldRefreshToken {
		log.Printf("Invalid refresh token: %v", oldRefreshToken)
		return "", models.NewApiError(http.StatusUnauthorized, models.ErrInvalidRefreshToken)
	}

	accessToken, err := utils.CreateToken(utils.CreateTokenParams{
		User: *user,
		Type: "access",
	})
	if err != nil {
		log.Printf("Error generating JWT token: %v", err)
		return "", models.NewApiError(http.StatusInternalServerError, models.ErrErrorGeneratingJWTToken)
	}

	user.UserUpdatedAt = time.Now()
	user.Token = &accessToken

	if err := s.userRepository.Update(ctx, user.ID, user); err != nil {
		log.Printf("Failed to update user: %v", err)
		return "", models.NewApiError(http.StatusInternalServerError, models.ErrFailedToUpdateUser)
	}

	return accessToken, nil
}

func (s *AuthServiceImpl) UpdateUser(ctx context.Context, req api.RequestUpdateUser) (*api.ApiUserResponse, *models.ApiError) {
	// Fetch user data by external ID
	data, err := s.userRepository.GetByEmail(ctx, req.User.UserEmail)
	if err != nil {
		return nil, err // Return the error if fetching fails
	}

	// Apply update masks to the user data
	dataDB, err := masks.UpdateUserMasks(data, &req)

	if err != nil {
		return nil, err // Return the error if masking fails
	}

	// Update user data in the repository
	updatedData, err := s.userRepository.UpdateUser(ctx, dataDB)
	if err != nil {
		return nil, err // Return the error if updating fails
	}
	log.Printf("token: %s", updatedData.Token) // Log the created event details
	// Convert updated data to API user response format
	dataResp := mapping.ToApiUserResponse(updatedData)

	return &dataResp, nil // Return the updated user response
}

func (s *AuthServiceImpl) GetAllUsersPag(searchUser api.SearchUser) ([]*api.ApiUserResponse, int, error) {
	// Convert the API SearchUser object to DB format
	sDB := mapping.SearchUserAPIToDB(searchUser)

	// Call the repository to retrieve users with pagination and filters
	res, count, err := s.userRepository.GetAllUsersPag(context.Background(), &sDB)
	if err != nil {
		return nil, 0, err.Error // Return an error if the retrieval fails
	}

	// Convert results from DB format to ApiUser format
	results := make([]*api.ApiUserResponse, 0)
	for _, v := range res {
		result := mapping.ToApiUserResponse(v)
		results = append(results, &result) // Append the converted user to the results
	}

	// Return the paginated users and the total count
	return results, count, nil
}

func (s *AuthServiceImpl) DeleteUser(ctx *gin.Context, id string) *models.ApiError {
	// Fetch user data by external ID
	_, err := s.userRepository.GetUserByExternalIdUpdate(ctx, id)
	if err != nil {
		log.Printf("UpdateUser: error fetching User from database - %s", err)
		return err // Return the error if fetching fails
	}
	// Delete the user from the repository
	err = s.userRepository.DeleteUser(ctx, id)
	if err != nil {
		return err // Return the error if user deletion fails
	}

	return nil // Return nil if everything was successful
}

func (s AuthServiceImpl) AddUser(ctx *gin.Context, req *api.CreateUserRequest) (*api.RegisterResponse, *models.ApiError) {
	now := time.Now()
	// Password validation
	if !core.ValidatePassword(req.Password) {
		return nil, models.NewApiError(http.StatusBadRequest, models.ErrInvalidPassword)
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

	token, err := utils.CreateToken(utils.CreateTokenParams{
		User: *dbUser,
		Type: "access",
	})
	if err != nil {

		return nil, err
	}
	refreshToken, err := utils.CreateToken(utils.CreateTokenParams{
		User: *dbUser,
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

	// Send the notification email
	res, er = s.notificationClient.Send(ctx, data)
	log.Printf("%v", data)
	if er != nil {
		// Log error if sending fails
		fmt.Printf("Failed to send email: %v\n", er.Error)

	}

	// Log success response
	fmt.Println("Email sent successfully!", res)

	if dbUser == nil {
		return nil, models.NewApiError(http.StatusBadRequest, models.ErrDbUserIsNil)
	}
	resp := &api.RegisterResponse{
		User:      mapping.ToApiUserResponse(inserted),
		AuthToken: token,

		RereshToken: refreshToken,
	}

	if req.Autologin {
		token, err := utils.CreateToken(utils.CreateTokenParams{
			User: *dbUser,
			Type: "access",
		})
		if err != nil {
			return nil, models.NewApiError(http.StatusInternalServerError, models.ErrErrorGeneratingJWTToken)
		}
		resp.AuthToken = token

	}

	return resp, nil
}
