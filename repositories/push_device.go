package repositories

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"coachify-account-api/models/db"
)

const pushDevicesCollection = "push_devices"

// PushDeviceRepository stores the Expo push tokens of signed-in apps.
type PushDeviceRepository struct {
	collection *mongo.Collection
}

// NewPushDeviceRepository does not create indexes: they live in InitializeIndexes (indexes.go).
func NewPushDeviceRepository(database *mongo.Database) *PushDeviceRepository {
	return &PushDeviceRepository{collection: database.Collection(pushDevicesCollection)}
}

// Upsert registers the token for the user. A token already registered by someone else moves to this user:
// it is the same device, now signed in to another account.
func (r *PushDeviceRepository) Upsert(ctx context.Context, d db.PushDevice) error {
	now := time.Now().UTC()
	set := bson.M{"user_id": d.UserID, "platform": d.Platform, "updated_at": now}
	update := bson.M{"$set": set, "$setOnInsert": bson.M{"created_at": now}}
	if d.Locale != "" {
		set["locale"] = d.Locale
	} else {
		update["$unset"] = bson.M{"locale": ""}
	}
	_, err := r.collection.UpdateOne(ctx, bson.M{"token": d.Token}, update, options.Update().SetUpsert(true))
	return err
}

// ListByUser returns the user's devices, most recently registered first.
func (r *PushDeviceRepository) ListByUser(ctx context.Context, userID string, limit int64) ([]db.PushDevice, error) {
	opts := options.Find().SetSort(bson.D{{Key: "updated_at", Value: -1}}).SetLimit(limit)
	cur, err := r.collection.Find(ctx, bson.M{"user_id": userID}, opts)
	if err != nil {
		return nil, err
	}
	out := []db.PushDevice{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteForUser removes the token only if it belongs to the user.
func (r *PushDeviceRepository) DeleteForUser(ctx context.Context, userID, token string) error {
	_, err := r.collection.DeleteOne(ctx, bson.M{"user_id": userID, "token": token})
	return err
}

// TrimUser keeps the user's keep most recently registered devices and deletes the others.
func (r *PushDeviceRepository) TrimUser(ctx context.Context, userID string, keep int64) error {
	opts := options.Find().SetSort(bson.D{{Key: "updated_at", Value: -1}}).SetSkip(keep).SetProjection(bson.M{"token": 1})
	cur, err := r.collection.Find(ctx, bson.M{"user_id": userID}, opts)
	if err != nil {
		return err
	}
	var stale []struct {
		Token string `bson:"token"`
	}
	if err := cur.All(ctx, &stale); err != nil {
		return err
	}
	if len(stale) == 0 {
		return nil
	}
	tokens := make([]string, 0, len(stale))
	for _, s := range stale {
		tokens = append(tokens, s.Token)
	}
	_, err = r.collection.DeleteMany(ctx, bson.M{"user_id": userID, "token": bson.M{"$in": tokens}})
	return err
}
