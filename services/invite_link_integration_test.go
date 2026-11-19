//go:build integration

package services_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
	"coachify-account-api/services"
	"coachify-account-api/utils"
)

// itUsers serves test users to the invite service (the user repository is not under test here).
type itUsers map[string]*db.User

func (u itUsers) GetUserByExternalIdUpdate(_ context.Context, id string) (*db.User, *models.ApiError) {
	if x, ok := u[id]; ok {
		return x, nil
	}
	return nil, models.NewApiError(http.StatusNotFound, models.ErrInviteNotFound)
}

func (u itUsers) GetUserNameByExternalID(_ context.Context, id string) (string, *models.ApiError) {
	return id, nil
}

type itNoQuota struct{}

func (itNoQuota) Check(context.Context, string, string, string) *models.ApiError { return nil }

type inviteEnv struct {
	*memberEnv
	svc     services.InviteLinkService
	links   *repositories.InviteLinkRepository
	reg     *repositories.RegistrationLinkRepository
	clients *repositories.GymClientRepository
	ctx     context.Context
	gymID   string
	users   itUsers
}

func newInviteEnv(t *testing.T) *inviteEnv {
	t.Helper()
	e := newMemberEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	d := e.database

	coachRepo := repositories.NewCoachRepository(d, "coach_clients", d.Collection("users"))
	gcRepo := repositories.NewGymClientRepository(d, "users")
	reader := services.NewRepoMembershipReader(e.members)
	gymClients := services.NewGymClientService(services.GymClientDeps{Members: reader, Store: gcRepo, Links: coachRepo, Quota: itNoQuota{}}, true)

	links := repositories.NewInviteLinkRepository(d)
	reg := repositories.NewRegistrationLinkRepository(d, "registration_links")
	users := itUsers{}
	registry := services.NewInviteKindRegistry(
		services.NewClientPersonalKind(nil, coachRepo),
		services.NewClientGymKind(nil, reader, e.gyms, gymClients, gcRepo, true),
	)
	svc := services.NewInviteLinkService(services.InviteLinkDeps{
		Store: links, RegLinks: reg, Users: users, Gyms: e.gyms, Members: reader, Registry: registry,
	}, true)

	return &inviteEnv{memberEnv: e, svc: svc, links: links, reg: reg, clients: gcRepo, ctx: ctx, gymID: e.gym(t, "owner1"), users: users}
}

func (e *inviteEnv) addUser(id, email string, status db.UserStatus) {
	e.users[id] = &db.User{ExternalID: id, UserEmail: email, UserRole: "client", UserStatus: status}
}

func (e *inviteEnv) coachMember(t *testing.T, id string) {
	t.Helper()
	require.NoError(t, e.members.UpsertActive(e.ctx, &db.GymMember{GymID: e.gymID, UserID: id, Role: db.GymMemberRoleCoach,
		Status: db.GymMemberStatusActive, JoinedAt: time.Now(), AddedBy: "x", Source: db.GymMemberSource("test")}))
}

func (e *inviteEnv) seed(t *testing.T, raw string, l db.InviteLink) *db.InviteLink {
	t.Helper()
	l.TokenHash = utils.HashToken(raw)
	if l.Status == "" {
		l.Status = db.InviteStatusActive
	}
	l.CreatedAt = time.Now().UTC()
	require.NoError(t, e.links.Insert(e.ctx, &l))
	return &l
}

func (e *inviteEnv) n(t *testing.T, coll string, f bson.M) int64 {
	t.Helper()
	n, err := e.database.Collection(coll).CountDocuments(e.ctx, f)
	require.NoError(t, err)
	return n
}

func (e *inviteEnv) uses(t *testing.T, l *db.InviteLink) (int, string) {
	t.Helper()
	g, err := e.links.GetByID(e.ctx, l.ID.Hex())
	require.NoError(t, err)
	require.NotNil(t, g)
	return g.Uses, g.Status
}

