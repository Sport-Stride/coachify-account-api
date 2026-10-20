package services

import (
	"context"
	"errors"
	"net/http"
	"time"

	"go.uber.org/zap"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
)

// AdminCoachRow is the coach record returned by the admin list endpoint.
// Subscription data is NOT included here; the frontend fetches it directly
// from payments-api to avoid synchronous cross-service coupling.
type AdminCoachRow struct {
	ExternalID     string    `json:"external_id"`
	Firstname      string    `json:"firstname"`
	Lastname       string    `json:"lastname"`
	Email          string    `json:"email"`
	Status         string    `json:"status"`
	ProfilePicture string    `json:"profile_picture,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
	IsHeadCoach    bool      `json:"is_head_coach"`
	HeadCoachID    string    `json:"head_coach_id,omitempty"`
	HeadCoachName  string    `json:"head_coach_name,omitempty"`
	TeamSize       int       `json:"team_size"`
}

// AdminClientRow is the client record returned by the admin coach-clients endpoint.
type AdminClientRow struct {
	ExternalID     string    `json:"external_id"`
	Firstname      string    `json:"firstname"`
	Lastname       string    `json:"lastname"`
	Email          string    `json:"email"`
	Status         string    `json:"status"`
	ProfilePicture string    `json:"profile_picture,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// AdminUserList is a page of admin user rows.
type AdminUserList struct {
	Users []db.AdminUserRow `json:"users"`
	Total int               `json:"total"`
	Page  int               `json:"page"`
	Size  int               `json:"size"`
}

const maxAdminUserIDs = 100

var (
	adminUserRoles    = map[string]bool{"client": true, "coach": true, "nutritionist": true, "admin": true}
	adminUserStatuses = map[string]bool{
		string(db.Active): true, string(db.Inactive): true, string(db.Suspended): true,
		string(db.ComReg1): true, string(db.ComReg2): true, string(db.ComReg3): true,
		string(db.Deactivated): true, string(db.ToConfirm): true, string(db.Blocked): true,
	}
	adminUserSorts = map[string]bool{
		db.AdminUserSortCreatedAt: true, db.AdminUserSortLastLogin: true,
		db.AdminUserSortName: true, db.AdminUserSortEmail: true,
	}
)

// AdminService exposes read-only admin views over users, coaches and their clients.
type AdminService interface {
	ListCoaches(ctx context.Context, page, size int, filter db.AdminCoachFilter) ([]AdminCoachRow, int, error)
	ListCoachClients(ctx context.Context, coachID string) ([]AdminClientRow, error)
	ListUsers(ctx context.Context, q db.AdminUserQuery) (*AdminUserList, *models.ApiError)
	GetUser(ctx context.Context, externalID string) (*db.AdminUserDetail, *models.ApiError)
	UserStats(ctx context.Context, days int) (*db.AdminUserStats, *models.ApiError)
	UpdateUserStatus(ctx context.Context, actorID, targetID, status, reason string) (*db.AdminUserRow, *models.ApiError)
	ResendConfirmation(ctx context.Context, actorID, targetID string) *models.ApiError
	SendPasswordReset(ctx context.Context, actorID, targetID string) *models.ApiError
	ListAudit(ctx context.Context, f db.AdminAuditFilter, page, size int) (*AdminAuditList, *models.ApiError)
}

type adminServiceImpl struct {
	userRepo  AdminUserStore
	coachRepo *repositories.CoachRepository
	// auditRepo records admin mutations (used by the write endpoints).
	auditRepo AdminAuditStore
	// mailer sends the confirmation and password reset emails (AuthService).
	mailer AccountEmailSender
}

func NewAdminService(
	userRepo AdminUserStore,
	coachRepo *repositories.CoachRepository,
	auditRepo AdminAuditStore,
	mailer AccountEmailSender,
) AdminService {
	return &adminServiceImpl{
		userRepo:  userRepo,
		coachRepo: coachRepo,
		auditRepo: auditRepo,
		mailer:    mailer,
	}
}

func badRequest(msg string) *models.ApiError {
	return models.NewApiError(http.StatusBadRequest, errors.New(msg))
}

func internalError() *models.ApiError {
	return models.NewApiError(http.StatusInternalServerError, errors.New("internal error"))
}

func (s *adminServiceImpl) ListUsers(ctx context.Context, q db.AdminUserQuery) (*AdminUserList, *models.ApiError) {
	if q.Role != "" && !adminUserRoles[q.Role] {
		return nil, badRequest("invalid role")
	}
	if q.Status != "" && !adminUserStatuses[q.Status] {
		return nil, badRequest("invalid status")
	}
	if q.Sort == "" {
		q.Sort = db.AdminUserSortCreatedAt
	}
	if !adminUserSorts[q.Sort] {
		return nil, badRequest("invalid sort")
	}
	if q.Order == "" {
		q.Order = db.AdminOrderDesc
		if q.Sort == db.AdminUserSortName || q.Sort == db.AdminUserSortEmail {
			q.Order = db.AdminOrderAsc
		}
	}
	if q.Order != db.AdminOrderAsc && q.Order != db.AdminOrderDesc {
		return nil, badRequest("invalid order")
	}
	if len(q.IDs) > maxAdminUserIDs {
		return nil, badRequest("too many ids")
	}
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Size < 1 || q.Size > 100 {
		q.Size = 20
	}

	rows, total, err := s.userRepo.ListAdminUsers(ctx, q)
	if err != nil {
		zap.L().Error("admin list users failed", zap.String("component", "admin"), zap.Error(err))
		return nil, internalError()
	}
	for i := range rows {
		if rows[i].LastLogin != nil && rows[i].LastLogin.IsZero() {
			rows[i].LastLogin = nil
		}
	}
	return &AdminUserList{Users: rows, Total: total, Page: q.Page, Size: q.Size}, nil
}

func (s *adminServiceImpl) GetUser(ctx context.Context, externalID string) (*db.AdminUserDetail, *models.ApiError) {
	d, err := s.userRepo.GetAdminUserDetail(ctx, externalID)
	if err != nil {
		zap.L().Error("admin get user failed", zap.String("component", "admin"), zap.Error(err))
		return nil, internalError()
	}
	if d == nil {
		return nil, models.NewApiError(http.StatusNotFound, models.ErrUserNotFound)
	}
	if d.LastLogin != nil && d.LastLogin.IsZero() {
		d.LastLogin = nil
	}
	return d, nil
}

func (s *adminServiceImpl) UserStats(ctx context.Context, days int) (*db.AdminUserStats, *models.ApiError) {
	st, err := s.userRepo.AdminUserStats(ctx, days, time.Now())
	if err != nil {
		zap.L().Error("admin user stats failed", zap.String("component", "admin"), zap.Error(err))
		return nil, internalError()
	}
	return st, nil
}

func (s *adminServiceImpl) ListCoaches(ctx context.Context, page, size int, filter db.AdminCoachFilter) ([]AdminCoachRow, int, error) {
	users, total, err := s.userRepo.GetCoachesByPage(ctx, page, size, filter)
	if err != nil {
		return nil, 0, err
	}

	rows := make([]AdminCoachRow, 0, len(users))
	for _, u := range users {
		rows = append(rows, AdminCoachRow{
			ExternalID:     u.ExternalID,
			Firstname:      u.Firstname,
			Lastname:       u.Lastname,
			Email:          u.Email,
			Status:         u.Status,
			ProfilePicture: u.ProfilePicture,
			UpdatedAt:      u.UpdatedAt,
			IsHeadCoach:    u.IsHeadCoach,
			HeadCoachID:    u.HeadCoachID,
			HeadCoachName:  u.HeadCoachName,
			TeamSize:       u.TeamSize,
		})
	}
	return rows, total, nil
}

func (s *adminServiceImpl) ListCoachClients(ctx context.Context, coachID string) ([]AdminClientRow, error) {
	clientIDs, err := s.coachRepo.GetClientIDsByCoach(ctx, coachID)
	if err != nil {
		return nil, err
	}
	if len(clientIDs) == 0 {
		return []AdminClientRow{}, nil
	}

	users, err := s.userRepo.GetClientsByExternalIDs(ctx, clientIDs)
	if err != nil {
		return nil, err
	}

	rows := make([]AdminClientRow, 0, len(users))
	for _, u := range users {
		rows = append(rows, AdminClientRow{
			ExternalID:     u.ExternalID,
			Firstname:      u.Firstname,
			Lastname:       u.Lastname,
			Email:          u.Email,
			Status:         u.Status,
			ProfilePicture: u.ProfilePicture,
			UpdatedAt:      u.UpdatedAt,
		})
	}
	return rows, nil
}
