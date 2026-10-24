package core

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"coachify-account-api/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func TestHashPassword(t *testing.T) {
	pc := NewPasswordChecker()
	h, apiErr := pc.HashPassword("Str0ng!pass")
	require.Nil(t, apiErr)
	assert.NotContains(t, h, "Str0ng!pass")
	assert.True(t, strings.HasPrefix(h, "$2"), "must be a bcrypt hash")

	h2, _ := pc.HashPassword("Str0ng!pass")
	assert.NotEqual(t, h, h2, "salted hashes must differ")

	// bcrypt rejects >72 byte input.
	_, apiErr = pc.HashPassword(strings.Repeat("x", 80))
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
	assert.ErrorIs(t, apiErr.Error, models.ErrFailedToHashPassword)
}

func TestVerifyPassword(t *testing.T) {
	pc := NewPasswordChecker()
	h, apiErr := pc.HashPassword("Str0ng!pass")
	require.Nil(t, apiErr)

	assert.Nil(t, pc.VerifyPassword("Str0ng!pass", h))

	apiErr = pc.VerifyPassword("wrong-pass", h)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusUnauthorized, apiErr.Code)
	assert.ErrorIs(t, apiErr.Error, models.ErrPasswordMismatch)

	apiErr = pc.VerifyPassword("", h)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusUnauthorized, apiErr.Code)

	// A corrupted / non-bcrypt stored hash is a server error, not "bad password".
	apiErr = pc.VerifyPassword("Str0ng!pass", "not-a-bcrypt-hash")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
	assert.ErrorIs(t, apiErr.Error, models.ErrFailedToVerifyPassword)

	// Empty stored hash (e.g. OAuth-only account) must never verify.
	apiErr = pc.VerifyPassword("anything", "")
	require.NotNil(t, apiErr)
	assert.NotEqual(t, 0, apiErr.Code)

	// Plaintext equal to stored value must not pass.
	apiErr = pc.VerifyPassword(h, h)
	require.NotNil(t, apiErr)

	_ = bcrypt.DefaultCost
}

func TestValidatePassword(t *testing.T) {
	cases := []struct {
		name string
		pw   string
		want bool
	}{
		{"valid", "Abcdef1!", true},
		{"valid symbol only no digit", "Abcdefg!", true},
		{"too short", "Ab1!xyz", false},
		{"empty", "", false},
		{"no upper", "abcdef1!", false},
		{"no lower", "ABCDEF1!", false},
		{"no symbol", "Abcdefg1", false},
		{"space counts as symbol", "Abcdef g", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, ValidatePassword(c.pw))
		})
	}
}

func TestGenerateCode(t *testing.T) {
	m := NewSimpleActivationManager()
	re := regexp.MustCompile(`^\d{6}$`)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		c := m.GenerateCode()
		require.Regexp(t, re, c)
		assert.GreaterOrEqual(t, c, "100000")
		seen[c] = true
	}
	assert.Greater(t, len(seen), 1, "codes must vary")
}