func (e *inviteEnv) snapshot(t *testing.T, coll string) []bson.M {
	t.Helper()
	cur, err := e.database.Collection(coll).Find(e.ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	require.NoError(t, err)
	var out []bson.M
	require.NoError(t, cur.All(e.ctx, &out))
	return out
}

func TestInviteIT_ClientGymRedeemPoolAndIdempotent(t *testing.T) {
	e := newInviteEnv(t)
	e.addUser("cl1", "cl1@x.io", db.Active)
	// a pre-seeded personal link must survive a gym redeem
	_, err := e.database.Collection("coach_clients").InsertOne(e.ctx, bson.M{"coach_id": "pcoach", "client_id": "cl1", "created_at": time.Now()})
	require.NoError(t, err)

	l := e.seed(t, "tok-pool", db.InviteLink{Kind: db.InviteKindClientGym, CreatorID: "owner1", CreatorRole: "owner", GymID: e.gymID, MaxUses: 3})

	res, apiErr := e.svc.Redeem(e.ctx, "cl1", "tok-pool")
	require.Nil(t, apiErr)
	assert.Equal(t, services.RedeemJoined, res.Status)
	assert.Equal(t, e.gymID, res.GymID)
	assert.Empty(t, res.CoachID, "pool")

	row, err := e.clients.Get(e.ctx, e.gymID, "cl1")
	require.NoError(t, err)
	require.NotNil(t, row)
	assert.Equal(t, "", row.CoachID)
	assert.False(t, row.ConsentAt.IsZero(), "consent_at set")
	assert.Equal(t, l.ID.Hex(), row.InviteID)
	assert.EqualValues(t, 1, e.n(t, "coach_clients", bson.M{"coach_id": "pcoach", "client_id": "cl1"}), "personal row survives")
	assert.EqualValues(t, 1, e.n(t, "coach_clients", bson.M{}), "no coach_clients write")
	u, st := e.uses(t, l)
	assert.Equal(t, 1, u)
	assert.Equal(t, db.InviteStatusActive, st)

	// second redeem: already_member, no use burned, still one row
	res, apiErr = e.svc.Redeem(e.ctx, "cl1", "tok-pool")
	require.Nil(t, apiErr)
	assert.Equal(t, services.RedeemAlreadyMember, res.Status)
	u, _ = e.uses(t, l)
	assert.Equal(t, 1, u, "no use burned by an idempotent redeem")
	assert.EqualValues(t, 1, e.n(t, "gym_clients", bson.M{"gym_id": e.gymID, "client_id": "cl1"}))
}

func TestInviteIT_ClientGymCoachTargetAndExhaust(t *testing.T) {
	e := newInviteEnv(t)
	e.coachMember(t, "coachA")
	e.addUser("c1", "c1@x.io", db.Active)
	e.addUser("c2", "c2@x.io", db.Active)
	l := e.seed(t, "tok-coach", db.InviteLink{Kind: db.InviteKindClientGym, CreatorID: "coachA", CreatorRole: "coach",
		GymID: e.gymID, TargetCoachID: "coachA", MaxUses: 1})

	res, apiErr := e.svc.Redeem(e.ctx, "c1", "tok-coach")
	require.Nil(t, apiErr)
	assert.Equal(t, services.RedeemJoined, res.Status)
	assert.Equal(t, "coachA", res.CoachID)
	row, err := e.clients.Get(e.ctx, e.gymID, "c1")
	require.NoError(t, err)
	require.NotNil(t, row)
	assert.Equal(t, "coachA", row.CoachID)
	require.NotNil(t, row.AssignedAt)
	u, st := e.uses(t, l)
	assert.Equal(t, 1, u)
	assert.Equal(t, db.InviteStatusExhausted, st)

	// exhausted: a second client gets the uniform 404 and no row
	_, apiErr = e.svc.Redeem(e.ctx, "c2", "tok-coach")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusNotFound, apiErr.Code)
	assert.EqualValues(t, 0, e.n(t, "gym_clients", bson.M{"client_id": "c2"}))
}

func TestInviteIT_RedeemFailureReleasesUse(t *testing.T) {
	e := newInviteEnv(t)
	e.addUser("c1", "c1@x.io", db.Active)
	// target coach is not a gym member: Join fails, the single use must be given back
	l := e.seed(t, "tok-bad", db.InviteLink{Kind: db.InviteKindClientGym, CreatorID: "ghost", CreatorRole: "coach",
		GymID: e.gymID, TargetCoachID: "ghost", MaxUses: 1})
	_, apiErr := e.svc.Redeem(e.ctx, "c1", "tok-bad")
	require.NotNil(t, apiErr)
	u, st := e.uses(t, l)
	assert.Equal(t, 0, u)
	assert.Equal(t, db.InviteStatusActive, st)
	assert.EqualValues(t, 0, e.n(t, "gym_clients", bson.M{}))
}

