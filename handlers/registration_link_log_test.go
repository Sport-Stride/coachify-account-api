package handlers_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"coachify-account-api/models/api"
)

func TestRegisterViaLink_DoesNotLogResponseSecrets(t *testing.T) {
	obsCore, logs := observer.New(zapcore.DebugLevel)
	restore := zap.ReplaceGlobals(zap.New(obsCore))
	defer restore()

	tok, hash := "access-secret-token", "$2a$10$hashhashhash"
	s := newSvcs()
	s.auth.On("RegisterViaLink", mock.Anything, "tok", mock.Anything).Return(
		&api.RegisterResponse{User: api.ApiUserResponse{Email: "pii@example.com", Password: hash, Token: &tok}, AuthToken: tok}, nil)

	do(newAPIRouter(s, "", ""), "POST", "/coach/registration-link/tok/register", `{"email":"pii@example.com","password":"Passw0rd!x"}`)

	for _, e := range logs.All() {
		line := e.Message
		for _, f := range e.Context {
			line += " " + f.String + " " + f.Key
		}
		assert.NotContains(t, line, tok)
		assert.NotContains(t, line, hash)
		assert.NotContains(t, line, "pii@example.com")
	}
}
