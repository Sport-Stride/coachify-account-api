package db

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type CoachClient struct {
	ID        primitive.ObjectID `bson:"_id,omitempty"`
	CoachID   string             `bson:"coach_id" validate:"required"`
	ClientID  string             `bson:"client_id" validate:"required"`
	Status    string             `bson:"status"`     // e.g., "invited", "accepted", "rejected"
	InvitedAt time.Time          `bson:"invited_at"` // Timestamp of the invitation
	CreatedAt time.Time          `bson:"created_at"`
	UpdatedAt time.Time          `bson:"updated_at"`
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
