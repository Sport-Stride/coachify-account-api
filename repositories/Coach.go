package repositories

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/utils"
)

type CoachRepository struct {
	collection *mongo.Collection
}

func NewCoachRepository(db *mongo.Database, collName string) *CoachRepository {
	collection := db.Collection(collName)

	// Create unique index on client_id
	indexModel := mongo.IndexModel{
		Keys:    bson.D{{Key: "client_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	}
	if _, err := collection.Indexes().CreateOne(context.Background(), indexModel); err != nil {
		utils.Logger.Fatal("failed to create client_id index", zap.Error(err))
	}

	return &CoachRepository{collection: collection}
}

// GetAllCoachClientIDs retrieves all distinct client IDs associated with the given coach.
func (r *CoachRepository) GetAllCoachClientIDs(ctx context.Context, coachID string) ([]string, *models.ApiError) {
	filter := bson.M{"coach_id": coachID}

	// Use Distinct to fetch all unique client_id values.
	results, err := r.collection.Distinct(ctx, "client_id", filter)
	if err != nil {
		utils.Logger.Error("Failed to retrieve distinct client IDs", zap.Error(err))
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
		}
	}

	clientIDs := make([]string, 0, len(results))
	for _, v := range results {
		if idStr, ok := v.(string); ok {
			clientIDs = append(clientIDs, idStr)
		} else {
			utils.Logger.Error("Unexpected type for client_id", zap.Any("value", v))
			return nil, &models.ApiError{
				Code:  http.StatusInternalServerError,
				Error: models.ErrInternalError,
			}
		}
	}

	return clientIDs, nil
}

func (r *CoachRepository) GetCoachClientIDs(ctx context.Context, coachID string, search db.SearchClient) ([]string, int, *models.ApiError) {
	filter := bson.M{"coach_id": coachID}
	opts := options.Find().
		SetProjection(bson.M{"client_id": 1}).
		SetSkip(int64((search.Page - 1) * search.Size)).
		SetLimit(int64(search.Size))

	cursor, err := r.collection.Find(ctx, filter, opts)
	if err != nil {
		utils.Logger.Error("Failed to find coach clients", zap.Error(err))
		return []string{}, 0, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
		}
	}
	defer cursor.Close(ctx)

	// Initialize the slice as an empty slice instead of nil.
	clientIDs := []string{}

	for cursor.Next(ctx) {
		var result struct {
			ClientID string `bson:"client_id"`
		}
		if err := cursor.Decode(&result); err != nil {
			utils.Logger.Error("Failed to decode coach client", zap.Error(err))
			return []string{}, 0, &models.ApiError{
				Code:  http.StatusInternalServerError,
				Error: models.ErrInternalError,
			}
		}
		clientIDs = append(clientIDs, result.ClientID)
	}

	total, err := r.collection.CountDocuments(ctx, filter)
	if err != nil {
		utils.Logger.Error("Failed to count coach clients", zap.Error(err))
		return []string{}, 0, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
		}
	}

	return clientIDs, int(total), nil
}

// repositories/coach_repository.go
func (r *CoachRepository) CreateCoachClient(ctx context.Context, coachClient *db.CoachClient) *models.ApiError {
	coachClient.CreatedAt = time.Now()
	coachClient.UpdatedAt = time.Now()

	_, err := r.collection.InsertOne(ctx, coachClient)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return &models.ApiError{
				Code:  http.StatusConflict,
				Error: models.ErrClientAlreadyLinked,
			}
		}
		utils.Logger.Error("failed to create coach client", zap.Error(err))
		return &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
		}
	}
	return nil
}

func (r *CoachRepository) GetUserByEmail(ctx context.Context, email string) (*db.User, *models.ApiError) {
	filter := bson.M{"email": email}
	var user db.User
	err := r.collection.FindOne(ctx, filter).Decode(&user)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		utils.Logger.Error("failed to find user by email", zap.Error(err))
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
		}
	}
	return &user, nil
}

func (r *CoachRepository) FindInvitation(ctx context.Context, clientID, coachID string) (*db.CoachClient, *models.ApiError) {
	var invitation db.CoachClient
	err := r.collection.FindOne(ctx, bson.M{"client_id": clientID, "coach_id": coachID}).Decode(&invitation)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, nil
		}
		utils.Logger.Error("Failed to find invitation", zap.Error(err))
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
		}
	}
	return &invitation, nil
}

func (r *CoachRepository) CreateInvitations(ctx context.Context, invitations []*db.CoachClient) error {
	if len(invitations) == 0 {
		return nil
	}

	var models []mongo.WriteModel

	for _, inv := range invitations {
		// Build a filter based on the composite key: coachID and userExternalID.
		filter := bson.M{
			"coach_id":  inv.CoachID,
			"client_id": inv.ClientID,
		}

		// Prepare the update document.
		// Using $set will update only the fields present in the provided invitation structure.
		// You can customize this if only a subset of fields should be updated.
		update := bson.M{"$set": inv}

		// Create an upsert model: update if exists, insert if not.
		model := mongo.NewUpdateOneModel().
			SetFilter(filter).
			SetUpdate(update).
			SetUpsert(true)

		models = append(models, model)
	}

	// Use unordered bulk write to improve performance and allow parallel execution.
	opts := options.BulkWrite().SetOrdered(false)

	// Execute the bulk write operation.
	_, err := r.collection.BulkWrite(ctx, models, opts)
	if err != nil {
		return err
	}

	return nil
}

func (r *CoachRepository) CreateInvitationsA(ctx context.Context, clientIDs []string, coachID string) error {
	if len(clientIDs) == 0 {
		return nil
	}

	// Prepare invitations for bulk insert
	var invitations []interface{}
	for _, clientID := range clientIDs {
		invitation := bson.M{
			"coach_id":   coachID,
			"client_id":  clientID,
			"status":     "pending",
			"created_at": time.Now(),
			"updated_at": time.Now(),
		}
		invitations = append(invitations, invitation)
	}

	// Perform bulk insert
	opts := options.InsertMany().SetOrdered(false) // Continue on errors
	_, err := r.collection.InsertMany(ctx, invitations, opts)
	if err != nil {
		if mongoErr, ok := err.(mongo.BulkWriteException); ok {
			for _, writeErr := range mongoErr.WriteErrors {
				fmt.Printf("Failed to insert invitation: %v\n", writeErr)
			}
		}
		return fmt.Errorf("failed to insert invitations: %w", err)
	}

	return nil
}

func (r *CoachRepository) CreateInvitationsBulk(ctx context.Context, invitations []*db.CoachClient) *models.ApiError {
	docs := make([]interface{}, len(invitations))
	for i, inv := range invitations {
		docs[i] = inv
	}
	_, err := r.collection.InsertMany(ctx, docs)
	if err != nil {
		utils.Logger.Error("Failed to create bulk invitations", zap.Error(err))
		return &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
		}
	}
	return nil
}
