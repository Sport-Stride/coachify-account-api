package router_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v4"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models/api"
	"coachify-account-api/router"
	"coachify-account-api/services"
	"coachify-account-api/services/mocks"
	"coachify-account-api/utils"
)

// Throwaway test-only values; AuthMiddleware reads them via utils.LoadConfig (sync.Once),
// so they must be set before the first call.
const (
	testSecret = "unit-test-signing-key-not-a-secret"
	testEncKey = "0123456789abcdef0123456789abcdef" // AES-256 needs 32 bytes
)

func TestMain(m *testing.M) {
	os.Setenv("COACHIFY_SECRET_KEY", testSecret)
	os.Setenv("COACHIFY_ENCRYPTION_KEY", testEncKey)
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

func makeToken(t *testing.T, key string, claims jwt.MapClaims) string {
	t.Helper()
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(key))
	require.NoError(t, err)
	enc, err := utils.Encrypt(signed, []byte(testEncKey))
	require.NoError(t, err)
	return enc
}

func validClaims(id, role string) jwt.MapClaims {
	c := jwt.MapClaims{"id": id, "exp": time.Now().Add(time.Hour).Unix()}
	if role != "" {
		c["role"] = role
	}
	return c
}

func serve(r http.Handler, method, path, authHeader, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestAuthMiddleware(t *testing.T) {
	var gotID, gotRole interface{}
	var gotRoleSet bool
	r := gin.New()
	r.Use(router.AuthMiddleware())
	r.GET("/p", func(c *gin.Context) {
		gotID, _ = c.Get("userID")
		gotRole, gotRoleSet = c.Get("userRole")
		c.Status(http.StatusNoContent)
	})

	expired := validClaims("u1", "coach")
	expired["exp"] = time.Now().Add(-time.Hour).Unix()
	noID := jwt.MapClaims{"role": "coach", "exp": time.Now().Add(time.Hour).Unix()}
	numericID := jwt.MapClaims{"id": 42, "exp": time.Now().Add(time.Hour).Unix()}

	// A token whose JWT is validly signed but never AES-encrypted by us.
	plainJWT, err := jwt.NewWithClaims(jwt.SigningMethodHS256, validClaims("u1", "coach")).SignedString([]byte(testSecret))
	require.NoError(t, err)

	for _, tc := range []struct {
		name, header string
		code         int
		errContains  string
	}{
		{"missing header", "", 401, "Authorization header missing"},
		{"not bearer", "Basic abc", 401, "Invalid authorization format"},
		{"bearer without token", "Bearer", 401, "Invalid authorization format"},
		{"too many parts", "Bearer a b", 401, "Invalid authorization format"},
		{"lowercase scheme", "bearer abc", 401, "Invalid authorization format"},
		{"garbage token", "Bearer not-base64!!", 401, "Invalid token encryption"},
		{"unencrypted jwt rejected", "Bearer " + plainJWT, 401, "Invalid token encryption"},
		{"wrong signing key", "Bearer " + makeToken(t, "another-key", validClaims("u1", "coach")), 401, "Invalid or expired token"},
		{"expired", "Bearer " + makeToken(t, testSecret, expired), 401, "Invalid or expired token"},
		{"missing id claim", "Bearer " + makeToken(t, testSecret, noID), 401, "id missing"},
		{"non-string id claim", "Bearer " + makeToken(t, testSecret, numericID), 401, "id missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotID = nil
			w := serve(r, "GET", "/p", tc.header, "")
			assert.Equal(t, tc.code, w.Code)
			assert.Contains(t, w.Body.String(), tc.errContains)
			assert.Contains(t, w.Body.String(), `"error"`)
			assert.Nil(t, gotID, "handler must not run")
		})
	}

	t.Run("alg none is rejected", func(t *testing.T) {
		unsigned, err := jwt.NewWithClaims(jwt.SigningMethodNone, validClaims("u1", "admin")).SignedString(jwt.UnsafeAllowNoneSignatureType)
		require.NoError(t, err)
		enc, err := utils.Encrypt(unsigned, []byte(testEncKey))
		require.NoError(t, err)
		w := serve(r, "GET", "/p", "Bearer "+enc, "")
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("valid token sets userID and userRole", func(t *testing.T) {
		gotID, gotRole, gotRoleSet = nil, nil, false
		w := serve(r, "GET", "/p", "Bearer "+makeToken(t, testSecret, validClaims("u1", "coach")), "")
		require.Equal(t, http.StatusNoContent, w.Code)
		assert.Equal(t, "u1", gotID)
		assert.Equal(t, "coach", gotRole)
	})

	t.Run("token without role still passes, role unset", func(t *testing.T) {
		gotID, gotRole, gotRoleSet = nil, nil, false
		w := serve(r, "GET", "/p", "Bearer "+makeToken(t, testSecret, validClaims("u2", "")), "")
		require.Equal(t, http.StatusNoContent, w.Code)
		assert.Equal(t, "u2", gotID)
		assert.False(t, gotRoleSet)
	})
}

var (
	fullOnce sync.Once
	fullEng  *gin.Engine
	fullSvcs *services.Services
	fullAuth *mocks.AuthService
	fullAdm  *mocks.AdminService
	fullCoach *mocks.CoachService
)

// fullRouter builds the real production router (InitializeRouter) once; ginprometheus
// registers global collectors and panics when registered twice in one process.
func fullRouter() *gin.Engine {
	fullOnce.Do(func() {
		fullAuth, fullAdm, fullCoach = &mocks.AuthService{}, &mocks.AdminService{}, &mocks.CoachService{}
		fullSvcs = &services.Services{
			AuthService:             fullAuth,
			CoachService:            fullCoach,
			AdminService:            fullAdm,
			TeamService:             &mocks.TeamService{},
			RegistrationLinkService: &mocks.RegistrationLinkService{},
		}
		fullEng = router.InitializeRouter(fullSvcs)
	})
	return fullEng
}

// Protected routes must reject anonymous callers with 401 through the real route table.
func TestProtectedRoutesRequireToken(t *testing.T) {
	r := fullRouter()
	for _, tc := range []struct{ method, path string }{
		{"PUT", "/user/update-user"},
		{"DELETE", "/user"},
		{"GET", "/user"},
		{"PATCH", "/user/matricule-fiscale"},
		{"DELETE", "/user/tax-registration"},
		{"GET", "/coach/clients"},
		{"GET", "/coach/client"},
		{"DELETE", "/coach/client/c1"},
		{"POST", "/coach/registration-link"},
		{"GET", "/coach/team"},
		{"GET", "/head-coach/me"},
		{"GET", "/head-coach/overview"},
		{"GET", "/head-coach/coach-ids"},
		{"GET", "/head-coach/coaches"},
		{"GET", "/head-coach/coaches/c1"},
		{"DELETE", "/head-coach/coaches/c1"},
		{"GET", "/admin/coaches"},
		{"GET", "/admin/coaches/c1/clients"},
		{"PUT", "/admin/coaches/c1/head-coach"},
		{"DELETE", "/admin/coaches/c1/head-coach"},
		{"GET", "/admin/head-coaches"},
		{"POST", "/admin/head-coaches/c1"},
		{"DELETE", "/admin/head-coaches/c1"},
		{"GET", "/admin/head-coaches/c1/coaches"},
		{"GET", "/admin/team-audit"},
		{"GET", "/admin/matricule-fiscale"},
		{"POST", "/admin/matricule-fiscale/u1/approve"},
		{"POST", "/admin/matricule-fiscale/u1/reject"},
	} {
		w := serve(r, tc.method, tc.path, "", "")
		assert.Equal(t, http.StatusUnauthorized, w.Code, "%s %s", tc.method, tc.path)
	}
}

func TestPublicRoutesNeedNoToken(t *testing.T) {
	r := fullRouter()
	assert.Equal(t, http.StatusOK, serve(r, "GET", "/health", "", "").Code)
	assert.Equal(t, http.StatusOK, serve(r, "GET", "/", "", "").Code)
	assert.Equal(t, http.StatusNotFound, serve(r, "GET", "/nope", "", "").Code)
	// public auth routes reach the handler (400 from body validation, not 401 from the middleware)
	for _, p := range []string{"/user/login", "/user/signup", "/user/confirm", "/user/refresh-token", "/user/reset-password/init"} {
		assert.Equal(t, http.StatusBadRequest, serve(r, "POST", p, "", "{").Code, p)
	}
}

func TestAdminRouteRoleEnforcementThroughRealRouter(t *testing.T) {
	r := fullRouter()
	coachTok := "Bearer " + makeToken(t, testSecret, validClaims("c1", "coach"))
	adminTok := "Bearer " + makeToken(t, testSecret, validClaims("a1", "admin"))

	assert.Equal(t, http.StatusForbidden, serve(r, "GET", "/admin/coaches", coachTok, "").Code)
	assert.Equal(t, http.StatusForbidden, serve(r, "GET", "/admin/matricule-fiscale", coachTok, "").Code)

	fullAdm.On("ListCoaches", mock.Anything, 1, 20, mock.Anything).Return([]services.AdminCoachRow{}, 0, nil).Once()
	assert.Equal(t, http.StatusOK, serve(r, "GET", "/admin/coaches", adminTok, "").Code)
}

func TestUpdateUserUsesTokenIdentityThroughRealRouter(t *testing.T) {
	r := fullRouter()
	var got api.RequestUpdateUser
	fullAuth.On("UpdateUser", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { got = args.Get(1).(api.RequestUpdateUser) }).
		Return(&api.ApiUser{}, nil).Once()

	tok := "Bearer " + makeToken(t, testSecret, validClaims("token-user", "client"))
	w := serve(r, "PUT", "/user/update-user", tok, `{"user":{"external_id":"victim","firstname":"A","lastname":"B","role":"admin"},"update_masks":["role"]}`)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "token-user", got.User.ExternalID)
}

// GET /user is admin-only; POST /user/ no longer exists.
func TestUserListAdminOnlyAndNoCreateRoute(t *testing.T) {
	r := fullRouter()
	tok := "Bearer " + makeToken(t, testSecret, validClaims("u1", "client"))
	assert.Equal(t, http.StatusForbidden, serve(r, "GET", "/user", tok, `{"page":1,"size":10}`).Code)
	assert.Equal(t, http.StatusNotFound, serve(r, "POST", "/user/", "", `{"email":"a@b.co","role":"admin"}`).Code)
}
