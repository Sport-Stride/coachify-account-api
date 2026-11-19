// Hand-written in mockery style (mockery unavailable): mock of services.GymMembersService.

package mocks

import (
	context "context"

	mock "github.com/stretchr/testify/mock"

	models "coachify-account-api/models"
	services "coachify-account-api/services"
)

// GymMembersService is a mock type for the GymMembersService type
type GymMembersService struct {
	mock.Mock
}

func (_m *GymMembersService) ListMembers(ctx context.Context, callerID, gymID, role string, page, size int) ([]services.GymMemberView, int64, *models.ApiError) {
	ret := _m.Called(ctx, callerID, gymID, role, page, size)
	var r0 []services.GymMemberView
	if ret.Get(0) != nil {
		r0 = ret.Get(0).([]services.GymMemberView)
	}
	return r0, ret.Get(1).(int64), apiErrAt(ret, 2)
}

func (_m *GymMembersService) SetSupervisor(ctx context.Context, callerID, gymID, userID, supervisorID string) (*services.GymMemberView, *models.ApiError) {
	ret := _m.Called(ctx, callerID, gymID, userID, supervisorID)
	var r0 *services.GymMemberView
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.GymMemberView)
	}
	return r0, apiErrAt(ret, 1)
}
