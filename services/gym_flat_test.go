package services_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
	"coachify-account-api/services"
	"coachify-account-api/services/mocks"
)

const flatGym = "G1"

func flatRow(user string, role db.GymMemberRole, sup string) *db.GymMember {
	return &db.GymMember{GymID: flatGym, UserID: user, Role: role, SupervisorID: sup, Status: db.GymMemberStatusActive, JoinedAt: time.Now()}
}

// flatFixture: gym G1 with owner o, heads h (supervising c1) and h2, coaches c1 (under h) and c2 (no supervisor),
// plus a coach x in another gym.
func flatFixture() *fakeMemberStore {
	other := flatRow("x", db.GymMemberRoleCoach, "")
	other.GymID = "G2"
	return &fakeMemberStore{rows: []*db.GymMember{
		flatRow("o", db.GymMemberRoleOwner, ""),
		flatRow("h", db.GymMemberRoleHeadCoach, ""),
		flatRow("h2", db.GymMemberRoleHeadCoach, ""),
		flatRow("c1", db.GymMemberRoleCoach, "h"),
		flatRow("c2", db.GymMemberRoleCoach, ""),
		other,
	}}
}

type flatEnv struct {
	svc    services.GymMembersService
	store  *fakeMemberStore
	team   *mocks.TeamStore
	policy *fakePolicyStore
}

func newFlatEnv(enabled bool) *flatEnv {
	fakeProfiles = map[string][3]string{"c1": {"Cora", "One", "http://img/c1.png"}, "o": {"Olga", "Owner", ""}}
	store, policy, team := flatFixture(), newFakePolicyStore(), &mocks.TeamStore{}
	authz := services.NewCachingAuthorizer(services.NewChainAuthorizer(services.NewRepoMembershipReader(store), policy, nil), time.Hour)
	guarded := services.NewInvalidatingMemberStore(store, authz, policy)
	return &flatEnv{svc: services.NewGymMembersService(authz, guarded, team, enabled), store: store, team: team, policy: policy}
}

func (e *flatEnv) expectProjection() {
	e.team.On("Upsert", mock.Anything, mock.Anything).Return(nil, nil)
	e.team.On("DeleteByCoach", mock.Anything, mock.Anything).Return(int64(1), nil)
	e.team.On("InsertAudit", mock.Anything, mock.Anything).Return(nil)
}

func TestSetSupervisorWritesAuditPeriodProjectionAndBumpsVersion(t *testing.T) {
	ctx := context.Background()
	e := newFlatEnv(true)
	e.expectProjection()

	v, apiErr := e.svc.SetSupervisor(ctx, "o", flatGym, "c1", "h2")
	require.Nil(t, apiErr)
	require.NotNil(t, v.SupervisorID)
	assert.Equal(t, "h2", *v.SupervisorID)
	assert.Equal(t, "Cora", v.Firstname, "response carries the profile fields")
	assert.Equal(t, "http://img/c1.png", v.ProfilePicture)
	assert.Equal(t, "h2", e.store.active(flatGym, "c1").SupervisorID)

	// Projection: Upsert closes the open team period of the old head and opens one for the new head (team repository).
	e.team.AssertCalled(t, "Upsert", mock.Anything, mock.MatchedBy(func(m *db.TeamMember) bool {
		return m.CoachID == "c1" && m.HeadCoachID == "h2" && m.AssignedBy == "o" && m.AssignedByRole == db.TeamAssignerGymOwner
	}))
	e.team.AssertCalled(t, "InsertAudit", mock.Anything, mock.MatchedBy(func(a *db.TeamAuditEntry) bool {
		return a.Action == db.TeamActionSupervisorChange && a.ActorID == "o" && a.CoachID == "c1" &&
			a.HeadCoachID == "h2" && a.PrevHeadCoachID == "h" && a.GymID == flatGym
	}))
	assert.EqualValues(t, 1, e.policy.versions[flatGym], "authz_version bumped through the invalidating store")
}

