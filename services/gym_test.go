package services_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
	"coachify-account-api/services"
	"coachify-account-api/services/mocks"
)

// fakeGymStore is an in-memory services.GymStore.
type fakeGymStore struct {
	gyms       map[string]*db.Gym
	heads      map[string]*db.GymHeadCoach // by head coach id
	invites    map[string]*db.GymInvitation
	team       map[string]bool // coaches that belong to a team
	audits     []*db.TeamAuditEntry
	periods    []string // "open:<head>:<reason>" / "close:<head>:<reason>"
	failOn     string   // method name that returns an error
	lastStatus string   // status filter of the last ListGymsAdmin call
}

func newFakeGymStore() *fakeGymStore {
	return &fakeGymStore{
		gyms: map[string]*db.Gym{}, heads: map[string]*db.GymHeadCoach{},
		invites: map[string]*db.GymInvitation{}, team: map[string]bool{},
	}
}

var errBoom = errors.New("mongo: connection refused to 10.0.0.5")

func (f *fakeGymStore) fail(op string) error {
	if f.failOn == op {
		return errBoom
	}
	return nil
}

func (f *fakeGymStore) addGym(owner, status string) *db.Gym {
	g := &db.Gym{ID: primitive.NewObjectID(), Name: "Iron Gym", OwnerID: owner, Status: status,
		DefaultCurrency: "TND", Timezone: "Africa/Tunis", CreatedAt: time.Now()}
	f.gyms[g.ID.Hex()] = g
	return g
}

func (f *fakeGymStore) addInvite(gym *db.Gym, email, status string, expires time.Time) *db.GymInvitation {
	inv := &db.GymInvitation{ID: primitive.NewObjectID(), GymID: gym.ID.Hex(), InviterID: gym.OwnerID,
		Email: email, Status: status, ExpiresAt: expires, CreatedAt: time.Now()}
	f.invites[inv.ID.Hex()] = inv
	return inv
}

func (f *fakeGymStore) actions() []string {
	out := []string{}
	for _, a := range f.audits {
		out = append(out, a.Action)
	}
	return out
}

