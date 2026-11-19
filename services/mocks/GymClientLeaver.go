// Hand-written in mockery style (mockery unavailable): mock of services.GymClientLeaver.

package mocks

import (
	context "context"

	mock "github.com/stretchr/testify/mock"

	models "coachify-account-api/models"
)

// GymClientLeaver is a mock type for the GymClientLeaver type
type GymClientLeaver struct {
	mock.Mock
}

func (_m *GymClientLeaver) LeaveGym(ctx context.Context, clientID, gymID string) *models.ApiError {
	ret := _m.Called(ctx, clientID, gymID)
	return apiErrAt(ret, 0)
}
