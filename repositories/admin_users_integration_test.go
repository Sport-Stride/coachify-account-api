//go:build integration

package repositories_test

import (
	"context"
	"fmt"
	"os"
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

type adminFixture struct {
	database *mongo.Database
	users    *repositories.UserRepository
	audit    *repositories.AdminAuditRepository
	now      time.Time
}

func adminSetup(t *testing.T) (*adminFixture, context.Context) {
	t.Helper()
	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		t.Skip("MONGO_TEST_URI not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	require.NoError(t, err)
	database := client.Database(fmt.Sprintf("admin_it_%d", time.Now().UnixNano()))
	t.Cleanup(func() {
		dctx, dcancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer dcancel()
		_ = database.Drop(dctx)
		_ = client.Disconnect(dctx)
	})

	require.NoError(t, repositories.InitializeIndexes(ctx, database))
	ok, missing := repositories.VerifyIndexes(ctx, database)
	require.True(t, ok, "missing indexes: %v", missing)

	f := &adminFixture{
		database: database,
		users:    repositories.NewUserRepository(database.Collection("users")),
		audit:    repositories.NewAdminAuditRepository(database, "admin_audit"),
		now:      time.Now(),
	}
	f.seed(t, ctx)
	return f, ctx
}

func (f *adminFixture) ago(d time.Duration) time.Time {
	return f.now.Add(-d).Truncate(time.Millisecond)
}

func adminDays(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }

// Seed: 12 users.
//
//	co1 Zed Alpha   coach        Active     signup 2d,  login 1d,  pending matricule, head coach
//	co2 Dana Beta   coach        Inactive   signup 10d, login 5d,  approved matricule, member of co1, 2 clients
//	co3 Eve a.b     coach        Active     signup 40d, never logged in
//	nu1 Nina Nutri  nutritionist Active     signup 20d
//	ad1 Root Admin  admin        Active     signup 100d
//	cl1 Carl One    client       Active     signup 3d, coach co2
//	cl2 Cleo Two    client       To Confirm signup 1d
//	cl3 Cora Three  client       To Confirm signup 15d
//	cl4..cl6 more clients Active, signup 60d
func (f *adminFixture) seed(t *testing.T, ctx context.Context) {
	t.Helper()
	var docs []interface{}
	u := func(id, first, last, email, role, status string, signup time.Duration, login *time.Duration, mf string) bson.M {
		d := bson.M{
			"externalid": id, "firstname": first, "lastname": last, "email": email,
			"role": role, "status": status, "created_at": f.ago(signup), "updated_at": f.ago(signup / 2),
			"password": "SECRET-HASH", "token": "SECRET-TOKEN", "refresh_token": "SECRET-REFRESH",
			"confirm_code": bson.M{"code": "123456"}, "reset_password_code": bson.M{"code": "654321"},
			"phone_number": "+21600000", "gender": "Female", "verification_status": true,
			"matricule_fiscale": "SECRET-MF-" + id,
		}
		if login != nil {
			d["last_login"] = f.ago(*login)
		}
		if mf != "" {
			d["matricule_fiscale_status"] = mf
		}
		docs = append(docs, d)
		return d
	}
	d := func(n int) *time.Duration { v := adminDays(n); return &v }

	co1 := u("co1", "Zed", "Alpha", "zed@it.test", "coach", "Active", adminDays(2), d(1), "pending")
	co1["is_head_coach"] = true
	u("co2", "Dana", "Beta", "dana@it.test", "coach", "Inactive", adminDays(10), d(5), "approved")
	u("co3", "Eve", "a.b", "eve@it.test", "coach", "Active", adminDays(40), nil, "")
	u("nu1", "Nina", "Nutri", "nina@it.test", "nutritionist", "Active", adminDays(20), d(2), "")
	u("ad1", "Root", "Admin", "root@it.test", "admin", "Active", adminDays(100), d(1), "")
	u("cl1", "Carl", "One", "carl@it.test", "client", "Active", adminDays(3), d(1), "")
	u("cl2", "Cleo", "Two", "cleo@it.test", "client", "To Confirm", adminDays(1), nil, "")
	u("cl3", "Cora", "Three", "cora@it.test", "client", "To Confirm", adminDays(15), nil, "")
	u("cl4", "Dave", "Four", "dave@it.test", "client", "Active", adminDays(60), d(30), "")
	u("cl5", "Dina", "Five", "dina@it.test", "client", "Active", adminDays(60), d(30), "")
	u("cl6", "Dora", "Six", "dora@it.test", "client", "Active", adminDays(60), d(30), "")
	_, err := f.database.Collection("users").InsertMany(ctx, docs)
	require.NoError(t, err)

	_, err = f.database.Collection("coach_clients").InsertMany(ctx, []interface{}{
		bson.M{"coach_id": "co2", "client_id": "cl1", "created_at": f.ago(adminDays(3))},
		bson.M{"coach_id": "co2", "client_id": "cl4", "created_at": f.ago(adminDays(3))},
	})
	require.NoError(t, err)
	_, err = f.database.Collection("coach_team_members").InsertOne(ctx, bson.M{
		"coach_id": "co2", "head_coach_id": "co1", "assigned_by": "ad1", "assigned_by_role": "admin", "assigned_at": f.ago(adminDays(5)),
	})
	require.NoError(t, err)
}

func adminRowIDs(rows []db.AdminUserRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ExternalID)
	}
	return out
}

