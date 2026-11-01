// Hand-written in mockery style (mockery unavailable): mock of services.EntitlementService.

package mocks

import (
	context "context"

	mock "github.com/stretchr/testify/mock"

	models "coachify-account-api/models"
	api "coachify-account-api/models/api"
	db "coachify-account-api/models/db"
	services "coachify-account-api/services"
)

// EntitlementService is a mock type for the EntitlementService type
type EntitlementService struct {
	mock.Mock
}

func (_m *EntitlementService) Features() []services.FeatureDef {
	ret := _m.Called()
	if ret.Get(0) != nil {
		return ret.Get(0).([]services.FeatureDef)
	}
	return nil
}

func (_m *EntitlementService) Effective(ctx context.Context, userID string) (*services.Effective, *models.ApiError) {
	ret := _m.Called(ctx, userID)
	var r0 *services.Effective
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.Effective)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *EntitlementService) Resolve(ctx context.Context, userIDs []string) (map[string]*services.Effective, *models.ApiError) {
	ret := _m.Called(ctx, userIDs)
	var r0 map[string]*services.Effective
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(map[string]*services.Effective)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *EntitlementService) Consume(ctx context.Context, userID string, req api.UsageDeltaRequest) (map[string]int64, *models.ApiError) {
	ret := _m.Called(ctx, userID, req)
	var r0 map[string]int64
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(map[string]int64)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *EntitlementService) Release(ctx context.Context, userID string, req api.UsageDeltaRequest) *models.ApiError {
	ret := _m.Called(ctx, userID, req)
	return apiErrAt(ret, 0)
}

func (_m *EntitlementService) CheckQuota(ctx context.Context, userID string, req api.CheckQuotaRequest) (*services.QuotaResult, *models.ApiError) {
	ret := _m.Called(ctx, userID, req)
	var r0 *services.QuotaResult
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.QuotaResult)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *EntitlementService) ListCoachAccess(ctx context.Context, q db.CoachAccessQuery) (*services.CoachAccessList, *models.ApiError) {
	ret := _m.Called(ctx, q)
	var r0 *services.CoachAccessList
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.CoachAccessList)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *EntitlementService) GetCoachAccess(ctx context.Context, coachID string) (*services.CoachAccessDetail, *models.ApiError) {
	ret := _m.Called(ctx, coachID)
	var r0 *services.CoachAccessDetail
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.CoachAccessDetail)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *EntitlementService) SetCoachAccess(ctx context.Context, actorID string, coachID string, req api.UpdateAccessRequest) (*services.CoachAccessDetail, *models.ApiError) {
	ret := _m.Called(ctx, actorID, coachID, req)
	var r0 *services.CoachAccessDetail
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.CoachAccessDetail)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *EntitlementService) ListPlanDefaults(ctx context.Context) ([]db.PlanDefault, *models.ApiError) {
	ret := _m.Called(ctx)
	var r0 []db.PlanDefault
	if ret.Get(0) != nil {
		r0 = ret.Get(0).([]db.PlanDefault)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *EntitlementService) SetPlanDefault(ctx context.Context, actorID string, planID string, req api.PlanDefaultRequest) (*db.PlanDefault, *models.ApiError) {
	ret := _m.Called(ctx, actorID, planID, req)
	var r0 *db.PlanDefault
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*db.PlanDefault)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *EntitlementService) ListAudit(ctx context.Context, f db.AdminAuditFilter, page int, size int) (*services.AdminAuditList, *models.ApiError) {
	ret := _m.Called(ctx, f, page, size)
	var r0 *services.AdminAuditList
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.AdminAuditList)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *EntitlementService) ListClientAccess(ctx context.Context, coachID string, page int, size int, q string) (*services.ClientAccessList, *models.ApiError) {
	ret := _m.Called(ctx, coachID, page, size, q)
	var r0 *services.ClientAccessList
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.ClientAccessList)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *EntitlementService) GetClientAccess(ctx context.Context, coachID string, clientID string) (*services.ClientAccessDetail, *models.ApiError) {
	ret := _m.Called(ctx, coachID, clientID)
	var r0 *services.ClientAccessDetail
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.ClientAccessDetail)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *EntitlementService) SetClientAccess(ctx context.Context, actorID string, actorRole string, coachID string, clientID string, req api.UpdateAccessRequest) (*services.ClientAccessDetail, *models.ApiError) {
	ret := _m.Called(ctx, actorID, actorRole, coachID, clientID, req)
	var r0 *services.ClientAccessDetail
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*services.ClientAccessDetail)
	}
	return r0, apiErrAt(ret, 1)
}
