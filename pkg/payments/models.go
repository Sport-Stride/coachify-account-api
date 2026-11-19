package payments

import (
	"coachify-account-api/utils"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// PaymentClient represents a client to connect to the payments API
type PaymentClient struct {
	httpClient         *http.Client
	baseURL            string
	subscribeWithTrial string
	breaker            *utils.CircuitBreaker
	target             string
	apiKey             string
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

// SubscriptionSummaryEntry is one entry of POST /internal/subscriptions/summary.
type SubscriptionSummaryEntry struct {
	UserID           string     `json:"user_id"`
	Status           string     `json:"status"`
	PlanID           string     `json:"plan_id"`
	PlanName         string     `json:"plan_name"`
	CurrentPeriodEnd *time.Time `json:"current_period_end,omitempty"`
	// Type is personal or gym ("" from an older payments-api means personal). Consumers keyed by user_id must ignore gym.
	Type          string `json:"type,omitempty"`
	PlanOwnerType string `json:"plan_owner_type,omitempty"`
	PlanOwnerID   string `json:"plan_owner_id,omitempty"`
}

// IsPersonal reports whether the entry is a personal subscription (gym entries are never one).
func (e SubscriptionSummaryEntry) IsPersonal() bool { return e.Type != SummaryTypeGym }

type subscriptionSummaryRequest struct {
	UserIDs []string `json:"user_ids"`
}

type subscriptionSummaryResponse struct {
	Summaries []SubscriptionSummaryEntry `json:"summaries"`
}

// Summary entry types of POST /internal/subscriptions/summary. An empty type (older payments-api) is personal.
const (
	SummaryTypePersonal = "personal"
	SummaryTypeGym      = "gym"
)

// GymSummary is one entry of POST /internal/gym-subscriptions/summary. Status is active, grace, lapsed or none;
// a gym without a subscription has zero seats and no features.
type GymSummary struct {
	GymID         string     `json:"gym_id"`
	Status        string     `json:"status"`
	PlanID        string     `json:"plan_id"`
	IncludedSeats int64      `json:"included_seats"`
	ExtraSeats    int64      `json:"extra_seats"`
	TotalSeats    int64      `json:"total_seats"`
	MaxGymClients int64      `json:"max_gym_clients"`
	PeriodEnd     *time.Time `json:"period_end"`
	GraceUntil    *time.Time `json:"grace_until,omitempty"`
	Features      []string   `json:"features"`
}

type gymSummaryRequest struct {
	GymIDs []string `json:"gym_ids"`
}

type gymSummaryResponse struct {
	Summaries []GymSummary `json:"summaries"`
}
