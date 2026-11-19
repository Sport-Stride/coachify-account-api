package services_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
	"coachify-account-api/services"
	"coachify-account-api/services/mocks"
)

// fakeMemberStore is an in-memory services.GymMemberStore with the semantics of the repository.
type fakeMemberStore struct {
	rows   []*db.GymMember
	failOn string // method name that returns errBoom
	// missingOwners is the answer of CountGymsWithoutOwnerRow.
	missingOwners int
}

func (f *fakeMemberStore) active(gym, user string) *db.GymMember {
	for _, r := range f.rows {
		if r.GymID == gym && r.UserID == user && r.Status == db.GymMemberStatusActive {
			return r
		}
	}
	return nil
}

func (f *fakeMemberStore) leave(r *db.GymMember, at time.Time) {
	r.Status, r.LeftAt = db.GymMemberStatusLeft, &at
}

func (f *fakeMemberStore) UpsertActive(_ context.Context, m *db.GymMember) error {
	if f.failOn == "UpsertActive" {
		return errBoom
	}
	if m.Role != db.GymMemberRoleOwner {
		for _, r := range f.rows {
			if r.UserID == m.UserID && r.Status == db.GymMemberStatusActive && r.GymID != m.GymID && r.Role != db.GymMemberRoleOwner {
				f.leave(r, m.JoinedAt)
			}
		}
	}
	if r := f.active(m.GymID, m.UserID); r != nil {
		r.Role, r.SupervisorID, r.AddedBy, r.Source = m.Role, m.SupervisorID, m.AddedBy, m.Source
		return nil
	}
	c := *m
	c.Status, c.SeatAssigned = db.GymMemberStatusActive, false
	f.rows = append(f.rows, &c)
	return nil
}

func (f *fakeMemberStore) MarkLeft(_ context.Context, gym, user string, at time.Time) (bool, error) {
	if f.failOn == "MarkLeft" {
		return false, errBoom
	}
	if r := f.active(gym, user); r != nil {
		f.leave(r, at)
		return true, nil
	}
	return false, nil
}

func (f *fakeMemberStore) MarkLeftCoach(_ context.Context, user string, at time.Time) (bool, error) {
	if f.failOn == "MarkLeftCoach" {
		return false, errBoom
	}
	done := false
	for _, r := range f.rows {
		if r.UserID == user && r.Role == db.GymMemberRoleCoach && r.Status == db.GymMemberStatusActive {
			f.leave(r, at)
			done = true
		}
	}
	return done, nil
}

func (f *fakeMemberStore) MarkLeftBySupervisor(_ context.Context, gym, sup string, at time.Time) (int64, error) {
	if f.failOn == "MarkLeftBySupervisor" {
		return 0, errBoom
	}
	var n int64
	for _, r := range f.rows {
		if r.GymID == gym && r.SupervisorID == sup && r.Role == db.GymMemberRoleCoach && r.Status == db.GymMemberStatusActive {
			f.leave(r, at)
			n++
		}
	}
	return n, nil
}

func (f *fakeMemberStore) MarkLeftHead(_ context.Context, user string, at time.Time) (bool, error) {
	if f.failOn == "MarkLeftHead" {
		return false, errBoom
	}
	done := false
	for _, r := range f.rows {
		if r.UserID == user && r.Role == db.GymMemberRoleHeadCoach && r.Status == db.GymMemberStatusActive {
			f.leave(r, at)
			done = true
		}
	}
	return done, nil
}

func (f *fakeMemberStore) MarkLeftSupervised(_ context.Context, sup string, at time.Time) (int64, error) {
	if f.failOn == "MarkLeftSupervised" {
		return 0, errBoom
	}
	var n int64
	for _, r := range f.rows {
		if r.SupervisorID == sup && r.Role == db.GymMemberRoleCoach && r.Status == db.GymMemberStatusActive {
			f.leave(r, at)
			n++
		}
	}
	return n, nil
}

