package services_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/pkg/payments"
	"coachify-account-api/services"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCanceller struct {
	calls []string
	err   error
}

func (f *fakeCanceller) CancelGymMembership(_ context.Context, gymID, userID string) (*payments.GymMembershipCancelResult, error) {
	f.calls = append(f.calls, gymID+"/"+userID)
	if f.err != nil {
		return nil, f.err
	}
	return &payments.GymMembershipCancelResult{Status: "cancel_at_period_end"}, nil
}

func TestPaymentsMembershipBilling(t *testing.T) {
	ctx := context.Background()
	fc := &fakeCanceller{}
	require.NoError(t, services.NewPaymentsMembershipBilling(fc).CancelAtPeriodEnd(ctx, "g1", "cl1"))
	assert.Equal(t, []string{"g1/cl1"}, fc.calls)

	fc.err = errors.New("payments down")
	assert.Error(t, services.NewPaymentsMembershipBilling(fc).CancelAtPeriodEnd(ctx, "g1", "cl1"))

	// Flag off or payments not configured: the no-op, no call.
	assert.NoError(t, services.NewPaymentsMembershipBilling(nil).CancelAtPeriodEnd(ctx, "g1", "cl1"))
}

func TestDispatchBillingFailureDoesNotFailDeparture(t *testing.T) {
	f := newDispatchFixture(t)
	f.billing.err = errors.New("payments down")
	ctx := context.Background()
	require.Nil(t, f.svc.LeaveGym(ctx, "cl1", gymA))
	assert.Equal(t, "<none>", f.coachOf("cl1"), "departure committed")
	assert.Len(t, f.billing.calls, 3, "retried before the replay log")
	assert.Equal(t, []string{gymA + "/cl1"}, f.tracker.cancels)

	require.Nil(t, f.svc.Remove(ctx, "owner", gymA, "cl2"))
	assert.Equal(t, gymA+"/cl2", f.billing.calls[len(f.billing.calls)-1])
}

type seatStandingFake struct {
	st  *services.SeatStanding
	err *models.ApiError
}

func (f seatStandingFake) Standing(context.Context, string, string) (*services.SeatStanding, *models.ApiError) {
	return f.st, f.err
}

func standing(role db.GymMemberRole, state db.SeatState, unlimited bool) *services.SeatStanding {
	return &services.SeatStanding{Member: &db.GymMember{UserID: "c2", Role: role}, State: state, Unlimited: unlimited}
}

func TestDispatchAssignCoachSeatRequired(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name    string
		seats   services.GymSeatStanding
		refused bool
	}{
		{"read_only refused", seatStandingFake{st: standing(db.GymMemberRoleCoach, db.SeatStateReadOnly, false)}, true},
		{"none refused", seatStandingFake{st: standing(db.GymMemberRoleHeadCoach, db.SeatStateNone, false)}, true},
		{"seated ok", seatStandingFake{st: standing(db.GymMemberRoleCoach, db.SeatStateSeated, false)}, false},
		{"grace ok", seatStandingFake{st: standing(db.GymMemberRoleCoach, db.SeatStateGrace, false)}, false},
		{"unlimited gym ok", seatStandingFake{st: standing(db.GymMemberRoleCoach, db.SeatStateNone, true)}, false},
		{"owner target ok", seatStandingFake{st: standing(db.GymMemberRoleOwner, db.SeatStateNone, false)}, false},
		{"lookup error fails open", seatStandingFake{err: &models.ApiError{Code: 500, Error: errors.New("x")}}, false},
		{"seats off (nil)", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newDispatchFixtureSeats(t, tc.seats)
			_, apiErr := f.svc.Assign(ctx, "owner", gymA, "cl2", "c2")
			if !tc.refused {
				require.Nil(t, apiErr)
				assert.Equal(t, "c2", f.coachOf("cl2"))
				return
			}
			requireCode(t, apiErr, http.StatusConflict)
			var ee *models.EntitlementError
			require.True(t, errors.As(apiErr.Error, &ee))
			assert.Equal(t, models.CodeCoachSeatRequired, ee.Code)
			assert.Equal(t, "", f.coachOf("cl2"), "nothing written")
			assert.Zero(t, f.tx.txCalls)
		})
	}
}

func TestInternalAuthzCheckPlanManage(t *testing.T) {
	svc := gymContextSvc()
	check := []services.AuthzCheck{{Capability: "plan.manage", GymID: gymA}}
	check[0].Resource.Type, check[0].Resource.ID = "gym", gymA
	ctx := context.Background()

	res, apiErr := svc.Check(ctx, services.Actor{ID: "owner", Role: "gym_owner"}, check)
	require.Nil(t, apiErr)
	require.Len(t, res.Decisions, 1)
	assert.True(t, res.Decisions[0].Allowed)
	assert.False(t, res.Decisions[0].WouldDeny)

	for _, u := range []string{"head", "coach"} {
		res, apiErr = svc.Check(ctx, services.Actor{ID: u, Role: "coach"}, check)
		require.Nil(t, apiErr)
		d := res.Decisions[0]
		assert.False(t, d.Allowed && !d.WouldDeny, "%s must not pass plan.manage by default", u)
	}
}