func TestSetSupervisorClearAndNoop(t *testing.T) {
	ctx := context.Background()
	e := newFlatEnv(true)
	e.expectProjection()

	v, apiErr := e.svc.SetSupervisor(ctx, "o", flatGym, "c1", "")
	require.Nil(t, apiErr)
	assert.Nil(t, v.SupervisorID)
	assert.Equal(t, "", e.store.active(flatGym, "c1").SupervisorID)
	e.team.AssertCalled(t, "DeleteByCoach", mock.Anything, "c1")

	e.team.Calls = nil
	_, apiErr = e.svc.SetSupervisor(ctx, "o", flatGym, "c2", "")
	require.Nil(t, apiErr)
	assert.Empty(t, e.team.Calls, "unchanged supervisor writes nothing")
}

func TestSetSupervisorInvalid(t *testing.T) {
	cases := map[string]struct{ target, sup string }{
		"supervisor is a coach":      {"c1", "c2"},
		"supervisor is the owner":    {"c1", "o"},
		"supervisor in another gym":  {"c1", "x"},
		"supervisor unknown":         {"c1", "ghost"},
		"self supervision (cycle)":   {"c1", "c1"},
		"target is the owner":        {"o", "h"},
		"target is a head coach":     {"h", "h2"},
		"head coach under his coach": {"h", "c1"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := newFlatEnv(true)
			e.expectProjection()
			prev := e.store.active(flatGym, tc.target).SupervisorID
			_, apiErr := e.svc.SetSupervisor(context.Background(), "o", flatGym, tc.target, tc.sup)
			require.NotNil(t, apiErr)
			assert.Equal(t, http.StatusBadRequest, apiErr.Code)
			assert.Equal(t, prev, e.store.active(flatGym, tc.target).SupervisorID)
			e.team.AssertNotCalled(t, "Upsert", mock.Anything, mock.Anything)
		})
	}

	t.Run("unknown target is 404", func(t *testing.T) {
		e := newFlatEnv(true)
		_, apiErr := e.svc.SetSupervisor(context.Background(), "o", flatGym, "ghost", "h")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)
	})
}

func TestSetSupervisorAuthzMatrix(t *testing.T) {
	cases := []struct {
		caller string
		code   int
	}{{"h", http.StatusForbidden}, {"c1", http.StatusForbidden}, {"stranger", http.StatusNotFound}, {"x", http.StatusNotFound}}
	for _, tc := range cases {
		t.Run(tc.caller, func(t *testing.T) {
			e := newFlatEnv(true)
			_, apiErr := e.svc.SetSupervisor(context.Background(), tc.caller, flatGym, "c2", "h")
			require.NotNil(t, apiErr)
			assert.Equal(t, tc.code, apiErr.Code)
			assert.Equal(t, "", e.store.active(flatGym, "c2").SupervisorID)
		})
	}
}

func TestSetSupervisorFlagOffIs404(t *testing.T) {
	e := newFlatEnv(false)
	_, apiErr := e.svc.SetSupervisor(context.Background(), "o", flatGym, "c2", "h")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusNotFound, apiErr.Code)
	_, _, apiErr = e.svc.ListMembers(context.Background(), "o", flatGym, "", 1, 20)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusNotFound, apiErr.Code)
	assert.Empty(t, e.team.Calls)
}

func TestSetSupervisorProjectionFailureReverts(t *testing.T) {
	e := newFlatEnv(true)
	e.team.On("Upsert", mock.Anything, mock.Anything).Return(nil, errors.New("boom"))
	_, apiErr := e.svc.SetSupervisor(context.Background(), "o", flatGym, "c2", "h")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
	assert.Equal(t, "", e.store.active(flatGym, "c2").SupervisorID, "reverted")
}

func ids(rows []services.GymMemberView) []string {
	out := []string{}
	for _, r := range rows {
		out = append(out, r.UserID)
	}
	return out
}

