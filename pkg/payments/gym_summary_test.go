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

func TestGymSummaries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/internal/gym-subscriptions/summary", r.URL.Path)
		assert.Equal(t, "k3y", r.Header.Get("x-api-key"))
		var b struct {
			GymIDs []string `json:"gym_ids"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&b))
		assert.Equal(t, []string{"g1", "g2"}, b.GymIDs)
		_, _ = w.Write([]byte(`{"summaries":[
			{"gym_id":"g1","status":"grace","plan_id":"p1","included_seats":2,"extra_seats":1,"total_seats":3,"max_gym_clients":50,"period_end":"2026-12-01T00:00:00Z","grace_until":"2026-12-08T00:00:00Z","features":["workouts"]},
			{"gym_id":"g2","status":"none","plan_id":"","included_seats":0,"extra_seats":0,"total_seats":0,"max_gym_clients":0,"period_end":null,"features":[]}]}`))
	}))
	defer srv.Close()
	c, err := NewPaymentClient(utils.PaymentAPIConfig{URL: srv.URL, APIKey: "k3y"})
	require.NoError(t, err)

	got, err := c.GymSummaries(context.Background(), []string{"g1", "g2"})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "grace", got[0].Status)
	assert.Equal(t, int64(3), got[0].TotalSeats)
	assert.Equal(t, int64(50), got[0].MaxGymClients)
	require.NotNil(t, got[0].GraceUntil)
	assert.Equal(t, "none", got[1].Status)
	assert.Nil(t, got[1].PeriodEnd)

	none, err := c.GymSummaries(context.Background(), nil)
	assert.NoError(t, err)
	assert.Empty(t, none)

	_, err = c.GymSummaries(context.Background(), make([]string, MaxGymSummaryIDs+1))
	assert.Error(t, err, "more than 200 ids is refused before the call")
}

func TestGymSummaries_FailureIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c, err := NewPaymentClient(utils.PaymentAPIConfig{URL: srv.URL, APIKey: "k"})
	require.NoError(t, err)
	_, err = c.GymSummaries(context.Background(), []string{"g1"})
	assert.Error(t, err)
}

func TestSubscriptionSummaryEntryTypeIsPersonal(t *testing.T) {
	var e SubscriptionSummaryEntry
	require.NoError(t, json.Unmarshal([]byte(`{"user_id":"u","type":"gym","plan_owner_type":"gym","plan_owner_id":"g1"}`), &e))
	assert.False(t, e.IsPersonal())
	assert.Equal(t, "g1", e.PlanOwnerID)
	assert.True(t, SubscriptionSummaryEntry{}.IsPersonal(), "no type (older payments-api) is personal")
	assert.True(t, SubscriptionSummaryEntry{Type: SummaryTypePersonal}.IsPersonal())
}
