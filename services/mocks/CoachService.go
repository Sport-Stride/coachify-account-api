// Hand-written in mockery style (mockery unavailable): mock of services.CoachService.

package mocks

import (
	context "context"

	mock "github.com/stretchr/testify/mock"

	db "coachify-account-api/models/db"
)

// CoachService is a mock type for the CoachService type
type CoachService struct {
	mock.Mock
}

func (_m *CoachService) ListCoachClients(ctx context.Context, query db.CoachClientListQuery) ([]map[string]interface{}, int, error) {
	ret := _m.Called(ctx, query)
	var r0 []map[string]interface{}
	if ret.Get(0) != nil {
		r0 = ret.Get(0).([]map[string]interface{})
	}
	return r0, ret.Int(1), ret.Error(2)
}

func (_m *CoachService) ListCoachClientsSummary(ctx context.Context, coachID string, page int, size int) ([]db.CoachClientSummary, int, error) {
	ret := _m.Called(ctx, coachID, page, size)
	var r0 []db.CoachClientSummary
	if ret.Get(0) != nil {
		r0 = ret.Get(0).([]db.CoachClientSummary)
	}
	return r0, ret.Int(1), ret.Error(2)
}

func (_m *CoachService) DissociateCoachClient(ctx context.Context, coachID string, clientID string) error {
	return _m.Called(ctx, coachID, clientID).Error(0)
}

func (_m *CoachService) AddCoachClient(ctx context.Context, coachID string, clientID string) error {
	return _m.Called(ctx, coachID, clientID).Error(0)
}

func (_m *CoachService) GetCoachIDByClientID(ctx context.Context, clientID string) (string, error) {
	ret := _m.Called(ctx, clientID)
	return ret.String(0), ret.Error(1)
}

func (_m *CoachService) IsClientOfCoach(ctx context.Context, coachID string, clientID string) (bool, error) {
	ret := _m.Called(ctx, coachID, clientID)
	return ret.Bool(0), ret.Error(1)
}