func TestListMembersVisibility(t *testing.T) {
	ctx := context.Background()
	e := newFlatEnv(true)

	rows, total, apiErr := e.svc.ListMembers(ctx, "o", flatGym, "", 1, 20)
	require.Nil(t, apiErr)
	assert.ElementsMatch(t, []string{"o", "h", "h2", "c1", "c2"}, ids(rows))
	assert.EqualValues(t, 5, total)

	for _, r := range rows {
		if r.UserID == "c1" {
			assert.Equal(t, "Cora", r.Firstname)
			assert.Equal(t, "One", r.Lastname)
			assert.Equal(t, "http://img/c1.png", r.ProfilePicture)
			require.NotNil(t, r.SupervisorID)
			assert.Equal(t, "h", *r.SupervisorID)
			assert.Equal(t, "coach", r.Role)
			assert.Equal(t, "active", r.Status)
		}
		if r.UserID == "c2" {
			assert.Equal(t, "", r.Firstname, "missing user document gives empty profile fields")
			assert.Equal(t, "", r.ProfilePicture)
			assert.Nil(t, r.SupervisorID)
		}
	}

	rows, total, apiErr = e.svc.ListMembers(ctx, "h", flatGym, "", 1, 20)
	require.Nil(t, apiErr)
	assert.ElementsMatch(t, []string{"h", "c1"}, ids(rows), "head coach: self and own supervisees only")
	assert.EqualValues(t, 2, total)

	_, _, apiErr = e.svc.ListMembers(ctx, "c1", flatGym, "", 1, 20)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusForbidden, apiErr.Code)

	_, _, apiErr = e.svc.ListMembers(ctx, "stranger", flatGym, "", 1, 20)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusNotFound, apiErr.Code)

	rows, total, apiErr = e.svc.ListMembers(ctx, "o", flatGym, "", 2, 2)
	require.Nil(t, apiErr)
	assert.Len(t, rows, 2)
	assert.EqualValues(t, 5, total)
}

// ---------- joining and inviting ----------

type joinEnv struct {
	svc   services.GymService
	users *mocks.TeamUserStore
	gyms  *fakeGymStore
	store *fakeMemberStore
	team  *mocks.TeamStore
	gym   *db.Gym
}

func newJoinEnv(flag bool) *joinEnv {
	users, gyms, team := &mocks.TeamUserStore{}, newFakeGymStore(), &mocks.TeamStore{}
	gym := gyms.addGym("o", db.GymStatusActive)
	gid := gym.ID.Hex()
	mk := func(user string, role db.GymMemberRole) *db.GymMember {
		r := flatRow(user, role, "")
		r.GymID = gid
		return r
	}
	store := &fakeMemberStore{rows: []*db.GymMember{mk("o", db.GymMemberRoleOwner), mk("h", db.GymMemberRoleHeadCoach)}}
	authz := services.NewChainAuthorizer(services.NewRepoMembershipReader(store), newFakePolicyStore(), nil)
	svc := services.NewGymService(users, gyms, services.GymSettings{
		InviteTTL: time.Hour, InvitesPerDay: 20, Authz: authz, Members: store, FlatHierarchy: flag, Projector: team,
	})
	return &joinEnv{svc: svc, users: users, gyms: gyms, store: store, team: team, gym: gym}
}

func (e *joinEnv) coachInvite(email, supervisor string) *db.GymInvitation {
	inv := e.gyms.addInvite(e.gym, email, db.GymInvitePending, time.Now().Add(time.Hour))
	inv.Role, inv.SupervisorID = db.GymInviteRoleCoach, supervisor
	return inv
}

