package repositories

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"coachify-account-api/models/db"
)

const inviteLinksCollection = "invite_links"

// ErrInviteLinkExists is returned when a link with the same token hash already exists.
var ErrInviteLinkExists = errors.New("invite link already exists")

// InviteLinkFilter narrows List; empty fields do not filter.
type InviteLinkFilter struct {
	CreatorID string
	GymID     string
	Status    string
}

// InviteLinkRepository persists the unified invite links (collection invite_links). Tokens are stored as hashes only.
type InviteLinkRepository struct {
	links *mongo.Collection
}

func NewInviteLinkRepository(database *mongo.Database) *InviteLinkRepository {
	return &InviteLinkRepository{links: database.Collection(inviteLinksCollection)}
}

func inviteLinkIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "token_hash", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("idx_invite_links_token_hash_unique"),
		},
		{
			Keys:    bson.D{{Key: "creator_id", Value: 1}, {Key: "status", Value: 1}, {Key: "created_at", Value: -1}},
			Options: options.Index().SetName("idx_invite_links_creator_status_created"),
		},
		{
			Keys:    bson.D{{Key: "gym_id", Value: 1}, {Key: "status", Value: 1}, {Key: "created_at", Value: -1}},
			Options: options.Index().SetName("idx_invite_links_gym_status_created"),
		},
		{
			Keys:    bson.D{{Key: "email", Value: 1}, {Key: "status", Value: 1}},
			Options: options.Index().SetName("idx_invite_links_email_status"),
		},
	}
}

func inviteOID(id string) (primitive.ObjectID, bool) {
	oid, err := primitive.ObjectIDFromHex(id)
	return oid, err == nil
}

// notExpired matches links without an expiry or with one in the future.
func notExpired(now time.Time) bson.M {
	return bson.M{"$or": bson.A{
		bson.M{"expires_at": bson.M{"$exists": false}},
		bson.M{"expires_at": bson.M{"$gt": now}},
	}}
}

// Insert stores a new link. ErrInviteLinkExists on a token hash collision.
func (r *InviteLinkRepository) Insert(ctx context.Context, l *db.InviteLink) error {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	res, err := r.links.InsertOne(ctx, l)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return ErrInviteLinkExists
		}
		return err
	}
	if oid, ok := res.InsertedID.(primitive.ObjectID); ok {
		l.ID = oid
	}
	return nil
}

func (r *InviteLinkRepository) findOne(ctx context.Context, filter bson.M) (*db.InviteLink, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	var l db.InviteLink
	if err := r.links.FindOne(ctx, filter).Decode(&l); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	return &l, nil
}

// GetByHash returns the link with the given token hash, or nil.
func (r *InviteLinkRepository) GetByHash(ctx context.Context, hash string) (*db.InviteLink, error) {
	return r.findOne(ctx, bson.M{"token_hash": hash})
}

// GetByID returns the link with the given id (hex), or nil (also for a malformed id).
func (r *InviteLinkRepository) GetByID(ctx context.Context, id string) (*db.InviteLink, error) {
	oid, ok := inviteOID(id)
	if !ok {
		return nil, nil
	}
	return r.findOne(ctx, bson.M{"_id": oid})
}