func (f *fakeGymStore) CreateGym(_ context.Context, g *db.Gym) error {
	if err := f.fail("CreateGym"); err != nil {
		return err
	}
	g.ID = primitive.NewObjectID()
	f.gyms[g.ID.Hex()] = g
	return nil
}
func (f *fakeGymStore) GetGym(_ context.Context, id string) (*db.Gym, error) {
	if err := f.fail("GetGym"); err != nil {
		return nil, err
	}
	if g := f.gyms[id]; g != nil {
		c := *g
		return &c, nil
	}
	return nil, nil
}
func (f *fakeGymStore) GetOwnedGym(_ context.Context, id, owner string) (*db.Gym, error) {
	if err := f.fail("GetOwnedGym"); err != nil {
		return nil, err
	}
	if g := f.gyms[id]; g != nil && g.OwnerID == owner {
		c := *g
		return &c, nil
	}
	return nil, nil
}
func (f *fakeGymStore) UpdateGym(_ context.Context, id string, u db.GymUpdate) (bool, error) {
	g := f.gyms[id]
	if g == nil {
		return false, nil
	}
	if u.Name != nil {
		g.Name = *u.Name
	}
	if u.OwnerID != nil {
		g.OwnerID = *u.OwnerID
	}
	if u.Status != nil {
		g.Status = *u.Status
	}
	return true, nil
}
func (f *fakeGymStore) ListGymsByOwner(_ context.Context, owner string) ([]db.GymSummary, error) {
	out := []db.GymSummary{}
	for id, g := range f.gyms {
		if g.OwnerID == owner {
			out = append(out, db.GymSummary{GymID: id, Name: g.Name, Status: g.Status})
		}
	}
	return out, nil
}
func (f *fakeGymStore) ListGymsAdmin(_ context.Context, _, _ int, _, status string) ([]db.AdminGymRow, int, error) {
	f.lastStatus = status
	return nil, 0, nil
}
func (f *fakeGymStore) ListGymHeadCoachesAdmin(_ context.Context, gymID string) ([]db.AdminGymHeadCoachRow, error) {
	if err := f.fail("ListGymHeadCoachesAdmin"); err != nil {
		return nil, err
	}
	out := []db.AdminGymHeadCoachRow{}
	for _, h := range f.heads {
		if h.GymID == gymID {
			out = append(out, db.AdminGymHeadCoachRow{ExternalID: h.HeadCoachID, AddedAt: h.AddedAt, AddedByRole: h.AddedByRole})
		}
	}
	return out, nil
}
func (f *fakeGymStore) AddHeadCoach(_ context.Context, h *db.GymHeadCoach, reason string) error {
	if _, ok := f.heads[h.HeadCoachID]; ok {
		return repositories.ErrHeadCoachInGym
	}
	f.heads[h.HeadCoachID] = h
	f.periods = append(f.periods, "open:"+h.HeadCoachID+":"+reason)
	return nil
}
func (f *fakeGymStore) GetGymOfHead(_ context.Context, id string) (*db.GymHeadCoach, error) {
	return f.heads[id], nil
}
func (f *fakeGymStore) RemoveHeadCoach(_ context.Context, gymID, id, reason string) (bool, error) {
	if h := f.heads[id]; h != nil && h.GymID == gymID {
		delete(f.heads, id)
		f.periods = append(f.periods, "close:"+id+":"+reason)
		return true, nil
	}
	return false, nil
}
func (f *fakeGymStore) ReleaseHead(_ context.Context, id, reason string) (string, bool, error) {
	if h := f.heads[id]; h != nil {
		delete(f.heads, id)
		f.periods = append(f.periods, "close:"+id+":"+reason)
		return h.GymID, true, nil
	}
	return "", false, nil
}
func (f *fakeGymStore) RemoveAllByGym(_ context.Context, gymID, reason string) ([]string, error) {
	ids := []string{}
	for id, h := range f.heads {
		if h.GymID == gymID {
			ids = append(ids, id)
			delete(f.heads, id)
			f.periods = append(f.periods, "close:"+id+":"+reason)
		}
	}
	return ids, nil
}
func (f *fakeGymStore) Overview(context.Context, string) (*db.GymOverview, error) {
	return &db.GymOverview{HeadCoaches: 1}, nil
}
func (f *fakeGymStore) ListHeadCoachRows(context.Context, string, string, int, int) ([]db.GymHeadCoachRow, int, error) {
	return nil, 0, nil
}
func (f *fakeGymStore) ListCoaches(context.Context, string, db.GymCoachQuery) ([]db.GymCoachRow, int, error) {
	return nil, 0, nil
}
func (f *fakeGymStore) ListClients(context.Context, string, db.GymClientQuery) ([]db.GymClientRow, int, error) {
	return nil, 0, nil
}
func (f *fakeGymStore) InsertInvitation(_ context.Context, inv *db.GymInvitation) error {
	for _, e := range f.invites {
		if e.GymID == inv.GymID && e.Email == inv.Email && e.Status == db.GymInvitePending {
			return repositories.ErrInvitationPending
		}
	}
	inv.ID = primitive.NewObjectID()
	f.invites[inv.ID.Hex()] = inv
	return nil
}
func (f *fakeGymStore) CountInvitationsSince(_ context.Context, gymID string, since time.Time) (int, error) {
	n := 0
	for _, i := range f.invites {
		if i.GymID == gymID && !i.CreatedAt.Before(since) {
			n++
		}
	}
	return n, nil
}
func (f *fakeGymStore) ExpireDue(_ context.Context, gymID, email string, now time.Time) error {
	for _, i := range f.invites {
		if i.Status == db.GymInvitePending && !i.ExpiresAt.After(now) &&
			(gymID == "" || i.GymID == gymID) && (email == "" || i.Email == email) {
			i.Status = db.GymInviteExpired
		}
	}
	return nil
}
func (f *fakeGymStore) GetInvitation(_ context.Context, id string) (*db.GymInvitation, error) {
	return f.invites[id], nil
}
func (f *fakeGymStore) TransitionInvitation(_ context.Context, id, from, to, invitee string, _ time.Time) (bool, error) {
	i := f.invites[id]
	if i == nil || i.Status != from {
		return false, nil
	}
	i.Status = to
	i.InviteeID = invitee
	return true, nil
}
func (f *fakeGymStore) RevokePendingByGym(_ context.Context, gymID string, _ time.Time) error {
	for _, i := range f.invites {
		if i.GymID == gymID && i.Status == db.GymInvitePending {
			i.Status = db.GymInviteRevoked
		}
	}
	return nil
}
func (f *fakeGymStore) ListInvitations(_ context.Context, gymID, status string, _, _ int) ([]db.GymInvitation, int, error) {
	out := []db.GymInvitation{}
	for _, i := range f.invites {
		if i.GymID == gymID && (status == "" || i.Status == status) {
			out = append(out, *i)
		}
	}
	return out, len(out), nil
}
func (f *fakeGymStore) ListPendingForEmail(context.Context, string, time.Time) ([]db.GymInvitationView, error) {
	return nil, nil
}
func (f *fakeGymStore) IsTeamMember(_ context.Context, id string) (bool, error) {
	return f.team[id], nil
}
func (f *fakeGymStore) InsertAudit(_ context.Context, e *db.TeamAuditEntry) error {
	f.audits = append(f.audits, e)
	return nil
}
func (f *fakeGymStore) InsertAuditMany(_ context.Context, es []*db.TeamAuditEntry) error {
	f.audits = append(f.audits, es...)
	return nil
}
func (f *fakeGymStore) ListAudit(context.Context, string, int, int) ([]db.TeamAuditEntry, int, error) {
	return nil, 0, nil
}

