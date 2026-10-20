package services

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
)

const (
	maxStatusReasonLen = 500

	auditActionStatusChanged      = "user.status_changed"
	auditActionConfirmationResent = "user.confirmation_resent"
	auditActionPasswordResetSent  = "user.password_reset_sent"
	auditTargetUser               = "user"
)

// adminSettableStatuses are the statuses an admin may assign.
var adminSettableStatuses = map[string]bool{
	string(db.Active): true, string(db.Suspended): true,
	string(db.Blocked): true, string(db.Deactivated): true,
}

var (
	errCannotChangeOwnStatus   = errors.New("cannot change own status")
	errCannotChangeAdminStatus = errors.New("cannot change status of an admin")
	errStatusUnchanged         = errors.New("status unchanged")
	errAlreadyConfirmed        = errors.New("user already confirmed")
)

// AdminUserStore is the user persistence the admin service needs.
type AdminUserStore interface {
	ListAdminUsers(ctx context.Context, q db.AdminUserQuery) ([]db.AdminUserRow, int, error)
	GetAdminUserDetail(ctx context.Context, externalID string) (*db.AdminUserDetail, error)
	AdminUserStats(ctx context.Context, days int, now time.Time) (*db.AdminUserStats, error)
	GetCoachesByPage(ctx context.Context, page, size int, f db.AdminCoachFilter) ([]db.AdminCoachListRow, int, error)
	GetClientsByExternalIDs(ctx context.Context, ids []string) ([]*db.UserResponse, error)
	UpdateStatusAndRevoke(ctx context.Context, externalID, status string) (bool, error)
}

// AdminAuditStore is the audit trail persistence the admin service needs.
type AdminAuditStore interface {
	Insert(ctx context.Context, e *db.AdminAuditEntry) error
	List(ctx context.Context, f db.AdminAuditFilter, page, size int) ([]db.AdminAuditEntry, int, error)
}

// AccountEmailSender sends the account emails an admin can trigger; AuthService satisfies it.
type AccountEmailSender interface {
	ResendConfirmEmail(ctx context.Context, email string) *models.ApiError
	InitResetPassword(ctx context.Context, request *api.ResetPasswordRequest) *models.ApiError
}

// AdminAuditList is a page of audit entries.
type AdminAuditList struct {
	Entries []db.AdminAuditEntry `json:"entries"`
	Total   int                  `json:"total"`
	Page    int                  `json:"page"`
	Size    int                  `json:"size"`
}

// recordAudit stores an entry; a failure is logged (ids only, no PII) and never fails the request.
func (s *adminServiceImpl) recordAudit(ctx context.Context, e *db.AdminAuditEntry) {
	if s.auditRepo == nil {
		return
	}
	e.TargetType = auditTargetUser
	if err := s.auditRepo.Insert(ctx, e); err != nil {
		zap.L().Error("admin audit insert failed",
			zap.String("component", "admin"),
			zap.String("action", e.Action),
			zap.String("actor_id", e.ActorID),
			zap.String("target_id", e.TargetID),
			zap.Error(err))
	}
}

// loadTarget returns the target user row or a 404 / 500 ApiError.
func (s *adminServiceImpl) loadTarget(ctx context.Context, targetID string) (*db.AdminUserDetail, *models.ApiError) {
	d, err := s.userRepo.GetAdminUserDetail(ctx, targetID)
	if err != nil {
		zap.L().Error("admin load target failed", zap.String("component", "admin"), zap.Error(err))
		return nil, internalError()
	}
	if d == nil {
		return nil, models.NewApiError(http.StatusNotFound, models.ErrUserNotFound)
	}
	return d, nil
}