func (f *fakeMemberStore) ListByUser(_ context.Context, user string) ([]db.GymMember, error) {
	if f.failOn == "ListByUser" {
		return nil, errBoom
	}
	out := []db.GymMember{}
	for _, r := range f.rows {
		if r.UserID == user && r.Status == db.GymMemberStatusActive {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (f *fakeMemberStore) Get(_ context.Context, user, gym string) (*db.GymMember, error) {
	if f.failOn == "Get" {
		return nil, errBoom
	}
	if r := f.active(gym, user); r != nil {
		c := *r
		return &c, nil
	}
	return nil, nil
}

var _ services.GymMemberStore = (*fakeMemberStore)(nil)

// shape is what both readers must agree on.
type shape struct {
	Gym, User, Supervisor string
	Role                  db.GymMemberRole
	Status                db.GymMemberStatus
}

func shapes(rows []db.GymMember) []shape {
	out := []shape{}
	for _, r := range rows {
		out = append(out, shape{r.GymID, r.UserID, r.SupervisorID, r.Role, r.Status})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Gym+string(out[i].Role) < out[j].Gym+string(out[j].Role) })
	return out
}

// legacyFixture: gym G1 (owner o1) with head h1 and coaches c1, c2; head h2 has coach c3 but is in no gym; archived gym G2 of o1.
func legacyFixture() (*fakeGymStore, *mocks.TeamStore, string, *fakeMemberStore) {
	g, ts := newFakeGymStore(), &mocks.TeamStore{}
	g1 := g.addGym("o1", db.GymStatusActive)
	g.addGym("o1", db.GymStatusArchived)
	gid := g1.ID.Hex()
	g.heads["h1"] = &db.GymHeadCoach{GymID: gid, HeadCoachID: "h1", AddedBy: "o1", AddedAt: time.Now()}
	teams := map[string]string{"c1": "h1", "c2": "h1", "c3": "h2"}
	for coach, head := range teams {
		ts.On("GetHeadOf", mock.Anything, coach).Return(&db.TeamMember{HeadCoachID: head, CoachID: coach, AssignedBy: "admin1", AssignedAt: time.Now()}, nil)
	}
	for _, id := range []string{"o1", "h1", "h2", "c4"} {
		ts.On("GetHeadOf", mock.Anything, id).Return(nil, nil)
	}

	var team []db.TeamMember
	for coach, head := range teams {
		team = append(team, db.TeamMember{HeadCoachID: head, CoachID: coach, AssignedBy: "admin1", AssignedAt: time.Now()})
	}
	var gyms []db.Gym
	for _, x := range g.gyms {
		gyms = append(gyms, *x)
	}
	var heads []db.GymHeadCoach
	for _, h := range g.heads {
		heads = append(heads, *h)
	}
	store := &fakeMemberStore{}
	for _, m := range services.BackfillMembers(gyms, heads, team) {
		c := m
		store.rows = append(store.rows, &c)
	}
	return g, ts, gid, store
}

func TestMembershipReadersAgree(t *testing.T) {
	ctx := context.Background()
	g, ts, gid, store := legacyFixture()
	legacy := services.NewLegacyMembershipAdapter(g, ts)
	repoBacked := services.NewRepoMembershipReader(store)

	for _, user := range []string{"o1", "h1", "c1", "c2", "c3", "h2", "c4"} {
		a, err := legacy.MembershipsOf(ctx, user)
		require.NoError(t, err)
		b, err := repoBacked.MembershipsOf(ctx, user)
		require.NoError(t, err)
		assert.Equal(t, shapes(a), shapes(b), "MembershipsOf %s", user)

		ma, err := legacy.MembershipOf(ctx, user, gid)
		require.NoError(t, err)
		mb, err := repoBacked.MembershipOf(ctx, user, gid)
		require.NoError(t, err)
		assert.Equal(t, ma == nil, mb == nil, "MembershipOf %s", user)
		if ma != nil {
			assert.Equal(t, shapes([]db.GymMember{*ma}), shapes([]db.GymMember{*mb}))
		}
	}

	t.Run("roles and supervisor", func(t *testing.T) {
		m, err := legacy.MembershipOf(ctx, "c1", gid)
		require.NoError(t, err)
		require.NotNil(t, m)
		assert.Equal(t, db.GymMemberRoleCoach, m.Role)
		assert.Equal(t, "h1", m.SupervisorID)
		assert.Equal(t, db.GymMemberSourceLegacyTeam, m.Source)
		assert.False(t, m.SeatAssigned)

		o, err := legacy.MembershipsOf(ctx, "o1")
		require.NoError(t, err)
		require.Len(t, o, 1, "the archived gym gives no owner membership")
		assert.Equal(t, db.GymMemberRoleOwner, o[0].Role)
	})

	t.Run("coach of a head outside any gym has no membership", func(t *testing.T) {
		m, err := legacy.MembershipsOf(ctx, "c3")
		require.NoError(t, err)
		assert.Empty(t, m)
	})
}

func TestMembershipReaderSelection(t *testing.T) {
	legacy := services.NewLegacyMembershipAdapter(newFakeGymStore(), &mocks.TeamStore{})
	repoBacked := services.NewRepoMembershipReader(&fakeMemberStore{})

	for mode, want := range map[string]services.GymMembershipReader{
		"":       legacy,
		"legacy": legacy,
		"bogus":  legacy,
		"new":    repoBacked,
		" NEW ":  repoBacked,
	} {
		assert.Same(t, want, services.NewGymMembershipReader(mode, legacy, repoBacked), "mode %q", mode)
	}
}

func TestMembershipReaderErrors(t *testing.T) {
	ctx := context.Background()
	store := &fakeMemberStore{failOn: "ListByUser"}
	_, err := services.NewRepoMembershipReader(store).MembershipsOf(ctx, "u1")
	assert.ErrorIs(t, err, errBoom)

	ts := &mocks.TeamStore{}
	ts.On("GetHeadOf", mock.Anything, "u1").Return(nil, errBoom)
	_, err = services.NewLegacyMembershipAdapter(newFakeGymStore(), ts).MembershipsOf(ctx, "u1")
	assert.ErrorIs(t, err, errBoom)
}

func TestBackfillMembers(t *testing.T) {
	_, _, _, store := legacyFixture()
	got := shapes(derefRows(store.rows))
	require.Len(t, got, 4, "owner of the active gym, one head, two coaches; c3 and the archived gym are skipped")
	for _, r := range store.rows {
		assert.False(t, r.SeatAssigned)
	}
}

func derefRows(in []*db.GymMember) []db.GymMember {
	out := make([]db.GymMember, 0, len(in))
	for _, r := range in {
		out = append(out, *r)
	}
	return out
}

func newMirroredGymSvc(store services.GymMemberStore) (services.GymService, *mocks.TeamUserStore, *fakeGymStore) {
	u, g := &mocks.TeamUserStore{}, newFakeGymStore()
	return services.NewGymService(u, g, services.GymSettings{InviteTTL: time.Hour, InvitesPerDay: 20, Members: store}), u, g
}

func TestDualWriteGym(t *testing.T) {
	ctx := context.Background()
	future := time.Now().Add(time.Hour)

	join := func(store *fakeMemberStore) (services.GymService, *fakeGymStore, *db.Gym, *mocks.TeamUserStore) {
		svc, u, g := newMirroredGymSvc(store)
		gym := g.addGym("owner1", db.GymStatusActive)
		inv := g.addInvite(gym, "coach@x.tn", db.GymInvitePending, future)
		givenUser(u, gymUser("c1", "coach", db.Active, "coach@x.tn", false))
		u.On("SetHeadCoach", mock.Anything, "c1", true, "owner1").Return(nil)
		_, apiErr := svc.AcceptInvitation(ctx, "c1", inv.ID.Hex())
		require.Nil(t, apiErr)
		return svc, g, gym, u
	}

	t.Run("joinGym upserts the head coach row", func(t *testing.T) {
		store := &fakeMemberStore{}
		_, _, gym, _ := join(store)
		require.Len(t, store.rows, 1)
		r := store.rows[0]
		assert.Equal(t, gym.ID.Hex(), r.GymID)
		assert.Equal(t, "c1", r.UserID)
		assert.Equal(t, db.GymMemberRoleHeadCoach, r.Role)
		assert.Equal(t, db.GymMemberSourceInvite, r.Source)
		assert.Equal(t, "owner1", r.AddedBy)
		assert.False(t, r.SeatAssigned)
	})

	t.Run("joinGym still succeeds when the dual-write fails", func(t *testing.T) {
		store := &fakeMemberStore{failOn: "UpsertActive"}
		_, g, _, _ := join(store)
		assert.NotNil(t, g.heads["c1"], "legacy row written")
		assert.Empty(t, store.rows)
	})

	t.Run("LeaveGym marks the head and its coaches left", func(t *testing.T) {
		store := &fakeMemberStore{}
		svc, _, gym, u := join(store)
		store.rows = append(store.rows, &db.GymMember{GymID: gym.ID.Hex(), UserID: "k1", Role: db.GymMemberRoleCoach,
			SupervisorID: "c1", Status: db.GymMemberStatusActive})
		u.ExpectedCalls = nil
		givenUser(u, gymUser("c1", "coach", db.Active, "coach@x.tn", true))
		require.Nil(t, svc.LeaveGym(ctx, "c1"))
		for _, r := range store.rows {
			assert.Equal(t, db.GymMemberStatusLeft, r.Status, r.UserID)
			assert.NotNil(t, r.LeftAt)
		}
	})

	t.Run("LeaveGym still succeeds when the dual-write fails", func(t *testing.T) {
		store := &fakeMemberStore{}
		svc, g, _, u := join(store)
		store.failOn = "MarkLeft"
		u.ExpectedCalls = nil
		givenUser(u, gymUser("c1", "coach", db.Active, "coach@x.tn", true))
		require.Nil(t, svc.LeaveGym(ctx, "c1"))
		assert.Nil(t, g.heads["c1"], "legacy row removed")
	})

	t.Run("AdminAddHeadCoach and AdminRemoveHeadCoach mirror", func(t *testing.T) {
		store := &fakeMemberStore{}
		svc, u, g := newMirroredGymSvc(store)
		gym := g.addGym("owner1", db.GymStatusActive)
		givenUser(u, gymUser("h1", "coach", db.Active, "h@x.tn", true))

		require.Nil(t, svc.AdminAddHeadCoach(ctx, "admin1", gym.ID.Hex(), "h1"))
		require.Len(t, store.rows, 1)
		assert.Equal(t, db.GymMemberSourceAdmin, store.rows[0].Source)
		assert.Equal(t, "admin1", store.rows[0].AddedBy)

		require.Nil(t, svc.AdminRemoveHeadCoach(ctx, "admin1", gym.ID.Hex(), "h1"))
		assert.Equal(t, db.GymMemberStatusLeft, store.rows[0].Status)
	})

	t.Run("RemoveHeadCoach by the owner mirrors", func(t *testing.T) {
		store := &fakeMemberStore{}
		svc, u, g := newMirroredGymSvc(store)
		gym := g.addGym("owner1", db.GymStatusActive)
		givenUser(u, gymUser("owner1", db.RoleGymOwner, db.Active, "o@x.tn", false))
		givenUser(u, gymUser("h1", "coach", db.Active, "h@x.tn", true))
		require.Nil(t, svc.AdminAddHeadCoach(ctx, "admin1", gym.ID.Hex(), "h1"))

		require.Nil(t, svc.RemoveHeadCoach(ctx, "owner1", gym.ID.Hex(), "h1"))
		assert.Equal(t, db.GymMemberStatusLeft, store.rows[0].Status)
	})
}

func TestDualWriteTeam(t *testing.T) {
	ctx := context.Background()

	setup := func(store *fakeMemberStore) (services.TeamService, *mocks.TeamStore) {
		u, ts := &mocks.TeamUserStore{}, &mocks.TeamStore{}
		givenUser(u, coachUser("c1", false, db.Active))
		givenUser(u, coachUser("h1", true, db.Active))
		return services.NewTeamService(u, ts, services.WithGymMembers(store)), ts
	}
	headRow := &db.GymMember{GymID: "g1", UserID: "h1", Role: db.GymMemberRoleHeadCoach, Status: db.GymMemberStatusActive}

	t.Run("assign under a head coach in a gym adds a coach row with the supervisor", func(t *testing.T) {
		store := &fakeMemberStore{rows: []*db.GymMember{headRow}}
		svc, ts := setup(store)
		ts.On("Upsert", mock.Anything, mock.Anything).Return(nil, nil)
		ts.On("InsertAudit", mock.Anything, auditAction("assign")).Return(nil)

		_, apiErr := svc.Assign(ctx, "admin1", "c1", "h1")
		require.Nil(t, apiErr)
		r := store.active("g1", "c1")
		require.NotNil(t, r)
		assert.Equal(t, db.GymMemberRoleCoach, r.Role)
		assert.Equal(t, "h1", r.SupervisorID)
		assert.Equal(t, "admin1", r.AddedBy)
	})

	t.Run("assign under a head coach outside any gym writes no row", func(t *testing.T) {
		store := &fakeMemberStore{}
		svc, ts := setup(store)
		ts.On("Upsert", mock.Anything, mock.Anything).Return(nil, nil)
		ts.On("InsertAudit", mock.Anything, auditAction("assign")).Return(nil)

		_, apiErr := svc.Assign(ctx, "admin1", "c1", "h1")
		require.Nil(t, apiErr)
		assert.Empty(t, store.rows)
	})

	t.Run("assign succeeds when the dual-write fails", func(t *testing.T) {
		store := &fakeMemberStore{rows: []*db.GymMember{headRow}, failOn: "UpsertActive"}
		svc, ts := setup(store)
		ts.On("Upsert", mock.Anything, mock.Anything).Return(nil, nil)
		ts.On("InsertAudit", mock.Anything, auditAction("assign")).Return(nil)

		m, apiErr := svc.Assign(ctx, "admin1", "c1", "h1")
		require.Nil(t, apiErr)
		assert.Equal(t, "h1", m.HeadCoachID)
		assert.Nil(t, store.active("g1", "c1"))
	})

	t.Run("a failed rollback does not write a row", func(t *testing.T) {
		store := &fakeMemberStore{rows: []*db.GymMember{headRow}}
		u, ts := &mocks.TeamUserStore{}, &mocks.TeamStore{}
		givenUser(u, coachUser("c1", false, db.Active))
		u.On("GetUserByExternalIdUpdate", mock.Anything, "h1").Return(coachUser("h1", true, db.Active), nil).Once()
		u.On("GetUserByExternalIdUpdate", mock.Anything, "h1").Return(coachUser("h1", false, db.Active), nil)
		ts.On("Upsert", mock.Anything, mock.Anything).Return(nil, nil)
		ts.On("DeleteByCoach", mock.Anything, "c1").Return(int64(1), nil)
		svc := services.NewTeamService(u, ts, services.WithGymMembers(store))

		_, apiErr := svc.Assign(ctx, "admin1", "c1", "h1")
		require.NotNil(t, apiErr)
		assert.Nil(t, store.active("g1", "c1"))
	})

	t.Run("unassign marks the coach row left", func(t *testing.T) {
		store := &fakeMemberStore{rows: []*db.GymMember{headRow,
			{GymID: "g1", UserID: "c1", Role: db.GymMemberRoleCoach, SupervisorID: "h1", Status: db.GymMemberStatusActive}}}
		svc, ts := setup(store)
		ts.On("GetHeadOf", mock.Anything, "c1").Return(&db.TeamMember{HeadCoachID: "h1", CoachID: "c1"}, nil)
		ts.On("DeleteByCoach", mock.Anything, "c1").Return(int64(1), nil)
		ts.On("InsertAudit", mock.Anything, auditAction("unassign")).Return(nil)

		require.Nil(t, svc.Unassign(ctx, "admin1", "c1"))
		assert.Nil(t, store.active("g1", "c1"))
		assert.NotNil(t, store.active("g1", "h1"), "the head coach row is untouched")
	})

	t.Run("unassign succeeds when the dual-write fails", func(t *testing.T) {
		store := &fakeMemberStore{failOn: "MarkLeftCoach"}
		svc, ts := setup(store)
		ts.On("GetHeadOf", mock.Anything, "c1").Return(&db.TeamMember{HeadCoachID: "h1", CoachID: "c1"}, nil)
		ts.On("DeleteByCoach", mock.Anything, "c1").Return(int64(1), nil)
		ts.On("InsertAudit", mock.Anything, auditAction("unassign")).Return(nil)

		require.Nil(t, svc.Unassign(ctx, "admin1", "c1"))
	})
}

type fakeTeamLister struct {
	ids map[string][]string
	err error
}

func (f fakeTeamLister) IDsByHead(_ context.Context, head string) ([]string, error) {
	return f.ids[head], f.err
}

func activeRow(gym, user string, role db.GymMemberRole, sup string) *db.GymMember {
	return &db.GymMember{GymID: gym, UserID: user, Role: role, SupervisorID: sup, Status: db.GymMemberStatusActive}
}

func TestDualWriteGymAdminGaps(t *testing.T) {
	ctx := context.Background()

	t.Run("AdminCreateGym upserts the owner row", func(t *testing.T) {
		store := &fakeMemberStore{}
		svc, u, _ := newMirroredGymSvc(store)
		givenUser(u, gymUser("owner1", db.RoleGymOwner, db.Active, "o@x.tn", false))
		v, apiErr := svc.AdminCreateGym(ctx, "admin1", "Iron Gym", "owner1")
		require.Nil(t, apiErr)
		r := store.active(v.GymID, "owner1")
		require.NotNil(t, r)
		assert.Equal(t, db.GymMemberRoleOwner, r.Role)
		assert.Equal(t, "admin1", r.AddedBy)
	})

	t.Run("AdminCreateGym succeeds when the dual-write fails", func(t *testing.T) {
		store := &fakeMemberStore{failOn: "UpsertActive"}
		svc, u, g := newMirroredGymSvc(store)
		givenUser(u, gymUser("owner1", db.RoleGymOwner, db.Active, "o@x.tn", false))
		_, apiErr := svc.AdminCreateGym(ctx, "admin1", "Iron Gym", "owner1")
		require.Nil(t, apiErr)
		assert.Len(t, g.gyms, 1)
		assert.Empty(t, store.rows)
	})

	t.Run("owner transfer upserts the new owner and closes the previous one", func(t *testing.T) {
		store := &fakeMemberStore{}
		svc, u, g := newMirroredGymSvc(store)
		givenUser(u, gymUser("owner2", db.RoleGymOwner, db.Active, "o2@x.tn", false))
		gym := g.addGym("owner1", db.GymStatusActive)
		gid := gym.ID.Hex()
		store.rows = append(store.rows, activeRow(gid, "owner1", db.GymMemberRoleOwner, ""))
		newOwner := "owner2"
		_, apiErr := svc.AdminUpdateGym(ctx, "admin1", gid, services.GymAdminUpdate{OwnerID: &newOwner})
		require.Nil(t, apiErr)
		assert.Nil(t, store.active(gid, "owner1"))
		assert.NotNil(t, store.active(gid, "owner2"))
	})

	t.Run("owner transfer succeeds when the dual-write fails", func(t *testing.T) {
		store := &fakeMemberStore{failOn: "UpsertActive"}
		svc, u, g := newMirroredGymSvc(store)
		givenUser(u, gymUser("owner2", db.RoleGymOwner, db.Active, "o2@x.tn", false))
		gym := g.addGym("owner1", db.GymStatusActive)
		newOwner := "owner2"
		v, apiErr := svc.AdminUpdateGym(ctx, "admin1", gym.ID.Hex(), services.GymAdminUpdate{OwnerID: &newOwner})
		require.Nil(t, apiErr)
		assert.Equal(t, "owner2", v.OwnerID)
	})

	t.Run("archive closes owner, head and coach rows", func(t *testing.T) {
		store := &fakeMemberStore{}
		svc, _, g := newMirroredGymSvc(store)
		gym := g.addGym("owner1", db.GymStatusActive)
		gid := gym.ID.Hex()
		g.heads["h1"] = &db.GymHeadCoach{GymID: gid, HeadCoachID: "h1"}
		store.rows = append(store.rows, activeRow(gid, "owner1", db.GymMemberRoleOwner, ""),
			activeRow(gid, "h1", db.GymMemberRoleHeadCoach, ""), activeRow(gid, "c1", db.GymMemberRoleCoach, "h1"))
		archived := db.GymStatusArchived
		_, apiErr := svc.AdminUpdateGym(ctx, "admin1", gid, services.GymAdminUpdate{Status: &archived})
		require.Nil(t, apiErr)
		for _, r := range store.rows {
			assert.Equal(t, db.GymMemberStatusLeft, r.Status, r.UserID)
		}
	})

	t.Run("archive succeeds when the dual-write fails", func(t *testing.T) {
		store := &fakeMemberStore{failOn: "MarkLeft"}
		svc, _, g := newMirroredGymSvc(store)
		gym := g.addGym("owner1", db.GymStatusActive)
		g.heads["h1"] = &db.GymHeadCoach{GymID: gym.ID.Hex(), HeadCoachID: "h1"}
		archived := db.GymStatusArchived
		v, apiErr := svc.AdminUpdateGym(ctx, "admin1", gym.ID.Hex(), services.GymAdminUpdate{Status: &archived})
		require.Nil(t, apiErr)
		assert.Equal(t, db.GymStatusArchived, v.Status)
		assert.Empty(t, g.heads)
	})

	t.Run("AdminAddHeadCoach mirrors the existing team", func(t *testing.T) {
		store := &fakeMemberStore{}
		u, g := &mocks.TeamUserStore{}, newFakeGymStore()
		svc := services.NewGymService(u, g, services.GymSettings{InviteTTL: time.Hour, InvitesPerDay: 20, Members: store,
			Team: fakeTeamLister{ids: map[string][]string{"h1": {"c1", "c2"}}}})
		gym := g.addGym("owner1", db.GymStatusActive)
		givenUser(u, gymUser("h1", "coach", db.Active, "h@x.tn", true))
		require.Nil(t, svc.AdminAddHeadCoach(ctx, "admin1", gym.ID.Hex(), "h1"))
		gid := gym.ID.Hex()
		require.NotNil(t, store.active(gid, "h1"))
		for _, c := range []string{"c1", "c2"} {
			r := store.active(gid, c)
			require.NotNil(t, r, c)
			assert.Equal(t, "h1", r.SupervisorID)
			assert.Equal(t, db.GymMemberRoleCoach, r.Role)
		}
	})

	t.Run("AdminAddHeadCoach succeeds when the team listing fails", func(t *testing.T) {
		store := &fakeMemberStore{}
		u, g := &mocks.TeamUserStore{}, newFakeGymStore()
		svc := services.NewGymService(u, g, services.GymSettings{InviteTTL: time.Hour, InvitesPerDay: 20, Members: store,
			Team: fakeTeamLister{err: errBoom}})
		gym := g.addGym("owner1", db.GymStatusActive)
		givenUser(u, gymUser("h1", "coach", db.Active, "h@x.tn", true))
		require.Nil(t, svc.AdminAddHeadCoach(ctx, "admin1", gym.ID.Hex(), "h1"))
		assert.NotNil(t, g.heads["h1"])
	})
}

func TestDualWriteTeamGaps(t *testing.T) {
	ctx := context.Background()

	demote := func(store *fakeMemberStore) (*fakeGymStore, error) {
		u, ts, g := &mocks.TeamUserStore{}, &mocks.TeamStore{}, newFakeGymStore()
		svc := services.NewTeamService(u, ts, services.WithGymMembership(g), services.WithGymMembers(store))
		g.heads["h1"] = &db.GymHeadCoach{GymID: "g1", HeadCoachID: "h1"}
		givenUser(u, coachUser("h1", true, db.Active))
		u.On("SetHeadCoach", mock.Anything, "h1", false, "admin1").Return(nil)
		ts.On("IDsByHead", mock.Anything, "h1").Return([]string{"c1"}, nil)
		ts.On("DeleteByHead", mock.Anything, "h1").Return(int64(1), nil)
		ts.On("InsertAuditMany", mock.Anything, mock.Anything).Return(nil)
		_, apiErr := svc.Demote(ctx, "admin1", "h1")
		if apiErr != nil {
			return g, apiErr.Error
		}
		return g, nil
	}

	t.Run("Demote closes the head and its coach rows", func(t *testing.T) {
		store := &fakeMemberStore{rows: []*db.GymMember{
			activeRow("g1", "h1", db.GymMemberRoleHeadCoach, ""), activeRow("g1", "c1", db.GymMemberRoleCoach, "h1")}}
		_, err := demote(store)
		require.NoError(t, err)
		assert.Nil(t, store.active("g1", "h1"))
		assert.Nil(t, store.active("g1", "c1"))
	})

	t.Run("Demote succeeds when the dual-write fails", func(t *testing.T) {
		store := &fakeMemberStore{failOn: "MarkLeft", rows: []*db.GymMember{activeRow("g1", "h1", db.GymMemberRoleHeadCoach, "")}}
		g, err := demote(store)
		require.NoError(t, err)
		assert.Empty(t, g.heads)
	})

	removeMine := func(store *fakeMemberStore) {
		u, ts := &mocks.TeamUserStore{}, &mocks.TeamStore{}
		svc := services.NewTeamService(u, ts, services.WithGymMembers(store))
		givenUser(u, coachUser("h1", true, db.Active))
		ts.On("GetHeadOf", mock.Anything, "c1").Return(&db.TeamMember{HeadCoachID: "h1", CoachID: "c1"}, nil)
		ts.On("DeleteByCoach", mock.Anything, "c1").Return(int64(1), nil)
		ts.On("InsertAudit", mock.Anything, auditAction("unassign")).Return(nil)
		require.Nil(t, svc.RemoveMyCoach(ctx, "h1", "c1"))
	}

	t.Run("RemoveMyCoach closes the coach row", func(t *testing.T) {
		store := &fakeMemberStore{rows: []*db.GymMember{activeRow("g1", "c1", db.GymMemberRoleCoach, "h1")}}
		removeMine(store)
		assert.Nil(t, store.active("g1", "c1"))
	})

	t.Run("RemoveMyCoach succeeds when the dual-write fails", func(t *testing.T) {
		removeMine(&fakeMemberStore{failOn: "MarkLeftCoach"})
	})
}

func TestReconcileMembers(t *testing.T) {
	desired := []db.GymMember{
		*activeRow("g1", "o1", db.GymMemberRoleOwner, ""),
		*activeRow("g1", "h1", db.GymMemberRoleHeadCoach, ""),
		*activeRow("g1", "c1", db.GymMemberRoleCoach, "h1"),
	}

	t.Run("in sync is an empty plan", func(t *testing.T) {
		plan := services.ReconcileMembers(desired, desired)
		assert.Empty(t, plan.Close)
		assert.Empty(t, plan.Fix)
		assert.Empty(t, plan.Create)
	})

	t.Run("valid rows are never closed", func(t *testing.T) {
		existing := append([]db.GymMember{}, desired...)
		existing = append(existing, *activeRow("g1", "c9", db.GymMemberRoleCoach, "h1"))
		plan := services.ReconcileMembers(existing, desired)
		require.Len(t, plan.Close, 1)
		assert.Equal(t, "c9", plan.Close[0].UserID)
	})

	t.Run("left rows are ignored and missing rows are created", func(t *testing.T) {
		existing := []db.GymMember{{GymID: "g1", UserID: "c1", Role: db.GymMemberRoleCoach, Status: db.GymMemberStatusLeft}}
		plan := services.ReconcileMembers(existing, desired)
		assert.Len(t, plan.Create, 3)
		assert.Empty(t, plan.Close)
	})

	t.Run("role and supervisor drift is corrected in place", func(t *testing.T) {
		existing := []db.GymMember{
			*activeRow("g1", "o1", db.GymMemberRoleOwner, ""),
			*activeRow("g1", "h1", db.GymMemberRoleHeadCoach, ""),
			*activeRow("g1", "c1", db.GymMemberRoleCoach, "h-old"),
		}
		plan := services.ReconcileMembers(existing, desired)
		assert.Empty(t, plan.Close, "a drifted but valid row is fixed, not closed")
		assert.Empty(t, plan.Create)
		require.Len(t, plan.Fix, 1)
		assert.Equal(t, "h1", plan.Fix[0].SupervisorID)

		existing[1].Role = db.GymMemberRoleCoach
		plan = services.ReconcileMembers(existing, desired)
		assert.Len(t, plan.Fix, 2)
	})

	t.Run("a moved coach is closed in the old gym and created in the new one", func(t *testing.T) {
		existing := []db.GymMember{*activeRow("g-old", "c1", db.GymMemberRoleCoach, "h0")}
		want := []db.GymMember{*activeRow("g-new", "c1", db.GymMemberRoleCoach, "h1")}
		plan := services.ReconcileMembers(existing, want)
		require.Len(t, plan.Close, 1)
		require.Len(t, plan.Create, 1)
		assert.Equal(t, "g-old", plan.Close[0].GymID, "closing first keeps the one-gym-per-coach index free")
		assert.Equal(t, "g-new", plan.Create[0].GymID)
	})
}

func TestJoinGymMirrorsExistingTeam(t *testing.T) {
	ctx := context.Background()
	store := &fakeMemberStore{}
	u, g := &mocks.TeamUserStore{}, newFakeGymStore()
	svc := services.NewGymService(u, g, services.GymSettings{InviteTTL: time.Hour, InvitesPerDay: 20, Members: store,
		Team: fakeTeamLister{ids: map[string][]string{"c1": {"k1", "k2"}}}})
	gym := g.addGym("owner1", db.GymStatusActive)
	inv := g.addInvite(gym, "coach@x.tn", db.GymInvitePending, time.Now().Add(time.Hour))
	givenUser(u, gymUser("c1", "coach", db.Active, "coach@x.tn", true))

	_, apiErr := svc.AcceptInvitation(ctx, "c1", inv.ID.Hex())
	require.Nil(t, apiErr)
	gid := gym.ID.Hex()
	require.NotNil(t, store.active(gid, "c1"))
	for _, k := range []string{"k1", "k2"} {
		r := store.active(gid, k)
		require.NotNil(t, r, k)
		assert.Equal(t, "c1", r.SupervisorID)
	}

	t.Run("a failing team listing does not fail the join", func(t *testing.T) {
		store := &fakeMemberStore{}
		u, g := &mocks.TeamUserStore{}, newFakeGymStore()
		svc := services.NewGymService(u, g, services.GymSettings{InviteTTL: time.Hour, InvitesPerDay: 20, Members: store,
			Team: fakeTeamLister{err: errBoom}})
		gym := g.addGym("owner1", db.GymStatusActive)
		inv := g.addInvite(gym, "coach@x.tn", db.GymInvitePending, time.Now().Add(time.Hour))
		givenUser(u, gymUser("c1", "coach", db.Active, "coach@x.tn", true))
		_, apiErr := svc.AcceptInvitation(ctx, "c1", inv.ID.Hex())
		require.Nil(t, apiErr)
		assert.NotNil(t, g.heads["c1"])
	})
}

// With no member store (default legacy config) every mirrored operation behaves as before.
func TestMirrorsDisabledWithoutStore(t *testing.T) {
	ctx := context.Background()

	t.Run("gym operations", func(t *testing.T) {
		svc, u, g := newGymSvc(20)
		gym := g.addGym("owner1", db.GymStatusActive)
		inv := g.addInvite(gym, "coach@x.tn", db.GymInvitePending, time.Now().Add(time.Hour))
		givenUser(u, gymUser("c1", "coach", db.Active, "coach@x.tn", true))
		givenUser(u, gymUser("owner1", db.RoleGymOwner, db.Active, "o@x.tn", false))
		givenUser(u, gymUser("owner2", db.RoleGymOwner, db.Active, "o2@x.tn", false))
		givenUser(u, gymUser("h2", "coach", db.Active, "h2@x.tn", true))

		_, apiErr := svc.AcceptInvitation(ctx, "c1", inv.ID.Hex())
		require.Nil(t, apiErr)
		require.Nil(t, svc.LeaveGym(ctx, "c1"))
		require.Nil(t, svc.AdminAddHeadCoach(ctx, "admin1", gym.ID.Hex(), "h2"))
		require.Nil(t, svc.AdminRemoveHeadCoach(ctx, "admin1", gym.ID.Hex(), "h2"))
		require.Nil(t, svc.AdminAddHeadCoach(ctx, "admin1", gym.ID.Hex(), "h2"))
		require.Nil(t, svc.RemoveHeadCoach(ctx, "owner1", gym.ID.Hex(), "h2"))
		_, apiErr = svc.AdminCreateGym(ctx, "admin1", "Second", "owner1")
		require.Nil(t, apiErr)
		newOwner, archived := "owner2", db.GymStatusArchived
		_, apiErr = svc.AdminUpdateGym(ctx, "admin1", gym.ID.Hex(), services.GymAdminUpdate{OwnerID: &newOwner})
		require.Nil(t, apiErr)
		_, apiErr = svc.AdminUpdateGym(ctx, "admin1", gym.ID.Hex(), services.GymAdminUpdate{Status: &archived})
		require.Nil(t, apiErr)
	})

	t.Run("team operations", func(t *testing.T) {
		svc, u, ts := newTeamSvc()
		givenUser(u, coachUser("c1", false, db.Active))
		givenUser(u, coachUser("h1", true, db.Active))
		ts.On("Upsert", mock.Anything, mock.Anything).Return(nil, nil)
		ts.On("InsertAudit", mock.Anything, mock.Anything).Return(nil)
		ts.On("GetHeadOf", mock.Anything, "c1").Return(&db.TeamMember{HeadCoachID: "h1", CoachID: "c1"}, nil)
		ts.On("DeleteByCoach", mock.Anything, "c1").Return(int64(1), nil)

		_, apiErr := svc.Assign(ctx, "admin1", "c1", "h1")
		require.Nil(t, apiErr)
		require.Nil(t, svc.Unassign(ctx, "admin1", "c1"))
		require.Nil(t, svc.RemoveMyCoach(ctx, "h1", "c1"))
	})
}

func TestOwnerWins(t *testing.T) {
	ctx := context.Background()
	gym := db.Gym{ID: primitive.NewObjectID(), OwnerID: "o1", Status: db.GymStatusActive, CreatedAt: time.Now()}
	gid := gym.ID.Hex()
	heads := []db.GymHeadCoach{{GymID: gid, HeadCoachID: "o1", AddedBy: "admin1", AddedAt: time.Now()}}
	team := []db.TeamMember{{HeadCoachID: "o1", CoachID: "c1", AssignedBy: "admin1", AssignedAt: time.Now()}}

	t.Run("backfill keeps one owner row for a user who is owner and head of the gym", func(t *testing.T) {
		rows := services.BackfillMembers([]db.Gym{gym}, heads, team)
		require.Len(t, rows, 2)
		assert.Equal(t, db.GymMemberRoleOwner, rows[0].Role)
		assert.Equal(t, "o1", rows[0].UserID)
		assert.Equal(t, db.GymMemberRoleCoach, rows[1].Role)
		assert.Equal(t, "o1", rows[1].SupervisorID)
	})

	t.Run("reconcile creates the owner once and converges", func(t *testing.T) {
		desired := services.BackfillMembers([]db.Gym{gym}, heads, team)
		first := services.ReconcileMembers(nil, desired)
		assert.Len(t, first.Create, 2, "never two creates for one (gym, user)")

		// Even a raw duplicated desired list is deduped by owner > head_coach > coach.
		dup := []db.GymMember{*activeRow(gid, "o1", db.GymMemberRoleHeadCoach, ""), *activeRow(gid, "o1", db.GymMemberRoleOwner, "")}
		plan := services.ReconcileMembers(nil, dup)
		require.Len(t, plan.Create, 1)
		assert.Equal(t, db.GymMemberRoleOwner, plan.Create[0].Role)

		// Second run: existing rows are what the first run created, so the plan is empty.
		existing := append([]db.GymMember{}, first.Create...)
		second := services.ReconcileMembers(existing, desired)
		assert.Empty(t, second.Create)
		assert.Empty(t, second.Fix, "no role flipping between runs")
		assert.Empty(t, second.Close)
	})

	t.Run("mirrors do not overwrite the owner row", func(t *testing.T) {
		store := &fakeMemberStore{rows: []*db.GymMember{activeRow(gid, "o1", db.GymMemberRoleOwner, "")}}
		u, g := &mocks.TeamUserStore{}, newFakeGymStore()
		g.gyms[gid] = &gym
		svc := services.NewGymService(u, g, services.GymSettings{InviteTTL: time.Hour, InvitesPerDay: 20, Members: store,
			Team: fakeTeamLister{ids: map[string][]string{"o1": {"c1"}}}})
		givenUser(u, gymUser("o1", "coach", db.Active, "o@x.tn", true))

		require.Nil(t, svc.AdminAddHeadCoach(ctx, "admin1", gid, "o1"))
		r := store.active(gid, "o1")
		require.NotNil(t, r)
		assert.Equal(t, db.GymMemberRoleOwner, r.Role, "owner row untouched")
		c := store.active(gid, "c1")
		require.NotNil(t, c)
		assert.Equal(t, "o1", c.SupervisorID)

		require.Nil(t, svc.AdminRemoveHeadCoach(ctx, "admin1", gid, "o1"))
		require.NotNil(t, store.active(gid, "o1"), "the owner row survives the head coach removal")
		assert.Nil(t, store.active(gid, "c1"), "supervised coach rows are closed")
	})
}

func TestLegacyAdapterOwnerWins(t *testing.T) {
	g := newFakeGymStore()
	gym := g.addGym("o1", db.GymStatusActive)
	g.heads["o1"] = &db.GymHeadCoach{GymID: gym.ID.Hex(), HeadCoachID: "o1"}
	ts := &mocks.TeamStore{}
	ts.On("GetHeadOf", mock.Anything, "o1").Return(nil, nil)

	got, err := services.NewLegacyMembershipAdapter(g, ts).MembershipsOf(context.Background(), "o1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, db.GymMemberRoleOwner, got[0].Role)
}

func (f *fakeMemberStore) AddActive(_ context.Context, m *db.GymMember) error {
	if f.failOn == "AddActive" {
		return errBoom
	}
	if f.active(m.GymID, m.UserID) != nil {
		return repositories.ErrAlreadyMember
	}
	if m.Role != db.GymMemberRoleOwner {
		for _, r := range f.rows {
			if r.UserID == m.UserID && r.Status == db.GymMemberStatusActive && r.Role != db.GymMemberRoleOwner {
				return repositories.ErrAlreadyMember
			}
		}
	}
	c := *m
	c.Status, c.SeatAssigned = db.GymMemberStatusActive, false
	f.rows = append(f.rows, &c)
	return nil
}

func (f *fakeMemberStore) SetSupervisor(_ context.Context, gym, user, prev, sup string) (bool, error) {
	if f.failOn == "SetSupervisor" {
		return false, errBoom
	}
	r := f.active(gym, user)
	if r == nil || r.Role != db.GymMemberRoleCoach || r.SupervisorID != prev {
		return false, nil
	}
	r.SupervisorID, r.Source = sup, db.GymMemberSourceFlat
	return true, nil
}

func (f *fakeMemberStore) CountGymsWithoutOwnerRow(context.Context) (int, error) {
	if f.failOn == "CountGymsWithoutOwnerRow" {
		return 0, errBoom
	}
	return f.missingOwners, nil
}

// fakeProfiles maps user id to {firstname, lastname, picture} for the joined list.
var fakeProfiles = map[string][3]string{}

func (f *fakeMemberStore) row(r db.GymMember) db.GymMemberRow {
	p := fakeProfiles[r.UserID]
	return db.GymMemberRow{GymMember: r, Firstname: p[0], Lastname: p[1], ProfilePicture: p[2]}
}

func (f *fakeMemberStore) GetRow(_ context.Context, gym, user string) (*db.GymMemberRow, error) {
	if f.failOn == "GetRow" {
		return nil, errBoom
	}
	if r := f.active(gym, user); r != nil {
		row := f.row(*r)
		return &row, nil
	}
	return nil, nil
}

func (f *fakeMemberStore) ListByGymPage(_ context.Context, gym, scope, role string, page, size int) ([]db.GymMemberRow, int64, error) {
	if f.failOn == "ListByGymPage" {
		return nil, 0, errBoom
	}
	all := []db.GymMemberRow{}
	for _, r := range f.rows {
		if r.GymID == gym && r.Status == db.GymMemberStatusActive && (scope == "" || r.UserID == scope || r.SupervisorID == scope) && (role == "" || string(r.Role) == role) {
			all = append(all, f.row(*r))
		}
	}
	lo := (page - 1) * size
	if lo > len(all) {
		lo = len(all)
	}
	hi := lo + size
	if hi > len(all) {
		hi = len(all)
	}
	return all[lo:hi], int64(len(all)), nil
}