func TestInviteIT_EmailBoundPendingThenAccept(t *testing.T) {
	e := newInviteEnv(t)
	e.addUser("c1", "c1@x.io", db.Active)
	e.addUser("c9", "other@x.io", db.Active)
	e.addUser("cu", "cu@x.io", db.ToConfirm)
	l := e.seed(t, "tok-mail", db.InviteLink{Kind: db.InviteKindClientGym, CreatorID: "owner1", CreatorRole: "owner",
		GymID: e.gymID, Email: "c1@x.io", MaxUses: 1})
	l2 := e.seed(t, "tok-mail-u", db.InviteLink{Kind: db.InviteKindClientGym, CreatorID: "owner1", CreatorRole: "owner",
		GymID: e.gymID, Email: "cu@x.io", MaxUses: 1})

	// wrong mailbox
	_, apiErr := e.svc.Redeem(e.ctx, "c9", "tok-mail")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusForbidden, apiErr.Code)
	// unverified account: no write
	_, apiErr = e.svc.Redeem(e.ctx, "cu", "tok-mail-u")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusForbidden, apiErr.Code)
	assert.EqualValues(t, 0, e.n(t, "gym_clients", bson.M{}))
	u, _ := e.uses(t, l2)
	assert.Equal(t, 0, u)

	// redeem by link is only pending: nothing written, no use burned
	res, apiErr := e.svc.Redeem(e.ctx, "c1", "tok-mail")
	require.Nil(t, apiErr)
	assert.Equal(t, services.RedeemPendingAcceptance, res.Status)
	assert.EqualValues(t, 0, e.n(t, "gym_clients", bson.M{}))
	u, st := e.uses(t, l)
	assert.Equal(t, 0, u)
	assert.Equal(t, db.InviteStatusActive, st)

	mine, apiErr := e.svc.ListMine(e.ctx, "c1")
	require.Nil(t, apiErr)
	require.Len(t, mine, 1)
	assert.Equal(t, l.ID.Hex(), mine[0].ID)

	// someone else cannot accept it
	_, apiErr = e.svc.Accept(e.ctx, "c9", l.ID.Hex())
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusNotFound, apiErr.Code)

	res, apiErr = e.svc.Accept(e.ctx, "c1", l.ID.Hex())
	require.Nil(t, apiErr)
	assert.Equal(t, services.RedeemJoined, res.Status)
	row, err := e.clients.Get(e.ctx, e.gymID, "c1")
	require.NoError(t, err)
	require.NotNil(t, row)
	assert.False(t, row.ConsentAt.IsZero())
	u, st = e.uses(t, l)
	assert.Equal(t, 1, u)
	assert.Equal(t, db.InviteStatusExhausted, st)

	mine, _ = e.svc.ListMine(e.ctx, "c1")
	assert.Empty(t, mine)
}

func TestInviteIT_DeclineEmailInvite(t *testing.T) {
	e := newInviteEnv(t)
	e.addUser("c1", "c1@x.io", db.Active)
	l := e.seed(t, "tok-dec", db.InviteLink{Kind: db.InviteKindClientGym, CreatorID: "owner1", CreatorRole: "owner",
		GymID: e.gymID, Email: "c1@x.io", MaxUses: 1})
	require.Nil(t, e.svc.Decline(e.ctx, "c1", l.ID.Hex()))
	_, st := e.uses(t, l)
	assert.Equal(t, db.InviteStatusDeclined, st)
	_, apiErr := e.svc.Accept(e.ctx, "c1", l.ID.Hex())
	require.NotNil(t, apiErr)
	assert.EqualValues(t, 0, e.n(t, "gym_clients", bson.M{}))
}

