package services_test

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
	"coachify-account-api/services"
	"coachify-account-api/services/mocks"
)

// fakeAuthzMembers is an in-memory GymMembershipReader.
type fakeAuthzMembers struct{ rows []db.GymMember }

func (f *fakeAuthzMembers) MembershipsOf(_ context.Context, userID string) ([]db.GymMember, error) {
	out := []db.GymMember{}
	for _, m := range f.rows {
		if m.UserID == userID {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeAuthzMembers) MembershipOf(_ context.Context, userID, gymID string) (*db.GymMember, error) {
	for i := range f.rows {
		if f.rows[i].UserID == userID && f.rows[i].GymID == gymID {
			return &f.rows[i], nil
		}
	}
	return nil, nil
}

// fakePolicyStore is an in-memory services.GymPolicyStore that counts reads.
type fakePolicyStore struct {
	policies              map[string]*db.GymRolePolicy // gym|role
	grants                map[string][]db.PermissionGrant
	versions              map[string]int64
	reads                 atomic.Int64
	failReplace, failBump bool
}

func newFakePolicyStore() *fakePolicyStore {
	return &fakePolicyStore{policies: map[string]*db.GymRolePolicy{}, grants: map[string][]db.PermissionGrant{}, versions: map[string]int64{}}
}

func (f *fakePolicyStore) GetRolePolicy(_ context.Context, gymID string, role db.GymMemberRole) (*db.GymRolePolicy, error) {
	f.reads.Add(1)
	return f.policies[gymID+"|"+string(role)], nil
}

func (f *fakePolicyStore) ListGrants(_ context.Context, gymID, userID string) ([]db.PermissionGrant, error) {
	f.reads.Add(1)
	return f.grants[gymID+"|"+userID], nil
}

func (f *fakePolicyStore) GetAuthzVersion(_ context.Context, gymID string) (int64, error) {
	return f.versions[gymID], nil
}

func (f *fakePolicyStore) UpsertRolePolicy(_ context.Context, p *db.GymRolePolicy, expected int64) (*db.GymRolePolicy, error) {
	k := p.GymID + "|" + string(p.Role)
	cur := f.policies[k]
	var have int64
	if cur != nil {
		have = cur.Version
	}
	if have != expected {
		return nil, repositories.ErrPolicyVersionConflict
	}
	n := *p
	n.Version = have + 1
	f.policies[k] = &n
	return &n, nil
}

func (f *fakePolicyStore) ReplaceGrants(_ context.Context, gymID, userID string, g []db.PermissionGrant) error {
	if f.failReplace {
		// A failed bulk write may have applied part of the set.
		f.grants[gymID+"|"+userID] = g
		return errors.New("bulk write failed")
	}
	f.grants[gymID+"|"+userID] = g
	return nil
}

func (f *fakePolicyStore) BumpAuthzVersion(_ context.Context, gymID string) (int64, error) {
	if f.failBump {
		return 0, errors.New("bump failed")
	}
	f.versions[gymID]++
	return f.versions[gymID], nil
}

const gymA = "gymA"

func member(user string, role db.GymMemberRole) db.GymMember {
	return db.GymMember{GymID: gymA, UserID: user, Role: role, Status: db.GymMemberStatusActive}
}

type chainUnderTest interface {
	services.Authorizer
	services.CapabilityResolver
}

func authzFixture() (*fakeAuthzMembers, *fakePolicyStore, chainUnderTest) {
	m := &fakeAuthzMembers{rows: []db.GymMember{
		member("owner", db.GymMemberRoleOwner),
		member("head", db.GymMemberRoleHeadCoach),
		member("coach", db.GymMemberRoleCoach),
	}}
	s := newFakePolicyStore()
	return m, s, services.NewChainAuthorizer(m, s, nil)
}

func can(t *testing.T, a services.Authorizer, user string, c services.Capability) services.Decision {
	t.Helper()
	d, apiErr := a.Can(context.Background(), services.Actor{ID: user}, c, services.Resource{Type: services.ResourceGym, GymID: gymA})
	require.Nil(t, apiErr)
	return d
}

func grant(s *fakePolicyStore, user, capability string, e db.GrantEffect) {
	k := gymA + "|" + user
	s.grants[k] = append(s.grants[k], db.PermissionGrant{GymID: gymA, UserID: user, Capability: capability, Effect: e})
}

func TestChainPrecedence(t *testing.T) {
	type tc struct {
		name   string
		setup  func(s *fakePolicyStore)
		user   string
		cap    services.Capability
		allow  bool
		reason string
	}
	rolePolicy := func(role db.GymMemberRole, caps map[string]bool) func(s *fakePolicyStore) {
		return func(s *fakePolicyStore) {
			s.policies[gymA+"|"+string(role)] = &db.GymRolePolicy{GymID: gymA, Role: role, Capabilities: caps, Version: 1}
		}
	}
	cases := []tc{
		{"owner has everything", nil, "owner", services.CapPlanManage, true, services.ReasonOwner},
		{"owner has gym.manage", nil, "owner", services.CapGymManage, true, services.ReasonOwner},
		{"preset head allow", nil, "head", services.CapClientAssign, true, services.ReasonPlatformPreset},
		{"preset head write off", nil, "head", services.CapWorkoutWrite, false, services.ReasonPlatformPreset},
		{"preset head plan.manage off", nil, "head", services.CapPlanManage, false, services.ReasonPlatformPreset},
		{"preset coach library.read", nil, "coach", services.CapLibraryRead, true, services.ReasonPlatformPreset},
		{"preset coach cannot assign", nil, "coach", services.CapClientAssign, false, services.ReasonPlatformPreset},
		{"preset coach is not head", nil, "coach", services.CapGymHead, false, services.ReasonNoRule},
		{"head is head", nil, "head", services.CapGymHead, true, services.ReasonPlatformPreset},
		{"head cannot manage gym", nil, "head", services.CapGymManage, false, services.ReasonNoRule},
		{"role policy beats preset (on)", rolePolicy(db.GymMemberRoleCoach, map[string]bool{"exercise.write": true}), "coach", services.CapExerciseWrite, true, services.ReasonRolePolicy},
		{"role policy beats preset (off)", rolePolicy(db.GymMemberRoleHeadCoach, map[string]bool{"client.assign": false}), "head", services.CapClientAssign, false, services.ReasonRolePolicy},
		{"role policy falls through to preset", rolePolicy(db.GymMemberRoleHeadCoach, map[string]bool{"client.assign": false}), "head", services.CapClientUnassign, true, services.ReasonPlatformPreset},
		{"other role policy does not apply", rolePolicy(db.GymMemberRoleHeadCoach, map[string]bool{"exercise.write": true}), "coach", services.CapExerciseWrite, false, services.ReasonPlatformPreset},
		{"member allow beats role policy", func(s *fakePolicyStore) {
			rolePolicy(db.GymMemberRoleCoach, map[string]bool{"workout.write": false})(s)
			grant(s, "coach", "workout.write", db.GrantAllow)
		}, "coach", services.CapWorkoutWrite, true, services.ReasonMemberAllow},
		{"member deny beats role policy and preset", func(s *fakePolicyStore) {
			rolePolicy(db.GymMemberRoleHeadCoach, map[string]bool{"client.view_all": true})(s)
			grant(s, "head", "client.view_all", db.GrantDeny)
		}, "head", services.CapClientViewAll, false, services.ReasonMemberDeny},
		{"member deny beats preset", func(s *fakePolicyStore) { grant(s, "head", "library.read", db.GrantDeny) }, "head", services.CapLibraryRead, false, services.ReasonMemberDeny},
		{"non member", nil, "stranger", services.CapLibraryRead, false, services.ReasonNotMember},
		{"unknown capability", nil, "owner", services.Capability("nope"), false, services.ReasonUnknownCap},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, s, a := authzFixture()
			if c.setup != nil {
				c.setup(s)
			}
			d := can(t, a, c.user, c.cap)
			assert.Equal(t, c.allow, d.Allowed)
			assert.Equal(t, c.reason, d.Reason)
		})
	}
}