var _ services.GymStore = (*fakeGymStore)(nil)

func newGymSvc(perDay int) (services.GymService, *mocks.TeamUserStore, *fakeGymStore) {
	u, g := &mocks.TeamUserStore{}, newFakeGymStore()
	return services.NewGymService(u, g, services.GymSettings{InviteTTL: time.Hour, InvitesPerDay: perDay}), u, g
}

func gymUser(id, role string, status db.UserStatus, email string, head bool) *db.User {
	return &db.User{ExternalID: id, UserRole: role, UserStatus: status, UserEmail: email, IsHeadCoach: head,
		UserFirstname: "First" + id, UserLastname: "Last" + id}
}

func TestGymOwnerAuthorization(t *testing.T) {
	ctx := context.Background()

	t.Run("gym not owned is 404", func(t *testing.T) {
		svc, u, g := newGymSvc(20)
		givenUser(u, gymUser("owner1", db.RoleGymOwner, db.Active, "o1@x.tn", false))
		other := g.addGym("owner2", db.GymStatusActive)

		_, apiErr := svc.Overview(ctx, "owner1", other.ID.Hex())
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)
		assert.EqualError(t, apiErr.Error, "gym not found")

		_, apiErr = svc.Overview(ctx, "owner1", "not-an-id")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)
	})

	t.Run("wrong role and inactive status are 403", func(t *testing.T) {
		for name, user := range map[string]*db.User{
			"coach":     gymUser("x1", "coach", db.Active, "x@x.tn", true),
			"admin":     gymUser("x1", "admin", db.Active, "x@x.tn", false),
			"suspended": gymUser("x1", db.RoleGymOwner, db.Suspended, "x@x.tn", false),
			"blocked":   gymUser("x1", db.RoleGymOwner, db.Blocked, "x@x.tn", false),
		} {
			t.Run(name, func(t *testing.T) {
				svc, u, g := newGymSvc(20)
				givenUser(u, user)
				gym := g.addGym("x1", db.GymStatusActive)

				_, apiErr := svc.Overview(ctx, "x1", gym.ID.Hex())
				require.NotNil(t, apiErr)
				assert.Equal(t, http.StatusForbidden, apiErr.Code)
				assert.EqualError(t, apiErr.Error, "Gym owner access required")
			})
		}
	})

	t.Run("me for a non owner is not an error", func(t *testing.T) {
		svc, u, _ := newGymSvc(20)
		givenUser(u, gymUser("c1", "coach", db.Active, "c@x.tn", false))
		me, apiErr := svc.OwnerMe(ctx, "c1")
		require.Nil(t, apiErr)
		assert.False(t, me.IsGymOwner)
		assert.NotNil(t, me.Gyms)
	})

	t.Run("me lists the owner's gyms", func(t *testing.T) {
		svc, u, g := newGymSvc(20)
		givenUser(u, gymUser("owner1", db.RoleGymOwner, db.Active, "o1@x.tn", false))
		g.addGym("owner1", db.GymStatusActive)
		g.addGym("owner2", db.GymStatusActive)
		me, apiErr := svc.OwnerMe(ctx, "owner1")
		require.Nil(t, apiErr)
		assert.True(t, me.IsGymOwner)
		assert.Len(t, me.Gyms, 1)
	})

	t.Run("store failure hides the cause", func(t *testing.T) {
		svc, u, g := newGymSvc(20)
		givenUser(u, gymUser("owner1", db.RoleGymOwner, db.Active, "o1@x.tn", false))
		g.failOn = "GetOwnedGym"
		_, apiErr := svc.Overview(ctx, "owner1", "whatever")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
		assert.EqualError(t, apiErr.Error, "internal error")
		assert.NotContains(t, apiErr.Error.Error(), "mongo")
	})
}

