package utils

import (
	"bytes"
	"log"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestTokenParsingRejectsOtherHMACMethods(t *testing.T) {
	claims := jwt.MapClaims{"id": "x", "exp": time.Now().Add(time.Hour).Unix()}
	for name, m := range map[string]jwt.SigningMethod{"HS384": jwt.SigningMethodHS384, "HS512": jwt.SigningMethodHS512} {
		t.Run(name, func(t *testing.T) {
			tok := signAndEncrypt(t, m, []byte(testSecret), claims)
			expired, err := IsTokenExpired(tok)
			assert.True(t, expired)
			assert.Error(t, err)
			c, err := GetTokenClaims(tok)
			assert.Nil(t, c)
			assert.Error(t, err)
			assert.Error(t, LogTokenClaims(tok, log.New(&bytes.Buffer{}, "", 0)))
		})
	}
}

func TestGetTokenClaimsRequiresExp(t *testing.T) {
	tok := signAndEncrypt(t, jwt.SigningMethodHS256, []byte(testSecret), jwt.MapClaims{"id": "x"})
	c, err := GetTokenClaims(tok)
	assert.Nil(t, c)
	assert.Error(t, err)
}

func TestLogTokenClaimsDoesNotLogPII(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	orig := Logger
	Logger = LogWrapperObj{Logger: zap.New(core).Sugar()}
	t.Cleanup(func() { Logger = orig })

	tok, apiErr := CreateToken(CreateTokenParams{User: testUser(), Type: "access"})
	require.Nil(t, apiErr)
	require.NoError(t, LogTokenClaims(tok, log.New(&bytes.Buffer{}, "", 0)))

	require.NotZero(t, logs.Len())
	for _, e := range logs.All() {
		for _, f := range e.Context {
			assert.NotEqual(t, "email", f.Key)
			assert.NotContains(t, f.String, "ada@example.com")
			assert.NotContains(t, f.String, "Ada")
			if f.Interface != nil {
				assert.NotContains(t, f.Interface, "ada@example.com")
			}
		}
	}
	var sawID bool
	for _, e := range logs.All() {
		for _, f := range e.Context {
			if f.Key == "userId" && f.String == "ext-1" {
				sawID = true
			}
		}
	}
	assert.True(t, sawID, "user id is still logged")
}
