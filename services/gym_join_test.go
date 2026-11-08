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

	"coachify-account-api/models/db"
	"coachify-account-api/services"
	"coachify-account-api/services/mocks"
	"coachify-account-api/utils"
)

// fakeJoinMailer records the join emails and can fail.
type fakeJoinMailer struct {
	err   error
	sent  int
	email string
	gym   string
	link  string
}

func (m *fakeJoinMailer) SendGymJoinInviteEmail(_ context.Context, email, gymName, link string) error {
	m.sent++
	m.email, m.gym, m.link = email, gymName, link
	return m.err
}

func newJoinSvc(mailer services.GymJoinMailer) (services.GymService, *mocks.TeamUserStore, *fakeGymStore) {
	u, g := &mocks.TeamUserStore{}, newFakeGymStore()
	return services.NewGymService(u, g, services.GymSettings{
		InviteTTL: time.Hour, InvitesPerDay: 20, ClientURL: "https://app.test/", Mailer: mailer,
	}), u, g
}

// addTokenInvite stores an invitation reachable through the returned raw token.
func addTokenInvite(g *fakeGymStore, gym *db.Gym, email, status string, expires time.Time) (string, *db.GymInvitation) {
	raw, hash, _ := utils.NewURLToken()
	inv := g.addInvite(gym, email, status, expires)
	inv.TokenHash = hash
	return raw, inv
}

func tokenFromLink(t *testing.T, link string) string {
	t.Helper()
	i := strings.Index(link, "gym_join=")
	require.GreaterOrEqual(t, i, 0)
	return link[i+len("gym_join="):]
}

func TestInviteEmailsJoinLink(t *testing.T) {
	ctx := context.Background()

	t.Run("sends a link that redeems the invitation, answer stays generic", func(t *testing.T) {
		m := &fakeJoinMailer{}
		svc, u, g := newJoinSvc(m)
		gym := g.addGym("owner1", db.GymStatusActive)
		givenUser(u, gymUser("owner1", db.RoleGymOwner, db.Active, "o@x.tn", false))

		res, apiErr := svc.Invite(ctx, "owner1", gym.ID.Hex(), "New@X.tn")
		require.Nil(t, apiErr)
		assert.Equal(t, db.GymInvitePending, res.Status)
		require.Equal(t, 1, m.sent)
		assert.Equal(t, "new@x.tn", m.email)
		assert.Equal(t, "Iron Gym", m.gym)
		assert.True(t, strings.HasPrefix(m.link, "https://app.test/auth/signup?gym_join="))

		lk, apiErr := svc.LookupJoin(ctx, tokenFromLink(t, m.link))
		require.Nil(t, apiErr)
		assert.Equal(t, "new@x.tn", lk.Email)
		assert.Equal(t, "Iron Gym", lk.GymName)
	})

	t.Run("mail failure keeps the invitation and succeeds", func(t *testing.T) {
		m := &fakeJoinMailer{err: errors.New("mailgun down")}
		svc, u, g := newJoinSvc(m)
		gym := g.addGym("owner1", db.GymStatusActive)
		givenUser(u, gymUser("owner1", db.RoleGymOwner, db.Active, "o@x.tn", false))

		res, apiErr := svc.Invite(ctx, "owner1", gym.ID.Hex(), "new@x.tn")
		require.Nil(t, apiErr)
		assert.Equal(t, 1, m.sent)
		assert.Len(t, g.invites, 1)
		assert.Equal(t, db.GymInvitePending, g.invites[res.InvitationID].Status)
	})

	t.Run("no mailer configured still creates the invitation", func(t *testing.T) {
		svc, u, g := newJoinSvc(nil)
		gym := g.addGym("owner1", db.GymStatusActive)
		givenUser(u, gymUser("owner1", db.RoleGymOwner, db.Active, "o@x.tn", false))

		_, apiErr := svc.Invite(ctx, "owner1", gym.ID.Hex(), "new@x.tn")
		require.Nil(t, apiErr)
		assert.Len(t, g.invites, 1)
	})
}

