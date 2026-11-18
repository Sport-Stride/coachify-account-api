// Hand-written in mockery style (mockery unavailable): mock of services.GymPolicyService.

package mocks

import (
	context "context"

	mock "github.com/stretchr/testify/mock"

	models "coachify-account-api/models"
	services "coachify-account-api/services"
)

// GymPolicyService is a mock type for the GymPolicyService type
type GymPolicyService struct {
	mock.Mock
}

func (_m *GymPolicyService) GetRolePolicy(ctx context.Context, callerID, gymID, role string) (*services.RolePolicyView, *models.ApiError) {
	ret := _m.Called(ctx, callerID, gymID, role)
	var r0 *services.RolePolicyView
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.RolePolicyView)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *GymPolicyService) PutRolePolicy(ctx context.Context, callerID, gymID, role string, caps map[string]bool, version int64) (*services.RolePolicyView, *models.ApiError) {
	ret := _m.Called(ctx, callerID, gymID, role, caps, version)
	var r0 *services.RolePolicyView
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.RolePolicyView)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *GymPolicyService) GetGrants(ctx context.Context, callerID, gymID, userID string) (*services.GrantsView, *models.ApiError) {
	ret := _m.Called(ctx, callerID, gymID, userID)
	var r0 *services.GrantsView
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.GrantsView)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *GymPolicyService) PutGrants(ctx context.Context, callerID, gymID, userID string, grants []services.GrantInput) (*services.GrantsView, *models.ApiError) {
	ret := _m.Called(ctx, callerID, gymID, userID, grants)
	var r0 *services.GrantsView
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.GrantsView)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *GymPolicyService) GymContext(ctx context.Context, userID string) (*services.GymContextView, *models.ApiError) {
	ret := _m.Called(ctx, userID)
	var r0 *services.GymContextView
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.GymContextView)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *GymPolicyService) Check(ctx context.Context, actor services.Actor, checks []services.AuthzCheck) (*services.AuthzCheckResult, *models.ApiError) {
	ret := _m.Called(ctx, actor, checks)
	var r0 *services.AuthzCheckResult
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.AuthzCheckResult)
	}
	return r0, apiErrAt(ret, 1)
}
