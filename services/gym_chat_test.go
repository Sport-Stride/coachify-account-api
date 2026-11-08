package services_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/services"
	"coachify-account-api/services/mocks"
)

// fakeChat records EnsureConversation calls as "owner:peer"; peers in fail return an error.
type fakeChat struct {
	calls []string
	fail  map[string]bool
}

func (f *fakeChat) EnsureConversation(_ context.Context, adminID, peerID string) *models.ApiError {
	f.calls = append(f.calls, adminID+":"+peerID)
	if f.fail[peerID] {
		return models.NewApiError(http.StatusInternalServerError, errors.New("boom"))
	}
	return nil
}

// fakeChatStore is an in-memory services.GymChatStore.
type fakeChatStore struct {
	owners map[string]string   // head id -> owner id
	teams  map[string][]string // head id -> coach ids
	gyms   []db.GymChatPeers
}

func (f *fakeChatStore) OwnerOfHead(_ context.Context, headID string) (string, error) {
	return f.owners[headID], nil
}
func (f *fakeChatStore) IDsByHead(_ context.Context, headID string) ([]string, error) {
	return f.teams[headID], nil
}
func (f *fakeChatStore) ListGymChatPeers(context.Context, string) ([]db.GymChatPeers, error) {
	return f.gyms, nil
}

func syncSpawn(f func()) { f() }

func newProvisioner(store *fakeChatStore, chat *fakeChat) *services.GymChatProvisioner {
	return services.NewGymChatProvisioner(store, chat, syncSpawn)
}

func TestGymChatProvisioner(t *testing.T) {
	ctx := context.Background()

	t.Run("nil provisioner is a no-op", func(t *testing.T) {
		var p *services.GymChatProvisioner
		p.HeadJoined(ctx, "o", "h")
		p.CoachAssigned(ctx, "h", "c")
		p.GymOwnerChanged(ctx, "g")
		pairs, created, failed := p.Backfill(ctx, "", false)
		assert.Zero(t, pairs+created+failed)
	})

	t.Run("head joined opens owner with head and each coach", func(t *testing.T) {
		chat := &fakeChat{}
		p := newProvisioner(&fakeChatStore{teams: map[string][]string{"h1": {"c1", "c2"}}}, chat)
		p.HeadJoined(ctx, "o1", "h1")
		assert.Equal(t, []string{"o1:h1", "o1:c1", "o1:c2"}, chat.calls)
	})

	t.Run("coach assigned without a gym makes no call", func(t *testing.T) {
		chat := &fakeChat{}
		p := newProvisioner(&fakeChatStore{}, chat)
		p.CoachAssigned(ctx, "h1", "c1")
		assert.Empty(t, chat.calls)
	})

	t.Run("coach assigned opens owner with coach", func(t *testing.T) {
		chat := &fakeChat{}
		p := newProvisioner(&fakeChatStore{owners: map[string]string{"h1": "o1"}}, chat)
		p.CoachAssigned(ctx, "h1", "c1")
		assert.Equal(t, []string{"o1:c1"}, chat.calls)
	})

	t.Run("chat error does not stop the rest", func(t *testing.T) {
		chat := &fakeChat{fail: map[string]bool{"h1": true}}
		p := newProvisioner(&fakeChatStore{teams: map[string][]string{"h1": {"c1"}}}, chat)
		p.HeadJoined(ctx, "o1", "h1")
		assert.Equal(t, []string{"o1:h1", "o1:c1"}, chat.calls)
	})

	t.Run("self pairs and empty ids are skipped", func(t *testing.T) {
		chat := &fakeChat{}
		p := newProvisioner(&fakeChatStore{teams: map[string][]string{"h1": {"o1", ""}}}, chat)
		p.HeadJoined(ctx, "o1", "h1")
		assert.Equal(t, []string{"o1:h1"}, chat.calls)
	})

	t.Run("owner changed opens new owner with every head and coach", func(t *testing.T) {
		chat := &fakeChat{}
		p := newProvisioner(&fakeChatStore{gyms: []db.GymChatPeers{
			{GymID: "g1", OwnerID: "o2", HeadIDs: []string{"h1"}, CoachIDs: []string{"c1", "o2"}},
		}}, chat)
		p.GymOwnerChanged(ctx, "g1")
		assert.Equal(t, []string{"o2:h1", "o2:c1"}, chat.calls)
	})

	t.Run("backfill counts and dry run makes no calls", func(t *testing.T) {
		gyms := []db.GymChatPeers{
			{GymID: "g1", OwnerID: "o1", HeadIDs: []string{"h1", "h2"}, CoachIDs: []string{"c1"}},
			{GymID: "g2", OwnerID: "o2", HeadIDs: []string{"h3"}},
		}
		chat := &fakeChat{fail: map[string]bool{"h2": true}}
		p := newProvisioner(&fakeChatStore{gyms: gyms}, chat)

		pairs, created, failed := p.Backfill(ctx, "", true)
		assert.Equal(t, []int{4, 0, 0}, []int{pairs, created, failed})
		assert.Empty(t, chat.calls)

		pairs, created, failed = p.Backfill(ctx, "", false)
		assert.Equal(t, []int{4, 3, 1}, []int{pairs, created, failed})
		assert.Len(t, chat.calls, 4)
	})
}

