//go:build integration

package repositories_test

import (
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
)

func flatMember(gym, user string, role db.GymMemberRole, joined time.Time) *db.GymMember {
	return &db.GymMember{GymID: gym, UserID: user, Role: role, JoinedAt: joined, AddedBy: "o1", Source: db.GymMemberSourceInvite}
}

// insertUser stores a user document with PII fields, to prove the member list never returns them.
func insertUser(t *testing.T, database *mongo.Database, id, first, last string) {
	t.Helper()
	ctx, cancel := memberCtx()
	defer cancel()
	_, err := database.Collection("users").InsertOne(ctx, bson.M{
		"externalid": id, "firstname": first, "lastname": last, "profile_picture": "pic-" + id,
		"email": id + "@example.test", "phone_number": "+21600000000", "password": "hash",
	})
	require.NoError(t, err)
}

func TestGymMemberFlatIT_AddActiveDuplicates(t *testing.T) {
	_, repo := gymMemberFixture(t)
	ctx, cancel := memberCtx()
	defer cancel()
	now := time.Now()

	require.NoError(t, repo.AddActive(ctx, flatMember("g1", "c1", db.GymMemberRoleCoach, now)))
	got, err := repo.Get(ctx, "c1", "g1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.True(t, got.CoachSlot)
	assert.False(t, got.SeatAssigned)
	assert.Equal(t, db.GymMemberStatusActive, got.Status)

	assert.ErrorIs(t, repo.AddActive(ctx, flatMember("g1", "c1", db.GymMemberRoleCoach, now)), repositories.ErrAlreadyMember,
		"same user twice in the same gym (gym_user index)")
	assert.ErrorIs(t, repo.AddActive(ctx, flatMember("g2", "c1", db.GymMemberRoleCoach, now)), repositories.ErrAlreadyMember,
		"coach already in another gym (idx_gym_members_coach_unique)")
	assert.ErrorIs(t, repo.AddActive(ctx, flatMember("g2", "c1", db.GymMemberRoleHeadCoach, now)), repositories.ErrAlreadyMember,
		"a head coach slot is the same coach slot")

	// an owner has no coach slot, so an owner row elsewhere is fine
	require.NoError(t, repo.AddActive(ctx, flatMember("g2", "c1", db.GymMemberRoleOwner, now)))

	rows, err := repo.ListByUser(ctx, "c1")
	require.NoError(t, err)
	assert.Len(t, rows, 2, "failed inserts left nothing behind")

	// after leaving, the coach may join elsewhere
	ok, err := repo.MarkLeft(ctx, "g1", "c1", now)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, repo.AddActive(ctx, flatMember("g3", "c1", db.GymMemberRoleCoach, now)))
}

func TestGymMemberFlatIT_SetSupervisor(t *testing.T) {
	_, repo := gymMemberFixture(t)
	ctx, cancel := memberCtx()
	defer cancel()
	now := time.Now()
	require.NoError(t, repo.AddActive(ctx, flatMember("g1", "c1", db.GymMemberRoleCoach, now)))
	require.NoError(t, repo.AddActive(ctx, flatMember("g1", "h1", db.GymMemberRoleHeadCoach, now)))

	ok, err := repo.SetSupervisor(ctx, "g1", "c1", "", "h1")
	require.NoError(t, err)
	assert.True(t, ok)
	got, _ := repo.Get(ctx, "c1", "g1")
	assert.Equal(t, "h1", got.SupervisorID)

	ok, err = repo.SetSupervisor(ctx, "g1", "c1", "wrong", "")
	require.NoError(t, err)
	assert.False(t, ok, "compare-and-set: stale previous supervisor does not apply")
	got, _ = repo.Get(ctx, "c1", "g1")
	assert.Equal(t, "h1", got.SupervisorID)

	ok, err = repo.SetSupervisor(ctx, "g1", "c1", "h1", "")
	require.NoError(t, err)
	assert.True(t, ok)
	got, _ = repo.Get(ctx, "c1", "g1")
	assert.Empty(t, got.SupervisorID)

	ok, err = repo.SetSupervisor(ctx, "g1", "h1", "", "c1")
	require.NoError(t, err)
	assert.False(t, ok, "only a coach row takes a supervisor")
	ok, err = repo.SetSupervisor(ctx, "g1", "nobody", "", "h1")
	require.NoError(t, err)
	assert.False(t, ok)
	ok, err = repo.SetSupervisor(ctx, "other-gym", "c1", "", "h1")
	require.NoError(t, err)
	assert.False(t, ok)

	_, err = repo.MarkLeft(ctx, "g1", "c1", now)
	require.NoError(t, err)
	ok, err = repo.SetSupervisor(ctx, "g1", "c1", "", "h1")
	require.NoError(t, err)
	assert.False(t, ok, "a left row is not touched")
}

