package services

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"coachify-account-api/core"
	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
	"coachify-account-api/models/mapping"
	"coachify-account-api/pkg/identifier"
	"coachify-account-api/pkg/notification"
	"coachify-account-api/repositories"
	"coachify-account-api/utils"

	"go.uber.org/zap"
)

// services/coach_service.go
type CoachService interface {
	CheckClient(ctx context.Context, email string, coachID string) (*api.CheckClientResponse, *models.ApiError)
	GetClientsPaginated(ctx context.Context, coachID string, search api.SearchClient) ([]*api.ClientResponse, int, *models.ApiError)
	InviteClient(ctx context.Context, req *api.CreateUserRequest, coachID string) (*api.RegisterResponse, *models.ApiError)
	InviteMultipleClientsBulk(ctx context.Context, emails []string, coachID string) (*api.InviteClientResponse, *models.ApiError)
}

type CoachServiceImpl struct {
	coachRepo          *repositories.CoachRepository
	userRepo           *repositories.UserRepository
	notificationClient *notification.NotificationClient
	identifier         *identifier.IdentifierClient
	activationManager  core.ActivationManager
	baseURL            string
}

func NewCoachService(
	cr *repositories.CoachRepository,
	ur *repositories.UserRepository,
	nc *notification.NotificationClient,
	id *identifier.IdentifierClient,
	ac core.ActivationManager,
	baseURL string) *CoachServiceImpl {
	return &CoachServiceImpl{
		coachRepo:          cr,
		userRepo:           ur,
		notificationClient: nc,
		identifier:         id,
		activationManager:  ac,
		baseURL:            baseURL,
	}
}

func (s *CoachServiceImpl) GetClientsPaginated(ctx context.Context, coachID string, search api.SearchClient) ([]*api.ClientResponse, int, *models.ApiError) {
	utils.Logger.Info("Fetching clients for coach", zap.String("coachID", coachID))

	searchDB := mapping.SearchClientAPIToDB(search)

	clientIDs, total, err := s.coachRepo.GetCoachClientIDs(ctx, coachID, searchDB)
	if err != nil {
		return nil, 0, err
	}

	users, err := s.userRepo.GetUsersByExternalIds(ctx, clientIDs)
	if err != nil {
		return nil, 0, err
	}

	responses := make([]*api.ClientResponse, 0, len(users))
	for _, user := range users {
		response := mapping.ToClientResponse(user)
		responses = append(responses, &response)
	}

	return responses, total, nil
}

func (s *CoachServiceImpl) sendInvitationEmail(ctx context.Context, dbUser *db.User, plainPassword, coachName string) {
	//invitationLink := fmt.Sprintf("%s/confirm-client-invite?code=%s", s.baseURL, code)
	// Prepare dynamic email data, including the temporary password.
	confirmLink := fmt.Sprintf("%s/confirm-registration?code=%s", s.baseURL, dbUser.UserConfirmCode.Code)
	dynamicData := map[string]string{
		"message":  "Thank you for registering! Your temporary password is: " + plainPassword + ". Please confirm your email by clicking the following link: " + confirmLink + " Coach :" + coachName,
		"username": dbUser.UserFirstname + " " + dbUser.UserLastname,
		"subject":  "Confirmation of Your Registration",
	}
	// dynamicData := map[string]string{
	// 	"coachName":   coachName,
	// 	"clientEmail": email,
	// 	"link":        invitationLink,
	// }
	data := notification.Request{
		To:          dbUser.UserEmail,
		DynamicData: dynamicData,
	}
	res, er := s.notificationClient.Send(ctx, data)
	log.Printf("Invitation email data: %v", data)
	if er != nil {
		fmt.Printf("Failed to send email: %v\n", er.Error)
	} else {
		fmt.Println("Email sent successfully!", res)
	}
}

