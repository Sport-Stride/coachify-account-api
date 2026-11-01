package payments

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"coachify-account-api/utils"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetSubscriptionSummaries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/internal/subscriptions/summary", r.URL.Path)
		assert.Equal(t, "k3y", r.Header.Get("x-api-key"))
		assert.Empty(t, r.Header.Get("Authorization"))
		var b struct {
			UserIDs []string `json:"user_ids"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&b))
		assert.Equal(t, []string{"u1", "u2"}, b.UserIDs)
		_, _ = w.Write([]byte(`{"summaries":[{"user_id":"u1","status":"active","plan_id":"p1","plan_name":"Pro","current_period_end":"2026-12-01T00:00:00Z"}]}`))
	}))
	defer srv.Close()
	c, err := NewPaymentClient(utils.PaymentAPIConfig{URL: srv.URL, APIKey: "k3y"})
	require.NoError(t, err)

	got, err := c.GetSubscriptionSummaries(context.Background(), []string{"u1", "u2"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "p1", got[0].PlanID)
	assert.Equal(t, "Pro", got[0].PlanName)
	require.NotNil(t, got[0].CurrentPeriodEnd)

	none, err := c.GetSubscriptionSummaries(context.Background(), nil)
	assert.NoError(t, err)
	assert.Empty(t, none)
}

func TestGetSubscriptionSummaries_FailuresAreErrors(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusInternalServerError, http.StatusUnauthorized} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		c, err := NewPaymentClient(utils.PaymentAPIConfig{URL: srv.URL})
		require.NoError(t, err)
		got, err := c.GetSubscriptionSummaries(context.Background(), []string{"u1"})
		assert.Error(t, err, status)
		assert.Nil(t, got)
		srv.Close()
	}
	c, err := NewPaymentClient(utils.PaymentAPIConfig{URL: "http://127.0.0.1:1"})
	require.NoError(t, err)
	_, err = c.GetSubscriptionSummaries(context.Background(), []string{"u1"})
	assert.Error(t, err, "unreachable payments is an error the service treats as no plan")
}
