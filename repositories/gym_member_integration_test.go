//go:build integration

package repositories_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
)

// gymTestHost is the only server these tests may talk to (a throwaway container behind an SSH tunnel).
const gymTestHost = "127.0.0.1:27999"

// gymTestDB connects to GYM_TEST_MONGO_URI and returns a fresh database named gymtest_<random>.
// The database is dropped at the end of the test; no other database is ever touched.
func gymTestDB(t *testing.T) *mongo.Database {
	t.Helper()
	uri := os.Getenv("GYM_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("GYM_TEST_MONGO_URI not set")
	}
	require.Contains(t, uri, gymTestHost, "gym integration tests only run against the throwaway server")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	require.NoError(t, err)

	b := make([]byte, 6)
	_, err = rand.Read(b)
	require.NoError(t, err)
	name := "gymtest_" + hex.EncodeToString(b)
	require.True(t, strings.HasPrefix(name, "gymtest_"))
	database := client.Database(name)
	t.Cleanup(func() {
		dctx, dcancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer dcancel()
		if strings.HasPrefix(database.Name(), "gymtest_") { // never drop anything else
			_ = database.Drop(dctx)
		}
		_ = client.Disconnect(dctx)
	})
	return database
}

func gymMemberFixture(t *testing.T) (*mongo.Database, *repositories.GymMemberRepository) {
	t.Helper()
	database := gymTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	require.NoError(t, repositories.InitializeIndexes(ctx, database))
	return database, repositories.NewGymMemberRepository(database)
}

func activeRows(t *testing.T, database *mongo.Database, filter bson.M) []bson.M {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	filter["status"] = "active"
	cur, err := database.Collection("gym_members").Find(ctx, filter)
	require.NoError(t, err)
	var out []bson.M
	require.NoError(t, cur.All(ctx, &out))
	return out
}

func memberCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 20*time.Second)
}

func TestGymMemberIT_Indexes(t *testing.T) {
	database := gymTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	require.NoError(t, repositories.InitializeIndexes(ctx, database), "fresh database")
	require.NoError(t, repositories.InitializeIndexes(ctx, database), "second run is idempotent, no IndexOptionsConflict")
	ok, missing := repositories.VerifyIndexes(ctx, database)
	require.True(t, ok, "missing: %v", missing)

	cur, err := database.Collection("gym_members").Indexes().List(ctx)
	require.NoError(t, err)
	var idx []bson.M
	require.NoError(t, cur.All(ctx, &idx))
	byName := map[string]bson.M{}
	for _, i := range idx {
		byName[i["name"].(string)] = i
	}

	coach := byName["idx_gym_members_coach_unique"]
	require.NotNil(t, coach)
	assert.Equal(t, true, coach["unique"])
	assert.Equal(t, bson.M{"status": "active", "coach_slot": true}, toM(coach["partialFilterExpression"]))

	pair := byName["idx_gym_members_gym_user_unique"]
	require.NotNil(t, pair)
	assert.Equal(t, true, pair["unique"])
	assert.Equal(t, bson.M{"status": "active"}, toM(pair["partialFilterExpression"]))

	assert.NotNil(t, byName["idx_gym_members_gym_role_status"])
	assert.NotNil(t, byName["idx_gym_members_supervisor"])
}

func toM(v interface{}) bson.M {
	switch x := v.(type) {
	case bson.M:
		return x
	case bson.D:
		return x.Map()
	}
	return nil
}

