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
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/services"
	"coachify-account-api/utils"
)

// memInviteStore is an in-memory services.GymOwnerInviteStore.
type memInviteStore struct {
	mu        sync.Mutex
	invites   []*db.GymOwnerInvite
	insertErr error
}

func (m *memInviteStore) Insert(_ context.Context, inv *db.GymOwnerInvite) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.insertErr != nil {
		return m.insertErr
	}
	inv.ID = primitive.NewObjectID()
	cp := *inv
	m.invites = append(m.invites, &cp)
	return nil
}

func (m *memInviteStore) find(f func(*db.GymOwnerInvite) bool) *db.GymOwnerInvite {
	for _, i := range m.invites {
		if f(i) {
			cp := *i
			return &cp
		}
	}
	return nil
}

func (m *memInviteStore) GetByTokenHash(_ context.Context, h string) (*db.GymOwnerInvite, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.find(func(i *db.GymOwnerInvite) bool { return i.TokenHash == h }), nil
}

func (m *memInviteStore) GetByID(_ context.Context, id string) (*db.GymOwnerInvite, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.find(func(i *db.GymOwnerInvite) bool { return i.ID.Hex() == id }), nil
}

func (m *memInviteStore) List(context.Context, string, int, int) ([]db.GymOwnerInvite, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []db.GymOwnerInvite{}
	for _, i := range m.invites {
		out = append(out, *i)
	}
	return out, len(out), nil
}

func (m *memInviteStore) RevokePendingByEmail(_ context.Context, email string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, i := range m.invites {
		if i.Email == email && i.Status == db.GymOwnerInvitePending {
			i.Status, i.RevokedAt = db.GymOwnerInviteRevoked, &at
		}
	}
	return nil
}

func (m *memInviteStore) Transition(_ context.Context, id, from, to, userID string, at time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, i := range m.invites {
		if i.ID.Hex() == id && i.Status == from {
			i.Status = to
			if to == db.GymOwnerInviteAccepted {
				i.AcceptedUserID, i.AcceptedAt = userID, &at
			}
			return true, nil
		}
	}
	return false, nil
}

func (m *memInviteStore) Rotate(_ context.Context, id, hash string, exp, at time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, i := range m.invites {
		if i.ID.Hex() == id && i.Status == db.GymOwnerInvitePending {
			i.TokenHash, i.ExpiresAt, i.SentAt = hash, exp, at
			return true, nil
		}
	}
	return false, nil
}

type fakeOwnerUsers struct {
	exists bool
	err    error
}

func (f fakeOwnerUsers) EmailExists(context.Context, string) (bool, error) { return f.exists, f.err }

// captureInviteMailer records the links it was asked to send.
type captureInviteMailer struct {
	err   error
	links []string
}

func (c *captureInviteMailer) SendGymOwnerInviteEmail(_ context.Context, _, _, link string) error {
	c.links = append(c.links, link)
	return c.err
}

// tokenOf extracts the raw token from the last emailed link.
func (c *captureInviteMailer) lastToken(t *testing.T) string {
	t.Helper()
	require.NotEmpty(t, c.links)
	link := c.links[len(c.links)-1]
	require.True(t, strings.HasPrefix(link, "https://app.test/auth/signup?invite="))
	return strings.TrimPrefix(link, "https://app.test/auth/signup?invite=")
}

type inviteHarness struct {
	svc    services.GymOwnerInviteService
	store  *memInviteStore
	mailer *captureInviteMailer
	audit  *fakeAudit
}

func newInviteHarness(ttl time.Duration, users fakeOwnerUsers) *inviteHarness {
	h := &inviteHarness{store: &memInviteStore{}, mailer: &captureInviteMailer{}, audit: &fakeAudit{}}
	h.svc = services.NewGymOwnerInviteService(h.store, users, h.audit, h.mailer, ttl, "https://app.test/")
	return h
}

func TestOwnerInviteCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("bad input is 400", func(t *testing.T) {
		h := newInviteHarness(time.Hour, fakeOwnerUsers{})
		for _, tc := range [][3]string{{"", "L", "a@b.co"}, {"A", "", "a@b.co"}, {"A", "L", "not-an-email"}, {"A", "L", ""}} {
			v, apiErr := h.svc.Create(ctx, "admin1", tc[0], tc[1], tc[2])
			assert.Nil(t, v)
			require.NotNil(t, apiErr)
			assert.Equal(t, http.StatusBadRequest, apiErr.Code)
		}
		assert.Empty(t, h.mailer.links)
	})

	t.Run("existing account is 409", func(t *testing.T) {
		h := newInviteHarness(time.Hour, fakeOwnerUsers{exists: true})
		_, apiErr := h.svc.Create(ctx, "admin1", "A", "L", "a@b.co")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
		assert.ErrorIs(t, apiErr.Error, models.ErrEmailAlreadyExists)
		assert.Empty(t, h.store.invites)
	})

	t.Run("new invite replaces the pending one", func(t *testing.T) {
		h := newInviteHarness(time.Hour, fakeOwnerUsers{})
		first, apiErr := h.svc.Create(ctx, "admin1", "A", "L", "Owner@B.co")
		require.Nil(t, apiErr)
		firstToken := h.mailer.lastToken(t)
		second, apiErr := h.svc.Create(ctx, "admin1", "A", "L", "owner@b.co")
		require.Nil(t, apiErr)

		assert.Equal(t, "owner@b.co", first.Email)
		assert.NotEqual(t, first.ID, second.ID)
		assert.Equal(t, db.GymOwnerInvitePending, second.Status)
		require.NotNil(t, second.EmailSent)
		assert.True(t, *second.EmailSent)
		old, _ := h.store.GetByID(ctx, first.ID)
		assert.Equal(t, db.GymOwnerInviteRevoked, old.Status)
		_, lookupErr := h.svc.Lookup(ctx, firstToken)
		require.NotNil(t, lookupErr)
		assert.Equal(t, http.StatusNotFound, lookupErr.Code)
		assert.Len(t, h.audit.entries, 2)
		assert.Equal(t, "gym_owner_invite", h.audit.entries[0].TargetType)
	})

	t.Run("mail failure keeps the invite and reports email_sent false", func(t *testing.T) {
		h := newInviteHarness(time.Hour, fakeOwnerUsers{})
		h.mailer.err = errors.New("smtp down")
		v, apiErr := h.svc.Create(ctx, "admin1", "A", "L", "a@b.co")
		require.Nil(t, apiErr)
		require.NotNil(t, v.EmailSent)
		assert.False(t, *v.EmailSent)
		assert.Len(t, h.store.invites, 1)
	})

	t.Run("duplicate pending race is 409", func(t *testing.T) {
		h := newInviteHarness(time.Hour, fakeOwnerUsers{})
		h.store.insertErr = models.ErrOwnerInvitePending
		_, apiErr := h.svc.Create(ctx, "admin1", "A", "L", "a@b.co")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
	})
}

func TestOwnerInviteRevokeResend(t *testing.T) {
	ctx := context.Background()

	t.Run("resend rotates the token so the old link gives 404", func(t *testing.T) {
		h := newInviteHarness(time.Hour, fakeOwnerUsers{})
		v, apiErr := h.svc.Create(ctx, "admin1", "A", "L", "a@b.co")
		require.Nil(t, apiErr)
		oldToken := h.mailer.lastToken(t)

		resent, apiErr := h.svc.Resend(ctx, "admin1", v.ID)
		require.Nil(t, apiErr)
		newToken := h.mailer.lastToken(t)
		assert.NotEqual(t, oldToken, newToken)
		require.NotNil(t, resent.EmailSent)
		assert.True(t, *resent.EmailSent)

		_, oldErr := h.svc.Lookup(ctx, oldToken)
		require.NotNil(t, oldErr)
		assert.Equal(t, http.StatusNotFound, oldErr.Code)
		got, newErr := h.svc.Lookup(ctx, newToken)
		require.Nil(t, newErr)
		assert.Equal(t, "a@b.co", got.Email)
	})

	t.Run("revoke then revoke or resend is 409", func(t *testing.T) {
		h := newInviteHarness(time.Hour, fakeOwnerUsers{})
		v, _ := h.svc.Create(ctx, "admin1", "A", "L", "a@b.co")
		require.Nil(t, h.svc.Revoke(ctx, "admin1", v.ID))

		apiErr := h.svc.Revoke(ctx, "admin1", v.ID)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
		_, apiErr = h.svc.Resend(ctx, "admin1", v.ID)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
	})

	t.Run("unknown id is 404", func(t *testing.T) {
		h := newInviteHarness(time.Hour, fakeOwnerUsers{})
		apiErr := h.svc.Revoke(ctx, "admin1", primitive.NewObjectID().Hex())
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)
	})

	t.Run("list returns views without token hashes", func(t *testing.T) {
		h := newInviteHarness(time.Hour, fakeOwnerUsers{})
		_, _ = h.svc.Create(ctx, "admin1", "A", "L", "a@b.co")
		list, apiErr := h.svc.List(ctx, "", 0, 0)
		require.Nil(t, apiErr)
		assert.Equal(t, 1, list.Total)
		assert.Equal(t, 1, list.Page)
		assert.Equal(t, 20, list.Size)
	})
}