func TestGymMemberFlatIT_ListByGymPage(t *testing.T) {
	database, repo := gymMemberFixture(t)
	ctx, cancel := memberCtx()
	defer cancel()
	base := time.Now().Add(-time.Hour).Truncate(time.Millisecond)

	// o1 owner, h1 head, c1 c2 under h1, c3 without supervisor, ghost without user document, one member of another
	// gym and one who left
	require.NoError(t, repo.AddActive(ctx, flatMember("g1", "o1", db.GymMemberRoleOwner, base)))
	require.NoError(t, repo.AddActive(ctx, flatMember("g1", "h1", db.GymMemberRoleHeadCoach, base.Add(time.Minute))))
	for i, id := range []string{"c1", "c2"} {
		m := flatMember("g1", id, db.GymMemberRoleCoach, base.Add(time.Duration(2+i)*time.Minute))
		m.SupervisorID = "h1"
		require.NoError(t, repo.AddActive(ctx, m))
	}
	require.NoError(t, repo.AddActive(ctx, flatMember("g1", "c3", db.GymMemberRoleCoach, base.Add(4*time.Minute))))
	require.NoError(t, repo.AddActive(ctx, flatMember("g1", "ghost", db.GymMemberRoleCoach, base.Add(5*time.Minute))))
	require.NoError(t, repo.AddActive(ctx, flatMember("g2", "x1", db.GymMemberRoleCoach, base)))
	require.NoError(t, repo.AddActive(ctx, flatMember("g1", "gone", db.GymMemberRoleCoach, base.Add(6*time.Minute))))
	_, err := repo.MarkLeft(ctx, "g1", "gone", time.Now())
	require.NoError(t, err)

	insertUser(t, database, "o1", "Olga", "Owner")
	insertUser(t, database, "h1", "Hugo", "Head")
	insertUser(t, database, "c1", "Cara", "One")
	insertUser(t, database, "c2", "Cyrus", "Two")
	insertUser(t, database, "c3", "Cleo", "Three")
	insertUser(t, database, "gone", "Gone", "Left")

	ids := func(rows []db.GymMemberRow) []string {
		out := []string{}
		for _, r := range rows {
			out = append(out, r.UserID)
		}
		return out
	}

	t.Run("names resolved, missing user gives empty strings, total counts active only", func(t *testing.T) {
		rows, total, err := repo.ListByGymPage(ctx, "g1", "", "", 1, 50)
		require.NoError(t, err)
		assert.EqualValues(t, 6, total)
		assert.Equal(t, []string{"o1", "h1", "c1", "c2", "c3", "ghost"}, ids(rows), "joined_at order")
		assert.Equal(t, "Olga", rows[0].Firstname)
		assert.Equal(t, "Owner", rows[0].Lastname)
		assert.Equal(t, "pic-o1", rows[0].ProfilePicture)
		assert.Equal(t, "h1", rows[2].SupervisorID)
		assert.Empty(t, rows[5].Firstname)
		assert.Empty(t, rows[5].Lastname)
		assert.Empty(t, rows[5].ProfilePicture)
	})

	t.Run("pagination bounds", func(t *testing.T) {
		rows, total, err := repo.ListByGymPage(ctx, "g1", "", "", 2, 4)
		require.NoError(t, err)
		assert.EqualValues(t, 6, total)
		assert.Equal(t, []string{"c3", "ghost"}, ids(rows))

		rows, total, err = repo.ListByGymPage(ctx, "g1", "", "", 3, 4)
		require.NoError(t, err)
		assert.EqualValues(t, 6, total, "total stays right past the last page")
		assert.NotNil(t, rows)
		assert.Empty(t, rows)

		rows, _, err = repo.ListByGymPage(ctx, "g1", "", "", 0, 2)
		require.NoError(t, err)
		assert.Equal(t, []string{"o1", "h1"}, ids(rows), "page below 1 is page 1")
	})

	t.Run("empty gym", func(t *testing.T) {
		rows, total, err := repo.ListByGymPage(ctx, "nope", "", "", 1, 10)
		require.NoError(t, err)
		assert.EqualValues(t, 0, total)
		assert.NotNil(t, rows)
		assert.Empty(t, rows)
	})

	t.Run("scoped to a head coach", func(t *testing.T) {
		rows, total, err := repo.ListByGymPage(ctx, "g1", "h1", "", 1, 50)
		require.NoError(t, err)
		assert.EqualValues(t, 3, total)
		assert.Equal(t, []string{"h1", "c1", "c2"}, ids(rows))
	})

	t.Run("row type has no email or phone field", func(t *testing.T) {
		var walk func(rt reflect.Type)
		walk = func(rt reflect.Type) {
			for i := 0; i < rt.NumField(); i++ {
				f := rt.Field(i)
				if f.Anonymous {
					walk(f.Type)
					continue
				}
				tag := strings.ToLower(f.Tag.Get("bson") + f.Tag.Get("json") + f.Name)
				for _, bad := range []string{"email", "phone", "password", "address"} {
					assert.NotContains(t, tag, bad)
				}
			}
		}
		walk(reflect.TypeOf(db.GymMemberRow{}))
	})
}

