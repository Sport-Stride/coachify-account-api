// Hand-written in mockery style (mockery unavailable): mocks of services.TeamStore and services.TeamUserStore.

package mocks

import (
	context "context"

	mock "github.com/stretchr/testify/mock"

	models "coachify-account-api/models"
	db "coachify-account-api/models/db"
)

// TeamStore is a mock type for the TeamStore type
type TeamStore struct {
	mock.Mock
}

func (_m *TeamStore) Upsert(ctx context.Context, m *db.TeamMember) (*db.TeamMember, error) {
	ret := _m.Called(ctx, m)
	var r0 *db.TeamMember
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*db.TeamMember)
	}
	return r0, ret.Error(1)
}

func (_m *TeamStore) DeleteByCoach(ctx context.Context, coachID string) (int64, error) {
	ret := _m.Called(ctx, coachID)
	return ret.Get(0).(int64), ret.Error(1)
}

func (_m *TeamStore) DeleteByHead(ctx context.Context, headID string) (int64, error) {
	ret := _m.Called(ctx, headID)
	return ret.Get(0).(int64), ret.Error(1)
}

func (_m *TeamStore) GetHeadOf(ctx context.Context, coachID string) (*db.TeamMember, error) {
	ret := _m.Called(ctx, coachID)
	var r0 *db.TeamMember
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*db.TeamMember)
	}
	return r0, ret.Error(1)
}

func (_m *TeamStore) IsMember(ctx context.Context, coachID string) (bool, error) {
	ret := _m.Called(ctx, coachID)
	return ret.Get(0).(bool), ret.Error(1)
}

func (_m *TeamStore) CountByHead(ctx context.Context, headID string) (int, error) {
	ret := _m.Called(ctx, headID)
	return ret.Get(0).(int), ret.Error(1)
}

func (_m *TeamStore) ListByHead(ctx context.Context, headID string, q db.TeamListQuery) ([]db.TeamCoachRow, int, error) {
	ret := _m.Called(ctx, headID, q)
	var r0 []db.TeamCoachRow
	if ret.Get(0) != nil {
		r0 = ret.Get(0).([]db.TeamCoachRow)
	}
	return r0, ret.Get(1).(int), ret.Error(2)
}

func (_m *TeamStore) GetRow(ctx context.Context, headID string, coachID string) (*db.TeamCoachRow, error) {
	ret := _m.Called(ctx, headID, coachID)
	var r0 *db.TeamCoachRow
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*db.TeamCoachRow)
	}
	return r0, ret.Error(1)
}

func (_m *TeamStore) Overview(ctx context.Context, headID string) (*db.TeamOverview, error) {
	ret := _m.Called(ctx, headID)
	var r0 *db.TeamOverview
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*db.TeamOverview)
	}
	return r0, ret.Error(1)
}

func (_m *TeamStore) IDsByHead(ctx context.Context, headID string) ([]string, error) {
	ret := _m.Called(ctx, headID)
	var r0 []string
	if ret.Get(0) != nil {
		r0 = ret.Get(0).([]string)
	}
	return r0, ret.Error(1)
}

func (_m *TeamStore) InsertAudit(ctx context.Context, e *db.TeamAuditEntry) error {
	return _m.Called(ctx, e).Error(0)
}

func (_m *TeamStore) InsertAuditMany(ctx context.Context, entries []*db.TeamAuditEntry) error {
	return _m.Called(ctx, entries).Error(0)
}

func (_m *TeamStore) ListAudit(ctx context.Context, headCoachID string, coachID string, page int, size int) ([]db.TeamAuditEntry, int, error) {
	ret := _m.Called(ctx, headCoachID, coachID, page, size)
	var r0 []db.TeamAuditEntry
	if ret.Get(0) != nil {
		r0 = ret.Get(0).([]db.TeamAuditEntry)
	}
	return r0, ret.Get(1).(int), ret.Error(2)
}

// TeamUserStore is a mock type for the TeamUserStore type
type TeamUserStore struct {
	mock.Mock
}

func (_m *TeamUserStore) GetUserByExternalIdUpdate(ctx context.Context, userID string) (*db.User, *models.ApiError) {
	ret := _m.Called(ctx, userID)
	var r0 *db.User
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*db.User)
	}
	var r1 *models.ApiError
	if ret.Get(1) != nil {
		r1 = ret.Get(1).(*models.ApiError)
	}
	return r0, r1
}

func (_m *TeamUserStore) SetHeadCoach(ctx context.Context, externalID string, flag bool, promotedBy string) error {
	return _m.Called(ctx, externalID, flag, promotedBy).Error(0)
}

func (_m *TeamUserStore) ListHeadCoaches(ctx context.Context, page int, size int, q string) ([]db.HeadCoachRow, int, error) {
	ret := _m.Called(ctx, page, size, q)
	var r0 []db.HeadCoachRow
	if ret.Get(0) != nil {
		r0 = ret.Get(0).([]db.HeadCoachRow)
	}
	return r0, ret.Get(1).(int), ret.Error(2)
}
