package payments

import (
"coachify-account-api/utils"
"net/http"

"go.mongodb.org/mongo-driver/bson/primitive"
)

// PaymentClient represents a client to connect to the payments API
type PaymentClient struct {
httpClient         *http.Client
baseURL            string
subscribeWithTrial string
planHexCoach       string
planHexClient      string
breaker            *utils.CircuitBreaker
target             string
}

// PaymentResponse represents the response structure from the payments API
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

// Subscription represents a user subscription stub (minimal, not the payments-api model)
type Subscription struct {
PlanID    primitive.ObjectID `bson:"plan_id" json:"plan_id"`
TrialDays int                `json:"trial_days,omitempty"`
}

// SubscriptionSummary is a minimal view of a subscription for admin use.
type SubscriptionSummary struct {
Status string `json:"status"`
Plan   *struct {
Name string `json:"name"`
} `json:"plan,omitempty"`
UsageTracking *struct {
InvitationsUsed int `json:"invitations_used"`
MessagesUsed    int `json:"messages_used"`
} `json:"usage_tracking,omitempty"`
}

// BillingInfoEntry is one entry in the bulk billing response from payments-api.
type BillingInfoEntry struct {
UserID       string               `json:"user_id"`
Subscription *SubscriptionSummary `json:"subscription"`
}

// BulkBillingResponse is the top-level shape returned by GET /admin/subscriptions/bulk-billing.
type BulkBillingResponse struct {
Billings []BillingInfoEntry `json:"billings"`
Total    int                `json:"total"`
}