func TestOwnerCannotBeRevoked(t *testing.T) {
	_, s, a := authzFixture()
	// Even a stray deny row or a role policy naming the owner role changes nothing.
	for _, c := range db.GymCapabilities {
		grant(s, "owner", c, db.GrantDeny)
	}
	s.policies[gymA+"|owner"] = &db.GymRolePolicy{GymID: gymA, Role: db.GymMemberRoleOwner, Capabilities: map[string]bool{"library.read": false}}
	for _, c := range db.GymCapabilities {
		d := can(t, a, "owner", services.Capability(c))
		assert.True(t, d.Allowed, c)
		assert.Equal(t, services.ReasonOwner, d.Reason)
	}
}

func TestOwnerWithoutGymIDResolvesOwnGym(t *testing.T) {
	_, _, a := authzFixture()
	d, apiErr := a.Can(context.Background(), services.Actor{ID: "head"}, services.CapGymHead, services.Resource{Type: services.ResourceGym})
	require.Nil(t, apiErr)
	assert.True(t, d.Allowed)
	assert.Equal(t, gymA, d.GymID)
}

func TestPersonalContextInvariant(t *testing.T) {
	_, s, a := authzFixture()
	ctx := context.Background()
	for _, res := range []services.Resource{
		{Type: services.ResourceClient, ID: "c1", GymID: gymA, Context: services.ContextPersonal},
		{Type: services.ResourceClient, ID: "c1", GymID: gymA}, // missing context means personal
		{Type: "exercise", ID: "e1", GymID: gymA, Context: services.ContextPersonal},
	} {
		for _, user := range []string{"owner", "head", "coach"} {
			// Not even the owner link or a member allow grants personal-context data.
			grant(s, user, "client.view_all", db.GrantAllow)
			d, apiErr := a.Can(ctx, services.Actor{ID: user}, services.CapClientViewAll, res)
			require.Nil(t, apiErr)
			assert.False(t, d.Allowed, "%s on %+v", user, res)
			assert.Equal(t, services.ReasonPersonalContext, d.Reason)
		}
	}
	// Gym context with no active gym relationship (P2 has no gym_clients) is denied for the owner too.
	d, apiErr := a.Can(ctx, services.Actor{ID: "owner"}, services.CapClientViewAll,
		services.Resource{Type: services.ResourceClient, ID: "c1", GymID: gymA, Context: services.ContextGym})
	require.Nil(t, apiErr)
	assert.False(t, d.Allowed)
	assert.Equal(t, services.ReasonClientNotInGym, d.Reason)
}

