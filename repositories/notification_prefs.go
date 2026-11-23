package repositories

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"coachify-account-api/models/db"
)

const notificationPrefsCollection = "notification_prefs"

const notificationPrefsTimeout = 10 * time.Second

// NotificationPrefsRepository stores push preferences keyed by user external ID (_id), so it needs no index.
type NotificationPrefsRepository struct {
	collection *mongo.Collection
}

// NewNotificationPrefsRepository uses the notification_prefs collection of database.
func NewNotificationPrefsRepository(database *mongo.Database) *NotificationPrefsRepository {
	return &NotificationPrefsRepository{collection: database.Collection(notificationPrefsCollection)}
}

// Get returns the user's saved preferences, or nil when they never saved any.
func (r *NotificationPrefsRepository) Get(ctx context.Context, userID string) (*db.NotificationPrefs, error) {
	ctx, cancel := context.WithTimeout(ctx, notificationPrefsTimeout)
	defer cancel()
	var out db.NotificationPrefs
	err := r.collection.FindOne(ctx, bson.M{"_id": userID}).Decode(&out)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Patch sets the given fields in one atomic upsert; on the first save the other fields get their defaults.
func (r *NotificationPrefsRepository) Patch(ctx context.Context, userID string, p db.NotificationPrefsPatch) (db.NotificationPrefs, error) {
	ctx, cancel := context.WithTimeout(ctx, notificationPrefsTimeout)
	defer cancel()
	def := db.DefaultNotificationPrefs(userID)
	set := bson.M{"updated_at": time.Now().UTC()}
	onInsert := bson.M{}
	pick := func(field string, v interface{}, isSet bool, fallback interface{}) {
		if isSet {
			set[field] = v
		} else {
			onInsert[field] = fallback
		}
	}
	pick("messages", prefBool(p.Messages), p.Messages != nil, def.Messages)
	pick("reminders", prefBool(p.Reminders), p.Reminders != nil, def.Reminders)
	pick("reminder_time", prefString(p.ReminderTime), p.ReminderTime != nil, def.ReminderTime)
	pick("timezone", prefString(p.Timezone), p.Timezone != nil, def.Timezone)
	update := bson.M{"$set": set}
	if len(onInsert) > 0 {
		update["$setOnInsert"] = onInsert
	}
	opts := options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)
	var out db.NotificationPrefs
	err := r.collection.FindOneAndUpdate(ctx, bson.M{"_id": userID}, update, opts).Decode(&out)
	return out, err
}

// ListByIDs returns the saved preferences of the given users; users who never saved any are absent.
func (r *NotificationPrefsRepository) ListByIDs(ctx context.Context, userIDs []string) ([]db.NotificationPrefs, error) {
	ctx, cancel := context.WithTimeout(ctx, notificationPrefsTimeout)
	defer cancel()
	cur, err := r.collection.Find(ctx, bson.M{"_id": bson.M{"$in": userIDs}})
	if err != nil {
		return nil, err
	}
	out := []db.NotificationPrefs{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func prefBool(b *bool) bool {
	return b != nil && *b
}

func prefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
