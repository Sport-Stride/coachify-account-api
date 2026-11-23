package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/handlers"
	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
)

type fakePushDevices struct {
	user, token string
	req         api.RegisterDeviceRequest
	list        []db.PushDevice
	err         *models.ApiError
}

func (f *fakePushDevices) Register(_ context.Context, userID string, req api.RegisterDeviceRequest) *models.ApiError {
	f.user, f.req = userID, req
	return f.err
}
func (f *fakePushDevices) Unregister(_ context.Context, userID, token string) *models.ApiError {
	f.user, f.token = userID, token
	return f.err
}
func (f *fakePushDevices) List(_ context.Context, userID string) ([]db.PushDevice, *models.ApiError) {
	f.user = userID
	return f.list, f.err
}

func pushRouter(svc *fakePushDevices, userID string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	auth := func(c *gin.Context) {
		if userID != "" {
			c.Set("userID", userID)
		}
	}
	r.POST("/user/devices", auth, handlers.RegisterPushDevice(svc))
	r.DELETE("/user/devices/:token", auth, handlers.UnregisterPushDevice(svc))
	r.GET("/internal/users/:id/devices", handlers.ListInternalPushDevices(svc))
	return r
}

func send(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

const pushToken = "ExponentPushToken[xxxxxxxxxxxxxxxxxxxxxx]"

func TestRegisterPushDevice(t *testing.T) {
	svc := &fakePushDevices{}
	assert.Equal(t, http.StatusUnauthorized, send(pushRouter(svc, ""), "POST", "/user/devices", `{}`).Code)
	assert.Equal(t, http.StatusBadRequest, send(pushRouter(svc, "u1"), "POST", "/user/devices", `{`).Code)

	w := send(pushRouter(svc, "u1"), "POST", "/user/devices", `{"token":"`+pushToken+`","platform":"ios","locale":"fr","user_id":"other"}`)
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "u1", svc.user, "the owner comes from the token claims")
	assert.Equal(t, api.RegisterDeviceRequest{Token: pushToken, Platform: "ios", Locale: "fr"}, svc.req)

	svc.err = apiErr(http.StatusBadRequest, "invalid platform: expected ios or android")
	w = send(pushRouter(svc, "u1"), "POST", "/user/devices", `{"token":"x","platform":"web"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.JSONEq(t, `{"error":"invalid platform: expected ios or android"}`, w.Body.String())
}

func TestUnregisterPushDevice(t *testing.T) {
	svc := &fakePushDevices{}
	assert.Equal(t, http.StatusUnauthorized, send(pushRouter(svc, ""), "DELETE", "/user/devices/"+url.PathEscape(pushToken), "").Code)

	w := send(pushRouter(svc, "u1"), "DELETE", "/user/devices/"+url.PathEscape(pushToken), "")
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "u1", svc.user)
	assert.Equal(t, pushToken, svc.token, "the escaped token reaches the service decoded")
}

func TestListInternalPushDevices(t *testing.T) {
	at := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	svc := &fakePushDevices{list: []db.PushDevice{{Token: pushToken, UserID: "u1", Platform: "android", Locale: "ar", CreatedAt: at, UpdatedAt: at}}}
	w := send(pushRouter(svc, ""), "GET", "/internal/users/u1/devices", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "u1", svc.user)
	assert.JSONEq(t, `{"devices":[{"token":"`+pushToken+`","platform":"android","locale":"ar","created_at":"2026-11-23T12:00:00Z","updated_at":"2026-11-23T12:00:00Z"}]}`, w.Body.String())
}
