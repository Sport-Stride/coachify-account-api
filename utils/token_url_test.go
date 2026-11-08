package utils

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewURLToken(t *testing.T) {
	raw, hash, err := NewURLToken()
	require.NoError(t, err)

	assert.Len(t, raw, 43, "32 bytes in unpadded base64url")
	assert.Regexp(t, regexp.MustCompile(`^[A-Za-z0-9_-]+$`), raw)
	assert.Len(t, hash, 64)
	assert.Equal(t, HashToken(raw), hash)
	assert.NotEqual(t, raw, hash)

	seen := map[string]bool{raw: true}
	for i := 0; i < 50; i++ {
		r, _, err := NewURLToken()
		require.NoError(t, err)
		assert.False(t, seen[r], "tokens must be unique")
		seen[r] = true
	}
}

func TestHashTokenStable(t *testing.T) {
	assert.Equal(t, HashToken("abc"), HashToken("abc"))
	assert.NotEqual(t, HashToken("abc"), HashToken("abd"))
	// SHA-256 of "abc"
	assert.Equal(t, "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", HashToken("abc"))
}
