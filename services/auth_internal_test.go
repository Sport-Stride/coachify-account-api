package services

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	xoauth2 "golang.org/x/oauth2"

	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
	"coachify-account-api/oauth2"
	"coachify-account-api/utils"
)

const (
	svcTestSecret = "services-test-signing-secret"
	svcTestEncKey = "0123456789abcdef0123456789abcdef"
)

// utils.LoadConfig is a sync.Once singleton: the env must be set before its first call.
func TestMain(m *testing.M) {
	os.Setenv("COACHIFY_SECRET_KEY", svcTestSecret)
	os.Setenv("COACHIFY_ENCRYPTION_KEY", svcTestEncKey)
	os.Exit(m.Run())
}

// fakeProvider is an oauth2.Provider whose ValidateToken result is scripted.
type fakeProvider struct {
	validateErr *models.ApiError
	claims      *oauth2.TokenClaims
	gotToken    string
	loginURL    string
}

func (f *fakeProvider) GetLoginURL(state string) string { return f.loginURL + "?state=" + state }
func (f *fakeProvider) ExchangeCode(context.Context, string) (*xoauth2.Token, *models.ApiError) {
	return nil, nil
}
func (f *fakeProvider) GetUserInfo(context.Context, *xoauth2.Token) (*db.OAuthUser, *models.ApiError) {
	return nil, nil
}
func (f *fakeProvider) RefreshToken(context.Context, string) (*db.OAuthProviderDetails, *models.ApiError) {
	return nil, nil
}
func (f *fakeProvider) ValidateToken(_ context.Context, idToken string) (*oauth2.TokenClaims, *models.ApiError) {
	f.gotToken = idToken
	if f.validateErr != nil {
		return nil, f.validateErr
	}
	return f.claims, nil
}

func newAuthSvcWithProviders(p map[oauth2.ProviderType]oauth2.Provider) *AuthServiceImpl {
	return &AuthServiceImpl{providers: p}
}

func sealedToken(t *testing.T, exp time.Time, secret string) string {
	t.Helper()
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"exp": exp.Unix(), "id": "u1"}).SignedString([]byte(secret))
	require.NoError(t, err)
	enc, err := utils.Encrypt(signed, []byte(svcTestEncKey))
	require.NoError(t, err)
	return enc
}

func tokenUser() *db.User {
	return &db.User{ExternalID: "u1", UserFirstname: "Ada", UserLastname: "L", UserEmail: "ada@example.com", UserRole: "coach", UserStatus: db.Active}
}

func TestGenerateTokens(t *testing.T) {
	s := &AuthServiceImpl{}
	access, refresh, apiErr := s.generateTokens(tokenUser())
	require.Nil(t, apiErr)
	assert.NotEqual(t, access, refresh)

	for _, tok := range []string{access, refresh} {
		exp, err := utils.IsTokenExpired(tok)
		require.NoError(t, err)
		assert.False(t, exp)
		claims, err := utils.GetTokenClaims(tok)
		require.NoError(t, err)
		assert.Equal(t, "coach", claims["role"])
		assert.Equal(t, "u1", claims["id"])
		assert.NotContains(t, claims, "password")
	}
}