type fakeClientScope struct{ scope *services.ResourceScope }

func (f fakeClientScope) ClientScope(context.Context, string) (*services.ResourceScope, error) {
	return f.scope, nil
}

func TestGymContextClientNeedsActiveGymRelationship(t *testing.T) {
	m, s, _ := authzFixture()
	scope := &services.ResourceScope{PersonalCoachID: "someone", Gyms: []services.ScopeGym{{GymID: gymA, CoachID: "coach"}}}
	a := services.NewChainAuthorizer(m, s, services.NewResourceResolver(fakeClientScope{scope}))
	res := services.Resource{Type: services.ResourceClient, ID: "c1", GymID: gymA, Context: services.ContextGym}
	d, apiErr := a.Can(context.Background(), services.Actor{ID: "owner"}, services.CapClientViewAll, res)
	require.Nil(t, apiErr)
	assert.True(t, d.Allowed)
	assert.Equal(t, services.ReasonOwner, d.Reason)
	// Same client, other gym: not reachable.
	m.rows = append(m.rows, db.GymMember{GymID: "gymB", UserID: "owner", Role: db.GymMemberRoleOwner})
	res.GymID = "gymB"
	d, _ = a.Can(context.Background(), services.Actor{ID: "owner"}, services.CapClientViewAll, res)
	assert.False(t, d.Allowed)
	assert.Equal(t, services.ReasonClientNotInGym, d.Reason)
}