func TestGymMemberIT_OneGymPerCoach(t *testing.T) {
	database, _ := gymMemberFixture(t)
	coll := database.Collection("gym_members")
	ctx, cancel := memberCtx()
	defer cancel()

	_, err := coll.InsertOne(ctx, bson.M{"gym_id": "g1", "user_id": "u1", "role": "coach", "status": "active", "coach_slot": true})
	require.NoError(t, err)

	_, err = coll.InsertOne(ctx, bson.M{"gym_id": "g2", "user_id": "u1", "role": "head_coach", "status": "active", "coach_slot": true})
	require.Error(t, err)
	assert.True(t, mongo.IsDuplicateKeyError(err), "second active coach_slot row for one user is E11000: %v", err)

	t.Run("owner rows are unconstrained", func(t *testing.T) {
		for _, g := range []string{"g1", "g2", "g3"} {
			_, err := coll.InsertOne(ctx, bson.M{"gym_id": g, "user_id": "own1", "role": "owner", "status": "active"})
			require.NoError(t, err, g)
		}
		// an owner row alongside the user's coach row in another gym is fine too
		_, err := coll.InsertOne(ctx, bson.M{"gym_id": "g9", "user_id": "u1", "role": "owner", "status": "active"})
		require.NoError(t, err)
	})

	t.Run("a left row does not block a new active row", func(t *testing.T) {
		_, err := coll.InsertOne(ctx, bson.M{"gym_id": "g5", "user_id": "u2", "role": "coach", "status": "left"})
		require.NoError(t, err)
		_, err = coll.InsertOne(ctx, bson.M{"gym_id": "g6", "user_id": "u2", "role": "coach", "status": "active", "coach_slot": true})
		require.NoError(t, err)
		_, err = coll.InsertOne(ctx, bson.M{"gym_id": "g5", "user_id": "u2", "role": "coach", "status": "left"})
		require.NoError(t, err, "history rows are not unique")
	})

	t.Run("one active row per gym and user", func(t *testing.T) {
		_, err := coll.InsertOne(ctx, bson.M{"gym_id": "g1", "user_id": "own1", "role": "owner", "status": "active"})
		require.Error(t, err)
		assert.True(t, mongo.IsDuplicateKeyError(err))
	})
}

func TestGymMemberIT_UpsertActive(t *testing.T) {
	database, repo := gymMemberFixture(t)

	t.Run("insert then refresh keeps added_by, source and joined_at", func(t *testing.T) {
		ctx, cancel := memberCtx()
		defer cancel()
		joined := time.Now().Add(-time.Hour).Truncate(time.Millisecond)
		require.NoError(t, repo.UpsertActive(ctx, &db.GymMember{GymID: "g1", UserID: "c1", Role: db.GymMemberRoleCoach,
			SupervisorID: "h1", AddedBy: "admin1", Source: db.GymMemberSourceAdmin, JoinedAt: joined}))
		rows := activeRows(t, database, bson.M{"user_id": "c1"})
		require.Len(t, rows, 1)
		assert.Equal(t, true, rows[0]["coach_slot"])
		assert.Equal(t, false, rows[0]["seat_assigned"])
		assert.Equal(t, "admin1", rows[0]["added_by"])

		require.NoError(t, repo.UpsertActive(ctx, &db.GymMember{GymID: "g1", UserID: "c1", Role: db.GymMemberRoleCoach,
			SupervisorID: "h2", AddedBy: "someone-else", Source: db.GymMemberSourceInvite, JoinedAt: time.Now()}))
		rows = activeRows(t, database, bson.M{"user_id": "c1"})
		require.Len(t, rows, 1)
		assert.Equal(t, "h2", rows[0]["supervisor_id"], "supervisor refreshed")
		assert.Equal(t, "admin1", rows[0]["added_by"], "added_by kept")
		assert.Equal(t, "admin", rows[0]["source"], "source kept")
		assert.True(t, joined.Equal(rows[0]["joined_at"].(primitive.DateTime).Time()), "joined_at kept")
	})

	t.Run("owner upsert combines $set, $unset and $setOnInsert on insert", func(t *testing.T) {
		ctx, cancel := memberCtx()
		defer cancel()
		require.NoError(t, repo.UpsertActive(ctx, &db.GymMember{GymID: "g1", UserID: "own1", Role: db.GymMemberRoleOwner,
			AddedBy: "admin1", Source: db.GymMemberSourceAdmin}))
		rows := activeRows(t, database, bson.M{"user_id": "own1"})
		require.Len(t, rows, 1)
		assert.Equal(t, "owner", rows[0]["role"])
		assert.NotContains(t, rows[0], "coach_slot")
		assert.NotContains(t, rows[0], "supervisor_id")
		assert.Equal(t, false, rows[0]["seat_assigned"])
	})

	t.Run("moving between gyms closes the old row and unsets coach_slot", func(t *testing.T) {
		ctx, cancel := memberCtx()
		defer cancel()
		require.NoError(t, repo.UpsertActive(ctx, &db.GymMember{GymID: "g2", UserID: "c1", Role: db.GymMemberRoleCoach,
			SupervisorID: "h9", AddedBy: "admin1", Source: db.GymMemberSourceAdmin}))
		rows := activeRows(t, database, bson.M{"user_id": "c1"})
		require.Len(t, rows, 1)
		assert.Equal(t, "g2", rows[0]["gym_id"])

		var old bson.M
		require.NoError(t, database.Collection("gym_members").FindOne(ctx,
			bson.M{"user_id": "c1", "gym_id": "g1"}).Decode(&old))
		assert.Equal(t, "left", old["status"])
		assert.NotNil(t, old["left_at"])
		assert.NotContains(t, old, "coach_slot")
	})

	t.Run("concurrent upserts of one (gym, user) leave exactly one active row", func(t *testing.T) {
		ctx, cancel := memberCtx()
		defer cancel()
		const n = 8
		errs := make(chan error, n)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				errs <- repo.UpsertActive(ctx, &db.GymMember{GymID: "gc", UserID: "race1", Role: db.GymMemberRoleHeadCoach,
					AddedBy: "a", Source: db.GymMemberSourceAdmin})
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			assert.NoError(t, err, "the duplicate-key retry resolves the race")
		}
		assert.Len(t, activeRows(t, database, bson.M{"user_id": "race1"}), 1)
	})
}

