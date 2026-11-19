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

func TestCancelGymMembership(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/internal/gym-memberships/cancel", r.URL.Path)
		assert.Equal(t, "k3y", r.Header.Get("x-api-key"))
		var b map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&b))
		assert.Equal(t, map[string]string{"gym_id": "g1", "user_id": "u1"}, b)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"status":"cancel_at_period_end","subscription_id":"s1"}`))
	}))
	defer srv.Close()
	c, err := NewPaymentClient(utils.PaymentAPIConfig{URL: srv.URL, APIKey: "k3y"})
	require.NoError(t, err)

	res, err := c.CancelGymMembership(context.Background(), "g1", "u1")
	require.NoError(t, err)
	assert.Equal(t, "cancel_at_period_end", res.Status)
	assert.Equal(t, "s1", res.SubscriptionID)

	status = http.StatusServiceUnavailable
	_, err = c.CancelGymMembership(context.Background(), "g1", "u1")
	assert.Error(t, err)

	_, err = c.CancelGymMembership(context.Background(), "", "u1")
	assert.Error(t, err)

	nokey, err := NewPaymentClient(utils.PaymentAPIConfig{URL: srv.URL})
	require.NoError(t, err)
	_, err = nokey.CancelGymMembership(context.Background(), "g1", "u1")
	assert.Error(t, err, "no key: refused before the call")
}
