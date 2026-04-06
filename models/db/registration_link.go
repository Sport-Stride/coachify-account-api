package db

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type RegistrationLink struct {
	ID        primitive.ObjectID `bson:"_id,omitempty"`
	Token     string             `bson:"token"`
	CoachID   string             `bson:"coach_id"`
	CreatedAt time.Time          `bson:"created_at"`
}