func gymWithChat(chat *fakeChat, store *fakeChatStore) (services.GymService, *mocks.TeamUserStore, *fakeGymStore) {
	u, g := &mocks.TeamUserStore{}, newFakeGymStore()
	return services.NewGymService(u, g, services.GymSettings{
		InviteTTL: time.Hour, InvitesPerDay: 20, Chat: newProvisioner(store, chat),
	}), u, g
}

func TestGymHooksOpenChats(t *testing.T) {
	ctx := context.Background()
	future := time.Now().Add(time.Hour)

	t.Run("accept invitation opens owner chats", func(t *testing.T) {
		chat := &fakeChat{}
		svc, u, g := gymWithChat(chat, &fakeChatStore{teams: map[string][]string{"c1": {"t1"}}})
		gym := g.addGym("owner1", db.GymStatusActive)
		inv := g.addInvite(gym, "coach@x.tn", db.GymInvitePending, future)
		givenUser(u, gymUser("c1", "coach", db.Active, "coach@x.tn", true))

		_, apiErr := svc.AcceptInvitation(ctx, "c1", inv.ID.Hex())
		require.Nil(t, apiErr)
		assert.Equal(t, []string{"owner1:c1", "owner1:t1"}, chat.calls)
	})

	t.Run("accept conflict opens nothing", func(t *testing.T) {
		chat := &fakeChat{}
		svc, u, g := gymWithChat(chat, &fakeChatStore{})
		gym := g.addGym("owner1", db.GymStatusActive)
		inv := g.addInvite(gym, "coach@x.tn", db.GymInvitePending, future)
		givenUser(u, gymUser("c1", "coach", db.Active, "coach@x.tn", true))
		g.heads["c1"] = &db.GymHeadCoach{GymID: "some-other-gym", HeadCoachID: "c1"}

		_, apiErr := svc.AcceptInvitation(ctx, "c1", inv.ID.Hex())
		require.NotNil(t, apiErr)
		assert.Empty(t, chat.calls)
	})

	t.Run("admin add head opens chats once, not on the idempotent retry", func(t *testing.T) {
		chat := &fakeChat{}
		svc, u, g := gymWithChat(chat, &fakeChatStore{})
		gym := g.addGym("owner1", db.GymStatusActive)
		givenUser(u, coachUser("h1", true, db.Active))

		require.Nil(t, svc.AdminAddHeadCoach(ctx, "admin1", gym.ID.Hex(), "h1"))
		require.Nil(t, svc.AdminAddHeadCoach(ctx, "admin1", gym.ID.Hex(), "h1"))
		assert.Equal(t, []string{"owner1:h1"}, chat.calls)
	})

	t.Run("admin add head conflict opens nothing", func(t *testing.T) {
		chat := &fakeChat{}
		svc, u, g := gymWithChat(chat, &fakeChatStore{})
		gym := g.addGym("owner1", db.GymStatusActive)
		other := g.addGym("owner2", db.GymStatusActive)
		givenUser(u, coachUser("h3", true, db.Active))
		g.heads["h3"] = &db.GymHeadCoach{GymID: other.ID.Hex(), HeadCoachID: "h3"}

		require.NotNil(t, svc.AdminAddHeadCoach(ctx, "admin1", gym.ID.Hex(), "h3"))
		assert.Empty(t, chat.calls)
	})

	t.Run("owner transfer opens chats, rename does not", func(t *testing.T) {
		chat := &fakeChat{}
		store := &fakeChatStore{gyms: []db.GymChatPeers{{GymID: "g", OwnerID: "owner2", HeadIDs: []string{"h1"}}}}
		svc, u, g := gymWithChat(chat, store)
		givenUser(u, gymUser("owner2", db.RoleGymOwner, db.Active, "o2@x.tn", false))
		gym := g.addGym("owner1", db.GymStatusActive)

		name := "Renamed"
		_, apiErr := svc.AdminUpdateGym(ctx, "admin1", gym.ID.Hex(), services.GymAdminUpdate{Name: &name})
		require.Nil(t, apiErr)
		assert.Empty(t, chat.calls)

		owner := "owner2"
		_, apiErr = svc.AdminUpdateGym(ctx, "admin1", gym.ID.Hex(), services.GymAdminUpdate{OwnerID: &owner})
		require.Nil(t, apiErr)
		assert.Equal(t, []string{"owner2:h1"}, chat.calls)
	})
}

