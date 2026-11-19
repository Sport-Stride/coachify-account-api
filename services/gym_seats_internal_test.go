package services

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
	"coachify-account-api/pkg/payments"
)

var seatT0 = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

const week = 7 * 24 * time.Hour

func member(user string, role db.GymMemberRole, assigned bool, joinedDays int) db.GymMember {
	m := db.GymMember{GymID: "g1", UserID: user, Role: role, Status: db.GymMemberStatusActive, SeatAssigned: assigned,
		JoinedAt: seatT0.Add(time.Duration(joinedDays) * 24 * time.Hour)}
	if assigned {
		at := m.JoinedAt
		m.SeatAssignedAt = &at
	}
	return m
}

func sum(status string, total int64) *payments.GymSummary {
	return &payments.GymSummary{GymID: "g1", Status: status, PlanID: "p1", TotalSeats: total, IncludedSeats: total, Features: []string{"workouts", "chat"}}
}

func states(ds []seatDecision) map[string]db.SeatState {
	out := map[string]db.SeatState{}
	for _, d := range ds {
		out[d.Member.UserID] = d.State
	}
	return out
}

func TestResolveSeatsCountingAndOwnerConsumesNone(t *testing.T) {
	ms := []db.GymMember{
		member("owner", db.GymMemberRoleOwner, true, 0), // even a stray seat flag on the owner is ignored
		member("c1", db.GymMemberRoleCoach, true, 1),
		member("h1", db.GymMemberRoleHeadCoach, true, 2),
		member("c2", db.GymMemberRoleCoach, false, 3),
	}
	ds := resolveSeats(ms, sum(gymStatusActive, 2), false, week, seatT0)
	assert.Equal(t, map[string]db.SeatState{"c1": db.SeatStateSeated, "h1": db.SeatStateSeated, "c2": db.SeatStateNone}, states(ds))
	assert.Equal(t, int64(2), countHolders(ds), "owner is not a seat holder")
}

func TestResolveSeatsOverflowGraceThenReadOnly(t *testing.T) {
	ms := []db.GymMember{
		member("c1", db.GymMemberRoleCoach, true, 1),
		member("c2", db.GymMemberRoleCoach, true, 2),
		member("c3", db.GymMemberRoleCoach, true, 3),
	}
	s := sum(gymStatusActive, 2) // downgraded from 3 to 2
	ds := resolveSeats(ms, s, false, week, seatT0)
	assert.Equal(t, map[string]db.SeatState{"c1": db.SeatStateSeated, "c2": db.SeatStateSeated, "c3": db.SeatStateGrace}, states(ds), "newest overflows")
	require.NotNil(t, ds[2].GraceUntil)
	assert.True(t, ds[2].GraceUntil.Equal(seatT0.Add(week)))

	// the stored grace_until is kept, so a re-run is stable (idempotent) and expires on time
	ms[2].SeatState, ms[2].SeatGraceUntil = db.SeatStateGrace, ds[2].GraceUntil
	again := resolveSeats(ms, s, false, week, seatT0.Add(24*time.Hour))
	assert.Equal(t, db.SeatStateGrace, again[2].State)
	assert.False(t, again[2].changed())
	after := resolveSeats(ms, s, false, week, seatT0.Add(week))
	assert.Equal(t, db.SeatStateReadOnly, after[2].State)

	// buying more seats resolves the overflow
	fixed := resolveSeats(ms, sum(gymStatusActive, 3), false, week, seatT0.Add(week))
	assert.Equal(t, db.SeatStateSeated, fixed[2].State)
	assert.Nil(t, fixed[2].GraceUntil)
}

func TestResolveSeatsLapse(t *testing.T) {
	ms := []db.GymMember{member("c1", db.GymMemberRoleCoach, true, 1), member("c2", db.GymMemberRoleCoach, false, 2)}
	// payments in grace: still seated
	ds := resolveSeats(ms, sum(gymStatusGrace, 1), false, week, seatT0)
	assert.Equal(t, db.SeatStateSeated, states(ds)["c1"])
	// lapsed: coach capabilities suspended
	ds = resolveSeats(ms, sum(gymStatusLapsed, 1), false, week, seatT0)
	assert.Equal(t, map[string]db.SeatState{"c1": db.SeatStateReadOnly, "c2": db.SeatStateNone}, states(ds))
}

