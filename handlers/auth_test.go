package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
)

func strp(s string) *string { return &s }

func TestLogin(t *testing.T) {
	t.Run("invalid body", func(t *testing.T) {
		s := newSvcs()
		w := do(newAPIRouter(s, "", ""), "POST", "/user/login", `not-json`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, decode(t, w), "error")
		s.auth.AssertNotCalled(t, "TryToConnect", mock.Anything, mock.Anything)
	})

	t.Run("service errors are propagated with their status", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			err  *models.ApiError
			code int
			msg  string
		}{
			{"wrong password", models.NewApiError(http.StatusUnauthorized, models.ErrIncorrectPassword), 401, "incorrect password provided"},
			{"unverified", models.NewApiError(http.StatusForbidden, models.ErrAccountNotConfirmed), 403, "account not confirmed"},
			{"blocked", models.NewApiError(http.StatusForbidden, models.ErrUserBlocked), 403, "account is blocked"},
			{"unknown user", models.NewApiError(http.StatusNotFound, models.ErrUnknownUser), 404, "unknown user"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				s := newSvcs()
				s.auth.On("TryToConnect", mock.Anything, api.LoginRequest{Email: "a@b.co", Password: "pw"}).Return(nil, tc.err)
				w := do(newAPIRouter(s, "", ""), "POST", "/user/login", `{"username":"a@b.co","password":"pw"}`)
				assert.Equal(t, tc.code, w.Code)
				assert.Equal(t, tc.msg, decode(t, w)["error"])
				s.auth.AssertExpectations(t)
			})
		}
	})

	t.Run("success returns user with tokens", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("TryToConnect", mock.Anything, mock.Anything).Return(&api.LoginResponse{
			User: api.ApiUser{ExternalID: "u1", Email: "a@b.co", Token: strp("access"), RefreshToken: strp("refresh")},
		}, nil)
		w := do(newAPIRouter(s, "", ""), "POST", "/user/login", `{"username":"a@b.co","password":"pw","autologin":true}`)
		require.Equal(t, http.StatusOK, w.Code)
		body := decode(t, w)
		assert.Equal(t, "Login successful", body["message"])
		user := body["user"].(map[string]interface{})
		assert.Equal(t, "u1", user["external_id"])
		assert.Equal(t, "access", user["token"])
		assert.Equal(t, "refresh", user["refresh_token"])
		s.auth.AssertCalled(t, "TryToConnect", mock.Anything, api.LoginRequest{Email: "a@b.co", Password: "pw", Autologin: true})
	})
}

