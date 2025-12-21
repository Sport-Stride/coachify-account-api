package repositories

import (
	"context"
	"log"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
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
	// Build filter
	filter := bson.M{"coach_id": query.CoachID}
	if query.ClientID != "" {
		filter["client_id"] = query.ClientID
	}
	
	// Date range filter optimization
	if !query.FromDate.IsZero() || !query.ToDate.IsZero() {
		dateFilter := bson.M{}
		if !query.FromDate.IsZero() {
			dateFilter["$gte"] = query.FromDate
		}
		if !query.ToDate.IsZero() {
			dateFilter["$lte"] = query.ToDate
		}
		filter["created_at"] = dateFilter
	}
	
	// Pagination defaults
	page := query.Page
	if page < 1 {
		page = 1
	}
	size := query.Size
	if size < 1 {
		size = 10
	}

	log.Printf("ListCoachClients - CoachID: %s, Page: %d, Size: %d, Filter: %+v", 
		query.CoachID, page, size, filter)

	// Single aggregation using $facet to get both count and data in one query
	pipeline := mongo.Pipeline{
		// Stage 1: Match coach_clients by filter
		bson.D{{Key: "$match", Value: filter}},
		
		// Stage 2: Lookup to join with users collection
		bson.D{
			{Key: "$lookup", Value: bson.D{
				{Key: "from", Value: r.userColl.Name()},
				{Key: "localField", Value: "client_id"},
				{Key: "foreignField", Value: "externalid"},
				{Key: "as", Value: "user"},
			}},
		},
		
		// Stage 3: Unwind user array (filter out non-matching)
		bson.D{
			{Key: "$unwind", Value: bson.D{
				{Key: "path", Value: "$user"},
				{Key: "preserveNullAndEmptyArrays", Value: false},
			}},
		},
		
		// Stage 4: Sort BEFORE facet (important!)
		bson.D{
			{Key: "$sort", Value: bson.D{
				{Key: "created_at", Value: -1},
			}},
		},
		
		// Stage 5: Use $facet to split into count and paginated data
		bson.D{
			{Key: "$facet", Value: bson.D{
				// Count pipeline - counts all matched documents
				{Key: "total", Value: bson.A{
					bson.D{{Key: "$count", Value: "count"}},
				}},
				// Data pipeline - applies pagination and projection
				{Key: "data", Value: bson.A{
					// Skip for pagination
					bson.D{{Key: "$skip", Value: int64((page - 1) * size)}},
					// Limit for page size
					bson.D{{Key: "$limit", Value: int64(size)}},
					// Project required fields
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
							{Key: "created_at", Value: "$created_at"},
						}},
					},
				}},
			}},
		},
	}

	// Execute aggregation
	cursor, err := r.collection.Aggregate(ctx, pipeline)
	if err != nil {
		log.Printf("ERROR: Aggregation failed: %v", err)
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	// Parse result
	var facetResult []bson.M
	if err := cursor.All(ctx, &facetResult); err != nil {
		log.Printf("ERROR: Failed to decode results: %v", err)
		return nil, 0, err
	}

	if len(facetResult) == 0 {
		log.Println("No results from aggregation")
		return []map[string]interface{}{}, 0, nil
	}

	// Extract total count
	var total int64 = 0
	if totalArr, ok := facetResult[0]["total"].(primitive.A); ok && len(totalArr) > 0 {
		if totalDoc, ok := totalArr[0].(bson.M); ok {
			if count, ok := totalDoc["count"].(int32); ok {
				total = int64(count)
			} else if count, ok := totalDoc["count"].(int64); ok {
				total = count
			}
		}
	}

	// Extract data
	var results []map[string]interface{}
	if dataArr, ok := facetResult[0]["data"].(primitive.A); ok {
		for _, item := range dataArr {
			if doc, ok := item.(bson.M); ok {
				results = append(results, doc)
			}
		}
	}

	if results == nil {
		results = []map[string]interface{}{}
	}

	log.Printf("ListCoachClients - Returned %d items out of %d total (page %d, size %d)", 
		len(results), total, page, size)
	
	return results, int(total), nil
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