func TestValidateAndRefreshTokens(t *testing.T) {
	s := &AuthServiceImpl{}
	future := time.Now().Add(time.Hour)
	past := time.Now().Add(-time.Hour)

	t.Run("no tokens issues both", func(t *testing.T) {
		td, apiErr := s.ValidateAndRefreshTokens(tokenUser())
		require.Nil(t, apiErr)
		assert.True(t, td.IsNewAccess)
		assert.True(t, td.IsNewRefresh)
		assert.NotEmpty(t, td.AccessToken)
		assert.NotEmpty(t, td.RefreshToken)
	})

	t.Run("valid access and refresh are reused", func(t *testing.T) {
		u := tokenUser()
		at, rt := sealedToken(t, future, svcTestSecret), sealedToken(t, future.Add(time.Hour), svcTestSecret)
		u.Token, u.UserRefreshToken = &at, &rt
		td, apiErr := s.ValidateAndRefreshTokens(u)
		require.Nil(t, apiErr)
		assert.False(t, td.IsNewAccess)
		assert.False(t, td.IsNewRefresh)
		assert.Equal(t, at, td.AccessToken)
		assert.Equal(t, rt, td.RefreshToken)
	})

	t.Run("valid access without refresh", func(t *testing.T) {
		u := tokenUser()
		at := sealedToken(t, future, svcTestSecret)
		u.Token = &at
		td, apiErr := s.ValidateAndRefreshTokens(u)
		require.Nil(t, apiErr)
		assert.False(t, td.IsNewAccess)
		assert.Empty(t, td.RefreshToken)
	})

	t.Run("expired access with valid refresh issues new access only", func(t *testing.T) {
		u := tokenUser()
		at, rt := sealedToken(t, past, svcTestSecret), sealedToken(t, future, svcTestSecret)
		u.Token, u.UserRefreshToken = &at, &rt
		td, apiErr := s.ValidateAndRefreshTokens(u)
		require.Nil(t, apiErr)
		assert.True(t, td.IsNewAccess)
		assert.False(t, td.IsNewRefresh)
		assert.NotEqual(t, at, td.AccessToken)
		assert.Equal(t, rt, td.RefreshToken)
	})

	t.Run("both expired issues both", func(t *testing.T) {
		u := tokenUser()
		at, rt := sealedToken(t, past, svcTestSecret), sealedToken(t, past, svcTestSecret)
		u.Token, u.UserRefreshToken = &at, &rt
		td, apiErr := s.ValidateAndRefreshTokens(u)
		require.Nil(t, apiErr)
		assert.True(t, td.IsNewAccess)
		assert.True(t, td.IsNewRefresh)
		assert.NotEqual(t, rt, td.RefreshToken)
	})

	t.Run("forged tokens are not trusted", func(t *testing.T) {
		u := tokenUser()
		forged := sealedToken(t, future, "attacker-secret")
		u.Token, u.UserRefreshToken = &forged, &forged
		td, apiErr := s.ValidateAndRefreshTokens(u)
		require.Nil(t, apiErr)
		assert.True(t, td.IsNewAccess)
		assert.True(t, td.IsNewRefresh)
		assert.NotEqual(t, forged, td.AccessToken)
	})

	t.Run("garbage tokens are replaced", func(t *testing.T) {
		u := tokenUser()
		g := "garbage"
		u.Token, u.UserRefreshToken = &g, &g
		td, apiErr := s.ValidateAndRefreshTokens(u)
		require.Nil(t, apiErr)
		assert.True(t, td.IsNewAccess)
		assert.True(t, td.IsNewRefresh)
	})
}

func TestGetOAuth2LoginURL(t *testing.T) {
	s := newAuthSvcWithProviders(map[oauth2.ProviderType]oauth2.Provider{
		oauth2.ProviderGoogle: &fakeProvider{loginURL: "https://accounts.example/auth"},
	})
	assert.Equal(t, "https://accounts.example/auth?state=abc", s.GetOAuth2LoginURL("google", "abc"))
	assert.Empty(t, s.GetOAuth2LoginURL("facebook", "abc"), "unconfigured provider")
	assert.Empty(t, s.GetOAuth2LoginURL("", "abc"))
	assert.Empty(t, s.GetOAuth2LoginURL("../etc/passwd", "abc"))
}

