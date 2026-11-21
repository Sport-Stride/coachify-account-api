package services_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/pkg/tracker"
	"coachify-account-api/services"
	"coachify-account-api/services/mocks"
)

// fakeDispatchStore is an in-memory services.GymDispatchStore over the rows of a fakeGymClientStore.
type fakeDispatchStore struct {
	clients *fakeGymClientStore
	audit   *fakeClientAudit
	failCAS bool
	txCalls int
	// failRelease makes the next N ReleaseCoachTx calls fail.
	failRelease  int
	releaseCalls int
	lastCoachIDs []string
}

func (f *fakeDispatchStore) AssignTx(_ context.Context, gymID, clientID, prev, coachID string, at time.Time, a *db.TeamAuditEntry) (bool, error) {
	f.txCalls++
	if f.failCAS {
		return false, nil
	}
	ok, _ := f.clients.SetCoach(context.Background(), gymID, clientID, prev, coachID, at)
	if ok && a != nil {
		_ = f.audit.InsertAudit(context.Background(), a)
	}
	return ok, nil
}

func (f *fakeDispatchStore) LeaveTx(_ context.Context, gymID, clientID string, at time.Time, a *db.TeamAuditEntry) (bool, error) {
	f.txCalls++
	ok, _ := f.clients.Leave(context.Background(), gymID, clientID, at)
	if ok && a != nil {
		_ = f.audit.InsertAudit(context.Background(), a)
	}
	return ok, nil
}

func (f *fakeDispatchStore) ReleaseCoachTx(_ context.Context, coachIDs []string, gymID string, at time.Time, audit func(db.GymClient) *db.TeamAuditEntry) ([]db.GymClient, error) {
	f.releaseCalls++
	f.lastCoachIDs = coachIDs
	if f.failRelease > 0 {
		f.failRelease--
		return nil, errors.New("tx aborted")
	}
	f.txCalls++
	var out []db.GymClient
	for _, r := range f.clients.rows {
		if contains(coachIDs, r.CoachID) && r.Status == db.GymClientStatusActive && (gymID == "" || r.GymID == gymID) {
			out = append(out, *r)
			_ = f.audit.InsertAudit(context.Background(), audit(*r))
			r.CoachID, r.AssignedAt = "", nil
		}
	}
	return out, nil
}