// Auth signin Service
func (s *CoachServiceImpl) RegisterClient(ctx context.Context, req *api.CreateUserRequest, coachName string) (*api.RegisterResponse, *models.ApiError) {

	id, apiErr := s.identifier.GenerateId(ctx, "user")
	if apiErr != nil {
		return nil, apiErr
	}

	confirmCode := &db.UserConfirmCode{
		Code:           s.activationManager.GenerateCode(), // Générer un code
		ExpirationDate: time.Now().Add(24 * time.Hour),
	}

	// Generate a strong temporary password and hash it.
	plainPassword, hashedPassword, err := utils.GenerateAndHashPassword()
	if err != nil {
		return nil, err
	}

	dbUser := mapping.CreateToDbUser(req, hashedPassword, id.Code, *confirmCode)
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

	inserted, err := s.userRepo.CreateUser(ctx, dbUser)
	if err != nil {

		return nil, err
	}

	s.sendInvitationEmail(ctx, dbUser, plainPassword, coachName)

	resp := &api.RegisterResponse{
		User:        mapping.ToApiUserResponse(inserted),
		AuthToken:   token, // Inclure le token dans la réponse
		RereshToken: refreshToken,
	}

	// if req.Autologin {
	// 	token, err := utils.CreateToken(utils.CreateTokenParams{
	// 		User: refreshTokenData,
	// 		Type: "access",
	// 	})
	// 	if err != nil {
	// 		return nil, err
	// 	}
	// 	resp.AuthToken = token
	// }

	return resp, nil
}

// InviteClient invites a single client.
func (s *CoachServiceImpl) CheckClient(ctx context.Context, email string, coachID string) (*api.CheckClientResponse, *models.ApiError) {
	// Get user by email
	user, err := s.userRepo.CheckEmail(ctx, email)
	if err != nil {
		return nil, err
	}

	// Get coach name
	coachName, err := s.userRepo.GetUserNameByExternalID(ctx, coachID)
	if err != nil {
		return nil, err
	}

	response := &api.CheckClientResponse{
		CoachName: coachName,
	}

	if user == nil {
		response.UserExists = false
		return response, nil
	} else {
		response.UserExists = true
	}

	// Check existing relationship
	_, err = s.coachRepo.FindInvitation(ctx, user.ExternalID, coachID)
	if err == nil {
		response.AlreadyLinked = false
		return response, nil
	} else {
		response.UserExists = true
		return response, nil
	}
}

func (s *CoachServiceImpl) InviteClient(ctx context.Context, req *api.CreateUserRequest, coachID string) (*api.RegisterResponse, *models.ApiError) {

	user, err := s.userRepo.GetByEmailToInvite(ctx, req.Email)
	if err != nil {
		return nil, err
	}
	coachName, err := s.userRepo.GetUserNameByExternalID(ctx, coachID)
	if err != nil {
		return nil, err
	}

	var clientID string
	var registerResponse *api.RegisterResponse

	if user != nil {
		clientID = user.ExternalID

		invitation, err := s.coachRepo.FindInvitation(ctx, clientID, coachID)
		if err != nil {
			return nil, err
		}

		if invitation != nil {
			return nil, &models.ApiError{
				Code:  http.StatusBadRequest,
				Error: models.ErrClientAlreadyLinked,
			}
		}

		userResp := mapping.ToUserResponse(user)
		registerResponse = &api.RegisterResponse{
			User:        mapping.ToApiUserResponse(&userResp),
			AuthToken:   "",
			RereshToken: "",
		}
	} else {
		registerResponse, err = s.RegisterClient(ctx, req, coachName)
		if err != nil {
			return nil, err
		}
		clientID = registerResponse.User.ExternalID
	}

	invitationRecord := mapping.MapCoachClientInvitation(coachID, clientID)

	if apiErr := s.coachRepo.CreateCoachClient(ctx, invitationRecord); apiErr != nil {
		return nil, apiErr
	}

	return registerResponse, nil
}

