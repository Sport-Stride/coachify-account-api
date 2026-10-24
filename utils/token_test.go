package utils

import (
	"bytes"
	"log"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"coachify-account-api/models"
	"coachify-account-api/models/api"

	"github.com/golang-jwt/jwt/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testSecret = "unit-test-signing-secret"
	testEncKey = "0123456789abcdef0123456789abcdef" // 32 bytes -> AES-256
)

// LoadConfig is a sync.Once singleton, so the env must be set before the first call.
func TestMain(m *testing.M) {
	os.Setenv("COACHIFY_SECRET_KEY", testSecret)
	os.Setenv("COACHIFY_ENCRYPTION_KEY", testEncKey)
	os.Exit(m.Run())
}

func testUser() api.RefreshToken {
	return api.RefreshToken{
		ExternalID:    "ext-1",
		UserFirstname: "Ada",
		UserLastname:  "Lovelace",
		UserEmail:     "ada@example.com",
		UserRole:      "coach",
	}
}

// signAndEncrypt builds an encrypted token from arbitrary claims/method/key.
func signAndEncrypt(t *testing.T, method jwt.SigningMethod, key interface{}, claims jwt.MapClaims) string {
	t.Helper()
	signed, err := jwt.NewWithClaims(method, claims).SignedString(key)
	require.NoError(t, err)
	enc, err := Encrypt(signed, []byte(testEncKey))
	require.NoError(t, err)
	return enc
}

func TestCreateToken_AccessAndRefresh(t *testing.T) {
	cases := map[string]time.Duration{"access": 24 * time.Hour, "refresh": 30 * 24 * time.Hour}
	for typ, ttl := range cases {
		t.Run(typ, func(t *testing.T) {
			tok, apiErr := CreateToken(CreateTokenParams{User: testUser(), Type: typ})
			require.Nil(t, apiErr)
			require.NotEmpty(t, tok)

			claims, err := GetTokenClaims(tok)
			require.NoError(t, err)
			assert.Equal(t, "ext-1", claims["id"])
			assert.Equal(t, "Ada Lovelace", claims["name"])
			assert.Equal(t, "ada@example.com", claims["email"])
			assert.Equal(t, "coach", claims["role"])
			exp := time.Unix(int64(claims["exp"].(float64)), 0)
			assert.WithinDuration(t, time.Now().Add(ttl), exp, 10*time.Second)

			expired, err := IsTokenExpired(tok)
			assert.NoError(t, err)
			assert.False(t, expired)
		})
	}
}

func TestCreateToken_InvalidType(t *testing.T) {
	tok, apiErr := CreateToken(CreateTokenParams{User: testUser(), Type: "bogus"})
	assert.Empty(t, tok)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.Code)
	assert.ErrorIs(t, apiErr.Error, models.ErrInvalidTokenType)
}

func TestCreateToken_DoesNotLeakPlainJWT(t *testing.T) {
	tok, apiErr := CreateToken(CreateTokenParams{User: testUser(), Type: "access"})
	require.Nil(t, apiErr)
	// Output is AES-GCM ciphertext, not a dotted JWT, and holds no plaintext claims.
	assert.Equal(t, 0, strings.Count(tok, "."))
	assert.NotContains(t, tok, "ada@example.com")
}

func TestIsTokenExpired_Expired(t *testing.T) {
	tok := signAndEncrypt(t, jwt.SigningMethodHS256, []byte(testSecret),
		jwt.MapClaims{"exp": time.Now().Add(-time.Hour).Unix()})
	// jwt.Parse itself rejects an expired exp -> treated as expired with error.
	expired, err := IsTokenExpired(tok)
	assert.True(t, expired)
	assert.Error(t, err)
}

func TestIsTokenExpired_Errors(t *testing.T) {
	valid := signAndEncrypt(t, jwt.SigningMethodHS256, []byte(testSecret),
		jwt.MapClaims{"exp": time.Now().Add(time.Hour).Unix()})
	noExp := signAndEncrypt(t, jwt.SigningMethodHS256, []byte(testSecret), jwt.MapClaims{"id": "x"})
	wrongSecret := signAndEncrypt(t, jwt.SigningMethodHS256, []byte("other-secret"),
		jwt.MapClaims{"exp": time.Now().Add(time.Hour).Unix()})
	none := signAndEncrypt(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType,
		jwt.MapClaims{"exp": time.Now().Add(time.Hour).Unix()})

	// Flip a byte inside the ciphertext (GCM auth must fail).
	tampered := []byte(valid)
	if tampered[len(tampered)-6] == 'A' {
		tampered[len(tampered)-6] = 'B'
	} else {
		tampered[len(tampered)-6] = 'A'
	}

	// Valid ciphertext wrapping an altered JWT signature.
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte(testSecret))
	require.NoError(t, err)
	badSig, err := Encrypt(signed[:len(signed)-3]+"abc", []byte(testEncKey))
	require.NoError(t, err)

	for name, tok := range map[string]string{
		"empty":            "",
		"not base64":       "!!!not-base64!!!",
		"too short":        "QUJD",
		"tampered cipher":  string(tampered),
		"no exp claim":     noExp,
		"wrong secret":     wrongSecret,
		"alg none":         none,
		"tampered jwt sig": badSig,
	} {
		t.Run(name, func(t *testing.T) {
			expired, err := IsTokenExpired(tok)
			assert.True(t, expired, "invalid token must be treated as expired")
			assert.Error(t, err)

			// GetTokenClaims does not require exp, so the missing-exp token is only
			// rejected by IsTokenExpired.
			if name == "no exp claim" {
				return
			}
			claims, err := GetTokenClaims(tok)
			assert.Nil(t, claims)
			assert.Error(t, err)
		})
	}
}

func TestIsTokenExpired_EncryptedWithWrongKey(t *testing.T) {
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte(testSecret))
	require.NoError(t, err)
	enc, err := Encrypt(signed, []byte("ffffffffffffffffffffffffffffffff"))
	require.NoError(t, err)
	expired, err := IsTokenExpired(enc)
	assert.True(t, expired)
	assert.Error(t, err)
}

func TestLogTokenClaims(t *testing.T) {
	logger := log.New(&bytes.Buffer{}, "", 0)

	tok, apiErr := CreateToken(CreateTokenParams{User: testUser(), Type: "access"})
	require.Nil(t, apiErr)
	assert.NoError(t, LogTokenClaims(tok, logger))

	// Extra claim and iat exercise the additional-claims branch.
	withExtra := signAndEncrypt(t, jwt.SigningMethodHS256, []byte(testSecret), jwt.MapClaims{
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "custom": "v",
	})
	assert.NoError(t, LogTokenClaims(withExtra, logger))

	noExp := signAndEncrypt(t, jwt.SigningMethodHS256, []byte(testSecret), jwt.MapClaims{"id": "x"})
	assert.Error(t, LogTokenClaims(noExp, logger))
	assert.Error(t, LogTokenClaims("", logger))
	assert.Error(t, LogTokenClaims(signAndEncrypt(t, jwt.SigningMethodHS256, []byte("bad"), jwt.MapClaims{"exp": time.Now().Add(time.Hour).Unix()}), logger))
}
