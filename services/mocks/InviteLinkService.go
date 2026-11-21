// Hand-written in mockery style (mockery unavailable): mock of services.InviteLinkService.

package mocks

import (
	context "context"

	mock "github.com/stretchr/testify/mock"

	models "coachify-account-api/models"
	db "coachify-account-api/models/db"
	services "coachify-account-api/services"
)

// InviteLinkService is a mock type for the InviteLinkService type
type InviteLinkService struct {
	mock.Mock
}

func (_m *InviteLinkService) Create(ctx context.Context, a services.Actor, req services.CreateInvite) (*services.InviteCreated, *models.ApiError) {
	ret := _m.Called(ctx, a, req)
	var r0 *services.InviteCreated
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.InviteCreated)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *InviteLinkService) List(ctx context.Context, actorID, gymID, status string) ([]services.InviteLinkView, *models.ApiError) {
	ret := _m.Called(ctx, actorID, gymID, status)
	var r0 []services.InviteLinkView
	if ret.Get(0) != nil {
		r0 = ret.Get(0).([]services.InviteLinkView)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *InviteLinkService) Revoke(ctx context.Context, actorID, id string) *models.ApiError {
	return apiErrAt(_m.Called(ctx, actorID, id), 0)
}

func (_m *InviteLinkService) Lookup(ctx context.Context, token string) (*services.InviteLookup, *models.ApiError) {
	ret := _m.Called(ctx, token)
	var r0 *services.InviteLookup
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.InviteLookup)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *InviteLinkService) Redeem(ctx context.Context, userID, token string) (*services.RedeemResult, *models.ApiError) {
	ret := _m.Called(ctx, userID, token)
	var r0 *services.RedeemResult
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.RedeemResult)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *InviteLinkService) ListMine(ctx context.Context, userID string) ([]services.MyInvite, *models.ApiError) {
	ret := _m.Called(ctx, userID)
	var r0 []services.MyInvite
	if ret.Get(0) != nil {
		r0 = ret.Get(0).([]services.MyInvite)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *InviteLinkService) Accept(ctx context.Context, userID, id string) (*services.RedeemResult, *models.ApiError) {
	ret := _m.Called(ctx, userID, id)
	var r0 *services.RedeemResult
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.RedeemResult)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *InviteLinkService) Decline(ctx context.Context, userID, id string) *models.ApiError {
	return apiErrAt(_m.Called(ctx, userID, id), 0)
}

func (_m *InviteLinkService) PrepareSignup(ctx context.Context, token, email string) (*db.InviteLink, *models.ApiError) {
	ret := _m.Called(ctx, token, email)
	var r0 *db.InviteLink
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*db.InviteLink)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *InviteLinkService) CompleteSignup(ctx context.Context, l *db.InviteLink, u *db.User) *models.ApiError {
	return apiErrAt(_m.Called(ctx, l, u), 0)
}

func (_m *InviteLinkService) AcceptAfterConfirm(ctx context.Context, userID string) {
	_m.Called(ctx, userID)
}

func (_m *InviteLinkService) AbortSignup(ctx context.Context, l *db.InviteLink) {
	_m.Called(ctx, l)
}
