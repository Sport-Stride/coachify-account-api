package router_test

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	jwt "github.com/golang-jwt/jwt/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/router"
	"coachify-account-api/utils"
)

func encTokenWith(t *testing.T, m jwt.SigningMethod, claims jwt.MapClaims) string {
	t.Helper()
	signed, err := jwt.NewWithClaims(m, claims).SignedString([]byte(testSecret))
	require.NoError(t, err)
	enc, err := utils.Encrypt(signed, []byte(testEncKey))
	require.NoError(t, err)
	return enc
}

func TestAuthMiddlewareRejectsOtherHMACAndMissingExp(t *testing.T) {
	r := gin.New()
	r.Use(router.AuthMiddleware())
	r.GET("/p", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	for name, tok := range map[string]string{
		"HS384":  encTokenWith(t, jwt.SigningMethodHS384, validClaims("u1", "coach")),
		"HS512":  encTokenWith(t, jwt.SigningMethodHS512, validClaims("u1", "coach")),
		"no exp": encTokenWith(t, jwt.SigningMethodHS256, jwt.MapClaims{"id": "u1", "role": "coach"}),
	} {
		t.Run(name, func(t *testing.T) {
			w := serve(r, "GET", "/p", "Bearer "+tok, "")
			assert.Equal(t, http.StatusUnauthorized, w.Code)
		})
	}
}
