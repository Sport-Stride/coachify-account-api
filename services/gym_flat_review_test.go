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
	"coachify-account-api/services"
)

// ---------- 1. reconcile never touches flat rows ----------

func TestReconcileSkipsFlatRows(t *testing.T) {
	flat := func(user, sup string) db.GymMember {
		return db.GymMember{GymID: "g", UserID: user, Role: db.GymMemberRoleCoach, SupervisorID: sup,
			Status: db.GymMemberStatusActive, Source: db.GymMemberSourceFlat}
	}
	existing := []db.GymMember{
		flat("direct", ""), // no legacy counterpart
		flat("moved", "hX"), // legacy still says h1
		{GymID: "g", UserID: "stale", Role: db.GymMemberRoleCoach, Status: db.GymMemberStatusActive, Source: db.GymMemberSourceLegacyTeam},
	}
	desired := []db.GymMember{
		{GymID: "g", UserID: "moved", Role: db.GymMemberRoleCoach, SupervisorID: "h1", Status: db.GymMemberStatusActive, Source: db.GymMemberSourceLegacyTeam},
	}
	plan := services.ReconcileMembers(existing, desired)
	require.Len(t, plan.Close, 1, "only the non-flat stale row is closed")
	assert.Equal(t, "stale", plan.Close[0].UserID)
	assert.Empty(t, plan.Fix, "a flat row's supervisor_id is never reset")
	assert.Empty(t, plan.Create, "a flat row already is the active row")
}

// ---------- 2. startup guard ----------

func TestCheckFlatHierarchyReady(t *testing.T) {
	ctx := context.Background()
	assert.NoError(t, services.CheckFlatHierarchyReady(ctx, &fakeMemberStore{}))

	err := services.CheckFlatHierarchyReady(ctx, &fakeMemberStore{missingOwners: 2})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "backfill")
	assert.Contains(t, err.Error(), "2 active gym")

	err = services.CheckFlatHierarchyReady(ctx, &fakeMemberStore{failOn: "CountGymsWithoutOwnerRow"})
	require.Error(t, err, "an unreadable state also refuses to start")
}

// ---------- helpers ----------

// flipMembers answers ListByUser with no rows on the first call and the real rows afterwards: a flat join landing
// between the check and the write.
type flipMembers struct {
	*fakeMemberStore
	calls int
	late  *db.GymMember
}

func (f *flipMembers) ListByUser(ctx context.Context, user string) ([]db.GymMember, error) {
	f.calls++
	if f.calls == 1 {
		return []db.GymMember{}, nil
	}
	return f.fakeMemberStore.ListByUser(ctx, user)
}

// flipTeam answers IsTeamMember false once and true afterwards (a concurrent team assignment).
type flipTeam struct {
	*fakeGymStore
	calls int
}

func (f *flipTeam) IsTeamMember(ctx context.Context, id string) (bool, error) {
	f.calls++
	return f.calls > 1, nil
}

// strictGyms refuses TransitionInvitation on a cancelled context, like a real Mongo call would.
type strictGyms struct{ *fakeGymStore }

func (s *strictGyms) TransitionInvitation(ctx context.Context, id, from, to, invitee string, at time.Time) (bool, error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	return s.fakeGymStore.TransitionInvitation(ctx, id, from, to, invitee, at)
}

func (e *joinEnv) svcWith(gyms services.GymStore, members services.GymMemberStore) services.GymService {
	return services.NewGymService(e.users, gyms, services.GymSettings{
		InviteTTL: time.Hour, InvitesPerDay: 20, Members: members, FlatHierarchy: true, Projector: e.team,
	})
}

// ---------- 3. head coach join is rejected for a flat coach ----------

