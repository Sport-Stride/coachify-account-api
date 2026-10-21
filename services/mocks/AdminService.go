// Hand-written in mockery style (mockery unavailable): mock of services.AdminService.

package mocks

import (
	context "context"

	mock "github.com/stretchr/testify/mock"

	models "coachify-account-api/models"
	db "coachify-account-api/models/db"
	services "coachify-account-api/services"
)

// AdminService is a mock type for the AdminService type
type AdminService struct {
	mock.Mock
}

func (_m *AdminService) ListCoaches(ctx context.Context, page int, size int, filter db.AdminCoachFilter) ([]services.AdminCoachRow, int, error) {
	ret := _m.Called(ctx, page, size, filter)
	var r0 []services.AdminCoachRow
	if ret.Get(0) != nil {
		r0 = ret.Get(0).([]services.AdminCoachRow)
	}
	return r0, ret.Int(1), ret.Error(2)
}

func (_m *AdminService) ListCoachClients(ctx context.Context, coachID string) ([]services.AdminClientRow, error) {
	ret := _m.Called(ctx, coachID)
	var r0 []services.AdminClientRow
	if ret.Get(0) != nil {
		r0 = ret.Get(0).([]services.AdminClientRow)
	}
	return r0, ret.Error(1)
}

func (_m *AdminService) ListUsers(ctx context.Context, q db.AdminUserQuery) (*services.AdminUserList, *models.ApiError) {
	ret := _m.Called(ctx, q)
	var r0 *services.AdminUserList
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.AdminUserList)
	}
	var r1 *models.ApiError
	if ret.Get(1) != nil {
		r1 = ret.Get(1).(*models.ApiError)
	}
	return r0, r1
}

func (_m *AdminService) GetUser(ctx context.Context, externalID string) (*db.AdminUserDetail, *models.ApiError) {
	ret := _m.Called(ctx, externalID)
	var r0 *db.AdminUserDetail
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*db.AdminUserDetail)
	}
	var r1 *models.ApiError
	if ret.Get(1) != nil {
		r1 = ret.Get(1).(*models.ApiError)
	}
	return r0, r1
}

func (_m *AdminService) UserStats(ctx context.Context, days int) (*db.AdminUserStats, *models.ApiError) {
	ret := _m.Called(ctx, days)
	var r0 *db.AdminUserStats
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*db.AdminUserStats)
	}
	var r1 *models.ApiError
	if ret.Get(1) != nil {
		r1 = ret.Get(1).(*models.ApiError)
	}
	return r0, r1
}

func (_m *AdminService) UpdateUserStatus(ctx context.Context, actorID string, targetID string, status string, reason string) (*db.AdminUserRow, *models.ApiError) {
	ret := _m.Called(ctx, actorID, targetID, status, reason)
	var r0 *db.AdminUserRow
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*db.AdminUserRow)
	}
	var r1 *models.ApiError
	if ret.Get(1) != nil {
		r1 = ret.Get(1).(*models.ApiError)
	}
	return r0, r1
}

func (_m *AdminService) ResendConfirmation(ctx context.Context, actorID string, targetID string) *models.ApiError {
	ret := _m.Called(ctx, actorID, targetID)
	if ret.Get(0) != nil {
		return ret.Get(0).(*models.ApiError)
	}
	return nil
}

func (_m *AdminService) SendPasswordReset(ctx context.Context, actorID string, targetID string) *models.ApiError {
	ret := _m.Called(ctx, actorID, targetID)
	if ret.Get(0) != nil {
		return ret.Get(0).(*models.ApiError)
	}
	return nil
}

func (_m *AdminService) CreateGymOwner(ctx context.Context, actorID string, firstname string, lastname string, email string) (*db.AdminUserRow, *models.ApiError) {
	ret := _m.Called(ctx, actorID, firstname, lastname, email)
	var r0 *db.AdminUserRow
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*db.AdminUserRow)
	}
	var r1 *models.ApiError
	if ret.Get(1) != nil {
		r1 = ret.Get(1).(*models.ApiError)
	}
	return r0, r1
}

func (_m *AdminService) ListAudit(ctx context.Context, f db.AdminAuditFilter, page int, size int) (*services.AdminAuditList, *models.ApiError) {
	ret := _m.Called(ctx, f, page, size)
	var r0 *services.AdminAuditList
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.AdminAuditList)
	}
	var r1 *models.ApiError
	if ret.Get(1) != nil {
		r1 = ret.Get(1).(*models.ApiError)
	}
	return r0, r1
}
