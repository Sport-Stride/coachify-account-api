package services

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
)

type stubLinkLookup struct{ coachID string }

func (s stubLinkLookup) GetByToken(context.Context, string) (*db.RegistrationLink, error) {
	return &db.RegistrationLink{Token: "tok", CoachID: s.coachID}, nil
}

type stubGuard struct {
	deny   *models.ApiError
	gotID  string
	called int
}

func (g *stubGuard) CheckInvitations(_ context.Context, coachID string) *models.ApiError {
	g.called++
	g.gotID = coachID
	return g.deny
}

// A denied guard must stop RegisterViaLink before any user lookup or creation (those repositories are zero here).
func TestRegisterViaLinkDeniedByEntitlements(t *testing.T) {
	for name, deny := range map[string]*models.ApiError{
		"feature disabled": models.NewEntitlementApiError(http.StatusForbidden, &models.EntitlementError{Code: models.CodeFeatureDisabled, Message: "feature disabled: invitations", Feature: "invitations"}),
		"quota exceeded":   models.NewEntitlementApiError(http.StatusForbidden, &models.EntitlementError{Code: models.CodeQuotaExceeded, Message: "quota exceeded: max_clients", Feature: "invitations", Limit: "max_clients"}),
	} {
		t.Run(name, func(t *testing.T) {
			g := &stubGuard{deny: deny}
			s := &AuthServiceImpl{registrationLinkRepo: stubLinkLookup{coachID: "coach-9"}}
			s.WithEntitlements(g)

			resp, apiErr := s.RegisterViaLink(context.Background(), "tok", &api.CreateUserRequest{Email: "a@b.io"})
			assert.Nil(t, resp)
			require.NotNil(t, apiErr)
			assert.Equal(t, http.StatusForbidden, apiErr.Code)
			var ee *models.EntitlementError
			require.True(t, errors.As(apiErr.Error, &ee))
			assert.Equal(t, deny.Error.(*models.EntitlementError).Code, ee.Code)
			assert.Equal(t, 1, g.called)
			assert.Equal(t, "coach-9", g.gotID, "the link's coach is checked")
		})
	}
}
