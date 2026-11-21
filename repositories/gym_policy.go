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

const (
	gymRolePoliciesCollection  = "gym_role_policies"
	permissionGrantsCollection = "permission_grants"
)

// ErrPolicyVersionConflict is returned when a role policy write used a stale version.
var ErrPolicyVersionConflict = errors.New("gym role policy version conflict")

// GymPolicyRepository persists role policies, per-member grants and the per-gym authz_version counter.
type GymPolicyRepository struct {
	policies *mongo.Collection
	grants   *mongo.Collection
	gyms     *mongo.Collection
}

func NewGymPolicyRepository(database *mongo.Database) *GymPolicyRepository {
	return &GymPolicyRepository{
		policies: database.Collection(gymRolePoliciesCollection),
		grants:   database.Collection(permissionGrantsCollection),
		gyms:     database.Collection("gyms"),
	}
}

// gymPolicyIndexes declares the gym_role_policies indexes; indexes.go creates them.
func gymPolicyIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{{
		Keys:    bson.D{{Key: "gym_id", Value: 1}, {Key: "role", Value: 1}},
		Options: options.Index().SetUnique(true).SetName("idx_gym_role_policies_gym_role_unique"),
	}}
}

// permissionGrantIndexes declares the permission_grants indexes; indexes.go creates them.
func permissionGrantIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "gym_id", Value: 1}, {Key: "user_id", Value: 1}, {Key: "capability", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("idx_permission_grants_gym_user_cap_unique"),
		},
		{
			Keys:    bson.D{{Key: "gym_id", Value: 1}, {Key: "user_id", Value: 1}},
			Options: options.Index().SetName("idx_permission_grants_gym_user"),
		},
	}
}

// GetRolePolicy returns the stored policy of a role, or nil, nil when the owner never set one.
func (r *GymPolicyRepository) GetRolePolicy(ctx context.Context, gymID string, role db.GymMemberRole) (*db.GymRolePolicy, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	var p db.GymRolePolicy
	if err := r.policies.FindOne(ctx, bson.M{"gym_id": gymID, "role": role}).Decode(&p); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

// UpsertRolePolicy replaces the capability map when expectedVersion matches the stored version (0 = no policy yet)
// and returns the stored policy. A stale version returns ErrPolicyVersionConflict.
func (r *GymPolicyRepository) UpsertRolePolicy(ctx context.Context, p *db.GymRolePolicy, expectedVersion int64) (*db.GymRolePolicy, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	now := time.Now()
	if expectedVersion == 0 {
		doc := *p
		doc.Version, doc.UpdatedAt = 1, now
		if _, err := r.policies.InsertOne(ctx, &doc); err != nil {
			if mongo.IsDuplicateKeyError(err) {
				return nil, ErrPolicyVersionConflict
			}
			return nil, err
		}
		return &doc, nil
	}
	var out db.GymRolePolicy
	err := r.policies.FindOneAndUpdate(ctx,
		bson.M{"gym_id": p.GymID, "role": p.Role, "version": expectedVersion},
		bson.M{"$set": bson.M{"capabilities": p.Capabilities, "updated_by": p.UpdatedBy, "updated_at": now}, "$inc": bson.M{"version": 1}},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&out)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrPolicyVersionConflict
		}
		return nil, err
	}
	return &out, nil
}

// ListGrants returns the overrides of one member in one gym.
func (r *GymPolicyRepository) ListGrants(ctx context.Context, gymID, userID string) ([]db.PermissionGrant, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	cur, err := r.grants.Find(ctx, bson.M{"gym_id": gymID, "user_id": userID})
	if err != nil {
		return nil, err
	}
	out := []db.PermissionGrant{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ReplaceGrants makes grants the complete override set of one member in one unordered bulk write: each grant is
// upserted by (gym, user, capability) and the capabilities outside the new set are deleted. There is no window in
// which the member has no overrides; a failure leaves a mix of old and new rows and the caller must invalidate.
func (r *GymPolicyRepository) ReplaceGrants(ctx context.Context, gymID, userID string, grants []db.PermissionGrant) error {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	keep := make([]string, 0, len(grants))
	models := make([]mongo.WriteModel, 0, len(grants)+1)
	for _, g := range grants {
		keep = append(keep, g.Capability)
		models = append(models, mongo.NewUpdateOneModel().
			SetFilter(bson.M{"gym_id": gymID, "user_id": userID, "capability": g.Capability}).
			SetUpdate(bson.M{
				"$set":         bson.M{"effect": g.Effect, "granted_by": g.GrantedBy},
				"$setOnInsert": bson.M{"created_at": g.CreatedAt},
			}).
			SetUpsert(true))
	}
	models = append(models, mongo.NewDeleteManyModel().
		SetFilter(bson.M{"gym_id": gymID, "user_id": userID, "capability": bson.M{"$nin": keep}}))
	_, err := r.grants.BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false))
	return err
}

// GetAuthzVersion returns gyms.authz_version (0 when unset or the gym is unknown).
func (r *GymPolicyRepository) GetAuthzVersion(ctx context.Context, gymID string) (int64, error) {
	oid, ok := gymOID(gymID)
	if !ok {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	var g struct {
		Version int64 `bson:"authz_version"`
	}
	err := r.gyms.FindOne(ctx, bson.M{"_id": oid}, options.FindOne().SetProjection(bson.M{"authz_version": 1})).Decode(&g)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return 0, nil
		}
		return 0, err
	}
	return g.Version, nil
}

// PlanManageLocked reports gyms.plan_manage_locked (false when unset or the gym is unknown).
func (r *GymPolicyRepository) PlanManageLocked(ctx context.Context, gymID string) (bool, error) {
	oid, ok := gymOID(gymID)
	if !ok {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	var g struct {
		Locked bool `bson:"plan_manage_locked"`
	}
	err := r.gyms.FindOne(ctx, bson.M{"_id": oid}, options.FindOne().SetProjection(bson.M{"plan_manage_locked": 1})).Decode(&g)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return false, nil
		}
		return false, err
	}
	return g.Locked, nil
}

// BumpAuthzVersion increments gyms.authz_version and returns the new value.
func (r *GymPolicyRepository) BumpAuthzVersion(ctx context.Context, gymID string) (int64, error) {
	oid, ok := gymOID(gymID)
	if !ok {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	var g struct {
		Version int64 `bson:"authz_version"`
	}
	err := r.gyms.FindOneAndUpdate(ctx, bson.M{"_id": oid}, bson.M{"$inc": bson.M{"authz_version": 1}},
		options.FindOneAndUpdate().SetReturnDocument(options.After).SetProjection(bson.M{"authz_version": 1}),
	).Decode(&g)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return 0, nil
		}
		return 0, err
	}
	return g.Version, nil
}
