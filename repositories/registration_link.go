package repositories

import (
	"coachify-account-api/models/db"
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"coachify-account-api/utils"
)

type RegistrationLinkRepository struct {
	collection *mongo.Collection
}

func NewRegistrationLinkRepository(database *mongo.Database, collName string) *RegistrationLinkRepository {
	collection := database.Collection(collName)

	// Unique index on token
	tokenIndex := mongo.IndexModel{
		Keys:    bson.D{{Key: "token", Value: 1}},
		Options: options.Index().SetUnique(true),
	}
	if _, err := collection.Indexes().CreateOne(context.Background(), tokenIndex); err != nil {
		utils.Logger.Fatal("failed to create token index on registration_links", zap.Error(err))
	}

	// Unique index on coach_id (one link per coach)
	coachIndex := mongo.IndexModel{
		Keys:    bson.D{{Key: "coach_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	}
	if _, err := collection.Indexes().CreateOne(context.Background(), coachIndex); err != nil {
		utils.Logger.Fatal("failed to create coach_id index on registration_links", zap.Error(err))
	}

	return &RegistrationLinkRepository{collection: collection}
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