func TestCachingAuthorizerStalenessAndVersionBump(t *testing.T) {
	_, s, chain := authzFixture()
	cache := services.NewCachingAuthorizer(chain, time.Hour)
	assert.True(t, can(t, cache, "head", services.CapClientAssign).Allowed)
	first := s.reads.Load()

	// Served from the cache: no new store reads.
	assert.True(t, can(t, cache, "head", services.CapClientAssign).Allowed)
	assert.Equal(t, first, s.reads.Load())

	// Underlying change is invisible until the gym version moves (stale by design within the TTL).
	grant(s, "head", "client.assign", db.GrantDeny)
	assert.True(t, can(t, cache, "head", services.CapClientAssign).Allowed, "stale until invalidated")

	cache.Invalidate(gymA)
	d := can(t, cache, "head", services.CapClientAssign)
	assert.False(t, d.Allowed)
	assert.Equal(t, services.ReasonMemberDeny, d.Reason)
}

func TestCachingAuthorizerExpiresAfterTTL(t *testing.T) {
	_, s, chain := authzFixture()
	cache := services.NewCachingAuthorizer(chain, 20*time.Millisecond)
	assert.True(t, can(t, cache, "head", services.CapClientAssign).Allowed)
	grant(s, "head", "client.assign", db.GrantDeny)
	time.Sleep(40 * time.Millisecond)
	assert.False(t, can(t, cache, "head", services.CapClientAssign).Allowed)
}

func newPolicySvc(enforce bool) (services.GymPolicyService, *fakePolicyStore, services.Authorizer) {
	m, s, chain := authzFixture()
	cache := services.NewCachingAuthorizer(chain, time.Hour)
	return services.NewGymPolicyService(cache, chain, m, s, fakeGymNames{}, fakePolicyUsers{}, cache, enforce), s, cache
}

type fakeGymNames struct{}

func (fakeGymNames) GetGym(_ context.Context, id string) (*db.Gym, error) {
	return &db.Gym{Name: "Gym " + id}, nil
}

func TestPolicyServiceOwnerOnly(t *testing.T) {
	svc, _, _ := newPolicySvc(true)
	ctx := context.Background()

	_, apiErr := svc.GetRolePolicy(ctx, "head", gymA, "coach")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusForbidden, apiErr.Code)

	_, apiErr = svc.GetRolePolicy(ctx, "stranger", gymA, "coach")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusNotFound, apiErr.Code)

	_, apiErr = svc.PutGrants(ctx, "coach", gymA, "head", nil)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusForbidden, apiErr.Code)
}

