package services_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/pkg/invitation"
	"coachify-account-api/repositories"
	"coachify-account-api/services"
	"coachify-account-api/services/mocks"
	"coachify-account-api/utils"
)

// ---------- fakes ----------

type fakeInviteStore struct {
	mu    sync.Mutex
	rows  map[string]*db.InviteLink // by hex id
	order []string
}

func newFakeInviteStore() *fakeInviteStore {
	return &fakeInviteStore{rows: map[string]*db.InviteLink{}}
}

func (f *fakeInviteStore) add(l *db.InviteLink) *db.InviteLink {
	if l.ID.IsZero() {
		l.ID = primitive.NewObjectID()
	}
	if l.Status == "" {
		l.Status = db.InviteStatusActive
	}
	f.rows[l.ID.Hex()] = l
	f.order = append(f.order, l.ID.Hex())
	return l
}

func (f *fakeInviteStore) Insert(_ context.Context, l *db.InviteLink) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.add(l)
	return nil
}

func (f *fakeInviteStore) GetByHash(_ context.Context, hash string) (*db.InviteLink, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, l := range f.rows {
		if l.TokenHash == hash {
			c := *l
			return &c, nil
		}
	}
	return nil, nil
}

func (f *fakeInviteStore) GetByID(_ context.Context, id string) (*db.InviteLink, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if l, ok := f.rows[id]; ok {
		c := *l
		return &c, nil
	}
	return nil, nil
}

func (f *fakeInviteStore) List(_ context.Context, flt repositories.InviteLinkFilter, _ int64) ([]db.InviteLink, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []db.InviteLink{}
	for _, id := range f.order {
		l := f.rows[id]
		if (flt.CreatorID == "" || l.CreatorID == flt.CreatorID) && (flt.GymID == "" || l.GymID == flt.GymID) &&
			(flt.Status == "" || l.Status == flt.Status) {
			out = append(out, *l)
		}
	}
	return out, nil
}

func (f *fakeInviteStore) ListPendingByEmail(_ context.Context, email, kind string, now time.Time) ([]db.InviteLink, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []db.InviteLink{}
	for _, id := range f.order {
		l := f.rows[id]
		if l.Email == email && l.Kind == kind && l.Status == db.InviteStatusActive && (l.ExpiresAt == nil || l.ExpiresAt.After(now)) {
			out = append(out, *l)
		}
	}
	return out, nil
}

func (f *fakeInviteStore) SetStatus(_ context.Context, id, from, to string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	l, ok := f.rows[id]
	if !ok || l.Status != from {
		return false, nil
	}
	l.Status = to
	return true, nil
}

func (f *fakeInviteStore) ConsumeUse(_ context.Context, id string, now time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	l, ok := f.rows[id]
	if !ok || l.Status != db.InviteStatusActive || (l.ExpiresAt != nil && !l.ExpiresAt.After(now)) ||
		(l.MaxUses > 0 && l.Uses >= l.MaxUses) {
		return false, nil
	}
	l.Uses++
	if l.MaxUses > 0 && l.Uses >= l.MaxUses {
		l.Status = db.InviteStatusExhausted
	}
	return true, nil
}

func (f *fakeInviteStore) ReleaseUse(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if l, ok := f.rows[id]; ok {
		if l.Uses > 0 {
			l.Uses--
		}
		if l.Status == db.InviteStatusExhausted {
			l.Status = db.InviteStatusActive
		}
	}
	return nil
}

type fakeRegLinks map[string]*db.RegistrationLink

func (f fakeRegLinks) GetByToken(_ context.Context, token string) (*db.RegistrationLink, error) {
	return f[token], nil
}

type fakeInviteUsers map[string]*db.User

func (f fakeInviteUsers) GetUserByExternalIdUpdate(_ context.Context, id string) (*db.User, *models.ApiError) {
	if u, ok := f[id]; ok {
		return u, nil
	}
	return nil, &models.ApiError{Code: http.StatusNotFound, Error: models.ErrUserNotFound}
}

func (f fakeInviteUsers) GetUserNameByExternalID(_ context.Context, id string) (string, *models.ApiError) {
	return "Coach " + id, nil
}

type fakeInviteGyms map[string]*db.Gym

func (f fakeInviteGyms) GetGym(_ context.Context, id string) (*db.Gym, error) { return f[id], nil }

// fakeCoachAdder records personal coach_clients writes; rows is the pre-existing coach_clients content.
type fakeCoachAdder struct {
	rows  map[string]string // client -> coach
	calls int
	err   error
}

func (f *fakeCoachAdder) AddCoachClient(_ context.Context, coachID, clientID string) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	if _, ok := f.rows[clientID]; ok {
		return errors.New("duplicate")
	}
	f.rows[clientID] = coachID
	return nil
}

type fakeEnt struct{ err *models.ApiError }

func (f fakeEnt) CheckInvitations(context.Context, string) *models.ApiError { return f.err }

type recObserver struct {
	name   string
	err    error
	events []services.InviteRedeemed
}

func (r *recObserver) Name() string { return r.name }
func (r *recObserver) OnRedeemed(_ context.Context, e services.InviteRedeemed) error {
	r.events = append(r.events, e)
	return r.err
}

type fakeMemberJoiner struct {
	signupTokens, acceptTokens []string
	lookup                     *services.JoinLookup
	err                        *models.ApiError
}

func (f *fakeMemberJoiner) LookupJoin(context.Context, string) (*services.JoinLookup, *models.ApiError) {
	if f.lookup == nil {
		return nil, &models.ApiError{Code: http.StatusNotFound, Error: models.ErrInviteNotFound}
	}
	return f.lookup, nil
}
func (f *fakeMemberJoiner) JoinOnSignup(_ context.Context, _, token string) *models.ApiError {
	f.signupTokens = append(f.signupTokens, token)
	return f.err
}
func (f *fakeMemberJoiner) AcceptByToken(_ context.Context, _, token string) (*services.GymJoined, *models.ApiError) {
	f.acceptTokens = append(f.acceptTokens, token)
	return &services.GymJoined{GymID: gymA, Name: "G"}, f.err
}

type inviteFixture struct {
	svc     services.InviteLinkService
	store   *fakeInviteStore
	adder   *fakeCoachAdder
	gym     *gymClientFixture
	obs     *recObserver
	async   *recObserver
	observe *services.InviteObservers
	joiner  *fakeMemberJoiner
	regs    fakeRegLinks
	users   fakeInviteUsers
	ent     *fakeEnt
}