func TestHandleOAuthLogin_EarlyErrors(t *testing.T) {
	t.Run("unknown provider is 400", func(t *testing.T) {
		s := newAuthSvcWithProviders(map[oauth2.ProviderType]oauth2.Provider{})
		res, apiErr := s.HandleOAuthLogin(context.Background(), "github", db.GoogleLoginRequest{})
		assert.Nil(t, res)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code)
		assert.ErrorIs(t, apiErr.Error, models.ErrProviderNotFound)
	})

	t.Run("invalid id token is propagated and nothing else runs", func(t *testing.T) {
		fp := &fakeProvider{validateErr: &models.ApiError{Code: http.StatusUnauthorized, Error: models.ErrInvalidIDToken}}
		s := newAuthSvcWithProviders(map[oauth2.ProviderType]oauth2.Provider{oauth2.ProviderGoogle: fp})
		req := db.GoogleLoginRequest{}
		req.Account.IdToken = "forged-id-token"
		// userRepository is the zero value: reaching it would panic, proving validation gates the flow.
		res, apiErr := s.HandleOAuthLogin(context.Background(), "google", req)
		assert.Nil(t, res)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusUnauthorized, apiErr.Code)
		assert.Equal(t, "forged-id-token", fp.gotToken)
	})
}

func TestIsAdminProvisionedRoleMatrix(t *testing.T) {
	assert.True(t, isAdminProvisionedRole(db.RoleGymOwner))
	for _, r := range []string{"coach", "client", "nutritionist", "admin", ""} {
		assert.False(t, isAdminProvisionedRole(r), r)
	}
}

func TestIsCoachOrNutritionist(t *testing.T) {
	assert.True(t, isCoachOrNutritionist("coach"))
	assert.True(t, isCoachOrNutritionist("nutritionist"))
	for _, r := range []string{"client", "admin", "gym_owner", "", "Coach"} {
		assert.False(t, isCoachOrNutritionist(r), r)
	}
}

func TestConfirmResetPassword_WeakPasswordRejectedBeforeLookup(t *testing.T) {
	s := &AuthServiceImpl{}
	for _, pw := range []string{"", "short1!", "alllowercase1!", "ALLUPPERCASE1!", "NoSymbol123"} {
		apiErr := s.ConfirmResetPassword(nil, &api.ConfirmResetPasswordRequest{Email: "a@b.c", Code: "123456", NewPassword: pw})
		require.NotNil(t, apiErr, pw)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code)
		assert.ErrorIs(t, apiErr.Error, models.ErrInvalidPassword)
	}
}

func TestSubmitMatriculeFiscale_ValidationBeforeLookup(t *testing.T) {
	s := &AuthServiceImpl{}

	for _, in := range []string{"", "   ", "\t\n"} {
		apiErr := s.SubmitMatriculeFiscale(context.Background(), "u1", in)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusUnprocessableEntity, apiErr.Code)
		assert.ErrorIs(t, apiErr.Error, models.ErrMatriculeFiscaleEmpty)
	}

	apiErr := s.SubmitMatriculeFiscale(context.Background(), "u1", strings.Repeat("9", 51))
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusUnprocessableEntity, apiErr.Code)
	assert.ErrorIs(t, apiErr.Error, models.ErrMatriculeFiscaleTooLong)
}

func TestGetMatriculeFiscaleApplications_InvalidStatus(t *testing.T) {
	s := &AuthServiceImpl{}
	list, total, apiErr := s.GetMatriculeFiscaleApplications(context.Background(), "bogus", 1, 10)
	assert.Nil(t, list)
	assert.Zero(t, total)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.Code)
	assert.ErrorIs(t, apiErr.Error, models.ErrMatriculeFiscaleInvalidStatus)
}

func TestTokenHelpersDoNotLeakSecrets(t *testing.T) {
	// A mismatch error must not echo either token.
	stored := "stored-secret-token"
	apiErr := checkRefreshToken(&apiRefresh{RefreshToken: &stored, UserStatus: string(db.Active)}, "attacker-guess")
	require.NotNil(t, apiErr)
	assert.False(t, strings.Contains(apiErr.Error.Error(), stored))
	assert.False(t, strings.Contains(apiErr.Error.Error(), "attacker-guess"))
	assert.True(t, errors.Is(apiErr.Error, models.ErrInvalidRefreshToken))
}

type apiRefresh = api.RefreshToken
