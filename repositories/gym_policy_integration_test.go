//go:build integration

package repositories_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
)

func policyFixture(t *testing.T) (*mongo.Database, *repositories.GymPolicyRepository) {
	t.Helper()
	database := gymTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	require.NoError(t, repositories.InitializeIndexes(ctx, database))
	return database, repositories.NewGymPolicyRepository(database)
}

func grantSet(caps map[string]db.GrantEffect) []db.PermissionGrant {
	out := make([]db.PermissionGrant, 0, len(caps))
	for c, e := range caps {
		out = append(out, db.PermissionGrant{Capability: c, Effect: e, GrantedBy: "o1", CreatedAt: time.Now()})
	}
	return out
}

func grantMap(t *testing.T, repo *repositories.GymPolicyRepository, gym, user string) map[string]db.GrantEffect {
	t.Helper()
	ctx, cancel := memberCtx()
	defer cancel()
	rows, err := repo.ListGrants(ctx, gym, user)
	require.NoError(t, err)
	out := map[string]db.GrantEffect{}
	for _, r := range rows {
		_, dup := out[r.Capability]
		require.False(t, dup, "duplicate grant row for %s", r.Capability)
		out[r.Capability] = r.Effect
	}
	return out
}

func TestGymPolicyIT_UniqueIndexes(t *testing.T) {
	database, repo := policyFixture(t)
	ctx, cancel := memberCtx()
	defer cancel()
	ok, missing := repositories.VerifyIndexes(ctx, database)
	require.True(t, ok, "missing: %v", missing)

	p := &db.GymRolePolicy{GymID: "g1", Role: db.GymMemberRoleCoach, Capabilities: map[string]bool{db.CapPlanManage: true}, UpdatedBy: "o1"}
	_, err := repo.UpsertRolePolicy(ctx, p, 0)
	require.NoError(t, err)
	// a raw second insert for the same (gym, role) hits the unique index
	_, err = database.Collection("gym_role_policies").InsertOne(ctx, bson.M{"gym_id": "g1", "role": "coach", "version": int64(1)})
	assert.True(t, mongo.IsDuplicateKeyError(err), "got %v", err)
	// other role and other gym are fine
	_, err = repo.UpsertRolePolicy(ctx, &db.GymRolePolicy{GymID: "g1", Role: db.GymMemberRoleHeadCoach, Capabilities: map[string]bool{}}, 0)
	require.NoError(t, err)
	_, err = repo.UpsertRolePolicy(ctx, &db.GymRolePolicy{GymID: "g2", Role: db.GymMemberRoleCoach, Capabilities: map[string]bool{}}, 0)
	require.NoError(t, err)

	grants := database.Collection("permission_grants")
	_, err = grants.InsertOne(ctx, bson.M{"gym_id": "g1", "user_id": "u1", "capability": db.CapPlanManage, "effect": "allow"})
	require.NoError(t, err)
	_, err = grants.InsertOne(ctx, bson.M{"gym_id": "g1", "user_id": "u1", "capability": db.CapPlanManage, "effect": "deny"})
	assert.True(t, mongo.IsDuplicateKeyError(err), "got %v", err)
	_, err = grants.InsertOne(ctx, bson.M{"gym_id": "g1", "user_id": "u2", "capability": db.CapPlanManage, "effect": "deny"})
	require.NoError(t, err)
}

func TestGymPolicyIT_RolePolicyVersioning(t *testing.T) {
	_, repo := policyFixture(t)
	ctx, cancel := memberCtx()
	defer cancel()
	mk := func(v bool) *db.GymRolePolicy {
		return &db.GymRolePolicy{GymID: "g1", Role: db.GymMemberRoleCoach, Capabilities: map[string]bool{db.CapPlanManage: v}, UpdatedBy: "o1"}
	}
	s1, err := repo.UpsertRolePolicy(ctx, mk(true), 0)
	require.NoError(t, err)
	assert.EqualValues(t, 1, s1.Version)
	_, err = repo.UpsertRolePolicy(ctx, mk(false), 0)
	assert.ErrorIs(t, err, repositories.ErrPolicyVersionConflict, "create over an existing policy")
	s2, err := repo.UpsertRolePolicy(ctx, mk(false), 1)
	require.NoError(t, err)
	assert.EqualValues(t, 2, s2.Version)
	assert.False(t, s2.Capabilities[db.CapPlanManage])
	_, err = repo.UpsertRolePolicy(ctx, mk(true), 1)
	assert.ErrorIs(t, err, repositories.ErrPolicyVersionConflict, "stale version")
	got, err := repo.GetRolePolicy(ctx, "g1", db.GymMemberRoleCoach)
	require.NoError(t, err)
	assert.EqualValues(t, 2, got.Version)
	none, err := repo.GetRolePolicy(ctx, "g1", db.GymMemberRoleHeadCoach)
	require.NoError(t, err)
	assert.Nil(t, none)
}

