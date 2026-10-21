package services_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/services"
)

type fakeAUsers struct {
	users map[string]*db.User
	err   *models.ApiError
}

func (f *fakeAUsers) GetUserByExternalIdUpdate(_ context.Context, id string) (*db.User, *models.ApiError) {
	if f.err != nil {
		return nil, f.err
	}
	u, ok := f.users[id]
	if !ok {
		return nil, &models.ApiError{Code: http.StatusNotFound, Error: errors.New("not found")}
	}
	return u, nil
}

type fakeAStore struct {
	gym         *db.Gym
	gymPeriods  []db.MembershipPeriod
	teamPeriods []db.MembershipPeriod
	idents      map[string]db.UserIdentity
	coaches     []db.UserIdentity
	clients     []db.ClientRowLinked
	err         error

	lastLimit    int
	lastCoaches  []string
	lastFilter   db.ScopeClientFilter
	identQueries int
}

func (f *fakeAStore) GetOwnedGym(_ context.Context, gymID, ownerID string) (*db.Gym, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.gym != nil && f.gym.ID.Hex() == gymID && f.gym.OwnerID == ownerID {
		return f.gym, nil
	}
	return nil, nil
}

func (f *fakeAStore) GymHeadPeriods(context.Context, string) ([]db.MembershipPeriod, error) {
	return f.gymPeriods, f.err
}

