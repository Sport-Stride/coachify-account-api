package db

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// InviteFlag is the Viper key that turns the unified InviteLink aggregate on (default false).
const InviteFlag = "INVITE_LINKS_V2"

// Kinds of invite link.
const (
	InviteKindClientPersonal  = "client_personal"
	InviteKindClientGym       = "client_gym"
	InviteKindMemberCoach     = "member_coach"
	InviteKindMemberHeadCoach = "member_head_coach"
)

// Status of an invite link.
const (
	InviteStatusActive    = "active"
	InviteStatusRevoked   = "revoked"
	InviteStatusExhausted = "exhausted"
	InviteStatusDeclined  = "declined"
)

// InviteLink is the unified invitation aggregate (collection invite_links). Only the SHA-256 hash of the token is
// stored: the raw token is shown once, at creation, and never logged. MaxUses 0 means unlimited.
type InviteLink struct {
	ID        primitive.ObjectID `bson:"_id,omitempty" json:"-"`
	Kind      string             `bson:"kind" json:"kind"`
	TokenHash string             `bson:"token_hash" json:"-"`
	CreatorID string             `bson:"creator_id" json:"creator_id"`
	// CreatorRole is the creator's role at creation: coach, head_coach, gym_owner or legacy.
	CreatorRole string `bson:"creator_role" json:"creator_role"`
	GymID       string `bson:"gym_id,omitempty" json:"gym_id,omitempty"`
	// TargetCoachID is the coach a client_gym redeemer is assigned to; "" puts the client in the pool.
	TargetCoachID string `bson:"target_coach_id,omitempty" json:"target_coach_id,omitempty"`
	// Email (lowercased) binds the link to one address.
	Email     string     `bson:"email,omitempty" json:"email,omitempty"`
	MaxUses   int        `bson:"max_uses" json:"max_uses"`
	Uses      int        `bson:"uses" json:"uses"`
	Status    string     `bson:"status" json:"status"`
	ExpiresAt *time.Time `bson:"expires_at,omitempty" json:"expires_at,omitempty"`
	CreatedAt time.Time  `bson:"created_at" json:"created_at"`
	// LegacyRef is "registration_links:<id>" for rows copied from a legacy collection.
	LegacyRef string `bson:"legacy_ref,omitempty" json:"-"`
}
