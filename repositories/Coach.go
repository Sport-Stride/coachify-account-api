package repositories

import (
	"context"
	"log"
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

	// Base pipeline for data transformation (without pagination)
	basePipeline := mongo.Pipeline{
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
	}

	// Pipeline for getting total count (same as base but with $count)
	totalCountPipeline := append(basePipeline, bson.D{
		{Key: "$count", Value: "total"},
	})

	// Pipeline for getting paginated results (base + sort + pagination)
	dataPipeline := append(basePipeline,
		// Sort by created_at desc
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
	)

	// Execute both queries concurrently for better performance
	type result struct {
		data  []map[string]interface{}
		total int64
		err   error
	}

	dataChan := make(chan result, 1)
	totalChan := make(chan result, 1)

	// Get paginated results
	go func() {
		cursor, err := r.collection.Aggregate(ctx, dataPipeline)
		if err != nil {
			dataChan <- result{err: err}
			return
		}
		defer cursor.Close(ctx)
		
		var results []map[string]interface{}
		if err := cursor.All(ctx, &results); err != nil {
			dataChan <- result{err: err}
			return
		}
		dataChan <- result{data: results}
	}()

	// Get total count
	go func() {
		totalCursor, err := r.collection.Aggregate(ctx, totalCountPipeline)
		if err != nil {
			totalChan <- result{err: err}
			return
		}
		defer totalCursor.Close(ctx)
		
		var totalResult []bson.M
		if err := totalCursor.All(ctx, &totalResult); err != nil {
			totalChan <- result{err: err}
			return
		}
		
		var total int64 = 0
		if len(totalResult) > 0 {
			if t, ok := totalResult[0]["total"].(int32); ok {
				total = int64(t)
			} else if t, ok := totalResult[0]["total"].(int64); ok {
				total = t
			}
		}
		totalChan <- result{total: total}
	}()

	// Wait for both results
	dataResult := <-dataChan
	totalResult := <-totalChan

	// Check for errors
	if dataResult.err != nil {
		return nil, 0, dataResult.err
	}
	if totalResult.err != nil {
		return nil, 0, totalResult.err
	}
	log.Printf("query: %v, results: %v, total: %v", query, dataResult.data, totalResult.total)

	return dataResult.data, int(totalResult.total), nil

	
}

// DissociateCoachClient removes a coach-client relationship
func (r *CoachRepository) DissociateCoachClient(ctx context.Context, coachID, clientID string) error {
	filter := bson.M{"coach_id": coachID, "client_id": clientID}
	_, err := r.collection.DeleteOne(ctx, filter)
	return err
}

func (r *CoachRepository) GetCoachIDByClientID(ctx context.Context, clientID string) (string, error) {
	var result struct {
		CoachID string `bson:"coach_id"`
	}
	filter := bson.M{"client_id": clientID}
	err := r.collection.FindOne(ctx, filter).Decode(&result)
	if err != nil {
		return "", err
	}
	return result.CoachID, nil
}