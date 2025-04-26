package services

import (
	"context"
	"fmt"

	"coachify-account-api/core"
	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
	"coachify-account-api/pkg/identifier"
	"coachify-account-api/pkg/notification"
	"coachify-account-api/repositories"
)

// services/coach_service.go
type CoachService interface {
	InviteClient(ctx context.Context, email string, coachID string) error
	InviteMultipleClientsBulk(ctx context.Context, emails []string, coachID string) (int, []string)
	RegisterClientWithInvitation(ctx context.Context, code string, req *api.CreateUserRequest) (*api.RegisterResponse, error)
	ListInvitations(ctx context.Context, coachID string) ([]*db.CoachClientInvitation, error)
	DeleteInvitation(ctx context.Context, code string) error
	GetAllCoachClientIDs(ctx context.Context, coachID string) ([]string, *models.ApiError)
}

type CoachServiceImpl struct {
	coachRepo          *repositories.CoachRepository
	userRepo           *repositories.UserRepository
	authService        AuthService
	notificationClient *notification.NotificationClient
	identifier         *identifier.IdentifierClient
	activationManager  core.ActivationManager
	baseURL            string
}

func NewCoachService(
	cr *repositories.CoachRepository,
	ur *repositories.UserRepository,
	authService AuthService,
	nc *notification.NotificationClient,
	id *identifier.IdentifierClient,
	ac core.ActivationManager,
	baseURL string) *CoachServiceImpl {
	return &CoachServiceImpl{
		coachRepo:          cr,
		userRepo:           ur,
		authService:        authService,
		notificationClient: nc,
		identifier:         id,
		activationManager:  ac,
		baseURL:            baseURL,
	}
}
func (s *CoachServiceImpl) GetAllCoachClientIDs(ctx context.Context, coachID string) ([]string, *models.ApiError) {
	clientIDs, err := s.coachRepo.GetAllCoachClientIDs(ctx, coachID)
	if err != nil {
		return nil, err
	}
	return clientIDs, nil
}

// Invite a single client (send email, do not register user)
func (s *CoachServiceImpl) InviteClient(ctx context.Context, email string, coachID string) error {
	code := generateInvitationCode()
	invitation := &db.CoachClientInvitation{
		CoachID: coachID,
		Email:   email,
		Code:    code,
		Status:  db.InvitationPending,
	}
	if err := s.coachRepo.CreateInvitation(ctx, invitation); err != nil {
		return err
	}
	link := fmt.Sprintf("%s/register?invite=%s", s.baseURL, code)
	data := notification.Request{
		To: email,
		DynamicData: map[string]string{
			"subject": "You've been invited!",
			"message": fmt.Sprintf("You have been invited to join. Click here to register: %s", link),
			"link":    link,
		},
	}
	_, err := s.notificationClient.Send(ctx, data)
	return err.Error
}

// Bulk invite
func (s *CoachServiceImpl) InviteMultipleClientsBulk(ctx context.Context, emails []string, coachID string) (int, []string) {
	var failed []string
	var invitations []*db.CoachClientInvitation
	for _, email := range emails {
		code := generateInvitationCode()
		invitations = append(invitations, &db.CoachClientInvitation{
			CoachID: coachID,
			Email:   email,
			Code:    code,
			Status:  db.InvitationPending,
		})
	}
	if err := s.coachRepo.CreateInvitations(ctx, invitations); err != nil {
		for _, inv := range invitations {
			failed = append(failed, inv.Email)
		}
		return 0, failed
	}
	// Send emails
	for _, inv := range invitations {
		link := fmt.Sprintf("%s/register?invite=%s", s.baseURL, inv.Code)
		data := notification.Request{
			To: inv.Email,
			DynamicData: map[string]string{
				"subject": "You've been invited!",
				"message": fmt.Sprintf("You have been invited to join. Click here to register: %s", link),
				"link":    link,
			},
		}
		if _, err := s.notificationClient.Send(ctx, data); err != nil {
			failed = append(failed, inv.Email)
		}
	}
	return len(invitations) - len(failed), failed
}

// When client registers, validate invitation code
func (s *CoachServiceImpl) RegisterClientWithInvitation(ctx context.Context, code string, req *api.CreateUserRequest) (*api.RegisterResponse, error) {
	inv, err := s.coachRepo.FindInvitationByCode(ctx, code)
	if err != nil || inv.Status != db.InvitationPending {
		return nil, fmt.Errorf("invalid or expired invitation")
	}

	// Ensure the email in the invitation matches the registration request
	if inv.Email != req.Email {
		return nil, fmt.Errorf("email does not match invitation")
	}

	registerResp, apiErr := s.authService.Register(ctx, req)
	if apiErr != nil {
		return nil, apiErr.Error
	}

	// Mark invitation as accepted
	_ = s.coachRepo.UpdateInvitationStatus(ctx, code, db.InvitationAccepted)

	return registerResp, nil
}

// CRUD for invitations
func (s *CoachServiceImpl) ListInvitations(ctx context.Context, coachID string) ([]*db.CoachClientInvitation, error) {
	return s.coachRepo.ListInvitations(ctx, coachID)
}
func (s *CoachServiceImpl) DeleteInvitation(ctx context.Context, code string) error {
	return s.coachRepo.DeleteInvitation(ctx, code)
}
