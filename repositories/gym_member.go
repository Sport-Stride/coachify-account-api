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
	gymMembersCollection = "gym_members"
	// memberScanBatch is the cursor batch size of the full-collection reads used by the migration.
	memberScanBatch = 1000
)

// GymMemberRepository persists gym memberships (owners, head coaches, coaches).
type GymMemberRepository struct {
	members *mongo.Collection
}

func NewGymMemberRepository(database *mongo.Database) *GymMemberRepository {
	return &GymMemberRepository{members: database.Collection(gymMembersCollection)}
}

// coachSlot reports whether a role is bound to a single gym per user. Rows of those roles carry coach_slot=true,
// which the partial unique index on user_id filters on (plain equality, no $in).
func coachSlot(role db.GymMemberRole) bool {
	return role == db.GymMemberRoleHeadCoach || role == db.GymMemberRoleCoach
}

// roleFields returns the update parts that follow the role of a row: role, supervisor_id and coach_slot.
func roleFields(m *db.GymMember) (set, unset bson.M) {
	set, unset = bson.M{"role": m.Role}, bson.M{}
	if m.SupervisorID != "" {
		set["supervisor_id"] = m.SupervisorID
	} else {
		unset["supervisor_id"] = ""
	}
	if coachSlot(m.Role) {
		set["coach_slot"] = true
	} else {
		unset["coach_slot"] = ""
	}
	return set, unset
}

// upsertUpdate builds the UpsertActive update. Role, supervisor_id and coach_slot are refreshed on an existing
// active row; added_by, source, joined_at and seat_assigned are written only when the row is created.
func upsertUpdate(m *db.GymMember, joined time.Time) bson.M {
	set, unset := roleFields(m)
	u := bson.M{
		"$set":         set,
		"$setOnInsert": bson.M{"added_by": m.AddedBy, "source": m.Source, "joined_at": joined, "seat_assigned": false},
	}
	if len(unset) > 0 {
		u["$unset"] = unset
	}
	return u
}

// ensureUpdate builds the EnsureActive update: every field is written only on insert.
func ensureUpdate(m *db.GymMember) bson.M {
	set, _ := roleFields(m)
	set["joined_at"], set["added_by"], set["source"], set["seat_assigned"] = m.JoinedAt, m.AddedBy, m.Source, false
	return bson.M{"$setOnInsert": set}
}

// leaveUpdate marks a row left and frees its coach slot.
func leaveUpdate(at time.Time) bson.M {
	return bson.M{
		"$set":   bson.M{"status": db.GymMemberStatusLeft, "left_at": at},
		"$unset": bson.M{"coach_slot": ""},
	}
}

// gymMemberIndexes declares the gym_members indexes; indexes.go creates them and VerifyIndexes checks their names.
func gymMemberIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{
		{
			Keys: bson.D{{Key: "gym_id", Value: 1}, {Key: "user_id", Value: 1}},
			Options: options.Index().
				SetUnique(true).
				SetPartialFilterExpression(bson.M{"status": "active"}).
				SetName("idx_gym_members_gym_user_unique"),
		},
		// One active gym per head coach or coach: owners have no coach_slot and are not constrained.
		{
			Keys: bson.D{{Key: "user_id", Value: 1}},
			Options: options.Index().
				SetUnique(true).
				SetPartialFilterExpression(bson.M{"status": "active", "coach_slot": true}).
				SetName("idx_gym_members_coach_unique"),
		},
		{
			Keys:    bson.D{{Key: "gym_id", Value: 1}, {Key: "role", Value: 1}, {Key: "status", Value: 1}},
			Options: options.Index().SetName("idx_gym_members_gym_role_status"),
		},
		{
			Keys:    bson.D{{Key: "supervisor_id", Value: 1}},
			Options: options.Index().SetName("idx_gym_members_supervisor"),
		},
	}
}