func (f *fakeAStore) TeamPeriodsByHeads(_ context.Context, heads []string) ([]db.MembershipPeriod, error) {
	if f.err != nil {
		return nil, f.err
	}
	in := map[string]bool{}
	for _, h := range heads {
		in[h] = true
	}
	out := []db.MembershipPeriod{}
	for _, p := range f.teamPeriods {
		if in[p.ParentID] {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakeAStore) UserIdentities(_ context.Context, ids []string) (map[string]db.UserIdentity, error) {
	f.identQueries++
	out := map[string]db.UserIdentity{}
	for _, id := range ids {
		if v, ok := f.idents[id]; ok {
			out[id] = v
		}
	}
	return out, f.err
}

func (f *fakeAStore) CoachIdentities(_ context.Context, limit int) ([]db.UserIdentity, error) {
	f.lastLimit = limit
	if len(f.coaches) > limit {
		return f.coaches[:limit], f.err
	}
	return f.coaches, f.err
}

func (f *fakeAStore) ClientsOfCoaches(_ context.Context, coachIDs []string, flt db.ScopeClientFilter) ([]db.ClientRowLinked, int, error) {
	f.lastCoaches, f.lastFilter = coachIDs, flt
	if f.err != nil {
		return nil, 0, f.err
	}
	allowed := map[string]bool{}
	for _, c := range coachIDs {
		allowed[c] = true
	}
	ids := map[string]bool{}
	for _, c := range flt.ClientIDs {
		ids[c] = true
	}
	out := []db.ClientRowLinked{}
	for _, r := range f.clients {
		if !allowed[r.CoachID] || (len(ids) > 0 && !ids[r.ExternalID]) {
			continue
		}
		out = append(out, r)
	}
	return out, len(out), nil
}

var t0 = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

func mo(n int) time.Time        { return t0.AddDate(0, n, 0) }
func tp(t time.Time) *time.Time { return &t }

func period(kind, member, parent string, from time.Time, to *time.Time) db.MembershipPeriod {
	return db.MembershipPeriod{Kind: kind, MemberID: member, ParentID: parent, Start: from, End: to}
}

func ident(id, first, last string) db.UserIdentity {
	return db.UserIdentity{ExternalID: id, Firstname: first, Lastname: last, CreatedAt: t0}
}

func user(id, role string, head bool, st db.UserStatus) *db.User {
	return &db.User{ExternalID: id, UserRole: role, IsHeadCoach: head, UserStatus: st, UserFirstname: id, UserLastname: "L", UserCreatedAt: t0}
}

func newAnalytics(users map[string]*db.User, st *fakeAStore, max int) services.AnalyticsScopeService {
	return services.NewAnalyticsScopeService(&fakeAUsers{users: users}, st, max)
}

func findEntry(t *testing.T, s *db.AnalyticsScope, coach, head string) db.ScopeCoach {
	t.Helper()
	for _, c := range s.Coaches {
		if c.CoachID == coach && c.HeadCoachID == head {
			return c
		}
	}
	require.Failf(t, "entry not found", "coach=%s head=%s", coach, head)
	return db.ScopeCoach{}
}

func TestAnalyticsTeamScope(t *testing.T) {
	ctx := context.Background()
	since := mo(1)
	h1 := user("h1", "coach", true, db.Active)
	h1.HeadCoachSince = &since
	users := map[string]*db.User{"h1": h1}
	st := &fakeAStore{
		teamPeriods: []db.MembershipPeriod{
			period("team", "c1", "h1", mo(2), nil),
			period("team", "c2", "h1", mo(2), tp(mo(4))),
			period("team", "c2", "h1", mo(6), nil), // came back
			period("team", "c3", "h1", mo(2), tp(mo(3))),
			period("team", "c3", "h9", mo(3), nil), // moved to another head: not ours
		},
		idents: map[string]db.UserIdentity{"c1": ident("c1", "Cy", "One"), "c2": ident("c2", "Di", "Two")},
	}
	svc := newAnalytics(users, st, 0)

	t.Run("current, moved and self", func(t *testing.T) {
		sc, apiErr := svc.Scope(ctx, "h1", services.ScopeRequest{Scope: "team"})
		require.Nil(t, apiErr)
		assert.Equal(t, "team:h1", sc.ScopeKey)
		assert.Len(t, sc.Coaches, 4) // self, c1, c2, c3
		self := findEntry(t, sc, "h1", "h1")
		assert.True(t, self.Current)
		assert.Equal(t, []db.PeriodRange{{From: since}}, self.Periods)
		c2 := findEntry(t, sc, "c2", "h1")
		assert.True(t, c2.Current)
		assert.Len(t, c2.Periods, 2)
		assert.Equal(t, "Di", c2.Firstname)
		assert.Equal(t, "h1 L", c2.HeadCoachName)
		c3 := findEntry(t, sc, "c3", "h1")
		assert.False(t, c3.Current)
		assert.Equal(t, "", c3.Firstname) // deleted/unknown user keeps the id with empty names
		assert.ElementsMatch(t, []string{"h1", "c1", "c2"}, sc.CurrentCoachIDs)
		assert.False(t, sc.GeneratedAt.IsZero())
	})

	t.Run("head without since falls back to created_at", func(t *testing.T) {
		h := user("h2", "coach", true, db.Active)
		s, apiErr := newAnalytics(map[string]*db.User{"h2": h}, &fakeAStore{}, 0).Scope(ctx, "h2", services.ScopeRequest{Scope: "team"})
		require.Nil(t, apiErr)
		assert.Equal(t, t0, findEntry(t, s, "h2", "h2").Periods[0].From)
	})

	t.Run("coach_id narrows and is validated", func(t *testing.T) {
		sc, apiErr := svc.Scope(ctx, "h1", services.ScopeRequest{Scope: "team", CoachID: "c1"})
		require.Nil(t, apiErr)
		assert.Len(t, sc.Coaches, 1)
		assert.Equal(t, "team:h1:c=c1", sc.ScopeKey)

		_, apiErr = svc.Scope(ctx, "h1", services.ScopeRequest{Scope: "team", CoachID: "stranger"})
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)

		_, apiErr = svc.Scope(ctx, "h1", services.ScopeRequest{Scope: "team", HeadCoachID: "h9"})
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)
	})

	t.Run("denied roles", func(t *testing.T) {
		all := map[string]*db.User{
			"plain":   user("plain", "coach", false, db.Active),
			"client":  user("client", "client", false, db.Active),
			"nutri":   user("nutri", "nutritionist", true, db.Active),
			"blocked": user("blocked", "coach", true, db.Blocked),
		}
		s := newAnalytics(all, &fakeAStore{}, 0)
		for _, id := range []string{"plain", "client", "nutri", "blocked", "ghost"} {
			_, apiErr := s.Scope(ctx, id, services.ScopeRequest{Scope: "team"})
			require.NotNil(t, apiErr, id)
			assert.Equal(t, http.StatusForbidden, apiErr.Code, id)
			assert.EqualError(t, apiErr.Error, "Access denied")
		}
	})

	t.Run("invalid scope", func(t *testing.T) {
		_, apiErr := svc.Scope(ctx, "h1", services.ScopeRequest{Scope: "galaxy"})
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code)
	})

	t.Run("store failure is a generic 500", func(t *testing.T) {
		s := newAnalytics(users, &fakeAStore{err: errors.New("mongo down: secret")}, 0)
		_, apiErr := s.Scope(ctx, "h1", services.ScopeRequest{Scope: "team"})
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
		assert.EqualError(t, apiErr.Error, "internal error")
	})
}

