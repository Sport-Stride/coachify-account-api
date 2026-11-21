package services_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models/db"
	"coachify-account-api/services"
	"coachify-account-api/services/mocks"
)

func TestPlanManageLockedChain(t *testing.T) {
	m, s, a := authzFixture()
	m.rows = append(m.rows, member("grantee", db.GymMemberRoleCoach))
	grant(s, "grantee", string(services.CapPlanManage), db.GrantAllow)

	assert.True(t, can(t, a, "owner", services.CapPlanManage).Allowed, "unlocked: owner manages plans")
	assert.True(t, can(t, a, "grantee", services.CapPlanManage).Allowed, "unlocked: grantee manages plans")

	s.locked = map[string]bool{gymA: true}
	for _, user := range []string{"owner", "grantee"} {
		d := can(t, a, user, services.CapPlanManage)
		assert.False(t, d.Allowed, user)
		assert.Equal(t, services.ReasonPlatformLocked, d.Reason, user)
	}
	assert.True(t, can(t, a, "owner", services.CapGymManage).Allowed, "owner keeps other capabilities")
	assert.True(t, can(t, a, "owner", services.CapClientAssign).Allowed, "owner keeps other capabilities")

	caps, _, apiErr := a.Effective(context.Background(), member("owner", db.GymMemberRoleOwner))
	require.Nil(t, apiErr)
	assert.False(t, caps[services.CapPlanManage])
	assert.True(t, caps[services.CapClientAssign])
}

func TestAdminUpdateGymPlanManageLocked(t *testing.T) {
	ctx := context.Background()
	u, g := &mocks.TeamUserStore{}, newFakeGymStore()
	policy := newFakePolicyStore()
	svc := services.NewGymService(u, g, services.GymSettings{InviteTTL: time.Hour, Bump: policy})
	gym := g.addGym("owner1", db.GymStatusActive)
	gid := gym.ID.Hex()
	locked, unlocked := true, false

	v, apiErr := svc.AdminUpdateGym(ctx, "admin1", gid, services.GymAdminUpdate{PlanManageLocked: &locked})
	require.Nil(t, apiErr)
	assert.True(t, v.PlanManageLocked)
	assert.True(t, g.gyms[gid].PlanManageLocked)
	assert.Equal(t, int64(1), policy.versions[gid], "authz version bumped")

	_, apiErr = svc.AdminUpdateGym(ctx, "admin1", gid, services.GymAdminUpdate{PlanManageLocked: &locked})
	require.Nil(t, apiErr)
	assert.Equal(t, int64(1), policy.versions[gid], "no change, no bump")

	v, apiErr = svc.AdminUpdateGym(ctx, "admin1", gid, services.GymAdminUpdate{PlanManageLocked: &unlocked})
	require.Nil(t, apiErr)
	assert.False(t, v.PlanManageLocked)
	assert.Equal(t, int64(2), policy.versions[gid])
}

type fakeDefaultBilling struct {
	calls []string
	errs  int // number of leading calls that fail
}

func (f *fakeDefaultBilling) CancelAtPeriodEnd(context.Context, string, string) error { return nil }

func (f *fakeDefaultBilling) SubscribeDefault(_ context.Context, gymID, clientID, joinRef string) error {
	f.calls = append(f.calls, gymID+"/"+clientID+"/"+joinRef)
	if len(f.calls) <= f.errs {
		return errors.New("payments down")
	}
	return nil
}

func TestGymClientJoinSubscribesDefaultPlan(t *testing.T) {
	ctx := context.Background()
	newSvc := func(b *fakeDefaultBilling) (services.GymClientService, *gymClientFixture) {
		f := newGymClientFixture(t, true)
		f.quota.On("Check", mock.Anything, services.QuotaKindGym, "", gymA).Return(nil)
		svc := services.NewGymClientService(services.GymClientDeps{
			Members: f.members, Store: f.store, Audit: f.audit, Quota: f.quota,
			Billing: b, Spawn: func(fn func()) { fn() },
		}, true)
		return svc, f
	}

	t.Run("join calls SubscribeDefault once with the join ref", func(t *testing.T) {
		b := &fakeDefaultBilling{}
		svc, _ := newSvc(b)
		row, apiErr := svc.Join(ctx, services.JoinGymClientInput{GymID: gymA, ClientID: "cl9", Consent: true})
		require.Nil(t, apiErr)
		assert.Equal(t, []string{gymA + "/cl9/" + strconv.FormatInt(row.JoinedAt.UnixNano(), 10)}, b.calls)
	})

	t.Run("already a member (409) does not call it", func(t *testing.T) {
		b := &fakeDefaultBilling{}
		svc, _ := newSvc(b)
		_, apiErr := svc.Join(ctx, services.JoinGymClientInput{GymID: gymA, ClientID: "cl1", Consent: true})
		requireCode(t, apiErr, 409)
		assert.Empty(t, b.calls)
	})

	t.Run("a billing failure is retried and never fails the join", func(t *testing.T) {
		b := &fakeDefaultBilling{errs: 2}
		svc, _ := newSvc(b)
		row, apiErr := svc.Join(ctx, services.JoinGymClientInput{GymID: gymA, ClientID: "cl9", Consent: true})
		require.Nil(t, apiErr)
		require.NotNil(t, row)
		assert.Len(t, b.calls, 3)
	})

	t.Run("a persistent billing failure still joins", func(t *testing.T) {
		b := &fakeDefaultBilling{errs: 100}
		svc, _ := newSvc(b)
		row, apiErr := svc.Join(ctx, services.JoinGymClientInput{GymID: gymA, ClientID: "cl9", Consent: true})
		require.Nil(t, apiErr)
		require.NotNil(t, row)
		assert.Len(t, b.calls, 3)
	})
}