// newInviteFixture wires the real client_gym kind over the real GymClientService (gym_clients store fake) and the
// personal kind over an in-memory coach_clients. Clients: cl1 and cl2 (client role), cl9 has a personal coach row.
func newInviteFixture(t *testing.T, enabled bool) *inviteFixture {
	t.Helper()
	gym := newGymClientFixture(t, true)
	gym.quota.On("Check", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	authz := services.NewChainAuthorizer(gym.members, gym.policy, nil)
	f := &inviteFixture{
		store: newFakeInviteStore(), adder: &fakeCoachAdder{rows: map[string]string{"cl9": "personalCoach"}}, gym: gym,
		obs: &recObserver{name: "sync"}, async: &recObserver{name: "async"}, joiner: &fakeMemberJoiner{},
		regs: fakeRegLinks{}, ent: &fakeEnt{},
		users: fakeInviteUsers{
			"n1":  {ExternalID: "n1", UserEmail: "n1@x.tn", UserRole: "client", UserStatus: db.Active},
			"n2":  {ExternalID: "n2", UserEmail: "n2@x.tn", UserRole: "client", UserStatus: db.Active},
			"cl9": {ExternalID: "cl9", UserEmail: "cl9@x.tn", UserRole: "client", UserStatus: db.Active},
			"c1":  {ExternalID: "c1", UserEmail: "c1@x.tn", UserRole: "coach"},
		},
	}
	f.observe = services.NewInviteObservers([]services.RedeemObserver{f.obs}, []services.RedeemObserver{f.async})
	gyms := fakeInviteGyms{gymA: {Name: "Gym A", Status: db.GymStatusActive}}
	kinds := []services.InvitationKind{
		services.NewClientPersonalKind(f.ent, f.adder),
		services.NewClientGymKind(authz, gym.members, gyms, gym.svc, gym.store, true),
		services.NewLegacyEmailKind(nil),
	}
	kinds = append(kinds, services.NewMemberKinds(f.joiner, nil, nil, nil)...)
	f.svc = services.NewInviteLinkService(services.InviteLinkDeps{
		Store: f.store, RegLinks: f.regs, Users: f.users, Gyms: gyms, Members: gym.members, Authz: authz,
		Joiner: f.joiner, Registry: services.NewInviteKindRegistry(kinds...), Observers: f.observe,
		ClientURL: "https://app.test",
	}, enabled)
	return f
}

// seedLink stores a link for a raw token and returns it.
func (f *inviteFixture) seedLink(token string, l db.InviteLink) *db.InviteLink {
	l.TokenHash = utils.HashToken(token)
	return f.store.add(&l)
}

func future() *time.Time { t := time.Now().Add(time.Hour); return &t }
func past() *time.Time   { t := time.Now().Add(-time.Hour); return &t }

// ---------- lookup, legacy adapters ----------

func TestInviteLookupStates(t *testing.T) {
	ctx := context.Background()
	f := newInviteFixture(t, true)
	f.seedLink("ok", db.InviteLink{Kind: db.InviteKindClientGym, GymID: gymA, TargetCoachID: "c1", ExpiresAt: future()})
	f.seedLink("expired", db.InviteLink{Kind: db.InviteKindClientGym, GymID: gymA, ExpiresAt: past()})
	f.seedLink("used", db.InviteLink{Kind: db.InviteKindClientGym, GymID: gymA, MaxUses: 1, Uses: 1, Status: db.InviteStatusExhausted})
	f.seedLink("revoked", db.InviteLink{Kind: db.InviteKindClientGym, GymID: gymA, Status: db.InviteStatusRevoked})

	res, apiErr := f.svc.Lookup(ctx, "ok")
	require.Nil(t, apiErr)
	assert.Equal(t, db.InviteKindClientGym, res.Kind)
	assert.Equal(t, "Gym A", res.GymName)
	assert.Equal(t, "Coach c1", res.CoachName)
	assert.False(t, res.EmailBound)

	for _, tok := range []string{"expired", "used", "revoked", "unknown", ""} {
		_, apiErr := f.svc.Lookup(ctx, tok)
		requireCode(t, apiErr, http.StatusNotFound)
	}
}

func TestInviteLegacyLookupHashThenRaw(t *testing.T) {
	ctx := context.Background()
	f := newInviteFixture(t, true)
	f.regs["rawtoken"] = &db.RegistrationLink{ID: primitive.NewObjectID(), Token: "rawtoken", CoachID: "c1"}

	// No hashed row: the raw legacy token still resolves.
	res, apiErr := f.svc.Lookup(ctx, "rawtoken")
	require.Nil(t, apiErr)
	assert.Equal(t, db.InviteKindClientPersonal, res.Kind)
	assert.Equal(t, "Coach c1", res.CoachName)

	// A hashed row wins over the legacy raw row: a revoked hashed link is not resurrected by the old collection.
	f.seedLink("rawtoken", db.InviteLink{Kind: db.InviteKindClientPersonal, CreatorID: "c1", Status: db.InviteStatusRevoked})
	_, apiErr = f.svc.Lookup(ctx, "rawtoken")
	requireCode(t, apiErr, http.StatusNotFound)
}

func TestInviteLegacyGymInvitationViaSeam(t *testing.T) {
	ctx := context.Background()
	f := newInviteFixture(t, true)
	f.joiner.lookup = &services.JoinLookup{GymName: "Gym A", Role: db.GymInviteRoleCoach}
	res, apiErr := f.svc.Lookup(ctx, "joinTok")
	require.Nil(t, apiErr)
	assert.Equal(t, db.InviteKindMemberCoach, res.Kind)
	assert.Equal(t, "Gym A", res.GymName)

	f.joiner.lookup.Role = ""
	res, apiErr = f.svc.Lookup(ctx, "joinTok")
	require.Nil(t, apiErr)
	assert.Equal(t, db.InviteKindMemberHeadCoach, res.Kind)

	// Redeem by a signed-in coach goes through AcceptByToken with the raw token.
	out, apiErr := f.svc.Redeem(ctx, "c1", "joinTok")
	require.Nil(t, apiErr)
	assert.Equal(t, services.RedeemJoined, out.Status)
	assert.Equal(t, []string{"joinTok"}, f.joiner.acceptTokens)
}

func TestInviteFlagOffAnswers404(t *testing.T) {
	ctx := context.Background()
	f := newInviteFixture(t, false)
	f.seedLink("t", db.InviteLink{Kind: db.InviteKindClientGym, GymID: gymA})
	_, apiErr := f.svc.Create(ctx, services.Actor{ID: "owner", Role: db.RoleGymOwner}, services.CreateInvite{Kind: db.InviteKindClientGym, GymID: gymA})
	requireCode(t, apiErr, http.StatusNotFound)
	_, apiErr = f.svc.Lookup(ctx, "t")
	requireCode(t, apiErr, http.StatusNotFound)
	_, apiErr = f.svc.Redeem(ctx, "n1", "t")
	requireCode(t, apiErr, http.StatusNotFound)
	_, apiErr = f.svc.ListMine(ctx, "n1")
	requireCode(t, apiErr, http.StatusNotFound)
	_, apiErr = f.svc.PrepareSignup(ctx, "t", "a@x.tn")
	requireCode(t, apiErr, http.StatusNotFound)
	requireCode(t, f.svc.Revoke(ctx, "owner", "x"), http.StatusNotFound)
}

// ---------- create / authorize ----------

func TestInviteCreateAuthorizeAndScope(t *testing.T) {
	ctx := context.Background()
	f := newInviteFixture(t, true)
	gymReq := services.CreateInvite{Kind: db.InviteKindClientGym, GymID: gymA}

	t.Run("coach member links to himself", func(t *testing.T) {
		res, apiErr := f.svc.Create(ctx, services.Actor{ID: "c1", Role: "coach"}, gymReq)
		require.Nil(t, apiErr)
		assert.True(t, strings.HasPrefix(res.Link, "https://app.test/invite/"))
		row, _ := f.store.GetByID(ctx, res.ID)
		assert.Equal(t, "c1", row.TargetCoachID)
		assert.Equal(t, gymA, row.GymID)
		raw := strings.TrimPrefix(res.Link, "https://app.test/invite/")
		assert.Equal(t, utils.HashToken(raw), row.TokenHash)
		assert.NotContains(t, row.TokenHash, raw, "only the hash is stored")
	})
	t.Run("owner and head create pool links", func(t *testing.T) {
		for _, id := range []string{"owner", "head"} {
			role := "coach"
			if id == "owner" {
				role = db.RoleGymOwner
			}
			res, apiErr := f.svc.Create(ctx, services.Actor{ID: id, Role: role}, gymReq)
			require.Nil(t, apiErr, id)
			row, _ := f.store.GetByID(ctx, res.ID)
			assert.Empty(t, row.TargetCoachID)
		}
	})
	t.Run("non member is 404, gym required is 400, unknown kind is 400", func(t *testing.T) {
		_, apiErr := f.svc.Create(ctx, services.Actor{ID: "stranger", Role: "coach"}, gymReq)
		requireCode(t, apiErr, http.StatusNotFound)
		_, apiErr = f.svc.Create(ctx, services.Actor{ID: "owner", Role: db.RoleGymOwner}, services.CreateInvite{Kind: db.InviteKindClientGym})
		requireCode(t, apiErr, http.StatusBadRequest)
		_, apiErr = f.svc.Create(ctx, services.Actor{ID: "owner", Role: db.RoleGymOwner}, services.CreateInvite{Kind: "nope"})
		requireCode(t, apiErr, http.StatusBadRequest)
		_, apiErr = f.svc.Create(ctx, services.Actor{ID: "owner", Role: db.RoleGymOwner}, services.CreateInvite{Kind: "legacy_email"})
		requireCode(t, apiErr, http.StatusBadRequest)
	})
	t.Run("head without member.invite is 403", func(t *testing.T) {
		g := newInviteFixture(t, true)
		g.gym.policy.policies[gymA+"|"+string(db.GymMemberRoleHeadCoach)] = &db.GymRolePolicy{
			GymID: gymA, Role: db.GymMemberRoleHeadCoach, Capabilities: map[string]bool{db.CapMemberInvite: false},
		}
		_, apiErr := g.svc.Create(ctx, services.Actor{ID: "head", Role: "coach"}, gymReq)
		requireCode(t, apiErr, http.StatusForbidden)
	})
	t.Run("email binding forces a single use", func(t *testing.T) {
		res, apiErr := f.svc.Create(ctx, services.Actor{ID: "owner", Role: db.RoleGymOwner}, services.CreateInvite{Kind: db.InviteKindClientGym, GymID: gymA, Email: " N1@x.tn ", MaxUses: 50})
		require.Nil(t, apiErr)
		row, _ := f.store.GetByID(ctx, res.ID)
		assert.Equal(t, "n1@x.tn", row.Email)
		assert.Equal(t, 1, row.MaxUses)
	})
	t.Run("personal needs a coaching role and the entitlement", func(t *testing.T) {
		_, apiErr := f.svc.Create(ctx, services.Actor{ID: "n1", Role: "client"}, services.CreateInvite{Kind: db.InviteKindClientPersonal})
		requireCode(t, apiErr, http.StatusForbidden)
		res, apiErr := f.svc.Create(ctx, services.Actor{ID: "c1", Role: "coach"}, services.CreateInvite{Kind: db.InviteKindClientPersonal})
		require.Nil(t, apiErr)
		row, _ := f.store.GetByID(ctx, res.ID)
		assert.Equal(t, "c1", row.CreatorID)
		assert.Empty(t, row.GymID)

		f.ent.err = &models.ApiError{Code: http.StatusForbidden, Error: errors.New("plan")}
		_, apiErr = f.svc.Create(ctx, services.Actor{ID: "c1", Role: "coach"}, services.CreateInvite{Kind: db.InviteKindClientPersonal})
		requireCode(t, apiErr, http.StatusForbidden)
		f.ent.err = nil
	})
}

func TestInviteListAndRevoke(t *testing.T) {
	ctx := context.Background()
	f := newInviteFixture(t, true)
	f.seedLink("a", db.InviteLink{Kind: db.InviteKindClientGym, GymID: gymA, CreatorID: "c1", TargetCoachID: "c1"})
	b := f.seedLink("b", db.InviteLink{Kind: db.InviteKindClientGym, GymID: gymA, CreatorID: "owner"})

	rows, apiErr := f.svc.List(ctx, "c1", gymA, "")
	require.Nil(t, apiErr)
	assert.Len(t, rows, 1, "a coach sees only his own links")
	rows, apiErr = f.svc.List(ctx, "owner", gymA, "")
	require.Nil(t, apiErr)
	assert.Len(t, rows, 2, "member.invite sees the whole gym")
	_, apiErr = f.svc.List(ctx, "owner", gymA, "bogus")
	requireCode(t, apiErr, http.StatusBadRequest)
	_, apiErr = f.svc.List(ctx, "stranger", gymA, "")
	requireCode(t, apiErr, http.StatusNotFound)

	requireCode(t, f.svc.Revoke(ctx, "c1", b.ID.Hex()), http.StatusNotFound) // coach cannot revoke the owner's link
	require.Nil(t, f.svc.Revoke(ctx, "owner", b.ID.Hex()))
	requireCode(t, f.svc.Revoke(ctx, "owner", b.ID.Hex()), http.StatusConflict)
	_, apiErr = f.svc.Lookup(ctx, "b")
	requireCode(t, apiErr, http.StatusNotFound)
}

// ---------- redeem ----------

func TestRedeemClientGymAutoAssignAndKeepsPersonalRow(t *testing.T) {
	ctx := context.Background()
	f := newInviteFixture(t, true)
	f.seedLink("coachLink", db.InviteLink{Kind: db.InviteKindClientGym, GymID: gymA, CreatorID: "c1", TargetCoachID: "c1"})
	f.seedLink("poolLink", db.InviteLink{Kind: db.InviteKindClientGym, GymID: gymA, CreatorID: "owner"})

	// cl9 already has a personal coach_clients row.
	res, apiErr := f.svc.Redeem(ctx, "cl9", "coachLink")
	require.Nil(t, apiErr)
	assert.Equal(t, services.RedeemJoined, res.Status)
	assert.Equal(t, "c1", res.CoachID)
	row, _ := f.gym.store.Get(ctx, gymA, "cl9")
	require.NotNil(t, row)
	assert.Equal(t, "c1", row.CoachID)
	assert.False(t, row.ConsentAt.IsZero(), "consent recorded")
	assert.Equal(t, 0, f.adder.calls, "coach_clients never touched")
	assert.Equal(t, "personalCoach", f.adder.rows["cl9"], "personal relationship survives")

	// An owner link puts the client in the pool.
	res, apiErr = f.svc.Redeem(ctx, "n2", "poolLink")
	require.Nil(t, apiErr)
	assert.Equal(t, services.RedeemJoined, res.Status)
	row, _ = f.gym.store.Get(ctx, gymA, "n2")
	assert.Empty(t, row.CoachID)
	assert.Len(t, f.obs.events, 2, "sync observer saw both")
}

func TestRedeemIdempotentWhenAlreadyMember(t *testing.T) {
	ctx := context.Background()
	f := newInviteFixture(t, true)
	l := f.seedLink("t", db.InviteLink{Kind: db.InviteKindClientGym, GymID: gymA, CreatorID: "owner", MaxUses: 5})

	_, apiErr := f.svc.Redeem(ctx, "n1", "t")
	require.Nil(t, apiErr)
	res, apiErr := f.svc.Redeem(ctx, "n1", "t")
	require.Nil(t, apiErr)
	assert.Equal(t, services.RedeemAlreadyMember, res.Status)
	assert.Equal(t, 1, f.store.rows[l.ID.Hex()].Uses, "the second call does not burn a use")
	assert.Len(t, f.obs.events, 1, "observers run once")
}

func TestRedeemUsedAndExpiredAre404(t *testing.T) {
	ctx := context.Background()
	f := newInviteFixture(t, true)
	f.seedLink("single", db.InviteLink{Kind: db.InviteKindClientGym, GymID: gymA, CreatorID: "owner", MaxUses: 1})
	f.seedLink("old", db.InviteLink{Kind: db.InviteKindClientGym, GymID: gymA, CreatorID: "owner", ExpiresAt: past()})

	_, apiErr := f.svc.Redeem(ctx, "n1", "single")
	require.Nil(t, apiErr)
	_, apiErr = f.svc.Redeem(ctx, "n2", "single")
	requireCode(t, apiErr, http.StatusNotFound)
	_, apiErr = f.svc.Redeem(ctx, "n2", "old")
	requireCode(t, apiErr, http.StatusNotFound)
}

func TestRedeemEmailMismatchIs403(t *testing.T) {
	ctx := context.Background()
	f := newInviteFixture(t, true)
	l := f.seedLink("t", db.InviteLink{Kind: db.InviteKindClientGym, GymID: gymA, CreatorID: "owner", Email: "n1@x.tn", MaxUses: 1})
	_, apiErr := f.svc.Redeem(ctx, "n2", "t")
	requireCode(t, apiErr, http.StatusForbidden)
	assert.Equal(t, 0, f.store.rows[l.ID.Hex()].Uses)

	_, apiErr = f.svc.PrepareSignup(ctx, "t", "other@x.tn")
	requireCode(t, apiErr, http.StatusForbidden)
}

func TestEmailBoundGymInviteNeedsExplicitAccept(t *testing.T) {
	ctx := context.Background()
	f := newInviteFixture(t, true)
	l := f.seedLink("t", db.InviteLink{Kind: db.InviteKindClientGym, GymID: gymA, CreatorID: "owner", Email: "n1@x.tn", MaxUses: 1, ExpiresAt: future()})
	id := l.ID.Hex()

	res, apiErr := f.svc.Redeem(ctx, "n1", "t")
	require.Nil(t, apiErr)
	assert.Equal(t, services.RedeemPendingAcceptance, res.Status)
	row, _ := f.gym.store.Get(ctx, gymA, "n1")
	assert.Nil(t, row, "never auto-redeemed")
	assert.Equal(t, 0, f.store.rows[id].Uses)

	mine, apiErr := f.svc.ListMine(ctx, "n1")
	require.Nil(t, apiErr)
	require.Len(t, mine, 1)
	assert.Equal(t, id, mine[0].ID)
	assert.Equal(t, "Gym A", mine[0].GymName)
	other, _ := f.svc.ListMine(ctx, "n2")
	assert.Empty(t, other)

	_, apiErr = f.svc.Accept(ctx, "n2", id)
	requireCode(t, apiErr, http.StatusNotFound)
	acc, apiErr := f.svc.Accept(ctx, "n1", id)
	require.Nil(t, apiErr)
	assert.Equal(t, services.RedeemJoined, acc.Status)
	row, _ = f.gym.store.Get(ctx, gymA, "n1")
	require.NotNil(t, row)
	assert.Equal(t, db.InviteStatusExhausted, f.store.rows[id].Status)

	// Decline closes the invite without a membership.
	d := f.seedLink("d", db.InviteLink{Kind: db.InviteKindClientGym, GymID: gymA, CreatorID: "owner", Email: "n2@x.tn", MaxUses: 1})
	require.Nil(t, f.svc.Decline(ctx, "n2", d.ID.Hex()))
	assert.Equal(t, db.InviteStatusDeclined, f.store.rows[d.ID.Hex()].Status)
	_, apiErr = f.svc.Accept(ctx, "n2", d.ID.Hex())
	requireCode(t, apiErr, http.StatusNotFound)
}

func TestSignupWithEmailBoundInviteStaysPendingUntilConfirmed(t *testing.T) {
	ctx := context.Background()
	f := newInviteFixture(t, true)
	l0 := f.seedLink("t", db.InviteLink{Kind: db.InviteKindClientGym, GymID: gymA, CreatorID: "c1", TargetCoachID: "c1", Email: "new@x.tn", MaxUses: 1, ExpiresAt: future()})
	l, apiErr := f.svc.PrepareSignup(ctx, "t", "NEW@x.tn")
	require.Nil(t, apiErr)
	assert.Equal(t, 1, f.store.rows[l0.ID.Hex()].Uses, "use reserved during signup")
	u := &db.User{ExternalID: "new1", UserEmail: "new@x.tn", UserRole: "client", UserStatus: db.ToConfirm}
	f.users["new1"] = u
	require.Nil(t, f.svc.CompleteSignup(ctx, l, u))

	row, _ := f.gym.store.Get(ctx, gymA, "new1")
	assert.Nil(t, row, "no gym_clients write, no consent at signup")
	assert.Equal(t, 0, f.store.rows[l0.ID.Hex()].Uses, "reservation released, invite still pending")
	assert.Equal(t, db.InviteStatusActive, f.store.rows[l0.ID.Hex()].Status)

	// Unconfirmed: nothing to see, nothing to accept or redeem.
	mine, apiErr := f.svc.ListMine(ctx, "new1")
	require.Nil(t, apiErr)
	assert.Empty(t, mine)
	_, apiErr = f.svc.Accept(ctx, "new1", l0.ID.Hex())
	requireCode(t, apiErr, http.StatusForbidden)
	_, apiErr = f.svc.Redeem(ctx, "new1", "t")
	requireCode(t, apiErr, http.StatusForbidden)
	row, _ = f.gym.store.Get(ctx, gymA, "new1")
	assert.Nil(t, row)

	// Confirmed: the invite shows up and needs an explicit accept.
	u.UserStatus = db.Active
	mine, apiErr = f.svc.ListMine(ctx, "new1")
	require.Nil(t, apiErr)
	require.Len(t, mine, 1)
	res, apiErr := f.svc.Accept(ctx, "new1", l0.ID.Hex())
	require.Nil(t, apiErr)
	assert.Equal(t, services.RedeemJoined, res.Status)
	row, _ = f.gym.store.Get(ctx, gymA, "new1")
	require.NotNil(t, row)
	assert.Equal(t, "c1", row.CoachID)
}

func TestSignupWithOpenLinkJoinsOnceAndReleasesOnAbort(t *testing.T) {
	ctx := context.Background()
	f := newInviteFixture(t, true)
	l0 := f.seedLink("t", db.InviteLink{Kind: db.InviteKindClientGym, GymID: gymA, CreatorID: "owner", MaxUses: 1})
	l, apiErr := f.svc.PrepareSignup(ctx, "t", "a@x.tn")
	require.Nil(t, apiErr)
	f.svc.AbortSignup(ctx, l) // signup failed
	assert.Equal(t, 0, f.store.rows[l0.ID.Hex()].Uses)

	l, apiErr = f.svc.PrepareSignup(ctx, "t", "a@x.tn")
	require.Nil(t, apiErr)
	u := &db.User{ExternalID: "new2", UserEmail: "a@x.tn", UserRole: "client", UserStatus: db.ToConfirm}
	f.users["new2"] = u
	require.Nil(t, f.svc.CompleteSignup(ctx, l, u))
	row, _ := f.gym.store.Get(ctx, gymA, "new2")
	require.NotNil(t, row, "open link keeps the signup auto-join")
	assert.Equal(t, 1, f.store.rows[l0.ID.Hex()].Uses, "counted once, not twice")
	assert.Equal(t, db.InviteStatusExhausted, f.store.rows[l0.ID.Hex()].Status)
}

func TestConcurrentSingleUseSignupsReserveOneUse(t *testing.T) {
	ctx := context.Background()
	f := newInviteFixture(t, true)
	f.seedLink("t", db.InviteLink{Kind: db.InviteKindClientGym, GymID: gymA, CreatorID: "owner", MaxUses: 1})
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, apiErr := f.svc.PrepareSignup(ctx, "t", "a@x.tn")
			mu.Lock()
			defer mu.Unlock()
			if apiErr == nil {
				ok++
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, 1, ok, "no more accounts than uses")
}

func TestClientGymNeedsActiveGym(t *testing.T) {
	ctx := context.Background()
	f := newInviteFixture(t, true)
	f.seedLink("t", db.InviteLink{Kind: db.InviteKindClientGym, GymID: "gone", CreatorID: "owner"})
	e := f.seedLink("e", db.InviteLink{Kind: db.InviteKindClientGym, GymID: "gone", CreatorID: "owner", Email: "n1@x.tn", MaxUses: 1})
	_, apiErr := f.svc.Redeem(ctx, "n2", "t")
	requireCode(t, apiErr, http.StatusNotFound)
	_, apiErr = f.svc.PrepareSignup(ctx, "t", "a@x.tn")
	requireCode(t, apiErr, http.StatusNotFound)
	_, apiErr = f.svc.Accept(ctx, "n1", e.ID.Hex())
	requireCode(t, apiErr, http.StatusNotFound)
	row, _ := f.gym.store.Get(ctx, "gone", "n1")
	assert.Nil(t, row)
}

func TestClientGymRefusedWhenRelationsFlagOff(t *testing.T) {
	ctx := context.Background()
	f := newInviteFixture(t, true)
	gyms := fakeInviteGyms{gymA: {Name: "Gym A", Status: db.GymStatusActive}}
	authz := services.NewChainAuthorizer(f.gym.members, f.gym.policy, nil)
	svc := services.NewInviteLinkService(services.InviteLinkDeps{
		Store: f.store, Users: f.users, Gyms: gyms, Members: f.gym.members, Authz: authz,
		Registry:  services.NewInviteKindRegistry(services.NewClientGymKind(authz, f.gym.members, gyms, f.gym.svc, f.gym.store, false)),
		Observers: f.observe, ClientURL: "https://app.test",
	}, true)
	_, apiErr := svc.Create(ctx, services.Actor{ID: "owner", Role: db.RoleGymOwner}, services.CreateInvite{Kind: db.InviteKindClientGym, GymID: gymA})
	requireCode(t, apiErr, http.StatusBadRequest)
	f.seedLink("t", db.InviteLink{Kind: db.InviteKindClientGym, GymID: gymA, CreatorID: "owner"})
	_, apiErr = svc.PrepareSignup(ctx, "t", "a@x.tn")
	requireCode(t, apiErr, http.StatusBadRequest)
	_, apiErr = svc.Redeem(ctx, "n1", "t")
	requireCode(t, apiErr, http.StatusBadRequest)
}

func TestCreateValidation(t *testing.T) {
	ctx := context.Background()
	f := newInviteFixture(t, true)
	owner := services.Actor{ID: "owner", Role: db.RoleGymOwner}
	for _, bad := range []string{"not-an-email", "A <a@x.tn>", "a@"} {
		_, apiErr := f.svc.Create(ctx, owner, services.CreateInvite{Kind: db.InviteKindClientGym, GymID: gymA, Email: bad})
		requireCode(t, apiErr, http.StatusBadRequest)
	}
	res, apiErr := f.svc.Create(ctx, owner, services.CreateInvite{Kind: db.InviteKindClientGym, GymID: gymA, Email: "  New@X.TN "})
	require.Nil(t, apiErr)
	row, _ := f.store.GetByID(ctx, res.ID)
	assert.Equal(t, "new@x.tn", row.Email)

	// Role versus kind is refused up front.
	for _, k := range []string{db.InviteKindClientGym, db.InviteKindMemberCoach, db.InviteKindMemberHeadCoach, db.InviteKindClientPersonal} {
		_, apiErr := f.svc.Create(ctx, services.Actor{ID: "n1", Role: "client"}, services.CreateInvite{Kind: k, GymID: gymA})
		requireCode(t, apiErr, http.StatusForbidden)
	}
}

type fakeMaker struct{ calls int }

func (f *fakeMaker) CreateJoinLink(context.Context, string, string) (*services.JoinLinkResult, *models.ApiError) {
	f.calls++
	return &services.JoinLinkResult{InvitationID: "inv", Link: "l", ExpiresAt: time.Now()}, nil
}
func (f *fakeMaker) CreateCoachJoinLink(context.Context, string, string, string) (*services.JoinLinkResult, *models.ApiError) {
	f.calls++
	return &services.JoinLinkResult{InvitationID: "inv", Link: "l", ExpiresAt: time.Now()}, nil
}

func TestMemberCoachSupervisorMustBeActiveMember(t *testing.T) {
	ctx := context.Background()
	f := newInviteFixture(t, true)
	maker := &fakeMaker{}
	authz := services.NewChainAuthorizer(f.gym.members, f.gym.policy, nil)
	svc := services.NewInviteLinkService(services.InviteLinkDeps{
		Store: f.store, Users: f.users, Members: f.gym.members, Authz: authz,
		Registry:  services.NewInviteKindRegistry(services.NewMemberKinds(f.joiner, maker, nil, f.gym.members)...),
		Observers: f.observe,
	}, true)
	owner := services.Actor{ID: "owner", Role: db.RoleGymOwner}
	_, apiErr := svc.Create(ctx, owner, services.CreateInvite{Kind: db.InviteKindMemberCoach, GymID: gymA, SupervisorID: "outsider"})
	requireCode(t, apiErr, http.StatusBadRequest)
	assert.Equal(t, 0, maker.calls)
	_, apiErr = svc.Create(ctx, owner, services.CreateInvite{Kind: db.InviteKindMemberCoach, GymID: gymA, SupervisorID: "head"})
	require.Nil(t, apiErr)
	assert.Equal(t, 1, maker.calls)
}

func TestFlagOffLookupMatchesUnknownToken(t *testing.T) {
	ctx := context.Background()
	off := newInviteFixture(t, false)
	_, offErr := off.svc.Lookup(ctx, "whatever")
	on := newInviteFixture(t, true)
	_, onErr := on.svc.Lookup(ctx, "whatever")
	require.NotNil(t, offErr)
	require.NotNil(t, onErr)
	assert.Equal(t, onErr.Code, offErr.Code)
	assert.Equal(t, onErr.Error.Error(), offErr.Error.Error())
}

func TestRedeemClientPersonal(t *testing.T) {
	ctx := context.Background()
	f := newInviteFixture(t, true)
	f.seedLink("t", db.InviteLink{Kind: db.InviteKindClientPersonal, CreatorID: "c1"})

	res, apiErr := f.svc.Redeem(ctx, "n1", "t")
	require.Nil(t, apiErr)
	assert.Equal(t, services.RedeemJoined, res.Status)
	assert.Equal(t, "c1", f.adder.rows["n1"])
	assert.Equal(t, "c1", res.CoachID)

	// A coach account cannot use a client link.
	_, apiErr = f.svc.Redeem(ctx, "c1", "t")
	requireCode(t, apiErr, http.StatusConflict)
	// A client already linked to a coach: 409, use given back.
	_, apiErr = f.svc.Redeem(ctx, "cl9", "t")
	requireCode(t, apiErr, http.StatusConflict)

	// Entitlement hook blocks the signup path before the account is created.
	f.ent.err = &models.ApiError{Code: http.StatusForbidden, Error: errors.New("max clients")}
	_, apiErr = f.svc.PrepareSignup(ctx, "t", "x@x.tn")
	requireCode(t, apiErr, http.StatusForbidden)
}

func TestPrepareSignupLegacyRawTokenAndKindFilter(t *testing.T) {
	ctx := context.Background()
	f := newInviteFixture(t, true)
	f.regs["raw"] = &db.RegistrationLink{ID: primitive.NewObjectID(), Token: "raw", CoachID: "c1"}
	l, apiErr := f.svc.PrepareSignup(ctx, "raw", "a@x.tn")
	require.Nil(t, apiErr)
	assert.Equal(t, db.InviteKindClientPersonal, l.Kind)
	f.users["n"] = &db.User{ExternalID: "n", UserEmail: "a@x.tn", UserRole: "client", UserStatus: db.Active}
	require.Nil(t, f.svc.CompleteSignup(ctx, l, f.users["n"]), "legacy rows have no use accounting")
	assert.Equal(t, "c1", f.adder.rows["n"])

	// Member kinds are not a client signup.
	f.seedLink("m", db.InviteLink{Kind: db.InviteKindMemberCoach, GymID: gymA})
	_, apiErr = f.svc.PrepareSignup(ctx, "m", "a@x.tn")
	requireCode(t, apiErr, http.StatusNotFound)
}

func TestRedeemMemberKindsUseTheSeam(t *testing.T) {
	ctx := context.Background()
	f := newInviteFixture(t, true)
	f.seedLink("m", db.InviteLink{Kind: db.InviteKindMemberCoach, GymID: gymA, CreatorID: "owner", MaxUses: 1})
	res, apiErr := f.svc.Redeem(ctx, "c1", "m")
	require.Nil(t, apiErr)
	assert.Equal(t, services.RedeemJoined, res.Status)
	assert.Equal(t, []string{"m"}, f.joiner.acceptTokens)

	f.joiner.err = &models.ApiError{Code: http.StatusConflict, Error: errors.New("one gym only")}
	f.seedLink("m2", db.InviteLink{Kind: db.InviteKindMemberCoach, GymID: gymA, CreatorID: "owner", MaxUses: 1})
	_, apiErr = f.svc.Redeem(ctx, "c1", "m2")
	requireCode(t, apiErr, http.StatusConflict)
	_, apiErr = f.svc.Lookup(ctx, "m2")
	require.Nil(t, apiErr, "a failed redeem leaves the invite usable")
}

// ---------- observers ----------

func TestSyncObserverFailureAbortsRedemption(t *testing.T) {
	ctx := context.Background()
	f := newInviteFixture(t, true)
	l := f.seedLink("t", db.InviteLink{Kind: db.InviteKindClientGym, GymID: gymA, CreatorID: "owner", MaxUses: 1})
	f.obs.err = errors.New("affiliation writer down")

	_, apiErr := f.svc.Redeem(ctx, "n1", "t")
	requireCode(t, apiErr, http.StatusInternalServerError)
	assert.NotContains(t, apiErr.Error.Error(), "affiliation", "internal detail not exposed")
	assert.Equal(t, 0, f.store.rows[l.ID.Hex()].Uses, "use given back")
	assert.Equal(t, db.InviteStatusActive, f.store.rows[l.ID.Hex()].Status)
	f.observe.Wait()
	assert.Empty(t, f.async.events, "async observers do not run after a sync failure")

	// The retry is idempotent and succeeds once the observer recovers.
	f.obs.err = nil
	res, apiErr := f.svc.Redeem(ctx, "n1", "t")
	require.Nil(t, apiErr)
	assert.Equal(t, services.RedeemAlreadyMember, res.Status)
}

func TestAsyncObserverFailureIsLoggedWithoutToken(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	prev := zap.L()
	zap.ReplaceGlobals(zap.New(core))
	defer zap.ReplaceGlobals(prev)

	ctx := context.Background()
	f := newInviteFixture(t, true)
	const token = "SECRET-RAW-TOKEN-123"
	f.seedLink(token, db.InviteLink{Kind: db.InviteKindClientGym, GymID: gymA, CreatorID: "owner"})
	f.async.err = errors.New("chat-api returned 500")

	res, apiErr := f.svc.Redeem(ctx, "n1", token)
	require.Nil(t, apiErr, "an async failure never fails the redemption")
	assert.Equal(t, services.RedeemJoined, res.Status)
	f.observe.Wait()

	entries := logs.FilterMessage("invite observer failed").All()
	require.Len(t, entries, 1)
	for _, e := range logs.All() {
		assert.NotContains(t, e.Message, token)
		for _, fld := range e.Context {
			assert.NotContains(t, fld.String, token)
			if fld.Interface != nil {
				assert.NotContains(t, strings.ToLower(fld.Interface.(error).Error()), strings.ToLower(token))
			}
		}
	}
	assert.Equal(t, "async", entries[0].ContextMap()["observer"])
}

func TestEnsureConversationObserver(t *testing.T) {
	ctx := context.Background()
	ens := &fakeEnsurer{}
	ob := services.NewEnsureConversationObserver(ens)
	require.NoError(t, ob.OnRedeemed(ctx, services.InviteRedeemed{Kind: db.InviteKindClientPersonal, CoachID: "c1", UserID: "n1"}))
	assert.Equal(t, [][2]string{{"c1", "n1"}}, ens.calls)

	// Gym clients (opened by Join), pool clients and member kinds are not opened by the observer.
	require.NoError(t, ob.OnRedeemed(ctx, services.InviteRedeemed{Kind: db.InviteKindClientGym, CoachID: "c1", UserID: "n1"}))
	require.NoError(t, ob.OnRedeemed(ctx, services.InviteRedeemed{Kind: db.InviteKindClientPersonal, UserID: "n1"}))
	require.NoError(t, ob.OnRedeemed(ctx, services.InviteRedeemed{Kind: db.InviteKindMemberCoach, CoachID: "c1", UserID: "x"}))
	assert.Len(t, ens.calls, 1)

	ens.err = &models.ApiError{Code: 500, Error: errors.New("boom")}
	assert.Error(t, ob.OnRedeemed(ctx, services.InviteRedeemed{Kind: db.InviteKindClientPersonal, CoachID: "c1", UserID: "n1"}))
	assert.NoError(t, services.NewEnsureConversationObserver(nil).OnRedeemed(ctx, services.InviteRedeemed{Kind: db.InviteKindClientPersonal, CoachID: "c1", UserID: "n1"}))
}

type fakeEnsurer struct {
	calls    [][2]string
	gymCalls [][3]string
	err      *models.ApiError
}

func (f *fakeEnsurer) EnsureConversation(_ context.Context, adminID, peerID string) *models.ApiError {
	f.calls = append(f.calls, [2]string{adminID, peerID})
	return f.err
}

// ---------- registry, legacy email adapter, backfill ----------

func TestInviteKindRegistry(t *testing.T) {
	r := services.NewInviteKindRegistry(services.NewMemberKinds(&fakeMemberJoiner{}, nil, nil, nil)...)
	for _, n := range []string{db.InviteKindMemberCoach, db.InviteKindMemberHeadCoach} {
		k, ok := r.Get(n)
		require.True(t, ok)
		assert.Equal(t, n, k.Name())
	}
	_, ok := r.Get("nope")
	assert.False(t, ok)
}

type fakeAcceptor struct {
	gotID, gotEmail string
	err             *models.ApiError
}

func (f *fakeAcceptor) AcceptInvitation(_ context.Context, id, email, _ string) (*invitation.Invitation, *models.ApiError) {
	f.gotID, f.gotEmail = id, email
	return &invitation.Invitation{}, f.err
}

func TestLegacyEmailKindAdapter(t *testing.T) {
	a := &fakeAcceptor{}
	k := services.NewLegacyEmailKind(a)
	res, apiErr := k.Redeem(context.Background(), services.RedeemInput{User: &db.User{ExternalID: "u1", UserEmail: " U@X.tn "}})
	require.Nil(t, apiErr)
	assert.Equal(t, services.RedeemJoined, res.Status)
	assert.Equal(t, "u1", a.gotID)
	assert.Equal(t, "u@x.tn", a.gotEmail)
	_, apiErr = k.AuthorizeCreate(context.Background(), services.Actor{}, services.CreateInvite{})
	requireCode(t, apiErr, http.StatusBadRequest)
}

type fakeBackfillSrc []db.RegistrationLink

func (f fakeBackfillSrc) ForEach(_ context.Context, fn func(db.RegistrationLink) error) error {
	for _, l := range f {
		if err := fn(l); err != nil {
			return err
		}
	}
	return nil
}

type fakeBackfillDst struct{ byHash map[string]*db.InviteLink }

func (f *fakeBackfillDst) GetByHash(_ context.Context, h string) (*db.InviteLink, error) {
	return f.byHash[h], nil
}
func (f *fakeBackfillDst) UpsertByHash(_ context.Context, l *db.InviteLink) (bool, error) {
	if _, ok := f.byHash[l.TokenHash]; ok {
		return false, nil
	}
	f.byHash[l.TokenHash] = l
	return true, nil
}

func TestInviteBackfill(t *testing.T) {
	ctx := context.Background()
	src := fakeBackfillSrc{
		{ID: primitive.NewObjectID(), Token: "t1", CoachID: "c1", CreatedAt: time.Now()},
		{ID: primitive.NewObjectID(), Token: "t2", CoachID: "c2"},
		{ID: primitive.NewObjectID(), Token: "", CoachID: "c3"},
	}
	dst := &fakeBackfillDst{byHash: map[string]*db.InviteLink{}}
	logf := func(string, ...any) {}

	rep, err := services.RunInviteBackfill(ctx, src, dst, true, logf)
	require.NoError(t, err)
	assert.Equal(t, 2, rep.WouldCreate)
	assert.Equal(t, 1, rep.Failed)
	assert.Empty(t, dst.byHash, "dry run writes nothing")

	rep, err = services.RunInviteBackfill(ctx, src, dst, false, logf)
	require.NoError(t, err)
	assert.Equal(t, 2, rep.Created)
	l := dst.byHash[utils.HashToken("t1")]
	require.NotNil(t, l)
	assert.Equal(t, db.InviteKindClientPersonal, l.Kind)
	assert.Equal(t, "c1", l.CreatorID)
	assert.Equal(t, 0, l.MaxUses, "legacy links stay unlimited")
	assert.Nil(t, l.ExpiresAt)

	rep, err = services.RunInviteBackfill(ctx, src, dst, false, logf)
	require.NoError(t, err)
	assert.Equal(t, 0, rep.Created, "idempotent")
	assert.Equal(t, 2, rep.Existing)
}

// compile-time check that the mock keeps satisfying the service interface.
var _ services.InviteLinkService = (*mocks.InviteLinkService)(nil)

func (f *fakeEnsurer) EnsureGymConversation(_ context.Context, adminID, peerID, gymID string) *models.ApiError {
	f.gymCalls = append(f.gymCalls, [3]string{adminID, peerID, gymID})
	return f.err
}

func TestEnsureConversationObserverGymIsNoOp(t *testing.T) {
	ens := &fakeEnsurer{}
	ob := services.NewEnsureConversationObserver(ens)
	require.NoError(t, ob.OnRedeemed(context.Background(), services.InviteRedeemed{Kind: db.InviteKindClientGym, GymID: gymA, CoachID: "c1", UserID: "n1"}))
	assert.Empty(t, ens.gymCalls, "Join/openJoinChats opens gym conversations")
	assert.Empty(t, ens.calls)
}

func TestSignupWithEmailBoundInviteJoinsWhenVerified(t *testing.T) {
	ctx := context.Background()
	f := newInviteFixture(t, true)
	l0 := f.seedLink("t", db.InviteLink{Kind: db.InviteKindClientGym, GymID: gymA, CreatorID: "c1", TargetCoachID: "c1", Email: "g@x.tn", MaxUses: 1, ExpiresAt: future()})
	l, apiErr := f.svc.PrepareSignup(ctx, "t", "g@x.tn")
	require.Nil(t, apiErr)
	u := &db.User{ExternalID: "g1", UserEmail: "g@x.tn", UserRole: "client", UserStatus: db.Active} // OAuth signup
	f.users["g1"] = u
	require.Nil(t, f.svc.CompleteSignup(ctx, l, u))
	row, _ := f.gym.store.Get(ctx, gymA, "g1")
	require.NotNil(t, row, "verified account redeems the email-bound link at signup")
	assert.Equal(t, "c1", row.CoachID)
	assert.Equal(t, 1, f.store.rows[l0.ID.Hex()].Uses)
}