func TestAdminListUsers_Integration(t *testing.T) {
	f, ctx := adminSetup(t)
	q := func(mod func(*db.AdminUserQuery)) db.AdminUserQuery {
		v := db.AdminUserQuery{Page: 1, Size: 50, Sort: "created_at", Order: "desc"}
		mod(&v)
		return v
	}

	t.Run("no filter: total and newest first", func(t *testing.T) {
		rows, total, err := f.users.ListAdminUsers(ctx, q(func(*db.AdminUserQuery) {}))
		require.NoError(t, err)
		assert.Equal(t, 11, total)
		assert.Equal(t, []string{"cl2", "co1", "cl1", "co2", "cl3", "nu1", "co3", "cl4", "cl5", "cl6", "ad1"}, adminRowIDs(rows))
	})

	t.Run("role and status", func(t *testing.T) {
		rows, total, err := f.users.ListAdminUsers(ctx, q(func(v *db.AdminUserQuery) { v.Role = "client"; v.Status = "To Confirm" }))
		require.NoError(t, err)
		assert.Equal(t, 2, total)
		assert.ElementsMatch(t, []string{"cl2", "cl3"}, adminRowIDs(rows))
	})

	t.Run("ids filter", func(t *testing.T) {
		rows, total, err := f.users.ListAdminUsers(ctx, q(func(v *db.AdminUserQuery) { v.IDs = []string{"co1", "cl1", "ghost"} }))
		require.NoError(t, err)
		assert.Equal(t, 2, total)
		assert.ElementsMatch(t, []string{"co1", "cl1"}, adminRowIDs(rows))
	})

	t.Run("q is a case-insensitive literal", func(t *testing.T) {
		rows, total, err := f.users.ListAdminUsers(ctx, q(func(v *db.AdminUserQuery) { v.Q = "ZED" }))
		require.NoError(t, err)
		assert.Equal(t, 1, total)
		assert.Equal(t, []string{"co1"}, adminRowIDs(rows))

		// "a.*" would match everything as a regex; literally it matches nobody.
		_, total, err = f.users.ListAdminUsers(ctx, q(func(v *db.AdminUserQuery) { v.Q = "a.*" }))
		require.NoError(t, err)
		assert.Equal(t, 0, total)

		// literal dot matches only the lastname "a.b"
		rows, total, err = f.users.ListAdminUsers(ctx, q(func(v *db.AdminUserQuery) { v.Q = "a.b" }))
		require.NoError(t, err)
		assert.Equal(t, 1, total)
		assert.Equal(t, []string{"co3"}, adminRowIDs(rows))

		// matches email too
		rows, _, err = f.users.ListAdminUsers(ctx, q(func(v *db.AdminUserQuery) { v.Q = "dana@it" }))
		require.NoError(t, err)
		assert.Equal(t, []string{"co2"}, adminRowIDs(rows))
	})

	t.Run("sort and order", func(t *testing.T) {
		rows, _, err := f.users.ListAdminUsers(ctx, q(func(v *db.AdminUserQuery) { v.Role = "coach"; v.Sort = "name"; v.Order = "asc" }))
		require.NoError(t, err)
		assert.Equal(t, []string{"co1", "co2", "co3"}, adminRowIDs(rows)) // Alpha, Beta, a.b (binary order puts "a.b" last)

		rows, _, err = f.users.ListAdminUsers(ctx, q(func(v *db.AdminUserQuery) { v.Role = "coach"; v.Sort = "email"; v.Order = "desc" }))
		require.NoError(t, err)
		assert.Equal(t, []string{"co1", "co3", "co2"}, adminRowIDs(rows))

		rows, _, err = f.users.ListAdminUsers(ctx, q(func(v *db.AdminUserQuery) { v.Role = "coach"; v.Sort = "last_login"; v.Order = "desc" }))
		require.NoError(t, err)
		assert.Equal(t, []string{"co1", "co2", "co3"}, adminRowIDs(rows)) // never logged in sorts last in desc
		assert.Nil(t, rows[2].LastLogin)
		assert.NotNil(t, rows[0].LastLogin)
	})

	t.Run("pagination and total", func(t *testing.T) {
		rows, total, err := f.users.ListAdminUsers(ctx, q(func(v *db.AdminUserQuery) { v.Page = 2; v.Size = 4 }))
		require.NoError(t, err)
		assert.Equal(t, 11, total)
		assert.Len(t, rows, 4)
		rows, total, err = f.users.ListAdminUsers(ctx, q(func(v *db.AdminUserQuery) { v.Page = 3; v.Size = 4 }))
		require.NoError(t, err)
		assert.Equal(t, 11, total)
		assert.Len(t, rows, 3)
		rows, total, err = f.users.ListAdminUsers(ctx, q(func(v *db.AdminUserQuery) { v.Page = 9; v.Size = 4 }))
		require.NoError(t, err)
		assert.Equal(t, 11, total)
		assert.Empty(t, rows)
	})

	t.Run("row projection", func(t *testing.T) {
		rows, _, err := f.users.ListAdminUsers(ctx, q(func(v *db.AdminUserQuery) { v.IDs = []string{"co1"} }))
		require.NoError(t, err)
		require.Len(t, rows, 1)
		r := rows[0]
		assert.True(t, r.IsHeadCoach)
		assert.True(t, r.VerificationStatus)
		assert.Equal(t, "pending", r.MatriculeFiscaleStatus)
		assert.Equal(t, "zed@it.test", r.Email)

		rows, _, _ = f.users.ListAdminUsers(ctx, q(func(v *db.AdminUserQuery) { v.IDs = []string{"cl1"} }))
		assert.Equal(t, "none", rows[0].MatriculeFiscaleStatus)
		assert.False(t, rows[0].IsHeadCoach)
	})
}