func TestPolicyServiceRolePolicyVersioningAndInvalidation(t *testing.T) {
	svc, _, authz := newPolicySvc(true)
	ctx := context.Background()

	v, apiErr := svc.GetRolePolicy(ctx, "owner", gymA, "coach")
	require.Nil(t, apiErr)
	assert.Equal(t, int64(0), v.Version)
	assert.True(t, v.Capabilities["library.read"])
	assert.False(t, v.Capabilities["workout.write"])
	assert.Equal(t, v.Capabilities, v.Preset, "no override yet: effective equals preset")

	// Warm the cache, then write: the next check must see the new policy at once.
	assert.False(t, can(t, authz, "coach", services.CapWorkoutWrite).Allowed)
	v, apiErr = svc.PutRolePolicy(ctx, "owner", gymA, "coach", map[string]bool{"workout.write": true}, 0)
	require.Nil(t, apiErr)
	assert.Equal(t, int64(1), v.Version)
	assert.True(t, v.Capabilities["workout.write"])
	assert.False(t, v.Preset["workout.write"], "preset stays the platform value, not the override")
	assert.True(t, v.Preset["library.read"])
	assert.Len(t, v.Preset, 9)
	assert.True(t, can(t, authz, "coach", services.CapWorkoutWrite).Allowed)

	_, apiErr = svc.PutRolePolicy(ctx, "owner", gymA, "coach", map[string]bool{}, 0)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusConflict, apiErr.Code)

	_, apiErr = svc.PutRolePolicy(ctx, "owner", gymA, "owner", map[string]bool{}, 0)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.Code)

	_, apiErr = svc.PutRolePolicy(ctx, "owner", gymA, "coach", map[string]bool{"gym.manage": true}, 1)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.Code, "structural capabilities cannot be configured")
}

func TestPolicyServiceGrants(t *testing.T) {
	svc, _, authz := newPolicySvc(true)
	ctx := context.Background()

	_, apiErr := svc.PutGrants(ctx, "owner", gymA, "owner", nil)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.Code, "owner cannot be targeted")

	_, apiErr = svc.PutGrants(ctx, "owner", gymA, "ghost", nil)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusNotFound, apiErr.Code)

	for name, in := range map[string][]services.GrantInput{
		"bad capability": {{Capability: "nope", Effect: "allow"}},
		"bad effect":     {{Capability: "library.read", Effect: "maybe"}},
		"duplicate":      {{Capability: "library.read", Effect: "allow"}, {Capability: "library.read", Effect: "deny"}},
	} {
		_, apiErr = svc.PutGrants(ctx, "owner", gymA, "coach", in)
		require.NotNil(t, apiErr, name)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code, name)
	}

	assert.False(t, can(t, authz, "coach", services.CapProgramWrite).Allowed)
	g, apiErr := svc.PutGrants(ctx, "owner", gymA, "coach", []services.GrantInput{{Capability: "program.write", Effect: "allow"}})
	require.Nil(t, apiErr)
	assert.Len(t, g.Grants, 1)
	assert.True(t, can(t, authz, "coach", services.CapProgramWrite).Allowed)

	got, apiErr := svc.GetGrants(ctx, "owner", gymA, "coach")
	require.Nil(t, apiErr)
	assert.Len(t, got.Grants, 1)
}

func TestGymContextPersonalVersusGym(t *testing.T) {
	svc, _, _ := newPolicySvc(true)
	ctx := context.Background()

	// A client (no membership) is in personal context: no gym, no capabilities.
	v, apiErr := svc.GymContext(ctx, "client1")
	require.Nil(t, apiErr)
	assert.Equal(t, services.ContextPersonal, v.Context)
	assert.Empty(t, v.Memberships)
	assert.Empty(t, v.DefaultGym)

	v, apiErr = svc.GymContext(ctx, "head")
	require.Nil(t, apiErr)
	require.Len(t, v.Memberships, 1)
	assert.Equal(t, services.ContextGym, v.Context)
	assert.Equal(t, gymA, v.DefaultGym)
	assert.Equal(t, "Gym "+gymA, v.Memberships[0].GymName)
	assert.True(t, v.Memberships[0].Capabilities["client.assign"])
	assert.False(t, v.Memberships[0].Capabilities["plan.manage"])
	assert.Len(t, v.Memberships[0].Capabilities, 9)
}

