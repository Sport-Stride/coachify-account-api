package services_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models"
	"coachify-account-api/services"
)

func TestPaymentsOutageIsNotSeatOnly(t *testing.T) {
	ctx := context.Background()
	e := newEntEnv()
	e.plans.err = errors.New("payments down")
	impl := services.NewEntitlementService(e.store, e.users, e.links, e.plans, e.audit).WithSeatOnlyCheck(seatLookup{in: true})
	assert.Nil(t, impl.CheckInvitations(ctx, "c1"), "outage fails open: invites are not denied")

	// the outage result was not cached: once payments answers, the real state applies
	e.plans.err = nil
	denied := impl.CheckInvitations(ctx, "c1")
	require.NotNil(t, denied)
	assert.Equal(t, models.CodePersonalSubRequired, entErr(t, denied).Code)
}
