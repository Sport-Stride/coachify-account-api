// Hand-written in mockery style (mockery unavailable): mock of services.AdminService.

package mocks

import (
	context "context"

	mock "github.com/stretchr/testify/mock"

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