func TestGymPolicyIT_ConcurrentPolicyWritersOneWins(t *testing.T) {
	_, repo := policyFixture(t)
	ctx, cancel := memberCtx()
	defer cancel()
	base := &db.GymRolePolicy{GymID: "g1", Role: db.GymMemberRoleCoach, Capabilities: map[string]bool{}, UpdatedBy: "o1"}
	_, err := repo.UpsertRolePolicy(ctx, base, 0)
	require.NoError(t, err)

	for round, expected := range []int64{1, 2, 3} {
		const writers = 8
		var wins, conflicts, other int
		var mu sync.Mutex
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < writers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				p := &db.GymRolePolicy{GymID: "g1", Role: db.GymMemberRoleCoach, Capabilities: map[string]bool{db.CapPlanManage: i%2 == 0}, UpdatedBy: fmt.Sprintf("w%d", i)}
				_, err := repo.UpsertRolePolicy(ctx, p, expected)
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					wins++
				case err == repositories.ErrPolicyVersionConflict:
					conflicts++
				default:
					other++
				}
			}(i)
		}
		close(start)
		wg.Wait()
		assert.Equal(t, 1, wins, "round %d: exactly one writer wins", round)
		assert.Equal(t, writers-1, conflicts, "round %d", round)
		assert.Zero(t, other)
		got, err := repo.GetRolePolicy(ctx, "g1", db.GymMemberRoleCoach)
		require.NoError(t, err)
		assert.Equal(t, expected+1, got.Version)
	}
}

func TestGymPolicyIT_ConcurrentFirstCreateOneWins(t *testing.T) {
	_, repo := policyFixture(t)
	ctx, cancel := memberCtx()
	defer cancel()
	const writers = 8
	var wins, conflicts int
	var mu sync.Mutex
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := repo.UpsertRolePolicy(ctx, &db.GymRolePolicy{GymID: "g1", Role: db.GymMemberRoleCoach, Capabilities: map[string]bool{}}, 0)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				wins++
			} else if err == repositories.ErrPolicyVersionConflict {
				conflicts++
			} else {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	assert.Equal(t, 1, wins)
	assert.Equal(t, writers-1, conflicts)
}

func TestGymPolicyIT_ReplaceGrants(t *testing.T) {
	database, repo := policyFixture(t)
	ctx, cancel := memberCtx()
	defer cancel()

	require.NoError(t, repo.ReplaceGrants(ctx, "g1", "u1", grantSet(map[string]db.GrantEffect{
		db.CapPlanManage: db.GrantAllow, db.CapClientAssign: db.GrantAllow, db.CapLibraryRead: db.GrantDeny})))
	assert.Equal(t, map[string]db.GrantEffect{db.CapPlanManage: db.GrantAllow, db.CapClientAssign: db.GrantAllow, db.CapLibraryRead: db.GrantDeny},
		grantMap(t, repo, "g1", "u1"))
	// another member and another gym must be untouched by every replace of u1
	require.NoError(t, repo.ReplaceGrants(ctx, "g1", "u2", grantSet(map[string]db.GrantEffect{db.CapPlanManage: db.GrantDeny})))
	require.NoError(t, repo.ReplaceGrants(ctx, "g2", "u1", grantSet(map[string]db.GrantEffect{db.CapPlanManage: db.GrantDeny})))

	// replace: keep one (effect flips), add one, drop two stale
	require.NoError(t, repo.ReplaceGrants(ctx, "g1", "u1", grantSet(map[string]db.GrantEffect{
		db.CapPlanManage: db.GrantDeny, db.CapWorkoutWrite: db.GrantAllow})))
	assert.Equal(t, map[string]db.GrantEffect{db.CapPlanManage: db.GrantDeny, db.CapWorkoutWrite: db.GrantAllow}, grantMap(t, repo, "g1", "u1"))
	assert.Equal(t, map[string]db.GrantEffect{db.CapPlanManage: db.GrantDeny}, grantMap(t, repo, "g1", "u2"))
	assert.Equal(t, map[string]db.GrantEffect{db.CapPlanManage: db.GrantDeny}, grantMap(t, repo, "g2", "u1"))

	// created_at and _id of a kept row survive a replace (setOnInsert)
	var before, after bson.M
	col := database.Collection("permission_grants")
	filter := bson.M{"gym_id": "g1", "user_id": "u1", "capability": db.CapPlanManage}
	require.NoError(t, col.FindOne(ctx, filter).Decode(&before))
	time.Sleep(20 * time.Millisecond)
	require.NoError(t, repo.ReplaceGrants(ctx, "g1", "u1", grantSet(map[string]db.GrantEffect{db.CapPlanManage: db.GrantAllow})))
	require.NoError(t, col.FindOne(ctx, filter).Decode(&after))
	assert.Equal(t, before["created_at"], after["created_at"])
	assert.Equal(t, before["_id"], after["_id"])

	// empty set clears the member only
	require.NoError(t, repo.ReplaceGrants(ctx, "g1", "u1", nil))
	assert.Empty(t, grantMap(t, repo, "g1", "u1"))
	assert.Len(t, grantMap(t, repo, "g1", "u2"), 1)
}