func gymFixture() (*db.Gym, map[string]*db.User, *fakeAStore) {
	gym := &db.Gym{ID: primitive.NewObjectID(), OwnerID: "o1", Status: db.GymStatusActive}
	gid := gym.ID.Hex()
	users := map[string]*db.User{
		"o1": user("o1", db.RoleGymOwner, false, db.Active),
	}
	st := &fakeAStore{
		gym: gym,
		gymPeriods: []db.MembershipPeriod{
			// H1 was in the gym Jan-Mar, left, came back from Jun.
			period("gym", "H1", gid, mo(0), tp(mo(2))),
			period("gym", "H1", gid, mo(5), nil),
			// H2 joined mid-January and is still there.
			period("gym", "H2", gid, t0.AddDate(0, 0, 14), nil),
		},
		teamPeriods: []db.MembershipPeriod{
			period("team", "c1", "H1", mo(1), tp(mo(6))),               // overlaps both H1 stays
			period("team", "c2", "H1", mo(3), tp(mo(4))),               // entirely while H1 was outside the gym
			period("team", "c3", "H1", mo(0), nil),                     // open, follows H1
			period("team", "c4", "H1", mo(0), tp(t0.AddDate(0, 1, 0))), // moved to H2 ...
			period("team", "c4", "H2", t0.AddDate(0, 1, 0), nil),       // ... which joined earlier
			period("team", "c5", "H2", mo(0), nil),                     // started before H2 joined the gym
			period("team", "x1", "OUT", mo(0), nil),                    // head outside the gym
		},
		idents: map[string]db.UserIdentity{"H1": ident("H1", "Hana", "One"), "H2": ident("H2", "Hugo", "Two")},
	}
	return gym, users, st
}

