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

func newClient(t *testing.T, h http.HandlerFunc) *PaymentClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := NewPaymentClient(utils.PaymentAPIConfig{URL: srv.URL, PlanHexCoach: "plan-coach", PlanHexClient: "plan-client"})
	require.NoError(t, err)
	return c
}

func TestSubscribeWithTrial_PlanByRole(t *testing.T) {
	for role, plan := range map[string]string{"coach": "plan-coach", "client": "plan-client", "nutritionist": "plan-client"} {
		c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "/subscriptions", r.URL.Path)
			assert.Equal(t, "Bearer rt", r.Header.Get("Authorization"))
			var b map[string]interface{}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&b))
			assert.Equal(t, plan, b["plan_id"])
			assert.EqualValues(t, 30, b["trial_days"])
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"success":true}`))
		})
		pr, err := c.SubscribeWithTrial(context.Background(), role, "rt")
		require.NoError(t, err, role)
		assert.True(t, pr.Success)
	}
}

func TestSubscribeWithTrial_Errors(t *testing.T) {
	t.Run("non-success status", func(t *testing.T) {
		c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("nope"))
		})
		pr, err := c.SubscribeWithTrial(context.Background(), "coach", "rt")
		assert.Nil(t, pr)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "status=403")
	})
	t.Run("bad json", func(t *testing.T) {
		c := newClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("{x")) })
		pr, err := c.SubscribeWithTrial(context.Background(), "coach", "rt")
		assert.Nil(t, pr)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "decoding")
	})
	t.Run("unreachable", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		url := srv.URL
		srv.Close()
		c, _ := NewPaymentClient(utils.PaymentAPIConfig{URL: url})
		_, err := c.SubscribeWithTrial(context.Background(), "coach", "rt")
		assert.Error(t, err)
	})
	t.Run("bad url", func(t *testing.T) {
		c, _ := NewPaymentClient(utils.PaymentAPIConfig{URL: "http://bad host"})
		_, err := c.SubscribeWithTrial(context.Background(), "coach", "rt")
		assert.Error(t, err)
	})
}

func TestGetBulkBillingInfo(t *testing.T) {
	t.Run("empty ids short-circuits", func(t *testing.T) {
		called := false
		c := newClient(t, func(w http.ResponseWriter, r *http.Request) { called = true })
		res, err := c.GetBulkBillingInfo(context.Background(), "adm", nil)
		require.NoError(t, err)
		assert.Empty(t, res.Billings)
		assert.False(t, called)
	})
	t.Run("success forwards token and ids", func(t *testing.T) {
		c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/admin/subscriptions/bulk-billing", r.URL.Path)
			assert.Equal(t, "Bearer adm", r.Header.Get("Authorization"))
			assert.Equal(t, []string{"u1", "u2"}, r.URL.Query()["user_ids"])
			_, _ = w.Write([]byte(`{"billings":[{"user_id":"u1","subscription":{"status":"active","plan":{"name":"Pro"}}},{"user_id":"u2","subscription":null}],"total":2}`))
		})
		res, err := c.GetBulkBillingInfo(context.Background(), "adm", []string{"u1", "u2"})
		require.NoError(t, err)
		require.Len(t, res.Billings, 2)
		assert.Equal(t, "Pro", res.Billings[0].Subscription.Plan.Name)
		assert.Nil(t, res.Billings[1].Subscription)
	})
	t.Run("non-200", func(t *testing.T) {
		c := newClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
		res, err := c.GetBulkBillingInfo(context.Background(), "bad", []string{"u1"})
		assert.Nil(t, res)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "status=401")
	})
	t.Run("bad json", func(t *testing.T) {
		c := newClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("{x")) })
		res, err := c.GetBulkBillingInfo(context.Background(), "adm", []string{"u1"})
		assert.Nil(t, res)
		assert.Error(t, err)
	})
	t.Run("unreachable and bad url", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		url := srv.URL
		srv.Close()
		c, _ := NewPaymentClient(utils.PaymentAPIConfig{URL: url})
		_, err := c.GetBulkBillingInfo(context.Background(), "adm", []string{"u1"})
		assert.Error(t, err)
		c, _ = NewPaymentClient(utils.PaymentAPIConfig{URL: "http://bad host"})
		_, err = c.GetBulkBillingInfo(context.Background(), "adm", []string{"u1"})
		assert.Error(t, err)
	})
}

func TestPaymentErrorsDoNotEmbedBody(t *testing.T) {
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("SECRET-DOWNSTREAM-BODY"))
	})
	_, err := c.SubscribeWithTrial(context.Background(), "coach", "rt")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SECRET-DOWNSTREAM-BODY")
	assert.Contains(t, err.Error(), "403")
	_, err = c.GetBulkBillingInfo(context.Background(), "tok", []string{"u1"})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SECRET-DOWNSTREAM-BODY")
}
