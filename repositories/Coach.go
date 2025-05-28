package repositories

import (
	"context"
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
	userColl   *mongo.Collection
}

func NewCoachRepository(db *mongo.Database, collName string, userColl *mongo.Collection) *CoachRepository {
	collection := db.Collection(collName)

	// Create unique index on client_id
	indexModel := mongo.IndexModel{
		Keys:    bson.D{{Key: "client_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	}
	if _, err := collection.Indexes().CreateOne(context.Background(), indexModel); err != nil {
		utils.Logger.Fatal("failed to create client_id index", zap.Error(err))
	}

	return &CoachRepository{collection: collection, userColl: userColl}
}

// GetAllCoachClientDetails retrieves all client details (externalid, firstname, lastname, profile_picture) for a given coach.
func (r *CoachRepository) GetAllCoachClientDetails(ctx context.Context, coachID string) ([]map[string]interface{}, *models.ApiError) {
	pipeline := mongo.Pipeline{
		// Match coach_id
		{{Key: "$match", Value: bson.D{{Key: "coach_id", Value: coachID}}}},
		// Join with users collection
		{{Key: "$lookup", Value: bson.D{
			{Key: "from", Value: r.userColl.Name()},
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

// AddCoachClient creates a direct coach-client relationship.
func (r *CoachRepository) AddCoachClient(ctx context.Context, coachID, clientID string) error {
	doc := db.CoachClient{
		CoachID:   coachID,
		ClientID:  clientID,
		CreatedAt: time.Now(),
	}
	_, err := r.collection.InsertOne(ctx, doc)
	return err
}

// ListCoachClients returns a paginated and filtered list of coach-client relationships with enriched user details
func (r *CoachRepository) ListCoachClients(ctx context.Context, query db.CoachClientListQuery) ([]map[string]interface{}, int, error) {
	filter := bson.M{"coach_id": query.CoachID}
	if query.ClientID != "" {
		filter["client_id"] = query.ClientID
	}
	if !query.FromDate.IsZero() {
		filter["created_at"] = bson.M{"$gte": query.FromDate}
	}
	if !query.ToDate.IsZero() {
		if f, ok := filter["created_at"].(bson.M); ok {
			f["$lte"] = query.ToDate
			filter["created_at"] = f
		} else {
			filter["created_at"] = bson.M{"$lte": query.ToDate}
		}
	}
	page := query.Page
	if page < 1 {
		page = 1
	}
	size := query.Size
	if size < 1 {
		size = 10
	}

	pipeline := mongo.Pipeline{
		// Match coach_id and filters
		bson.D{
			{Key: "$match", Value: filter},
		},
		// Join with users collection
		bson.D{
			{Key: "$lookup", Value: bson.D{
				{Key: "from", Value: r.userColl.Name()},
				{Key: "localField", Value: "client_id"},
				{Key: "foreignField", Value: "externalid"},
				{Key: "as", Value: "user"},
			}},
		},
		// Unwind user array
		bson.D{
			{Key: "$unwind", Value: bson.D{
				{Key: "path", Value: "$user"},
				{Key: "preserveNullAndEmptyArrays", Value: false},
			}},
		},
		// Project required user fields
		bson.D{
			{Key: "$project", Value: bson.D{
				{Key: "externalid", Value: "$user.externalid"},
				{Key: "firstname", Value: "$user.firstname"},
				{Key: "lastname", Value: "$user.lastname"},
				{Key: "profile_picture", Value: "$user.profile_picture"},
				{Key: "email", Value: "$user.email"},
				{Key: "phone_number", Value: "$user.phone_number"},
				{Key: "address", Value: "$user.address"},
				{Key: "status", Value: "$user.status"},
				{Key: "created_at", Value: 1},
			}},
		},
		// Sort by created_at desc (optional, can be parameterized)
		bson.D{
			{Key: "$sort", Value: bson.D{
				{Key: "created_at", Value: -1},
			}},
		},
		// Pagination
		bson.D{
			{Key: "$skip", Value: int64((page - 1) * size)},
		},
		bson.D{
			{Key: "$limit", Value: int64(size)},
		},
	}

	totalCountPipeline := mongo.Pipeline{
		bson.D{
			{Key: "$match", Value: filter},
		},
		bson.D{
			{Key: "$count", Value: "total"},
		},
	}

	// Get paginated results
	cursor, err := r.collection.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)
	var results []map[string]interface{}
	if err := cursor.All(ctx, &results); err != nil {
		return nil, 0, err
	}

	// Get total count
	total := 0
	totalCursor, err := r.collection.Aggregate(ctx, totalCountPipeline)
	if err == nil {
		var totalResult []bson.M
		totalCursor.All(ctx, &totalResult)
		if len(totalResult) > 0 {
			if t, ok := totalResult[0]["total"].(int32); ok {
				total = int(t)
			}
		}
	}

	return results, total, nil
}

// DissociateCoachClient removes a coach-client relationship
func (r *CoachRepository) DissociateCoachClient(ctx context.Context, coachID, clientID string) error {
	filter := bson.M{"coach_id": coachID, "client_id": clientID}
	_, err := r.collection.DeleteOne(ctx, filter)
	return err
}