func TestRegister(t *testing.T) {
	t.Run("invalid body", func(t *testing.T) {
		w := do(newAPIRouter(newSvcs(), "", ""), "POST", "/user/signup", `{`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, decode(t, w)["error"], "Invalid request payload")
	})

	t.Run("duplicate email", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("Register", mock.Anything, mock.Anything).Return(nil, models.NewApiError(http.StatusConflict, models.ErrEmailAlreadyExists))
		w := do(newAPIRouter(s, "", ""), "POST", "/user/signup", `{"firstname":"A","lastname":"B","email":"a@b.co","password":"x"}`)
		assert.Equal(t, http.StatusConflict, w.Code)
		assert.Equal(t, "email already exists", decode(t, w)["error"])
	})

	t.Run("weak password", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("Register", mock.Anything, mock.Anything).Return(nil, models.NewApiError(http.StatusBadRequest, models.ErrInvalidPassword))
		w := do(newAPIRouter(s, "", ""), "POST", "/user/signup", `{"email":"a@b.co","password":"x"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("success", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("Register", mock.Anything, mock.MatchedBy(func(r *api.CreateUserRequest) bool {
			return r.Email == "a@b.co" && r.Role == "client" && r.FirstName == "A"
		})).Return(&api.RegisterResponse{User: api.ApiUserResponse{ExternalID: "u1"}}, nil)
		w := do(newAPIRouter(s, "", ""), "POST", "/user/signup", `{"firstname":"A","lastname":"B","email":"a@b.co","password":"x","role":"client"}`)
		require.Equal(t, http.StatusCreated, w.Code)
		assert.Equal(t, "User registered successfully", decode(t, w)["message"])
		s.auth.AssertExpectations(t)
	})
}

func TestRegisterViaLink(t *testing.T) {
	t.Run("invalid body", func(t *testing.T) {
		w := do(newAPIRouter(newSvcs(), "", ""), "POST", "/coach/registration-link/tok/register", `{`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
	t.Run("service error", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("RegisterViaLink", mock.Anything, "tok", mock.Anything).Return(nil, models.NewApiError(http.StatusNotFound, models.ErrRegistrationLinkNotFound))
		w := do(newAPIRouter(s, "", ""), "POST", "/coach/registration-link/tok/register", `{"email":"a@b.co"}`)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Equal(t, "registration link not found", decode(t, w)["error"])
	})
	t.Run("success", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("RegisterViaLink", mock.Anything, "tok", mock.Anything).Return(&api.RegisterResponse{}, nil)
		w := do(newAPIRouter(s, "", ""), "POST", "/coach/registration-link/tok/register", `{"email":"a@b.co"}`)
		assert.Equal(t, http.StatusCreated, w.Code)
	})
}

func TestConfirmAndResend(t *testing.T) {
	t.Run("confirm invalid body", func(t *testing.T) {
		w := do(newAPIRouter(newSvcs(), "", ""), "POST", "/user/confirm", `{`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
	t.Run("confirm wrong code", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("Confirm", mock.Anything, &api.ConfirmUserRequest{Email: "a@b.co", ConfirmCode: "000"}).
			Return(models.NewApiError(http.StatusBadRequest, models.ErrInvalidConfirmationCode))
		w := do(newAPIRouter(s, "", ""), "POST", "/user/confirm", `{"email":"a@b.co","code":"000"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, "invalid confirmation code", decode(t, w)["error"])
	})
	t.Run("confirm already verified", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("Confirm", mock.Anything, mock.Anything).Return(models.NewApiError(http.StatusConflict, models.ErrUserAlreadyVerified))
		w := do(newAPIRouter(s, "", ""), "POST", "/user/confirm", `{"email":"a@b.co","code":"123"}`)
		assert.Equal(t, http.StatusConflict, w.Code)
	})
	t.Run("confirm success", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("Confirm", mock.Anything, &api.ConfirmUserRequest{Email: "a@b.co", ConfirmCode: "123"}).Return(nil)
		w := do(newAPIRouter(s, "", ""), "POST", "/user/confirm", `{"email":"a@b.co","code":"123"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		s.auth.AssertExpectations(t)
	})
	t.Run("resend requires email", func(t *testing.T) {
		w := do(newAPIRouter(newSvcs(), "", ""), "POST", "/user/resend-confirm", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
	t.Run("resend error", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("ResendConfirmEmail", mock.Anything, "a@b.co").Return(models.NewApiError(http.StatusNotFound, models.ErrUnknownUser))
		w := do(newAPIRouter(s, "", ""), "POST", "/user/resend-confirm", `{"email":"a@b.co"}`)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
	t.Run("resend success", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("ResendConfirmEmail", mock.Anything, "a@b.co").Return(nil)
		w := do(newAPIRouter(s, "", ""), "POST", "/user/resend-confirm", `{"email":"a@b.co"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, true, decode(t, w)["success"])
	})
}

