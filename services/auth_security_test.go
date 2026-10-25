package services

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
	"coachify-account-api/oauth2"
)

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

func TestGenericSignupResponse_LeaksNothingAboutExistingUser(t *testing.T) {
	out := jsonOf(t, genericSignupResponse(&api.CreateUserRequest{Email: "victim@example.com", FirstName: "Mallory"}))
	var m struct {
		User map[string]any `json:"user"`
		Auth string         `json:"auth_token"`
		Ref  string         `json:"reresh_token"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &m))
	assert.Equal(t, "victim@example.com", m.User["email"])
	assert.Empty(t, m.Auth)
	assert.Empty(t, m.Ref)
	assert.Nil(t, m.User["token"])
	assert.Nil(t, m.User["refresh_token"])
	assert.Empty(t, m.User["id"])
	assert.Empty(t, m.User["external_id"])
	assert.NotContains(t, m.User, "password")
}

func TestSignupResponse_StripsTokensAndHash(t *testing.T) {
	tok, rt := "access-secret", "refresh-secret"
	u := &db.UserResponse{Email: "a@b.c", Password: "$2a$hash", Token: &tok, RefreshToken: &rt}
	out := jsonOf(t, signupResponse(u))
	assert.Contains(t, out, "a@b.c")
	for _, secret := range []string{tok, rt, "$2a$hash"} {
		assert.NotContains(t, out, secret)
	}
}

func TestApplyVerifiedClaims(t *testing.T) {
	t.Run("token identity overrides body identity", func(t *testing.T) {
		req := &db.GoogleLoginRequest{Profile: db.GoogleProfile{Email: "victim@example.com", Sub: "evil-sub", EmailVerified: true}}
		apiErr := applyVerifiedClaims(req, &oauth2.TokenClaims{Email: "me@example.com", Subject: "real-sub", EmailVerified: true, GivenName: "Me"})
		require.Nil(t, apiErr)
		assert.Equal(t, "me@example.com", req.Profile.Email)
		assert.Equal(t, "real-sub", req.Profile.Sub)
		assert.Equal(t, "Me", req.Profile.GivenName)
	})
	t.Run("unverified email rejected even if body claims verified", func(t *testing.T) {
		req := &db.GoogleLoginRequest{Profile: db.GoogleProfile{Email: "me@example.com", EmailVerified: true}}
		apiErr := applyVerifiedClaims(req, &oauth2.TokenClaims{Email: "me@example.com", Subject: "s", EmailVerified: false})
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusForbidden, apiErr.Code)
	})
	t.Run("missing email or subject rejected", func(t *testing.T) {
		for _, c := range []*oauth2.TokenClaims{nil, {Subject: "s", EmailVerified: true}, {Email: "a@b.c", EmailVerified: true}} {
			apiErr := applyVerifiedClaims(&db.GoogleLoginRequest{}, c)
			require.NotNil(t, apiErr)
			assert.Equal(t, http.StatusUnauthorized, apiErr.Code)
		}
	})
}

func TestHandleOAuthLogin_RejectsUnverifiedTokenEmail(t *testing.T) {
	p := &fakeProvider{claims: &oauth2.TokenClaims{Email: "me@example.com", Subject: "s", EmailVerified: false}}
	s := newAuthSvcWithProviders(map[oauth2.ProviderType]oauth2.Provider{oauth2.ProviderGoogle: p})
	req := db.GoogleLoginRequest{Account: db.GoogleAccount{IdToken: "tok"}, Profile: db.GoogleProfile{Email: "victim@example.com", EmailVerified: true}}
	resp, apiErr := s.HandleOAuthLogin(context.Background(), "google", req)
	assert.Nil(t, resp)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusForbidden, apiErr.Code)
}

func TestAuthorizeUpdateMasks_DropsEmailAndVerification(t *testing.T) {
	current := &db.User{UserRole: "coach", UserStatus: db.Active}
	allowed, apiErr := authorizeUpdateMasks(current, updateReq("coach", "email", "verification_status", "firstname"), false)
	require.Nil(t, apiErr)
	assert.Equal(t, []string{"firstname"}, allowed)
}

func TestApplyVerifiedClaims_NormalizesEmail(t *testing.T) {
	req := &db.GoogleLoginRequest{}
	require.Nil(t, applyVerifiedClaims(req, &oauth2.TokenClaims{Email: "  Me@Example.COM ", Subject: "s", EmailVerified: true}))
	assert.Equal(t, "me@example.com", req.Profile.Email)
}
