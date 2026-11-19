package db

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Status of a gym client relationship.
type GymClientStatus string

const (
	GymClientStatusActive GymClientStatus = "active"
	GymClientStatusLeft   GymClientStatus = "left"
)

// GymClient is the relationship between a client and a gym (collection gym_clients). It is separate from the personal
// coach_clients link: the gym never sees or reassigns personal clients. CoachID "" means the unassigned pool.
type GymClient struct {
	ID       primitive.ObjectID `bson:"_id,omitempty" json:"-"`
	GymID    string             `bson:"gym_id" json:"gym_id"`
	ClientID string             `bson:"client_id" json:"client_id"`
	CoachID  string             `bson:"coach_id" json:"coach_id"`
	Status   GymClientStatus    `bson:"status" json:"status"`
	JoinedAt time.Time          `bson:"joined_at" json:"joined_at"`
	// AssignedAt is when the current coach was set; nil while the client is in the pool.
	AssignedAt *time.Time `bson:"assigned_at,omitempty" json:"assigned_at,omitempty"`
	LeftAt     *time.Time `bson:"left_at,omitempty" json:"left_at,omitempty"`
	InviteID   string     `bson:"invite_id,omitempty" json:"invite_id,omitempty"`
	// ConsentAt is when the client agreed to become a gym client (redeeming a gym invite). Always set.
	ConsentAt time.Time `bson:"consent_at" json:"consent_at"`
}
