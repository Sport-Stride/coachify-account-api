//go:build integration

package repositories_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
)

// ActiveGymClientsByClients on real Mongo: $in on client ids, only status=active rows, whoever holds them.
func TestActiveGymClientsByClientsIntegration(t *testing.T) {
	database := gymTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	require.NoError(t, repositories.InitializeIndexes(ctx, database))

	gc := repositories.NewGymClientRepository(database, "users")
	coach := repositories.NewCoachRepository(database, "coach_clients", database.Collection("users"))
	now := time.Now()
	row := func(gym, client, coachID string) *db.GymClient {
		return &db.GymClient{GymID: gym, ClientID: client, CoachID: coachID, Status: db.GymClientStatusActive, JoinedAt: now, ConsentAt: now}
	}
	require.NoError(t, gc.Insert(ctx, row("g1", "cl1", "c1")))
	require.NoError(t, gc.Insert(ctx, row("g2", "cl1", "c2"))) // same client, other gym, other coach
	require.NoError(t, gc.Insert(ctx, row("g1", "cl2", "")))   // pool
	require.NoError(t, gc.Insert(ctx, row("g1", "cl3", "c1"))) // not requested
	require.NoError(t, gc.Insert(ctx, row("g3", "cl4", "c1")))
	ok, err := gc.Leave(ctx, "g3", "cl4", now) // a left row must not come back
	require.NoError(t, err)
	require.True(t, ok)

	rows, err := coach.ActiveGymClientsByClients(ctx, []string{"cl1", "cl2", "cl4", "nobody"})
	require.NoError(t, err)
	type k struct{ gym, client string }
	got := map[k]string{}
	for _, r := range rows {
		assert.Equal(t, db.GymClientStatusActive, r.Status)
		got[k{r.GymID, r.ClientID}] = r.CoachID
	}
	assert.Equal(t, map[k]string{{"g1", "cl1"}: "c1", {"g2", "cl1"}: "c2", {"g1", "cl2"}: ""}, got)

	empty, err := coach.ActiveGymClientsByClients(ctx, []string{})
	require.NoError(t, err)
	assert.Empty(t, empty)
	assert.NotNil(t, empty)
}