func TestCoachJoinsWithoutHeadCoach(t *testing.T) {
	ctx := context.Background()
	e := newJoinEnv(true)
	givenUser(e.users, gymUser("c1", "coach", db.Active, "c1@x.tn", false))
	inv := e.coachInvite("c1@x.tn", "")

	joined, apiErr := e.svc.AcceptInvitation(ctx, "c1", inv.ID.Hex())
	require.Nil(t, apiErr)
	assert.Equal(t, e.gym.ID.Hex(), joined.GymID)
	row := e.store.active(e.gym.ID.Hex(), "c1")
	require.NotNil(t, row)
	assert.Equal(t, db.GymMemberRoleCoach, row.Role)
	assert.Equal(t, "", row.SupervisorID)
	assert.Nil(t, e.gyms.heads["c1"], "no legacy head coach row")
	assert.Equal(t, db.GymInviteAccepted, inv.Status)
	assert.Equal(t, []string{"member_join", "gym_invite_accept"}, e.gyms.actions())
	e.users.AssertNotCalled(t, "SetHeadCoach", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	e.team.AssertNotCalled(t, "Upsert", mock.Anything, mock.Anything)
}

func TestCoachJoinsUnderSupervisorKeepsProjection(t *testing.T) {
	e := newJoinEnv(true)
	givenUser(e.users, gymUser("c1", "coach", db.Active, "c1@x.tn", false))
	e.team.On("Upsert", mock.Anything, mock.Anything).Return(nil, nil)
	inv := e.coachInvite("c1@x.tn", "h")

	_, apiErr := e.svc.AcceptInvitation(context.Background(), "c1", inv.ID.Hex())
	require.Nil(t, apiErr)
	assert.Equal(t, "h", e.store.active(e.gym.ID.Hex(), "c1").SupervisorID)
	e.team.AssertCalled(t, "Upsert", mock.Anything, mock.MatchedBy(func(m *db.TeamMember) bool {
		return m.CoachID == "c1" && m.HeadCoachID == "h"
	}))
}

func TestCoachJoinDropsStaleSupervisor(t *testing.T) {
	e := newJoinEnv(true)
	givenUser(e.users, gymUser("c1", "coach", db.Active, "c1@x.tn", false))
	inv := e.coachInvite("c1@x.tn", "gone")

	_, apiErr := e.svc.AcceptInvitation(context.Background(), "c1", inv.ID.Hex())
	require.Nil(t, apiErr)
	assert.Equal(t, "", e.store.active(e.gym.ID.Hex(), "c1").SupervisorID)
}

// racyStore reports no membership on read but a duplicate on insert (two concurrent joins).
type racyStore struct{ *fakeMemberStore }

func (r *racyStore) ListByUser(context.Context, string) ([]db.GymMember, error) {
	return []db.GymMember{}, nil
}
func (r *racyStore) AddActive(context.Context, *db.GymMember) error { return repositories.ErrAlreadyMember }

func TestCoachJoinOneGymPerCoachIs409(t *testing.T) {
	ctx := context.Background()
	t.Run("already a coach elsewhere", func(t *testing.T) {
		e := newJoinEnv(true)
		other := flatRow("c1", db.GymMemberRoleCoach, "")
		other.GymID = "elsewhere"
		e.store.rows = append(e.store.rows, other)
		givenUser(e.users, gymUser("c1", "coach", db.Active, "c1@x.tn", false))
		inv := e.coachInvite("c1@x.tn", "")

		_, apiErr := e.svc.AcceptInvitation(ctx, "c1", inv.ID.Hex())
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
		assert.Equal(t, db.GymInvitePending, inv.Status)
		assert.NotNil(t, e.store.active("elsewhere", "c1"), "other membership untouched")
	})

	t.Run("duplicate key race maps to 409 and releases the invitation", func(t *testing.T) {
		e := newJoinEnv(true)
		givenUser(e.users, gymUser("c1", "coach", db.Active, "c1@x.tn", false))
		inv := e.coachInvite("c1@x.tn", "")
		svc := services.NewGymService(e.users, e.gyms, services.GymSettings{
			InviteTTL: time.Hour, InvitesPerDay: 20, Members: &racyStore{fakeMemberStore: e.store}, FlatHierarchy: true,
		})

		_, apiErr := svc.AcceptInvitation(ctx, "c1", inv.ID.Hex())
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
		assert.Equal(t, db.GymInvitePending, inv.Status)
	})

	t.Run("head coach cannot join as coach", func(t *testing.T) {
		e := newJoinEnv(true)
		givenUser(e.users, gymUser("c1", "coach", db.Active, "c1@x.tn", true))
		inv := e.coachInvite("c1@x.tn", "")
		_, apiErr := e.svc.AcceptInvitation(ctx, "c1", inv.ID.Hex())
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
	})
}

func TestCoachInviteIgnoredWhenFlagOff(t *testing.T) {
	e := newJoinEnv(false)
	givenUser(e.users, gymUser("c1", "coach", db.Active, "c1@x.tn", false))
	inv := e.coachInvite("c1@x.tn", "")

	_, apiErr := e.svc.AcceptInvitation(context.Background(), "c1", inv.ID.Hex())
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusNotFound, apiErr.Code)
	assert.Nil(t, e.store.active(e.gym.ID.Hex(), "c1"))
	assert.Nil(t, e.gyms.heads["c1"])
	e.users.AssertNotCalled(t, "SetHeadCoach", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	assert.Equal(t, db.GymInvitePending, inv.Status)

	_, apiErr = e.svc.InviteCoach(context.Background(), "o", e.gym.ID.Hex(), "n@x.tn", "")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusNotFound, apiErr.Code)
}

