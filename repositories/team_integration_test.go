//go:build integration

package repositories_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
)

// Fixture ids.
const (
	hA = "head-a" // Active head coach, team: c1 c2 c3 + orphan
	hB = "head-b" // Blocked head coach, team: c4
	c1 = "c1"     // Zed,  Active,   login 1d ago, 3 clients (2 active, 2 new)
	c2 = "c2"     // Dana, Inactive, login 20d ago, 3 clients (1 active, 2 new, one client user missing)
	c3 = "c3"     // Eve,  Active,   never logged in, 0 clients, email contains a dot
	c4 = "c4"     // Frank, Active, team B, 1 client
	c5 = "c5"     // Gina, unassigned
)

type fixture struct {
	database *mongo.Database
	users    *repositories.UserRepository
	team     *repositories.TeamRepository
	now      time.Time
}

// setup connects to MONGO_TEST_URI, creates a unique database, builds the indexes as the app does and seeds data.
func setup(t *testing.T) *fixture {
	t.Helper()
	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		t.Skip("MONGO_TEST_URI not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	require.NoError(t, err)
	database := client.Database(fmt.Sprintf("hc_it_%d", time.Now().UnixNano()))
	t.Cleanup(func() {
		dctx, dcancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer dcancel()
		_ = database.Drop(dctx)
		_ = client.Disconnect(dctx)
	})

	// Same index bootstrap as app/app.go (includes the coach_team_members unique index).
	require.NoError(t, repositories.InitializeIndexes(ctx, database))
	ok, missing := repositories.VerifyIndexes(ctx, database)
	require.True(t, ok, "missing indexes: %v", missing)

	f := &fixture{
		database: database,
		users:    repositories.NewUserRepository(database.Collection("users")),
		team:     repositories.NewTeamRepository(database, "users", "coach_clients"),
		now:      time.Now(),
	}
	f.seed(t, ctx)
	return f
}

func (f *fixture) ago(d time.Duration) time.Time { return f.now.Add(-d).Truncate(time.Millisecond) }

func day(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }

func (f *fixture) seed(t *testing.T, ctx context.Context) {
	t.Helper()
	var users []interface{}
	u := func(id, first, last, email, role, status string, head bool, headSince *time.Time, login time.Time, updatedAgo time.Duration) {
		doc := bson.M{
			"externalid": id, "firstname": first, "lastname": last, "email": email,
			"role": role, "status": status, "last_login": login,
			"created_at": f.ago(day(200)), "updated_at": f.ago(updatedAgo),
		}
		if head {
			doc["is_head_coach"] = true
			doc["head_coach_since"] = *headSince
		} else if role == "coach" && id != c1 {
			doc["is_head_coach"] = false // some coaches carry false, c1 omits the field entirely
		}
		users = append(users, doc)
	}
	sinceA, sinceB := f.ago(day(30)), f.ago(day(10))
	// updated_at ascending age gives the default admin list order: hA hB c1 c2 c3 c4 c5.
	u(hA, "Alice", "Head", "alice@hctest", "coach", "Active", true, &sinceA, f.ago(day(1)), 1*time.Hour)
	u(hB, "Bob", "Boss", "bob@hctest", "coach", "Blocked", true, &sinceB, f.ago(day(1)), 2*time.Hour)
	u(c1, "Zed", "Alpha", "zed@hctest", "coach", "Active", false, nil, f.ago(day(1)), 3*time.Hour)
	u(c2, "Dana", "Beta", "dana@hctest", "coach", "Inactive", false, nil, f.ago(day(20)), 4*time.Hour)
	u(c3, "Eve", "Gamma", "eve.c@hctest", "coach", "Active", false, nil, time.Time{}, 5*time.Hour)
	u(c4, "Frank", "Delta", "frank@hctest", "coach", "Active", false, nil, f.ago(day(3)), 6*time.Hour)
	u(c5, "Gina", "Epsilon", "gina@hctest", "coach", "Active", false, nil, f.ago(day(2)), 7*time.Hour)
	u("admin1", "Root", "Admin", "root@hctest", "admin", "Active", false, nil, f.ago(day(1)), 8*time.Hour)
	// A nutritionist flagged as head coach must still be excluded everywhere.
	nutri := f.ago(day(5))
	u("nutri1", "Nina", "Nutri", "nina@hctest", "nutritionist", "Active", true, &nutri, f.ago(day(1)), 9*time.Hour)
	clientStatus := map[string]string{
		"cl1": "Active", "cl2": "Active", "cl3": "Inactive", "cl4": "Active",
		"cl5": "Inactive", "cl6": "Active", "cl7": "Active", "cl8": "Inactive",
	}
	for id, st := range clientStatus {
		u(id, "Client", id, id+"@hctest", "client", st, false, nil, f.ago(day(1)), 10*time.Hour)
	}
	_, err := f.database.Collection("users").InsertMany(ctx, users)
	require.NoError(t, err)

	link := func(coach, client string, age time.Duration) interface{} {
		return bson.M{"coach_id": coach, "client_id": client, "created_at": f.ago(age)}
	}
	_, err = f.database.Collection("coach_clients").InsertMany(ctx, []interface{}{
		link(c1, "cl1", day(5)), link(c1, "cl2", day(40)), link(c1, "cl3", day(10)),
		link(c2, "cl4", day(20)), link(c2, "cl5", day(60)), link(c2, "ghostclient", day(1)),
		link(c4, "cl6", day(1)),
		link(c5, "cl7", day(100)), link(c5, "cl8", day(2)),
	})
	require.NoError(t, err)

	asg := f.ago(day(7))
	member := func(coach, head string) interface{} {
		return bson.M{"coach_id": coach, "head_coach_id": head, "assigned_by": "admin1", "assigned_by_role": "admin", "assigned_at": asg}
	}
	_, err = f.database.Collection("coach_team_members").InsertMany(ctx, []interface{}{
		member(c1, hA), member(c2, hA), member(c3, hA), member("ghost-coach", hA), // orphan: no user doc
		member(c4, hB),
	})
	require.NoError(t, err)
}

func ids(rows []db.TeamCoachRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ExternalID)
	}
	return out
}