func TestTeamAssignOpensOwnerChat(t *testing.T) {
	ctx := context.Background()
	setup := func() (services.TeamService, *mocks.TeamStore, *fakeChat) {
		u, ts, chat := &mocks.TeamUserStore{}, &mocks.TeamStore{}, &fakeChat{}
		givenUser(u, coachUser("c1", false, db.Active))
		givenUser(u, coachUser("h1", true, db.Active))
		p := newProvisioner(&fakeChatStore{owners: map[string]string{"h1": "owner1"}}, chat)
		return services.NewTeamService(u, ts, services.WithGymChat(p)), ts, chat
	}

	t.Run("new assignment opens the chat", func(t *testing.T) {
		svc, ts, chat := setup()
		ts.On("Upsert", mock.Anything, mock.Anything).Return(nil, nil)
		ts.On("InsertAudit", mock.Anything, mock.Anything).Return(nil)
		_, apiErr := svc.Assign(ctx, "admin1", "c1", "h1")
		require.Nil(t, apiErr)
		assert.Equal(t, []string{"owner1:c1"}, chat.calls)
	})

	t.Run("same head again opens nothing", func(t *testing.T) {
		svc, ts, chat := setup()
		ts.On("Upsert", mock.Anything, mock.Anything).Return(&db.TeamMember{CoachID: "c1", HeadCoachID: "h1"}, nil)
		_, apiErr := svc.Assign(ctx, "admin1", "c1", "h1")
		require.Nil(t, apiErr)
		assert.Empty(t, chat.calls)
	})

	t.Run("rolled back assignment opens nothing", func(t *testing.T) {
		u, ts, chat := &mocks.TeamUserStore{}, &mocks.TeamStore{}, &fakeChat{}
		givenUser(u, coachUser("c1", false, db.Active))
		u.On("GetUserByExternalIdUpdate", mock.Anything, "h1").Return(coachUser("h1", true, db.Active), nil).Once()
		u.On("GetUserByExternalIdUpdate", mock.Anything, "h1").Return(coachUser("h1", false, db.Active), nil)
		ts.On("Upsert", mock.Anything, mock.Anything).Return(nil, nil)
		ts.On("DeleteByCoach", mock.Anything, "c1").Return(int64(1), nil)
		p := newProvisioner(&fakeChatStore{owners: map[string]string{"h1": "owner1"}}, chat)
		svc := services.NewTeamService(u, ts, services.WithGymChat(p))

		_, apiErr := svc.Assign(ctx, "admin1", "c1", "h1")
		require.NotNil(t, apiErr)
		assert.Empty(t, chat.calls)
	})
}