func TestResolveSeatsGrandfather(t *testing.T) {
	ms := []db.GymMember{member("c1", db.GymMemberRoleCoach, false, 1), member("c2", db.GymMemberRoleCoach, false, 2)}
	none := sum(gymStatusNone, 0)
	ds := resolveSeats(ms, none, true, week, seatT0)
	assert.Equal(t, map[string]db.SeatState{"c1": db.SeatStateSeated, "c2": db.SeatStateSeated}, states(ds))
	assert.True(t, isUnlimited(none, true))
	// once the owner has a subscription, enforcement applies: members that already had a seat state and hold no seat
	// stay without one
	released := []db.GymMember{ms[0], ms[1]}
	released[0].SeatState, released[1].SeatState = db.SeatStateNone, db.SeatStateNone
	ds = resolveSeats(released, sum(gymStatusActive, 1), true, week, seatT0)
	assert.False(t, isUnlimited(sum(gymStatusActive, 1), true))
	assert.Equal(t, map[string]db.SeatState{"c1": db.SeatStateNone, "c2": db.SeatStateNone}, states(ds))
	// grandfather off and no subscription: no seats
	ds = resolveSeats(ms, none, false, week, seatT0)
	assert.Equal(t, db.SeatStateNone, states(ds)["c1"])
}

// ---- fakes ----

type fakeSeatStore struct {
	mu      sync.Mutex
	members []db.GymMember
	setSeat int
}

func (f *fakeSeatStore) ListByGym(context.Context, string) ([]db.GymMember, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]db.GymMember(nil), f.members...), nil
}

