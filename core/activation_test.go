package core

import (
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGenerateCode_SixDigitsUnique(t *testing.T) {
	m := NewSimpleActivationManager()
	re := regexp.MustCompile(`^\d{6}$`)
	seen := map[string]bool{}
	for i := 0; i < 20000; i++ {
		c := m.GenerateCode()
		assert.Regexp(t, re, c)
		seen[c] = true
	}
	assert.Greater(t, len(seen), 15000, "codes must not repeat like a weak generator")
}

func TestCodeTTLs(t *testing.T) {
	assert.Equal(t, 15*time.Minute, ResetCodeTTL)
	assert.Equal(t, 24*time.Hour, ConfirmCodeTTL)
}

func TestCheckCode(t *testing.T) {
	now := time.Now()
	future, past := now.Add(time.Minute), now.Add(-time.Minute)
	for _, tc := range []struct {
		name     string
		stored   string
		exp      time.Time
		attempts int
		given    string
		want     CodeResult
	}{
		{"match", "123456", future, 1, "123456", CodeOK},
		{"match on last allowed attempt", "123456", future, MaxCodeAttempts, "123456", CodeOK},
		{"mismatch", "123456", future, 1, "654321", CodeInvalid},
		{"expired", "123456", past, 1, "123456", CodeInvalid},
		{"cleared code", "", future, 1, "", CodeInvalid},
		{"over attempt cap even with right code", "123456", future, MaxCodeAttempts + 1, "123456", CodeLocked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, CheckCode(tc.stored, tc.exp, tc.attempts, tc.given, now))
		})
	}
}

func TestShouldLockAndRegenBlocked(t *testing.T) {
	now := time.Now()
	assert.False(t, ShouldLock(MaxCodeAttempts-1))
	assert.True(t, ShouldLock(MaxCodeAttempts))
	assert.True(t, RegenBlocked(now.Add(time.Minute), now))
	assert.False(t, RegenBlocked(now.Add(-time.Minute), now))
	assert.False(t, RegenBlocked(time.Time{}, now))
}
