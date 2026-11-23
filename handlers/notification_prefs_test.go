package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/handlers"
	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
)

type fakePrefsService struct {
	user string
	req  api.UpdateNotificationPrefsRequest
	ids  []string
	out  db.NotificationPrefs
	err  *models.ApiError
}

func (f *fakePrefsService) Get(_ context.Context, userID string) (db.NotificationPrefs, *models.ApiError) {
	f.user = userID
	return f.out, f.err
}
func (f *fakePrefsService) Update(_ context.Context, userID string, req api.UpdateNotificationPrefsRequest) (db.NotificationPrefs, *models.ApiError) {
	f.user, f.req = userID, req
	return f.out, f.err
}
func (f *fakePrefsService) Lookup(_ context.Context, ids []string) (map[string]db.NotificationPrefs, *models.ApiError) {
	f.ids = ids
	if f.err != nil {
		return nil, f.err
	}
	return map[string]db.NotificationPrefs{ids[0]: f.out}, nil
}

func prefsRouter(svc *fakePrefsService, userID string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	auth := func(c *gin.Context) {
		if userID != "" {
			c.Set("userID", userID)
		}
	}
	r.GET("/user/notification-prefs", auth, handlers.GetNotificationPrefs(svc))
	r.PUT("/user/notification-prefs", auth, handlers.UpdateNotificationPrefs(svc))
	r.POST("/internal/notification-prefs", handlers.LookupInternalNotificationPrefs(svc))
	return r
}

func prefsCall(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func TestNotificationPrefsHandlers_GetAsJSON(t *testing.T) {
	svc := &fakePrefsService{out: db.DefaultNotificationPrefs("u1")}
	w := prefsCall(prefsRouter(svc, "u1"), http.MethodGet, "/user/notification-prefs", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "u1", svc.user)
	assert.JSONEq(t, `{"messages":true,"reminders":true,"reminder_time":"20:00","timezone":"Africa/Tunis"}`, w.Body.String())
}

func TestNotificationPrefsHandlers_UserComesFromJWT(t *testing.T) {
	svc := &fakePrefsService{out: db.DefaultNotificationPrefs("u1")}
	w := prefsCall(prefsRouter(svc, "u1"), http.MethodPut, "/user/notification-prefs", `{"reminders":false,"user_id":"other","reminder_time":"07:00"}`)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "u1", svc.user)
	require.NotNil(t, svc.req.Reminders)
	assert.False(t, *svc.req.Reminders)
	assert.Equal(t, "07:00", *svc.req.ReminderTime)
	assert.Nil(t, svc.req.Messages)
}

func TestNotificationPrefsHandlers_Errors(t *testing.T) {
	w := prefsCall(prefsRouter(&fakePrefsService{}, ""), http.MethodGet, "/user/notification-prefs", "")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	w = prefsCall(prefsRouter(&fakePrefsService{}, ""), http.MethodPut, "/user/notification-prefs", `{}`)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	w = prefsCall(prefsRouter(&fakePrefsService{}, "u1"), http.MethodPut, "/user/notification-prefs", `{"messages":"yes"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	bad := &fakePrefsService{err: &models.ApiError{Code: http.StatusBadRequest, Error: assert.AnError}}
	w = prefsCall(prefsRouter(bad, "u1"), http.MethodPut, "/user/notification-prefs", `{"reminder_time":"x"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.NotEmpty(t, body["error"])
}

func TestNotificationPrefsHandlers_InternalLookup(t *testing.T) {
	svc := &fakePrefsService{out: db.DefaultNotificationPrefs("c1")}
	w := prefsCall(prefsRouter(svc, ""), http.MethodPost, "/internal/notification-prefs", `{"user_ids":["c1","c2"]}`)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, []string{"c1", "c2"}, svc.ids)
	assert.JSONEq(t, `{"prefs":{"c1":{"messages":true,"reminders":true,"reminder_time":"20:00","timezone":"Africa/Tunis"}}}`, w.Body.String())
	w = prefsCall(prefsRouter(svc, ""), http.MethodPost, "/internal/notification-prefs", `not json`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
