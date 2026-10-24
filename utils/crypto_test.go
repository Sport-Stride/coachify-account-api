package utils

import (
	"net/http"
	"strings"
	"testing"

	"coachify-account-api/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

var aesKey = []byte(testEncKey)

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	enc, err := Encrypt("hello secret", aesKey)
	require.NoError(t, err)
	assert.NotContains(t, enc, "hello")
	dec, err := Decrypt(enc, aesKey)
	require.NoError(t, err)
	assert.Equal(t, "hello secret", dec)
}

func TestEncrypt_UsesFreshNonce(t *testing.T) {
	a, err := Encrypt("same", aesKey)
	require.NoError(t, err)
	b, err := Encrypt("same", aesKey)
	require.NoError(t, err)
	assert.NotEqual(t, a, b, "AES-GCM nonce must never repeat for the same key")
}

func TestEncryptDecrypt_Errors(t *testing.T) {
	_, err := Encrypt("x", []byte("short"))
	assert.Error(t, err, "bad key length")
	_, err = Decrypt("QUJD", []byte("short"))
	assert.Error(t, err, "bad key length")

	_, err = Decrypt("%%%", aesKey)
	assert.Error(t, err, "bad base64")

	_, err = Decrypt("QUJD", aesKey)
	assert.Error(t, err, "ciphertext shorter than nonce must error, not return empty plaintext")

	enc, err := Encrypt("payload", aesKey)
	require.NoError(t, err)
	_, err = Decrypt(enc, []byte("ffffffffffffffffffffffffffffffff"))
	assert.Error(t, err, "wrong key")
}

func TestGenerateRandomString(t *testing.T) {
	for _, n := range []int{1, 7, 16, 33} {
		s, apiErr := GenerateRandomString(n)
		require.Nil(t, apiErr)
		assert.Len(t, s, n)
	}
	a, _ := GenerateRandomString(24)
	b, _ := GenerateRandomString(24)
	assert.NotEqual(t, a, b)

	for _, n := range []int{0, -3} {
		s, apiErr := GenerateRandomString(n)
		assert.Empty(t, s)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
		assert.ErrorIs(t, apiErr.Error, models.ErrInvalidIdStringGeneration)
	}
}

func TestGenerateStrongPassword(t *testing.T) {
	for i := 0; i < 50; i++ {
		p, apiErr := GenerateStrongPassword()
		require.Nil(t, apiErr)
		require.Len(t, p, 8)
		assert.True(t, strings.ContainsAny(p, lowercase), p)
		assert.True(t, strings.ContainsAny(p, uppercase), p)
		assert.True(t, strings.ContainsAny(p, digits), p)
		assert.True(t, strings.ContainsAny(p, special), p)
	}
}

func TestShuffleKeepsElements(t *testing.T) {
	in := []byte("abcdefgh")
	out := append([]byte(nil), in...)
	shuffle(out)
	assert.ElementsMatch(t, in, out)
	shuffle(nil)
}

func TestHashPassword(t *testing.T) {
	h, apiErr := HashPassword("Pa$$w0rd!")
	require.Nil(t, apiErr)
	assert.NotContains(t, h, "Pa$$w0rd!")
	assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(h), []byte("Pa$$w0rd!")))
	assert.Error(t, bcrypt.CompareHashAndPassword([]byte(h), []byte("wrong")))

	h2, _ := HashPassword("Pa$$w0rd!")
	assert.NotEqual(t, h, h2, "bcrypt salts must differ")

	// bcrypt rejects inputs over 72 bytes -> internal error path.
	_, apiErr = HashPassword(strings.Repeat("a", 100))
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
	assert.ErrorIs(t, apiErr.Error, models.ErrInternalError)
}

func TestGenerateAndHashPassword(t *testing.T) {
	plain, hashed, apiErr := GenerateAndHashPassword()
	require.Nil(t, apiErr)
	assert.Len(t, plain, 8)
	assert.NotEqual(t, plain, hashed)
	assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(hashed), []byte(plain)))
}
