package repositories

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"coachify-account-api/models/db"
)

// AdminAuditRepository persists the admin action audit trail.
type AdminAuditRepository struct {
	collection *mongo.Collection
}

func NewAdminAuditRepository(database *mongo.Database, collName string) *AdminAuditRepository {
	return &AdminAuditRepository{collection: database.Collection(collName)}
}

// Insert stores an entry; CreatedAt defaults to now.
func (r *AdminAuditRepository) Insert(ctx context.Context, e *db.AdminAuditEntry) error {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	_, err := r.collection.InsertOne(ctx, e)
	return err
}

// List returns entries newest first with the total number of matches.
func (r *AdminAuditRepository) List(ctx context.Context, f db.AdminAuditFilter, page, size int) ([]db.AdminAuditEntry, int, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()

	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	filter := bson.M{}
	if f.ActorID != "" {
		filter["actor_id"] = f.ActorID
	}
	if f.TargetID != "" {
		filter["target_id"] = f.TargetID
	}
	if f.Action != "" {
		filter["action"] = f.Action
	}
	rng := bson.M{}
	if !f.From.IsZero() {
		rng["$gte"] = f.From
	}
	if !f.To.IsZero() {
		rng["$lt"] = f.To
	}
	if len(rng) > 0 {
		filter["created_at"] = rng
	}

	total, err := r.collection.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	cursor, err := r.collection.Find(ctx, filter, options.Find().
		SetSort(bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}}).
		SetSkip(int64((page-1)*size)).
		SetLimit(int64(size)))
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	entries := []db.AdminAuditEntry{}
	if err := cursor.All(ctx, &entries); err != nil {
		return nil, 0, err
	}
	return entries, int(total), nil
}