func TestGymInvite(t *testing.T) {
	ctx := context.Background()

	t.Run("neutral response, no account lookup", func(t *testing.T) {
		svc, u, g := newGymSvc(20)
		givenUser(u, gymUser("owner1", db.RoleGymOwner, db.Active, "o1@x.tn", false))
		gym := g.addGym("owner1", db.GymStatusActive)

		a, apiErr := svc.Invite(ctx, "owner1", gym.ID.Hex(), "Known@X.tn ")
		require.Nil(t, apiErr)
		b, apiErr := svc.Invite(ctx, "owner1", gym.ID.Hex(), "nobody@nowhere.tn")
		require.Nil(t, apiErr)
		assert.Equal(t, "pending", a.Status)
		assert.Equal(t, "pending", b.Status)
		assert.NotEmpty(t, a.InvitationID)

		// Only the owner was ever loaded: the invitee's account is never looked up.
		u.AssertNumberOfCalls(t, "GetUserByExternalIdUpdate", 2)
		u.AssertNotCalled(t, "GetUserByExternalIdUpdate", mock.Anything, "known@x.tn")
		assert.Equal(t, "known@x.tn", g.invites[a.InvitationID].Email)
		assert.Equal(t, []string{"gym_invite", "gym_invite"}, g.actions())
	})

	t.Run("duplicate pending is 409", func(t *testing.T) {
		svc, u, g := newGymSvc(20)
		givenUser(u, gymUser("owner1", db.RoleGymOwner, db.Active, "o1@x.tn", false))
		gym := g.addGym("owner1", db.GymStatusActive)

		_, apiErr := svc.Invite(ctx, "owner1", gym.ID.Hex(), "a@x.tn")
		require.Nil(t, apiErr)
		_, apiErr = svc.Invite(ctx, "owner1", gym.ID.Hex(), "A@x.tn")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
		assert.EqualError(t, apiErr.Error, "already invited")
	})

	t.Run("expired pending does not block a new invitation", func(t *testing.T) {
		svc, u, g := newGymSvc(20)
		givenUser(u, gymUser("owner1", db.RoleGymOwner, db.Active, "o1@x.tn", false))
		gym := g.addGym("owner1", db.GymStatusActive)
		old := g.addInvite(gym, "a@x.tn", db.GymInvitePending, time.Now().Add(-time.Minute))

		_, apiErr := svc.Invite(ctx, "owner1", gym.ID.Hex(), "a@x.tn")
		require.Nil(t, apiErr)
		assert.Equal(t, db.GymInviteExpired, old.Status)
	})

	t.Run("rate limit per gym", func(t *testing.T) {
		svc, u, g := newGymSvc(2)
		givenUser(u, gymUser("owner1", db.RoleGymOwner, db.Active, "o1@x.tn", false))
		gym := g.addGym("owner1", db.GymStatusActive)

		for _, e := range []string{"a@x.tn", "b@x.tn"} {
			_, apiErr := svc.Invite(ctx, "owner1", gym.ID.Hex(), e)
			require.Nil(t, apiErr)
		}
		_, apiErr := svc.Invite(ctx, "owner1", gym.ID.Hex(), "c@x.tn")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusTooManyRequests, apiErr.Code)
		assert.Len(t, g.invites, 2)
	})

	t.Run("invalid email and archived gym", func(t *testing.T) {
		svc, u, g := newGymSvc(20)
		givenUser(u, gymUser("owner1", db.RoleGymOwner, db.Active, "o1@x.tn", false))
		gym := g.addGym("owner1", db.GymStatusActive)
		_, apiErr := svc.Invite(ctx, "owner1", gym.ID.Hex(), "not-an-email")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code)

		archived := g.addGym("owner1", db.GymStatusArchived)
		_, apiErr = svc.Invite(ctx, "owner1", archived.ID.Hex(), "a@x.tn")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
	})

	t.Run("revoke", func(t *testing.T) {
		svc, u, g := newGymSvc(20)
		givenUser(u, gymUser("owner1", db.RoleGymOwner, db.Active, "o1@x.tn", false))
		gym := g.addGym("owner1", db.GymStatusActive)
		inv := g.addInvite(gym, "a@x.tn", db.GymInvitePending, time.Now().Add(time.Hour))
		foreign := g.addInvite(g.addGym("owner2", db.GymStatusActive), "a@x.tn", db.GymInvitePending, time.Now().Add(time.Hour))

		apiErr := svc.RevokeInvitation(ctx, "owner1", gym.ID.Hex(), foreign.ID.Hex())
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)

		require.Nil(t, svc.RevokeInvitation(ctx, "owner1", gym.ID.Hex(), inv.ID.Hex()))
		assert.Equal(t, db.GymInviteRevoked, inv.Status)
		apiErr = svc.RevokeInvitation(ctx, "owner1", gym.ID.Hex(), inv.ID.Hex())
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
	})
}