func TestAnalyticsGymScope(t *testing.T) {
	ctx := context.Background()
	gym, users, st := gymFixture()
	svc := newAnalytics(users, st, 0)
	req := services.ScopeRequest{Scope: "gym", GymID: gym.ID.Hex()}

	t.Run("interval intersection", func(t *testing.T) {
		sc, apiErr := svc.Scope(ctx, "o1", req)
		require.Nil(t, apiErr)
		assert.Equal(t, "gym:"+gym.ID.Hex(), sc.ScopeKey)

		h1 := findEntry(t, sc, "H1", "H1")
		assert.Equal(t, []db.PeriodRange{{From: mo(0), To: tp(mo(2))}, {From: mo(5)}}, h1.Periods)
		assert.True(t, h1.Current)
		assert.Equal(t, "Hana One", h1.HeadCoachName)

		// c1 [Feb,Jul) x H1 [Jan,Mar) + [Jun,open) -> [Feb,Mar) and [Jun,Jul)
		c1 := findEntry(t, sc, "c1", "H1")
		assert.Equal(t, []db.PeriodRange{{From: mo(1), To: tp(mo(2))}, {From: mo(5), To: tp(mo(6))}}, c1.Periods)
		assert.False(t, c1.Current)

		// c2 only while H1 was outside the gym: dropped
		for _, c := range sc.Coaches {
			assert.NotEqual(t, "c2", c.CoachID)
			assert.NotEqual(t, "x1", c.CoachID)
		}

		// c3 open under H1: follows H1's gym periods, current
		c3 := findEntry(t, sc, "c3", "H1")
		assert.Equal(t, []db.PeriodRange{{From: mo(0), To: tp(mo(2))}, {From: mo(5)}}, c3.Periods)
		assert.True(t, c3.Current)

		// c4 moved H1 -> H2: two entries, only the H2 one is current
		c4a, c4b := findEntry(t, sc, "c4", "H1"), findEntry(t, sc, "c4", "H2")
		assert.Equal(t, []db.PeriodRange{{From: mo(0), To: tp(mo(1))}}, c4a.Periods)
		assert.False(t, c4a.Current)
		assert.Equal(t, []db.PeriodRange{{From: mo(1)}}, c4b.Periods)
		assert.True(t, c4b.Current)

		// c5 started before H2 joined: clipped to H2's gym period
		c5 := findEntry(t, sc, "c5", "H2")
		assert.Equal(t, []db.PeriodRange{{From: t0.AddDate(0, 0, 14)}}, c5.Periods)

		assert.ElementsMatch(t, []string{"H1", "H2", "c3", "c4", "c5"}, sc.CurrentCoachIDs)
	})

	t.Run("head filter narrows", func(t *testing.T) {
		r := req
		r.HeadCoachID = "H2"
		sc, apiErr := svc.Scope(ctx, "o1", r)
		require.Nil(t, apiErr)
		assert.Equal(t, "gym:"+gym.ID.Hex()+":hc=H2", sc.ScopeKey)
		for _, c := range sc.Coaches {
			assert.Equal(t, "H2", c.HeadCoachID)
		}
		assert.Len(t, sc.Coaches, 3) // H2, c4, c5
	})

	t.Run("coach filter narrows", func(t *testing.T) {
		r := req
		r.CoachID = "c4"
		sc, apiErr := svc.Scope(ctx, "o1", r)
		require.Nil(t, apiErr)
		assert.Len(t, sc.Coaches, 2)
		assert.Contains(t, sc.ScopeKey, ":c=c4")
	})

	t.Run("filters outside the scope are 404", func(t *testing.T) {
		for _, r := range []services.ScopeRequest{
			{Scope: "gym", GymID: gym.ID.Hex(), HeadCoachID: "OUT"},
			{Scope: "gym", GymID: gym.ID.Hex(), CoachID: "x1"},
			{Scope: "gym", GymID: gym.ID.Hex(), CoachID: "c2"},
			{Scope: "gym", GymID: gym.ID.Hex(), HeadCoachID: "H2", CoachID: "c1"},
		} {
			_, apiErr := svc.Scope(ctx, "o1", r)
			require.NotNil(t, apiErr, "%+v", r)
			assert.Equal(t, http.StatusNotFound, apiErr.Code)
		}
	})

	t.Run("gym ownership", func(t *testing.T) {
		_, apiErr := svc.Scope(ctx, "o1", services.ScopeRequest{Scope: "gym"})
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)
		assert.EqualError(t, apiErr.Error, "gym not found")

		_, apiErr = svc.Scope(ctx, "o1", services.ScopeRequest{Scope: "gym", GymID: primitive.NewObjectID().Hex()})
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)

		other := map[string]*db.User{"o2": user("o2", db.RoleGymOwner, false, db.Active)}
		_, apiErr = newAnalytics(other, st, 0).Scope(ctx, "o2", req) // someone else's gym
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)
	})

	t.Run("not an active gym owner", func(t *testing.T) {
		bad := map[string]*db.User{
			"coach": user("coach", "coach", true, db.Active),
			"o1":    user("o1", db.RoleGymOwner, false, db.Blocked),
			"admin": user("admin", "admin", false, db.Active),
		}
		for id := range bad {
			_, apiErr := newAnalytics(bad, st, 0).Scope(ctx, id, req)
			require.NotNil(t, apiErr, id)
			assert.Equal(t, http.StatusForbidden, apiErr.Code, id)
		}
	})
}

