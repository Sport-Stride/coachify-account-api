// Hand-written in mockery style (mockery unavailable): mock of services.GymClientService.

package mocks

import (
	context "context"

	mock "github.com/stretchr/testify/mock"

	models "coachify-account-api/models"
	db "coachify-account-api/models/db"
	services "coachify-account-api/services"
)

// GymClientService is a mock type for the GymClientService type
type GymClientService struct {
	mock.Mock
}

func (_m *GymClientService) ListClients(ctx context.Context, callerID, gymID string, q services.GymClientListQuery) ([]db.GymClientRow, int, *models.ApiError) {
	ret := _m.Called(ctx, callerID, gymID, q)
	var r0 []db.GymClientRow
	if ret.Get(0) != nil {
		r0 = ret.Get(0).([]db.GymClientRow)
	}
	return r0, ret.Get(1).(int), apiErrAt(ret, 2)
}

func (_m *GymClientService) Assign(ctx context.Context, callerID, gymID, clientID, coachID string) (*services.DispatchResult, *models.ApiError) {
	ret := _m.Called(ctx, callerID, gymID, clientID, coachID)
	var r0 *services.DispatchResult
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.DispatchResult)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *GymClientService) Unassign(ctx context.Context, callerID, gymID, clientID string) (*services.DispatchResult, *models.ApiError) {
	ret := _m.Called(ctx, callerID, gymID, clientID)
	var r0 *services.DispatchResult
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.DispatchResult)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *GymClientService) Remove(ctx context.Context, callerID, gymID, clientID string) *models.ApiError {
	ret := _m.Called(ctx, callerID, gymID, clientID)
	return apiErrAt(ret, 0)
}

func (_m *GymClientService) Join(ctx context.Context, in services.JoinGymClientInput) (*db.GymClient, *models.ApiError) {
	ret := _m.Called(ctx, in)
	var r0 *db.GymClient
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*db.GymClient)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *GymClientService) Relationships(ctx context.Context, actorID, clientID string) (*services.ClientRelationships, *models.ApiError) {
	ret := _m.Called(ctx, actorID, clientID)
	var r0 *services.ClientRelationships
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.ClientRelationships)
	}
	return r0, apiErrAt(ret, 1)
}