func (f *fakeSeatStore) ListByUser(_ context.Context, u string) ([]db.GymMember, error) {
	var out []db.GymMember
	for _, m := range f.members {
		if m.UserID == u {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeSeatStore) Get(_ context.Context, u, g string) (*db.GymMember, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.members {
		if f.members[i].UserID == u && f.members[i].GymID == g {
			m := f.members[i]
			return &m, nil
		}
	}
	return nil, nil
}

func (f *fakeSeatStore) SetSeat(_ context.Context, _, u string, assigned bool, at time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setSeat++
	for i := range f.members {
		m := &f.members[i]
		if m.UserID == u && isSeatRole(m.Role) {
			m.SeatAssigned, m.SeatGraceUntil = assigned, nil
			if assigned {
				m.SeatState, m.SeatAssignedAt = db.SeatStateSeated, &at
			} else {
				m.SeatState, m.SeatAssignedAt = db.SeatStateNone, nil
			}
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeSeatStore) SetSeatState(_ context.Context, _, u string, st db.SeatState, until *time.Time, _ time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.members {
		if f.members[i].UserID == u {
			f.members[i].SeatState, f.members[i].SeatGraceUntil = st, until
			return true, nil
		}
	}
	return false, nil
}

type fakeGymSummaries struct {
	mu    sync.Mutex
	calls int
	sum   payments.GymSummary
	err   error
	// failN makes the first failN calls fail.
	failN int
}

func (f *fakeGymSummaries) GymSummaries(_ context.Context, ids []string) ([]payments.GymSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.failN > 0 {
		f.failN--
		return nil, errors.New("payments down")
	}
	if f.err != nil {
		return nil, f.err
	}
	s := f.sum
	s.GymID = ids[0]
	return []payments.GymSummary{s}, nil
}

type ownerAuthz struct{ owner string }

func (a ownerAuthz) Can(_ context.Context, act Actor, _ Capability, _ Resource) (Decision, *models.ApiError) {
	switch act.ID {
	case a.owner:
		return Decision{Allowed: true, Reason: ReasonOwner}, nil
	case "stranger":
		return Decision{Reason: ReasonNotMember}, nil
	}
	return Decision{Reason: ReasonNoRule}, nil
}

func newSeatEnv(enabled, grandfather bool, s payments.GymSummary, members ...db.GymMember) (*seatService, *fakeSeatStore, *fakeGymSummaries) {
	store := &fakeSeatStore{members: members}
	src := &fakeGymSummaries{sum: s}
	svc := NewSeatService(ownerAuthz{owner: "owner"}, store, NewGymSummaryCache(src, time.Minute),
		SeatSettings{Enabled: enabled, Grandfather: grandfather, OverflowGrace: week}).(*seatService)
	svc.now = func() time.Time { return seatT0 }
	return svc, store, src
}

func entErrCode(t *testing.T, e *models.ApiError) string {
	t.Helper()
	var ee *models.EntitlementError
	require.True(t, errors.As(e.Error, &ee))
	return ee.Code
}

func TestSeatsExhaustedOnCheckAndAssign(t *testing.T) {
	ctx := context.Background()
	svc, store, _ := newSeatEnv(true, true, *sum(gymStatusActive, 1),
		member("owner", db.GymMemberRoleOwner, false, 0),
		member("c1", db.GymMemberRoleCoach, true, 1),
		member("c2", db.GymMemberRoleCoach, false, 2))

	apiErr := svc.Check(ctx, "g1")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusConflict, apiErr.Code)
	assert.Equal(t, "SEATS_EXHAUSTED", entErrCode(t, apiErr))

	_, apiErr = svc.SetSeat(ctx, "owner", "g1", "c2", true)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusConflict, apiErr.Code)
	assert.Equal(t, "SEATS_EXHAUSTED", entErrCode(t, apiErr))
	assert.Zero(t, store.setSeat, "nothing written when exhausted")

	// release frees the seat, assign then works
	v, apiErr := svc.SetSeat(ctx, "owner", "g1", "c1", false)
	require.Nil(t, apiErr)
	assert.False(t, v.SeatAssigned)
	v, apiErr = svc.SetSeat(ctx, "owner", "g1", "c2", true)
	require.Nil(t, apiErr)
	assert.Equal(t, "seated", v.SeatState)
	// assigning twice is idempotent
	_, apiErr = svc.SetSeat(ctx, "owner", "g1", "c2", true)
	require.Nil(t, apiErr)
}

func TestSeatEndpointsAuthorization(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newSeatEnv(true, true, *sum(gymStatusActive, 2),
		member("owner", db.GymMemberRoleOwner, false, 0), member("c1", db.GymMemberRoleCoach, true, 1))
	_, apiErr := svc.Overview(ctx, "stranger", "g1")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusNotFound, apiErr.Code, "foreign gym is 404")
	_, apiErr = svc.Overview(ctx, "c1", "g1")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusForbidden, apiErr.Code, "a coach is not the owner")
	_, apiErr = svc.SetSeat(ctx, "c1", "g1", "c1", false)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusForbidden, apiErr.Code)
	_, apiErr = svc.SetSeat(ctx, "owner", "g1", "owner", true)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.Code, "owner consumes no seat")

	ov, apiErr := svc.Overview(ctx, "owner", "g1")
	require.Nil(t, apiErr)
	assert.Equal(t, int64(2), ov.Purchased)
	assert.Equal(t, int64(1), ov.Used)
	assert.Equal(t, int64(1), ov.Free)
	assert.Len(t, ov.Members, 1, "owner is not listed as a seat holder")
}

func TestOverflowReconcilePersistsAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	svc, store, _ := newSeatEnv(true, true, *sum(gymStatusActive, 1),
		member("owner", db.GymMemberRoleOwner, false, 0),
		member("c1", db.GymMemberRoleCoach, true, 1),
		member("c2", db.GymMemberRoleCoach, true, 2))
	st, apiErr := svc.Standing(ctx, "g1", "c2")
	require.Nil(t, apiErr)
	assert.Equal(t, db.SeatStateGrace, st.State)
	stored, _ := store.Get(ctx, "c2", "g1")
	require.NotNil(t, stored.SeatGraceUntil)
	first := *stored.SeatGraceUntil

	// a later read keeps the original deadline
	svc.now = func() time.Time { return seatT0.Add(3 * 24 * time.Hour) }
	st, _ = svc.Standing(ctx, "g1", "c2")
	assert.Equal(t, db.SeatStateGrace, st.State)
	stored, _ = store.Get(ctx, "c2", "g1")
	assert.True(t, stored.SeatGraceUntil.Equal(first))

	svc.now = func() time.Time { return seatT0.Add(week) }
	st, _ = svc.Standing(ctx, "g1", "c2")
	assert.Equal(t, db.SeatStateReadOnly, st.State)
	st, _ = svc.Standing(ctx, "g1", "c1")
	assert.Equal(t, db.SeatStateSeated, st.State)
}

