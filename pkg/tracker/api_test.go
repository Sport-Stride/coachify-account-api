package tracker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"coachify-account-api/utils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newClient(t *testing.T, h http.HandlerFunc) *TrackerClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := NewTrackerClient(utils.TrackerAPIConfig{URL: srv.URL, APIKey: "k"})
	require.NoError(t, err)
	return c
}

func TestTransfer(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/internal/assignments/transfer", r.URL.Path)
		assert.Equal(t, "k", r.Header.Get("x-api-key"))
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, map[string]string{"gym_id": "g", "client_id": "c", "from_coach_id": "a", "to_coach_id": "", "reason": "assign"}, body)
		_, _ = w.Write([]byte(`{"matched":3,"modified":2}`))
	})
	res, err := c.Transfer(context.Background(), "g", "c", "a", "", "assign")
	require.NoError(t, err)
	assert.Equal(t, &BulkResult{Matched: 3, Modified: 2}, res)
}

func TestCancelGymAndErrors(t *testing.T) {
	from := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/internal/assignments/cancel-gym", r.URL.Path)
		var body map[string]interface{}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "2026-05-01T00:00:00Z", body["from"])
		_, _ = w.Write([]byte(`{"matched":1,"modified":1}`))
	})
	res, err := c.CancelGym(context.Background(), "g", "c", &from)
	require.NoError(t, err)
	assert.Equal(t, int64(1), res.Modified)

	bad := newClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadRequest) })
	_, err = bad.CancelGym(context.Background(), "g", "c", nil)
	assert.Error(t, err)
}