func TestAnalyticsPlatformScope(t *testing.T) {
	ctx := context.Background()
	coaches := []db.UserIdentity{ident("a", "A", "A"), ident("b", "B", "B"), ident("c", "C", "C")}
	users := map[string]*db.User{
		"adm":      user("adm", "admin", false, db.Active),
		"coach":    user("coach", "coach", true, db.Active),
		"staleadm": user("staleadm", "coach", false, db.Active), // JWT may still say admin; DB says coach
		"offadm":   user("offadm", "admin", false, db.Blocked),
	}
	req := services.ScopeRequest{Scope: "platform"}

	t.Run("admin sees all coaches", func(t *testing.T) {
		st := &fakeAStore{coaches: coaches}
		sc, apiErr := newAnalytics(users, st, 10).Scope(ctx, "adm", req)
		require.Nil(t, apiErr)
		assert.Equal(t, "platform", sc.ScopeKey)
		assert.Len(t, sc.Coaches, 3)
		assert.Equal(t, 11, st.lastLimit) // cap + 1 to detect overflow
		assert.Equal(t, []string{"a", "b", "c"}, sc.CurrentCoachIDs)
		assert.True(t, sc.Coaches[0].Current)
		assert.Equal(t, t0, sc.Coaches[0].Periods[0].From)
		assert.Nil(t, sc.Coaches[0].Periods[0].To)
		assert.Zero(t, st.identQueries) // names came with the coach list
	})

	t.Run("only db admins", func(t *testing.T) {
		for _, id := range []string{"coach", "staleadm", "offadm", "ghost"} {
			_, apiErr := newAnalytics(users, &fakeAStore{coaches: coaches}, 10).Scope(ctx, id, req)
			require.NotNil(t, apiErr, id)
			assert.Equal(t, http.StatusForbidden, apiErr.Code, id)
		}
	})

	t.Run("cap exceeded is 413", func(t *testing.T) {
		_, apiErr := newAnalytics(users, &fakeAStore{coaches: coaches}, 2).Scope(ctx, "adm", req)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusRequestEntityTooLarge, apiErr.Code)
		assert.EqualError(t, apiErr.Error, "scope too large")
	})

	t.Run("exactly at the cap is fine", func(t *testing.T) {
		_, apiErr := newAnalytics(users, &fakeAStore{coaches: coaches}, 3).Scope(ctx, "adm", req)
		require.Nil(t, apiErr)
	})
}

func clientRow(client, coach string) db.ClientRowLinked {
	return db.ClientRowLinked{ClientRow: db.ClientRow{ExternalID: client, Firstname: "F" + client, Email: client + "@x", CoachID: coach, CoachName: "N" + coach}, LinkedAt: t0}
}

func TestAnalyticsLookupClients(t *testing.T) {
	ctx := context.Background()
	h1 := user("h1", "coach", true, db.Active)
	users := map[string]*db.User{"h1": h1}
	st := &fakeAStore{
		teamPeriods: []db.MembershipPeriod{
			period("team", "c1", "h1", mo(1), nil),
			period("team", "c2", "h1", mo(1), tp(mo(3))), // former coach: out of the current set
		},
		clients: []db.ClientRowLinked{clientRow("k1", "c1"), clientRow("k2", "c2"), clientRow("k3", "h1"), clientRow("k4", "other")},
	}
	svc := newAnalytics(users, st, 0)

	t.Run("only clients of current in-scope coaches", func(t *testing.T) {
		items, apiErr := svc.LookupClients(ctx, "h1", services.ScopeRequest{Scope: "team"}, []string{"k1", "k2", "k3", "k4", "nobody"})
		require.Nil(t, apiErr)
		got := []string{}
		for _, it := range items {
			got = append(got, it.ExternalID)
		}
		assert.ElementsMatch(t, []string{"k1", "k3"}, got)
		assert.ElementsMatch(t, []string{"h1", "c1"}, st.lastCoaches)
	})

	t.Run("no sensitive fields in the payload", func(t *testing.T) {
		items, apiErr := svc.LookupClients(ctx, "h1", services.ScopeRequest{Scope: "team"}, []string{"k1"})
		require.Nil(t, apiErr)
		b, err := json.Marshal(items)
		require.NoError(t, err)
		s := string(b)
		for _, bad := range []string{"phone", "address", "password", "health", "token"} {
			assert.False(t, strings.Contains(s, bad), bad)
		}
		assert.Contains(t, s, `"coach_name":"Nc1"`)
	})

	t.Run("ids validation", func(t *testing.T) {
		_, apiErr := svc.LookupClients(ctx, "h1", services.ScopeRequest{Scope: "team"}, nil)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code)
		many := make([]string, 101)
		_, apiErr = svc.LookupClients(ctx, "h1", services.ScopeRequest{Scope: "team"}, many)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code)
	})

	t.Run("re-authorized", func(t *testing.T) {
		_, apiErr := svc.LookupClients(ctx, "nobody", services.ScopeRequest{Scope: "team"}, []string{"k1"})
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusForbidden, apiErr.Code)
	})
}

