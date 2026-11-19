package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
)

// raceBackfillStore has no active rows at read time and one legacy head coach, so the plan creates one row; the
// create then loses a duplicate-key race against a live dual write.
type raceBackfillStore struct {
	createErr error
	creates   int
}

func (s *raceBackfillStore) ListActive(context.Context) ([]db.GymMember, error) { return nil, nil }
func (s *raceBackfillStore) LegacySnapshot(context.Context) ([]db.Gym, []db.GymHeadCoach, []db.TeamMember, error) {
	return []db.Gym{{ID: primitive.NewObjectID(), OwnerID: "o1", Status: "active"}}, nil, nil, nil
}
func (s *raceBackfillStore) MarkLeft(context.Context, string, string, time.Time) (bool, error) {
	return false, nil
}
func (s *raceBackfillStore) FixRole(context.Context, *db.GymMember) (bool, error) { return false, nil }
func (s *raceBackfillStore) EnsureActive(context.Context, *db.GymMember) (bool, error) {
	s.creates++
	return false, s.createErr
}

func TestBackfillToleratesDuplicateKeyRaceOnCreate(t *testing.T) {
	ctx := context.Background()
	st := &raceBackfillStore{createErr: repositories.ErrAlreadyMember}
	rep, err := RunMemberBackfill(ctx, st, false, nil)
	require.NoError(t, err)
	require.Positive(t, st.creates, "the plan tried to create")
	assert.Zero(t, rep.Failed, "a lost create race is the exists outcome, not a failure")
	assert.Zero(t, rep.Created)

	// any other error still counts as failed
	st = &raceBackfillStore{createErr: errors.New("boom")}
	rep, err = RunMemberBackfill(ctx, st, false, nil)
	require.NoError(t, err)
	assert.Equal(t, st.creates, rep.Failed)
}
