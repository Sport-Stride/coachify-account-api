// Hand-written in mockery style (mockery unavailable): mock of services.AuthService.

package mocks

import (
	context "context"

	gin "github.com/gin-gonic/gin"
	mock "github.com/stretchr/testify/mock"

	models "coachify-account-api/models"
	api "coachify-account-api/models/api"
	db "coachify-account-api/models/db"
)

// AuthService is a mock type for the AuthService type
type AuthService struct {
	mock.Mock
}

func (_m *AuthService) GetUserById(ctx context.Context, userId string) (*api.PublicUser, *models.ApiError) {
	ret := _m.Called(ctx, userId)
	var r0 *api.PublicUser
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*api.PublicUser)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *AuthService) GetUserByEmail(ctx context.Context, userEmail string) (*api.PublicUser, *models.ApiError) {
	ret := _m.Called(ctx, userEmail)
	var r0 *api.PublicUser
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*api.PublicUser)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *AuthService) CheckEmail(ctx context.Context, userEmail string) (*api.PublicUser, *models.ApiError) {
	ret := _m.Called(ctx, userEmail)
	var r0 *api.PublicUser
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*api.PublicUser)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *AuthService) Register(ctx context.Context, req *api.CreateUserRequest) (*api.RegisterResponse, *models.ApiError) {
	ret := _m.Called(ctx, req)
	var r0 *api.RegisterResponse
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*api.RegisterResponse)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *AuthService) Confirm(ctx context.Context, req *api.ConfirmUserRequest) *models.ApiError {
	return apiErrAt(_m.Called(ctx, req), 0)
}

func (_m *AuthService) ResendConfirmEmail(ctx context.Context, email string) *models.ApiError {
	return apiErrAt(_m.Called(ctx, email), 0)
}

func (_m *AuthService) TryToConnect(ctx context.Context, request api.LoginRequest) (*api.LoginResponse, *models.ApiError) {
	ret := _m.Called(ctx, request)
	var r0 *api.LoginResponse
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*api.LoginResponse)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *AuthService) InitResetPassword(ctx context.Context, request *api.ResetPasswordRequest) *models.ApiError {
	return apiErrAt(_m.Called(ctx, request), 0)
}

func (_m *AuthService) VerifyResetPasswordCode(ctx context.Context, request *api.VerifyResetPasswordCodeRequest) *models.ApiError {
	return apiErrAt(_m.Called(ctx, request), 0)
}

func (_m *AuthService) ConfirmResetPassword(ctx *gin.Context, request *api.ConfirmResetPasswordRequest) *models.ApiError {
	return apiErrAt(_m.Called(ctx, request), 0)
}

func (_m *AuthService) GetUserByExternalId(ctx context.Context, userId string) (*api.PublicUser, *models.ApiError) {
	ret := _m.Called(ctx, userId)
	var r0 *api.PublicUser
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*api.PublicUser)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *AuthService) RefreshToken(ctx context.Context, username string, oldRefreshToken string) (string, *models.ApiError) {
	ret := _m.Called(ctx, username, oldRefreshToken)
	return ret.String(0), apiErrAt(ret, 1)
}

func (_m *AuthService) UpdateUser(ctx context.Context, req api.RequestUpdateUser) (*api.ApiUser, *models.ApiError) {
	ret := _m.Called(ctx, req)
	var r0 *api.ApiUser
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*api.ApiUser)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *AuthService) GetAllUsersPag(_a0 api.SearchUser) ([]*api.PublicUser, int, *models.ApiError) {
	ret := _m.Called(_a0)
	var r0 []*api.PublicUser
	if ret.Get(0) != nil {
		r0 = ret.Get(0).([]*api.PublicUser)
	}
	return r0, ret.Int(1), apiErrAt(ret, 2)
}

func (_m *AuthService) DeleteUser(ctx *gin.Context, id string) *models.ApiError {
	return apiErrAt(_m.Called(ctx, id), 0)
}


func (_m *AuthService) GetOAuth2LoginURL(providerType string, state string) string {
	return _m.Called(providerType, state).String(0)
}

func (_m *AuthService) HandleOAuthLogin(ctx context.Context, providerType string, oauth db.GoogleLoginRequest) (*api.OAuthResponse, *models.ApiError) {
	ret := _m.Called(ctx, providerType, oauth)
	var r0 *api.OAuthResponse
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*api.OAuthResponse)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *AuthService) RegisterViaLink(ctx context.Context, token string, req *api.CreateUserRequest) (*api.RegisterResponse, *models.ApiError) {
	ret := _m.Called(ctx, token, req)
	var r0 *api.RegisterResponse
	if ret.Get(0) != nil {
		r0 = ret.Get(0).(*api.RegisterResponse)
	}
	return r0, apiErrAt(ret, 1)
}

func (_m *AuthService) SubmitMatriculeFiscale(ctx context.Context, externalID string, matricule string) *models.ApiError {
	return apiErrAt(_m.Called(ctx, externalID, matricule), 0)
}

func (_m *AuthService) WithdrawTaxRegistration(ctx context.Context, externalID string) *models.ApiError {
	return apiErrAt(_m.Called(ctx, externalID), 0)
}

func (_m *AuthService) GetMatriculeFiscaleApplications(ctx context.Context, status string, page int, limit int) ([]api.MatriculeFiscaleApplication, int, *models.ApiError) {
	ret := _m.Called(ctx, status, page, limit)
	var r0 []api.MatriculeFiscaleApplication
	if ret.Get(0) != nil {
		r0 = ret.Get(0).([]api.MatriculeFiscaleApplication)
	}
	return r0, ret.Int(1), apiErrAt(ret, 2)
}

func (_m *AuthService) ApproveMatriculeFiscale(ctx context.Context, targetExternalID string, adminExternalID string) *models.ApiError {
	return apiErrAt(_m.Called(ctx, targetExternalID, adminExternalID), 0)
}

func (_m *AuthService) RejectMatriculeFiscale(ctx context.Context, targetExternalID string, adminExternalID string) *models.ApiError {
	return apiErrAt(_m.Called(ctx, targetExternalID, adminExternalID), 0)
}
