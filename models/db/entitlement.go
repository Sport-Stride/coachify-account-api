package db

import "time"

// Subject roles stored on entitlement grants.
const (
	EntitlementRoleCoach  = "coach"
	EntitlementRoleClient = "client"
)

// Plan default ids that are not payments plan ids.
const (
	PlanDefaultCoach  = "_coach"
	PlanDefaultClient = "_client"
)

// FeatureGrant is one feature entry of a grant or plan default. A nil Enabled and a missing limit mean inherit.
type FeatureGrant struct {
	Enabled *bool            `bson:"enabled,omitempty" json:"enabled,omitempty"`
	Limits  map[string]int64 `bson:"limits,omitempty" json:"limits,omitempty"`
}

// EntitlementGrant is a per-user override document (collection entitlement_grants).
// Coach documents are written by admins; client documents by the granting coach (CoachID).
type EntitlementGrant struct {
	SubjectID     string                  `bson:"subject_id" json:"subject_id"`
	SubjectRole   string                  `bson:"subject_role" json:"subject_role"`
	CoachID       string                  `bson:"coach_id,omitempty" json:"coach_id,omitempty"`
	Features      map[string]FeatureGrant `bson:"features" json:"features"`
	Version       int64                   `bson:"version" json:"version"`
	UpdatedBy     string                  `bson:"updated_by" json:"updated_by"`
	UpdatedByRole string                  `bson:"updated_by_role" json:"updated_by_role"`
	UpdatedAt     time.Time               `bson:"updated_at" json:"updated_at"`
}

// PlanDefault holds default feature values for a payments plan or for a whole role (collection entitlement_plan_defaults).
type PlanDefault struct {
	PlanID    string                  `bson:"plan_id" json:"plan_id"`
	Features  map[string]FeatureGrant `bson:"features" json:"features"`
	UpdatedBy string                  `bson:"updated_by" json:"updated_by"`
	UpdatedAt time.Time               `bson:"updated_at" json:"updated_at"`
}

// EntitlementUsage holds usage counters per feature (collection entitlement_usage).
type EntitlementUsage struct {
	SubjectID string                      `bson:"subject_id" json:"subject_id"`
	Counters  map[string]map[string]int64 `bson:"counters" json:"counters"`
	UpdatedAt time.Time                   `bson:"updated_at" json:"updated_at"`
}

// CoachAccessRow is one coach as returned by the admin access aggregation (user + client count + grant + usage).
type CoachAccessRow struct {
	ExternalID  string            `bson:"externalid"`
	Firstname   string            `bson:"firstname"`
	Lastname    string            `bson:"lastname"`
	Email       string            `bson:"email"`
	Role        string            `bson:"role"`
	Status      string            `bson:"status"`
	IsHeadCoach bool              `bson:"is_head_coach"`
	LastLogin   time.Time         `bson:"last_login"`
	ClientCount int               `bson:"client_count"`
	Grant       *EntitlementGrant `bson:"grant,omitempty"`
	Usage       *EntitlementUsage `bson:"usage,omitempty"`
}

// CoachAccessQuery filters the admin coach access list. Disabled with Feature matches coaches whose
// own override disables that feature.
type CoachAccessQuery struct {
	Page     int
	Size     int
	Q        string
	Status   string
	Feature  string
	Disabled bool
}

// ClientAccessRow is one linked client with the grant row, if any.
type ClientAccessRow struct {
	ExternalID string            `bson:"externalid"`
	Firstname  string            `bson:"firstname"`
	Lastname   string            `bson:"lastname"`
	Email      string            `bson:"email"`
	Status     string            `bson:"status"`
	LastLogin  time.Time         `bson:"last_login"`
	Grant      *EntitlementGrant `bson:"grant,omitempty"`
}
