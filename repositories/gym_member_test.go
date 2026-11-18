package repositories

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"

	"coachify-account-api/models/db"
)

func TestRepoOpTimeoutIsTenSeconds(t *testing.T) {
	assert.Equal(t, 10*time.Second, repoOpTimeout, "defined in repositories/team.go")
}

func TestGymMemberIndexes(t *testing.T) {
	byName := map[string]*struct {
		unique  bool
		partial interface{}
	}{}
	for _, m := range gymMemberIndexes() {
		require.NotNil(t, m.Options.Name)
		e := &struct {
			unique  bool
			partial interface{}
		}{partial: m.Options.PartialFilterExpression}
		if m.Options.Unique != nil {
			e.unique = *m.Options.Unique
		}
		byName[*m.Options.Name] = e
	}
	require.Len(t, byName, 4)

	coach := byName["idx_gym_members_coach_unique"]
	require.NotNil(t, coach, "index name is kept")
	assert.True(t, coach.unique)
	assert.Equal(t, bson.M{"status": "active", "coach_slot": true}, coach.partial, "plain equality only, no $in")

	pair := byName["idx_gym_members_gym_user_unique"]
	require.NotNil(t, pair)
	assert.True(t, pair.unique)
	assert.Equal(t, bson.M{"status": "active"}, pair.partial)
	assert.NotNil(t, byName["idx_gym_members_gym_role_status"])
	assert.NotNil(t, byName["idx_gym_members_supervisor"])
}

func TestCoachSlot(t *testing.T) {
	assert.True(t, coachSlot(db.GymMemberRoleHeadCoach))
	assert.True(t, coachSlot(db.GymMemberRoleCoach))
	assert.False(t, coachSlot(db.GymMemberRoleOwner))
}

func TestUpsertUpdate(t *testing.T) {
	joined := time.Now()
	m := &db.GymMember{GymID: "g", UserID: "c1", Role: db.GymMemberRoleCoach, SupervisorID: "h1", AddedBy: "admin1", Source: db.GymMemberSourceAdmin}
	u := upsertUpdate(m, joined)

	assert.Equal(t, bson.M{"role": db.GymMemberRoleCoach, "supervisor_id": "h1", "coach_slot": true}, u["$set"])
	assert.Equal(t, bson.M{"added_by": "admin1", "source": db.GymMemberSourceAdmin, "joined_at": joined, "seat_assigned": false}, u["$setOnInsert"],
		"added_by and source are written on insert only")
	assert.NotContains(t, u["$set"], "added_by")
	assert.NotContains(t, u["$set"], "source")
	assert.NotContains(t, u, "$unset")

	owner := upsertUpdate(&db.GymMember{Role: db.GymMemberRoleOwner}, joined)
	assert.Equal(t, bson.M{"supervisor_id": "", "coach_slot": ""}, owner["$unset"], "an owner has no supervisor and no coach slot")
	assert.Equal(t, bson.M{"role": db.GymMemberRoleOwner}, owner["$set"])
}

func TestEnsureAndLeaveUpdates(t *testing.T) {
	at := time.Now()
	e := ensureUpdate(&db.GymMember{Role: db.GymMemberRoleHeadCoach, JoinedAt: at, AddedBy: "o1", Source: db.GymMemberSourceLegacyHead})
	assert.Len(t, e, 1, "insert only")
	assert.Equal(t, bson.M{"role": db.GymMemberRoleHeadCoach, "coach_slot": true, "joined_at": at, "added_by": "o1",
		"source": db.GymMemberSourceLegacyHead, "seat_assigned": false}, e["$setOnInsert"])

	l := leaveUpdate(at)
	assert.Equal(t, bson.M{"status": db.GymMemberStatusLeft, "left_at": at}, l["$set"])
	assert.Equal(t, bson.M{"coach_slot": ""}, l["$unset"], "leaving frees the coach slot")
}
