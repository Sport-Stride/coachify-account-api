package db

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Role of a gym member inside one gym.
type GymMemberRole string

// Status of a gym membership.
type GymMemberStatus string

// Where a gym membership row came from.
type GymMemberSource string

const (
	GymMemberRoleOwner     GymMemberRole = "owner"
	GymMemberRoleHeadCoach GymMemberRole = "head_coach"
	GymMemberRoleCoach     GymMemberRole = "coach"

	GymMemberStatusActive GymMemberStatus = "active"
	GymMemberStatusLeft   GymMemberStatus = "left"

	GymMemberSourceLegacyHead GymMemberSource = "legacy_head"
	GymMemberSourceLegacyTeam GymMemberSource = "legacy_team"
	GymMemberSourceInvite     GymMemberSource = "invite"
	GymMemberSourceAdmin      GymMemberSource = "admin"
)

// GymMember is one user's membership of one gym (collection gym_members).
// While GYM_MEMBERS_READ=legacy the legacy collections stay the source of truth and this row is a mirror.
type GymMember struct {
	ID           primitive.ObjectID `bson:"_id,omitempty" json:"-"`
	GymID        string             `bson:"gym_id" json:"gym_id"`
	UserID       string             `bson:"user_id" json:"user_id"`
	Role         GymMemberRole      `bson:"role" json:"role"`
	SupervisorID string             `bson:"supervisor_id,omitempty" json:"supervisor_id,omitempty"`
	Status       GymMemberStatus    `bson:"status" json:"status"`
	SeatAssigned bool               `bson:"seat_assigned" json:"seat_assigned"`
	// CoachSlot is true on active head_coach and coach rows only; the one-gym-per-coach unique index filters on it.
	CoachSlot bool `bson:"coach_slot,omitempty" json:"-"`
	// SeatAssignedAt is set when a seat is assigned (P0 never assigns one).
	SeatAssignedAt *time.Time      `bson:"seat_assigned_at,omitempty" json:"seat_assigned_at,omitempty"`
	JoinedAt       time.Time       `bson:"joined_at" json:"joined_at"`
	LeftAt         *time.Time      `bson:"left_at,omitempty" json:"left_at,omitempty"`
	AddedBy        string          `bson:"added_by" json:"added_by"`
	Source         GymMemberSource `bson:"source" json:"source"`
}
