package identifier

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"coachify-account-api/models"
	"coachify-account-api/utils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newClient(t *testing.T, h http.HandlerFunc) *IdentifierClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := NewIdentifierClient(utils.IdentifierAPIConfig{URL: srv.URL})
	require.NoError(t, err)
	return c
}

func TestGenerateId_Success(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/identifier/user", r.URL.Path)
		_, _ = w.Write([]byte(`{"code":"abc123","label":"user"}`))
	})
	e, apiErr := c.GenerateId(context.Background(), "user")
	require.Nil(t, apiErr)
	assert.Equal(t, "abc123", e.Code)
	assert.Equal(t, "user", e.Label)
}

func TestGenerateId_Errors(t *testing.T) {
	cases := []struct {
		name     string
		h        http.HandlerFunc
		wantCode int
		wantErr  error
	}{
		{"400 from identifier", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadRequest) },
			http.StatusBadRequest, models.ErrBadRequestToIdentifier},
		{"500 from identifier", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
			http.StatusInternalServerError, models.ErrUnexpectedStatusCode},
		{"404 from identifier", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) },
			http.StatusInternalServerError, models.ErrUnexpectedStatusCode},
		{"invalid json", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{oops`)) },
			http.StatusInternalServerError, models.ErrFailedToUnmarshalResponse},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newClient(t, tc.h)
			e, apiErr := c.GenerateId(context.Background(), "user")
			assert.Equal(t, Entity{}, e)
			require.NotNil(t, apiErr)
			assert.Equal(t, tc.wantCode, apiErr.Code)
			assert.ErrorIs(t, apiErr.Error, tc.wantErr)
		})
	}
}

func TestGenerateId_Unreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	c, _ := NewIdentifierClient(utils.IdentifierAPIConfig{URL: url})
	_, apiErr := c.GenerateId(context.Background(), "user")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
	assert.ErrorIs(t, apiErr.Error, models.ErrFailedToSendRequest)
}

func TestGenerateId_BadURL(t *testing.T) {
	c, _ := NewIdentifierClient(utils.IdentifierAPIConfig{URL: "http://bad host"})
	_, apiErr := c.GenerateId(context.Background(), "user")
	require.NotNil(t, apiErr)
	assert.ErrorIs(t, apiErr.Error, models.ErrFailedToCreateRequest)
}

func TestGenerateId_ErrorDoesNotEmbedBody(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("SECRET-DOWNSTREAM-BODY"))
	})
	_, apiErr := c.GenerateId(context.Background(), "user")
	require.NotNil(t, apiErr)
	assert.NotContains(t, apiErr.Error.Error(), "SECRET-DOWNSTREAM-BODY")
	assert.Contains(t, apiErr.Error.Error(), "502")
}
