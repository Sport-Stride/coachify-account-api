package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"coachify-account-api/handlers"
	"coachify-account-api/models"
	"coachify-account-api/models/api"
)

type fakeUploads struct {
	user string
	req  api.UploadSignRequest
	sig  *api.UploadSignature
	err  *models.ApiError
}

func (f *fakeUploads) Sign(_ context.Context, userID string, req api.UploadSignRequest) (*api.UploadSignature, *models.ApiError) {
	f.user, f.req = userID, req
	return f.sig, f.err
}

func uploadsRouter(svc *fakeUploads, userID string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/uploads/sign", func(c *gin.Context) {
		if userID != "" {
			c.Set("userID", userID)
		}
	}, handlers.SignUpload(svc))
	return r
}

func postSign(r *gin.Engine, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/uploads/sign", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func TestSignUpload(t *testing.T) {
	t.Run("no token claims", func(t *testing.T) {
		svc := &fakeUploads{}
		w := postSign(uploadsRouter(svc, ""), `{"purpose":"profile","size":1}`)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Empty(t, svc.user)
	})

	t.Run("bad payload", func(t *testing.T) {
		svc := &fakeUploads{}
		w := postSign(uploadsRouter(svc, "u1"), `{`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Empty(t, svc.user)
	})

	t.Run("service error keeps the error body", func(t *testing.T) {
		svc := &fakeUploads{err: apiErr(http.StatusServiceUnavailable, "uploads not configured")}
		w := postSign(uploadsRouter(svc, "u1"), `{"purpose":"profile","size":1}`)
		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
		assert.JSONEq(t, `{"error":"uploads not configured"}`, w.Body.String())
	})

	t.Run("success uses the caller from the token", func(t *testing.T) {
		svc := &fakeUploads{sig: &api.UploadSignature{UploadURL: "https://api.cloudinary.com/v1_1/demo/image/upload", Fields: map[string]string{"signature": "x"}, ExpiresAt: 1}}
		w := postSign(uploadsRouter(svc, "u1"), `{"purpose":"progress","size":2048,"user_id":"someone-else"}`)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		assert.Equal(t, "u1", svc.user)
		assert.Equal(t, api.UploadSignRequest{Purpose: "progress", Size: 2048}, svc.req)
	})
}
