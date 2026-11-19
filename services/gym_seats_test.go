package services_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models"
	"coachify-account-api/pkg/payments"
	"coachify-account-api/services"
)

type seatLookup struct{ in bool }

func (s seatLookup) InGymAsCoach(context.Context, string) (bool, error) { return s.in, nil }

func TestSeatOnlyCoachDeniedPersonalInvites(t *testing.T) {
	ctx := context.Background()

	// flag on, coach in a gym with no personal plan: denied
	e := newEntEnv()
	impl := services.NewEntitlementService(e.store, e.users, e.links, e.plans, e.audit).WithSeatOnlyCheck(seatLookup{in: true})
	denied := impl.CheckInvitations(ctx, "c1")
	require.NotNil(t, denied)
	assert.Equal(t, http.StatusForbidden, denied.Code)
	assert.Equal(t, models.CodePersonalSubRequired, entErr(t, denied).Code)

	// a personal plan lifts the denial
	e = newEntEnv()
	e.plans.byUser["c1"] = payments.SubscriptionSummaryEntry{UserID: "c1", PlanID: "p1", Status: "active", Type: payments.SummaryTypePersonal}
	impl = services.NewEntitlementService(e.store, e.users, e.links, e.plans, e.audit).WithSeatOnlyCheck(seatLookup{in: true})
	assert.Nil(t, impl.CheckInvitations(ctx, "c1"))

	// a gym subscription held by the coach is not a personal plan
	e = newEntEnv()
	e.plans.byUser["c1"] = payments.SubscriptionSummaryEntry{UserID: "c1", PlanID: "gp", Status: "active", Type: payments.SummaryTypeGym}
	impl = services.NewEntitlementService(e.store, e.users, e.links, e.plans, e.audit).WithSeatOnlyCheck(seatLookup{in: true})
	require.NotNil(t, impl.CheckInvitations(ctx, "c1"))

	// not in a gym: unchanged
	e = newEntEnv()
	impl = services.NewEntitlementService(e.store, e.users, e.links, e.plans, e.audit).WithSeatOnlyCheck(seatLookup{in: false})
	assert.Nil(t, impl.CheckInvitations(ctx, "c1"))

	// flag off (no lookup): unchanged
	e = newEntEnv()
	assert.Nil(t, e.guard.CheckInvitations(ctx, "c1"))
}