func TestAcceptInvitation(t *testing.T) {
	ctx := context.Background()
	future := time.Now().Add(time.Hour)

	setup := func() (services.GymService, *mocks.TeamUserStore, *fakeGymStore, *db.Gym, *db.GymInvitation) {
		svc, u, g := newGymSvc(20)
		gym := g.addGym("owner1", db.GymStatusActive)
		inv := g.addInvite(gym, "coach@x.tn", db.GymInvitePending, future)
		return svc, u, g, gym, inv
	}

	t.Run("ok promotes with the owner as promoter and joins the gym", func(t *testing.T) {
		svc, u, g, gym, inv := setup()
		givenUser(u, gymUser("c1", "coach", db.Active, "Coach@X.tn", false))
		u.On("SetHeadCoach", mock.Anything, "c1", true, "owner1").Return(nil)

		joined, apiErr := svc.AcceptInvitation(ctx, "c1", inv.ID.Hex())
		require.Nil(t, apiErr)
		assert.Equal(t, gym.ID.Hex(), joined.GymID)
		assert.Equal(t, db.GymInviteAccepted, inv.Status)
		assert.Equal(t, "c1", inv.InviteeID)
		require.NotNil(t, g.heads["c1"])
		assert.Equal(t, gym.ID.Hex(), g.heads["c1"].GymID)
		assert.Equal(t, []string{"open:c1:gym_add"}, g.periods)
		assert.Equal(t, []string{"promote", "gym_invite_accept"}, g.actions())
		u.AssertExpectations(t)
	})

	t.Run("already head coach keeps the flag untouched", func(t *testing.T) {
		svc, u, g, _, inv := setup()
		givenUser(u, gymUser("c1", "coach", db.Active, "coach@x.tn", true))

		_, apiErr := svc.AcceptInvitation(ctx, "c1", inv.ID.Hex())
		require.Nil(t, apiErr)
		u.AssertNotCalled(t, "SetHeadCoach", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
		assert.Equal(t, []string{"gym_invite_accept"}, g.actions())
	})

	t.Run("member of a team is 409", func(t *testing.T) {
		svc, u, g, _, inv := setup()
		givenUser(u, gymUser("c1", "coach", db.Active, "coach@x.tn", false))
		g.team["c1"] = true

		_, apiErr := svc.AcceptInvitation(ctx, "c1", inv.ID.Hex())
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
		assert.EqualError(t, apiErr.Error, "coach belongs to a team, unassign first")
		u.AssertNotCalled(t, "SetHeadCoach", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
		assert.Equal(t, db.GymInvitePending, inv.Status)
	})

	t.Run("already in a gym is 409", func(t *testing.T) {
		svc, u, g, _, inv := setup()
		givenUser(u, gymUser("c1", "coach", db.Active, "coach@x.tn", true))
		g.heads["c1"] = &db.GymHeadCoach{GymID: "some-other-gym", HeadCoachID: "c1"}

		_, apiErr := svc.AcceptInvitation(ctx, "c1", inv.ID.Hex())
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
		assert.Equal(t, db.GymInvitePending, inv.Status)
	})

	t.Run("wrong email is 404 without leaking", func(t *testing.T) {
		svc, u, _, _, inv := setup()
		givenUser(u, gymUser("c2", "coach", db.Active, "other@x.tn", false))

		_, apiErr := svc.AcceptInvitation(ctx, "c2", inv.ID.Hex())
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)
		assert.EqualError(t, apiErr.Error, "invitation not found")
		assert.NotContains(t, apiErr.Error.Error(), "coach@x.tn")

		_, apiErr = svc.AcceptInvitation(ctx, "c2", "does-not-exist")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)
	})

	t.Run("expired is marked and rejected", func(t *testing.T) {
		svc, u, g := newGymSvc(20)
		gym := g.addGym("owner1", db.GymStatusActive)
		inv := g.addInvite(gym, "coach@x.tn", db.GymInvitePending, time.Now().Add(-time.Minute))
		givenUser(u, gymUser("c1", "coach", db.Active, "coach@x.tn", false))

		_, apiErr := svc.AcceptInvitation(ctx, "c1", inv.ID.Hex())
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
		assert.EqualError(t, apiErr.Error, "invitation expired")
		assert.Equal(t, db.GymInviteExpired, inv.Status)
		assert.Empty(t, g.heads)
	})

	t.Run("not pending, archived gym, non coach", func(t *testing.T) {
		svc, u, g, gym, inv := setup()
		givenUser(u, gymUser("c1", "coach", db.Active, "coach@x.tn", false))
		inv.Status = db.GymInviteRevoked
		_, apiErr := svc.AcceptInvitation(ctx, "c1", inv.ID.Hex())
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)

		inv.Status = db.GymInvitePending
		gym.Status = db.GymStatusArchived
		_, apiErr = svc.AcceptInvitation(ctx, "c1", inv.ID.Hex())
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)
		assert.Empty(t, g.heads)

		svc2, u2, _ := newGymSvc(20)
		givenUser(u2, gymUser("n1", "nutritionist", db.Active, "coach@x.tn", false))
		_, apiErr = svc2.AcceptInvitation(ctx, "n1", inv.ID.Hex())
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusForbidden, apiErr.Code)
	})

	t.Run("decline", func(t *testing.T) {
		svc, u, g, _, inv := setup()
		givenUser(u, gymUser("c1", "coach", db.Active, "coach@x.tn", false))
		givenUser(u, gymUser("c2", "coach", db.Active, "other@x.tn", false))

		apiErr := svc.DeclineInvitation(ctx, "c2", inv.ID.Hex())
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)

		require.Nil(t, svc.DeclineInvitation(ctx, "c1", inv.ID.Hex()))
		assert.Equal(t, db.GymInviteDeclined, inv.Status)
		assert.Equal(t, []string{"gym_invite_decline"}, g.actions())
		u.AssertNotCalled(t, "SetHeadCoach", mock.Anything, mock.Anything, mock.Anything, mock.Anything)

		apiErr = svc.DeclineInvitation(ctx, "c1", inv.ID.Hex())
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
	})
}