func TestGymMemberFlatIT_GetRow(t *testing.T) {
	database, repo := gymMemberFixture(t)
	ctx, cancel := memberCtx()
	defer cancel()
	now := time.Now()
	require.NoError(t, repo.AddActive(ctx, flatMember("g1", "c1", db.GymMemberRoleCoach, now)))
	require.NoError(t, repo.AddActive(ctx, flatMember("g1", "ghost", db.GymMemberRoleCoach, now)))
	insertUser(t, database, "c1", "Cara", "One")

	row, err := repo.GetRow(ctx, "g1", "c1")
	require.NoError(t, err)
	require.NotNil(t, row)
	assert.Equal(t, "Cara", row.Firstname)
	assert.Equal(t, "pic-c1", row.ProfilePicture)
	assert.Equal(t, db.GymMemberRoleCoach, row.Role)

	row, err = repo.GetRow(ctx, "g1", "ghost")
	require.NoError(t, err)
	require.NotNil(t, row, "missing user document still returns the membership")
	assert.Empty(t, row.Firstname)

	row, err = repo.GetRow(ctx, "g1", "unknown")
	require.NoError(t, err)
	assert.Nil(t, row)
	row, err = repo.GetRow(ctx, "g2", "c1")
	require.NoError(t, err)
	assert.Nil(t, row, "other gym")

	_, err = repo.MarkLeft(ctx, "g1", "c1", now)
	require.NoError(t, err)
	row, err = repo.GetRow(ctx, "g1", "c1")
	require.NoError(t, err)
	assert.Nil(t, row, "left rows are not returned")
}

func TestGymMemberFlatIT_ListByGymPageRoleFilter(t *testing.T) {
	_, repo := gymMemberFixture(t)
	ctx, cancel := memberCtx()
	defer cancel()
	base := time.Now().Add(-time.Hour)
	require.NoError(t, repo.AddActive(ctx, flatMember("g1", "o1", db.GymMemberRoleOwner, base)))
	require.NoError(t, repo.AddActive(ctx, flatMember("g1", "h1", db.GymMemberRoleHeadCoach, base.Add(time.Minute))))
	for i, id := range []string{"c1", "c2", "c3"} {
		m := flatMember("g1", id, db.GymMemberRoleCoach, base.Add(time.Duration(2+i)*time.Minute))
		if id != "c3" {
			m.SupervisorID = "h1"
		}
		require.NoError(t, repo.AddActive(ctx, m))
	}

	rows, total, err := repo.ListByGymPage(ctx, "g1", "", "coach", 1, 2)
	require.NoError(t, err)
	assert.EqualValues(t, 3, total, "total follows the role filter, not the page")
	require.Len(t, rows, 2)
	assert.Equal(t, "c1", rows[0].UserID)
	assert.Equal(t, "c2", rows[1].UserID)

	rows, total, err = repo.ListByGymPage(ctx, "g1", "", "coach", 2, 2)
	require.NoError(t, err)
	assert.EqualValues(t, 3, total)
	require.Len(t, rows, 1)
	assert.Equal(t, "c3", rows[0].UserID)

	rows, total, err = repo.ListByGymPage(ctx, "g1", "", "owner", 1, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, rows, 1)

	rows, total, err = repo.ListByGymPage(ctx, "g1", "h1", "coach", 1, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total, "scope and role combine")
	assert.Len(t, rows, 2)

	rows, total, err = repo.ListByGymPage(ctx, "g1", "", "head_coach", 1, 10)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	assert.Equal(t, "h1", rows[0].UserID)
}

func TestGymMemberFlatIT_SetSupervisorLostRace(t *testing.T) {
	_, repo := gymMemberFixture(t)
	ctx, cancel := memberCtx()
	defer cancel()
	now := time.Now()
	require.NoError(t, repo.AddActive(ctx, flatMember("g1", "c1", db.GymMemberRoleCoach, now)))
	for _, h := range []string{"h1", "h2"} {
		require.NoError(t, repo.AddActive(ctx, flatMember("g1", h, db.GymMemberRoleHeadCoach, now)))
	}

	// two writers both read "no supervisor"; the first wins, the second lost the race and must not overwrite
	ok, err := repo.SetSupervisor(ctx, "g1", "c1", "", "h1")
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = repo.SetSupervisor(ctx, "g1", "c1", "", "h2")
	require.NoError(t, err)
	assert.False(t, ok, "stale previous supervisor: lost race")
	got, _ := repo.Get(ctx, "c1", "g1")
	assert.Equal(t, "h1", got.SupervisorID)
	assert.Equal(t, db.GymMemberSourceFlat, got.Source)

	// truly concurrent: exactly one of N wins from the same previous value
	var wg sync.WaitGroup
	wins := make(chan bool, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := repo.SetSupervisor(ctx, "g1", "c1", "h1", "h2")
			if err == nil {
				wins <- ok
			}
		}()
	}
	wg.Wait()
	close(wins)
	n := 0
	for ok := range wins {
		if ok {
			n++
		}
	}
	assert.Equal(t, 1, n, "exactly one compare-and-set applies")
}