func TestHeadJoinRejectedForFlatCoach(t *testing.T) {
	ctx := context.Background()
	headInvite := func(e *joinEnv) *db.GymInvitation {
		return e.gyms.addInvite(e.gym, "c1@x.tn", db.GymInvitePending, time.Now().Add(time.Hour))
	}
	elsewhere := func() *db.GymMember {
		r := flatRow("c1", db.GymMemberRoleCoach, "")
		r.GymID = "elsewhere"
		return r
	}

	t.Run("existing coach row is 409", func(t *testing.T) {
		e := newJoinEnv(true)
		e.store.rows = append(e.store.rows, elsewhere())
		givenUser(e.users, gymUser("c1", "coach", db.Active, "c1@x.tn", false))
		inv := headInvite(e)

		_, apiErr := e.svc.AcceptInvitation(ctx, "c1", inv.ID.Hex())
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
		assert.Equal(t, db.GymInvitePending, inv.Status)
		e.users.AssertNotCalled(t, "SetHeadCoach", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("a coach row that appears meanwhile is compensated", func(t *testing.T) {
		e := newJoinEnv(true)
		flip := &flipMembers{fakeMemberStore: e.store}
		e.store.rows = append(e.store.rows, elsewhere())
		givenUser(e.users, gymUser("c1", "coach", db.Active, "c1@x.tn", false))
		e.users.On("SetHeadCoach", mock.Anything, "c1", true, "o").Return(nil)
		e.users.On("SetHeadCoach", mock.Anything, "c1", false, "o").Return(nil)
		inv := headInvite(e)

		_, apiErr := e.svcWith(e.gyms, flip).AcceptInvitation(ctx, "c1", inv.ID.Hex())
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
		assert.Equal(t, db.GymInvitePending, inv.Status, "claim undone")
		assert.Equal(t, "", inv.InviteeID, "invitee cleared")
		assert.Nil(t, e.gyms.heads["c1"])
		e.users.AssertCalled(t, "SetHeadCoach", mock.Anything, "c1", false, "o")
	})
}

// ---------- 4. projection failure on a direct join ----------

func TestCoachJoinProjectionFailureReverts(t *testing.T) {
	e := newJoinEnv(true)
	givenUser(e.users, gymUser("c1", "coach", db.Active, "c1@x.tn", false))
	ctx, cancel := context.WithCancel(context.Background())
	// The request is cancelled while the projection runs: compensation must still happen.
	e.team.On("Upsert", mock.Anything, mock.Anything).Run(func(mock.Arguments) { cancel() }).Return(nil, errors.New("boom"))
	inv := e.coachInvite("c1@x.tn", "h")

	_, apiErr := e.svcWith(&strictGyms{e.gyms}, e.store).AcceptInvitation(ctx, "c1", inv.ID.Hex())
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
	assert.Nil(t, e.store.active(e.gym.ID.Hex(), "c1"), "membership closed")
	assert.Equal(t, db.GymInvitePending, inv.Status, "claim undone on a detached context")
	assert.Equal(t, "", inv.InviteeID)
}

func TestCoachJoinRowIsFlatSource(t *testing.T) {
	e := newJoinEnv(true)
	givenUser(e.users, gymUser("c1", "coach", db.Active, "c1@x.tn", false))
	inv := e.coachInvite("c1@x.tn", "")
	_, apiErr := e.svc.AcceptInvitation(context.Background(), "c1", inv.ID.Hex())
	require.Nil(t, apiErr)
	assert.Equal(t, db.GymMemberSourceFlat, e.store.active(e.gym.ID.Hex(), "c1").Source)
}

// ---------- 6. team assignment racing a direct join ----------

func TestCoachJoinRecheckTeamMember(t *testing.T) {
	e := newJoinEnv(true)
	givenUser(e.users, gymUser("c1", "coach", db.Active, "c1@x.tn", false))
	inv := e.coachInvite("c1@x.tn", "")

	_, apiErr := e.svcWith(&flipTeam{fakeGymStore: e.gyms}, e.store).AcceptInvitation(context.Background(), "c1", inv.ID.Hex())
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusConflict, apiErr.Code)
	assert.Nil(t, e.store.active(e.gym.ID.Hex(), "c1"), "membership closed")
	assert.Equal(t, db.GymInvitePending, inv.Status)
	e.team.AssertNotCalled(t, "Upsert", mock.Anything, mock.Anything)
}

// ---------- 5. supervisor compare-and-set ----------

// racingStore runs a hook around SetSupervisor to simulate a concurrent writer.
type racingStore struct {
	*fakeMemberStore
	before, after func()
}

func (r *racingStore) SetSupervisor(ctx context.Context, gym, user, prev, sup string) (bool, error) {
	if r.before != nil {
		r.before()
	}
	ok, err := r.fakeMemberStore.SetSupervisor(ctx, gym, user, prev, sup)
	if r.after != nil {
		r.after()
	}
	return ok, err
}

func TestSetSupervisorLostRaceIs409(t *testing.T) {
	ctx := context.Background()
	e := newFlatEnv(true)
	e.expectProjection()
	racing := &racingStore{fakeMemberStore: e.store}
	svc := services.NewGymMembersService(
		services.NewChainAuthorizer(services.NewRepoMembershipReader(e.store), e.policy, nil), racing, e.team, true)

	t.Run("owner change between read and write", func(t *testing.T) {
		racing.before = func() { e.store.active(flatGym, "c2").SupervisorID = "h2" }
		racing.after = nil
		_, apiErr := svc.SetSupervisor(ctx, "o", flatGym, "c2", "h")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
		assert.Equal(t, "h2", e.store.active(flatGym, "c2").SupervisorID, "the concurrent change is kept")
		e.team.AssertNotCalled(t, "Upsert", mock.Anything, mock.Anything)
	})

	t.Run("supervisor demoted after the write is compensated", func(t *testing.T) {
		e.store.active(flatGym, "c2").SupervisorID = ""
		racing.before = nil
		racing.after = func() { e.store.active(flatGym, "h").Role = db.GymMemberRoleCoach }
		_, apiErr := svc.SetSupervisor(ctx, "o", flatGym, "c2", "h")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
		assert.Equal(t, "", e.store.active(flatGym, "c2").SupervisorID, "reverted")
		e.team.AssertNotCalled(t, "Upsert", mock.Anything, mock.Anything)
	})

	t.Run("revert does not overwrite a newer owner change", func(t *testing.T) {
		e.store.active(flatGym, "h").Role = db.GymMemberRoleHeadCoach
		e.store.active(flatGym, "c2").SupervisorID = ""
		racing.before = nil
		racing.after = func() {
			e.store.active(flatGym, "h").Role = db.GymMemberRoleCoach
			e.store.active(flatGym, "c2").SupervisorID = "h2" // another owner request landed
		}
		_, apiErr := svc.SetSupervisor(ctx, "o", flatGym, "c2", "h")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
		assert.Equal(t, "h2", e.store.active(flatGym, "c2").SupervisorID)
	})
}

func TestSetSupervisorMarksRowFlat(t *testing.T) {
	e := newFlatEnv(true)
	e.expectProjection()
	_, apiErr := e.svc.SetSupervisor(context.Background(), "o", flatGym, "c1", "")
	require.Nil(t, apiErr)
	assert.Equal(t, db.GymMemberSourceFlat, e.store.active(flatGym, "c1").Source, "reconcile must leave it alone")
}

func TestListMembersRoleFilter(t *testing.T) {
	ctx := context.Background()
	e := newFlatEnv(true)
	rows, total, apiErr := e.svc.ListMembers(ctx, "o", flatGym, "head_coach", 1, 20)
	require.Nil(t, apiErr)
	assert.EqualValues(t, 2, total)
	assert.Len(t, rows, 2)
	for _, r := range rows {
		assert.Equal(t, "head_coach", r.Role)
	}
	rows, total, apiErr = e.svc.ListMembers(ctx, "o", flatGym, "coach", 1, 1)
	require.Nil(t, apiErr)
	assert.EqualValues(t, 2, total, "total follows the filter, not the page")
	assert.Len(t, rows, 1)
	// scoping unchanged: head coach h sees only supervised coaches under the filter
	rows, total, apiErr = e.svc.ListMembers(ctx, "h", flatGym, "coach", 1, 20)
	require.Nil(t, apiErr)
	assert.EqualValues(t, 1, total)
	assert.Equal(t, "c1", rows[0].UserID)
}
