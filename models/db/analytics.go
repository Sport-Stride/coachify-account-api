package db

import "time"

const (
	AnalyticsScopeTeam     = "team"
	AnalyticsScopeGym      = "gym"
	AnalyticsScopePlatform = "platform"
)

// ScopeCoach is one (coach, head coach) pair visible in an analytics scope.
type ScopeCoach struct {
	CoachID       string        `json:"coach_id"`
	Firstname     string        `json:"firstname"`
	Lastname      string        `json:"lastname"`
	HeadCoachID   string        `json:"head_coach_id"`
	HeadCoachName string        `json:"head_coach_name"`
	Current       bool          `json:"current"`
	Periods       []PeriodRange `json:"periods"`
}

// AnalyticsScope is the set of coaches a caller may see, consumed by the payments API.
type AnalyticsScope struct {
	ScopeKey        string       `json:"scope_key"`
	Coaches         []ScopeCoach `json:"coaches"`
	CurrentCoachIDs []string     `json:"current_coach_ids"`
	GeneratedAt     time.Time    `json:"generated_at"`
}

// UserIdentity holds the user fields the scope needs (names and dates only).
type UserIdentity struct {
	ExternalID     string     `bson:"externalid"`
	Firstname      string     `bson:"firstname"`
	Lastname       string     `bson:"lastname"`
	CreatedAt      time.Time  `bson:"created_at"`
	HeadCoachSince *time.Time `bson:"head_coach_since,omitempty"`
}

// ClientRow holds only fields that are safe to expose (no phone, address or health data).
type ClientRow struct {
	ExternalID     string `bson:"externalid" json:"external_id"`
	Firstname      string `bson:"firstname" json:"firstname"`
	Lastname       string `bson:"lastname" json:"lastname"`
	Email          string `bson:"email" json:"email"`
	ProfilePicture string `bson:"profile_picture,omitempty" json:"profile_picture,omitempty"`
	Status         string `bson:"status" json:"status"`
	CoachID        string `bson:"coach_id" json:"coach_id"`
	CoachName      string `bson:"coach_name" json:"coach_name"`
}

// ClientRowLinked is a client row with the date the client was linked to the coach.
type ClientRowLinked struct {
	ClientRow `bson:",inline"`
	LinkedAt  time.Time `bson:"linked_at" json:"linked_at"`
}

// ScopeClientFilter filters and paginates the clients of a list of coaches.
type ScopeClientFilter struct {
	ClientIDs []string // empty = no restriction
	Q         string
	Sort      string // name | linked_at
	Page      int
	Size      int
}