// InviteMultipleClientsBulk invites multiple clients with bulk user insertion and bulk invitation creation.
func (s *CoachServiceImpl) InviteMultipleClientsBulk(ctx context.Context, emails []string, coachID string) (*api.InviteClientResponse, *models.ApiError) {
	coachName, err := s.userRepo.GetUserNameByExternalID(ctx, coachID)
	if err != nil {
		return nil, err
	}

	var successCount int
	var failedEmails []string
	var usersToInsert []*db.User
	var plainPasswords []string               // To store temporary passwords for new users
	var invitationsToInsert []*db.CoachClient // Bulk invitation entries

	// Process each email.
	for _, email := range emails {
		user, err := s.userRepo.GetByEmailToInvite(ctx, email)
		if err != nil {
			failedEmails = append(failedEmails, email)
			continue
		}

		if user != nil {
			// Check if an invitation already exists.
			invitation, err := s.coachRepo.FindInvitation(ctx, user.ExternalID, coachID)
			if err != nil {
				failedEmails = append(failedEmails, email)
				continue
			}

			if invitation != nil {
				continue // Already invited.
			}

			// Instead of creating invitation immediately, add to the bulk slice.
			invitationsToInsert = append(invitationsToInsert, &db.CoachClient{
				ClientID: user.ExternalID,
				CoachID:  coachID,
				// Add other fields as needed.
			})
			successCount++
		} else {
			// Prepare new user for bulk insertion.
			id, apiErr := s.identifier.GenerateId(ctx, "user")
			if apiErr != nil {
				failedEmails = append(failedEmails, email)
				continue
			}

			confirmCode := &db.UserConfirmCode{
				Code:           s.activationManager.GenerateCode(),
				ExpirationDate: time.Now().Add(24 * time.Hour),
			}

			plainPassword, hashedPassword, err := utils.GenerateAndHashPassword()
			if err != nil {
				failedEmails = append(failedEmails, email)
				continue
			}

			// Create a minimal CreateUserRequest from email.
			createReq := &api.CreateUserRequest{
				Email:     email,
				FirstName: "", // Defaults or extract from additional invitation data if available.
				LastName:  "",
			}

			// Map to db.User.
			dbUser := mapping.CreateToDbUser(createReq, hashedPassword, id.Code, *confirmCode)

			// Generate tokens similar to RegisterClient.
			refreshTokenData := mapping.ToRefreshToken(dbUser)
			accessToken, err := utils.CreateToken(utils.CreateTokenParams{
				User: refreshTokenData,
				Type: "access",
			})
			if err != nil {
				failedEmails = append(failedEmails, email)
				continue
			}

			refreshToken, err := utils.CreateToken(utils.CreateTokenParams{
				User: refreshTokenData,
				Type: "refresh",
			})
			if err != nil {
				failedEmails = append(failedEmails, email)
				continue
			}

			dbUser.Token = &accessToken
			dbUser.UserRefreshToken = &refreshToken
			dbUser.UserStatus = db.ToConfirm

			usersToInsert = append(usersToInsert, dbUser)
			plainPasswords = append(plainPasswords, plainPassword)
		}
	}

	// Bulk insert new users.
	if len(usersToInsert) > 0 {
		insertedUsers, err := s.userRepo.CreateUsers(ctx, usersToInsert)
		if err != nil {
			// If bulk user insertion fails, mark all pending emails as failed.
			for _, user := range usersToInsert {
				failedEmails = append(failedEmails, user.UserEmail)
			}
		} else {
			// For each successfully inserted user, send invitation email and accumulate invitation.
			for i, user := range insertedUsers {
				// Send invitation email.
				s.sendInvitationEmail(ctx, user, plainPasswords[i], coachName)

				invitationRecord := mapping.MapCoachClientInvitation(coachID, user.ExternalID)
				// Accumulate invitation.
				invitationsToInsert = append(invitationsToInsert, invitationRecord)
				successCount++
			}
		}
	}

	// Bulk insert invitations if there are any.
	if len(invitationsToInsert) > 0 {
		if err := s.coachRepo.CreateInvitations(ctx, invitationsToInsert); err != nil {
			// If the bulk insert of invitations fails, record all associated emails as failures.
			// (You might need additional mapping from invitation to email depending on your schema.)
			for _, inv := range invitationsToInsert {
				// For demonstration, assume you can obtain the email using the external ID.
				// In practice, you may store the email along with the invitation struct.

				user, uErr := s.userRepo.GetUserByExternalId(ctx, inv.ClientID)
				if uErr == nil && user != nil {
					failedEmails = append(failedEmails, user.Email)
				}
			}
		}
	}

	// Compile the response message.
	msg := fmt.Sprintf("Successfully processed %d invitations", successCount)
	if len(failedEmails) > 0 {
		msg += fmt.Sprintf(", %d failed: %v", len(failedEmails), failedEmails)
	}

	return &api.InviteClientResponse{
		Message:    msg,
		Successful: int32(successCount),
		Failed:     int32(len(failedEmails)),
	}, nil
}