func TestCreateJoinLink(t *testing.T) {
	ctx := context.Background()

	t.Run("ok returns a link, stores only the hash, several open links allowed", func(t *testing.T) {
		svc, u, g := newJoinSvc(nil)
		gym := g.addGym("owner1", db.GymStatusActive)
		givenUser(u, gymUser("owner1", db.RoleGymOwner, db.Active, "o@x.tn", false))

		first, apiErr := svc.CreateJoinLink(ctx, "owner1", gym.ID.Hex())
		require.Nil(t, apiErr)
		second, apiErr := svc.CreateJoinLink(ctx, "owner1", gym.ID.Hex())
		require.Nil(t, apiErr)
		assert.NotEqual(t, first.InvitationID, second.InvitationID)
		assert.True(t, strings.HasPrefix(first.Link, "https://app.test/auth/signup?gym_join="))
		assert.True(t, first.ExpiresAt.After(time.Now()))

		raw := tokenFromLink(t, first.Link)
		stored := g.invites[first.InvitationID]
		assert.Equal(t, "", stored.Email)
		assert.Equal(t, utils.HashToken(raw), stored.TokenHash)
		assert.NotEqual(t, raw, stored.TokenHash)
	})

	t.Run("foreign gym 404, archived gym 409, non owner 403", func(t *testing.T) {
		svc, u, g := newJoinSvc(nil)
		givenUser(u, gymUser("owner1", db.RoleGymOwner, db.Active, "o@x.tn", false))
		foreign := g.addGym("owner2", db.GymStatusActive)
		archived := g.addGym("owner1", db.GymStatusArchived)

		_, apiErr := svc.CreateJoinLink(ctx, "owner1", foreign.ID.Hex())
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)

		_, apiErr = svc.CreateJoinLink(ctx, "owner1", archived.ID.Hex())
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)

		givenUser(u, gymUser("coach1", "coach", db.Active, "c@x.tn", false))
		_, apiErr = svc.CreateJoinLink(ctx, "coach1", archived.ID.Hex())
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusForbidden, apiErr.Code)
	})

	t.Run("counts against the daily limit", func(t *testing.T) {
		u, g := &mocks.TeamUserStore{}, newFakeGymStore()
		svc := services.NewGymService(u, g, services.GymSettings{InviteTTL: time.Hour, InvitesPerDay: 1})
		gym := g.addGym("owner1", db.GymStatusActive)
		givenUser(u, gymUser("owner1", db.RoleGymOwner, db.Active, "o@x.tn", false))

		_, apiErr := svc.CreateJoinLink(ctx, "owner1", gym.ID.Hex())
		require.Nil(t, apiErr)
		_, apiErr = svc.CreateJoinLink(ctx, "owner1", gym.ID.Hex())
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusTooManyRequests, apiErr.Code)
	})
}

func TestLookupJoin(t *testing.T) {
	ctx := context.Background()
	future := time.Now().Add(time.Hour)

	t.Run("ok for open and email-bound links", func(t *testing.T) {
		svc, _, g := newJoinSvc(nil)
		gym := g.addGym("owner1", db.GymStatusActive)
		open, _ := addTokenInvite(g, gym, "", db.GymInvitePending, future)
		bound, _ := addTokenInvite(g, gym, "c@x.tn", db.GymInvitePending, future)

		lk, apiErr := svc.LookupJoin(ctx, open)
		require.Nil(t, apiErr)
		assert.Equal(t, services.JoinLookup{GymName: "Iron Gym", Email: ""}, *lk)
		lk, apiErr = svc.LookupJoin(ctx, bound)
		require.Nil(t, apiErr)
		assert.Equal(t, "c@x.tn", lk.Email)
	})

	t.Run("invalid, expired, revoked, used and archived are one generic 404", func(t *testing.T) {
		svc, _, g := newJoinSvc(nil)
		gym := g.addGym("owner1", db.GymStatusActive)
		archivedGym := g.addGym("owner1", db.GymStatusArchived)
		expired, _ := addTokenInvite(g, gym, "", db.GymInvitePending, time.Now().Add(-time.Minute))
		revoked, _ := addTokenInvite(g, gym, "", db.GymInviteRevoked, future)
		used, _ := addTokenInvite(g, gym, "", db.GymInviteAccepted, future)
		archived, _ := addTokenInvite(g, archivedGym, "", db.GymInvitePending, future)

		for name, tok := range map[string]string{
			"empty": "", "unknown": "nope", "expired": expired, "revoked": revoked, "used": used, "archived": archived,
		} {
			_, apiErr := svc.LookupJoin(ctx, tok)
			require.NotNil(t, apiErr, name)
			assert.Equal(t, http.StatusNotFound, apiErr.Code, name)
			assert.EqualError(t, apiErr.Error, "invitation not found", name)
		}
	})

	t.Run("store failure is a generic 500", func(t *testing.T) {
		svc, _, g := newJoinSvc(nil)
		g.failOn = "GetInvitationByTokenHash"
		_, apiErr := svc.LookupJoin(ctx, "tok")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
		assert.NotContains(t, apiErr.Error.Error(), "10.0.0.5")
	})
}