func TestInviteCoachAuthz(t *testing.T) {
	ctx := context.Background()
	t.Run("owner invites with a supervisor", func(t *testing.T) {
		e := newJoinEnv(true)
		res, apiErr := e.svc.InviteCoach(ctx, "o", e.gym.ID.Hex(), "n@x.tn", "h")
		require.Nil(t, apiErr)
		inv := e.gyms.invites[res.InvitationID]
		assert.Equal(t, db.GymInviteRoleCoach, inv.Role)
		assert.Equal(t, "h", inv.SupervisorID)
	})
	t.Run("head coach invites under himself", func(t *testing.T) {
		e := newJoinEnv(true)
		link, apiErr := e.svc.CreateCoachJoinLink(ctx, "h", e.gym.ID.Hex(), "")
		require.Nil(t, apiErr)
		inv := e.gyms.invites[link.InvitationID]
		assert.Equal(t, "h", inv.SupervisorID)
		assert.Equal(t, db.GymInviteRoleCoach, inv.Role)
		assert.Equal(t, []string{"gym_invite"}, e.gyms.actions())
		assert.Equal(t, db.TeamActorRoleHead, e.gyms.audits[0].ActorRole)
	})
	t.Run("head coach cannot name another supervisor", func(t *testing.T) {
		e := newJoinEnv(true)
		_, apiErr := e.svc.InviteCoach(ctx, "h", e.gym.ID.Hex(), "n@x.tn", "o")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code)
	})
	t.Run("owner names a non-head supervisor", func(t *testing.T) {
		e := newJoinEnv(true)
		_, apiErr := e.svc.InviteCoach(ctx, "o", e.gym.ID.Hex(), "n@x.tn", "o")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code)
	})
	t.Run("plain coach is 403, stranger 404", func(t *testing.T) {
		e := newJoinEnv(true)
		c := flatRow("c9", db.GymMemberRoleCoach, "")
		c.GymID = e.gym.ID.Hex()
		e.store.rows = append(e.store.rows, c)
		_, apiErr := e.svc.InviteCoach(ctx, "c9", e.gym.ID.Hex(), "n@x.tn", "")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusForbidden, apiErr.Code)
		_, apiErr = e.svc.InviteCoach(ctx, "stranger", e.gym.ID.Hex(), "n@x.tn", "")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)
	})
}
