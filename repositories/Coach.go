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

// Create a single invitation
func (r *CoachRepository) CreateInvitation(ctx context.Context, invitation *db.CoachClientInvitation) error {
	invitation.CreatedAt = time.Now()
	invitation.UpdatedAt = time.Now()
	_, err := r.collection.InsertOne(ctx, invitation)
	return err
}

// Bulk create invitations
func (r *CoachRepository) CreateInvitations(ctx context.Context, invitations []*db.CoachClientInvitation) error {
	if len(invitations) == 0 {
		return nil
	}
	var docs []interface{}
	now := time.Now()
	for _, inv := range invitations {
		inv.CreatedAt = now
		inv.UpdatedAt = now
		docs = append(docs, inv)
	}
	_, err := r.collection.InsertMany(ctx, docs, options.InsertMany().SetOrdered(false))
	return err
}

// Find invitation by code
func (r *CoachRepository) FindInvitationByCode(ctx context.Context, code string) (*db.CoachClientInvitation, error) {
	var inv db.CoachClientInvitation
	err := r.collection.FindOne(ctx, bson.M{"code": code}).Decode(&inv)
	if err != nil {
		return nil, err
	}
	return &inv, nil
}

// Update invitation status
func (r *CoachRepository) UpdateInvitationStatus(ctx context.Context, code string, status db.InvitationStatus) error {
	update := bson.M{
		"$set": bson.M{
			"status":     status,
			"updated_at": time.Now(),
			"accepted_at": func() *time.Time {
				if status == db.InvitationAccepted {
					t := time.Now()
					return &t
				}
				return nil
			}(),
		},
	}
	_, err := r.collection.UpdateOne(ctx, bson.M{"code": code}, update)
	return err
}

// Delete invitation
func (r *CoachRepository) DeleteInvitation(ctx context.Context, code string) error {
	_, err := r.collection.DeleteOne(ctx, bson.M{"code": code})
	return err
}

// List invitations for a coach
func (r *CoachRepository) ListInvitations(ctx context.Context, coachID string) ([]*db.CoachClientInvitation, error) {
	cursor, err := r.collection.Find(ctx, bson.M{"coach_id": coachID})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var invitations []*db.CoachClientInvitation
	for cursor.Next(ctx) {
		var inv db.CoachClientInvitation
		if err := cursor.Decode(&inv); err == nil {
			invitations = append(invitations, &inv)
		}
	}
	return invitations, nil
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

// GetAllCoachClientDetails retrieves all client details (externalid, firstname, lastname, profile_picture) for a given coach.
func (r *CoachRepository) GetAllCoachClientDetails(ctx context.Context, coachID string, userCollection *mongo.Collection) ([]map[string]interface{}, *models.ApiError) {
	pipeline := mongo.Pipeline{
		// Match coach_id
		{{Key: "$match", Value: bson.D{{Key: "coach_id", Value: coachID}}}},
		// Join with users collection
		{{Key: "$lookup", Value: bson.D{
			{Key: "from", Value: userCollection.Name()},
			{Key: "localField", Value: "client_id"},
			{Key: "foreignField", Value: "externalid"},
			{Key: "as", Value: "user"},
		}}},
		// Unwind user array
		{{Key: "$unwind", Value: bson.D{{Key: "path", Value: "$user"}, {Key: "preserveNullAndEmptyArrays", Value: false}}}},
		// Project only required fields
		{{Key: "$project", Value: bson.D{
			{Key: "externalid", Value: "$user.externalid"},
			{Key: "firstname", Value: "$user.firstname"},
			{Key: "lastname", Value: "$user.lastname"},
			{Key: "profile_picture", Value: "$user.profile_picture"},
		}}},
	}

	cursor, err := r.collection.Aggregate(ctx, pipeline)
	if err != nil {
		utils.Logger.Error("Failed to aggregate coach client details", zap.Error(err))
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
		}
	}
	defer cursor.Close(ctx)

	var results []map[string]interface{}
	if err := cursor.All(ctx, &results); err != nil {
		utils.Logger.Error("Failed to decode aggregation results", zap.Error(err))
		return nil, &models.ApiError{
			Code:  http.StatusInternalServerError,
			Error: models.ErrInternalError,
		}
	}

	return results, nil
}