func TestAdminCoachesQ_Integration(t *testing.T) {
	f, ctx := adminSetup(t)
	rows, total, err := f.users.GetCoachesByPage(ctx, 1, 20, db.AdminCoachFilter{Q: "a.*"})
	require.NoError(t, err)
	assert.Equal(t, 0, total)
	assert.Empty(t, rows)

	rows, total, err = f.users.GetCoachesByPage(ctx, 1, 20, db.AdminCoachFilter{Q: " DANA "})
	require.NoError(t, err)
	assert.Equal(t, 1, total)
	require.Len(t, rows, 1)
	assert.Equal(t, "co2", rows[0].ExternalID)

	// q never leaks non-coaches
	_, total, err = f.users.GetCoachesByPage(ctx, 1, 20, db.AdminCoachFilter{Q: "nina"})
	require.NoError(t, err)
	assert.Equal(t, 0, total)
}

func TestAdminUserDetail_Integration(t *testing.T) {
	f, ctx := adminSetup(t)

	t.Run("missing user", func(t *testing.T) {
		d, err := f.users.GetAdminUserDetail(ctx, "ghost")
		require.NoError(t, err)
		assert.Nil(t, d)
	})

	t.Run("client with coach", func(t *testing.T) {
		d, err := f.users.GetAdminUserDetail(ctx, "cl1")
		require.NoError(t, err)
		require.NotNil(t, d)
		assert.Equal(t, "cl1", d.ExternalID)
		assert.Equal(t, "+21600000", d.PhoneNumber)
		assert.Equal(t, "Female", d.Gender)
		require.NotNil(t, d.Coach)
		assert.Equal(t, "co2", d.Coach.ExternalID)
		assert.Equal(t, "Dana", d.Coach.Firstname)
		assert.Equal(t, "Beta", d.Coach.Lastname)
		assert.Nil(t, d.ClientsCount)
		assert.Empty(t, d.HeadCoachID)
		assert.False(t, d.UpdatedAt.IsZero())
	})

	t.Run("client without coach", func(t *testing.T) {
		d, err := f.users.GetAdminUserDetail(ctx, "cl2")
		require.NoError(t, err)
		require.NotNil(t, d)
		assert.Nil(t, d.Coach)
	})

	t.Run("coach with head coach and clients", func(t *testing.T) {
		d, err := f.users.GetAdminUserDetail(ctx, "co2")
		require.NoError(t, err)
		require.NotNil(t, d)
		require.NotNil(t, d.ClientsCount)
		assert.Equal(t, 2, *d.ClientsCount)
		assert.Equal(t, "co1", d.HeadCoachID)
		assert.Equal(t, "Zed Alpha", d.HeadCoachName)
		assert.Equal(t, "approved", d.MatriculeFiscaleStatus)
		assert.Nil(t, d.Coach)
	})

	t.Run("coach with zero clients", func(t *testing.T) {
		d, err := f.users.GetAdminUserDetail(ctx, "co3")
		require.NoError(t, err)
		require.NotNil(t, d.ClientsCount)
		assert.Equal(t, 0, *d.ClientsCount)
		assert.Empty(t, d.HeadCoachID)
	})
}

