package notification

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
	"coachify-account-api/utils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type captured struct {
	path string
	body map[string]interface{}
}

func newClient(t *testing.T, status int, resp string) (*NotificationClient, *captured) {
	t.Helper()
	got := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path = r.URL.Path
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		_ = json.NewDecoder(r.Body).Decode(&got.body)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	c, err := NewNotificationClient(utils.NotificationAPIConfig{URL: srv.URL})
	require.NoError(t, err)
	return c, got
}

func userWithCode() *db.User {
	return &db.User{
		UserEmail: "ada@example.com", UserFirstname: "Ada", UserLastname: "L",
		UserConfirmCode: &db.UserConfirmCode{Code: "123456"},
	}
}

func dyn(got *captured) map[string]interface{} {
	d, _ := got.body["dynamicData"].(map[string]interface{})
	return d
}

func TestSendConfirmationEmail(t *testing.T) {
	c, got := newClient(t, http.StatusOK, "")
	require.NoError(t, c.SendConfirmationEmail(context.Background(), userWithCode()))
	assert.Equal(t, "/emails", got.path)
	assert.Equal(t, "ada@example.com", got.body["to"])
	assert.Contains(t, dyn(got)["message"], "123456")
	assert.Equal(t, "Ada L", dyn(got)["username"])
}

func TestSendMail(t *testing.T) {
	c, got := newClient(t, http.StatusAccepted, "")
	require.NoError(t, c.SendMail(context.Background(), userWithCode()))
	assert.Equal(t, "ada@example.com", got.body["to"])
}

func TestSendResendConfirmationEmail(t *testing.T) {
	c, got := newClient(t, http.StatusOK, "")
	require.NoError(t, c.SendResendConfirmationEmail(context.Background(), userWithCode()))
	assert.Contains(t, dyn(got)["message"], "123456")
}

func TestSendResetPasswordEmail(t *testing.T) {
	c, got := newClient(t, http.StatusOK, "")
	err := c.SendResetPasswordEmail(context.Background(), &api.ResetPasswordResponse{
		UserEmail: "ada@example.com", UserFirstname: "Ada", UserLastname: "L",
		UserResetPasswordCode: db.UserResetPasswordCode{Code: "999000"},
	})
	require.NoError(t, err)
	assert.Contains(t, dyn(got)["message"], "999000")

	err = c.SendConfirmResetPasswordEmail(context.Background(), &api.ResetPasswordResponse{UserEmail: "ada@example.com"})
	require.NoError(t, err)
	assert.NotContains(t, dyn(got)["message"], "999000")
}

func TestSendWelcomeEmail(t *testing.T) {
	c, got := newClient(t, http.StatusOK, "")
	require.NoError(t, c.SendWelcomeEmail(context.Background(), userWithCode()))
	assert.NotEmpty(t, dyn(got)["validation_link"])
}

func TestEmptyRecipientRejectedWithoutHTTPCall(t *testing.T) {
	c, got := newClient(t, http.StatusOK, "")
	ctx := context.Background()
	empty := &db.User{UserConfirmCode: &db.UserConfirmCode{Code: "1"}}
	assert.Error(t, c.SendMail(ctx, empty))
	assert.Error(t, c.SendConfirmationEmail(ctx, empty))
	assert.Error(t, c.SendResendConfirmationEmail(ctx, empty))
	assert.Error(t, c.SendWelcomeEmail(ctx, empty))
	assert.Error(t, c.SendResetPasswordEmail(ctx, &api.ResetPasswordResponse{}))
	assert.Error(t, c.SendConfirmResetPasswordEmail(ctx, &api.ResetPasswordResponse{}))
	assert.Error(t, c.SendMatriculeFiscaleNotification(ctx, nil, "approved"))
	assert.Error(t, c.SendMatriculeFiscaleNotification(ctx, empty, "approved"))
	assert.Empty(t, got.path, "no request must be sent")
}

func TestNilConfirmCodeDoesNotPanic(t *testing.T) {
	c, got := newClient(t, http.StatusOK, "")
	u := &db.User{UserEmail: "ada@example.com"}
	assert.NotPanics(t, func() {
		assert.Error(t, c.SendMail(context.Background(), u))
		assert.Error(t, c.SendConfirmationEmail(context.Background(), u))
		assert.Error(t, c.SendResendConfirmationEmail(context.Background(), u))
	})
	assert.Empty(t, got.path)
}

func TestSendEmail_HTTPErrors(t *testing.T) {
	for status, want := range map[int]string{
		http.StatusBadRequest:          "invalid request",
		http.StatusInternalServerError: "server error",
		http.StatusTeapot:              "unexpected status code",
	} {
		c, _ := newClient(t, status, "downstream-detail")
		err := c.SendConfirmationEmail(context.Background(), userWithCode())
		require.Error(t, err, status)
		assert.Contains(t, err.Error(), want)
	}
}

func TestSendEmail_Unreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	c, _ := NewNotificationClient(utils.NotificationAPIConfig{URL: url})
	err := c.SendConfirmationEmail(context.Background(), userWithCode())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to send request")

	c, _ = NewNotificationClient(utils.NotificationAPIConfig{URL: "http://bad host"})
	err = c.SendConfirmationEmail(context.Background(), userWithCode())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create request")
}

func TestSendMatriculeFiscaleNotification(t *testing.T) {
	c, got := newClient(t, http.StatusOK, "")
	u := userWithCode()

	require.NoError(t, c.SendMatriculeFiscaleNotification(context.Background(), u, "approved"))
	assert.Equal(t, "matricule-fiscale-approved", got.body["templateName"])
	assert.Contains(t, dyn(got)["subject"], "Approved")

	require.NoError(t, c.SendMatriculeFiscaleNotification(context.Background(), u, "rejected"))
	assert.Equal(t, "matricule-fiscale-rejected", got.body["templateName"])

	got.path = ""
	err := c.SendMatriculeFiscaleNotification(context.Background(), u, "weird")
	require.Error(t, err)
	assert.Empty(t, got.path)
}

func TestSendEmail_ErrorsDoNotEmbedBody(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusInternalServerError, http.StatusTeapot} {
		c, _ := newClient(t, status, "SECRET-DOWNSTREAM-BODY")
		err := c.SendConfirmationEmail(context.Background(), userWithCode())
		require.Error(t, err, status)
		assert.NotContains(t, err.Error(), "SECRET-DOWNSTREAM-BODY")
	}
}
