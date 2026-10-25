package invitation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"coachify-account-api/models"
	"coachify-account-api/utils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newClient(t *testing.T, h http.HandlerFunc) *InvitationClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := NewInvitationClient(utils.InvitationAPIConfig{URL: srv.URL})
	require.NoError(t, err)
	return c
}

func respond(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func TestCheckInvitationByEmail(t *testing.T) {
	t.Run("exists", func(t *testing.T) {
		c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/invitations/check", r.URL.Path)
			var b map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&b))
			assert.Equal(t, "a@b.c", b["email"])
			_, _ = w.Write([]byte(`{"exists":true,"coach_external_id":"coach-9"}`))
		})
		ok, coach, apiErr := c.CheckInvitationByEmail(context.Background(), "a@b.c")
		require.Nil(t, apiErr)
		assert.True(t, ok)
		assert.Equal(t, "coach-9", coach)
	})
	t.Run("not exists", func(t *testing.T) {
		c := newClient(t, respond(200, `{"exists":false}`))
		ok, coach, apiErr := c.CheckInvitationByEmail(context.Background(), "a@b.c")
		require.Nil(t, apiErr)
		assert.False(t, ok)
		assert.Empty(t, coach)
	})
	t.Run("bad json", func(t *testing.T) {
		c := newClient(t, respond(200, `{x`))
		ok, _, apiErr := c.CheckInvitationByEmail(context.Background(), "a@b.c")
		assert.False(t, ok)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
	})
	t.Run("non-200", func(t *testing.T) {
		c := newClient(t, respond(500, `boom`))
		ok, _, apiErr := c.CheckInvitationByEmail(context.Background(), "a@b.c")
		assert.False(t, ok)
		require.NotNil(t, apiErr)
		assert.ErrorIs(t, apiErr.Error, models.ErrUnexpectedStatusCode)
	})
	t.Run("unreachable", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		url := srv.URL
		srv.Close()
		c, _ := NewInvitationClient(utils.InvitationAPIConfig{URL: url})
		_, _, apiErr := c.CheckInvitationByEmail(context.Background(), "a@b.c")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
	})
	t.Run("bad url", func(t *testing.T) {
		c, _ := NewInvitationClient(utils.InvitationAPIConfig{URL: "http://bad host"})
		_, _, apiErr := c.CheckInvitationByEmail(context.Background(), "a@b.c")
		require.NotNil(t, apiErr)
	})
}

type call func(c *InvitationClient) (*Invitation, *models.ApiError)

func TestValidateAndAcceptInvitation(t *testing.T) {
	calls := map[string]struct {
		path string
		fn   call
	}{
		"validate": {"/api/invitations/validate", func(c *InvitationClient) (*Invitation, *models.ApiError) {
			return c.ValidateInvitation(context.Background(), "inv-1", "a@b.c")
		}},
		"accept": {"/api/invitations/accept", func(c *InvitationClient) (*Invitation, *models.ApiError) {
			return c.AcceptInvitation(context.Background(), "inv-1", "a@b.c", "refresh-tok")
		}},
	}
	for name, tc := range calls {
		t.Run(name+"/success", func(t *testing.T) {
			c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, tc.path, r.URL.Path)
				var b AcceptInvitationRequest
				require.NoError(t, json.NewDecoder(r.Body).Decode(&b))
				assert.Equal(t, "inv-1", b.ExternalID)
				assert.Equal(t, "a@b.c", b.Email)
				if name == "accept" {
					assert.Equal(t, "Bearer refresh-tok", r.Header.Get("Authorization"))
				}
				_, _ = w.Write([]byte(`{"success":true,"invitation":{"external_id":"inv-1","sender_id":"coach-1"}}`))
			})
			inv, apiErr := tc.fn(c)
			require.Nil(t, apiErr)
			assert.Equal(t, "coach-1", inv.SenderID)
		})
		for status, want := range map[int]int{
			http.StatusNotFound:            http.StatusNotFound,
			http.StatusBadRequest:          http.StatusBadRequest,
			http.StatusInternalServerError: http.StatusInternalServerError,
		} {
			t.Run(name+"/status", func(t *testing.T) {
				c := newClient(t, respond(status, ""))
				inv, apiErr := tc.fn(c)
				assert.Nil(t, inv)
				require.NotNil(t, apiErr)
				assert.Equal(t, want, apiErr.Code)
			})
		}
		t.Run(name+"/success false", func(t *testing.T) {
			c := newClient(t, respond(200, `{"success":false,"error":"expired"}`))
			inv, apiErr := tc.fn(c)
			assert.Nil(t, inv)
			require.NotNil(t, apiErr)
			assert.Equal(t, http.StatusBadRequest, apiErr.Code)
			assert.Contains(t, apiErr.Error.Error(), "expired")
		})
		t.Run(name+"/bad json", func(t *testing.T) {
			c := newClient(t, respond(200, `{x`))
			_, apiErr := tc.fn(c)
			require.NotNil(t, apiErr)
			assert.ErrorIs(t, apiErr.Error, models.ErrFailedToUnmarshalResponse)
		})
		t.Run(name+"/unreachable", func(t *testing.T) {
			srv := httptest.NewServer(http.NotFoundHandler())
			url := srv.URL
			srv.Close()
			c, _ := NewInvitationClient(utils.InvitationAPIConfig{URL: url})
			_, apiErr := tc.fn(c)
			require.NotNil(t, apiErr)
			assert.ErrorIs(t, apiErr.Error, models.ErrFailedToSendRequest)
		})
		t.Run(name+"/bad url", func(t *testing.T) {
			c, _ := NewInvitationClient(utils.InvitationAPIConfig{URL: "http://bad host"})
			_, apiErr := tc.fn(c)
			require.NotNil(t, apiErr)
			assert.ErrorIs(t, apiErr.Error, models.ErrFailedToCreateRequest)
		})
	}
}

func TestInvitationErrorsDoNotEmbedBody(t *testing.T) {
	const secret = "SECRET-DOWNSTREAM-BODY"
	c := newClient(t, respond(http.StatusBadGateway, secret))
	_, _, apiErr := c.CheckInvitationByEmail(context.Background(), "a@b.c")
	require.NotNil(t, apiErr)
	assert.NotContains(t, apiErr.Error.Error(), secret)
	assert.Contains(t, apiErr.Error.Error(), "502")
	inv, apiErr := c.ValidateInvitation(context.Background(), "tok", "a@b.c")
	assert.Nil(t, inv)
	require.NotNil(t, apiErr)
	assert.NotContains(t, apiErr.Error.Error(), secret)
	inv, apiErr = c.AcceptInvitation(context.Background(), "tok", "a@b.c", "rt")
	assert.Nil(t, inv)
	require.NotNil(t, apiErr)
	assert.NotContains(t, apiErr.Error.Error(), secret)
}
