//go:build integration

package repositories_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
)

func TestMyGymsAggregationOnRealMongo(t *testing.T) {
	database := gymTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	now := time.Now()

	mkGym := func(name, status string) string {
		res, err := database.Collection("gyms").InsertOne(ctx, &db.Gym{Name: name, Status: status, OwnerID: "o", CreatedAt: now, UpdatedAt: now})
		require.NoError(t, err)
		return res.InsertedID.(primitive.ObjectID).Hex()
	}
	gA := mkGym("Alpha", db.GymStatusActive)
	gB := mkGym("Beta", db.GymStatusActive)
	gArch := mkGym("Old", db.GymStatusArchived)
	gLeft := mkGym("Left", db.GymStatusActive)
	gPool := mkGym("Pool", db.GymStatusActive)

	_, err := database.Collection("users").InsertOne(ctx, bson.M{"externalid": "coach1", "firstname": "Ada", "lastname": "Lovelace"})
	require.NoError(t, err)

	rows := []db.GymClient{
		{GymID: gB, ClientID: "me", CoachID: "coach1", Status: db.GymClientStatusActive, JoinedAt: now, ConsentAt: now},
		{GymID: gA, ClientID: "me", CoachID: "", Status: db.GymClientStatusActive, JoinedAt: now, ConsentAt: now},
		{GymID: gArch, ClientID: "me", CoachID: "coach1", Status: db.GymClientStatusActive, JoinedAt: now, ConsentAt: now},
		{GymID: gLeft, ClientID: "me", CoachID: "coach1", Status: db.GymClientStatusLeft, JoinedAt: now, ConsentAt: now},
		{GymID: gPool, ClientID: "other", CoachID: "coach1", Status: db.GymClientStatusActive, JoinedAt: now, ConsentAt: now},
	}
	for i := range rows {
		_, err := database.Collection("gym_clients").InsertOne(ctx, &rows[i])
		require.NoError(t, err)
	}

	repo := repositories.NewGymClientRepository(database, "users")
	got, err := repo.MyGyms(ctx, "me")
	require.NoError(t, err)
	require.Len(t, got, 2, "only active rows of active gyms")
	assert.Less(t, got[0].GymID, got[1].GymID, "sorted by gym_id")
	byID := map[string]db.MyGymRow{got[0].GymID: got[0], got[1].GymID: got[1]}
	assert.Equal(t, "Alpha", byID[gA].GymName)
	assert.Empty(t, byID[gA].CoachID)
	assert.Empty(t, byID[gA].CoachName)
	assert.Equal(t, "Beta", byID[gB].GymName)
	assert.Equal(t, "coach1", byID[gB].CoachID)
	assert.Equal(t, "Ada Lovelace", byID[gB].CoachName)
	assert.NotNil(t, byID[gB].ConsentAt)

	none, err := repo.MyGyms(ctx, "nobody")
	require.NoError(t, err)
	assert.NotNil(t, none)
	assert.Empty(t, none)
}

func TestAssignTxRollsBackOnAuditFailure(t *testing.T) {
	repo, ctx, of := dispatchFixture(t)
	// The audit insert fails (duplicate _id) after the client update: the update must roll back.
	id := primitive.NewObjectID()
	dup := &db.TeamAuditEntry{ID: id, Action: db.TeamActionClientReassign, ClientID: "cl1", GymID: "g1"}
	ok, err := repo.AssignTx(ctx, "g1", "cl1", "c1", "c2", time.Now(), dup)
	require.NoError(t, err)
	require.True(t, ok)

	// Same _id again: second AssignTx (c2 -> c3) matches the CAS but its audit insert collides.
	dup2 := &db.TeamAuditEntry{ID: id, Action: db.TeamActionClientReassign, ClientID: "cl1", GymID: "g1"}
	ok, err = repo.AssignTx(ctx, "g1", "cl1", "c2", "c3", time.Now(), dup2)
	assert.Error(t, err)
	assert.False(t, ok)
	assert.Len(t, of("c2"), 1, "client update rolled back, still with c2")
	assert.Empty(t, of("c3"), "no partial write")
}
