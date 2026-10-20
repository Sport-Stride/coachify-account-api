// Hand-written in mockery style (mockery unavailable): mock of services.TeamService.

package mocks

import (
	context "context"

	mock "github.com/stretchr/testify/mock"

	models "coachify-account-api/models"
	db "coachify-account-api/models/db"
	services "coachify-account-api/services"
)

// TeamService is a mock type for the TeamService type
type TeamService struct {
	mock.Mock
}

func apiErrAt(ret mock.Arguments, i int) *models.ApiError {
	if ret.Get(i) != nil {
		return ret.Get(i).(*models.ApiError)
	}
	return nil
}

func (_m *TeamService) ListHeadCoaches(ctx context.Context, page int, size int, q string) ([]db.HeadCoachRow, int, *models.ApiError) {
	ret := _m.Called(ctx, page, size, q)
	var r0 []db.HeadCoachRow
	if ret.Get(0) != nil {
		r0 = ret.Get(0).([]db.HeadCoachRow)
	}
	return r0, ret.Get(1).(int), apiErrAt(ret, 2)
}

func (_m *TeamService) Promote(ctx context.Context, adminID string, coachID string) (*db.HeadCoachRow, *models.ApiError) {
	ret := _m.Called(ctx, adminID, coachID)
	var r0 *db.HeadCoachRow
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*db.HeadCoachRow)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *TeamService) Demote(ctx context.Context, adminID string, headCoachID string) (int, *models.ApiError) {
	ret := _m.Called(ctx, adminID, headCoachID)
	return ret.Get(0).(int), apiErrAt(ret, 1)
}

func (_m *TeamService) ListTeam(ctx context.Context, headCoachID string, q db.TeamListQuery) ([]db.TeamCoachRow, int, *models.ApiError) {
	ret := _m.Called(ctx, headCoachID, q)
	var r0 []db.TeamCoachRow
	if ret.Get(0) != nil {
		r0 = ret.Get(0).([]db.TeamCoachRow)
	}
	return r0, ret.Get(1).(int), apiErrAt(ret, 2)
}

func (_m *TeamService) Assign(ctx context.Context, adminID string, coachID string, headCoachID string) (*db.TeamMember, *models.ApiError) {
	ret := _m.Called(ctx, adminID, coachID, headCoachID)
	var r0 *db.TeamMember
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*db.TeamMember)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *TeamService) Unassign(ctx context.Context, adminID string, coachID string) *models.ApiError {
	return apiErrAt(_m.Called(ctx, adminID, coachID), 0)
}

func (_m *TeamService) ListAudit(ctx context.Context, headCoachID string, coachID string, page int, size int) ([]db.TeamAuditEntry, int, *models.ApiError) {
	ret := _m.Called(ctx, headCoachID, coachID, page, size)
	var r0 []db.TeamAuditEntry
	if ret.Get(0) != nil {
		r0 = ret.Get(0).([]db.TeamAuditEntry)
	}
	return r0, ret.Get(1).(int), apiErrAt(ret, 2)
}

func (_m *TeamService) Me(ctx context.Context, callerID string) (*services.HeadCoachMe, *models.ApiError) {
	ret := _m.Called(ctx, callerID)
	var r0 *services.HeadCoachMe
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.HeadCoachMe)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *TeamService) Overview(ctx context.Context, callerID string) (*db.TeamOverview, *models.ApiError) {
	ret := _m.Called(ctx, callerID)
	var r0 *db.TeamOverview
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*db.TeamOverview)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *TeamService) ListMyCoaches(ctx context.Context, callerID string, q db.TeamListQuery) ([]db.TeamCoachRow, int, *models.ApiError) {
	ret := _m.Called(ctx, callerID, q)
	var r0 []db.TeamCoachRow
	if ret.Get(0) != nil {
		r0 = ret.Get(0).([]db.TeamCoachRow)
	}
	return r0, ret.Get(1).(int), apiErrAt(ret, 2)
}

func (_m *TeamService) GetMyCoach(ctx context.Context, callerID string, coachID string) (*db.TeamCoachRow, *models.ApiError) {
	ret := _m.Called(ctx, callerID, coachID)
	var r0 *db.TeamCoachRow
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*db.TeamCoachRow)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *TeamService) RemoveMyCoach(ctx context.Context, callerID string, coachID string) *models.ApiError {
	return apiErrAt(_m.Called(ctx, callerID, coachID), 0)
}

func (_m *TeamService) MyCoachIDs(ctx context.Context, callerID string) ([]string, *models.ApiError) {
	ret := _m.Called(ctx, callerID)
	var r0 []string
	if ret.Get(0) != nil {
		r0 = ret.Get(0).([]string)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *TeamService) MyHeadCoach(ctx context.Context, coachID string) (*services.TeamHeadInfo, *models.ApiError) {
	ret := _m.Called(ctx, coachID)
	var r0 *services.TeamHeadInfo
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.TeamHeadInfo)
	}
	return r0, apiErrAt(ret, 1)
}
