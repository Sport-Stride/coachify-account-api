//go:build integration

package services_test

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
	"coachify-account-api/services"
)

// gymTestHost is the only server these tests may talk to (a throwaway container behind an SSH tunnel).
const gymTestHost = "127.0.0.1:27999"

type memberEnv struct {
	database *mongo.Database
	members  *repositories.GymMemberRepository
	gyms     *repositories.GymRepository
	team     *repositories.TeamRepository
}

// newMemberEnv creates a unique gymtest_<random> database, builds the indexes and drops the database at the end.
func newMemberEnv(t *testing.T) *memberEnv {
	t.Helper()
	uri := os.Getenv("GYM_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("GYM_TEST_MONGO_URI not set")
	}
	require.Contains(t, uri, gymTestHost, "gym integration tests only run against the throwaway server")

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	require.NoError(t, err)
	b := make([]byte, 6)
	_, err = rand.Read(b)
	require.NoError(t, err)
	database := client.Database("gymtest_" + hex.EncodeToString(b))
	t.Cleanup(func() {
		dctx, dcancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer dcancel()
		if strings.HasPrefix(database.Name(), "gymtest_") { // never drop anything else
			_ = database.Drop(dctx)
		}
		_ = client.Disconnect(dctx)
	})
	require.NoError(t, repositories.InitializeIndexes(ctx, database))

	team := repositories.NewTeamRepository(database, "users", "coach_clients")
	return &memberEnv{
		database: database,
		members:  repositories.NewGymMemberRepository(database),
		gyms:     repositories.NewGymRepository(database, team),
		team:     team,
	}
}

func (e *memberEnv) gym(t *testing.T, owner string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	g := &db.Gym{Name: "G " + owner, OwnerID: owner, Status: db.GymStatusActive, CreatedBy: "admin1",
		CreatedAt: time.Now().Truncate(time.Millisecond), UpdatedAt: time.Now()}
	require.NoError(t, e.gyms.CreateGym(ctx, g))
	return g.ID.Hex()
}

func (e *memberEnv) head(t *testing.T, gymID, head string) {
	t.Helper()
	_, err := e.database.Collection("gym_head_coaches").InsertOne(context.Background(), db.GymHeadCoach{
		GymID: gymID, HeadCoachID: head, AddedBy: "admin1", AddedByRole: db.GymAdderAdmin, AddedAt: time.Now().Truncate(time.Millisecond)})
	require.NoError(t, err)
}

func (e *memberEnv) coach(t *testing.T, head, coach string) {
	t.Helper()
	_, err := e.database.Collection("coach_team_members").InsertOne(context.Background(), db.TeamMember{
		HeadCoachID: head, CoachID: coach, AssignedBy: "admin1", AssignedByRole: db.TeamAssignerAdmin, AssignedAt: time.Now().Truncate(time.Millisecond)})
	require.NoError(t, err)
}

func (e *memberEnv) moveCoach(t *testing.T, coach, toHead string) {
	t.Helper()
	_, err := e.database.Collection("coach_team_members").UpdateOne(context.Background(),
		bson.M{"coach_id": coach}, bson.M{"$set": bson.M{"head_coach_id": toHead}})
	require.NoError(t, err)
}

func (e *memberEnv) count(t *testing.T, filter bson.M) int64 {
	t.Helper()
	n, err := e.database.Collection("gym_members").CountDocuments(context.Background(), filter)
	require.NoError(t, err)
	return n
}

func (e *memberEnv) run(t *testing.T, dry bool) services.BackfillReport {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	rep, err := services.RunMemberBackfill(ctx, e.members, dry, t.Logf)
	require.NoError(t, err)
	return rep
}