func TestGymMemberIT_EnsureAndFixRole(t *testing.T) {
	database, repo := gymMemberFixture(t)
	ctx, cancel := memberCtx()
	defer cancel()

	m := &db.GymMember{GymID: "g1", UserID: "c1", Role: db.GymMemberRoleCoach, SupervisorID: "h1",
		AddedBy: "admin1", Source: db.GymMemberSourceLegacyTeam, JoinedAt: time.Now().Add(-time.Hour)}
	created, err := repo.EnsureActive(ctx, m)
	require.NoError(t, err)
	assert.True(t, created)

	other := *m
	other.Role, other.SupervisorID, other.AddedBy = db.GymMemberRoleHeadCoach, "hX", "evil"
	created, err = repo.EnsureActive(ctx, &other)
	require.NoError(t, err)
	assert.False(t, created, "EnsureActive never changes or recreates an existing row")
	rows := activeRows(t, database, bson.M{"user_id": "c1"})
	require.Len(t, rows, 1)
	assert.Equal(t, "coach", rows[0]["role"])
	assert.Equal(t, "h1", rows[0]["supervisor_id"])
	assert.Equal(t, "admin1", rows[0]["added_by"])

	changed, err := repo.FixRole(ctx, m)
	require.NoError(t, err)
	assert.False(t, changed, "same values: ModifiedCount is 0")

	fix := *m
	fix.SupervisorID = "h2"
	changed, err = repo.FixRole(ctx, &fix)
	require.NoError(t, err)
	assert.True(t, changed)

	owner := *m
	owner.Role, owner.SupervisorID = db.GymMemberRoleOwner, ""
	changed, err = repo.FixRole(ctx, &owner)
	require.NoError(t, err)
	assert.True(t, changed)
	rows = activeRows(t, database, bson.M{"user_id": "c1"})
	require.Len(t, rows, 1)
	assert.Equal(t, "owner", rows[0]["role"])
	assert.NotContains(t, rows[0], "coach_slot", "$unset applied with $set in one update")
	assert.NotContains(t, rows[0], "supervisor_id")

	missing := &db.GymMember{GymID: "gx", UserID: "nobody", Role: db.GymMemberRoleCoach}
	changed, err = repo.FixRole(ctx, missing)
	require.NoError(t, err)
	assert.False(t, changed, "no row: nothing is created by FixRole")
	assert.Empty(t, activeRows(t, database, bson.M{"user_id": "nobody"}))
}

func TestGymMemberIT_DuplicateKeyDetection(t *testing.T) {
	database, _ := gymMemberFixture(t)
	coll := database.Collection("gym_members")
	ctx, cancel := memberCtx()
	defer cancel()

	// A concurrent-style upsert collision: an upsert whose filter finds no row inserts one that collides.
	_, err := coll.InsertOne(ctx, bson.M{"gym_id": "g1", "user_id": "u1", "role": "coach", "status": "active", "coach_slot": true})
	require.NoError(t, err)
	_, err = coll.UpdateOne(ctx, bson.M{"gym_id": "g2", "user_id": "u1", "status": "active"},
		bson.M{"$set": bson.M{"role": "coach", "coach_slot": true}}, options.Update().SetUpsert(true))
	require.Error(t, err)
	assert.True(t, mongo.IsDuplicateKeyError(err), "the error UpsertActive retries on is recognised: %v", err)
}
