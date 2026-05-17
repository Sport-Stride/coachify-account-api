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
		// Join with users collection and project only required fields.
		{{Key: "$lookup", Value: bson.D{
			{Key: "from", Value: r.userColl.Name()},
			{Key: "let", Value: bson.D{{Key: "client_id", Value: "$client_id"}}},
			{Key: "pipeline", Value: bson.A{
				bson.D{{Key: "$match", Value: bson.D{{Key: "$expr", Value: bson.D{{Key: "$eq", Value: bson.A{"$externalid", "$$client_id"}}}}}}},
				bson.D{{Key: "$project", Value: bson.D{
					{Key: "externalid", Value: 1},
					{Key: "firstname", Value: 1},
					{Key: "lastname", Value: 1},
					{Key: "profile_picture", Value: 1},
				}}},
			}},
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

	// Single aggregation using $facet to get both count and paginated data in one query.
	// Keep total semantics aligned with previous behavior by counting only rows with a matching user.
	pipeline := mongo.Pipeline{
		// Stage 1: Match coach_clients by filter
		bson.D{{Key: "$match", Value: filter}},

		// Stage 2: Split into total and data branches.
		// Avoid sorting in the total branch and push pagination before the heavy lookup in the data branch.
		bson.D{
			{Key: "$facet", Value: bson.D{
				// Count pipeline - preserve previous semantics by counting only entries with a matching user.
				{Key: "total", Value: bson.A{
					bson.D{{Key: "$lookup", Value: bson.D{
						{Key: "from", Value: r.userColl.Name()},
						{Key: "let", Value: bson.D{{Key: "client_id", Value: "$client_id"}}},
						{Key: "pipeline", Value: bson.A{
							bson.D{{Key: "$match", Value: bson.D{{Key: "$expr", Value: bson.D{{Key: "$eq", Value: bson.A{"$externalid", "$$client_id"}}}}}}},
							bson.D{{Key: "$project", Value: bson.D{{Key: "_id", Value: 1}}}},
						}},
						{Key: "as", Value: "user"},
					}}},
					bson.D{{Key: "$unwind", Value: bson.D{{Key: "path", Value: "$user"}, {Key: "preserveNullAndEmptyArrays", Value: false}}}},
					bson.D{{Key: "$count", Value: "count"}},
				}},
				// Data pipeline - applies pagination and projection
				{Key: "data", Value: bson.A{
					// Sort only in data branch.
					bson.D{{Key: "$sort", Value: bson.D{{Key: "created_at", Value: -1}}}},
					// Join with users before paginating so that $unwind(preserveNull:false)
					// removes orphaned coach_client records, then paginate on the cleaned set.
					bson.D{{Key: "$lookup", Value: bson.D{
						{Key: "from", Value: r.userColl.Name()},
						{Key: "let", Value: bson.D{{Key: "client_id", Value: "$client_id"}}},
						{Key: "pipeline", Value: bson.A{
							bson.D{{Key: "$match", Value: bson.D{{Key: "$expr", Value: bson.D{{Key: "$eq", Value: bson.A{"$externalid", "$$client_id"}}}}}}},
							bson.D{{Key: "$project", Value: bson.D{
								{Key: "externalid", Value: 1},
								{Key: "firstname", Value: 1},
								{Key: "lastname", Value: 1},
								{Key: "profile_picture", Value: 1},
								{Key: "email", Value: 1},
								{Key: "phone_number", Value: 1},
								{Key: "address", Value: 1},
								{Key: "status", Value: 1},
								{Key: "last_login", Value: 1},
							}}},
						}},
						{Key: "as", Value: "user"},
					}}},
					bson.D{{Key: "$unwind", Value: bson.D{{Key: "path", Value: "$user"}, {Key: "preserveNullAndEmptyArrays", Value: false}}}},
					// Paginate after join so we skip/limit on actual joined records.
					bson.D{{Key: "$skip", Value: int64((page - 1) * size)}},
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
							{Key: "last_login", Value: "$user.last_login"},
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
				// Guarantee last_login key is always present so JSON always includes it.
				if _, exists := doc["last_login"]; !exists {
					doc["last_login"] = nil
				}
				log.Printf("DEBUG client last_login: externalid=%v last_login=%v", doc["externalid"], doc["last_login"])
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

// IsClientOfCoach checks if a client is already linked to a specific coach.
func (r *CoachRepository) IsClientOfCoach(ctx context.Context, coachID, clientID string) (bool, error) {
	filter := bson.M{"coach_id": coachID, "client_id": clientID}
	count, err := r.collection.CountDocuments(ctx, filter)
	if err != nil {
		return false, err
	}
	return count > 0, nil
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

// GetClientIDsByCoach returns all client external IDs belonging to a coach.
func (r *CoachRepository) GetClientIDsByCoach(ctx context.Context, coachID string) ([]string, error) {
	filter := bson.M{"coach_id": coachID}
	opts := options.Find().SetProjection(bson.M{"client_id": 1})
	cursor, err := r.collection.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var ids []string
	for cursor.Next(ctx) {
		var doc struct {
			ClientID string `bson:"client_id"`
		}
		if err := cursor.Decode(&doc); err != nil {
			return nil, err
		}
		ids = append(ids, doc.ClientID)
	}
	return ids, cursor.Err()
}
