//go:build integration

package repositories_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"

	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
)

func TestGymMemberIT_SeatAssignReleaseAndState(t *testing.T) {
	database, repo := gymMemberFixture(t)
	ctx, cancel := memberCtx()
	defer cancel()
	now := time.Now().UTC().Truncate(time.Millisecond)

	require.NoError(t, repo.AddActive(ctx, &db.GymMember{GymID: "g1", UserID: "owner", Role: db.GymMemberRoleOwner, JoinedAt: now, Source: db.GymMemberSourceAdmin}))
	require.NoError(t, repo.AddActive(ctx, &db.GymMember{GymID: "g1", UserID: "c1", Role: db.GymMemberRoleCoach, JoinedAt: now, Source: db.GymMemberSourceFlat}))

	ok, err := repo.SetSeat(ctx, "g1", "c1", true, now)
	require.NoError(t, err)
	assert.True(t, ok)
	m, err := repo.Get(ctx, "c1", "g1")
	require.NoError(t, err)
	assert.True(t, m.SeatAssigned)
	assert.Equal(t, db.SeatStateSeated, m.SeatState)
	require.NotNil(t, m.SeatAssignedAt)

	// the owner has no coach slot: a seat is never assigned to him
	ok, err = repo.SetSeat(ctx, "g1", "owner", true, now)
	require.NoError(t, err)
	assert.False(t, ok)

	grace := now.Add(7 * 24 * time.Hour)
	ok, err = repo.SetSeatState(ctx, "g1", "c1", db.SeatStateGrace, &grace, now)
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = repo.SetSeatState(ctx, "g1", "c1", db.SeatStateGrace, &grace, now) // idempotent
	require.NoError(t, err)
	assert.True(t, ok)
	m, _ = repo.Get(ctx, "c1", "g1")
	assert.Equal(t, db.SeatStateGrace, m.SeatState)
	require.NotNil(t, m.SeatGraceUntil)
	assert.True(t, m.SeatGraceUntil.Equal(grace))

	// the grace deadline is conditional on seat_grace_until being unset: a later write keeps the first deadline
	later := grace.Add(48 * time.Hour)
	ok, err = repo.SetSeatState(ctx, "g1", "c1", db.SeatStateGrace, &later, now)
	require.NoError(t, err)
	assert.True(t, ok)
	m, _ = repo.Get(ctx, "c1", "g1")
	require.NotNil(t, m.SeatGraceUntil)
	assert.True(t, m.SeatGraceUntil.Equal(grace), "first deadline wins")

	ok, err = repo.SetSeatState(ctx, "g1", "c1", db.SeatStateSeated, nil, now)
	require.NoError(t, err)
	assert.True(t, ok)
	m, _ = repo.Get(ctx, "c1", "g1")
	assert.Nil(t, m.SeatGraceUntil)

	ok, err = repo.SetSeat(ctx, "g1", "c1", false, now)
	require.NoError(t, err)
	assert.True(t, ok)
	m, _ = repo.Get(ctx, "c1", "g1")
	assert.False(t, m.SeatAssigned)
	assert.Equal(t, db.SeatStateNone, m.SeatState)
	assert.Nil(t, m.SeatAssignedAt)

	rows := activeRows(t, database, bson.M{"gym_id": "g1"})
	assert.Len(t, rows, 2)
}

func TestGymMemberIT_SeatIndexExists(t *testing.T) {
	database := gymTestDB(t)
	ctx, cancel := memberCtx()
	defer cancel()
	require.NoError(t, repositories.InitializeIndexes(ctx, database))
	cur, err := database.Collection("gym_members").Indexes().List(ctx)
	require.NoError(t, err)
	var idx []map[string]any
	require.NoError(t, cur.All(ctx, &idx))
	found := false
	for _, i := range idx {
		if i["name"] == "idx_gym_members_gym_seat_status" {
			found = true
		}
	}
	assert.True(t, found)
}