func TestFlagOffIsNoOp(t *testing.T) {
	ctx := context.Background()
	svc, store, src := newSeatEnv(false, true, *sum(gymStatusActive, 0), member("c1", db.GymMemberRoleCoach, false, 1))
	assert.Nil(t, svc.Check(ctx, "g1"))
	svc.OnJoined(ctx, "g1", "c1")
	_, apiErr := svc.Overview(ctx, "owner", "g1")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusNotFound, apiErr.Code)
	_, apiErr = svc.SetSeat(ctx, "owner", "g1", "c1", true)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusNotFound, apiErr.Code)
	assert.Zero(t, src.calls, "no payments call")
	assert.Zero(t, store.setSeat, "no write")
}

func TestOnJoinedAssignsSeatUnlessUnlimited(t *testing.T) {
	ctx := context.Background()
	svc, store, _ := newSeatEnv(true, true, *sum(gymStatusActive, 2), member("c1", db.GymMemberRoleCoach, false, 1))
	svc.OnJoined(ctx, "g1", "c1")
	m, _ := store.Get(ctx, "c1", "g1")
	assert.True(t, m.SeatAssigned)

	svc, store, _ = newSeatEnv(true, true, *sum(gymStatusNone, 0), member("c1", db.GymMemberRoleCoach, false, 1))
	assert.Nil(t, svc.Check(ctx, "g1"), "grandfathered gym admits anyone")
	svc.OnJoined(ctx, "g1", "c1")
	m, _ = store.Get(ctx, "c1", "g1")
	assert.False(t, m.SeatAssigned)

	svc, _, _ = newSeatEnv(true, false, *sum(gymStatusNone, 0))
	apiErr := svc.Check(ctx, "g1")
	require.NotNil(t, apiErr, "grandfather off: no subscription, no seat")
	assert.Equal(t, "SEATS_EXHAUSTED", entErrCode(t, apiErr))
}

func TestGymSummaryCacheKeyAndErrors(t *testing.T) {
	assert.Equal(t, "personal:u1", entCacheKey(EntContext{Kind: EntContextPersonal}, "u1"))
	assert.Equal(t, "gym:g1", entCacheKey(EntContext{Kind: EntContextGym, GymID: "g1"}, "u1"))
	assert.NotEqual(t, entCacheKey(EntContext{Kind: EntContextPersonal}, "x"), entCacheKey(EntContext{Kind: EntContextGym, GymID: "x"}, "x"))

	ctx := context.Background()
	src := &fakeGymSummaries{sum: *sum(gymStatusActive, 3), err: errors.New("down")}
	c := NewGymSummaryCache(src, time.Minute)
	_, apiErr := c.Get(ctx, "g1")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusServiceUnavailable, apiErr.Code)
	src.err = nil
	s, apiErr := c.Get(ctx, "g1")
	require.Nil(t, apiErr)
	assert.Equal(t, int64(3), s.TotalSeats)
	assert.Equal(t, 2, src.calls, "the error was not cached")
	_, _ = c.Get(ctx, "g1")
	assert.Equal(t, 2, src.calls, "success is cached")
	_, _ = c.Get(ctx, "g2")
	assert.Equal(t, 3, src.calls, "another gym is another key")
}

func TestGymSummaryCacheSingleflight(t *testing.T) {
	src := &fakeGymSummaries{sum: *sum(gymStatusActive, 3)}
	c := NewGymSummaryCache(src, time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = c.Get(context.Background(), "g1") }()
	}
	wg.Wait()
	assert.LessOrEqual(t, src.calls, 2)
}

