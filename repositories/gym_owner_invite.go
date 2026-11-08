package repositories

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
)

// GymOwnerInviteRepository persists admin-issued gym owner invitations.
type GymOwnerInviteRepository struct {
	coll *mongo.Collection
}

func NewGymOwnerInviteRepository(database *mongo.Database, collName string) *GymOwnerInviteRepository {
	return &GymOwnerInviteRepository{coll: database.Collection(collName)}
}

// Insert stores an invite. models.ErrOwnerInvitePending when a pending one exists for the email.
func (r *GymOwnerInviteRepository) Insert(ctx context.Context, inv *db.GymOwnerInvite) error {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	res, err := r.coll.InsertOne(ctx, inv)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return models.ErrOwnerInvitePending
		}
		return err
	}
	if oid, ok := res.InsertedID.(primitive.ObjectID); ok {
		inv.ID = oid
	}
	return nil
}

// GetByTokenHash returns the invite for a token hash, or nil when none exists.
func (r *GymOwnerInviteRepository) GetByTokenHash(ctx context.Context, hash string) (*db.GymOwnerInvite, error) {
	return r.findOne(ctx, bson.M{"token_hash": hash})
}

// GetByID returns an invite, or nil when it does not exist (or the id is malformed).
func (r *GymOwnerInviteRepository) GetByID(ctx context.Context, id string) (*db.GymOwnerInvite, error) {
	oid, ok := gymOID(id)
	if !ok {
		return nil, nil
	}
	return r.findOne(ctx, bson.M{"_id": oid})
}

func (r *GymOwnerInviteRepository) findOne(ctx context.Context, filter bson.M) (*db.GymOwnerInvite, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	var inv db.GymOwnerInvite
	if err := r.coll.FindOne(ctx, filter).Decode(&inv); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	return &inv, nil
}

// List returns invites newest first, optionally filtered by status.
func (r *GymOwnerInviteRepository) List(ctx context.Context, status string, page, size int) ([]db.GymOwnerInvite, int, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	page, size = pageDefaults(page, size)

	filter := bson.M{}
	if status != "" {
		filter["status"] = status
	}
	total, err := r.coll.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	cursor, err := r.coll.Find(ctx, filter, options.Find().
		SetSort(bson.D{{Key: "created_at", Value: -1}}).
		SetSkip(int64((page-1)*size)).
		SetLimit(int64(size)))
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)
	out := []db.GymOwnerInvite{}
	if err := cursor.All(ctx, &out); err != nil {
		return nil, 0, err
	}
	return out, int(total), nil
}

// RevokePendingByEmail revokes the pending invite of an email, if any.
func (r *GymOwnerInviteRepository) RevokePendingByEmail(ctx context.Context, email string, at time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	_, err := r.coll.UpdateMany(ctx,
		bson.M{"email": email, "status": db.GymOwnerInvitePending},
		bson.M{"$set": bson.M{"status": db.GymOwnerInviteRevoked, "revoked_at": at}})
	return err
}

// Transition moves an invite from one status to another atomically; false when it was not in `from`.
func (r *GymOwnerInviteRepository) Transition(ctx context.Context, id, from, to, userID string, at time.Time) (bool, error) {
	oid, ok := gymOID(id)
	if !ok {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	set := bson.M{"status": to}
	switch to {
	case db.GymOwnerInviteAccepted:
		set["accepted_at"] = at
		set["accepted_user_id"] = userID
	case db.GymOwnerInviteRevoked:
		set["revoked_at"] = at
	}
	res, err := r.coll.UpdateOne(ctx, bson.M{"_id": oid, "status": from}, bson.M{"$set": set})
	if err != nil {
		return false, err
	}
	return res.MatchedCount > 0, nil
}

// Rotate replaces a pending invite's token hash and expiry; false when the invite is not pending.
func (r *GymOwnerInviteRepository) Rotate(ctx context.Context, id, newHash string, newExpiry, at time.Time) (bool, error) {
	oid, ok := gymOID(id)
	if !ok {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	res, err := r.coll.UpdateOne(ctx,
		bson.M{"_id": oid, "status": db.GymOwnerInvitePending},
		bson.M{"$set": bson.M{"token_hash": newHash, "expires_at": newExpiry, "sent_at": at}})
	if err != nil {
		return false, err
	}
	return res.MatchedCount > 0, nil
}
