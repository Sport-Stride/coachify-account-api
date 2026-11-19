package repositories

import (
	"coachify-account-api/models/db"
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type RegistrationLinkRepository struct {
	collection *mongo.Collection
}

// NewRegistrationLinkRepository does not create indexes: token_1 and coach_id_1 live in InitializeIndexes (indexes.go).
func NewRegistrationLinkRepository(database *mongo.Database, collName string) *RegistrationLinkRepository {
	return &RegistrationLinkRepository{collection: database.Collection(collName)}
}

// GetByCoachID returns the existing registration link for a coach, or nil if none exists.
func (r *RegistrationLinkRepository) GetByCoachID(ctx context.Context, coachID string) (*db.RegistrationLink, error) {
	var link db.RegistrationLink
	err := r.collection.FindOne(ctx, bson.M{"coach_id": coachID}).Decode(&link)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &link, nil
}

// GetByToken returns the registration link for a given token, or nil if not found.
func (r *RegistrationLinkRepository) GetByToken(ctx context.Context, token string) (*db.RegistrationLink, error) {
	var link db.RegistrationLink
	err := r.collection.FindOne(ctx, bson.M{"token": token}).Decode(&link)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &link, nil
}

// Create inserts a new registration link.
func (r *RegistrationLinkRepository) Create(ctx context.Context, link *db.RegistrationLink) error {
	link.CreatedAt = time.Now().UTC()
	_, err := r.collection.InsertOne(ctx, link)
	return err
}
