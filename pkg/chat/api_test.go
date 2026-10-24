package chat

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

func newClient(t *testing.T, h http.HandlerFunc) *ChatClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := NewChatClient(utils.ChatAPIConfig{URL: srv.URL})
	require.NoError(t, err)
	return c
}

func TestCreateConversation_Success(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusCreated} {
		c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "/create-conversation", r.URL.Path)
			assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
			var body createConversationRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, "coach-1", body.AdminID)
			assert.Equal(t, []string{"client-1"}, body.ClientIDs)
			w.WriteHeader(status)
		})
		assert.Nil(t, c.CreateConversation(context.Background(), "coach-1", "client-1", "tok"))
	}
}

func TestCreateConversation_NoTokenOmitsAuthHeader(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	})
	assert.Nil(t, c.CreateConversation(context.Background(), "a", "b", ""))
}

func TestCreateConversation_Errors(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusInternalServerError} {
		c := newClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) })
		apiErr := c.CreateConversation(context.Background(), "a", "b", "t")
		require.NotNil(t, apiErr, status)
		assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
		assert.ErrorIs(t, apiErr.Error, models.ErrUnexpectedStatusCode)
	}
}

func TestCreateConversation_UnreachableAndBadURL(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	c, _ := NewChatClient(utils.ChatAPIConfig{URL: url})
	apiErr := c.CreateConversation(context.Background(), "a", "b", "t")
	require.NotNil(t, apiErr)
	assert.ErrorIs(t, apiErr.Error, models.ErrFailedToSendRequest)

	c, _ = NewChatClient(utils.ChatAPIConfig{URL: "http://bad host"})
	apiErr = c.CreateConversation(context.Background(), "a", "b", "t")
	require.NotNil(t, apiErr)
	assert.ErrorIs(t, apiErr.Error, models.ErrFailedToCreateRequest)
}