func TestInternalCheckEnforceAndReportModes(t *testing.T) {
	ctx := context.Background()
	check := func(cap string) services.AuthzCheck {
		ck := services.AuthzCheck{Capability: cap, GymID: gymA}
		ck.Resource.Type = services.ResourceGym
		return ck
	}

	enforce, _, _ := newPolicySvc(true)
	res, apiErr := enforce.Check(ctx, services.Actor{ID: "coach"}, []services.AuthzCheck{check("library.read"), check("workout.write")})
	require.Nil(t, apiErr)
	assert.Equal(t, "enforce", res.Mode)
	assert.Equal(t, services.AuthzTTLSeconds, res.TTLSeconds)
	assert.True(t, res.Decisions[0].Allowed)
	assert.False(t, res.Decisions[1].Allowed)
	assert.False(t, res.Decisions[1].WouldDeny)

	report, _, _ := newPolicySvc(false)
	res, apiErr = report.Check(ctx, services.Actor{ID: "coach"}, []services.AuthzCheck{check("library.read"), check("workout.write")})
	require.Nil(t, apiErr)
	assert.Equal(t, "report", res.Mode)
	assert.True(t, res.Decisions[0].Allowed)
	assert.False(t, res.Decisions[0].WouldDeny)
	assert.True(t, res.Decisions[1].Allowed, "report mode never denies")
	assert.True(t, res.Decisions[1].WouldDeny)
	assert.Equal(t, services.ReasonPlatformPreset, res.Decisions[1].Reason)

	_, apiErr = enforce.Check(ctx, services.Actor{}, []services.AuthzCheck{check("library.read")})
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.Code)
	_, apiErr = enforce.Check(ctx, services.Actor{ID: "coach"}, nil)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.Code)
	_, apiErr = enforce.Check(ctx, services.Actor{ID: "coach"}, make([]services.AuthzCheck, services.MaxAuthzChecks+1))
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.Code)
}

// spyAuthorizer records calls and answers with a fixed decision.
type spyAuthorizer struct {
	calls int
	d     services.Decision
}

func (s *spyAuthorizer) Can(context.Context, services.Actor, services.Capability, services.Resource) (services.Decision, *models.ApiError) {
	s.calls++
	return s.d, nil
}

func TestGymServiceAuthzFlag(t *testing.T) {
	ctx := context.Background()
	build := func(enforce bool, spy *spyAuthorizer) (services.GymService, *fakeGymStore, *db.Gym) {
		u, g := &mocks.TeamUserStore{}, newFakeGymStore()
		givenUser(u, gymUser("owner1", db.RoleGymOwner, db.Active, "o1@x.tn", false))
		givenUser(u, gymUser("head1", "coach", db.Active, "h1@x.tn", true))
		gym := g.addGym("owner1", db.GymStatusActive)
		svc := services.NewGymService(u, g, services.GymSettings{InviteTTL: time.Hour, InvitesPerDay: 20, Authz: spy, AuthzEnforce: enforce})
		return svc, g, gym
	}

	t.Run("flag off keeps legacy checks and never calls the authorizer", func(t *testing.T) {
		spy := &spyAuthorizer{d: services.Decision{Allowed: false, Reason: services.ReasonNotMember}}
		svc, g, gym := build(false, spy)
		_, apiErr := svc.Overview(ctx, "owner1", gym.ID.Hex())
		require.Nil(t, apiErr)
		_, apiErr = svc.Overview(ctx, "owner1", g.addGym("owner2", db.GymStatusActive).ID.Hex())
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)
		_, apiErr = svc.MyGym(ctx, "head1")
		require.Nil(t, apiErr)
		assert.Zero(t, spy.calls)
	})

	t.Run("flag on asks the authorizer", func(t *testing.T) {
		spy := &spyAuthorizer{d: services.Decision{Allowed: true, Reason: services.ReasonOwner}}
		svc, _, gym := build(true, spy)
		_, apiErr := svc.Overview(ctx, "owner1", gym.ID.Hex())
		require.Nil(t, apiErr)
		assert.Equal(t, 1, spy.calls)

		spy.d = services.Decision{Reason: services.ReasonNoRule}
		_, apiErr = svc.Overview(ctx, "owner1", gym.ID.Hex())
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)
	})

	t.Run("flag on head coach", func(t *testing.T) {
		spy := &spyAuthorizer{d: services.Decision{Reason: services.ReasonNoRule}}
		svc, _, _ := build(true, spy)
		_, apiErr := svc.MyGym(ctx, "head1")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusForbidden, apiErr.Code)

		spy.d = services.Decision{Allowed: true, Reason: services.ReasonPlatformPreset}
		_, apiErr = svc.MyGym(ctx, "head1")
		require.Nil(t, apiErr)

		// A head coach in no gym keeps the legacy flag.
		spy.d = services.Decision{Reason: services.ReasonNotMember}
		_, apiErr = svc.MyGym(ctx, "head1")
		require.Nil(t, apiErr)
	})
}