// Readers polling during replaces between two non-empty sets must never see an empty set.
func TestGymPolicyIT_ReplaceGrantsNeverEmptyDuringReplace(t *testing.T) {
	_, repo := policyFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	setA := map[string]db.GrantEffect{db.CapPlanManage: db.GrantAllow, db.CapClientAssign: db.GrantAllow}
	setB := map[string]db.GrantEffect{db.CapLibraryRead: db.GrantDeny, db.CapWorkoutWrite: db.GrantAllow}
	require.NoError(t, repo.ReplaceGrants(ctx, "g1", "u1", grantSet(setA)))

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var empties, reads int
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			rows, err := repo.ListGrants(ctx, "g1", "u1")
			if err != nil {
				t.Errorf("read: %v", err)
				return
			}
			reads++
			if len(rows) == 0 {
				empties++
			}
		}
	}()
	for i := 0; i < 40; i++ {
		set := setA
		if i%2 == 0 {
			set = setB
		}
		require.NoError(t, repo.ReplaceGrants(ctx, "g1", "u1", grantSet(set)))
	}
	close(stop)
	wg.Wait()
	t.Logf("reads=%d empties=%d", reads, empties)
	assert.Zero(t, empties, "member had no overrides during a replace")
}

func TestGymPolicyIT_ConcurrentReplaceGrantsConverge(t *testing.T) {
	_, repo := policyFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	sets := []map[string]db.GrantEffect{
		{db.CapPlanManage: db.GrantAllow, db.CapClientAssign: db.GrantAllow, db.CapLibraryRead: db.GrantDeny},
		{db.CapPlanManage: db.GrantDeny, db.CapWorkoutWrite: db.GrantAllow},
		{db.CapClientAssign: db.GrantDeny, db.CapProgramWrite: db.GrantAllow, db.CapPlanManage: db.GrantAllow},
	}
	const writers = 12
	start := make(chan struct{})
	errs := make([]error, writers)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = repo.ReplaceGrants(ctx, "g1", "u1", grantSet(sets[i%len(sets)]))
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil && mongo.IsDuplicateKeyError(err) {
			// a clean retry is the documented recovery; it must then succeed
			assert.NoError(t, repo.ReplaceGrants(ctx, "g1", "u1", grantSet(sets[i%len(sets)])), "retry after duplicate key")
			t.Logf("writer %d hit duplicate key and retried", i)
		} else {
			assert.NoError(t, err, "writer %d", i)
		}
	}
	// grantMap fails on a duplicate row. Interleaved unordered replaces may leave a mix, so a final
	// sequential replace must converge to exactly the requested set.
	_ = grantMap(t, repo, "g1", "u1")
	require.NoError(t, repo.ReplaceGrants(ctx, "g1", "u1", grantSet(sets[1])))
	assert.Equal(t, sets[1], grantMap(t, repo, "g1", "u1"))
}

func TestGymPolicyIT_BumpAuthzVersionAtomic(t *testing.T) {
	database, repo := policyFixture(t)
	ctx, cancel := memberCtx()
	defer cancel()
	oid := primitive.NewObjectID()
	_, err := database.Collection("gyms").InsertOne(ctx, bson.M{"_id": oid, "name": "G"})
	require.NoError(t, err)
	gym := oid.Hex()

	v, err := repo.GetAuthzVersion(ctx, gym)
	require.NoError(t, err)
	assert.Zero(t, v, "unset counter reads 0")

	const bumps = 50
	seen := make([]int64, bumps)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < bumps; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			n, err := repo.BumpAuthzVersion(ctx, gym)
			assert.NoError(t, err)
			seen[i] = n
		}(i)
	}
	close(start)
	wg.Wait()
	v, err = repo.GetAuthzVersion(ctx, gym)
	require.NoError(t, err)
	assert.EqualValues(t, bumps, v, "every concurrent bump counted")
	uniq := map[int64]bool{}
	for _, n := range seen {
		uniq[n] = true
	}
	assert.Len(t, uniq, bumps, "every bump returned a distinct value")

	// unknown or malformed gym: no error, 0, nothing created
	n, err := repo.BumpAuthzVersion(ctx, primitive.NewObjectID().Hex())
	require.NoError(t, err)
	assert.Zero(t, n)
	n, err = repo.BumpAuthzVersion(ctx, "not-an-oid")
	require.NoError(t, err)
	assert.Zero(t, n)
}
