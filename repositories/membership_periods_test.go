package repositories_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
)

func TestGroupPeriodsByMember(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(24 * time.Hour)
	t2 := t0.Add(48 * time.Hour)
	ps := []db.MembershipPeriod{
		{MemberID: "a", Start: t0, End: &t1},
		{MemberID: "b", Start: t1},
		{MemberID: "a", Start: t2},
	}
	got := repositories.GroupPeriodsByMember(ps)
	assert.Len(t, got, 2)
	assert.Equal(t, "a", got[0].CoachID)
	assert.Equal(t, []db.PeriodRange{{From: t0, To: &t1}, {From: t2, To: nil}}, got[0].Periods)
	assert.Equal(t, "b", got[1].CoachID)
	assert.Equal(t, []db.PeriodRange{{From: t1, To: nil}}, got[1].Periods)
	assert.Empty(t, repositories.GroupPeriodsByMember(nil))
}
