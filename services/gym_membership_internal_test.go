package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models/db"
)

// recMemberStore records the calls and the contexts of the mirror helpers.
type recMemberStore struct {
	rows   []db.GymMember
	calls  []string
	ctxs   []context.Context
	ctxErr []error // ctx.Err() as seen inside each call
	failOn string
}

func (s *recMemberStore) rec(ctx context.Context, name string) error {
	s.calls = append(s.calls, name)
	s.ctxs = append(s.ctxs, ctx)
	s.ctxErr = append(s.ctxErr, ctx.Err())
	if s.failOn == name {
		return errors.New("mongo down")
	}
	return nil
}

func (s *recMemberStore) UpsertActive(ctx context.Context, _ *db.GymMember) error {
	return s.rec(ctx, "UpsertActive")
}
func (s *recMemberStore) MarkLeft(ctx context.Context, _, _ string, _ time.Time) (bool, error) {
	return true, s.rec(ctx, "MarkLeft")
}
func (s *recMemberStore) MarkLeftCoach(ctx context.Context, _ string, _ time.Time) (bool, error) {
	return true, s.rec(ctx, "MarkLeftCoach")
}
func (s *recMemberStore) MarkLeftBySupervisor(ctx context.Context, _, _ string, _ time.Time) (int64, error) {
	return 1, s.rec(ctx, "MarkLeftBySupervisor")
}
func (s *recMemberStore) MarkLeftHead(ctx context.Context, _ string, _ time.Time) (bool, error) {
	return true, s.rec(ctx, "MarkLeftHead")
}
func (s *recMemberStore) MarkLeftSupervised(ctx context.Context, _ string, _ time.Time) (int64, error) {
	return 1, s.rec(ctx, "MarkLeftSupervised")
}
func (s *recMemberStore) ListByUser(ctx context.Context, _ string) ([]db.GymMember, error) {
	return s.rows, s.rec(ctx, "ListByUser")
}
func (s *recMemberStore) Get(ctx context.Context, _, _ string) (*db.GymMember, error) {
	return nil, s.rec(ctx, "Get")
}

func TestMirrorContext(t *testing.T) {
	t.Run("a cancelled request does not cancel the mirror, and a deadline applies", func(t *testing.T) {
		parent, cancel := context.WithCancel(context.Background())
		cancel()
		s := &recMemberStore{}
		mirrorCoachUnassigned(parent, s, "c1")
		require.Len(t, s.ctxErr, 1)
		assert.NoError(t, s.ctxErr[0])
	})

	t.Run("each mirror call carries a deadline of at most 2s", func(t *testing.T) {
		s := &recMemberStore{}
		mirrorHeadLeft(context.Background(), s, "g1", "h1")
		require.Len(t, s.ctxs, 3)
		for _, c := range s.ctxs {
			dl, ok := c.Deadline()
			require.True(t, ok)
			assert.LessOrEqual(t, time.Until(dl), mirrorTimeout)
		}
	})
}

func TestAuthServiceWithGymMembers(t *testing.T) {
	s := &AuthServiceImpl{}
	store := &recMemberStore{}
	assert.Same(t, s, s.WithGymMembers(store))
	assert.Equal(t, GymMemberStore(store), s.gymMembers)
}

func TestMirrorUserGone(t *testing.T) {
	ctx := context.Background()

	t.Run("closes head rows, supervised coaches and coach rows without any lookup", func(t *testing.T) {
		s := &recMemberStore{}
		mirrorUserGone(ctx, s, "u1")
		assert.Equal(t, []string{"MarkLeftHead", "MarkLeftSupervised", "MarkLeftCoach"}, s.calls)
	})

	t.Run("nil store is a no-op", func(t *testing.T) {
		assert.NotPanics(t, func() { mirrorUserGone(ctx, nil, "u1") })
	})

	for _, fail := range []string{"MarkLeftHead", "MarkLeftSupervised", "MarkLeftCoach"} {
		t.Run("failure on "+fail+" does not stop the other closes", func(t *testing.T) {
			s := &recMemberStore{failOn: fail}
			assert.NotPanics(t, func() { mirrorUserGone(ctx, s, "u1") })
			assert.Len(t, s.calls, 3)
		})
	}
}

func TestOwnerWinsMirrors(t *testing.T) {
	ctx := context.Background()
	owner := db.GymMember{GymID: "g1", UserID: "o1", Role: db.GymMemberRoleOwner}
	// ownerStore answers Get with the owner row of g1 only.
	s := &ownerGetStore{recMemberStore: recMemberStore{rows: []db.GymMember{owner}}}

	mirrorHeadJoined(ctx, s, "g1", "o1", "admin1", db.GymMemberSourceAdmin, time.Now())
	assert.NotContains(t, s.calls, "UpsertActive", "no head row for the owner of the gym")

	s.calls = nil
	mirrorHeadLeft(ctx, s, "g1", "o1")
	assert.NotContains(t, s.calls, "MarkLeft", "the owner row is not closed when the owner stops acting as head")
	assert.Contains(t, s.calls, "MarkLeftBySupervisor")

	s.calls = nil
	mirrorHeadJoined(ctx, s, "g1", "h1", "admin1", db.GymMemberSourceAdmin, time.Now())
	assert.Equal(t, []string{"Get", "UpsertActive"}, s.calls, "another user still gets a head row")

	s.calls = nil
	mirrorExistingTeam(ctx, s, teamIDs{"h1": {"o1", "c1"}}, "g1", "h1", "admin1", time.Now())
	upserts := 0
	for _, c := range s.calls {
		if c == "UpsertActive" {
			upserts++
		}
	}
	assert.Equal(t, 1, upserts, "the owner is skipped, the other coach is mirrored")
}

type ownerGetStore struct{ recMemberStore }

func (s *ownerGetStore) Get(ctx context.Context, user, gym string) (*db.GymMember, error) {
	if err := s.rec(ctx, "Get"); err != nil {
		return nil, err
	}
	for i := range s.rows {
		if s.rows[i].UserID == user && s.rows[i].GymID == gym {
			return &s.rows[i], nil
		}
	}
	return nil, nil
}

type teamIDs map[string][]string

func (t teamIDs) IDsByHead(_ context.Context, head string) ([]string, error) { return t[head], nil }

func TestExistingTeamUsesOneBudget(t *testing.T) {
	s := &recMemberStore{}
	mirrorExistingTeam(context.Background(), s, teamIDs{"h1": {"c1", "c2", "c3"}}, "g1", "h1", "admin1", time.Now())
	var upsertCtxs []context.Context
	for i, c := range s.calls {
		if c == "UpsertActive" {
			upsertCtxs = append(upsertCtxs, s.ctxs[i])
		}
	}
	require.Len(t, upsertCtxs, 3)
	d0, _ := upsertCtxs[0].Deadline()
	for _, c := range upsertCtxs {
		d, ok := c.Deadline()
		require.True(t, ok)
		assert.True(t, d.Equal(d0), "all upserts share one deadline")
	}
}