func contains(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

type transferCall struct{ gym, client, from, to, reason string }

type fakeDispatchTracker struct {
	mu        sync.Mutex
	transfers []transferCall
	cancels   []string
	fail      bool
	failFirst int  // fail the first N calls of Transfer and CancelGym, then succeed
	boom      bool // panic in Transfer
	cancelTry int
}

func (f *fakeDispatchTracker) Transfer(_ context.Context, gym, client, from, to, reason string) (*tracker.BulkResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.transfers = append(f.transfers, transferCall{gym, client, from, to, reason})
	if f.boom {
		panic("boom")
	}
	if f.failFirst > 0 {
		f.failFirst--
		return nil, errors.New("tracker down")
	}
	if f.fail {
		return nil, errors.New("tracker down")
	}
	return &tracker.BulkResult{}, nil
}

func (f *fakeDispatchTracker) CancelGym(_ context.Context, gym, client string, _ *time.Time) (*tracker.BulkResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelTry++
	if f.failFirst > 0 {
		f.failFirst--
		return nil, errors.New("tracker down")
	}
	f.cancels = append(f.cancels, gym+"/"+client)
	return &tracker.BulkResult{}, nil
}

type fakeDispatchChat struct{ calls [][2]string }

func (f *fakeDispatchChat) EnsureConversation(_ context.Context, adminID, peerID string) *models.ApiError {
	f.calls = append(f.calls, [2]string{adminID, peerID})
	return nil
}

type fakeDispatchBilling struct {
	calls []string
	err   error
}

func (f *fakeDispatchBilling) CancelAtPeriodEnd(_ context.Context, gymID, clientID string) error {
	f.calls = append(f.calls, gymID+"/"+clientID)
	return f.err
}

type dispatchFixture struct {
	svc     *services.GymDispatch
	store   *fakeGymClientStore
	tx      *fakeDispatchStore
	audit   *fakeClientAudit
	tracker *fakeDispatchTracker
	chat    *fakeDispatchChat
	billing *fakeDispatchBilling
	links   *mocks.CoachLinkStore // no expectations: any personal coach_clients access panics
	sleeps  *[]time.Duration
}

// newDispatchFixture mirrors newGymClientFixture: gymA has an owner, a head coach and coaches c1, c2; cl1 belongs to
// c1, cl2 is in the pool, cl3 belongs to another gym.
func newDispatchFixture(t *testing.T) *dispatchFixture { return newDispatchFixtureSeats(t, nil) }

func newDispatchFixtureSeats(t *testing.T, seats services.GymSeatStanding) *dispatchFixture {
	t.Helper()
	members := &fakeAuthzMembers{rows: []db.GymMember{
		member("owner", db.GymMemberRoleOwner),
		member("head", db.GymMemberRoleHeadCoach),
		member("c1", db.GymMemberRoleCoach),
		member("c2", db.GymMemberRoleCoach),
	}}
	store := &fakeGymClientStore{rows: []*db.GymClient{
		{GymID: gymA, ClientID: "cl1", CoachID: "c1", Status: db.GymClientStatusActive, JoinedAt: time.Now()},
		{GymID: gymA, ClientID: "cl2", CoachID: "", Status: db.GymClientStatusActive, JoinedAt: time.Now()},
		{GymID: "gymB", ClientID: "cl3", CoachID: "x", Status: db.GymClientStatusActive, JoinedAt: time.Now()},
	}}
	authz := services.NewChainAuthorizer(members, newFakePolicyStore(), services.NewResourceResolver(services.NewClientScopeSource(store)))
	audit := &fakeClientAudit{}
	links := &mocks.CoachLinkStore{}
	tx := &fakeDispatchStore{clients: store, audit: audit}
	tr, ch, bill := &fakeDispatchTracker{}, &fakeDispatchChat{}, &fakeDispatchBilling{}
	var sleeps []time.Duration
	svc := services.NewGymDispatch(services.GymClientDeps{
		Authz: authz, Members: members, Store: store, Team: fakeSupervision{"head": {"c1"}}, Links: links,
		Audit: audit, Quota: &mocks.QuotaGuard{}, Seats: seats,
	}, services.GymDispatchDeps{Repo: tx, Tracker: tr, Chat: ch, Billing: bill, Spawn: func(f func()) { f() },
		Sleep: func(_ context.Context, d time.Duration) { sleeps = append(sleeps, d) }})
	return &dispatchFixture{svc: svc, store: store, tx: tx, audit: audit, tracker: tr, chat: ch, billing: bill, links: links, sleeps: &sleeps}
}

func (f *dispatchFixture) coachOf(client string) string {
	r, _ := f.store.Get(context.Background(), gymA, client)
	if r == nil {
		return "<none>"
	}
	return r.CoachID
}

func TestDispatchAssignDenied(t *testing.T) {
	f := newDispatchFixture(t)
	ctx := context.Background()
	_, apiErr := f.svc.Assign(ctx, "c1", gymA, "cl2", "c2")
	requireCode(t, apiErr, http.StatusForbidden)
	_, apiErr = f.svc.Unassign(ctx, "c2", gymA, "cl1")
	requireCode(t, apiErr, http.StatusForbidden)
	_, apiErr = f.svc.Assign(ctx, "stranger", gymA, "cl2", "c2")
	requireCode(t, apiErr, http.StatusNotFound)
	assert.Zero(t, f.tx.txCalls)
	assert.Empty(t, f.tracker.transfers)
}

func TestDispatchAssignTargetAndClientChecks(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		client, coach string
		code          int
	}{
		"target not a member":         {"cl2", "nobody", http.StatusBadRequest},
		"target member of other gym":  {"cl2", "x", http.StatusBadRequest},
		"target not a coaching role":  {"cl2", "stranger", http.StatusBadRequest},
		"client of another gym":       {"cl3", "c1", http.StatusNotFound},
		"personal-only client refuse": {"personal-only", "c1", http.StatusNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			f := newDispatchFixture(t)
			_, apiErr := f.svc.Assign(ctx, "owner", gymA, tc.client, tc.coach)
			requireCode(t, apiErr, tc.code)
			assert.Zero(t, f.tx.txCalls)
			assert.Empty(t, f.tracker.transfers)
			assert.Empty(t, f.audit.entries)
		})
	}
}