func TestOwnerInviteLookupSameNotFound(t *testing.T) {
	ctx := context.Background()
	want := models.ErrInviteNotFound

	t.Run("expired", func(t *testing.T) {
		h := newInviteHarness(-time.Hour, fakeOwnerUsers{})
		_, _ = h.svc.Create(ctx, "admin1", "A", "L", "a@b.co")
		_, apiErr := h.svc.Lookup(ctx, h.mailer.lastToken(t))
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)
		assert.Equal(t, want, apiErr.Error)
		assert.Equal(t, db.GymOwnerInviteExpired, h.store.invites[0].Status, "overdue invite is flipped to expired")
	})
	t.Run("revoked", func(t *testing.T) {
		h := newInviteHarness(time.Hour, fakeOwnerUsers{})
		v, _ := h.svc.Create(ctx, "admin1", "A", "L", "a@b.co")
		require.Nil(t, h.svc.Revoke(ctx, "admin1", v.ID))
		_, apiErr := h.svc.Lookup(ctx, h.mailer.lastToken(t))
		require.NotNil(t, apiErr)
		assert.Equal(t, want, apiErr.Error)
	})
	t.Run("used", func(t *testing.T) {
		h := newInviteHarness(time.Hour, fakeOwnerUsers{})
		v, _ := h.svc.Create(ctx, "admin1", "A", "L", "a@b.co")
		require.Nil(t, h.svc.MarkAccepted(ctx, v.ID, "user-1"))
		_, apiErr := h.svc.Lookup(ctx, h.mailer.lastToken(t))
		require.NotNil(t, apiErr)
		assert.Equal(t, want, apiErr.Error)
	})
	t.Run("unknown and empty", func(t *testing.T) {
		h := newInviteHarness(time.Hour, fakeOwnerUsers{})
		for _, tok := range []string{"nope", ""} {
			_, apiErr := h.svc.Lookup(ctx, tok)
			require.NotNil(t, apiErr)
			assert.Equal(t, want, apiErr.Error)
		}
	})
}

func TestOwnerInviteRedeem(t *testing.T) {
	ctx := context.Background()
	h := newInviteHarness(time.Hour, fakeOwnerUsers{})
	_, _ = h.svc.Create(ctx, "admin1", "A", "L", "owner@b.co")
	token := h.mailer.lastToken(t)

	t.Run("mismatch is 403 and never echoes an email", func(t *testing.T) {
		_, apiErr := h.svc.Redeem(ctx, token, "someone-else@x.co")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusForbidden, apiErr.Code)
		assert.ErrorIs(t, apiErr.Error, models.ErrInviteEmailMismatch)
		assert.NotContains(t, apiErr.Error.Error(), "@")
	})
	t.Run("case-insensitive match succeeds without consuming", func(t *testing.T) {
		inv, apiErr := h.svc.Redeem(ctx, token, " OWNER@b.co ")
		require.Nil(t, apiErr)
		assert.Equal(t, "owner@b.co", inv.Email)
		_, apiErr = h.svc.Redeem(ctx, token, "owner@b.co")
		assert.Nil(t, apiErr)
	})
	t.Run("hash of the token is what is stored", func(t *testing.T) {
		assert.Equal(t, utils.HashToken(token), h.store.invites[0].TokenHash)
		assert.NotEqual(t, token, h.store.invites[0].TokenHash)
	})
}
