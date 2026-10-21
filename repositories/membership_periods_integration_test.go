//go:build integration

package repositories_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
)

func assignTo(t *testing.T, f *fixture, coach, head string) {
	t.Helper()
	_, err := f.team.Upsert(context.Background(), &db.TeamMember{CoachID: coach, HeadCoachID: head, AssignedBy: "admin1", AssignedByRole: "admin", AssignedAt: time.Now().UTC().Truncate(time.Millisecond)})
	require.NoError(t, err)
}

func TestPeriodsIntegration(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	pr := f.team.Periods()

	t.Run("assign opens period", func(t *testing.T) {
		assignTo(t, f, "pc1", "ph1")
		p, err := pr.OpenForMember(ctx, db.PeriodKindTeam, "pc1")
		require.NoError(t, err)
		require.NotNil(t, p)
		assert.Equal(t, "ph1", p.ParentID)
		assert.Equal(t, db.PeriodReasonAssign, p.Reason)
		assert.Nil(t, p.End)
		assignTo(t, f, "pc1", "ph1") // idempotent
		ps, err := pr.ListForMember(ctx, db.PeriodKindTeam, "pc1")
		require.NoError(t, err)
		assert.Len(t, ps, 1)
	})

	t.Run("reassign closes old and opens new", func(t *testing.T) {
		assignTo(t, f, "pc1", "ph2")
		ps, err := pr.ListForMember(ctx, db.PeriodKindTeam, "pc1")
		require.NoError(t, err)
		require.Len(t, ps, 2)
		assert.NotNil(t, ps[0].End)
		assert.Equal(t, "ph1", ps[0].ParentID)
		assert.Nil(t, ps[1].End)
		assert.Equal(t, "ph2", ps[1].ParentID)
		assert.Equal(t, db.PeriodReasonReassign, ps[0].Reason)
	})

	t.Run("unassign and head removal reasons", func(t *testing.T) {
		_, err := f.team.DeleteByCoach(repositories.WithPeriodReason(ctx, db.PeriodReasonRemovedByHead), "pc1")
		require.NoError(t, err)
		p, err := pr.OpenForMember(ctx, db.PeriodKindTeam, "pc1")
		require.NoError(t, err)
		assert.Nil(t, p)
		ps, _ := pr.ListForMember(ctx, db.PeriodKindTeam, "pc1")
		assert.Equal(t, db.PeriodReasonRemovedByHead, ps[len(ps)-1].Reason)
	})

	t.Run("demote closes all", func(t *testing.T) {
		assignTo(t, f, "pc2", "ph3")
		assignTo(t, f, "pc3", "ph3")
		_, err := f.team.DeleteByHead(ctx, "ph3")
		require.NoError(t, err)
		for _, c := range []string{"pc2", "pc3"} {
			p, err := pr.OpenForMember(ctx, db.PeriodKindTeam, c)
			require.NoError(t, err)
			assert.Nil(t, p)
		}
		ps, err := pr.ListForParent(ctx, db.PeriodKindTeam, "ph3")
		require.NoError(t, err)
		require.Len(t, ps, 2)
		assert.Equal(t, db.PeriodReasonDemote, ps[0].Reason)
	})

	t.Run("account delete closes as member and as head", func(t *testing.T) {
		assignTo(t, f, "pc4", "ph4")
		assignTo(t, f, "ph4x", "ph5")
		assignTo(t, f, "pc5", "ph4x")
		_, err := f.team.DeleteByAny(ctx, "ph4x")
		require.NoError(t, err)
		for _, c := range []string{"ph4x", "pc5"} {
			p, _ := pr.OpenForMember(ctx, db.PeriodKindTeam, c)
			assert.Nil(t, p, c)
		}
		p, _ := pr.OpenForMember(ctx, db.PeriodKindTeam, "pc4")
		assert.NotNil(t, p, "unrelated period untouched")
		ps, _ := pr.ListForMember(ctx, db.PeriodKindTeam, "pc5")
		assert.Equal(t, db.PeriodReasonAccountGone, ps[0].Reason)
	})

	t.Run("unique open index", func(t *testing.T) {
		coll := f.database.Collection("membership_periods")
		_, err := coll.InsertOne(ctx, &db.MembershipPeriod{Kind: db.PeriodKindTeam, MemberID: "dup", ParentID: "x", Start: time.Now()})
		require.NoError(t, err)
		_, err = coll.InsertOne(ctx, &db.MembershipPeriod{Kind: db.PeriodKindTeam, MemberID: "dup", ParentID: "y", Start: time.Now()})
		require.Error(t, err)
		end := time.Now()
		_, err = coll.InsertOne(ctx, &db.MembershipPeriod{Kind: db.PeriodKindTeam, MemberID: "dup", ParentID: "z", Start: time.Now(), End: &end})
		require.NoError(t, err, "closed periods are not constrained")
	})

	t.Run("scope for head", func(t *testing.T) {
		assignTo(t, f, "sc1", "sh1")
		assignTo(t, f, "sc2", "sh1")
		_, err := f.team.DeleteByCoach(ctx, "sc2")
		require.NoError(t, err)
		scope, err := f.team.ScopePeriodsForHead(ctx, "sh1")
		require.NoError(t, err)
		assert.Equal(t, []string{"sc1"}, scope.CurrentCoachIDs)
		require.Len(t, scope.Coaches, 2)
		byID := map[string]db.CoachPeriods{}
		for _, c := range scope.Coaches {
			byID[c.CoachID] = c
		}
		assert.Nil(t, byID["sc1"].Periods[0].To)
		assert.NotNil(t, byID["sc2"].Periods[0].To)
	})

	t.Run("reconcile backfills and closes orphans", func(t *testing.T) {
		// Row without period: insert directly.
		at := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Millisecond)
		_, err := f.database.Collection("coach_team_members").InsertOne(ctx, &db.TeamMember{CoachID: "rc1", HeadCoachID: "rh1", AssignedBy: "a", AssignedByRole: "admin", AssignedAt: at})
		require.NoError(t, err)
		// Period without row.
		require.NoError(t, pr.OpenPeriod(ctx, db.PeriodKindTeam, "rc-orphan", "rh1", db.PeriodReasonAssign, at))

		opened, closed, err := pr.ReconcileTeam(ctx)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, opened, 1)
		assert.GreaterOrEqual(t, closed, 1)

		p, err := pr.OpenForMember(ctx, db.PeriodKindTeam, "rc1")
		require.NoError(t, err)
		require.NotNil(t, p)
		assert.True(t, p.Start.Equal(at))
		assert.Equal(t, db.PeriodReasonBackfill, p.Reason)
		o, _ := pr.OpenForMember(ctx, db.PeriodKindTeam, "rc-orphan")
		assert.Nil(t, o)
		ps, _ := pr.ListForMember(ctx, db.PeriodKindTeam, "rc-orphan")
		assert.Equal(t, db.PeriodReasonReconcile, ps[0].Reason)

		opened, closed, err = pr.ReconcileTeam(ctx) // idempotent
		require.NoError(t, err)
		assert.Zero(t, opened)
		assert.Zero(t, closed)
	})
}
