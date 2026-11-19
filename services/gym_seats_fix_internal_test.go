package services

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
)

func TestGrandfatherAutoSeatOldestFirstAndIdempotent(t *testing.T) {
	ctx := context.Background()
	purged := 0
	svc, store, _ := newSeatEnv(true, true, *sum(gymStatusActive, 2),
		member("owner", db.GymMemberRoleOwner, false, 0),
		member("c3", db.GymMemberRoleCoach, false, 3),
		member("c1", db.GymMemberRoleCoach, false, 1),
		member("c2", db.GymMemberRoleHeadCoach, false, 2))
	svc.set.Purge = func() { purged++ }

	ds, _, apiErr := svc.reconcile(ctx, "g1")
	require.Nil(t, apiErr)
	assert.Equal(t, map[string]db.SeatState{"c1": db.SeatStateSeated, "c2": db.SeatStateSeated, "c3": db.SeatStateGrace}, states(ds))
	assert.Equal(t, 3, store.setSeat, "every existing coach gets a seat row, oldest first")
	owner, _ := store.Get(ctx, "owner", "g1")
	assert.False(t, owner.SeatAssigned, "the owner consumes no seat")
	c3, _ := store.Get(ctx, "c3", "g1")
	require.NotNil(t, c3.SeatGraceUntil)
	assert.True(t, c3.SeatGraceUntil.Equal(seatT0.Add(week)))
	assert.Equal(t, 1, purged, "writes bump authz and purge the context cache")

	// idempotent: a second reconcile writes nothing
	_, _, _ = svc.reconcile(ctx, "g1")
	assert.Equal(t, 3, store.setSeat)
	assert.Equal(t, 1, purged)

	// grandfather off: nobody is auto-seated
	svc, store, _ = newSeatEnv(true, false, *sum(gymStatusActive, 2), member("c1", db.GymMemberRoleCoach, false, 1))
	ds, _, _ = svc.reconcile(ctx, "g1")
	assert.Equal(t, db.SeatStateNone, states(ds)["c1"])
	assert.Zero(t, store.setSeat)
}

func TestReconcileWriteCallsChanged(t *testing.T) {
	ctx := context.Background()
	purged := 0
	svc, _, _ := newSeatEnv(true, true, *sum(gymStatusActive, 1),
		member("c1", db.GymMemberRoleCoach, true, 1), member("c2", db.GymMemberRoleCoach, true, 2))
	svc.set.Purge = func() { purged++ }
	_, _, _ = svc.reconcile(ctx, "g1") // c2 moves to grace
	assert.Equal(t, 1, purged)
	_, _, _ = svc.reconcile(ctx, "g1")
	assert.Equal(t, 1, purged, "no write, no purge")
}

func TestGymSourceUnseatedKeepsPersonalWhileGrandfather(t *testing.T) {
	ctx := context.Background()
	gym := EntContext{Kind: EntContextGym, GymID: "g1"}
	released := member("c1", db.GymMemberRoleCoach, false, 1)
	released.SeatState = db.SeatStateNone
	personal := func(has bool) GymPersonalFunc {
		return func(context.Context, string) ([]string, bool, *models.ApiError) {
			return []string{"workouts"}, has, nil
		}
	}

	svc, _, _ := newSeatEnv(true, true, *sum(gymStatusActive, 1), released)
	r, apiErr := NewGymSource(svc, personal(true), nil).Entitlements(ctx, "c1", gym)
	require.Nil(t, apiErr)
	assert.Equal(t, SourceGrandfather, r.Source)
	assert.Equal(t, []string{"workouts"}, r.Features)
	assert.Equal(t, "none", r.SeatState)

	r, _ = NewGymSource(svc, personal(false), nil).Entitlements(ctx, "c1", gym)
	assert.Equal(t, SourceNoGymSeat, r.Source, "no personal plan: nothing to keep")
	assert.Empty(t, r.Features)

	svc, _, _ = newSeatEnv(true, false, *sum(gymStatusActive, 1), released)
	r, _ = NewGymSource(svc, personal(true), nil).Entitlements(ctx, "c1", gym)
	assert.Empty(t, r.Features, "grandfather off")
}

