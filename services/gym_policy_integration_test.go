//go:build integration

package services_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
	"coachify-account-api/services"
)

// ownerUsers answers the owner re-check of the policy service: every id is an Active gym owner account.
type ownerUsers struct{}

func (ownerUsers) GetUserByExternalIdUpdate(_ context.Context, id string) (*db.User, *models.ApiError) {
	return &db.User{ExternalID: id, UserRole: db.RoleGymOwner, UserStatus: db.Active}, nil
}

type policyEnv struct {
	*memberEnv
	policies *repositories.GymPolicyRepository
	authz    services.Authorizer
	cache    interface{ Invalidate(string) }
	svc      services.GymPolicyService
	members  services.GymMemberStore // invalidating wrapper
	gymID    string
}

func newPolicyEnv(t *testing.T) *policyEnv {
	t.Helper()
	e := newMemberEnv(t)
	policies := repositories.NewGymPolicyRepository(e.database)
	reader := services.NewRepoMembershipReader(e.members)
	caching := services.NewCachingAuthorizer(services.NewChainAuthorizer(reader, policies, nil), time.Minute)
	members := services.NewInvalidatingMemberStore(e.members, caching, policies)
	svc := services.NewGymPolicyService(caching, services.NewChainAuthorizer(reader, policies, nil), reader, policies,
		e.gyms, ownerUsers{}, caching, true)

	gymID := e.gym(t, "o1")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	now := time.Now()
	require.NoError(t, e.members.UpsertActive(ctx, &db.GymMember{GymID: gymID, UserID: "o1", Role: db.GymMemberRoleOwner,
		Status: db.GymMemberStatusActive, JoinedAt: now, AddedBy: "admin1", Source: db.GymMemberSourceAdmin}))
	require.NoError(t, e.members.UpsertActive(ctx, &db.GymMember{GymID: gymID, UserID: "c1", Role: db.GymMemberRoleCoach,
		Status: db.GymMemberStatusActive, SupervisorID: "o1", JoinedAt: now, AddedBy: "o1", Source: db.GymMemberSourceInvite}))
	return &policyEnv{memberEnv: e, policies: policies, authz: caching, cache: caching, svc: svc, members: members, gymID: gymID}
}

func (e *policyEnv) can(t *testing.T, user string, c services.Capability) services.Decision {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	d, apiErr := e.authz.Can(ctx, services.Actor{ID: user}, c, services.Resource{Type: services.ResourceGym, ID: e.gymID, GymID: e.gymID})
	require.Nil(t, apiErr)
	return d
}

func TestGymPolicyServiceIT_OwnerPutFlipsDecision(t *testing.T) {
	e := newPolicyEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// preset: coach may not manage plans; the decision is now cached
	d := e.can(t, "c1", services.CapPlanManage)
	assert.False(t, d.Allowed)
	assert.Equal(t, services.ReasonPlatformPreset, d.Reason)

	view, apiErr := e.svc.PutRolePolicy(ctx, "o1", e.gymID, "coach", map[string]bool{db.CapPlanManage: true}, 0)
	require.Nil(t, apiErr)
	assert.EqualValues(t, 1, view.Version)
	assert.True(t, view.Capabilities[db.CapPlanManage])

	d = e.can(t, "c1", services.CapPlanManage)
	assert.True(t, d.Allowed, "role policy applies at once after Invalidate")
	assert.Equal(t, services.ReasonRolePolicy, d.Reason)

	// stale version is a 409 and changes nothing
	_, apiErr = e.svc.PutRolePolicy(ctx, "o1", e.gymID, "coach", map[string]bool{db.CapPlanManage: false}, 0)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusConflict, apiErr.Code)
	assert.True(t, e.can(t, "c1", services.CapPlanManage).Allowed)

	// next version flips it back
	_, apiErr = e.svc.PutRolePolicy(ctx, "o1", e.gymID, "coach", map[string]bool{db.CapPlanManage: false}, 1)
	require.Nil(t, apiErr)
	assert.False(t, e.can(t, "c1", services.CapPlanManage).Allowed)

	// member override wins over the role policy, and a replace to empty drops it
	_, apiErr = e.svc.PutGrants(ctx, "o1", e.gymID, "c1", []services.GrantInput{{Capability: db.CapPlanManage, Effect: "allow"}})
	require.Nil(t, apiErr)
	d = e.can(t, "c1", services.CapPlanManage)
	assert.True(t, d.Allowed)
	assert.Equal(t, services.ReasonMemberAllow, d.Reason)
	_, apiErr = e.svc.PutGrants(ctx, "o1", e.gymID, "c1", nil)
	require.Nil(t, apiErr)
	assert.False(t, e.can(t, "c1", services.CapPlanManage).Allowed)

	// every write bumped authz_version: 2 policy puts + 2 grant puts
	v, err := e.policies.GetAuthzVersion(ctx, e.gymID)
	require.NoError(t, err)
	assert.EqualValues(t, 4, v)
}

func TestGymPolicyServiceIT_RemovingMemberRevokesImmediately(t *testing.T) {
	e := newPolicyEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	d := e.can(t, "c1", services.CapLibraryRead)
	assert.True(t, d.Allowed, "coach reads the library by preset")
	assert.Equal(t, services.ReasonPlatformPreset, d.Reason)

	before, err := e.policies.GetAuthzVersion(ctx, e.gymID)
	require.NoError(t, err)

	left, err := e.members.MarkLeft(ctx, e.gymID, "c1", time.Now())
	require.NoError(t, err)
	require.True(t, left)

	d = e.can(t, "c1", services.CapLibraryRead)
	assert.False(t, d.Allowed, "removed member loses access with no TTL wait")
	assert.Equal(t, services.ReasonNotMember, d.Reason)

	after, err := e.policies.GetAuthzVersion(ctx, e.gymID)
	require.NoError(t, err)
	assert.Greater(t, after, before, "authz_version bumped for downstream caches")
}