// seedLegacy: G1 (owner o1) with head h1, o1 also head of G1 (owner who is head), coaches c1 c2 under h1 and c3 under o1;
// G2 (owner o2) with head h2 and coach c4. Returns the gym ids.
func (e *memberEnv) seedLegacy(t *testing.T) (g1, g2 string) {
	t.Helper()
	g1, g2 = e.gym(t, "o1"), e.gym(t, "o2")
	e.head(t, g1, "h1")
	e.head(t, g1, "o1")
	e.head(t, g2, "h2")
	e.coach(t, "h1", "c1")
	e.coach(t, "h1", "c2")
	e.coach(t, "o1", "c3")
	e.coach(t, "h2", "c4")
	return g1, g2
}

func TestGymMemberIT_BackfillEndToEnd(t *testing.T) {
	e := newMemberEnv(t)
	g1, g2 := e.seedLegacy(t)

	t.Run("dry run writes nothing and reports would_* counts", func(t *testing.T) {
		rep := e.run(t, true)
		assert.Equal(t, int64(0), e.count(t, bson.M{}))
		assert.Equal(t, 8, rep.WouldCreate, "2 owners, 2 heads (the owner-head is one row), 4 coaches")
		assert.Equal(t, 0, rep.WouldClose)
		assert.Equal(t, 0, rep.WouldFix)
		assert.Equal(t, 0, rep.Created)
	})

	t.Run("live run creates every row with failed=0", func(t *testing.T) {
		rep := e.run(t, false)
		assert.Equal(t, 0, rep.Failed)
		assert.Equal(t, 8, rep.Created)
		assert.Equal(t, int64(8), e.count(t, bson.M{"status": "active"}))
		assert.Equal(t, int64(0), e.count(t, bson.M{"seat_assigned": true}))

		var o1 db.GymMember
		require.NoError(t, e.database.Collection("gym_members").FindOne(context.Background(),
			bson.M{"gym_id": g1, "user_id": "o1", "status": "active"}).Decode(&o1))
		assert.Equal(t, db.GymMemberRoleOwner, o1.Role, "owner wins over head coach")
		assert.False(t, o1.CoachSlot)
	})

	t.Run("second run is an empty plan and a fresh dry run closes nothing", func(t *testing.T) {
		rep := e.run(t, true)
		assert.Equal(t, 0, rep.WouldClose)
		assert.Equal(t, 0, rep.WouldFix)
		assert.Equal(t, 0, rep.WouldCreate)
		rep = e.run(t, false)
		assert.Equal(t, services.BackfillReport{Gyms: 2, Heads: 3, Team: 4, Desired: 8, ExistingActive: 8}, rep)
	})

	t.Run("a coach moved by a missed write is closed then created under the unique index", func(t *testing.T) {
		e.moveCoach(t, "c1", "h2") // legacy moves c1 from h1 (G1) to h2 (G2); no mirror ran
		dry := e.run(t, true)
		assert.Equal(t, 1, dry.WouldClose)
		assert.Equal(t, 1, dry.WouldCreate)

		rep := e.run(t, false)
		assert.Equal(t, 0, rep.Failed, "close first frees the one-gym-per-coach index")
		assert.Equal(t, 1, rep.Closed)
		assert.Equal(t, 1, rep.Created)
		var rows []db.GymMember
		cur, err := e.database.Collection("gym_members").Find(context.Background(), bson.M{"user_id": "c1", "status": "active"})
		require.NoError(t, err)
		require.NoError(t, cur.All(context.Background(), &rows))
		require.Len(t, rows, 1)
		assert.Equal(t, g2, rows[0].GymID)
		assert.Equal(t, "h2", rows[0].SupervisorID)

		again := e.run(t, true)
		assert.Equal(t, 0, again.WouldClose+again.WouldFix+again.WouldCreate)
	})

	t.Run("supervisor drift and a removed head are reconciled", func(t *testing.T) {
		e.moveCoach(t, "c2", "o1") // same gym, other head: a Fix, not a Close
		_, err := e.database.Collection("gym_head_coaches").DeleteOne(context.Background(), bson.M{"head_coach_id": "h2"})
		require.NoError(t, err) // h2 leaves G2: h2 and its coaches (c4, c1) lose their rows
		rep := e.run(t, false)
		assert.Equal(t, 0, rep.Failed)
		assert.Equal(t, 1, rep.Fixed)
		assert.Equal(t, 3, rep.Closed)
		assert.Equal(t, int64(0), e.count(t, bson.M{"user_id": "h2", "status": "active"}))
		again := e.run(t, true)
		assert.Equal(t, 0, again.WouldClose+again.WouldFix+again.WouldCreate)
	})
}