func rowByID(t *testing.T, rows []db.TeamCoachRow, id string) db.TeamCoachRow {
	t.Helper()
	for _, r := range rows {
		if r.ExternalID == id {
			return r
		}
	}
	t.Fatalf("row %s not found", id)
	return db.TeamCoachRow{}
}

func TestTeamIntegration_Upsert(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	now := time.Now()

	t.Run("assign new returns nil previous", func(t *testing.T) {
		prev, err := f.team.Upsert(ctx, &db.TeamMember{CoachID: c5, HeadCoachID: hA, AssignedBy: "admin1", AssignedByRole: "admin", AssignedAt: now})
		require.NoError(t, err)
		assert.Nil(t, prev)
		m, err := f.team.GetHeadOf(ctx, c5)
		require.NoError(t, err)
		require.NotNil(t, m)
		assert.Equal(t, hA, m.HeadCoachID)
	})

	t.Run("reassign returns previous head", func(t *testing.T) {
		prev, err := f.team.Upsert(ctx, &db.TeamMember{CoachID: c5, HeadCoachID: hB, AssignedBy: hB, AssignedByRole: "head_coach", AssignedAt: now})
		require.NoError(t, err)
		require.NotNil(t, prev)
		assert.Equal(t, hA, prev.HeadCoachID)
		m, err := f.team.GetHeadOf(ctx, c5)
		require.NoError(t, err)
		assert.Equal(t, hB, m.HeadCoachID)
		assert.Equal(t, "head_coach", m.AssignedByRole)
		n, err := f.database.Collection("coach_team_members").CountDocuments(ctx, bson.M{"coach_id": c5})
		require.NoError(t, err)
		assert.EqualValues(t, 1, n, "reassign must not create a second row")
	})

	t.Run("unique index rejects second row for same coach", func(t *testing.T) {
		_, err := f.database.Collection("coach_team_members").InsertOne(ctx, bson.M{"coach_id": c5, "head_coach_id": hA})
		require.Error(t, err)
		assert.True(t, mongo.IsDuplicateKeyError(err))
	})

	t.Run("concurrent first-time upserts leave exactly one row", func(t *testing.T) {
		const workers = 20
		var wg sync.WaitGroup
		errs := make([]error, workers)
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				head := hA
				if i%2 == 0 {
					head = hB
				}
				_, errs[i] = f.team.Upsert(ctx, &db.TeamMember{CoachID: "race-coach", HeadCoachID: head, AssignedBy: "admin1", AssignedByRole: "admin", AssignedAt: now})
			}(i)
		}
		wg.Wait()
		for i, err := range errs {
			assert.NoError(t, err, "worker %d", i)
		}
		n, err := f.database.Collection("coach_team_members").CountDocuments(ctx, bson.M{"coach_id": "race-coach"})
		require.NoError(t, err)
		assert.EqualValues(t, 1, n)
	})
}