func TestInviteIT_LegacyRegistrationLinkFallbackAndHashFirst(t *testing.T) {
	e := newInviteEnv(t)
	e.addUser("cl1", "cl1@x.io", db.Active)
	e.addUser("cl2", "cl2@x.io", db.Active)
	require.NoError(t, e.reg.Create(e.ctx, &db.RegistrationLink{Token: "raw-legacy-token", CoachID: "coachL", CreatedAt: time.Now().UTC()}))

	lk, apiErr := e.svc.Lookup(e.ctx, "raw-legacy-token")
	require.Nil(t, apiErr)
	assert.Equal(t, db.InviteKindClientPersonal, lk.Kind)

	// no hashed row yet: raw-token fallback writes coach_clients
	res, apiErr := e.svc.Redeem(e.ctx, "cl1", "raw-legacy-token")
	require.Nil(t, apiErr)
	assert.Equal(t, services.RedeemJoined, res.Status)
	assert.Equal(t, "coachL", res.CoachID)
	assert.EqualValues(t, 1, e.n(t, "coach_clients", bson.M{"coach_id": "coachL", "client_id": "cl1"}))
	assert.EqualValues(t, 0, e.n(t, "gym_clients", bson.M{}))
	assert.EqualValues(t, 0, e.n(t, "invite_links", bson.M{}), "legacy fallback does not write invite_links")

	// the hashed row wins over the raw row once it exists (use accounting starts)
	h := e.seed(t, "raw-legacy-token", db.InviteLink{Kind: db.InviteKindClientPersonal, CreatorID: "coachL", CreatorRole: "legacy"})
	res, apiErr = e.svc.Redeem(e.ctx, "cl2", "raw-legacy-token")
	require.Nil(t, apiErr)
	assert.Equal(t, services.RedeemJoined, res.Status)
	assert.Equal(t, h.ID.Hex(), res.InviteID, "hashed row served the redeem")
	u, st := e.uses(t, h)
	assert.Equal(t, 1, u)
	assert.Equal(t, db.InviteStatusActive, st, "unlimited link stays active")

	// unknown token is a uniform 404
	_, apiErr = e.svc.Redeem(e.ctx, "cl1", "nope")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusNotFound, apiErr.Code)
}

func TestInviteIT_Backfill(t *testing.T) {
	e := newInviteEnv(t)
	for i, tok := range []string{"bf-1", "bf-2", "bf-3"} {
		require.NoError(t, e.reg.Create(e.ctx, &db.RegistrationLink{Token: tok, CoachID: "coach" + string(rune('A'+i)), CreatedAt: time.Now().UTC()}))
	}
	// one unusable legacy row (no coach) is skipped, not fatal
	_, err := e.database.Collection("registration_links").InsertOne(e.ctx, bson.M{"_id": primitive.NewObjectID(), "token": "bf-bad", "coach_id": ""})
	require.NoError(t, err)
	before := e.snapshot(t, "registration_links")

	rep, err := services.RunInviteBackfill(e.ctx, e.reg, e.links, true, t.Logf)
	require.NoError(t, err)
	assert.Equal(t, 4, rep.Legacy)
	assert.Equal(t, 3, rep.WouldCreate)
	assert.Equal(t, 1, rep.Failed)
	assert.Equal(t, 0, rep.Created)
	assert.EqualValues(t, 0, e.n(t, "invite_links", bson.M{}), "dry run writes nothing")

	rep, err = services.RunInviteBackfill(e.ctx, e.reg, e.links, false, t.Logf)
	require.NoError(t, err)
	assert.Equal(t, 3, rep.Created)
	assert.EqualValues(t, 3, e.n(t, "invite_links", bson.M{"kind": db.InviteKindClientPersonal, "creator_role": "legacy"}))
	for _, tok := range []string{"bf-1", "bf-2", "bf-3"} {
		l, err := e.links.GetByHash(e.ctx, utils.HashToken(tok))
		require.NoError(t, err)
		require.NotNil(t, l, tok)
		assert.Equal(t, 0, l.MaxUses)
		assert.Equal(t, db.InviteStatusActive, l.Status)
		assert.Nil(t, l.ExpiresAt)
		assert.Contains(t, l.LegacyRef, "registration_links:")
	}

	rep, err = services.RunInviteBackfill(e.ctx, e.reg, e.links, false, t.Logf)
	require.NoError(t, err)
	assert.Equal(t, 0, rep.Created, "second real run creates nothing")
	assert.Equal(t, 3, rep.Existing)
	assert.EqualValues(t, 3, e.n(t, "invite_links", bson.M{}))

	assert.Equal(t, before, e.snapshot(t, "registration_links"), "registration_links untouched")
}