// UpsertActive makes (gym, user) an active membership with the given role and supervisor.
// On an existing active row only role, supervisor_id and coach_slot are refreshed; joined_at, seat_assigned,
// added_by and source are set when the row is created. A head coach or coach active in another gym is marked
// left there first (one gym per coach). A duplicate-key race with a concurrent upsert is retried once.
// Failure window: the close and the upsert are two writes. If the upsert fails after the close, the user has no
// active row until the next reconcile run (the backfill) recreates it from the legacy collections.
func (r *GymMemberRepository) UpsertActive(ctx context.Context, m *db.GymMember) error {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()

	joined := m.JoinedAt
	if joined.IsZero() {
		joined = time.Now()
	}
	if coachSlot(m.Role) {
		_, err := r.members.UpdateMany(ctx,
			bson.M{"user_id": m.UserID, "status": db.GymMemberStatusActive, "gym_id": bson.M{"$ne": m.GymID}, "coach_slot": true},
			leaveUpdate(joined))
		if err != nil {
			return err
		}
	}

	filter := bson.M{"gym_id": m.GymID, "user_id": m.UserID, "status": db.GymMemberStatusActive}
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		_, err = r.members.UpdateOne(ctx, filter, upsertUpdate(m, joined), options.Update().SetUpsert(true))
		if err == nil || !mongo.IsDuplicateKeyError(err) {
			return err
		}
	}
	return err
}

// MarkLeft closes the active membership of a user in a gym; false when there was none.
func (r *GymMemberRepository) MarkLeft(ctx context.Context, gymID, userID string, at time.Time) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	res, err := r.members.UpdateOne(ctx,
		bson.M{"gym_id": gymID, "user_id": userID, "status": db.GymMemberStatusActive}, leaveUpdate(at))
	if err != nil {
		return false, err
	}
	return res.ModifiedCount > 0, nil
}

// MarkLeftCoach closes the active coach membership of a user in any gym; false when there was none.
func (r *GymMemberRepository) MarkLeftCoach(ctx context.Context, userID string, at time.Time) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	res, err := r.members.UpdateMany(ctx,
		bson.M{"user_id": userID, "role": db.GymMemberRoleCoach, "status": db.GymMemberStatusActive}, leaveUpdate(at))
	if err != nil {
		return false, err
	}
	return res.ModifiedCount > 0, nil
}

// MarkLeftBySupervisor closes every active coach row a head coach supervises in a gym; returns the count.
func (r *GymMemberRepository) MarkLeftBySupervisor(ctx context.Context, gymID, supervisorID string, at time.Time) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	res, err := r.members.UpdateMany(ctx,
		bson.M{"gym_id": gymID, "supervisor_id": supervisorID, "role": db.GymMemberRoleCoach, "status": db.GymMemberStatusActive},
		leaveUpdate(at))
	if err != nil {
		return 0, err
	}
	return res.ModifiedCount, nil
}

// ListByUser returns the active memberships of a user, oldest first.
func (r *GymMemberRepository) ListByUser(ctx context.Context, userID string) ([]db.GymMember, error) {
	return r.find(ctx, bson.M{"user_id": userID, "status": db.GymMemberStatusActive})
}

// ListByGym returns the active members of a gym, oldest first.
func (r *GymMemberRepository) ListByGym(ctx context.Context, gymID string) ([]db.GymMember, error) {
	return r.find(ctx, bson.M{"gym_id": gymID, "status": db.GymMemberStatusActive})
}

// Get returns the active membership of a user in a gym, or nil.
func (r *GymMemberRepository) Get(ctx context.Context, userID, gymID string) (*db.GymMember, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	var m db.GymMember
	err := r.members.FindOne(ctx, bson.M{"gym_id": gymID, "user_id": userID, "status": db.GymMemberStatusActive}).Decode(&m)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
}

