package services

import (
	"context"

	"coachify-account-api/pkg/payments"
)

// MembershipCanceller is the payments call behind GymMembershipBilling; *payments.PaymentClient satisfies it.
type MembershipCanceller interface {
	CancelGymMembership(ctx context.Context, gymID, userID string) (*payments.GymMembershipCancelResult, error)
}

var _ MembershipCanceller = (*payments.PaymentClient)(nil)

// paymentsMembershipBilling ends the gym membership plan subscription at the period end through payments-api
// (GYM_MEMBERSHIP_PLANS_ENABLED). A client without a membership subscription is a success ("none").
type paymentsMembershipBilling struct{ payments MembershipCanceller }

// NewPaymentsMembershipBilling builds the real GymMembershipBilling; nil payments gives the no-op.
func NewPaymentsMembershipBilling(p MembershipCanceller) GymMembershipBilling {
	if p == nil {
		return noopMembershipBilling{}
	}
	return paymentsMembershipBilling{payments: p}
}

func (b paymentsMembershipBilling) CancelAtPeriodEnd(ctx context.Context, gymID, clientID string) error {
	_, err := b.payments.CancelGymMembership(ctx, gymID, clientID)
	return err
}
