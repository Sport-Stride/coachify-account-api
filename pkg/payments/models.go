package payments

import (
	"net/http"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// InvitationClient represents a client to connect to the invitation API
type PaymentClient struct {
	httpClient         *http.Client
	baseURL            string
	subscribeWithTrial string
}

// InvitationResponse represents the response structure from the invitation API
type PaymentResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// BillingCycle defines how often billing occurs
type BillingCycle string

const (
	MonthlyBilling   BillingCycle = "monthly"
	QuarterlyBilling BillingCycle = "quarterly"
	YearlyBilling    BillingCycle = "yearly"
	OneTimeBilling   BillingCycle = "one_time"
)

// Subscription represents a user's subscription to a plan
type Subscription struct {
	PlanID primitive.ObjectID `bson:"plan_id" json:"plan_id"`

	// Billing information
	TrialDays int `json:"trial_days,omitempty"`
}