func TestAcceptByToken(t *testing.T) {
	ctx := context.Background()
	future := time.Now().Add(time.Hour)

	setup := func(email string) (services.GymService, *mocks.TeamUserStore, *fakeGymStore, *db.Gym, string, *db.GymInvitation) {
		svc, u, g := newJoinSvc(nil)
		gym := g.addGym("owner1", db.GymStatusActive)
		raw, inv := addTokenInvite(g, gym, email, db.GymInvitePending, future)
		return svc, u, g, gym, raw, inv
	}

	t.Run("open link binds to the caller", func(t *testing.T) {
		svc, u, g, gym, raw, inv := setup("")
		givenUser(u, gymUser("c1", "coach", db.Active, "any@x.tn", false))
		u.On("SetHeadCoach", mock.Anything, "c1", true, "owner1").Return(nil)

		joined, apiErr := svc.AcceptByToken(ctx, "c1", raw)
		require.Nil(t, apiErr)
		assert.Equal(t, gym.ID.Hex(), joined.GymID)
		assert.Equal(t, "Iron Gym", joined.Name)
		assert.Equal(t, db.GymInviteAccepted, inv.Status)
		assert.Equal(t, "c1", inv.InviteeID)
		require.NotNil(t, g.heads["c1"])

		// single use
		givenUser(u, gymUser("c2", "coach", db.Active, "two@x.tn", false))
		_, apiErr = svc.AcceptByToken(ctx, "c2", raw)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
	})

	t.Run("email-bound link accepts the matching email", func(t *testing.T) {
		svc, u, _, _, raw, inv := setup("coach@x.tn")
		givenUser(u, gymUser("c1", "coach", db.Active, "Coach@X.tn", false))
		u.On("SetHeadCoach", mock.Anything, "c1", true, "owner1").Return(nil)

		_, apiErr := svc.AcceptByToken(ctx, "c1", raw)
		require.Nil(t, apiErr)
		assert.Equal(t, db.GymInviteAccepted, inv.Status)
	})

	t.Run("email mismatch is 403 and leaves the invitation pending", func(t *testing.T) {
		svc, u, g, _, raw, inv := setup("coach@x.tn")
		givenUser(u, gymUser("c2", "coach", db.Active, "other@x.tn", false))

		_, apiErr := svc.AcceptByToken(ctx, "c2", raw)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusForbidden, apiErr.Code)
		assert.NotContains(t, apiErr.Error.Error(), "coach@x.tn")
		assert.Equal(t, db.GymInvitePending, inv.Status)
		assert.Empty(t, g.heads)
	})

	t.Run("invalid token 404, expired and revoked 409", func(t *testing.T) {
		svc, u, g, gym, _, _ := setup("")
		givenUser(u, gymUser("c1", "coach", db.Active, "c@x.tn", false))
		expired, expInv := addTokenInvite(g, gym, "", db.GymInvitePending, time.Now().Add(-time.Minute))
		revoked, _ := addTokenInvite(g, gym, "", db.GymInviteRevoked, future)

		_, apiErr := svc.AcceptByToken(ctx, "c1", "unknown")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)

		_, apiErr = svc.AcceptByToken(ctx, "c1", expired)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
		assert.Equal(t, db.GymInviteExpired, expInv.Status)

		_, apiErr = svc.AcceptByToken(ctx, "c1", revoked)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
	})

	t.Run("already in a gym is 409 and the link stays usable", func(t *testing.T) {
		svc, u, g, _, raw, inv := setup("")
		givenUser(u, gymUser("c1", "coach", db.Active, "c@x.tn", true))
		g.heads["c1"] = &db.GymHeadCoach{GymID: "some-other-gym", HeadCoachID: "c1"}

		_, apiErr := svc.AcceptByToken(ctx, "c1", raw)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
		assert.Equal(t, db.GymInvitePending, inv.Status)
	})

	t.Run("non coach is 403", func(t *testing.T) {
		svc, u, _, _, raw, _ := setup("")
		givenUser(u, gymUser("o2", db.RoleGymOwner, db.Active, "o2@x.tn", false))

		_, apiErr := svc.AcceptByToken(ctx, "o2", raw)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusForbidden, apiErr.Code)
	})
}

func TestJoinOnSignup(t *testing.T) {
	ctx := context.Background()
	future := time.Now().Add(time.Hour)

	t.Run("joins a coach that is not Active yet", func(t *testing.T) {
		svc, u, g := newJoinSvc(nil)
		gym := g.addGym("owner1", db.GymStatusActive)
		raw, inv := addTokenInvite(g, gym, "new@x.tn", db.GymInvitePending, future)
		givenUser(u, gymUser("c1", "coach", db.ComReg1, "new@x.tn", false))
		u.On("SetHeadCoach", mock.Anything, "c1", true, "owner1").Return(nil)

		require.Nil(t, svc.JoinOnSignup(ctx, "c1", raw))
		assert.Equal(t, db.GymInviteAccepted, inv.Status)
		require.NotNil(t, g.heads["c1"])
	})

	t.Run("blocked account and non coach are 403", func(t *testing.T) {
		svc, u, g := newJoinSvc(nil)
		gym := g.addGym("owner1", db.GymStatusActive)
		raw, _ := addTokenInvite(g, gym, "", db.GymInvitePending, future)
		givenUser(u, gymUser("c1", "coach", db.Blocked, "a@x.tn", false))
		givenUser(u, gymUser("c2", "client", db.Active, "b@x.tn", false))

		for _, id := range []string{"c1", "c2"} {
			apiErr := svc.JoinOnSignup(ctx, id, raw)
			require.NotNil(t, apiErr, id)
			assert.Equal(t, http.StatusForbidden, apiErr.Code, id)
		}
	})
}