func TestOwnerRemovesHeadCoachKeepsFlag(t *testing.T) {
	ctx := context.Background()
	svc, u, g := newGymSvc(20)
	givenUser(u, gymUser("owner1", db.RoleGymOwner, db.Active, "o1@x.tn", false))
	gym := g.addGym("owner1", db.GymStatusActive)
	g.heads["h1"] = &db.GymHeadCoach{GymID: gym.ID.Hex(), HeadCoachID: "h1"}

	require.Nil(t, svc.RemoveHeadCoach(ctx, "owner1", gym.ID.Hex(), "h1"))
	assert.Empty(t, g.heads)
	assert.Equal(t, []string{"close:h1:gym_remove"}, g.periods)
	assert.Equal(t, []string{"gym_remove_head"}, g.actions())
	assert.Equal(t, db.TeamActorRoleGymOwner, g.audits[0].ActorRole)
	// The head coach flag and team are untouched: only an admin demotes.
	u.AssertNotCalled(t, "SetHeadCoach", mock.Anything, mock.Anything, mock.Anything, mock.Anything)

	apiErr := svc.RemoveHeadCoach(ctx, "owner1", gym.ID.Hex(), "h1")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusNotFound, apiErr.Code)
}

func TestHeadCoachGym(t *testing.T) {
	ctx := context.Background()

	t.Run("my gym and leave", func(t *testing.T) {
		svc, u, g := newGymSvc(20)
		givenUser(u, gymUser("h1", "coach", db.Active, "h@x.tn", true))
		givenUser(u, gymUser("owner1", db.RoleGymOwner, db.Active, "o1@x.tn", false))
		gym := g.addGym("owner1", db.GymStatusActive)
		g.heads["h1"] = &db.GymHeadCoach{GymID: gym.ID.Hex(), HeadCoachID: "h1"}

		info, apiErr := svc.MyGym(ctx, "h1")
		require.Nil(t, apiErr)
		require.NotNil(t, info)
		assert.Equal(t, "Iron Gym", info.Name)
		assert.Equal(t, "Firstowner1", info.Owner.Firstname)

		require.Nil(t, svc.LeaveGym(ctx, "h1"))
		assert.Equal(t, []string{"close:h1:gym_leave"}, g.periods)
		assert.Equal(t, []string{"gym_leave"}, g.actions())

		info, apiErr = svc.MyGym(ctx, "h1")
		require.Nil(t, apiErr)
		assert.Nil(t, info)
		apiErr = svc.LeaveGym(ctx, "h1")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)
	})

	t.Run("not a head coach is 403", func(t *testing.T) {
		svc, u, _ := newGymSvc(20)
		givenUser(u, gymUser("c1", "coach", db.Active, "c@x.tn", false))
		_, apiErr := svc.MyGym(ctx, "c1")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusForbidden, apiErr.Code)
	})
}

func TestDemoteRemovesGymMembership(t *testing.T) {
	ctx := context.Background()
	u, ts, g := &mocks.TeamUserStore{}, &mocks.TeamStore{}, newFakeGymStore()
	svc := services.NewTeamService(u, ts, services.WithGymMembership(g))
	g.heads["h1"] = &db.GymHeadCoach{GymID: "gym-1", HeadCoachID: "h1"}

	givenUser(u, coachUser("h1", true, db.Active))
	u.On("SetHeadCoach", mock.Anything, "h1", false, "admin1").Return(nil)
	ts.On("IDsByHead", mock.Anything, "h1").Return([]string{}, nil)
	ts.On("DeleteByHead", mock.Anything, "h1").Return(int64(0), nil)
	ts.On("InsertAuditMany", mock.Anything, mock.MatchedBy(func(es []*db.TeamAuditEntry) bool {
		return len(es) == 2 && es[0].Action == "demote" && es[1].Action == "gym_remove_head" && es[1].GymID == "gym-1"
	})).Return(nil)

	_, apiErr := svc.Demote(ctx, "admin1", "h1")
	require.Nil(t, apiErr)
	assert.Empty(t, g.heads)
	assert.Equal(t, []string{"close:h1:demote"}, g.periods)
	ts.AssertExpectations(t)
}

