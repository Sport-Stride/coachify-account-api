// Hand-written in mockery style (mockery unavailable): mock of services.QuotaGuard.

package mocks

import (
	context "context"

	mock "github.com/stretchr/testify/mock"

	models "coachify-account-api/models"
)

// QuotaGuard is a mock type for the QuotaGuard type
type QuotaGuard struct {
	mock.Mock
}

func (_m *QuotaGuard) Check(ctx context.Context, kind, coachID, gymID string) *models.ApiError {
	ret := _m.Called(ctx, kind, coachID, gymID)
	return apiErrAt(ret, 0)
}
