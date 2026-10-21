package db

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

const (
	GymStatusActive   = "active"
	GymStatusArchived = "archived"

	GymDefaultCurrency = "TND"
	GymDefaultTimezone = "Africa/Tunis"

	GymInvitePending  = "pending"
	GymInviteAccepted = "accepted"
	GymInviteDeclined = "declined"
	GymInviteRevoked  = "revoked"
	GymInviteExpired  = "expired"

	GymAdderAdmin    = "admin"
	GymAdderGymOwner = "gym_owner"
)

// Gym is a business owned by one gym owner (v1) that groups head coaches.
type Gym struct {
	ID              primitive.ObjectID `bson:"_id,omitempty" json:"-"`
	Name            string             `bson:"name" json:"name"`
	OwnerID         string             `bson:"owner_id" json:"owner_id"`
	Status          string             `bson:"status" json:"status"` // active | archived
	DefaultCurrency string             `bson:"default_currency" json:"default_currency"`
	Timezone        string             `bson:"timezone" json:"timezone"`
	CreatedBy       string             `bson:"created_by" json:"created_by"`
	CreatedAt       time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt       time.Time          `bson:"updated_at" json:"updated_at"`
	ArchivedAt      *time.Time         `bson:"archived_at,omitempty" json:"archived_at,omitempty"`
}

// GymUpdate lists the fields to change; nil means unchanged.
type GymUpdate struct {
	Name    *string
	OwnerID *string
	Status  *string
}

// GymHeadCoach links a head coach to exactly one gym (head_coach_id is unique).
type GymHeadCoach struct {
	ID           primitive.ObjectID `bson:"_id,omitempty" json:"-"`
	GymID        string             `bson:"gym_id" json:"gym_id"`
	HeadCoachID  string             `bson:"head_coach_id" json:"head_coach_id"`
	AddedBy      string             `bson:"added_by" json:"added_by"`
	AddedByRole  string             `bson:"added_by_role" json:"added_by_role"` // admin | gym_owner
	InvitationID string             `bson:"invitation_id,omitempty" json:"invitation_id,omitempty"`
	AddedAt      time.Time          `bson:"added_at" json:"added_at"`
}

// GymInvitation invites an email address to become a head coach of a gym.
type GymInvitation struct {
	ID          primitive.ObjectID `bson:"_id,omitempty" json:"-"`
	GymID       string             `bson:"gym_id"`
	InviterID   string             `bson:"inviter_id"`
	Email       string             `bson:"email"` // lowercased
	InviteeID   string             `bson:"invitee_id,omitempty"`
	Status      string             `bson:"status"`
	ExpiresAt   time.Time          `bson:"expires_at"` // enforced by the service, no TTL index
	CreatedAt   time.Time          `bson:"created_at"`
	RespondedAt *time.Time         `bson:"responded_at,omitempty"`
}

// GymSummary is one gym of a gym owner.
type GymSummary struct {
	GymID          string `bson:"gym_id" json:"gym_id"`
	Name           string `bson:"name" json:"name"`
	Status         string `bson:"status" json:"status"`
	HeadCoachCount int    `bson:"head_coach_count" json:"head_coach_count"`
}

// AdminGymRow is a gym row of the admin list.
type AdminGymRow struct {
	GymID          string     `bson:"gym_id" json:"gym_id"`
	Name           string     `bson:"name" json:"name"`
	Status         string     `bson:"status" json:"status"`
	OwnerID        string     `bson:"owner_id" json:"owner_id"`
	OwnerName      string     `bson:"owner_name" json:"owner_name"`
	OwnerEmail     string     `bson:"owner_email" json:"owner_email"`
	OwnerPicture   string     `bson:"owner_profile_picture,omitempty" json:"owner_profile_picture,omitempty"`
	HeadCoachCount int        `bson:"head_coach_count" json:"head_coach_count"`
	CreatedAt      time.Time  `bson:"created_at" json:"created_at"`
	ArchivedAt     *time.Time `bson:"archived_at,omitempty" json:"archived_at,omitempty"`
}

