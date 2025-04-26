package db

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type InvitationStatus string

const (
	InvitationPending  InvitationStatus = "pending"
	InvitationAccepted InvitationStatus = "accepted"
	InvitationRevoked  InvitationStatus = "revoked"
)

type CoachClientInvitation struct {
	ID         primitive.ObjectID `bson:"_id,omitempty"`
	CoachID    string             `bson:"coach_id"`
	Email      string             `bson:"email"`
	Code       string             `bson:"code"`
	Status     InvitationStatus   `bson:"status"`
	CreatedAt  time.Time          `bson:"created_at"`
	UpdatedAt  time.Time          `bson:"updated_at"`
	AcceptedAt *time.Time         `bson:"accepted_at,omitempty"`
}

type SearchClient struct {
	Page    int
	Size    int
	Query   string
	Filters ClientFilters
}

type ClientFilters struct {
	Status      string
	JoinedAfter time.Time
}