func (s *adminServiceImpl) UpdateUserStatus(ctx context.Context, actorID, targetID, status, reason string) (*db.AdminUserRow, *models.ApiError) {
	reason = strings.TrimSpace(reason)
	if !adminSettableStatuses[status] {
		return nil, badRequest("invalid status")
	}
	if len([]rune(reason)) > maxStatusReasonLen {
		return nil, badRequest("reason too long")
	}
	if status != string(db.Active) && reason == "" {
		return nil, badRequest("reason is required")
	}
	if actorID == targetID {
		return nil, models.NewApiError(http.StatusForbidden, errCannotChangeOwnStatus)
	}

	target, apiErr := s.loadTarget(ctx, targetID)
	if apiErr != nil {
		return nil, apiErr
	}
	if target.Role == "admin" {
		return nil, models.NewApiError(http.StatusForbidden, errCannotChangeAdminStatus)
	}
	if target.Status == status {
		return nil, models.NewApiError(http.StatusConflict, errStatusUnchanged)
	}

	// Clearing the stored tokens only blocks refresh and forces a re-login: access tokens already
	// issued stay valid until they expire (up to 24h).
	found, err := s.userRepo.UpdateStatusAndRevoke(ctx, targetID, status)
	if err != nil {
		zap.L().Error("admin update status failed", zap.String("component", "admin"), zap.Error(err))
		return nil, internalError()
	}
	if !found {
		return nil, models.NewApiError(http.StatusNotFound, models.ErrUserNotFound)
	}

	s.recordAudit(ctx, &db.AdminAuditEntry{
		ActorID:  actorID,
		Action:   auditActionStatusChanged,
		TargetID: targetID,
		Before:   map[string]interface{}{"status": target.Status},
		After:    map[string]interface{}{"status": status},
		Reason:   reason,
	})

	row := target.AdminUserRow
	row.Status = status
	if row.LastLogin != nil && row.LastLogin.IsZero() {
		row.LastLogin = nil
	}
	return &row, nil
}

func (s *adminServiceImpl) ResendConfirmation(ctx context.Context, actorID, targetID string) *models.ApiError {
	target, apiErr := s.loadTarget(ctx, targetID)
	if apiErr != nil {
		return apiErr
	}
	if target.Status != string(db.ToConfirm) {
		return models.NewApiError(http.StatusConflict, errAlreadyConfirmed)
	}
	if apiErr := s.mailer.ResendConfirmEmail(ctx, target.Email); apiErr != nil {
		return s.mailerError("admin resend confirmation failed", apiErr)
	}
	s.recordAudit(ctx, &db.AdminAuditEntry{ActorID: actorID, Action: auditActionConfirmationResent, TargetID: targetID})
	return nil
}

func (s *adminServiceImpl) SendPasswordReset(ctx context.Context, actorID, targetID string) *models.ApiError {
	target, apiErr := s.loadTarget(ctx, targetID)
	if apiErr != nil {
		return apiErr
	}
	if apiErr := s.mailer.InitResetPassword(ctx, &api.ResetPasswordRequest{Email: target.Email}); apiErr != nil {
		return s.mailerError("admin password reset failed", apiErr)
	}
	s.recordAudit(ctx, &db.AdminAuditEntry{ActorID: actorID, Action: auditActionPasswordResetSent, TargetID: targetID})
	return nil
}

// mailerError passes 4xx through and hides 5xx details.
func (s *adminServiceImpl) mailerError(msg string, apiErr *models.ApiError) *models.ApiError {
	if apiErr.Code >= http.StatusInternalServerError {
		zap.L().Error(msg, zap.String("component", "admin"), zap.Error(apiErr.Error))
		return internalError()
	}
	return apiErr
}

func (s *adminServiceImpl) ListAudit(ctx context.Context, f db.AdminAuditFilter, page, size int) (*AdminAuditList, *models.ApiError) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	entries, total, err := s.auditRepo.List(ctx, f, page, size)
	if err != nil {
		zap.L().Error("admin list audit failed", zap.String("component", "admin"), zap.Error(err))
		return nil, internalError()
	}
	return &AdminAuditList{Entries: entries, Total: total, Page: page, Size: size}, nil
}
