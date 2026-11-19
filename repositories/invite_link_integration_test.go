//go:build integration

package repositories_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
)

func inviteFixture(t *testing.T) (*repositories.InviteLinkRepository, context.Context) {
	t.Helper()
	database := gymTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	t.Cleanup(cancel)
	require.NoError(t, repositories.InitializeIndexes(ctx, database))
	return repositories.NewInviteLinkRepository(database), ctx
}

func newLink(hash string, maxUses int, exp *time.Time) *db.InviteLink {
	return &db.InviteLink{
		Kind: db.InviteKindClientGym, TokenHash: hash, CreatorID: "c1", CreatorRole: "coach", GymID: "g1",
		MaxUses: maxUses, Status: db.InviteStatusActive, ExpiresAt: exp, CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
}

func TestInviteLinkIT_IndexesIdempotentAndTokenUnique(t *testing.T) {
	database := gymTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	require.NoError(t, repositories.InitializeIndexes(ctx, database))
	first := indexNames(t, ctx, database, "invite_links")
	require.NoError(t, repositories.InitializeIndexes(ctx, database), "second run must be a no-op")
	assert.ElementsMatch(t, first, indexNames(t, ctx, database, "invite_links"))
	for _, n := range []string{
		"idx_invite_links_token_hash_unique", "idx_invite_links_creator_status_created",
		"idx_invite_links_gym_status_created", "idx_invite_links_email_status",
	} {
		assert.Contains(t, first, n)
	}
	assert.True(t, indexUnique(t, ctx, database, "invite_links", "idx_invite_links_token_hash_unique"))

	repo := repositories.NewInviteLinkRepository(database)
	require.NoError(t, repo.Insert(ctx, newLink("h-dup", 1, nil)))
	assert.ErrorIs(t, repo.Insert(ctx, newLink("h-dup", 1, nil)), repositories.ErrInviteLinkExists)
	ok, missing := repositories.VerifyIndexes(ctx, database)
	assert.True(t, ok, "missing: %v", missing)
}

func TestInviteLinkIT_ConsumeUseConcurrent(t *testing.T) {
	repo, ctx := inviteFixture(t)
	l := newLink("h-conc", 5, nil)
	require.NoError(t, repo.Insert(ctx, l))

	var wins int32
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := repo.ConsumeUse(ctx, l.ID.Hex(), time.Now())
			if assert.NoError(t, err) && ok {
				atomic.AddInt32(&wins, 1)
			}
		}()
	}
	wg.Wait()
	assert.EqualValues(t, 5, wins)
	got, err := repo.GetByID(ctx, l.ID.Hex())
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, 5, got.Uses)
	assert.Equal(t, db.InviteStatusExhausted, got.Status)
}

func TestInviteLinkIT_ReleaseRestoresAndExhaustedTransitions(t *testing.T) {
	repo, ctx := inviteFixture(t)
	l := newLink("h-rel", 2, nil)
	require.NoError(t, repo.Insert(ctx, l))
	id := l.ID.Hex()
	get := func() *db.InviteLink {
		g, err := repo.GetByID(ctx, id)
		require.NoError(t, err)
		require.NotNil(t, g)
		return g
	}

	ok, err := repo.ConsumeUse(ctx, id, time.Now())
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, db.InviteStatusActive, get().Status)
	ok, _ = repo.ConsumeUse(ctx, id, time.Now())
	require.True(t, ok)
	assert.Equal(t, 2, get().Uses)
	assert.Equal(t, db.InviteStatusExhausted, get().Status)
	ok, _ = repo.ConsumeUse(ctx, id, time.Now())
	assert.False(t, ok, "exhausted link is not consumable")

	require.NoError(t, repo.ReleaseUse(ctx, id))
	assert.Equal(t, 1, get().Uses)
	assert.Equal(t, db.InviteStatusActive, get().Status, "release reopens an exhausted link")
	ok, _ = repo.ConsumeUse(ctx, id, time.Now())
	assert.True(t, ok, "released use is consumable again")

	// release never goes below zero
	z := newLink("h-zero", 3, nil)
	require.NoError(t, repo.Insert(ctx, z))
	require.NoError(t, repo.ReleaseUse(ctx, z.ID.Hex()))
	gz, _ := repo.GetByID(ctx, z.ID.Hex())
	assert.Equal(t, 0, gz.Uses)

	// release keeps a revoked link revoked
	changed, err := repo.SetStatus(ctx, z.ID.Hex(), db.InviteStatusActive, db.InviteStatusRevoked)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, repo.ReleaseUse(ctx, z.ID.Hex()))
	gz, _ = repo.GetByID(ctx, z.ID.Hex())
	assert.Equal(t, db.InviteStatusRevoked, gz.Status)

	// malformed id is a no-op, not an error
	ok, err = repo.ConsumeUse(ctx, "not-hex", time.Now())
	assert.NoError(t, err)
	assert.False(t, ok)
}

