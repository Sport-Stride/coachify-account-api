// Hand-written in mockery style (mockery unavailable): mock of services.RegistrationLinkService.

package mocks

import (
	context "context"

	mock "github.com/stretchr/testify/mock"

	models "coachify-account-api/models"
	db "coachify-account-api/models/db"
)

// RegistrationLinkService is a mock type for the RegistrationLinkService type
type RegistrationLinkService struct {
	mock.Mock
}

func (_m *RegistrationLinkService) GetOrCreateLink(ctx context.Context, coachID string) (*db.RegistrationLink, *models.ApiError) {
	ret := _m.Called(ctx, coachID)
	var r0 *db.RegistrationLink
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*db.RegistrationLink)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *RegistrationLinkService) ValidateToken(ctx context.Context, token string) (*db.RegistrationLink, string, *models.ApiError) {
	ret := _m.Called(ctx, token)
	var r0 *db.RegistrationLink
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*db.RegistrationLink)
	}
	return r0, ret.String(1), apiErrAt(ret, 2)
}