func TestGymSourceFeatureLayers(t *testing.T) {
	ctx := context.Background()
	gym := EntContext{Kind: EntContextGym, GymID: "g1"}
	off, on := false, true
	def := func(id string, chat *bool) db.PlanDefault {
		return db.PlanDefault{PlanID: id, Features: map[string]db.FeatureGrant{FeatureChat: {Enabled: chat}, FeatureWorkouts: {Enabled: &on}}}
	}
	defs := func(m map[string]db.PlanDefault, err *models.ApiError) GymDefaultsFunc {
		return func(context.Context) (map[string]db.PlanDefault, *models.ApiError) { return m, err }
	}
	run := func(d GymDefaultsFunc) []string {
		svc, _, _ := newSeatEnv(true, true, *sum(gymStatusActive, 2),
			member("owner", db.GymMemberRoleOwner, false, 0), member("c1", db.GymMemberRoleCoach, true, 1))
		src := NewGymSource(svc, nil, d)
		r, apiErr := src.Entitlements(ctx, "c1", gym)
		require.Nil(t, apiErr)
		o, _ := src.Entitlements(ctx, "owner", gym)
		assert.Equal(t, r.Features, o.Features, "owner and seat coach resolve alike")
		return r.Features
	}

	// layer 2a: the plan default of the gym plan id (summary plan id is p1)
	f := run(defs(map[string]db.PlanDefault{"p1": def("p1", &off)}, nil))
	assert.Contains(t, f, FeatureWorkouts)
	assert.NotContains(t, f, FeatureChat)
	// layer 2b: the admin-editable _gym_seat default when the plan has none
	f = run(defs(map[string]db.PlanDefault{db.PlanDefaultGymSeat: def(db.PlanDefaultGymSeat, &off)}, nil))
	assert.NotContains(t, f, FeatureChat)
	// the plan default wins over _gym_seat
	f = run(defs(map[string]db.PlanDefault{"p1": def("p1", &off), db.PlanDefaultGymSeat: def(db.PlanDefaultGymSeat, &on)}, nil))
	assert.NotContains(t, f, FeatureChat)
	// last fallback: the payments summary features
	assert.Equal(t, []string{"workouts", "chat"}, run(defs(map[string]db.PlanDefault{}, nil)))
	assert.Equal(t, []string{"workouts", "chat"}, run(nil))
	assert.Equal(t, []string{"workouts", "chat"}, run(defs(nil, models.NewApiError(http.StatusServiceUnavailable, errors.New("down")))))
}

func TestGymPlanLimitFailsOpenWithGrandfather(t *testing.T) {
	ctx := context.Background()
	mk := func(grandfather bool) *gymPlanLimit {
		return &gymPlanLimit{summaries: NewGymSummaryCache(&fakeGymSummaries{err: errors.New("down")}, time.Minute), grandfather: grandfather}
	}
	v, err := mk(true).MaxGymClients(ctx, "g1")
	assert.NoError(t, err)
	assert.Nil(t, v)
	_, err = mk(false).MaxGymClients(ctx, "g1")
	assert.Error(t, err)
}

func TestOnJoinedSummaryFailureStillSeats(t *testing.T) {
	ctx := context.Background()
	svc, store, src := newSeatEnv(true, true, *sum(gymStatusActive, 1), member("c1", db.GymMemberRoleCoach, false, 1))
	src.err = errors.New("down")
	svc.OnJoined(ctx, "g1", "c1")
	m, _ := store.Get(ctx, "c1", "g1")
	assert.True(t, m.SeatAssigned, "seated although the summary is down")
	assert.Equal(t, 2, src.calls, "lookup retried once")

	// the retry succeeds: normal path, and an unlimited gym still seats nobody
	svc, store, src = newSeatEnv(true, true, *sum(gymStatusNone, 0), member("c1", db.GymMemberRoleCoach, false, 1))
	src.failN = 1
	svc.OnJoined(ctx, "g1", "c1")
	m, _ = store.Get(ctx, "c1", "g1")
	assert.False(t, m.SeatAssigned)
	assert.Equal(t, 2, src.calls)
}

type countingResolver struct {
	calls int
	err   *models.ApiError
}

