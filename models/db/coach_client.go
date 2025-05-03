package db

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type CoachClient struct {
	ID        primitive.ObjectID `bson:"_id,omitempty"`
	CoachID   string             `bson:"coach_id"`
	ClientID  string             `bson:"client_id"`
	CreatedAt time.Time          `bson:"created_at"`
}

// CoachClientListQuery is used for filtering and paginating coach-client relationships
// (add more fields as needed for filtering)
type CoachClientListQuery struct {
	CoachID  string    // required
	ClientID string    // optional, for filtering by client
	FromDate time.Time // optional, filter by created after
	ToDate   time.Time // optional, filter by created before
	Page     int       // pagination
	Size     int       // pagination
}
