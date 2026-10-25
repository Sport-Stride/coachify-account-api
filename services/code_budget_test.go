package services

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"coachify-account-api/models/db"
)

func TestPlanConfirmIssue_KeepsBudgetAndCooldown(t *testing.T) {
	now := time.Now()
	old := &db.UserConfirmCode{Code: "1", ExpirationDate: now.Add(time.Hour), Attempts: 4, LastIssuedAt: now.Add(-10 * time.Second)}
	assert.False(t, planConfirmIssue(old, now).ok, "within cooldown")
	old.LastIssuedAt = now.Add(-2 * time.Minute)
	p := planConfirmIssue(old, now)
	assert.True(t, p.ok)
	assert.False(t, p.reset, "attempts not refilled by a resend")
	assert.True(t, p.hadCode)
	assert.False(t, planConfirmIssue(&db.UserConfirmCode{LockedUntil: now.Add(time.Minute)}, now).ok)
	assert.True(t, planConfirmIssue(nil, now).ok)
}

func TestPlanResetIssue_ZeroStructHasNoCode(t *testing.T) {
	now := time.Now()
	p := planResetIssue(db.UserResetPasswordCode{}, now)
	assert.True(t, p.ok)
	assert.False(t, p.hadCode)
	old := db.UserResetPasswordCode{Code: "1", ExpirationDate: now, RegenCount: 5, RegenWindowStart: now.Add(-time.Minute), LastIssuedAt: now.Add(-5 * time.Minute)}
	assert.False(t, planResetIssue(old, now).ok, "hourly cap")
}
