package db

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

const (
	GymOwnerInvitePending  = "pending"
	GymOwnerInviteAccepted = "accepted"
	GymOwnerInviteRevoked  = "revoked"
	GymOwnerInviteExpired  = "expired"
)

// GymOwnerInvite is an admin-issued, single-use invitation to sign up as a gym owner. Only the token hash is stored.
type GymOwnerInvite struct {
	ID             primitive.ObjectID `bson:"_id,omitempty" json:"-"`
	Email          string             `bson:"email" json:"email"` // lowercased
	Firstname      string             `bson:"firstname" json:"firstname"`
	Lastname       string             `bson:"lastname" json:"lastname"`
	TokenHash      string             `bson:"token_hash" json:"-"`
	Status         string             `bson:"status" json:"status"`
	CreatedBy      string             `bson:"created_by" json:"created_by"`
	CreatedAt      time.Time          `bson:"created_at" json:"created_at"`
	ExpiresAt      time.Time          `bson:"expires_at" json:"expires_at"`
	SentAt         time.Time          `bson:"sent_at" json:"sent_at"`
	AcceptedUserID string             `bson:"accepted_user_id,omitempty" json:"accepted_user_id,omitempty"`
	AcceptedAt     *time.Time         `bson:"accepted_at,omitempty" json:"accepted_at,omitempty"`
	RevokedAt      *time.Time         `bson:"revoked_at,omitempty" json:"revoked_at,omitempty"`
}