func (r *GymMemberRepository) find(ctx context.Context, filter bson.M) ([]db.GymMember, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	cursor, err := r.members.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "joined_at", Value: 1}, {Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	out := []db.GymMember{}
	if err := cursor.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// EnsureActive inserts the membership only when no active row exists for (gym, user); it never changes an existing row.
// It returns true when a row was created. Used by the backfill, which must not overwrite live dual-written rows.
func (r *GymMemberRepository) EnsureActive(ctx context.Context, m *db.GymMember) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	res, err := r.members.UpdateOne(ctx,
		bson.M{"gym_id": m.GymID, "user_id": m.UserID, "status": db.GymMemberStatusActive},
		ensureUpdate(m), options.Update().SetUpsert(true))
	if err != nil {
		return false, err
	}
	return res.UpsertedCount > 0, nil
}

// FixRole sets role, supervisor_id and coach_slot of the active (gym, user) row to those of m (reconcile); false when nothing changed.
func (r *GymMemberRepository) FixRole(ctx context.Context, m *db.GymMember) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	set, unset := roleFields(m)
	update := bson.M{"$set": set}
	if len(unset) > 0 {
		update["$unset"] = unset
	}
	res, err := r.members.UpdateOne(ctx,
		bson.M{"gym_id": m.GymID, "user_id": m.UserID, "status": db.GymMemberStatusActive}, update)
	if err != nil {
		return false, err
	}
	return res.ModifiedCount > 0, nil
}

// ListActive returns every active membership by streaming the cursor (reconcile input).
// It applies no timeout of its own: the caller passes a context whose deadline fits a full scan.
func (r *GymMemberRepository) ListActive(ctx context.Context) ([]db.GymMember, error) {
	return readAll[db.GymMember](ctx, r.members, bson.M{"status": db.GymMemberStatusActive})
}

// LegacySnapshot streams every gym, gym head coach and team member row (backfill input).
// It applies no timeout of its own: the caller passes a context whose deadline fits a full scan.
func (r *GymMemberRepository) LegacySnapshot(ctx context.Context) ([]db.Gym, []db.GymHeadCoach, []db.TeamMember, error) {
	database := r.members.Database()
	gyms, err := readAll[db.Gym](ctx, database.Collection(gymsCollection), bson.M{})
	if err != nil {
		return nil, nil, nil, err
	}
	heads, err := readAll[db.GymHeadCoach](ctx, database.Collection(gymHeadCoachesCollection), bson.M{})
	if err != nil {
		return nil, nil, nil, err
	}
	team, err := readAll[db.TeamMember](ctx, database.Collection(teamMembersCollection), bson.M{})
	if err != nil {
		return nil, nil, nil, err
	}
	return gyms, heads, team, nil
}

// readAll decodes a collection one document at a time in server batches instead of cursor.All.
func readAll[T any](ctx context.Context, coll *mongo.Collection, filter bson.M) ([]T, error) {
	cursor, err := coll.Find(ctx, filter, options.Find().SetBatchSize(memberScanBatch))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	out := []T{}
	for cursor.Next(ctx) {
		var v T
		if err := cursor.Decode(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, cursor.Err()
}

// MarkLeftHead closes every active head coach row of a user, in any gym; false when there was none.
func (r *GymMemberRepository) MarkLeftHead(ctx context.Context, userID string, at time.Time) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	res, err := r.members.UpdateMany(ctx,
		bson.M{"user_id": userID, "role": db.GymMemberRoleHeadCoach, "status": db.GymMemberStatusActive}, leaveUpdate(at))
	if err != nil {
		return false, err
	}
	return res.ModifiedCount > 0, nil
}

// MarkLeftSupervised closes every active coach row a head coach supervises, in any gym; returns the count.
func (r *GymMemberRepository) MarkLeftSupervised(ctx context.Context, supervisorID string, at time.Time) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	res, err := r.members.UpdateMany(ctx,
		bson.M{"supervisor_id": supervisorID, "role": db.GymMemberRoleCoach, "status": db.GymMemberStatusActive}, leaveUpdate(at))
	if err != nil {
		return 0, err
	}
	return res.ModifiedCount, nil
}