func TestGymMemberIT_ReadersAgree(t *testing.T) {
	e := newMemberEnv(t)
	g1, _ := e.seedLegacy(t)
	require.Equal(t, 0, e.run(t, false).Failed)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	legacy := services.NewLegacyMembershipAdapter(e.gyms, e.team)
	fromNew := services.NewRepoMembershipReader(e.members)

	for _, user := range []string{"o1", "o2", "h1", "h2", "c1", "c2", "c3", "c4", "nobody"} {
		a, err := legacy.MembershipsOf(ctx, user)
		require.NoError(t, err)
		b, err := fromNew.MembershipsOf(ctx, user)
		require.NoError(t, err)
		assert.Equal(t, shapes(a), shapes(b), "MembershipsOf %s", user)

		ma, err := legacy.MembershipOf(ctx, user, g1)
		require.NoError(t, err)
		mb, err := fromNew.MembershipOf(ctx, user, g1)
		require.NoError(t, err)
		assert.Equal(t, ma == nil, mb == nil, "MembershipOf %s", user)
		if ma != nil {
			assert.Equal(t, shapes([]db.GymMember{*ma}), shapes([]db.GymMember{*mb}))
		}
	}
	o1, err := fromNew.MembershipsOf(ctx, "o1")
	require.NoError(t, err)
	require.Len(t, o1, 1)
	assert.Equal(t, db.GymMemberRoleOwner, o1[0].Role)
}

// A real-Mongo store failure path: a legitimate row mirrored while the backfill runs is never closed.
func TestGymMemberIT_BackfillConcurrentWithMirrors(t *testing.T) {
	e := newMemberEnv(t)
	g1, _ := e.seedLegacy(t)
	require.Equal(t, 0, e.run(t, false).Failed)

	const n = 6
	var wg sync.WaitGroup
	start := make(chan struct{})
	failed := make(chan int, n+3)

	// Mirror side: legacy write first, then the dual-write upsert, as the services do.
	for i := 0; i < n; i++ {
		wg.Add(1)
		coach := "cc" + string(rune('a'+i))
		go func() {
			defer wg.Done()
			<-start
			e.coach(t, "h1", coach)
			err := e.members.UpsertActive(context.Background(), &db.GymMember{GymID: g1, UserID: coach, Role: db.GymMemberRoleCoach,
				SupervisorID: "h1", AddedBy: "admin1", Source: db.GymMemberSourceAdmin})
			if err != nil {
				t.Errorf("mirror upsert: %v", err)
			}
		}()
	}
	// Backfill side: three overlapping live runs.
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			rep, err := services.RunMemberBackfill(context.Background(), e.members, false, t.Logf)
			if err != nil {
				t.Errorf("backfill: %v", err)
				return
			}
			failed <- rep.Failed
		}()
	}
	close(start)
	wg.Wait()
	close(failed)
	for f := range failed {
		assert.Equal(t, 0, f, "no failed row (duplicate-key races on create are tolerated by EnsureActive)")
	}

	assert.Equal(t, int64(0), e.count(t, bson.M{"status": "left"}), "no valid active row was ever closed")
	final := e.run(t, true)
	assert.Equal(t, 0, final.WouldClose+final.WouldFix+final.WouldCreate)
	assert.Equal(t, int64(8+n), e.count(t, bson.M{"status": "active"}))
}

var _ = primitive.NilObjectID