func TestDispatchAssignIdempotentSameCoach(t *testing.T) {
	f := newDispatchFixture(t)
	res, apiErr := f.svc.Assign(context.Background(), "owner", gymA, "cl1", "c1")
	require.Nil(t, apiErr)
	assert.Equal(t, "c1", res.CoachID)
	assert.Zero(t, f.tx.txCalls)
	assert.Empty(t, f.tracker.transfers)
	assert.Empty(t, f.chat.calls)
	assert.Empty(t, f.audit.entries)
}

func TestDispatchAssignCASConflict(t *testing.T) {
	f := newDispatchFixture(t)
	f.tx.failCAS = true
	_, apiErr := f.svc.Assign(context.Background(), "owner", gymA, "cl1", "c2")
	requireCode(t, apiErr, http.StatusConflict)
	assert.Equal(t, "c1", f.coachOf("cl1"))
	assert.Empty(t, f.audit.entries)
	assert.Empty(t, f.tracker.transfers)
	assert.Empty(t, f.chat.calls)
}

func TestDispatchReassignCommitsThenTransfers(t *testing.T) {
	f := newDispatchFixture(t)
	f.tracker.fail = true // best effort: a tracker failure never rolls the commit back (3 attempts, then logged)
	res, apiErr := f.svc.Assign(context.Background(), "head", gymA, "cl1", "c2")
	require.Nil(t, apiErr)
	assert.Equal(t, "c2", res.CoachID)
	assert.Equal(t, "c1", res.PreviousCoachID)
	assert.Equal(t, "c2", f.coachOf("cl1"))
	require.Len(t, f.audit.entries, 1)
	assert.Equal(t, db.TeamActionClientReassign, f.audit.entries[0].Action)
	assert.Equal(t, db.TeamActorRoleHead, f.audit.entries[0].ActorRole)
	want := transferCall{gymA, "cl1", "c1", "c2", "gym_reassign"}
	assert.Equal(t, []transferCall{want, want, want}, f.tracker.transfers, "three attempts with the same arguments")
	assert.Equal(t, [][2]string{{"c2", "cl1"}}, f.chat.calls, "conversation opens for the new coach only")
}

func TestDispatchAssignFromPoolAndUnassign(t *testing.T) {
	f := newDispatchFixture(t)
	ctx := context.Background()
	_, apiErr := f.svc.Assign(ctx, "owner", gymA, "cl2", "c1")
	require.Nil(t, apiErr)
	assert.Equal(t, db.TeamActionClientAssign, f.audit.entries[0].Action)
	assert.Equal(t, transferCall{gymA, "cl2", "", "c1", "gym_assign"}, f.tracker.transfers[0])

	res, apiErr := f.svc.Unassign(ctx, "owner", gymA, "cl1")
	require.Nil(t, apiErr)
	assert.Equal(t, "c1", res.PreviousCoachID)
	assert.Equal(t, "", f.coachOf("cl1"))
	assert.Equal(t, transferCall{gymA, "cl1", "c1", "", "gym_unassign"}, f.tracker.transfers[1])

	// Unassigning a pool client is a no-op.
	n := len(f.tracker.transfers)
	_, apiErr = f.svc.Unassign(ctx, "owner", gymA, "cl1")
	require.Nil(t, apiErr)
	assert.Len(t, f.tracker.transfers, n)
}

