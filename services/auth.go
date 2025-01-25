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
	"errors"
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

	Register(ctx context.Context, req *api.CreateUserRequest) (*api.RegisterResponse, *models.ApiError)
	Confirm(ctx context.Context, req *api.ConfirmUserRequest) *models.ApiError
	ResendConfirmEmail(ctx context.Context, email string) *models.ApiError
	TryToConnect(ctx context.Context, request api.LoginRequest) (*api.LoginResponse, *models.ApiError)
	InitResetPassword(ctx context.Context, request *api.ResetPasswordRequest) *models.ApiError
	ConfirmResetPassword(ctx *gin.Context, request *api.ConfirmResetPasswordRequest) *models.ApiError
	GetUserByExternalId(ctx context.Context, userId string) (*api.ApiUserResponse, *models.ApiError)
	RefreshToken(ctx context.Context, username string, oldRefreshToken string) (string, *models.ApiError)
	UpdateUser(ctx context.Context, id string, req api.RequestUpdateUser) (*api.ApiUserResponse, *models.ApiError)
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

func (s AuthServiceImpl) Register(ctx context.Context, req *api.CreateUserRequest) (*api.RegisterResponse, *models.ApiError) {

	now := time.Now()
	// Password validation
	if !core.ValidatePassword(req.Password) {
		return nil, &models.ApiError{
			Code:  400,
			Error: errors.New("the password must contain at least 8 characters, one uppercase letter, one lowercase letter, and one symbol"),
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
	dbUser.CoachExternalID = ""
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
	res, er := s.notificationClient.Send(ctx, data)
	log.Printf("%v", data)
	if er != nil {
		// Log error if sending fails
		fmt.Printf("Failed to send email: %v\n", er.Error)

	}

	// Log success response
	fmt.Println("Email sent successfully!", res)
	if dbUser == nil {
		return nil, &models.ApiError{
			Code:  400,
			Error: errors.New("dbUser is nil")}
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
			return nil, &models.ApiError{
				Code:  500,
				Error: errors.New("error generating jwt token"),
			}
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
		return nil, &models.ApiError{
			Code:  401,
			Error: errors.New("authentication failed: user object is nil"),
		}
	}

	if user.UserStatus == db.Blocked {
		return nil, &models.ApiError{
			Code:  401,
			Error: errors.New("authentication failed: account is blocked"),
		}
	}

	if user.UserStatus == db.ToConfirm {
		return nil, &models.ApiError{
			Code:  403,
			Error: errors.New("authentication failed: account not confirmed"),
		}
	}

	checked, e := s.passwordChecker.VerifyPassword(request.Password, user.UserPassword)
	if e != nil {
		return nil, &models.ApiError{
			Code:  401,
			Error: errors.New("authentication failed: error verifying password"),
		}
	}

	if !checked {
		return nil, &models.ApiError{
			Code:  401,
			Error: errors.New("authentication failed: incorrect password provided"),
		}
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
		return nil, &models.ApiError{
			Code:  500,
			Error: errors.New("local server error: unable to update last login"),
		}
	}

	// Return the LoginResponse with the token, refreshToken, and apiUser
	return &api.LoginResponse{
		User: mapping.ToApiUser(user), // Dereference the apiUser pointer

	}, nil

}

func (s AuthServiceImpl) Confirm(ctx context.Context, req *api.ConfirmUserRequest) *models.ApiError {
	u, err := s.userRepository.GetByEmail(ctx, req.Email)
	if err != nil {
		return &models.ApiError{
			Code:  401,
			Error: errors.New("unknown_user"),
		}
	}

	if u.UserVerificationStatus {
		return &models.ApiError{
			Code:  401,
			Error: errors.New("user_already_verified"),
		}
	}

	if u.UserConfirmCode == nil || u.UserConfirmCode.ExpirationDate.Before(time.Now()) {
		confirmCode := &db.UserConfirmCode{
			Code:           s.activationManager.GenerateCode(),
			ExpirationDate: time.Now().Add(24 * time.Hour),
		}

		u.UserConfirmCode = confirmCode
		u.UserUpdatedAt = time.Now()
		err = s.userRepository.Update(ctx, u.ID, u)
		if err != nil {
			return err
		}

		dynamicData := map[string]string{
			"message":  "Thank you for registering! Here is your confirmation code: " + u.UserConfirmCode.Code,
			"username": u.UserFirstname + " " + u.UserLastname,
			"subject":  "Confirmation of Your Registration",
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
			Code:  401,
			Error: errors.New("invalid_confirmation_code"),
		}
	}

	if req.ConfirmCode != u.UserConfirmCode.Code {
		return &models.ApiError{
			Code:  401,
			Error: errors.New("invalid_confirmation_code"),
		}
	}

	u.UserStatus = "Active"
	u.UserVerificationStatus = true
	u.UserUpdatedAt = time.Now()
	err = s.userRepository.Update(ctx, u.ID, u)

	if err != nil {
		return err
	}

	dynamicData := map[string]string{
		"message":  "Welcome to our platform. We are excited to have you on board!",
		"username": u.UserFirstname + " " + u.UserLastname,
		"subject":  "Welcome to Our Service!",
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

func (s AuthServiceImpl) ResendConfirmEmail(ctx context.Context, email string) *models.ApiError {
	u, err := s.userRepository.GetByEmail(ctx, email)
	if err != nil {
		return &models.ApiError{
			Code:  401,
			Error: errors.New("unknown_user"),
		}
	}
	if u.UserVerificationStatus {
		return &models.ApiError{
			Code:  401,
			Error: errors.New("user_already_verified"),
		}
	}

	confirmCode := &db.UserConfirmCode{
		Code:           s.activationManager.GenerateCode(),
		ExpirationDate: time.Now().Add(24 * time.Hour),
	}

	u.UserConfirmCode = confirmCode
	err = s.userRepository.Update(ctx, u.ID, u)
	if err != nil {
		return err
	}

	dynamicData := map[string]string{
		"message":  "Thank you for registering! Here is your confirmation code: " + u.UserConfirmCode.Code,
		"username": u.UserFirstname + " " + u.UserLastname,
		"subject":  "Confirmation of Your Registration",
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
	user, err := s.userRepository.GetByEmail(ctx, request.Email)

	if err != nil {

		return err
	}

	if user.UserStatus == db.Blocked {
		utils.Logger.Info("unable to send reset password email, user banned ",
			zap.String("email", user.UserEmail),
			zap.String("status", string(user.UserStatus)),
		)
		return nil
	}

	user.UserResetPasswordCode = &db.UserResetPasswordCode{
		Code:           s.activationManager.GenerateCode(),
		ExpirationDate: time.Now().Add(24 * time.Hour),
	}
	err = s.userRepository.Update(ctx, user.ID, user)
	if err != nil {
		return err
	}

	dynamicData := map[string]string{
		"message":  "Please use the following code to reset your password: " + user.UserResetPasswordCode.Code + " Best regards,The Support Team.",
		"username": user.UserFirstname + " " + user.UserLastname,
		"subject":  "Password Reset Request",
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

func (s AuthServiceImpl) ConfirmResetPassword(ctx *gin.Context, request *api.ConfirmResetPasswordRequest) *models.ApiError {
	// Password validation
	if !core.ValidatePassword(request.NewPassword) {
		return &models.ApiError{
			Code:  400,
			Error: errors.New("the password must contain at least 8 characters, one uppercase letter, one lowercase letter, and one symbol"),
		}
	}
	user, err := s.userRepository.GetByEmail(ctx, request.Email)
	if err != nil || user.UserStatus == db.Blocked {

		return err
	}

	resetPasswordCode := user.UserResetPasswordCode
	if resetPasswordCode == nil || resetPasswordCode.ExpirationDate.Before(time.Now()) || resetPasswordCode.Code != request.Code {
		return &models.ApiError{
			Code:  401,
			Error: errors.New("invalid_reset_password_code"),
		}
	}

	encrypted, err := s.passwordChecker.HashPassword(request.NewPassword)
	if err != nil {
		return err
	}
	user.UserPassword = encrypted
	user.UserResetPasswordCode = nil
	err = s.userRepository.Update(ctx, user.ID, user)
	if err != nil {
		return err
	}

	dynamicData := map[string]string{
		"message":  "Your password has been successfully reset.",
		"username": user.UserFirstname + " " + user.UserLastname,
		"subject":  "Password Reset Confirmation",
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

func (s AuthServiceImpl) RefreshToken(ctx context.Context, username string, oldRefreshToken string) (string, *models.ApiError) {
	user, err := s.userRepository.GetByEmail(ctx, username)
	if err != nil {
		return "", &models.ApiError{
			Code:  http.StatusNotFound,
			Error: errors.New("user not found"),
		}
	}

	if *user.UserRefreshToken != oldRefreshToken {
		return "", &models.ApiError{
			Code:  http.StatusUnauthorized,
			Error: errors.New("invalid refresh token"),
		}
	}

	accessToken, e := utils.CreateToken(utils.CreateTokenParams{
		User: *user,
		Type: "access",
	})
	if e != nil {
		return "", &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: errors.New("error generating jwt token"),
		}
	}

	user.UserUpdatedAt = time.Now()
	user.Token = &accessToken
	//user.UserRefreshToken = &newRefreshToken

	if err := s.userRepository.Update(ctx, user.ID, user); err != nil {
		return "", &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: errors.New("local server error: unable to update user"),
		}
	}

	return accessToken, nil
}

func (s *AuthServiceImpl) UpdateUser(ctx context.Context, id string, req api.RequestUpdateUser) (*api.ApiUserResponse, *models.ApiError) {
	// Fetch user data by external ID
	data, err := s.userRepository.GetUserByExternalIdUpdate(ctx, id)
	if err != nil {
		log.Printf("UpdateUser: error fetching User from database - %s", err)
		return nil, err // Return the error if fetching fails
	}

	// Apply update masks to the user data
	dataDB, err := masks.UpdateUserMasks(data, &req)

	if err != nil {
		log.Printf("UpdateUser: error applying masks to user - %s", err)
		return nil, err // Return the error if masking fails
	}

	// Update user data in the repository
	updatedData, err := s.userRepository.UpdateUser(ctx, id, dataDB)
	if err != nil {
		log.Printf("UpdateUser: error updating user in database - %s", err)
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
	// Validate that CoachExternalID is provided
	if req.CoachExternalID == "" {
		return nil, &models.ApiError{
			Code:  400,
			Error: fmt.Errorf("CoachExternalID is required"),
		}
	}

	// Fetch users by coach ID
	nbr, err := s.userRepository.GetUsersByOrgID(ctx, req.CoachExternalID)
	if err != nil {
		return nil, &models.ApiError{
			Code:  500,
			Error: fmt.Errorf("failed to fetch users for coach %s: %w", req.CoachExternalID, err),
		}
	}
	log.Printf("users by coach: %s= %d", req.CoachExternalID, nbr)

	now := time.Now()
	// Password validation
	if !core.ValidatePassword(req.Password) {
		return nil, &models.ApiError{
			Code:  400,
			Error: errors.New("the password must contain at least 8 characters, one uppercase letter, one lowercase letter, and one symbol"),
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
		return nil, &models.ApiError{
			Code:  400,
			Error: errors.New("dbUser is nil")}
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
			return nil, &models.ApiError{
				Code:  500,
				Error: errors.New("error generating jwt token"),
			}
		}
		resp.AuthToken = token

	}

	return resp, nil
}