func TestAnalyticsListHeadClients(t *testing.T) {
	ctx := context.Background()
	users := map[string]*db.User{"h1": user("h1", "coach", true, db.Active), "c1": user("c1", "coach", false, db.Active)}
	st := &fakeAStore{
		teamPeriods: []db.MembershipPeriod{
			period("team", "c1", "h1", mo(1), nil),
			period("team", "c2", "h1", mo(1), tp(mo(3))),
		},
		clients: []db.ClientRowLinked{clientRow("k1", "c1"), clientRow("k2", "c2"), clientRow("k3", "h1")},
	}
	svc := newAnalytics(users, st, 0)

	t.Run("default is self plus current team", func(t *testing.T) {
		rows, total, apiErr := svc.ListHeadClients(ctx, "h1", services.HeadClientsQuery{})
		require.Nil(t, apiErr)
		assert.Equal(t, 2, total)
		assert.Len(t, rows, 2)
		assert.ElementsMatch(t, []string{"h1", "c1"}, st.lastCoaches)
		assert.Equal(t, 1, st.lastFilter.Page)
		assert.Equal(t, 20, st.lastFilter.Size)
	})

	t.Run("coach filter", func(t *testing.T) {
		_, _, apiErr := svc.ListHeadClients(ctx, "h1", services.HeadClientsQuery{CoachID: "c1"})
		require.Nil(t, apiErr)
		assert.Equal(t, []string{"c1"}, st.lastCoaches)

		_, _, apiErr = svc.ListHeadClients(ctx, "h1", services.HeadClientsQuery{CoachID: "h1"})
		require.Nil(t, apiErr)
		assert.Equal(t, []string{"h1"}, st.lastCoaches)

		for _, bad := range []string{"c2", "other"} { // former member, stranger
			_, _, apiErr = svc.ListHeadClients(ctx, "h1", services.HeadClientsQuery{CoachID: bad})
			require.NotNil(t, apiErr, bad)
			assert.Equal(t, http.StatusNotFound, apiErr.Code)
		}
	})

	t.Run("paging and search are bounded", func(t *testing.T) {
		_, _, apiErr := svc.ListHeadClients(ctx, "h1", services.HeadClientsQuery{Q: strings.Repeat("x", 200), Sort: "linked_at", Page: 3, Size: 500})
		require.Nil(t, apiErr)
		assert.Len(t, []rune(st.lastFilter.Q), 64)
		assert.Equal(t, 20, st.lastFilter.Size)
		assert.Equal(t, 3, st.lastFilter.Page)
		assert.Equal(t, "linked_at", st.lastFilter.Sort)

		_, _, apiErr = svc.ListHeadClients(ctx, "h1", services.HeadClientsQuery{Page: 2, Size: 50})
		require.Nil(t, apiErr)
		assert.Equal(t, 50, st.lastFilter.Size)
	})

	t.Run("non head coach denied", func(t *testing.T) {
		_, _, apiErr := svc.ListHeadClients(ctx, "c1", services.HeadClientsQuery{})
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusForbidden, apiErr.Code)
	})
}