func TestDispatchClientLeave(t *testing.T) {
	f := newDispatchFixture(t)
	ctx := context.Background()
	require.Nil(t, f.svc.LeaveGym(ctx, "cl1", gymA))
	assert.Equal(t, "<none>", f.coachOf("cl1"), "relationship is no longer active")
	assert.Equal(t, []string{gymA + "/cl1"}, f.tracker.cancels)
	assert.Equal(t, []string{gymA + "/cl1"}, f.billing.calls)
	require.Len(t, f.audit.entries, 1)
	assert.Equal(t, db.TeamActionClientLeaveGym, f.audit.entries[0].Action)
	assert.Equal(t, db.TeamActorRoleClient, f.audit.entries[0].ActorRole)
	f.links.AssertNotCalled(t, "LinkedClientIDs")

	// Idempotent: the second leave is a success and does nothing.
	require.Nil(t, f.svc.LeaveGym(ctx, "cl1", gymA))
	assert.Len(t, f.tracker.cancels, 1)
	assert.Len(t, f.billing.calls, 1)
	assert.Len(t, f.audit.entries, 1)

	// A user with no relationship to the gym is also a no-op success.
	require.Nil(t, f.svc.LeaveGym(ctx, "personal-only", gymA))
	requireCode(t, f.svc.LeaveGym(ctx, "", gymA), http.StatusBadRequest)
}

func TestDispatchOwnerRemoveUsesSameEnd(t *testing.T) {
	f := newDispatchFixture(t)
	ctx := context.Background()
	requireCode(t, f.svc.Remove(ctx, "c1", gymA, "cl1"), http.StatusForbidden)
	require.Nil(t, f.svc.Remove(ctx, "owner", gymA, "cl1"))
	assert.Equal(t, []string{gymA + "/cl1"}, f.tracker.cancels)
	assert.Equal(t, []string{gymA + "/cl1"}, f.billing.calls)
	assert.Equal(t, db.TeamActorRoleGymOwner, f.audit.entries[0].ActorRole)
}

func TestDispatchCoachLeaveMovesClientsToPool(t *testing.T) {
	f := newDispatchFixture(t)
	f.store.rows = append(f.store.rows, &db.GymClient{GymID: gymA, ClientID: "cl4", CoachID: "c1", Status: db.GymClientStatusActive})
	f.svc.ReleaseCoachClients(context.Background(), services.CoachRelease{GymID: gymA, CoachIDs: []string{"c1"}, ActorID: "head", ActorRole: db.TeamActorRoleHead, HeadCoachID: "head"})
	assert.Equal(t, "", f.coachOf("cl1"))
	assert.Equal(t, "", f.coachOf("cl4"))
	assert.Equal(t, "", f.coachOf("cl2"))
	assert.Equal(t, 1, f.tx.txCalls, "one transaction, not one per client")
	require.Len(t, f.tracker.transfers, 2)
	for _, c := range f.tracker.transfers {
		assert.Equal(t, "c1", c.from)
		assert.Equal(t, "", c.to)
		assert.Equal(t, gymA, c.gym)
	}
	f.links.AssertNotCalled(t, "LinkedClientIDs")
	require.Len(t, f.audit.entries, 2)
	assert.Equal(t, db.TeamActionClientUnassign, f.audit.entries[0].Action)
	assert.Equal(t, "head", f.audit.entries[0].ActorID, "real actor, not the departing coach")
	assert.Equal(t, db.TeamActorRoleHead, f.audit.entries[0].ActorRole)
	assert.Equal(t, "head", f.audit.entries[0].HeadCoachID)
	assert.Equal(t, "c1", f.audit.entries[0].PrevCoachID)

	// Another coach's clients and an empty coach id are untouched.
	f.svc.ReleaseCoachClients(context.Background(), services.CoachRelease{})
	assert.Equal(t, 1, f.tx.txCalls)
}