func (c *countingResolver) Resolve(_ context.Context, userID string, ec EntContext) (*ContextEntitlements, *models.ApiError) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return &ContextEntitlements{Context: ec.Kind, Features: []string{userID}}, nil
}

func TestCachedContextResolver(t *testing.T) {
	ctx := context.Background()
	next := &countingResolver{}
	c := NewCachedContextResolver(next, time.Minute)
	g1 := EntContext{Kind: EntContextGym, GymID: "g1"}

	a, _ := c.Resolve(ctx, "u1", g1)
	b, _ := c.Resolve(ctx, "u1", g1)
	assert.Equal(t, 1, next.calls)
	assert.Equal(t, a, b)
	_, _ = c.Resolve(ctx, "u2", g1)
	_, _ = c.Resolve(ctx, "u1", EntContext{Kind: EntContextGym, GymID: "g2"})
	assert.Equal(t, 3, next.calls, "keyed per gym and user")

	c.Invalidate()
	_, _ = c.Resolve(ctx, "u1", g1)
	assert.Equal(t, 4, next.calls)

	// the personal context is not cached here
	_, _ = c.Resolve(ctx, "u1", EntContext{Kind: EntContextPersonal})
	_, _ = c.Resolve(ctx, "u1", EntContext{Kind: EntContextPersonal})
	assert.Equal(t, 6, next.calls)

	// errors are never cached
	next = &countingResolver{err: models.NewApiError(http.StatusServiceUnavailable, errors.New("down"))}
	c = NewCachedContextResolver(next, time.Minute)
	_, apiErr := c.Resolve(ctx, "u1", g1)
	require.NotNil(t, apiErr)
	_, _ = c.Resolve(ctx, "u1", g1)
	assert.Equal(t, 2, next.calls)

	// TTL expiry
	next = &countingResolver{}
	c = NewCachedContextResolver(next, 20*time.Millisecond)
	_, _ = c.Resolve(ctx, "u1", g1)
	time.Sleep(40 * time.Millisecond)
	_, _ = c.Resolve(ctx, "u1", g1)
	assert.Equal(t, 2, next.calls)
}

func TestSeatChangePurgesContextCache(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newSeatEnv(true, true, *sum(gymStatusActive, 3),
		member("owner", db.GymMemberRoleOwner, false, 0), member("c1", db.GymMemberRoleCoach, true, 1))
	next := &countingResolver{}
	c := NewCachedContextResolver(next, time.Minute)
	svc.set.Purge = c.Invalidate
	g1 := EntContext{Kind: EntContextGym, GymID: "g1"}
	_, _ = c.Resolve(ctx, "c1", g1)
	_, apiErr := svc.SetSeat(ctx, "owner", "g1", "c1", false)
	require.Nil(t, apiErr)
	_, _ = c.Resolve(ctx, "c1", g1)
	assert.Equal(t, 2, next.calls, "the seat change dropped the cached result")
}

type errJoinRedeemer struct{ err *models.ApiError }

func (e errJoinRedeemer) LookupJoin(context.Context, string) (*JoinLookup, *models.ApiError) {
	return nil, nil
}
func (e errJoinRedeemer) JoinOnSignup(context.Context, string, string) *models.ApiError { return e.err }

func TestJoinGymAfterSignupReportsFailure(t *testing.T) {
	ctx := context.Background()
	ok := (&AuthServiceImpl{}).WithGymJoins(errJoinRedeemer{})
	assert.True(t, ok.joinGymAfterSignup(ctx, "u1", "tok"))
	bad := (&AuthServiceImpl{}).WithGymJoins(errJoinRedeemer{err: models.NewApiError(http.StatusConflict, errors.New("seats"))})
	assert.False(t, bad.joinGymAfterSignup(ctx, "u1", "tok"), "the caller subscribes the default plan on false")

	// the default plan is skipped only for gym joins while seats are on
	s := (&AuthServiceImpl{}).WithSeatOnlySignup(true)
	assert.True(t, s.skipDefaultPlan(true, nil))
	assert.False(t, s.skipDefaultPlan(false, nil))
	assert.False(t, (&AuthServiceImpl{}).skipDefaultPlan(true, nil))
}
