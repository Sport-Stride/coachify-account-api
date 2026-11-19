package services_test

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
	"coachify-account-api/services"
	"coachify-account-api/services/mocks"
)

// fakeGymClientStore is an in-memory services.GymClientStore.
type fakeGymClientStore struct {
	rows      []*db.GymClient
	lastQuery db.GymClientQuery
	failSet   bool // SetCoach reports a lost compare-and-set
	setCalls  int
	archived  map[string]bool
}

func (f *fakeGymClientStore) Insert(_ context.Context, c *db.GymClient) error {
	for _, r := range f.rows {
		if r.GymID == c.GymID && r.ClientID == c.ClientID && r.Status == db.GymClientStatusActive {
			return repositories.ErrGymClientExists
		}
	}
	cp := *c
	f.rows = append(f.rows, &cp)
	return nil
}

func (f *fakeGymClientStore) Get(_ context.Context, gymID, clientID string) (*db.GymClient, error) {
	for _, r := range f.rows {
		if r.GymID == gymID && r.ClientID == clientID && r.Status == db.GymClientStatusActive {
			cp := *r
			return &cp, nil
		}
	}
	return nil, nil
}

func (f *fakeGymClientStore) SetCoach(_ context.Context, gymID, clientID, prev, coachID string, at time.Time) (bool, error) {
	f.setCalls++
	if f.failSet {
		return false, nil
	}
	for _, r := range f.rows {
		if r.GymID == gymID && r.ClientID == clientID && r.Status == db.GymClientStatusActive && r.CoachID == prev {
			r.CoachID = coachID
			if coachID == "" {
				r.AssignedAt = nil
			} else {
				r.AssignedAt = &at
			}
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeGymClientStore) Leave(_ context.Context, gymID, clientID string, at time.Time) (bool, error) {
	for _, r := range f.rows {
		if r.GymID == gymID && r.ClientID == clientID && r.Status == db.GymClientStatusActive {
			r.Status, r.LeftAt = db.GymClientStatusLeft, &at
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeGymClientStore) ListByGym(_ context.Context, _ string, q db.GymClientQuery) ([]db.GymClientRow, int, error) {
	f.lastQuery = q
	return []db.GymClientRow{{ExternalID: "cl1"}}, 1, nil
}

func (f *fakeGymClientStore) ActiveByClient(_ context.Context, clientID string) ([]db.GymClient, error) {
	out := []db.GymClient{}
	for _, r := range f.rows {
		if r.ClientID == clientID && r.Status == db.GymClientStatusActive {
			out = append(out, *r)
		}
	}
	return out, nil
}

// MyGyms: archived gyms are the ones listed in archived; names come from gymNames.
func (f *fakeGymClientStore) MyGyms(_ context.Context, clientID string) ([]db.MyGymRow, error) {
	out := []db.MyGymRow{}
	for _, r := range f.rows {
		if r.ClientID != clientID || r.Status != db.GymClientStatusActive || f.archived[r.GymID] {
			continue
		}
		out = append(out, db.MyGymRow{GymID: r.GymID, GymName: "name-" + r.GymID, CoachID: r.CoachID})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GymID < out[j].GymID })
	return out, nil
}

type fakeClientAudit struct{ entries []db.TeamAuditEntry }

func (f *fakeClientAudit) InsertAudit(_ context.Context, e *db.TeamAuditEntry) error {
	f.entries = append(f.entries, *e)
	return nil
}

type fakeSupervision map[string][]string

func (f fakeSupervision) IDsByHead(_ context.Context, headID string) ([]string, error) {
	return f[headID], nil
}

type gymClientFixture struct {
	svc     services.GymClientService
	store   *fakeGymClientStore
	audit   *fakeClientAudit
	policy  *fakePolicyStore
	quota   *mocks.QuotaGuard
	members *fakeAuthzMembers
}

// newGymClientFixture wires the service over the real chain authorizer: gymA has an owner, a head coach and two
// coaches (c1, c2); client cl1 is a gym client of c1, cl2 sits in the pool, cl3 belongs to another gym.
func newGymClientFixture(t *testing.T, enabled bool) *gymClientFixture {
	t.Helper()
	members := &fakeAuthzMembers{rows: []db.GymMember{
		member("owner", db.GymMemberRoleOwner),
		member("head", db.GymMemberRoleHeadCoach),
		member("c1", db.GymMemberRoleCoach),
		member("c2", db.GymMemberRoleCoach),
	}}
	policy := newFakePolicyStore()
	store := &fakeGymClientStore{rows: []*db.GymClient{
		{GymID: gymA, ClientID: "cl1", CoachID: "c1", Status: db.GymClientStatusActive, JoinedAt: time.Now()},
		{GymID: gymA, ClientID: "cl2", CoachID: "", Status: db.GymClientStatusActive, JoinedAt: time.Now()},
		{GymID: "gymB", ClientID: "cl3", CoachID: "x", Status: db.GymClientStatusActive, JoinedAt: time.Now()},
	}}
	authz := services.NewChainAuthorizer(members, policy, services.NewResourceResolver(services.NewClientScopeSource(store)))
	audit := &fakeClientAudit{}
	quota := &mocks.QuotaGuard{}
	svc := services.NewGymClientService(services.GymClientDeps{
		Authz: authz, Members: members, Store: store, Team: fakeSupervision{"head": {"c1"}}, Audit: audit, Quota: quota,
	}, enabled)
	return &gymClientFixture{svc: svc, store: store, audit: audit, policy: policy, quota: quota, members: members}
}

func requireCode(t *testing.T, apiErr *models.ApiError, code int) {
	t.Helper()
	require.NotNil(t, apiErr)
	assert.Equal(t, code, apiErr.Code, apiErr.Error)
}

func TestGymClientFlagOffAnswers404(t *testing.T) {
	f := newGymClientFixture(t, false)
	ctx := context.Background()
	_, _, apiErr := f.svc.ListClients(ctx, "owner", gymA, services.GymClientListQuery{})
	requireCode(t, apiErr, http.StatusNotFound)
	_, apiErr = f.svc.Assign(ctx, "owner", gymA, "cl2", "c1")
	requireCode(t, apiErr, http.StatusNotFound)
	_, apiErr = f.svc.Unassign(ctx, "owner", gymA, "cl1")
	requireCode(t, apiErr, http.StatusNotFound)
	requireCode(t, f.svc.Remove(ctx, "owner", gymA, "cl1"), http.StatusNotFound)
	_, apiErr = f.svc.Join(ctx, services.JoinGymClientInput{GymID: gymA, ClientID: "cl9", Consent: true})
	requireCode(t, apiErr, http.StatusNotFound)
	assert.Empty(t, f.audit.entries)
	assert.Zero(t, f.store.setCalls)
}

func TestGymClientAuthzMatrix(t *testing.T) {
	ctx := context.Background()
	type op struct {
		name string
		run  func(f *gymClientFixture, caller, client string) *models.ApiError
	}
	ops := []op{
		{"assign", func(f *gymClientFixture, caller, client string) *models.ApiError {
			_, e := f.svc.Assign(ctx, caller, gymA, client, "c2")
			return e
		}},
		{"unassign", func(f *gymClientFixture, caller, client string) *models.ApiError {
			_, e := f.svc.Unassign(ctx, caller, gymA, client)
			return e
		}},
		{"remove", func(f *gymClientFixture, caller, client string) *models.ApiError {
			return f.svc.Remove(ctx, caller, gymA, client)
		}},
	}
	for _, o := range ops {
		t.Run(o.name+" non-member is 404", func(t *testing.T) {
			f := newGymClientFixture(t, true)
			requireCode(t, o.run(f, "stranger", "cl1"), http.StatusNotFound)
		})
		t.Run(o.name+" client of another gym is 404 even for the owner", func(t *testing.T) {
			f := newGymClientFixture(t, true)
			requireCode(t, o.run(f, "owner", "cl3"), http.StatusNotFound)
		})
		t.Run(o.name+" client without gym relationship is 404", func(t *testing.T) {
			f := newGymClientFixture(t, true)
			requireCode(t, o.run(f, "owner", "personal-only"), http.StatusNotFound)
		})
		t.Run(o.name+" owner is allowed", func(t *testing.T) {
			f := newGymClientFixture(t, true)
			assert.Nil(t, o.run(f, "owner", "cl1"))
		})
	}

	t.Run("plain coach is 403 on assign and remove", func(t *testing.T) {
		f := newGymClientFixture(t, true)
		_, apiErr := f.svc.Assign(ctx, "c1", gymA, "cl2", "c2")
		requireCode(t, apiErr, http.StatusForbidden)
		requireCode(t, f.svc.Remove(ctx, "c1", gymA, "cl1"), http.StatusForbidden)
	})

	t.Run("head coach can assign but not remove", func(t *testing.T) {
		f := newGymClientFixture(t, true)
		_, apiErr := f.svc.Assign(ctx, "head", gymA, "cl2", "c1")
		assert.Nil(t, apiErr)
		requireCode(t, f.svc.Remove(ctx, "head", gymA, "cl1"), http.StatusForbidden)
	})

	t.Run("allow grant lets a coach assign, deny grant takes it from the head coach", func(t *testing.T) {
		f := newGymClientFixture(t, true)
		grant(f.policy, "c1", "client.assign", db.GrantAllow)
		_, apiErr := f.svc.Assign(ctx, "c1", gymA, "cl2", "c2")
		assert.Nil(t, apiErr)
		grant(f.policy, "head", "client.assign", db.GrantDeny)
		_, apiErr = f.svc.Assign(ctx, "head", gymA, "cl1", "c2")
		requireCode(t, apiErr, http.StatusForbidden)
	})
}

func TestGymClientAssignSemantics(t *testing.T) {
	ctx := context.Background()

	t.Run("pool to coach is client_assign", func(t *testing.T) {
		f := newGymClientFixture(t, true)
		res, apiErr := f.svc.Assign(ctx, "owner", gymA, "cl2", "c2")
		require.Nil(t, apiErr)
		assert.Equal(t, "c2", res.CoachID)
		assert.Equal(t, "", res.PreviousCoachID)
		require.NotNil(t, res.AssignedAt)
		require.Len(t, f.audit.entries, 1)
		e := f.audit.entries[0]
		assert.Equal(t, db.TeamActionClientAssign, e.Action)
		assert.Equal(t, db.TeamActorRoleGymOwner, e.ActorRole)
		assert.Equal(t, []string{"owner", "c2", "cl2", gymA}, []string{e.ActorID, e.CoachID, e.ClientID, e.GymID})
	})

	t.Run("reassign moves access and is audited with the previous coach", func(t *testing.T) {
		f := newGymClientFixture(t, true)
		res, apiErr := f.svc.Assign(ctx, "owner", gymA, "cl1", "c2")
		require.Nil(t, apiErr)
		assert.Equal(t, "c1", res.PreviousCoachID)
		row, _ := f.store.Get(ctx, gymA, "cl1")
		require.NotNil(t, row)
		assert.Equal(t, "c2", row.CoachID)
		// Relationship stays active: only the coach changed. The service has no chat dependency, so chats stay open.
		assert.Equal(t, db.GymClientStatusActive, row.Status)
		require.Len(t, f.audit.entries, 1)
		assert.Equal(t, db.TeamActionClientReassign, f.audit.entries[0].Action)
		assert.Equal(t, "c1", f.audit.entries[0].PrevCoachID)
	})

	t.Run("same coach again is a no-op", func(t *testing.T) {
		f := newGymClientFixture(t, true)
		res, apiErr := f.svc.Assign(ctx, "owner", gymA, "cl1", "c1")
		require.Nil(t, apiErr)
		assert.Equal(t, "c1", res.CoachID)
		assert.Zero(t, f.store.setCalls)
		assert.Empty(t, f.audit.entries)
	})

	t.Run("target must be a member of the gym", func(t *testing.T) {
		f := newGymClientFixture(t, true)
		_, apiErr := f.svc.Assign(ctx, "owner", gymA, "cl2", "outsider")
		requireCode(t, apiErr, http.StatusBadRequest)
		_, apiErr = f.svc.Assign(ctx, "owner", gymA, "cl2", "")
		requireCode(t, apiErr, http.StatusBadRequest)
		assert.Zero(t, f.store.setCalls)
	})

	t.Run("lost compare-and-set is 409 without audit", func(t *testing.T) {
		f := newGymClientFixture(t, true)
		f.store.failSet = true
		_, apiErr := f.svc.Assign(ctx, "owner", gymA, "cl1", "c2")
		requireCode(t, apiErr, http.StatusConflict)
		assert.Empty(t, f.audit.entries)
	})

	t.Run("unassign returns the client to the pool", func(t *testing.T) {
		f := newGymClientFixture(t, true)
		res, apiErr := f.svc.Unassign(ctx, "owner", gymA, "cl1")
		require.Nil(t, apiErr)
		assert.Equal(t, "", res.CoachID)
		assert.Equal(t, "c1", res.PreviousCoachID)
		assert.Nil(t, res.AssignedAt)
		row, _ := f.store.Get(ctx, gymA, "cl1")
		assert.Equal(t, "", row.CoachID)
		require.Len(t, f.audit.entries, 1)
		assert.Equal(t, db.TeamActionClientUnassign, f.audit.entries[0].Action)

		// Already in the pool: nothing to do.
		_, apiErr = f.svc.Unassign(ctx, "owner", gymA, "cl2")
		require.Nil(t, apiErr)
		assert.Len(t, f.audit.entries, 1)
	})

	t.Run("remove ends the relationship", func(t *testing.T) {
		f := newGymClientFixture(t, true)
		require.Nil(t, f.svc.Remove(ctx, "owner", gymA, "cl1"))
		row, _ := f.store.Get(ctx, gymA, "cl1")
		assert.Nil(t, row)
		require.Len(t, f.audit.entries, 1)
		assert.Equal(t, db.TeamActionClientLeaveGym, f.audit.entries[0].Action)
		requireCode(t, f.svc.Remove(ctx, "owner", gymA, "cl1"), http.StatusNotFound)
	})
}

func TestGymClientList(t *testing.T) {
	ctx := context.Background()

	t.Run("non-member is 404", func(t *testing.T) {
		f := newGymClientFixture(t, true)
		_, _, apiErr := f.svc.ListClients(ctx, "stranger", gymA, services.GymClientListQuery{})
		requireCode(t, apiErr, http.StatusNotFound)
	})

	t.Run("owner sees every gym client, pool included", func(t *testing.T) {
		f := newGymClientFixture(t, true)
		_, _, apiErr := f.svc.ListClients(ctx, "owner", gymA, services.GymClientListQuery{})
		require.Nil(t, apiErr)
		assert.Nil(t, f.store.lastQuery.CoachIDs)
	})

	t.Run("without view_all a caller is limited to himself and his supervisees", func(t *testing.T) {
		f := newGymClientFixture(t, true)
		grant(f.policy, "head", "client.view_all", db.GrantDeny)
		_, _, apiErr := f.svc.ListClients(ctx, "head", gymA, services.GymClientListQuery{})
		require.Nil(t, apiErr)
		assert.Equal(t, []string{"head", "c1"}, f.store.lastQuery.CoachIDs)
	})

	t.Run("filters are forwarded and search and size are clamped", func(t *testing.T) {
		f := newGymClientFixture(t, true)
		pool := false
		_, _, apiErr := f.svc.ListClients(ctx, "owner", gymA, services.GymClientListQuery{
			Assigned: &pool, CoachID: "c1", Q: strings.Repeat("x", 500), Size: 5000, Page: 3,
		})
		require.Nil(t, apiErr)
		q := f.store.lastQuery
		assert.Equal(t, &pool, q.Assigned)
		assert.Equal(t, "c1", q.CoachID)
		assert.LessOrEqual(t, len([]rune(q.Q)), 64)
		assert.Equal(t, 20, q.Size)
		assert.Equal(t, 3, q.Page)
	})
}

func TestGymClientJoin(t *testing.T) {
	ctx := context.Background()

	t.Run("consent is required", func(t *testing.T) {
		f := newGymClientFixture(t, true)
		_, apiErr := f.svc.Join(ctx, services.JoinGymClientInput{GymID: gymA, ClientID: "cl9"})
		requireCode(t, apiErr, http.StatusBadRequest)
		f.quota.AssertNotCalled(t, "Check", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("dual client joins on consent and only the gym quota is consulted", func(t *testing.T) {
		f := newGymClientFixture(t, true)
		f.quota.On("Check", mock.Anything, services.QuotaKindGym, "c1", gymA).Return(nil)
		row, apiErr := f.svc.Join(ctx, services.JoinGymClientInput{GymID: gymA, ClientID: "cl9", CoachID: "c1", InviteID: "inv", Consent: true})
		require.Nil(t, apiErr)
		assert.Equal(t, "c1", row.CoachID)
		assert.NotNil(t, row.AssignedAt)
		assert.False(t, row.ConsentAt.IsZero())
		f.quota.AssertNumberOfCalls(t, "Check", 1)
		require.Len(t, f.audit.entries, 1)
		assert.Equal(t, db.TeamActionClientJoinGym, f.audit.entries[0].Action)
		assert.Equal(t, db.TeamActorRoleClient, f.audit.entries[0].ActorRole)
	})

	t.Run("pool join has no coach", func(t *testing.T) {
		f := newGymClientFixture(t, true)
		f.quota.On("Check", mock.Anything, services.QuotaKindGym, "", gymA).Return(nil)
		row, apiErr := f.svc.Join(ctx, services.JoinGymClientInput{GymID: gymA, ClientID: "cl9", Consent: true})
		require.Nil(t, apiErr)
		assert.Equal(t, "", row.CoachID)
		assert.Nil(t, row.AssignedAt)
	})

	t.Run("quota blocks the join", func(t *testing.T) {
		f := newGymClientFixture(t, true)
		f.quota.On("Check", mock.Anything, services.QuotaKindGym, "", gymA).
			Return(&models.ApiError{Code: http.StatusForbidden, Error: errors.New("quota")})
		_, apiErr := f.svc.Join(ctx, services.JoinGymClientInput{GymID: gymA, ClientID: "cl9", Consent: true})
		requireCode(t, apiErr, http.StatusForbidden)
		assert.Len(t, f.store.rows, 3)
	})

	t.Run("duplicate is 409 and a foreign coach is 400", func(t *testing.T) {
		f := newGymClientFixture(t, true)
		f.quota.On("Check", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		_, apiErr := f.svc.Join(ctx, services.JoinGymClientInput{GymID: gymA, ClientID: "cl1", Consent: true})
		requireCode(t, apiErr, http.StatusConflict)
		_, apiErr = f.svc.Join(ctx, services.JoinGymClientInput{GymID: gymA, ClientID: "cl9", CoachID: "outsider", Consent: true})
		requireCode(t, apiErr, http.StatusBadRequest)
	})
}

func TestClientScopeSourceReadsGymClientsOnly(t *testing.T) {
	f := newGymClientFixture(t, true)
	scope, err := services.NewClientScopeSource(f.store).ClientScope(context.Background(), "cl1")
	require.NoError(t, err)
	assert.Equal(t, "", scope.PersonalCoachID, "a personal link never feeds a gym-derived grant")
	require.Len(t, scope.Gyms, 1)
	assert.Equal(t, services.ScopeGym{GymID: gymA, CoachID: "c1"}, scope.Gyms[0])
}

// flakyMembers reports the target as an active coach on the first lookup and as gone on the next ones.
type flakyMembers struct {
	*fakeAuthzMembers
	target string
	calls  int
}

func (f *flakyMembers) MembershipOf(ctx context.Context, userID, gymID string) (*db.GymMember, error) {
	if userID != f.target {
		return f.fakeAuthzMembers.MembershipOf(ctx, userID, gymID)
	}
	f.calls++
	if f.calls > 1 {
		return nil, nil
	}
	return f.fakeAuthzMembers.MembershipOf(ctx, userID, gymID)
}

func TestGymClientAssignTargetEligibility(t *testing.T) {
	ctx := context.Background()

	t.Run("owner may be assigned clients", func(t *testing.T) {
		f := newGymClientFixture(t, true)
		res, apiErr := f.svc.Assign(ctx, "owner", gymA, "cl2", "owner")
		require.Nil(t, apiErr)
		assert.Equal(t, "owner", res.CoachID)
	})

	t.Run("inactive member and other roles are refused", func(t *testing.T) {
		f := newGymClientFixture(t, true)
		left := member("gone", db.GymMemberRoleCoach)
		left.Status = db.GymMemberStatusLeft
		f.members.rows = append(f.members.rows, left, member("nutri", db.GymMemberRole("nutritionist")))
		for _, target := range []string{"gone", "nutri"} {
			_, apiErr := f.svc.Assign(ctx, "owner", gymA, "cl2", target)
			requireCode(t, apiErr, http.StatusBadRequest)
		}
		assert.Zero(t, f.store.setCalls)
	})

	t.Run("target leaving during the write is compensated", func(t *testing.T) {
		f := newGymClientFixture(t, true)
		flaky := &flakyMembers{fakeAuthzMembers: f.members, target: "c2"}
		authz := services.NewChainAuthorizer(f.members, f.policy, services.NewResourceResolver(services.NewClientScopeSource(f.store)))
		svc := services.NewGymClientService(services.GymClientDeps{
			Authz: authz, Members: flaky, Store: f.store, Team: fakeSupervision{}, Audit: f.audit, Quota: f.quota,
		}, true)
		_, apiErr := svc.Assign(ctx, "owner", gymA, "cl1", "c2")
		requireCode(t, apiErr, http.StatusConflict)
		row, _ := f.store.Get(ctx, gymA, "cl1")
		assert.Equal(t, "c1", row.CoachID, "put back to the previous coach")
		assert.Empty(t, f.audit.entries)

		// Pool client: compensation returns it to the pool.
		flaky.calls = 0
		_, apiErr = svc.Assign(ctx, "owner", gymA, "cl2", "c2")
		requireCode(t, apiErr, http.StatusConflict)
		row, _ = f.store.Get(ctx, gymA, "cl2")
		assert.Equal(t, "", row.CoachID)
		assert.Nil(t, row.AssignedAt)
	})

	t.Run("join refuses an ineligible coach", func(t *testing.T) {
		f := newGymClientFixture(t, true)
		f.members.rows = append(f.members.rows, member("nutri", db.GymMemberRole("nutritionist")))
		_, apiErr := f.svc.Join(ctx, services.JoinGymClientInput{GymID: gymA, ClientID: "cl9", CoachID: "nutri", Consent: true})
		requireCode(t, apiErr, http.StatusBadRequest)
	})
}