type fakeDispatchProbe struct {
	ok  bool
	err error
}

func (f fakeDispatchProbe) SupportsTransactions(context.Context) (bool, error) { return f.ok, f.err }

func TestCheckDispatchReady(t *testing.T) {
	ctx := context.Background()
	assert.NoError(t, services.CheckDispatchReady(ctx, fakeDispatchProbe{ok: true}, true))
	err := services.CheckDispatchReady(ctx, fakeDispatchProbe{ok: false}, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "replica set")
	err = services.CheckDispatchReady(ctx, fakeDispatchProbe{ok: true}, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CLIENT_GYM_RELATIONS")
	err = services.CheckDispatchReady(ctx, fakeDispatchProbe{err: errors.New("boom")}, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot detect")
}

func TestMyGyms(t *testing.T) {
	ctx := context.Background()
	t.Run("flag off is a generic 404", func(t *testing.T) {
		f := newGymClientFixture(t, false)
		_, apiErr := f.svc.MyGyms(ctx, "cl1")
		requireCode(t, apiErr, http.StatusNotFound)
	})
	t.Run("none is an empty list", func(t *testing.T) {
		f := newGymClientFixture(t, true)
		rows, apiErr := f.svc.MyGyms(ctx, "nobody")
		require.Nil(t, apiErr)
		assert.NotNil(t, rows)
		assert.Empty(t, rows)
	})
	t.Run("one, only the callers own rows", func(t *testing.T) {
		f := newGymClientFixture(t, true)
		rows, apiErr := f.svc.MyGyms(ctx, "cl1")
		require.Nil(t, apiErr)
		require.Len(t, rows, 1)
		assert.Equal(t, gymA, rows[0].GymID)
		assert.Equal(t, "c1", rows[0].CoachID)
	})
	t.Run("several sorted by gym id, left and archived excluded", func(t *testing.T) {
		f := newGymClientFixture(t, true)
		store := f.store
		now := time.Now()
		store.rows = append(store.rows,
			&db.GymClient{GymID: "gymC", ClientID: "cl1", Status: db.GymClientStatusActive, JoinedAt: now},
			&db.GymClient{GymID: "gym0", ClientID: "cl1", Status: db.GymClientStatusLeft, JoinedAt: now},
			&db.GymClient{GymID: "gymZ", ClientID: "cl1", Status: db.GymClientStatusActive, JoinedAt: now})
		store.archived = map[string]bool{"gymZ": true}
		rows, apiErr := f.svc.MyGyms(ctx, "cl1")
		require.Nil(t, apiErr)
		require.Len(t, rows, 2)
		assert.Equal(t, []string{gymA, "gymC"}, []string{rows[0].GymID, rows[1].GymID})
	})
}

func TestDispatchTrackerRetriesThenSucceeds(t *testing.T) {
	f := newDispatchFixture(t)
	f.tracker.failFirst = 2
	_, apiErr := f.svc.Assign(context.Background(), "owner", gymA, "cl2", "c1")
	require.Nil(t, apiErr)
	assert.Len(t, f.tracker.transfers, 3, "two failures then a success")
	assert.Equal(t, []time.Duration{500 * time.Millisecond, time.Second}, *f.sleeps, "backoff between attempts")
	assert.Equal(t, "c1", f.coachOf("cl2"))
}

func TestDispatchCancelGymRetries(t *testing.T) {
	f := newDispatchFixture(t)
	f.tracker.failFirst = 1
	require.Nil(t, f.svc.LeaveGym(context.Background(), "cl1", gymA))
	assert.Equal(t, 2, f.tracker.cancelTry)
	assert.Equal(t, []string{gymA + "/cl1"}, f.tracker.cancels)
}

func TestDispatchReleaseRetriesThenSucceeds(t *testing.T) {
	f := newDispatchFixture(t)
	f.tx.failRelease = 2
	f.svc.ReleaseCoachClients(context.Background(), services.CoachRelease{GymID: gymA, CoachIDs: []string{"c1"}, ActorID: "c1", ActorRole: db.TeamActorRoleCoach})
	assert.Equal(t, 3, f.tx.releaseCalls)
	assert.Equal(t, "", f.coachOf("cl1"))
	assert.Len(t, f.tracker.transfers, 1)
}

func TestDispatchReleaseGivesUpQuietly(t *testing.T) {
	f := newDispatchFixture(t)
	f.tx.failRelease = 10
	f.svc.ReleaseCoachClients(context.Background(), services.CoachRelease{GymID: gymA, CoachIDs: []string{"c1"}, ActorID: "c1", ActorRole: db.TeamActorRoleCoach})
	assert.Equal(t, 3, f.tx.releaseCalls, "three attempts, no panic, caller is not failed")
	assert.Equal(t, "c1", f.coachOf("cl1"), "nothing released")
	assert.Empty(t, f.tracker.transfers)

	// A repeat finishes the release (idempotent).
	f.tx.failRelease = 0
	f.svc.ReleaseCoachClients(context.Background(), services.CoachRelease{GymID: gymA, CoachIDs: []string{"c1"}, ActorID: "c1", ActorRole: db.TeamActorRoleCoach})
	assert.Equal(t, "", f.coachOf("cl1"))
}

func TestDispatchPanicsAreRecovered(t *testing.T) {
	f := newDispatchFixture(t)
	f.tracker.boom = true
	f.store.rows = append(f.store.rows, &db.GymClient{GymID: gymA, ClientID: "cl4", CoachID: "c1", Status: db.GymClientStatusActive})
	assert.NotPanics(t, func() {
		f.svc.ReleaseCoachClients(context.Background(), services.CoachRelease{GymID: gymA, CoachIDs: []string{"c1"}, ActorID: "c1", ActorRole: db.TeamActorRoleCoach})
	}, "worker panics are recovered and the queue still drains")
	assert.Equal(t, "", f.coachOf("cl1"))
	assert.Equal(t, "", f.coachOf("cl4"))

	// The background goroutine of assign is recovered too; the commit stays.
	assert.NotPanics(t, func() {
		_, apiErr := f.svc.Assign(context.Background(), "owner", gymA, "cl2", "c2")
		assert.Nil(t, apiErr)
	})
	assert.Equal(t, "c2", f.coachOf("cl2"))
}

func TestDispatchReleaseWithSeveralCoachesAndActor(t *testing.T) {
	f := newDispatchFixture(t)
	f.store.rows = append(f.store.rows, &db.GymClient{GymID: gymA, ClientID: "cl5", CoachID: "c2", Status: db.GymClientStatusActive})
	f.svc.ReleaseCoachClients(context.Background(), services.CoachRelease{
		GymID: gymA, CoachIDs: []string{"head", "c1", "c2"}, ActorID: "owner", ActorRole: db.TeamActorRoleGymOwner, HeadCoachID: "head",
	})
	assert.Equal(t, 1, f.tx.txCalls)
	assert.Equal(t, []string{"head", "c1", "c2"}, f.tx.lastCoachIDs)
	assert.Equal(t, "", f.coachOf("cl5"))
	for _, e := range f.audit.entries {
		assert.Equal(t, "owner", e.ActorID)
		assert.Equal(t, "head", e.HeadCoachID)
	}
}

func (f *fakeDispatchChat) EnsureGymConversation(ctx context.Context, adminID, peerID, _ string) *models.ApiError {
	return f.EnsureConversation(ctx, adminID, peerID)
}

func (f *fakeDispatchBilling) SubscribeDefault(context.Context, string, string, string) error { return nil }
