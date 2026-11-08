// Hand-written in mockery style (mockery unavailable): mock of services.GymOwnerInviteService.

package mocks

import (
	context "context"

	mock "github.com/stretchr/testify/mock"

	models "coachify-account-api/models"
	db "coachify-account-api/models/db"
	services "coachify-account-api/services"
)

// GymOwnerInviteService is a mock type for the GymOwnerInviteService type
type GymOwnerInviteService struct {
	mock.Mock
}

func (_m *GymOwnerInviteService) Create(ctx context.Context, adminID, firstname, lastname, email string) (*services.GymOwnerInviteView, *models.ApiError) {
	ret := _m.Called(ctx, adminID, firstname, lastname, email)
	var r0 *services.GymOwnerInviteView
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.GymOwnerInviteView)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *GymOwnerInviteService) List(ctx context.Context, status string, page, size int) (*services.GymOwnerInviteList, *models.ApiError) {
	ret := _m.Called(ctx, status, page, size)
	var r0 *services.GymOwnerInviteList
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.GymOwnerInviteList)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *GymOwnerInviteService) Revoke(ctx context.Context, adminID, id string) *models.ApiError {
	ret := _m.Called(ctx, adminID, id)
	return apiErrAt(ret, 0)
}

func (_m *GymOwnerInviteService) Resend(ctx context.Context, adminID, id string) (*services.GymOwnerInviteView, *models.ApiError) {
	ret := _m.Called(ctx, adminID, id)
	var r0 *services.GymOwnerInviteView
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.GymOwnerInviteView)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *GymOwnerInviteService) Lookup(ctx context.Context, rawToken string) (*services.GymOwnerInviteLookup, *models.ApiError) {
	ret := _m.Called(ctx, rawToken)
	var r0 *services.GymOwnerInviteLookup
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.GymOwnerInviteLookup)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *GymOwnerInviteService) Redeem(ctx context.Context, rawToken, email string) (*db.GymOwnerInvite, *models.ApiError) {
	ret := _m.Called(ctx, rawToken, email)
	var r0 *db.GymOwnerInvite
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*db.GymOwnerInvite)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *GymOwnerInviteService) MarkAccepted(ctx context.Context, id, userID string) *models.ApiError {
	ret := _m.Called(ctx, id, userID)
	return apiErrAt(ret, 0)
}