func TestTeamIntegration_ListByHead(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	t.Run("default sort name, orphan excluded, total", func(t *testing.T) {
		rows, total, err := f.team.ListByHead(ctx, hA, db.TeamListQuery{})
		require.NoError(t, err)
		assert.Equal(t, 3, total)
		assert.Equal(t, []string{c2, c3, c1}, ids(rows)) // Dana, Eve, Zed
	})

	t.Run("pagination", func(t *testing.T) {
		rows, total, err := f.team.ListByHead(ctx, hA, db.TeamListQuery{Page: 1, Size: 2})
		require.NoError(t, err)
		assert.Equal(t, 3, total)
		assert.Equal(t, []string{c2, c3}, ids(rows))
		rows, total, err = f.team.ListByHead(ctx, hA, db.TeamListQuery{Page: 2, Size: 2})
		require.NoError(t, err)
		assert.Equal(t, 3, total)
		assert.Equal(t, []string{c1}, ids(rows))
		rows, total, err = f.team.ListByHead(ctx, hA, db.TeamListQuery{Page: 3, Size: 2})
		require.NoError(t, err)
		assert.Equal(t, 3, total)
		assert.Empty(t, rows)
	})

	t.Run("sort clients and last_login", func(t *testing.T) {
		rows, _, err := f.team.ListByHead(ctx, hA, db.TeamListQuery{Sort: "clients"})
		require.NoError(t, err)
		assert.Equal(t, []string{c1, c2, c3}, ids(rows)) // 3,3 (tie by externalid),0
		rows, total, err := f.team.ListByHead(ctx, hA, db.TeamListQuery{Sort: "clients", Page: 2, Size: 2})
		require.NoError(t, err)
		assert.Equal(t, 3, total)
		assert.Equal(t, []string{c3}, ids(rows))
		rows, _, err = f.team.ListByHead(ctx, hA, db.TeamListQuery{Sort: "last_login"})
		require.NoError(t, err)
		assert.Equal(t, []string{c1, c2, c3}, ids(rows)) // 1d, 20d, never
	})

	t.Run("status filter", func(t *testing.T) {
		rows, total, err := f.team.ListByHead(ctx, hA, db.TeamListQuery{Status: "Active"})
		require.NoError(t, err)
		assert.Equal(t, 2, total)
		assert.Equal(t, []string{c3, c1}, ids(rows))
		rows, total, err = f.team.ListByHead(ctx, hA, db.TeamListQuery{Status: "Inactive"})
		require.NoError(t, err)
		assert.Equal(t, 1, total)
		assert.Equal(t, []string{c2}, ids(rows))
		_, total, err = f.team.ListByHead(ctx, hA, db.TeamListQuery{Status: "Blocked"})
		require.NoError(t, err)
		assert.Equal(t, 0, total)
	})

	t.Run("q search and regex metachar safety", func(t *testing.T) {
		rows, total, err := f.team.ListByHead(ctx, hA, db.TeamListQuery{Q: "DANA"})
		require.NoError(t, err)
		assert.Equal(t, 1, total)
		assert.Equal(t, []string{c2}, ids(rows))
		rows, _, err = f.team.ListByHead(ctx, hA, db.TeamListQuery{Q: "gamma"}) // lastname
		require.NoError(t, err)
		assert.Equal(t, []string{c3}, ids(rows))
		rows, _, err = f.team.ListByHead(ctx, hA, db.TeamListQuery{Q: "zed@hctest"}) // email
		require.NoError(t, err)
		assert.Equal(t, []string{c1}, ids(rows))
		// "." must match a literal dot only (eve.c@hctest), not any char.
		rows, total, err = f.team.ListByHead(ctx, hA, db.TeamListQuery{Q: "."})
		require.NoError(t, err)
		assert.Equal(t, 1, total)
		assert.Equal(t, []string{c3}, ids(rows))
		for _, q := range []string{".*", "(", "[a-z]+", `\`, "a|d", "^Dana$"} {
			rows, total, err = f.team.ListByHead(ctx, hA, db.TeamListQuery{Q: q})
			require.NoError(t, err, "q=%q", q)
			assert.Equal(t, 0, total, "q=%q", q)
			assert.Empty(t, rows, "q=%q", q)
		}
		// q combined with status
		_, total, err = f.team.ListByHead(ctx, hA, db.TeamListQuery{Q: "dana", Status: "Active"})
		require.NoError(t, err)
		assert.Equal(t, 0, total)
	})

	t.Run("per-coach client stats", func(t *testing.T) {
		for _, sort := range []string{"", "clients", "last_login"} {
			rows, _, err := f.team.ListByHead(ctx, hA, db.TeamListQuery{Sort: sort})
			require.NoError(t, err)
			r1, r2, r3 := rowByID(t, rows, c1), rowByID(t, rows, c2), rowByID(t, rows, c3)
			assert.Equal(t, [3]int{3, 2, 2}, [3]int{r1.ClientCount, r1.ActiveClientCount, r1.NewClients30d}, "c1 sort=%q", sort)
			// c2: cl4 active 20d, cl5 inactive 60d, ghostclient (no user) 1d => not active
			assert.Equal(t, [3]int{3, 1, 2}, [3]int{r2.ClientCount, r2.ActiveClientCount, r2.NewClients30d}, "c2 sort=%q", sort)
			assert.Equal(t, [3]int{0, 0, 0}, [3]int{r3.ClientCount, r3.ActiveClientCount, r3.NewClients30d}, "c3 sort=%q", sort)
			assert.Equal(t, "eve.c@hctest", r3.Email)
			assert.False(t, r1.JoinedTeamAt.IsZero())
		}
		rows, _, err := f.team.ListByHead(ctx, hB, db.TeamListQuery{})
		require.NoError(t, err)
		require.Len(t, rows, 1)
		assert.Equal(t, [3]int{1, 1, 1}, [3]int{rows[0].ClientCount, rows[0].ActiveClientCount, rows[0].NewClients30d})
	})

	t.Run("unknown head and CountByHead parity", func(t *testing.T) {
		rows, total, err := f.team.ListByHead(ctx, "nobody", db.TeamListQuery{})
		require.NoError(t, err)
		assert.Equal(t, 0, total)
		assert.Empty(t, rows)
		for _, h := range []string{hA, hB, "nobody"} {
			_, total, err := f.team.ListByHead(ctx, h, db.TeamListQuery{})
			require.NoError(t, err)
			n, err := f.team.CountByHead(ctx, h)
			require.NoError(t, err)
			assert.Equal(t, total, n, "head %s", h)
		}
	})

	t.Run("GetRow", func(t *testing.T) {
		r, err := f.team.GetRow(ctx, hA, c1)
		require.NoError(t, err)
		require.NotNil(t, r)
		assert.Equal(t, [3]int{3, 2, 2}, [3]int{r.ClientCount, r.ActiveClientCount, r.NewClients30d})
		r, err = f.team.GetRow(ctx, hA, c4) // c4 is in team B
		require.NoError(t, err)
		assert.Nil(t, r)
	})
}

func TestTeamIntegration_Overview(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	o, err := f.team.Overview(ctx, hA)
	require.NoError(t, err)
	assert.Equal(t, db.TeamOverview{
		TeamSize: 3, ActiveCoaches: 2, InactiveCoaches14d: 2, // c2 (20d) + c3 (never)
		TotalClients: 6, ActiveClients: 3, NewClients30d: 4,
	}, *o)

	o, err = f.team.Overview(ctx, hB)
	require.NoError(t, err)
	assert.Equal(t, db.TeamOverview{TeamSize: 1, ActiveCoaches: 1, InactiveCoaches14d: 0, TotalClients: 1, ActiveClients: 1, NewClients30d: 1}, *o)

	o, err = f.team.Overview(ctx, "nobody")
	require.NoError(t, err)
	assert.Equal(t, db.TeamOverview{}, *o)
}

func TestTeamIntegration_LookupsAndDeletes(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	t.Run("IDsByHead includes orphan membership", func(t *testing.T) {
		got, err := f.team.IDsByHead(ctx, hA)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{c1, c2, c3, "ghost-coach"}, got)
		got, err = f.team.IDsByHead(ctx, "nobody")
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("GetHeadOf and IsMember", func(t *testing.T) {
		m, err := f.team.GetHeadOf(ctx, c4)
		require.NoError(t, err)
		require.NotNil(t, m)
		assert.Equal(t, hB, m.HeadCoachID)
		m, err = f.team.GetHeadOf(ctx, c5)
		require.NoError(t, err)
		assert.Nil(t, m)
		ok, err := f.team.IsMember(ctx, c1)
		require.NoError(t, err)
		assert.True(t, ok)
		ok, err = f.team.IsMember(ctx, c5)
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("DeleteByCoach", func(t *testing.T) {
		n, err := f.team.DeleteByCoach(ctx, c3)
		require.NoError(t, err)
		assert.EqualValues(t, 1, n)
		n, err = f.team.DeleteByCoach(ctx, c3)
		require.NoError(t, err)
		assert.EqualValues(t, 0, n)
		ok, _ := f.team.IsMember(ctx, c3)
		assert.False(t, ok)
	})

	t.Run("DeleteByHead", func(t *testing.T) {
		n, err := f.team.DeleteByHead(ctx, hB)
		require.NoError(t, err)
		assert.EqualValues(t, 1, n)
		ok, _ := f.team.IsMember(ctx, c4)
		assert.False(t, ok)
		n, err = f.team.DeleteByHead(ctx, hB)
		require.NoError(t, err)
		assert.EqualValues(t, 0, n)
	})

	t.Run("DeleteByAny removes as head and as member", func(t *testing.T) {
		// remaining under hA: c1 c2 ghost-coach; add hA as a member of someone else
		_, err := f.team.Upsert(ctx, &db.TeamMember{CoachID: hA, HeadCoachID: "other-head", AssignedBy: "admin1", AssignedByRole: "admin", AssignedAt: time.Now()})
		require.NoError(t, err)
		_, err = f.team.Upsert(ctx, &db.TeamMember{CoachID: c5, HeadCoachID: "other-head", AssignedBy: "admin1", AssignedByRole: "admin", AssignedAt: time.Now()})
		require.NoError(t, err)
		n, err := f.team.DeleteByAny(ctx, hA)
		require.NoError(t, err)
		assert.EqualValues(t, 4, n) // c1, c2, ghost-coach (head) + hA (member)
		got, err := f.team.IDsByHead(ctx, "other-head")
		require.NoError(t, err)
		assert.Equal(t, []string{c5}, got, "unrelated membership untouched")
		n, err = f.team.DeleteByAny(ctx, hA)
		require.NoError(t, err)
		assert.EqualValues(t, 0, n)
	})
}

func TestTeamIntegration_Audit(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	base := f.ago(3 * time.Hour)

	require.NoError(t, f.team.InsertAudit(ctx, &db.TeamAuditEntry{Action: db.TeamActionAssign, ActorID: "admin1", ActorRole: "admin", HeadCoachID: hA, CoachID: c1, CreatedAt: base}))
	require.NoError(t, f.team.InsertAudit(ctx, &db.TeamAuditEntry{Action: db.TeamActionAssign, ActorID: "admin1", ActorRole: "admin", HeadCoachID: hA, CoachID: c2, CreatedAt: base.Add(time.Hour)}))
	require.NoError(t, f.team.InsertAuditMany(ctx, []*db.TeamAuditEntry{
		{Action: db.TeamActionReassign, ActorID: "admin1", ActorRole: "admin", HeadCoachID: hB, CoachID: c4, PrevHeadCoachID: hA, CreatedAt: base.Add(2 * time.Hour)},
		{Action: db.TeamActionPromote, ActorID: "admin1", ActorRole: "admin", HeadCoachID: hB}, // CreatedAt defaulted to now
	}))
	require.NoError(t, f.team.InsertAuditMany(ctx, nil))

	t.Run("all newest first", func(t *testing.T) {
		got, total, err := f.team.ListAudit(ctx, "", "", 1, 10)
		require.NoError(t, err)
		assert.Equal(t, 4, total)
		require.Len(t, got, 4)
		assert.Equal(t, db.TeamActionPromote, got[0].Action)
		assert.False(t, got[0].CreatedAt.IsZero())
		assert.Equal(t, c4, got[1].CoachID)
		assert.Equal(t, hA, got[1].PrevHeadCoachID)
		assert.Equal(t, c2, got[2].CoachID)
		assert.Equal(t, c1, got[3].CoachID)
	})

	t.Run("filters", func(t *testing.T) {
		got, total, err := f.team.ListAudit(ctx, hA, "", 1, 10)
		require.NoError(t, err)
		assert.Equal(t, 2, total)
		assert.Equal(t, []string{c2, c1}, []string{got[0].CoachID, got[1].CoachID})
		got, total, err = f.team.ListAudit(ctx, "", c4, 1, 10)
		require.NoError(t, err)
		assert.Equal(t, 1, total)
		require.Len(t, got, 1)
		assert.Equal(t, hB, got[0].HeadCoachID)
		_, total, err = f.team.ListAudit(ctx, hA, c2, 1, 10)
		require.NoError(t, err)
		assert.Equal(t, 1, total)
		got, total, err = f.team.ListAudit(ctx, hA, c4, 1, 10)
		require.NoError(t, err)
		assert.Equal(t, 0, total)
		assert.Empty(t, got)
	})

	t.Run("pagination", func(t *testing.T) {
		got, total, err := f.team.ListAudit(ctx, "", "", 2, 3)
		require.NoError(t, err)
		assert.Equal(t, 4, total)
		require.Len(t, got, 1)
		assert.Equal(t, c1, got[0].CoachID)
	})
}

func TestUserRepositoryIntegration_GetCoachesByPage(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	yes, no := true, false

	rowIDs := func(rows []db.AdminCoachListRow) []string {
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.ExternalID)
		}
		return out
	}

	t.Run("no filter: coaches only, default order, enrichment", func(t *testing.T) {
		rows, total, err := f.users.GetCoachesByPage(ctx, 1, 20, db.AdminCoachFilter{})
		require.NoError(t, err)
		assert.Equal(t, 7, total) // admin, nutritionist and clients excluded
		assert.Equal(t, []string{hA, hB, c1, c2, c3, c4, c5}, rowIDs(rows))

		by := map[string]db.AdminCoachListRow{}
		for _, r := range rows {
			by[r.ExternalID] = r
		}
		a := by[hA]
		assert.True(t, a.IsHeadCoach)
		assert.Equal(t, 4, a.TeamSize) // raw membership count, includes the orphan row
		assert.Equal(t, "", a.HeadCoachID)
		assert.Equal(t, "", a.HeadCoachName)
		assert.Equal(t, 1, by[hB].TeamSize)
		assert.Equal(t, "Active", a.Status)
		assert.Equal(t, "Blocked", by[hB].Status)
		for _, id := range []string{c1, c2, c3} {
			r := by[id]
			assert.False(t, r.IsHeadCoach, id)
			assert.Equal(t, hA, r.HeadCoachID, id)
			assert.Equal(t, "Alice Head", r.HeadCoachName, id)
			assert.Equal(t, 0, r.TeamSize, id)
		}
		assert.Equal(t, hB, by[c4].HeadCoachID)
		assert.Equal(t, "Bob Boss", by[c4].HeadCoachName)
		assert.Equal(t, "", by[c5].HeadCoachID)
		assert.Equal(t, "", by[c5].HeadCoachName)
		assert.False(t, by[c5].IsHeadCoach)
	})

	t.Run("page and size preserved", func(t *testing.T) {
		rows, total, err := f.users.GetCoachesByPage(ctx, 2, 3, db.AdminCoachFilter{})
		require.NoError(t, err)
		assert.Equal(t, 7, total)
		assert.Equal(t, []string{c2, c3, c4}, rowIDs(rows))
		rows, _, err = f.users.GetCoachesByPage(ctx, 3, 3, db.AdminCoachFilter{})
		require.NoError(t, err)
		assert.Equal(t, []string{c5}, rowIDs(rows))
		rows, total, err = f.users.GetCoachesByPage(ctx, 9, 3, db.AdminCoachFilter{})
		require.NoError(t, err)
		assert.Equal(t, 7, total)
		assert.Empty(t, rows)
		// invalid page/size fall back to 1/20
		rows, total, err = f.users.GetCoachesByPage(ctx, 0, 0, db.AdminCoachFilter{})
		require.NoError(t, err)
		assert.Equal(t, 7, total)
		assert.Len(t, rows, 7)
	})

	t.Run("head_coach=true", func(t *testing.T) {
		rows, total, err := f.users.GetCoachesByPage(ctx, 1, 20, db.AdminCoachFilter{HeadCoach: &yes})
		require.NoError(t, err)
		assert.Equal(t, 2, total)
		assert.Equal(t, []string{hA, hB}, rowIDs(rows))
	})

	t.Run("head_coach=false includes missing and false flag", func(t *testing.T) {
		rows, total, err := f.users.GetCoachesByPage(ctx, 1, 20, db.AdminCoachFilter{HeadCoach: &no})
		require.NoError(t, err)
		assert.Equal(t, 5, total)
		assert.Equal(t, []string{c1, c2, c3, c4, c5}, rowIDs(rows))
		for _, r := range rows {
			assert.False(t, r.IsHeadCoach)
		}
	})

	t.Run("unassigned=true", func(t *testing.T) {
		rows, total, err := f.users.GetCoachesByPage(ctx, 1, 20, db.AdminCoachFilter{Unassigned: true})
		require.NoError(t, err)
		assert.Equal(t, 1, total)
		assert.Equal(t, []string{c5}, rowIDs(rows))
	})

	t.Run("unassigned reacts to membership changes", func(t *testing.T) {
		_, err := f.team.Upsert(ctx, &db.TeamMember{CoachID: c5, HeadCoachID: hA, AssignedBy: "admin1", AssignedByRole: "admin", AssignedAt: time.Now()})
		require.NoError(t, err)
		rows, total, err := f.users.GetCoachesByPage(ctx, 1, 20, db.AdminCoachFilter{Unassigned: true})
		require.NoError(t, err)
		assert.Equal(t, 0, total)
		assert.Empty(t, rows)
		_, err = f.team.DeleteByCoach(ctx, c2)
		require.NoError(t, err)
		rows, total, err = f.users.GetCoachesByPage(ctx, 1, 20, db.AdminCoachFilter{Unassigned: true})
		require.NoError(t, err)
		assert.Equal(t, 1, total)
		assert.Equal(t, []string{c2}, rowIDs(rows))
	})
}

func TestUserRepositoryIntegration_ListHeadCoaches(t *testing.T) {
	f := setup(t)
	ctx := context.Background()

	t.Run("lists head coaches newest promotion first with team_size", func(t *testing.T) {
		rows, total, err := f.users.ListHeadCoaches(ctx, 1, 20, "")
		require.NoError(t, err)
		assert.Equal(t, 2, total) // nutritionist flagged head is excluded
		require.Len(t, rows, 2)
		assert.Equal(t, hB, rows[0].ExternalID) // since 10d ago, newer than hA (30d)
		assert.Equal(t, hA, rows[1].ExternalID)
		assert.Equal(t, 1, rows[0].TeamSize)
		assert.Equal(t, 4, rows[1].TeamSize) // raw membership count incl. orphan
		assert.Equal(t, "Blocked", rows[0].Status)
		require.NotNil(t, rows[1].HeadCoachSince)
	})

	t.Run("pagination", func(t *testing.T) {
		rows, total, err := f.users.ListHeadCoaches(ctx, 2, 1, "")
		require.NoError(t, err)
		assert.Equal(t, 2, total)
		require.Len(t, rows, 1)
		assert.Equal(t, hA, rows[0].ExternalID)
	})

	t.Run("q search and metachar safety", func(t *testing.T) {
		rows, total, err := f.users.ListHeadCoaches(ctx, 1, 20, "ALICE")
		require.NoError(t, err)
		assert.Equal(t, 1, total)
		require.Len(t, rows, 1)
		assert.Equal(t, hA, rows[0].ExternalID)
		rows, total, err = f.users.ListHeadCoaches(ctx, 1, 20, "boss")
		require.NoError(t, err)
		assert.Equal(t, 1, total)
		assert.Equal(t, hB, rows[0].ExternalID)
		for _, q := range []string{".", ".*", "(", "a|b"} {
			rows, total, err = f.users.ListHeadCoaches(ctx, 1, 20, q)
			require.NoError(t, err, "q=%q", q)
			assert.Equal(t, 0, total, "q=%q", q)
			assert.Empty(t, rows, "q=%q", q)
		}
	})
}
