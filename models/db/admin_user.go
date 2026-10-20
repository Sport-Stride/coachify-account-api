package db

import "time"

// Admin user list sort keys and orders.
const (
	AdminUserSortCreatedAt = "created_at"
	AdminUserSortLastLogin = "last_login"
	AdminUserSortName      = "name"
	AdminUserSortEmail     = "email"

	AdminOrderAsc  = "asc"
	AdminOrderDesc = "desc"
)

// AdminUserQuery filters, sorts and paginates the admin user list.
type AdminUserQuery struct {
	Q      string
	Role   string
	Status string
	IDs    []string
	Page   int
	Size   int
	Sort   string
	Order  string
}

// AdminUserRow is the admin-safe projection of a user (no password, tokens or codes).
type AdminUserRow struct {
	ExternalID             string     `bson:"externalid" json:"external_id"`
	Firstname              string     `bson:"firstname" json:"firstname"`
	Lastname               string     `bson:"lastname" json:"lastname"`
	Email                  string     `bson:"email" json:"email"`
	Role                   string     `bson:"role" json:"role"`
	Status                 string     `bson:"status" json:"status"`
	ProfilePicture         string     `bson:"profile_picture" json:"profile_picture"`
	VerificationStatus     bool       `bson:"verification_status" json:"verification_status"`
	MatriculeFiscaleStatus string     `bson:"matricule_fiscale_status" json:"matricule_fiscale_status"`
	IsHeadCoach            bool       `bson:"is_head_coach" json:"is_head_coach"`
	CreatedAt              time.Time  `bson:"created_at" json:"created_at"`
	LastLogin              *time.Time `bson:"last_login" json:"last_login"`
}

// AdminUserAddress mirrors Address with json tags and plain strings.
type AdminUserAddress struct {
	City       string `bson:"city,omitempty" json:"city,omitempty"`
	Country    string `bson:"country,omitempty" json:"country,omitempty"`
	Line1      string `bson:"line1,omitempty" json:"line1,omitempty"`
	Line2      string `bson:"line2,omitempty" json:"line2,omitempty"`
	PostalCode string `bson:"postal_code,omitempty" json:"postal_code,omitempty"`
	State      string `bson:"state,omitempty" json:"state,omitempty"`
}

// AdminUserCoach is the coach of a client.
type AdminUserCoach struct {
	ExternalID string `bson:"external_id" json:"external_id"`
	Firstname  string `bson:"firstname" json:"firstname"`
	Lastname   string `bson:"lastname" json:"lastname"`
}

// AdminUserDetail is the row plus profile and relationship data. Matricule fiscale: status only.
type AdminUserDetail struct {
	AdminUserRow  `bson:",inline"`
	PhoneNumber   string            `bson:"phone_number,omitempty" json:"phone_number,omitempty"`
	Gender        string            `bson:"gender,omitempty" json:"gender,omitempty"`
	Address       *AdminUserAddress `bson:"address,omitempty" json:"address,omitempty"`
	Metadata      *UserMetadata     `bson:"metadata,omitempty" json:"metadata,omitempty"`
	HeadCoachID   string            `bson:"head_coach_id,omitempty" json:"head_coach_id,omitempty"`
	HeadCoachName string            `bson:"head_coach_name,omitempty" json:"head_coach_name,omitempty"`
	Coach         *AdminUserCoach   `bson:"coach" json:"coach"`
	ClientsCount  *int              `bson:"clients_count,omitempty" json:"clients_count,omitempty"`
	UpdatedAt     time.Time         `bson:"updated_at" json:"updated_at"`
}

// AdminSignupPoint is the number of signups of one role on one local day.
type AdminSignupPoint struct {
	Date  string `json:"date"`
	Role  string `json:"role"`
	Count int    `json:"count"`
}

// AdminNewUsers counts recent signups.
type AdminNewUsers struct {
	Last7d  int `json:"last_7d"`
	Last30d int `json:"last_30d"`
}

// AdminUserStats is the admin dashboard summary of the user base.
type AdminUserStats struct {
	ByRole           map[string]int     `json:"by_role"`
	ByStatus         map[string]int     `json:"by_status"`
	NewUsers         AdminNewUsers      `json:"new_users"`
	Signups          []AdminSignupPoint `json:"signups"`
	ToConfirm        int                `json:"to_confirm"`
	PendingMatricule int                `json:"pending_matricule"`
	GeneratedAt      time.Time          `json:"generated_at"`
}