func TestAdminGyms(t *testing.T) {
	ctx := context.Background()

	t.Run("create requires an active gym owner", func(t *testing.T) {
		svc, u, g := newGymSvc(20)
		givenUser(u, gymUser("owner1", db.RoleGymOwner, db.Active, "o@x.tn", false))
		givenUser(u, gymUser("c1", "coach", db.Active, "c@x.tn", false))
		givenUser(u, gymUser("owner3", db.RoleGymOwner, db.Suspended, "o3@x.tn", false))
		u.On("GetUserByExternalIdUpdate", mock.Anything, "ghost").Return(nil, nil)

		v, apiErr := svc.AdminCreateGym(ctx, "admin1", "  Iron Gym ", "owner1")
		require.Nil(t, apiErr)
		assert.Equal(t, "Iron Gym", v.Name)
		assert.Equal(t, "TND", v.DefaultCurrency)
		assert.Equal(t, "Africa/Tunis", v.Timezone)
		assert.Equal(t, []string{"gym_create"}, g.actions())

		for _, owner := range []string{"c1", "owner3"} {
			_, apiErr = svc.AdminCreateGym(ctx, "admin1", "X", owner)
			require.NotNil(t, apiErr, owner)
			assert.Equal(t, http.StatusBadRequest, apiErr.Code)
		}
		_, apiErr = svc.AdminCreateGym(ctx, "admin1", "X", "ghost")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)
		_, apiErr = svc.AdminCreateGym(ctx, "admin1", "   ", "owner1")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code)
		_, apiErr = svc.AdminCreateGym(ctx, "admin1", strings.Repeat("x", 101), "owner1")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code)
	})

	t.Run("archive releases head coaches, closes periods and revokes invitations", func(t *testing.T) {
		svc, _, g := newGymSvc(20)
		gym := g.addGym("owner1", db.GymStatusActive)
		other := g.addGym("owner1", db.GymStatusActive)
		g.heads["h1"] = &db.GymHeadCoach{GymID: gym.ID.Hex(), HeadCoachID: "h1"}
		g.heads["h2"] = &db.GymHeadCoach{GymID: gym.ID.Hex(), HeadCoachID: "h2"}
		g.heads["h3"] = &db.GymHeadCoach{GymID: other.ID.Hex(), HeadCoachID: "h3"}
		inv := g.addInvite(gym, "a@x.tn", db.GymInvitePending, time.Now().Add(time.Hour))
		archived := db.GymStatusArchived

		v, apiErr := svc.AdminUpdateGym(ctx, "admin1", gym.ID.Hex(), services.GymAdminUpdate{Status: &archived})
		require.Nil(t, apiErr)
		assert.Equal(t, db.GymStatusArchived, v.Status)
		assert.Equal(t, db.GymStatusArchived, g.gyms[gym.ID.Hex()].Status)
		assert.Len(t, g.heads, 1)
		assert.NotNil(t, g.heads["h3"])
		assert.ElementsMatch(t, []string{"close:h1:gym_archive", "close:h2:gym_archive"}, g.periods)
		assert.Equal(t, db.GymInviteRevoked, inv.Status)
		assert.ElementsMatch(t, []string{"gym_archive", "gym_remove_head", "gym_remove_head"}, g.actions())
	})

	t.Run("transfer owner", func(t *testing.T) {
		svc, u, g := newGymSvc(20)
		givenUser(u, gymUser("owner2", db.RoleGymOwner, db.Active, "o2@x.tn", false))
		givenUser(u, gymUser("c1", "coach", db.Active, "c@x.tn", false))
		gym := g.addGym("owner1", db.GymStatusActive)

		bad := "c1"
		_, apiErr := svc.AdminUpdateGym(ctx, "admin1", gym.ID.Hex(), services.GymAdminUpdate{OwnerID: &bad})
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code)
		assert.Equal(t, "owner1", gym.OwnerID)

		good := "owner2"
		v, apiErr := svc.AdminUpdateGym(ctx, "admin1", gym.ID.Hex(), services.GymAdminUpdate{OwnerID: &good})
		require.Nil(t, apiErr)
		assert.Equal(t, "owner2", v.OwnerID)
		assert.Equal(t, "owner2", g.gyms[gym.ID.Hex()].OwnerID)
		require.Len(t, g.audits, 1)
		assert.Equal(t, "gym_transfer", g.audits[0].Action)
		assert.Equal(t, "owner1", g.audits[0].PrevOwnerID)
		assert.Equal(t, "owner2", g.audits[0].OwnerID)
	})

	t.Run("update validation", func(t *testing.T) {
		svc, _, g := newGymSvc(20)
		gym := g.addGym("owner1", db.GymStatusActive)
		_, apiErr := svc.AdminUpdateGym(ctx, "admin1", gym.ID.Hex(), services.GymAdminUpdate{})
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code)

		bad := "deleted"
		_, apiErr = svc.AdminUpdateGym(ctx, "admin1", gym.ID.Hex(), services.GymAdminUpdate{Status: &bad})
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code)

		name := "x"
		_, apiErr = svc.AdminUpdateGym(ctx, "admin1", "nope", services.GymAdminUpdate{Name: &name})
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)
	})

	t.Run("add and remove head coach", func(t *testing.T) {
		svc, u, g := newGymSvc(20)
		gym := g.addGym("owner1", db.GymStatusActive)
		other := g.addGym("owner1", db.GymStatusActive)
		givenUser(u, coachUser("h1", true, db.Active))
		givenUser(u, coachUser("c1", false, db.Active))
		givenUser(u, coachUser("h2", true, db.Blocked))
		givenUser(u, coachUser("h3", true, db.Active))
		g.heads["h3"] = &db.GymHeadCoach{GymID: other.ID.Hex(), HeadCoachID: "h3"}

		require.Nil(t, svc.AdminAddHeadCoach(ctx, "admin1", gym.ID.Hex(), "h1"))
		require.NotNil(t, g.heads["h1"])
		assert.Equal(t, "admin", g.heads["h1"].AddedByRole)
		assert.Equal(t, []string{"open:h1:gym_add"}, g.periods)
		require.Nil(t, svc.AdminAddHeadCoach(ctx, "admin1", gym.ID.Hex(), "h1"), "idempotent")
		assert.Equal(t, []string{"gym_add_head"}, g.actions())

		for _, id := range []string{"c1", "h2"} {
			apiErr := svc.AdminAddHeadCoach(ctx, "admin1", gym.ID.Hex(), id)
			require.NotNil(t, apiErr, id)
			assert.Equal(t, http.StatusConflict, apiErr.Code)
		}
		apiErr := svc.AdminAddHeadCoach(ctx, "admin1", gym.ID.Hex(), "h3")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
		assert.EqualError(t, apiErr.Error, "head coach already belongs to another gym")

		require.Nil(t, svc.AdminRemoveHeadCoach(ctx, "admin1", gym.ID.Hex(), "h1"))
		apiErr = svc.AdminRemoveHeadCoach(ctx, "admin1", gym.ID.Hex(), "h1")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)
	})
}

