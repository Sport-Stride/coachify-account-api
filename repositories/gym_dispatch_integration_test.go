//go:build integration

package repositories_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"

	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
)

// These tests need a replica set (transactions): GYM_TEST_MONGO_URI must point at a throwaway replica set host.
// They skip on a standalone server.
func dispatchFixture(t *testing.T) (*repositories.GymDispatchRepository, context.Context, func(coach string) []db.GymClient) {
	t.Helper()
	database := gymTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	repo := repositories.NewGymDispatchRepository(database)
	ok, err := repo.SupportsTransactions(ctx)
	require.NoError(t, err)
	if !ok {
		t.Skip("deployment has no transaction support (replica set required)")
	}
	now := time.Now()
	for _, c := range []db.GymClient{
		{GymID: "g1", ClientID: "cl1", CoachID: "c1", Status: db.GymClientStatusActive, JoinedAt: now},
		{GymID: "g1", ClientID: "cl2", CoachID: "", Status: db.GymClientStatusActive, JoinedAt: now},
		{GymID: "g1", ClientID: "cl3", CoachID: "c1", Status: db.GymClientStatusActive, JoinedAt: now},
	} {
		c := c
		_, err := database.Collection("gym_clients").InsertOne(ctx, &c)
		require.NoError(t, err)
	}
	of := func(coach string) []db.GymClient {
		cur, err := database.Collection("gym_clients").Find(ctx, bson.M{"coach_id": coach, "status": db.GymClientStatusActive})
		require.NoError(t, err)
		var out []db.GymClient
		require.NoError(t, cur.All(ctx, &out))
		return out
	}
	return repo, ctx, of
}

func TestAssignTxCommitsClientAndAuditTogether(t *testing.T) {
	repo, ctx, of := dispatchFixture(t)
	ok, err := repo.AssignTx(ctx, "g1", "cl1", "c1", "c2", time.Now(), &db.TeamAuditEntry{Action: db.TeamActionClientReassign, ClientID: "cl1", GymID: "g1"})
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Len(t, of("c2"), 1)
	assert.Len(t, of("c1"), 1)
}

func TestAssignTxCASConflictWritesNothing(t *testing.T) {
	repo, ctx, of := dispatchFixture(t)
	ok, err := repo.AssignTx(ctx, "g1", "cl1", "someone-else", "c2", time.Now(), &db.TeamAuditEntry{Action: db.TeamActionClientReassign, ClientID: "cl1", GymID: "g1"})
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Empty(t, of("c2"))
	assert.Len(t, of("c1"), 2)
}

func TestLeaveTxAndReleaseCoachTx(t *testing.T) {
	repo, ctx, of := dispatchFixture(t)
	ok, err := repo.LeaveTx(ctx, "g1", "cl2", time.Now(), &db.TeamAuditEntry{Action: db.TeamActionClientLeaveGym, ClientID: "cl2", GymID: "g1"})
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = repo.LeaveTx(ctx, "g1", "cl2", time.Now(), nil)
	require.NoError(t, err)
	assert.False(t, ok, "second leave matches nothing")

	rows, err := repo.ReleaseCoachTx(ctx, []string{"c1"}, "", time.Now(), func(r db.GymClient) *db.TeamAuditEntry {
		return &db.TeamAuditEntry{Action: db.TeamActionClientUnassign, ClientID: r.ClientID, GymID: r.GymID, PrevCoachID: "c1"}
	})
	require.NoError(t, err)
	assert.Len(t, rows, 2)
	assert.Empty(t, of("c1"))
	assert.Len(t, of(""), 2, "released clients are in the pool")
}