func TestAdminUserStats_Integration(t *testing.T) {
	f, ctx := adminSetup(t)

	st, err := f.users.AdminUserStats(ctx, 30, f.now)
	require.NoError(t, err)
	assert.Equal(t, map[string]int{"coach": 3, "nutritionist": 1, "admin": 1, "client": 6}, st.ByRole)
	assert.Equal(t, map[string]int{"Active": 8, "Inactive": 1, "To Confirm": 2}, st.ByStatus)
	assert.Equal(t, 3, st.NewUsers.Last7d)  // co1 cl1 cl2
	assert.Equal(t, 6, st.NewUsers.Last30d) // + co2 cl3 nu1
	sum := 0
	for _, p := range st.Signups {
		assert.Regexp(t, `^\d{4}-\d{2}-\d{2}$`, p.Date)
		sum += p.Count
	}
	assert.Equal(t, 6, sum)
	assert.Equal(t, 2, st.ToConfirm)
	assert.Equal(t, 1, st.PendingMatricule)
	assert.True(t, st.GeneratedAt.Equal(f.now))
}

func TestAdminAudit_Integration(t *testing.T) {
	f, ctx := adminSetup(t)

	e := func(actor, action, target string, age time.Duration) *db.AdminAuditEntry {
		return &db.AdminAuditEntry{
			ActorID: actor, Action: action, TargetType: "user", TargetID: target,
			Before: bson.M{"status": "Active"}, After: bson.M{"status": "Blocked"},
			Reason: "because", CreatedAt: f.ago(age),
		}
	}
	require.NoError(t, f.audit.Insert(ctx, e("ad1", "block", "cl1", adminDays(3))))
	require.NoError(t, f.audit.Insert(ctx, e("ad1", "unblock", "cl1", adminDays(2))))
	require.NoError(t, f.audit.Insert(ctx, e("ad2", "block", "cl2", adminDays(1))))
	noTime := e("ad2", "block", "cl3", 0)
	noTime.CreatedAt = time.Time{}
	require.NoError(t, f.audit.Insert(ctx, noTime))
	assert.False(t, noTime.CreatedAt.IsZero(), "Insert defaults CreatedAt")

	list := func(flt db.AdminAuditFilter, page, size int) ([]db.AdminAuditEntry, int) {
		rows, total, err := f.audit.List(ctx, flt, page, size)
		require.NoError(t, err)
		return rows, total
	}

	rows, total := list(db.AdminAuditFilter{}, 1, 20)
	assert.Equal(t, 4, total)
	assert.Equal(t, "cl3", rows[0].TargetID) // newest first
	assert.Equal(t, "cl1", rows[3].TargetID)
	assert.Equal(t, "because", rows[3].Reason)
	assert.False(t, rows[3].ID.IsZero())

	rows, total = list(db.AdminAuditFilter{ActorID: "ad1"}, 1, 20)
	assert.Equal(t, 2, total)
	assert.Len(t, rows, 2)

	rows, total = list(db.AdminAuditFilter{TargetID: "cl1", Action: "unblock"}, 1, 20)
	assert.Equal(t, 1, total)
	assert.Equal(t, "unblock", rows[0].Action)

	rows, total = list(db.AdminAuditFilter{From: f.ago(adminDays(2) + time.Hour), To: f.ago(adminDays(1) - time.Hour)}, 1, 20)
	assert.Equal(t, 2, total)
	assert.Len(t, rows, 2)

	rows, total = list(db.AdminAuditFilter{}, 2, 3)
	assert.Equal(t, 4, total)
	assert.Len(t, rows, 1)
}