func TestAdminListGymsPassesStatus(t *testing.T) {
	svc, _, g := newGymSvc(20)
	_, _, apiErr := svc.AdminListGyms(context.Background(), 1, 20, "iron", db.GymStatusArchived)
	require.Nil(t, apiErr)
	assert.Equal(t, db.GymStatusArchived, g.lastStatus)
}

func TestAdminListGymHeadCoaches(t *testing.T) {
	ctx := context.Background()
	svc, _, g := newGymSvc(20)
	gym := g.addGym("owner1", db.GymStatusActive)
	other := g.addGym("owner1", db.GymStatusActive)
	g.heads["h1"] = &db.GymHeadCoach{GymID: gym.ID.Hex(), HeadCoachID: "h1", AddedByRole: db.GymAdderAdmin}
	g.heads["h2"] = &db.GymHeadCoach{GymID: other.ID.Hex(), HeadCoachID: "h2"}

	rows, apiErr := svc.AdminListGymHeadCoaches(ctx, gym.ID.Hex())
	require.Nil(t, apiErr)
	require.Len(t, rows, 1)
	assert.Equal(t, "h1", rows[0].ExternalID)
	assert.Equal(t, db.GymAdderAdmin, rows[0].AddedByRole)

	_, apiErr = svc.AdminListGymHeadCoaches(ctx, "missing")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusNotFound, apiErr.Code)
	assert.Equal(t, "gym not found", apiErr.Error.Error())

	g.failOn = "GetGym"
	_, apiErr = svc.AdminListGymHeadCoaches(ctx, gym.ID.Hex())
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
	assert.NotContains(t, apiErr.Error.Error(), "10.0.0.5")

	g.failOn = "ListGymHeadCoachesAdmin"
	_, apiErr = svc.AdminListGymHeadCoaches(ctx, gym.ID.Hex())
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
}

// ListMyInvitations is a pure read: it returns a non-nil list and never flips stored statuses.
func TestListMyInvitationsIsReadOnly(t *testing.T) {
	ctx := context.Background()
	svc, u, g := newGymSvc(10)
	givenUser(u, gymUser("c1", "coach", db.Active, "C1@Mail.com", false))
	id := primitive.NewObjectID()
	g.invites[id.Hex()] = &db.GymInvitation{ID: id, GymID: "g1", Email: "c1@mail.com",
		Status: db.GymInvitePending, ExpiresAt: time.Now().Add(-time.Hour)}

	views, apiErr := svc.ListMyInvitations(ctx, "c1")
	require.Nil(t, apiErr)
	assert.NotNil(t, views)
	assert.Equal(t, db.GymInvitePending, g.invites[id.Hex()].Status)
}
