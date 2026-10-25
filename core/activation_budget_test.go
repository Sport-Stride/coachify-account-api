package core

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlanIssue(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	t.Run("first issue ok", func(t *testing.T) {
		n, reset, ok := PlanIssue(CodeBudget{}, now)
		require.True(t, ok)
		assert.False(t, reset)
		assert.Equal(t, 1, n.RegenCount)
		assert.Equal(t, now, n.LastIssuedAt)
	})
	t.Run("cooldown blocks within 60s", func(t *testing.T) {
		_, _, ok := PlanIssue(CodeBudget{LastIssuedAt: now.Add(-59 * time.Second), RegenWindowStart: now.Add(-time.Minute)}, now)
		assert.False(t, ok)
		_, _, ok = PlanIssue(CodeBudget{LastIssuedAt: now.Add(-60 * time.Second), RegenWindowStart: now.Add(-time.Minute)}, now)
		assert.True(t, ok)
	})
	t.Run("cap 5 per hour then window rolls", func(t *testing.T) {
		b := CodeBudget{}
		at := now
		for i := 0; i < MaxRegensPerWindow; i++ {
			var ok bool
			b, _, ok = PlanIssue(b, at)
			require.True(t, ok, i)
			at = at.Add(2 * time.Minute)
		}
		_, _, ok := PlanIssue(b, at)
		assert.False(t, ok)
		_, _, ok = PlanIssue(b, now.Add(RegenWindow+time.Second))
		assert.True(t, ok)
	})
	t.Run("active lockout blocks; elapsed lockout resets attempts", func(t *testing.T) {
		_, _, ok := PlanIssue(CodeBudget{LockedUntil: now.Add(time.Minute)}, now)
		assert.False(t, ok)
		n, reset, ok := PlanIssue(CodeBudget{LockedUntil: now.Add(-time.Minute)}, now)
		require.True(t, ok)
		assert.True(t, reset)
		assert.True(t, n.LockedUntil.IsZero())
	})
}

type fakeCode struct {
	code     string
	exp      time.Time
	attempts int
	locked   bool
}

func (f *fakeCode) ops() CodeOps {
	return CodeOps{
		Consume: func() (string, time.Time, int, bool, error) {
			f.attempts++
			return f.code, f.exp, f.attempts, true, nil
		},
		Lock:  func(time.Time) error { f.locked = true; f.code = ""; return nil },
		Clear: func() error { f.attempts = 0; return nil },
	}
}

func TestVerifyCode(t *testing.T) {
	now := time.Now()
	f := &fakeCode{code: "123456", exp: now.Add(time.Hour)}
	for i := 0; i < 4; i++ {
		res, err := VerifyCode(f.ops(), "000000", now)
		require.NoError(t, err)
		assert.Equal(t, CodeInvalid, res)
	}
	assert.Equal(t, 4, f.attempts)
	// verify then confirm of the right code: budget is cleared, not burned
	res, _ := VerifyCode(f.ops(), "123456", now)
	assert.Equal(t, CodeOK, res)
	assert.Equal(t, 0, f.attempts)
	res, _ = VerifyCode(f.ops(), "123456", now)
	assert.Equal(t, CodeOK, res)
	// five wrong locks
	for i := 0; i < 5; i++ {
		_, _ = VerifyCode(f.ops(), "bad", now)
	}
	assert.True(t, f.locked)
	res, _ = VerifyCode(f.ops(), "123456", now)
	assert.Equal(t, CodeLocked, res)
}

func TestVerifyCode_StoreError(t *testing.T) {
	boom := errors.New("boom")
	_, err := VerifyCode(CodeOps{Consume: func() (string, time.Time, int, bool, error) { return "", time.Time{}, 0, false, boom }}, "x", time.Now())
	assert.ErrorIs(t, err, boom)
}