// GymOverview aggregates a whole gym.
type GymOverview struct {
	HeadCoaches        int `bson:"head_coaches" json:"head_coaches"`
	Coaches            int `bson:"coaches" json:"coaches"`
	ActiveCoaches      int `bson:"active_coaches" json:"active_coaches"`
	InactiveCoaches14d int `bson:"inactive_coaches_14d" json:"inactive_coaches_14d"`
	TotalClients       int `bson:"total_clients" json:"total_clients"`
	ActiveClients      int `bson:"active_clients" json:"active_clients"`
	NewClients30d      int `bson:"new_clients_30d" json:"new_clients_30d"`
}

// GymHeadCoachRow is a head coach of a gym; client_count sums the clients of the head coach's team.
type GymHeadCoachRow struct {
	HeadCoachRow `bson:",inline"`
	AddedAt      time.Time `bson:"added_at" json:"added_at"`
	ClientCount  int       `bson:"client_count" json:"client_count"`
}

// AdminGymHeadCoachRow is a current head coach of a gym in the admin view.
type AdminGymHeadCoachRow struct {
	ExternalID     string    `bson:"externalid" json:"external_id"`
	Firstname      string    `bson:"firstname" json:"firstname"`
	Lastname       string    `bson:"lastname" json:"lastname"`
	Email          string    `bson:"email" json:"email"`
	ProfilePicture string    `bson:"profile_picture,omitempty" json:"profile_picture,omitempty"`
	Status         string    `bson:"status" json:"status"`
	AddedAt        time.Time `bson:"added_at" json:"added_at"`
	AddedByRole    string    `bson:"added_by_role" json:"added_by_role"`
	TeamSize       int       `bson:"team_size" json:"team_size"`
}

// GymCoachRow is a team coach of one of the gym's head coaches.
type GymCoachRow struct {
	TeamCoachRow  `bson:",inline"`
	HeadCoachID   string `bson:"head_coach_id" json:"head_coach_id"`
	HeadCoachName string `bson:"head_coach_name" json:"head_coach_name"`
}

// GymClientRow holds only fields that are safe to expose to a gym owner (no phone, address or health data).
type GymClientRow struct {
	ExternalID     string    `bson:"externalid" json:"external_id"`
	Firstname      string    `bson:"firstname" json:"firstname"`
	Lastname       string    `bson:"lastname" json:"lastname"`
	Email          string    `bson:"email" json:"email"`
	ProfilePicture string    `bson:"profile_picture,omitempty" json:"profile_picture,omitempty"`
	Status         string    `bson:"status" json:"status"`
	CoachID        string    `bson:"coach_id" json:"coach_id"`
	CoachName      string    `bson:"coach_name" json:"coach_name"`
	HeadCoachID    string    `bson:"head_coach_id" json:"head_coach_id"`
	LinkedAt       time.Time `bson:"linked_at" json:"linked_at"`
}

// GymCoachQuery paginates and filters the coaches of a gym.
type GymCoachQuery struct {
	HeadCoachID string
	Page        int
	Size        int
	Q           string
	Status      string
	Sort        string // name | clients | last_login
}

// GymClientQuery paginates and filters the clients of a gym.
type GymClientQuery struct {
	HeadCoachID string
	CoachID     string
	Page        int
	Size        int
	Q           string
	Sort        string // name | linked_at
}

// GymInvitationView is a pending invitation as the invited coach sees it.
type GymInvitationView struct {
	ID        string    `bson:"id" json:"id"`
	GymName   string    `bson:"gym_name" json:"gym_name"`
	OwnerName string    `bson:"owner_name" json:"owner_name"`
	ExpiresAt time.Time `bson:"expires_at" json:"expires_at"`
}