func TestResetPasswordFlow(t *testing.T) {
	t.Run("init requires email", func(t *testing.T) {
		w := do(newAPIRouter(newSvcs(), "", ""), "POST", "/user/reset-password/init", `{}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
	t.Run("init service error", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("InitResetPassword", mock.Anything, mock.Anything).Return(models.NewApiError(http.StatusNotFound, models.ErrUnknownUser))
		w := do(newAPIRouter(s, "", ""), "POST", "/user/reset-password/init", `{"email":"a@b.co"}`)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
	t.Run("init success", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("InitResetPassword", mock.Anything, &api.ResetPasswordRequest{Email: "a@b.co"}).Return(nil)
		w := do(newAPIRouter(s, "", ""), "POST", "/user/reset-password/init", `{"email":"a@b.co"}`)
		assert.Equal(t, http.StatusOK, w.Code)
	})
	t.Run("verify requires email and code", func(t *testing.T) {
		w := do(newAPIRouter(newSvcs(), "", ""), "POST", "/user/reset-password/verify", `{"email":"a@b.co"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
	t.Run("verify invalid code", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("VerifyResetPasswordCode", mock.Anything, mock.Anything).Return(models.NewApiError(http.StatusBadRequest, models.ErrInvalidResetPasswordCode))
		w := do(newAPIRouter(s, "", ""), "POST", "/user/reset-password/verify", `{"email":"a@b.co","code":"1"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, "invalid reset password code", decode(t, w)["error"])
	})
	t.Run("verify success", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("VerifyResetPasswordCode", mock.Anything, mock.Anything).Return(nil)
		w := do(newAPIRouter(s, "", ""), "POST", "/user/reset-password/verify", `{"email":"a@b.co","code":"1"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, true, decode(t, w)["valid"])
	})
	t.Run("confirm requires code and new password", func(t *testing.T) {
		w := do(newAPIRouter(newSvcs(), "", ""), "POST", "/user/reset-password/confirm", `{"email":"a@b.co","code":"1"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
	t.Run("confirm weak password", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("ConfirmResetPassword", mock.Anything, mock.Anything).Return(models.NewApiError(http.StatusBadRequest, models.ErrInvalidPassword))
		w := do(newAPIRouter(s, "", ""), "POST", "/user/reset-password/confirm", `{"email":"a@b.co","code":"1","new_password":"weak"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
	t.Run("confirm success", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("ConfirmResetPassword", mock.Anything, &api.ConfirmResetPasswordRequest{Email: "a@b.co", Code: "1", NewPassword: "Str0ng!pass"}).Return(nil)
		w := do(newAPIRouter(s, "", ""), "POST", "/user/reset-password/confirm", `{"email":"a@b.co","code":"1","new_password":"Str0ng!pass"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		s.auth.AssertExpectations(t)
	})
}

func TestRefreshToken(t *testing.T) {
	t.Run("invalid body", func(t *testing.T) {
		w := do(newAPIRouter(newSvcs(), "", ""), "POST", "/user/refresh-token", `{`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
	t.Run("invalid refresh token", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("RefreshToken", mock.Anything, "a@b.co", "bad").Return("", models.NewApiError(http.StatusUnauthorized, models.ErrInvalidRefreshToken))
		w := do(newAPIRouter(s, "", ""), "POST", "/user/refresh-token", `{"email":"a@b.co","refresh_token":"bad"}`)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Equal(t, "invalid refresh token", decode(t, w)["error"])
	})
	t.Run("success", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("RefreshToken", mock.Anything, "a@b.co", "good").Return("new-access", nil)
		w := do(newAPIRouter(s, "", ""), "POST", "/user/refresh-token", `{"email":"a@b.co","refresh_token":"good"}`)
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "new-access", decode(t, w)["access_token"])
	})
}

func TestOAuth2Login(t *testing.T) {
	s := newSvcs()
	s.auth.On("GetOAuth2LoginURL", "google", mock.AnythingOfType("string")).Return("https://idp.example/auth")
	w := get(newAPIRouter(s, "", ""), "/oauth/google/login")
	assert.Equal(t, http.StatusTemporaryRedirect, w.Code)
	assert.Equal(t, "https://idp.example/auth", w.Header().Get("Location"))

	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "oauth_state" {
			cookie = c
		}
	}
	require.NotNil(t, cookie, "oauth_state cookie must be set")
	assert.NotEmpty(t, cookie.Value)
	assert.True(t, cookie.HttpOnly)
	assert.True(t, cookie.Secure)
	s.auth.AssertCalled(t, "GetOAuth2LoginURL", "google", cookie.Value)
}

func TestOAuth2ServerSideCallback(t *testing.T) {
	t.Run("invalid payload", func(t *testing.T) {
		s := newSvcs()
		w := do(newAPIRouter(s, "", ""), "POST", "/oauth/google/auth/me", `{`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, "Invalid OAuth user payload", decode(t, w)["error"])
		s.auth.AssertNotCalled(t, "HandleOAuthLogin", mock.Anything, mock.Anything, mock.Anything)
	})
	t.Run("provider rejected", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("HandleOAuthLogin", mock.Anything, "google", mock.Anything).Return(nil, models.NewApiError(http.StatusUnauthorized, models.ErrInvalidIDToken))
		w := do(newAPIRouter(s, "", ""), "POST", "/oauth/google/auth/me", `{"role":"client"}`)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Equal(t, "invalid id token", decode(t, w)["error"])
	})
	t.Run("email mismatch with invitation", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("HandleOAuthLogin", mock.Anything, "google", mock.Anything).Return(nil, models.NewApiError(http.StatusForbidden, models.ErrOAuthEmailMismatch))
		w := do(newAPIRouter(s, "", ""), "POST", "/oauth/google/auth/me", `{"invited_email":"x@y.z"}`)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})
	t.Run("success", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("HandleOAuthLogin", mock.Anything, "google", db.GoogleLoginRequest{Role: "client"}).
			Return(&api.OAuthResponse{User: api.ApiUser{ExternalID: "u1"}}, nil)
		w := do(newAPIRouter(s, "", ""), "POST", "/oauth/google/auth/me", `{"role":"client"}`)
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "User logged in successfully", decode(t, w)["message"])
	})
}

func TestUserLookups(t *testing.T) {
	t.Run("by email ok", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("GetUserByEmail", mock.Anything, "a@b.co").Return(&api.PublicUser{Email: "a@b.co"}, nil)
		w := get(newAPIRouter(s, "", ""), "/user/get-user-by-email/a@b.co")
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "a@b.co", decode(t, w)["email"])
	})
	t.Run("by email not found", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("GetUserByEmail", mock.Anything, "x@b.co").Return(nil, models.NewApiError(http.StatusNotFound, models.ErrUserNotFound))
		w := get(newAPIRouter(s, "", ""), "/user/check-email/x@b.co")
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Equal(t, "user not found", decode(t, w)["error"])
	})
	t.Run("by id and external id", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("GetUserById", mock.Anything, "i1").Return(&api.PublicUser{ID: "i1"}, nil)
		s.auth.On("GetUserByExternalId", mock.Anything, "e1").Return(nil, models.NewApiError(http.StatusNotFound, models.ErrUserNotFound))
		r := newAPIRouter(s, "", "")
		assert.Equal(t, http.StatusOK, get(r, "/user/get-user-by-id/i1").Code)
		assert.Equal(t, http.StatusNotFound, get(r, "/user/get-user/e1").Code)
	})
}

func TestGetAllUsersPag(t *testing.T) {
	t.Run("invalid body", func(t *testing.T) {
		w := do(newAPIRouter(newSvcs(), "u1", "admin"), "GET", "/user", `{`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
	t.Run("service error", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("GetAllUsersPag", mock.Anything).Return(nil, 0, models.NewApiError(http.StatusInternalServerError, models.ErrInternalError))
		w := do(newAPIRouter(s, "u1", "admin"), "GET", "/user", `{"page":1,"size":10}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Equal(t, "internal server error", decode(t, w)["error"])
	})
	t.Run("pagination math", func(t *testing.T) {
		for _, tc := range []struct {
			name              string
			page, size, total int
			wantPages         float64
		}{
			{"exact multiple", 1, 10, 30, 3},
			{"remainder rounds up", 2, 10, 31, 4},
			{"empty", 1, 10, 0, 0},
			{"size zero treated as one", 1, 0, 3, 3},
		} {
			t.Run(tc.name, func(t *testing.T) {
				s := newSvcs()
				s.auth.On("GetAllUsersPag", mock.Anything).Return([]*api.PublicUser{{ExternalID: "u1"}}, tc.total, nil)
				body := fmt.Sprintf(`{"page":%d,"size":%d}`, tc.page, tc.size)
				w := do(newAPIRouter(s, "u1", "admin"), "GET", "/user", body)
				require.Equal(t, http.StatusOK, w.Code)
				m := decode(t, w)
				assert.Equal(t, tc.wantPages, m["total_pages"])
				assert.Equal(t, float64(tc.total), m["total"])
				assert.Equal(t, float64(tc.page), m["page"])
				assert.Equal(t, float64(1), m["total_per_page"])
			})
		}
	})
}


func TestHealthAndNoRoute(t *testing.T) {
	r := newAPIRouter(newSvcs(), "", "")
	h := get(r, "/health")
	assert.Equal(t, http.StatusOK, h.Code)
	assert.Equal(t, "UP", decode(t, h)["status"])
	w := get(r, "/does-not-exist")
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "Not Found", decode(t, w)["error"])
}

func TestGetAllUsersPagRequiresAdmin(t *testing.T) {
	for _, role := range []string{"", "client", "coach"} {
		s := newSvcs()
		w := do(newAPIRouter(s, "u1", role), "GET", "/user", `{"page":1,"size":10}`)
		assert.Equal(t, http.StatusForbidden, w.Code, role)
		s.auth.AssertNotCalled(t, "GetAllUsersPag", mock.Anything)
	}
}

func TestPostUserRouteRemoved(t *testing.T) {
	w := do(newAPIRouter(newSvcs(), "", ""), "POST", "/user/", `{"email":"a@b.co","role":"admin"}`)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// Lookup and list responses must never carry credentials.
func TestPublicUserResponsesHaveNoSecrets(t *testing.T) {
	secrets := []string{"password", "token", "refresh_token", "confirm_code", "reset_password_code"}
	assertClean := func(t *testing.T, body string) {
		var raw interface{}
		require.NoError(t, json.Unmarshal([]byte(body), &raw))
		var walk func(v interface{})
		walk = func(v interface{}) {
			switch x := v.(type) {
			case map[string]interface{}:
				for k, c := range x {
					for _, sec := range secrets {
						assert.NotEqual(t, sec, k)
					}
					walk(c)
				}
			case []interface{}:
				for _, c := range x {
					walk(c)
				}
			}
		}
		walk(raw)
	}
	pu := &api.PublicUser{ID: "i1", ExternalID: "e1", Email: "a@b.co"}

	s := newSvcs()
	s.auth.On("GetUserByEmail", mock.Anything, "a@b.co").Return(pu, nil)
	s.auth.On("GetUserById", mock.Anything, "i1").Return(pu, nil)
	s.auth.On("GetUserByExternalId", mock.Anything, "e1").Return(pu, nil)
	s.auth.On("GetAllUsersPag", mock.Anything).Return([]*api.PublicUser{pu}, 1, nil)
	r := newAPIRouter(s, "u1", "admin")
	for _, p := range []string{"/user/get-user-by-email/a@b.co", "/user/check-email/a@b.co", "/user/get-user-by-id/i1", "/user/get-user/e1"} {
		w := get(r, p)
		require.Equal(t, http.StatusOK, w.Code, p)
		assertClean(t, w.Body.String())
	}
	w := do(r, "GET", "/user", `{"page":1,"size":10}`)
	require.Equal(t, http.StatusOK, w.Code)
	assertClean(t, w.Body.String())

	// Login/signup shapes must not leak the hash or one-time codes either.
	tok := "access"
	b, err := json.Marshal(api.ApiUser{Password: "hash", Token: &tok, ConfirmCode: db.UserConfirmCode{Code: "123"}, ResetPasswordCode: &db.UserResetPasswordCode{Code: "456"}})
	require.NoError(t, err)
	assert.NotContains(t, string(b), "hash")
	assert.NotContains(t, string(b), "confirm_code")
	assert.NotContains(t, string(b), "reset_password_code")
	b, err = json.Marshal(api.ApiUserResponse{Password: "hash"})
	require.NoError(t, err)
	assert.NotContains(t, string(b), "password")
}