// fakePolicyUsers knows the fixture users; the owner and "stranger" are active gym owners, "boss" is suspended.
type fakePolicyUsers struct{}

func (fakePolicyUsers) GetUserByExternalIdUpdate(_ context.Context, id string) (*db.User, *models.ApiError) {
	switch id {
	case "owner", "stranger":
		return &db.User{ExternalID: id, UserRole: db.RoleGymOwner, UserStatus: db.Active}, nil
	case "boss":
		return &db.User{ExternalID: id, UserRole: db.RoleGymOwner, UserStatus: db.Suspended}, nil
	case "head", "coach":
		return &db.User{ExternalID: id, UserRole: "coach", UserStatus: db.Active}, nil
	}
	return nil, &models.ApiError{Code: http.StatusNotFound, Error: errors.New("user not found")}
}

func TestPolicyServiceRequiresActiveGymOwnerAccount(t *testing.T) {
	m, s, chain := authzFixture()
	m.rows = append(m.rows, member("boss", db.GymMemberRoleOwner))
	cache := services.NewCachingAuthorizer(chain, time.Hour)
	svc := services.NewGymPolicyService(cache, chain, m, s, fakeGymNames{}, fakePolicyUsers{}, cache, true)
	ctx := context.Background()

	// Owner row in the gym, but the account is suspended: 403 like the legacy requireOwner.
	_, apiErr := svc.GetRolePolicy(ctx, "boss", gymA, "coach")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusForbidden, apiErr.Code)
	assert.EqualError(t, apiErr.Error, "Gym owner access required")

	// Unknown account: 403. Wrong token role (a coach row holder): 403, not 404.
	_, apiErr = svc.GetRolePolicy(ctx, "ghost", gymA, "coach")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusForbidden, apiErr.Code)

	// Active owner of another gym: 404.
	_, apiErr = svc.GetRolePolicy(ctx, "stranger", gymA, "coach")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusNotFound, apiErr.Code)
}

func TestPolicyServiceInvalidatesOnErrorPaths(t *testing.T) {
	ctx := context.Background()

	t.Run("bump failure", func(t *testing.T) {
		svc, s, authz := newPolicySvc(true)
		assert.False(t, can(t, authz, "coach", services.CapWorkoutWrite).Allowed) // warm the cache
		s.failBump = true
		_, apiErr := svc.PutRolePolicy(ctx, "owner", gymA, "coach", map[string]bool{"workout.write": true}, 0)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
		assert.True(t, can(t, authz, "coach", services.CapWorkoutWrite).Allowed, "policy was written, cache must not serve the old answer")
	})

	t.Run("partial grants write failure", func(t *testing.T) {
		svc, s, authz := newPolicySvc(true)
		assert.False(t, can(t, authz, "coach", services.CapProgramWrite).Allowed)
		s.failReplace = true
		_, apiErr := svc.PutGrants(ctx, "owner", gymA, "coach", []services.GrantInput{{Capability: "program.write", Effect: "allow"}})
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
		assert.EqualError(t, apiErr.Error, "internal error")
		assert.True(t, can(t, authz, "coach", services.CapProgramWrite).Allowed, "applied part of the write is visible, not hidden by the cache")
	})
}

