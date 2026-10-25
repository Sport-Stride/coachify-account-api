package repositories

import (
	"context"
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

	// Single aggregation: one indexed join (localField/foreignField uses idx_users_externalid) before the facet.
	// $unwind(preserveNull:false) drops orphaned coach_client rows, so total counts only entries with a matching user
	// and pagination runs on the cleaned set, same as before. The join used to run twice (once per facet branch).
	pipeline := mongo.Pipeline{
		bson.D{{Key: "$match", Value: filter}},
		bson.D{{Key: "$lookup", Value: bson.D{
			{Key: "from", Value: r.userColl.Name()},
			{Key: "localField", Value: "client_id"},
			{Key: "foreignField", Value: "externalid"},
			{Key: "pipeline", Value: bson.A{
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
		bson.D{
			{Key: "$facet", Value: bson.D{
				{Key: "total", Value: bson.A{
					bson.D{{Key: "$count", Value: "count"}},
				}},
				{Key: "data", Value: bson.A{
					bson.D{{Key: "$sort", Value: bson.D{{Key: "created_at", Value: -1}}}},
					bson.D{{Key: "$skip", Value: int64((page - 1) * size)}},
					bson.D{{Key: "$limit", Value: int64(size)}},
					bson.D{
						{Key: "$project", Value: bson.D{
							{Key: "_id", Value: 0},
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

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Execute aggregation
	cursor, err := r.collection.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	// Parse result
	var facetResult []bson.M
	if err := cursor.All(ctx, &facetResult); err != nil {
		return nil, 0, err
	}

	if len(facetResult) == 0 {
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
			var doc map[string]interface{}
			switch v := item.(type) {
			case bson.M:
				doc = map[string]interface{}(v)
			case bson.D:
				doc = make(map[string]interface{}, len(v))
				for _, e := range v {
					doc[e.Key] = e.Value
				}
			default:
				continue
			}
			// Guarantee last_login key is always present so JSON always includes it.
			if _, exists := doc["last_login"]; !exists {
				doc["last_login"] = nil
			}
			results = append(results, doc)
		}
	}

	if results == nil {
		results = []map[string]interface{}{}
	}

	return results, int(total), nil
}

// ListCoachClientsSummary returns a page of lightweight client rows plus the coach's total link count.
// One aggregation: indexed match + sort + page on coach_clients first, then join users via the
// unique externalid index and project only the summary fields.
func (r *CoachRepository) ListCoachClientsSummary(ctx context.Context, coachID string, page, size int) ([]db.CoachClientSummary, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 200
	}
	filter := bson.M{"coach_id": coachID}
	pipeline := mongo.Pipeline{
		bson.D{{Key: "$match", Value: filter}},
		bson.D{{Key: "$sort", Value: bson.D{{Key: "created_at", Value: -1}}}},
		bson.D{{Key: "$skip", Value: int64((page - 1) * size)}},
		bson.D{{Key: "$limit", Value: int64(size)}},
		bson.D{{Key: "$lookup", Value: bson.D{
			{Key: "from", Value: r.userColl.Name()},
			{Key: "localField", Value: "client_id"},
			{Key: "foreignField", Value: "externalid"},
			{Key: "as", Value: "user"},
		}}},
		bson.D{{Key: "$unwind", Value: "$user"}},
		bson.D{{Key: "$project", Value: bson.D{
			{Key: "_id", Value: 0},
			{Key: "externalid", Value: "$user.externalid"},
			{Key: "firstname", Value: "$user.firstname"},
			{Key: "lastname", Value: "$user.lastname"},
			{Key: "profile_picture", Value: "$user.profile_picture"},
		}}},
	}
	cursor, err := r.collection.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)
	out := []db.CoachClientSummary{}
	if err := cursor.All(ctx, &out); err != nil {
		return nil, 0, err
	}
	total, err := r.collection.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	return out, int(total), nil
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

// LinkedClientIDs returns the subset of clientIDs linked to coachID (one indexed Find, client_id only).
func (r *CoachRepository) LinkedClientIDs(ctx context.Context, coachID string, clientIDs []string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	filter := bson.M{"coach_id": coachID, "client_id": bson.M{"$in": clientIDs}}
	opts := options.Find().SetProjection(bson.M{"client_id": 1, "_id": 0})
	cursor, err := r.collection.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var rows []struct {
		ClientID string `bson:"client_id"`
	}
	if err := cursor.All(ctx, &rows); err != nil {
		return nil, err
	}
	linked := make([]string, 0, len(rows))
	for _, row := range rows {
		linked = append(linked, row.ClientID)
	}
	return linked, nil
}