type quotaStoreStub struct{ count int64 }

func (q quotaStoreStub) CountActive(context.Context, string) (int64, error) { return q.count, nil }
func (q quotaStoreStub) MaxGymClientsOverride(context.Context, string) (*int, error) {
	return nil, nil
}

func TestGymPlanLimit(t *testing.T) {
	ctx := context.Background()
	limit := func(grandfather bool, s payments.GymSummary) *int64 {
		l := &gymPlanLimit{summaries: NewGymSummaryCache(&fakeGymSummaries{sum: s}, time.Minute), grandfather: grandfather}
		v, err := l.MaxGymClients(ctx, "g1")
		require.NoError(t, err)
		return v
	}
	assert.Nil(t, limit(true, *sum(gymStatusNone, 0)), "grandfather: no limits")
	s := *sum(gymStatusActive, 3)
	s.MaxGymClients = 25
	require.NotNil(t, limit(true, s))
	assert.Equal(t, int64(25), *limit(true, s), "subscription enforces the cap")
	s.MaxGymClients = 0
	assert.Nil(t, limit(true, s), "active plan without cap is unlimited")
	require.NotNil(t, limit(false, *sum(gymStatusNone, 0)))
	assert.Equal(t, int64(0), *limit(false, *sum(gymStatusNone, 0)))

	// wired into the P4 gym quota: the cap blocks with QUOTA_EXCEEDED
	s.MaxGymClients = 2
	l := &gymPlanLimit{summaries: NewGymSummaryCache(&fakeGymSummaries{sum: s}, time.Minute), grandfather: true}
	apiErr := NewQuotaGuard(nil, quotaStoreStub{count: 2}, l).Check(ctx, QuotaKindGym, "c1", "g1")
	require.NotNil(t, apiErr)
	assert.Equal(t, models.CodeQuotaExceeded, entErrCode(t, apiErr))
	assert.Nil(t, NewQuotaGuard(nil, quotaStoreStub{count: 1}, l).Check(ctx, QuotaKindGym, "c1", "g1"))
}

// ---- sources ----

type fakeResolver struct{ eff *Effective }

func (f fakeResolver) resolve(context.Context, string) (*Effective, *models.ApiError) {
	return f.eff, nil
}

type fakeCoachLookup struct{ in bool }

func (f fakeCoachLookup) InGymAsCoach(context.Context, string) (bool, error) { return f.in, nil }

func TestPersonalSourceSeatOnly(t *testing.T) {
	ctx := context.Background()
	feats := map[string]FeatureState{FeatureWorkouts: {Enabled: true}, FeatureChat: {Enabled: false}}
	noPlan := &Effective{UserID: "c1", Role: "coach", Features: feats}
	withPlan := &Effective{UserID: "c1", Role: "coach", Features: feats, Plan: &PlanInfo{PlanID: "p"}}
	pc := EntContext{Kind: EntContextPersonal}

	r, apiErr := NewPersonalSource(fakeResolver{noPlan}, fakeCoachLookup{true}).Entitlements(ctx, "c1", pc)
	require.Nil(t, apiErr)
	assert.True(t, r.SeatOnly)
	assert.Empty(t, r.Features)
	assert.Equal(t, SourceNoPersonalSubscription, r.Source)

	r, _ = NewPersonalSource(fakeResolver{withPlan}, fakeCoachLookup{true}).Entitlements(ctx, "c1", pc)
	assert.False(t, r.SeatOnly, "a seat plus a personal plan works in both contexts")
	assert.Equal(t, []string{FeatureWorkouts}, r.Features)

	r, _ = NewPersonalSource(fakeResolver{noPlan}, fakeCoachLookup{false}).Entitlements(ctx, "c1", pc)
	assert.False(t, r.SeatOnly, "a coach in no gym is not seat-only")
}