func TestInviteLinkIT_UnlimitedNeverExhausts(t *testing.T) {
	repo, ctx := inviteFixture(t)
	l := newLink("h-unl", 0, nil)
	require.NoError(t, repo.Insert(ctx, l))
	for i := 0; i < 7; i++ {
		ok, err := repo.ConsumeUse(ctx, l.ID.Hex(), time.Now())
		require.NoError(t, err)
		require.True(t, ok)
	}
	g, _ := repo.GetByID(ctx, l.ID.Hex())
	assert.Equal(t, 7, g.Uses)
	assert.Equal(t, db.InviteStatusActive, g.Status)
}

func TestInviteLinkIT_ExpiredAndRevokedNotConsumable(t *testing.T) {
	repo, ctx := inviteFixture(t)
	past := time.Now().Add(-time.Minute)
	future := time.Now().Add(time.Hour)

	expired := newLink("h-exp", 5, &past)
	require.NoError(t, repo.Insert(ctx, expired))
	ok, err := repo.ConsumeUse(ctx, expired.ID.Hex(), time.Now())
	require.NoError(t, err)
	assert.False(t, ok, "expired")

	live := newLink("h-live", 5, &future)
	require.NoError(t, repo.Insert(ctx, live))
	ok, _ = repo.ConsumeUse(ctx, live.ID.Hex(), time.Now())
	assert.True(t, ok, "future expiry is fine")
	ok, _ = repo.ConsumeUse(ctx, live.ID.Hex(), time.Now().Add(2*time.Hour))
	assert.False(t, ok, "expires relative to the supplied now")

	rev := newLink("h-rev", 5, nil)
	require.NoError(t, repo.Insert(ctx, rev))
	changed, err := repo.SetStatus(ctx, rev.ID.Hex(), db.InviteStatusActive, db.InviteStatusRevoked)
	require.NoError(t, err)
	require.True(t, changed)
	ok, _ = repo.ConsumeUse(ctx, rev.ID.Hex(), time.Now())
	assert.False(t, ok, "revoked")
	changed, _ = repo.SetStatus(ctx, rev.ID.Hex(), db.InviteStatusActive, db.InviteStatusDeclined)
	assert.False(t, changed, "transition only from the expected status")

	// unused rows stay at zero uses
	for _, l := range []*db.InviteLink{expired, rev} {
		g, _ := repo.GetByID(ctx, l.ID.Hex())
		assert.Equal(t, 0, g.Uses)
	}
}

func TestInviteLinkIT_ListAndPendingByEmailAndUpsert(t *testing.T) {
	repo, ctx := inviteFixture(t)
	future := time.Now().Add(time.Hour)
	past := time.Now().Add(-time.Hour)
	a := newLink("h-a", 1, &future)
	a.Email = "a@x.io"
	b := newLink("h-b", 1, &past)
	b.Email = "a@x.io"
	require.NoError(t, repo.Insert(ctx, a))
	require.NoError(t, repo.Insert(ctx, b))

	rows, err := repo.ListPendingByEmail(ctx, "a@x.io", db.InviteKindClientGym, time.Now())
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, a.ID, rows[0].ID)

	all, err := repo.List(ctx, repositories.InviteLinkFilter{GymID: "g1", Status: db.InviteStatusActive}, 10)
	require.NoError(t, err)
	assert.Len(t, all, 2)

	l := &db.InviteLink{Kind: db.InviteKindClientPersonal, TokenHash: "h-up", CreatorID: "c9", CreatorRole: "legacy",
		Status: db.InviteStatusActive, CreatedAt: time.Now().UTC(), LegacyRef: "registration_links:x"}
	ins, err := repo.UpsertByHash(ctx, l)
	require.NoError(t, err)
	assert.True(t, ins)
	ins, err = repo.UpsertByHash(ctx, l)
	require.NoError(t, err)
	assert.False(t, ins, "second upsert is a no-op")
}
