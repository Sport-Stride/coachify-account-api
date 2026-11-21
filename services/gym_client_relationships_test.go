package services_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models/db"
	"coachify-account-api/services"
)

type fakeLinks struct {
	linked map[string]bool // coachID|clientID
	err    error
}

func (f fakeLinks) LinkedClientIDs(_ context.Context, coachID string, ids []string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := []string{}
	for _, id := range ids {
		if f.linked[coachID+"|"+id] {
			out = append(out, id)
		}
	}
	return out, nil
}

func newRelFixture(links fakeLinks, store *fakeGymClientStore, enabled bool) services.GymClientService {
	members := &fakeAuthzMembers{rows: []db.GymMember{
		member("owner", db.GymMemberRoleOwner),
		member("head", db.GymMemberRoleHeadCoach),
		member("c1", db.GymMemberRoleCoach),
		member("c2", db.GymMemberRoleCoach),
	}}
	return services.NewGymClientService(services.GymClientDeps{
		Members: members, Store: store, Links: links, Team: fakeSupervision{"head": {"c1"}},
	}, enabled)
}

func relStore() *fakeGymClientStore {
	return &fakeGymClientStore{rows: []*db.GymClient{
		{GymID: gymA, ClientID: "cl1", CoachID: "c1", Status: db.GymClientStatusActive, JoinedAt: time.Now()},
		{GymID: "gymB", ClientID: "cl1", CoachID: "x", Status: db.GymClientStatusActive, JoinedAt: time.Now()},
		{GymID: gymA, ClientID: "pool", CoachID: "", Status: db.GymClientStatusActive, JoinedAt: time.Now()},
	}}
}

func TestRelationships(t *testing.T) {
	ctx := context.Background()
	svc := newRelFixture(fakeLinks{linked: map[string]bool{"c1|cl1": true}}, relStore(), true)

	t.Run("assigned coach, dual client", func(t *testing.T) {
		r, apiErr := svc.Relationships(ctx, "c1", "cl1")
		require.Nil(t, apiErr)
		assert.True(t, r.Personal)
		assert.Equal(t, []services.GymRelation{{GymID: gymA, ActorRole: "coach", AssignedToActor: true}}, r.Gyms)
	})
	t.Run("other coach of the gym sees nothing", func(t *testing.T) {
		r, apiErr := svc.Relationships(ctx, "c2", "cl1")
		require.Nil(t, apiErr)
		assert.False(t, r.Personal)
		assert.Empty(t, r.Gyms)
	})
	t.Run("head coach and owner see any row, not assigned", func(t *testing.T) {
		for role, id := range map[string]string{"head_coach": "head", "owner": "owner"} {
			r, apiErr := svc.Relationships(ctx, id, "cl1")
			require.Nil(t, apiErr)
			assert.Equal(t, []services.GymRelation{{GymID: gymA, ActorRole: role}}, r.Gyms)
		}
	})
	t.Run("pool client is visible to owner only among non-assigned", func(t *testing.T) {
		r, _ := svc.Relationships(ctx, "owner", "pool")
		assert.Len(t, r.Gyms, 1)
		r, _ = svc.Relationships(ctx, "c1", "pool")
		assert.Empty(t, r.Gyms)
	})
	t.Run("non-member gets empty answer", func(t *testing.T) {
		r, apiErr := svc.Relationships(ctx, "stranger", "cl1")
		require.Nil(t, apiErr)
		assert.False(t, r.Personal)
		assert.NotNil(t, r.Gyms)
		assert.Empty(t, r.Gyms)
	})
	t.Run("missing ids are 400", func(t *testing.T) {
		_, apiErr := svc.Relationships(ctx, "", "cl1")
		requireCode(t, apiErr, http.StatusBadRequest)
	})
	t.Run("personal only when flag is off", func(t *testing.T) {
		off := newRelFixture(fakeLinks{linked: map[string]bool{"c1|cl1": true}}, relStore(), false)
		r, apiErr := off.Relationships(ctx, "c1", "cl1")
		require.Nil(t, apiErr)
		assert.True(t, r.Personal)
		assert.Empty(t, r.Gyms)
	})
	t.Run("store failure is 500", func(t *testing.T) {
		bad := newRelFixture(fakeLinks{err: errors.New("boom")}, relStore(), true)
		_, apiErr := bad.Relationships(ctx, "c1", "cl1")
		requireCode(t, apiErr, http.StatusInternalServerError)
	})
}

func TestAccess(t *testing.T) {
	ctx := context.Background()
	svc := newRelFixture(fakeLinks{}, relStore(), true)

	t.Run("staff roles", func(t *testing.T) {
		for role, id := range map[string]string{"owner": "owner", "head_coach": "head", "coach": "c1"} {
			a, apiErr := svc.Access(ctx, id, gymA)
			require.Nil(t, apiErr)
			assert.Equal(t, role, a.StaffRole)
			assert.False(t, a.ClientActive)
		}
	})
	t.Run("client", func(t *testing.T) {
		a, apiErr := svc.Access(ctx, "cl1", gymA)
		require.Nil(t, apiErr)
		assert.Equal(t, services.GymAccess{ClientActive: true}, *a)
	})
	t.Run("none", func(t *testing.T) {
		a, apiErr := svc.Access(ctx, "stranger", gymA)
		require.Nil(t, apiErr)
		assert.Equal(t, services.GymAccess{}, *a)
		a, _ = svc.Access(ctx, "cl1", "other-gym")
		assert.Equal(t, services.GymAccess{}, *a)
	})
	t.Run("bad input is 400", func(t *testing.T) {
		_, apiErr := svc.Access(ctx, "", gymA)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code)
		_, apiErr = svc.Access(ctx, "c1", "")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code)
	})
	t.Run("client check off", func(t *testing.T) {
		a, apiErr := newRelFixture(fakeLinks{}, relStore(), false).Access(ctx, "cl1", gymA)
		require.Nil(t, apiErr)
		assert.False(t, a.ClientActive)
	})
}