func TestUpdateStatusAndRevoke_Integration(t *testing.T) {
	f, ctx := adminSetup(t)
	users := f.database.Collection("users")
	load := func(id string) bson.M {
		var d bson.M
		require.NoError(t, users.FindOne(ctx, bson.M{"externalid": id}).Decode(&d))
		return d
	}

	t.Run("blocked unsets tokens", func(t *testing.T) {
		found, err := f.users.UpdateStatusAndRevoke(ctx, "cl1", "Blocked")
		require.NoError(t, err)
		assert.True(t, found)
		d := load("cl1")
		assert.Equal(t, "Blocked", d["status"])
		assert.NotContains(t, d, "token")
		assert.NotContains(t, d, "refresh_token")
		assert.Equal(t, "SECRET-HASH", d["password"])
	})

	t.Run("active keeps tokens", func(t *testing.T) {
		found, err := f.users.UpdateStatusAndRevoke(ctx, "cl4", "Active")
		require.NoError(t, err)
		assert.True(t, found)
		d := load("cl4")
		assert.Equal(t, "Active", d["status"])
		assert.Equal(t, "SECRET-TOKEN", d["token"])
		assert.Equal(t, "SECRET-REFRESH", d["refresh_token"])
	})

	t.Run("unknown user", func(t *testing.T) {
		found, err := f.users.UpdateStatusAndRevoke(ctx, "ghost", "Blocked")
		require.NoError(t, err)
		assert.False(t, found)
	})
}