// fakeMemberStore2 is a GymMemberStore whose mutations can fail.
type fakeMutatingStore struct {
	services.GymMemberStore
	err error
}

func (f fakeMutatingStore) UpsertActive(context.Context, *db.GymMember) error { return f.err }
func (f fakeMutatingStore) MarkLeft(context.Context, string, string, time.Time) (bool, error) {
	return true, f.err
}
func (f fakeMutatingStore) MarkLeftHead(context.Context, string, time.Time) (bool, error) {
	return true, f.err
}

type recordingBumper struct{ gyms []string }

func (r *recordingBumper) BumpAuthzVersion(_ context.Context, gymID string) (int64, error) {
	r.gyms = append(r.gyms, gymID)
	return int64(len(r.gyms)), nil
}

func TestMembershipMutationsInvalidateAndBump(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		mutate func(s services.GymMemberStore)
		bumped []string
	}{
		"upsert":         {func(s services.GymMemberStore) { _ = s.UpsertActive(ctx, &db.GymMember{GymID: gymA, UserID: "x"}) }, []string{gymA}},
		"mark left":      {func(s services.GymMemberStore) { _, _ = s.MarkLeft(ctx, gymA, "x", time.Now()) }, []string{gymA}},
		"mark left head": {func(s services.GymMemberStore) { _, _ = s.MarkLeftHead(ctx, "x", time.Now()) }, nil}, // gym unknown: global drop
		"upsert failed":  {func(s services.GymMemberStore) { _ = s.UpsertActive(ctx, &db.GymMember{GymID: gymA, UserID: "x"}) }, []string{gymA}},
	} {
		t.Run(name, func(t *testing.T) {
			m, s, chain := authzFixture()
			cache := services.NewCachingAuthorizer(chain, time.Hour)
			bumper := &recordingBumper{}
			var inner error
			if name == "upsert failed" {
				inner = errors.New("write failed")
			}
			store := services.NewInvalidatingMemberStore(fakeMutatingStore{err: inner}, cache, bumper)
			_ = s

			assert.True(t, can(t, cache, "head", services.CapClientAssign).Allowed)
			// The head coach leaves behind the cache's back; only the store hook can make it visible.
			m.rows = []db.GymMember{member("owner", db.GymMemberRoleOwner)}
			assert.True(t, can(t, cache, "head", services.CapClientAssign).Allowed, "stale before the mutation")
			tc.mutate(store)
			d := can(t, cache, "head", services.CapClientAssign)
			assert.False(t, d.Allowed)
			assert.Equal(t, services.ReasonNotMember, d.Reason)
			assert.Equal(t, tc.bumped, bumper.gyms)
		})
	}

	t.Run("nil invalidator and bumper keep plain behavior", func(t *testing.T) {
		store := services.NewInvalidatingMemberStore(fakeMutatingStore{}, nil, nil)
		assert.NoError(t, store.UpsertActive(ctx, &db.GymMember{GymID: gymA}))
	})
}

func TestCacheKeyFieldsCannotCollide(t *testing.T) {
	spy := &spyAuthorizer{d: services.Decision{Allowed: true, Reason: services.ReasonOwner}}
	cache := services.NewCachingAuthorizer(spy, time.Hour)
	a := services.Actor{ID: "u"}
	// With a plain "|" join these two resources produce the same key.
	r1 := services.Resource{Type: "client", ID: "a|gym", Context: ""}
	r2 := services.Resource{Type: "client", ID: "a", Context: "gym"}
	_, _ = cache.Can(context.Background(), a, services.CapLibraryRead, r1)
	_, _ = cache.Can(context.Background(), a, services.CapLibraryRead, r2)
	assert.Equal(t, 2, spy.calls, "distinct resources must not share a cache entry")
	_, _ = cache.Can(context.Background(), a, services.CapLibraryRead, r1)
	assert.Equal(t, 2, spy.calls, "same resource is served from the cache")
}