func TestGymSourceAndRegistry(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newSeatEnv(true, true, *sum(gymStatusActive, 1),
		member("owner", db.GymMemberRoleOwner, false, 0),
		member("c1", db.GymMemberRoleCoach, true, 1),
		member("c2", db.GymMemberRoleCoach, true, 2))
	reg := NewEntitlementSources(
		NewPersonalSource(fakeResolver{&Effective{Role: "coach"}}, nil),
		NewGymSource(svc, nil, nil))
	gym := EntContext{Kind: EntContextGym, GymID: "g1"}

	r, apiErr := reg.Resolve(ctx, "owner", gym)
	require.Nil(t, apiErr)
	assert.Equal(t, SourceGymOwner, r.Source)
	assert.Equal(t, []string{"workouts", "chat"}, r.Features)

	r, _ = reg.Resolve(ctx, "c1", gym)
	assert.Equal(t, SourceGymSeat, r.Source)
	assert.Equal(t, "seated", r.SeatState)
	assert.NotEmpty(t, r.Features)

	r, _ = reg.Resolve(ctx, "c2", gym) // overflow: grace keeps working
	assert.Equal(t, "grace", r.SeatState)
	assert.NotEmpty(t, r.Features)

	r, _ = reg.Resolve(ctx, "ghost", gym)
	assert.Equal(t, SourceNoGymSeat, r.Source)
	assert.Empty(t, r.Features)

	_, apiErr = reg.Resolve(ctx, "c1", EntContext{Kind: "x"})
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.Code)
	_, apiErr = reg.Resolve(ctx, "c1", EntContext{Kind: EntContextGym})
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.Code)

	// lapsed: read_only state and no features
	svc, _, _ = newSeatEnv(true, true, *sum(gymStatusLapsed, 1), member("c1", db.GymMemberRoleCoach, true, 1))
	r, _ = NewGymSource(svc, nil, nil).Entitlements(ctx, "c1", gym)
	assert.Equal(t, "read_only", r.SeatState)
	assert.Empty(t, r.Features)
}

func TestGymSourceGrandfatherUsesPersonalFeatures(t *testing.T) {
	svc, _, _ := newSeatEnv(true, true, *sum(gymStatusNone, 0), member("c1", db.GymMemberRoleCoach, false, 1))
	src := NewGymSource(svc, func(context.Context, string) ([]string, bool, *models.ApiError) {
		return []string{"workouts"}, true, nil
	}, nil)
	r, apiErr := src.Entitlements(context.Background(), "c1", EntContext{Kind: EntContextGym, GymID: "g1"})
	require.Nil(t, apiErr)
	assert.Equal(t, SourceGrandfather, r.Source)
	assert.Equal(t, []string{"workouts"}, r.Features)
}

// ---- personal summary callers ignore gym entries ----

type typedPlans struct {
	entries []payments.SubscriptionSummaryEntry
}

func (p typedPlans) GetSubscriptionSummaries(context.Context, []string) ([]payments.SubscriptionSummaryEntry, error) {
	return p.entries, nil
}

func TestPlansForIgnoresGymEntries(t *testing.T) {
	s := &entitlementServiceImpl{
		plans: typedPlans{[]payments.SubscriptionSummaryEntry{
			{UserID: "u1", Status: "active", PlanID: "gymplan", Type: payments.SummaryTypeGym, PlanOwnerType: "gym", PlanOwnerID: "g1"},
			{UserID: "u1", Status: "active", PlanID: "personal", Type: payments.SummaryTypePersonal},
			{UserID: "u2", Status: "active", PlanID: "gymplan2", Type: payments.SummaryTypeGym},
			{UserID: "u3", Status: "active", PlanID: "legacy"}, // older payments-api: no type
		}},
		now: time.Now, planMap: map[string]planEntry{},
	}
	got := s.plansFor(context.Background(), []string{"u1", "u2", "u3"})
	require.NotNil(t, got["u1"])
	assert.Equal(t, "personal", got["u1"].PlanID)
	assert.Nil(t, got["u2"], "only a gym entry: no personal plan")
	require.NotNil(t, got["u3"])
	assert.Equal(t, "legacy", got["u3"].PlanID)
}