// List returns the links matching f, newest first.
func (r *InviteLinkRepository) List(ctx context.Context, f InviteLinkFilter, limit int64) ([]db.InviteLink, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	filter := bson.M{}
	if f.CreatorID != "" {
		filter["creator_id"] = f.CreatorID
	}
	if f.GymID != "" {
		filter["gym_id"] = f.GymID
	}
	if f.Status != "" {
		filter["status"] = f.Status
	}
	cur, err := r.links.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(limit))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	out := []db.InviteLink{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListPendingByEmail returns the active, unexpired links of a kind bound to an email.
func (r *InviteLinkRepository) ListPendingByEmail(ctx context.Context, email, kind string, now time.Time) ([]db.InviteLink, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	filter := notExpired(now)
	filter["email"] = email
	filter["kind"] = kind
	filter["status"] = db.InviteStatusActive
	cur, err := r.links.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(100))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	out := []db.InviteLink{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// SetStatus moves a link from one status to another; false when it was not in the from status.
func (r *InviteLinkRepository) SetStatus(ctx context.Context, id, from, to string) (bool, error) {
	oid, ok := inviteOID(id)
	if !ok {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	res, err := r.links.UpdateOne(ctx, bson.M{"_id": oid, "status": from}, bson.M{"$set": bson.M{"status": to}})
	if err != nil {
		return false, err
	}
	return res.MatchedCount > 0, nil
}

// ConsumeUse atomically counts one use of an active, unexpired link with uses left, marking it exhausted on its last
// use. It returns false when the link can no longer be used.
func (r *InviteLinkRepository) ConsumeUse(ctx context.Context, id string, now time.Time) (bool, error) {
	oid, ok := inviteOID(id)
	if !ok {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	filter := bson.M{
		"_id": oid, "status": db.InviteStatusActive,
		"$and": bson.A{
			notExpired(now),
			bson.M{"$or": bson.A{
				bson.M{"max_uses": bson.M{"$lte": 0}},
				bson.M{"$expr": bson.M{"$lt": bson.A{"$uses", "$max_uses"}}},
			}},
		},
	}
	update := bson.A{bson.M{"$set": bson.M{
		"uses": bson.M{"$add": bson.A{"$uses", 1}},
		"status": bson.M{"$cond": bson.A{
			bson.M{"$and": bson.A{
				bson.M{"$gt": bson.A{"$max_uses", 0}},
				bson.M{"$gte": bson.A{bson.M{"$add": bson.A{"$uses", 1}}, "$max_uses"}},
			}},
			db.InviteStatusExhausted, "$status",
		}},
	}}}
	res, err := r.links.UpdateOne(ctx, filter, update)
	if err != nil {
		return false, err
	}
	return res.MatchedCount > 0, nil
}

// ReleaseUse gives back a use taken by ConsumeUse (the redemption failed or turned out to be a no-op).
func (r *InviteLinkRepository) ReleaseUse(ctx context.Context, id string) error {
	oid, ok := inviteOID(id)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	update := bson.A{bson.M{"$set": bson.M{
		"uses": bson.M{"$max": bson.A{0, bson.M{"$subtract": bson.A{"$uses", 1}}}},
		"status": bson.M{"$cond": bson.A{
			bson.M{"$eq": bson.A{"$status", db.InviteStatusExhausted}}, db.InviteStatusActive, "$status",
		}},
	}}}
	_, err := r.links.UpdateOne(ctx, bson.M{"_id": oid}, update)
	return err
}

// UpsertByHash inserts l unless a link with the same token hash exists (migration). It reports whether it inserted.
func (r *InviteLinkRepository) UpsertByHash(ctx context.Context, l *db.InviteLink) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	res, err := r.links.UpdateOne(ctx, bson.M{"token_hash": l.TokenHash},
		bson.M{"$setOnInsert": bson.M{
			"kind": l.Kind, "token_hash": l.TokenHash, "creator_id": l.CreatorID, "creator_role": l.CreatorRole,
			"gym_id": l.GymID, "max_uses": l.MaxUses, "uses": l.Uses, "status": l.Status,
			"created_at": l.CreatedAt, "legacy_ref": l.LegacyRef,
		}},
		options.Update().SetUpsert(true))
	if err != nil {
		return false, err
	}
	return res.UpsertedCount > 0, nil
}

// SetSignupUser marks the account that signed up with a link, to accept the invite once it confirms its email.
func (r *InviteLinkRepository) SetSignupUser(ctx context.Context, id, userID string) error {
	oid, ok := inviteOID(id)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	_, err := r.links.UpdateOne(ctx, bson.M{"_id": oid}, bson.M{"$set": bson.M{"signup_user_id": userID}})
	return err
}

// RevokeActiveByEmailGym revokes the other active links of a kind bound to an email for a gym (a newer invite
// supersedes them), keeping exceptID. It returns how many were revoked.
func (r *InviteLinkRepository) RevokeActiveByEmailGym(ctx context.Context, email, kind, gymID, exceptID string) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	filter := bson.M{"email": email, "kind": kind, "gym_id": gymID, "status": db.InviteStatusActive}
	if oid, ok := inviteOID(exceptID); ok {
		filter["_id"] = bson.M{"$ne": oid}
	}
	res, err := r.links.UpdateMany(ctx, filter, bson.M{"$set": bson.M{"status": db.InviteStatusRevoked}})
	if err != nil {
		return 0, err
	}
	return res.ModifiedCount, nil
}
