package repositories

import (
	"context"
	"errors"
	"strings"
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

// ErrAlreadyMember is returned by AddActive when the user is already an active member of the gym,
// or is a head coach or coach of any gym (one gym per coach).
var ErrAlreadyMember = errors.New("user already belongs to a gym")

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
		{
			Keys:    bson.D{{Key: "gym_id", Value: 1}, {Key: "seat_assigned", Value: 1}, {Key: "status", Value: 1}},
			Options: options.Index().SetName("idx_gym_members_gym_seat_status"),
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
		// A concurrent writer upserted the same (gym, user) between our match and insert: the active row exists, so
		// this is the "exists" outcome. Other duplicate keys (another gym for the coach) stay real errors.
		if mongo.IsDuplicateKeyError(err) && strings.Contains(err.Error(), "idx_gym_members_gym_user_unique") {
			if cur, getErr := r.Get(ctx, m.UserID, m.GymID); getErr == nil && cur != nil {
				return false, ErrAlreadyMember
			}
		}
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

// AddActive inserts a new active membership and never touches another row. A duplicate on the (gym, user) index or on
// the one-gym-per-coach index is ErrAlreadyMember.
func (r *GymMemberRepository) AddActive(ctx context.Context, m *db.GymMember) error {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	row := *m
	row.Status, row.SeatAssigned = db.GymMemberStatusActive, false
	row.CoachSlot = coachSlot(row.Role)
	if row.JoinedAt.IsZero() {
		row.JoinedAt = time.Now()
	}
	if _, err := r.members.InsertOne(ctx, &row); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return ErrAlreadyMember
		}
		return err
	}
	return nil
}

// SetSupervisor is a compare-and-set of the supervisor of an active coach row: it applies only while supervisor_id
// still equals prevSupervisorID (empty means none). An empty supervisorID clears it. The row becomes source "flat".
// False means no row matched (it changed or left meanwhile).
func (r *GymMemberRepository) SetSupervisor(ctx context.Context, gymID, userID, prevSupervisorID, supervisorID string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	var prev interface{} = prevSupervisorID
	if prevSupervisorID == "" {
		prev = nil // matches a missing or null supervisor_id
	}
	set := bson.M{"source": db.GymMemberSourceFlat}
	update := bson.M{"$set": set}
	if supervisorID == "" {
		update["$unset"] = bson.M{"supervisor_id": ""}
	} else {
		set["supervisor_id"] = supervisorID
	}
	res, err := r.members.UpdateOne(ctx,
		bson.M{"gym_id": gymID, "user_id": userID, "role": db.GymMemberRoleCoach, "status": db.GymMemberStatusActive, "supervisor_id": prev},
		update)
	if err != nil {
		return false, err
	}
	return res.MatchedCount > 0, nil
}

// CountGymsWithoutOwnerRow counts active gyms with no active owner row in gym_members.
func (r *GymMemberRepository) CountGymsWithoutOwnerRow(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	cursor, err := r.members.Database().Collection(gymsCollection).Aggregate(ctx, bson.A{
		bd("$match", bd("status", db.GymStatusActive)),
		bd("$lookup", bd(
			"from", gymMembersCollection,
			"let", bd("gid", bd("$toString", "$_id")),
			"pipeline", bson.A{
				bd("$match", bd("$expr", bd("$and", bson.A{
					bd("$eq", bson.A{"$gym_id", "$$gid"}),
					bd("$eq", bson.A{"$role", string(db.GymMemberRoleOwner)}),
					bd("$eq", bson.A{"$status", string(db.GymMemberStatusActive)}),
				}))),
				bd("$limit", 1),
			},
			"as", "owner",
		)),
		bd("$match", bd("owner", bd("$size", 0))),
		bd("$count", "n"),
	})
	if err != nil {
		return 0, err
	}
	defer cursor.Close(ctx)
	var res []struct {
		N int `bson:"n"`
	}
	if err := cursor.All(ctx, &res); err != nil {
		return 0, err
	}
	if len(res) == 0 {
		return 0, nil
	}
	return res[0].N, nil
}

// MaxMemberPage caps the page of the member list so the computed $skip cannot overflow.
const MaxMemberPage = 10000

// usersCollectionName is the users collection the member profile lookup reads.
const usersCollectionName = "users"

// profileStages joins the safe profile fields of the member's user in one $lookup; a missing user yields empty strings.
func profileStages() []bson.D {
	return []bson.D{
		{{Key: "$lookup", Value: bd(
			"from", usersCollectionName,
			"let", bd("uid", "$user_id"),
			"pipeline", bson.A{
				bd("$match", bd("$expr", bd("$eq", bson.A{"$externalid", "$$uid"}))),
				bd("$project", bd("firstname", 1, "lastname", 1, "profile_picture", 1)),
			},
			"as", "u",
		)}},
		{{Key: "$addFields", Value: bd(
			"firstname", bd("$ifNull", bson.A{bd("$first", "$u.firstname"), ""}),
			"lastname", bd("$ifNull", bson.A{bd("$first", "$u.lastname"), ""}),
			"profile_picture", bd("$ifNull", bson.A{bd("$first", "$u.profile_picture"), ""}),
		)}},
		{{Key: "$project", Value: bd("u", 0)}},
	}
}

// ListByGymPage returns one page of the active members of a gym with their profile fields, and the total, in one
// aggregation. A non-empty scopeUserID limits the result to that user and the coaches that user supervises; a
// non-empty role keeps only that role (applied before the count, so total follows the filter).
func (r *GymMemberRepository) ListByGymPage(ctx context.Context, gymID, scopeUserID, role string, page, size int) ([]db.GymMemberRow, int64, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	filter := bson.M{"gym_id": gymID, "status": db.GymMemberStatusActive}
	if scopeUserID != "" {
		filter["$or"] = bson.A{bson.M{"user_id": scopeUserID}, bson.M{"supervisor_id": scopeUserID}}
	}
	if role != "" {
		filter["role"] = role
	}
	if page < 1 {
		page = 1
	}
	if page > MaxMemberPage {
		page = MaxMemberPage
	}
	rowStages := bson.A{
		bd("$sort", bd("joined_at", 1, "_id", 1)),
		bd("$skip", int64((page-1)*size)),
		bd("$limit", int64(size)),
	}
	for _, st := range profileStages() {
		rowStages = append(rowStages, st)
	}
	cursor, err := r.members.Aggregate(ctx, bson.A{
		bd("$match", filter),
		bd("$facet", bd("rows", rowStages, "total", bson.A{bd("$count", "n")})),
	})
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)
	var res []struct {
		Rows  []db.GymMemberRow `bson:"rows"`
		Total []struct {
			N int64 `bson:"n"`
		} `bson:"total"`
	}
	if err := cursor.All(ctx, &res); err != nil {
		return nil, 0, err
	}
	if len(res) == 0 {
		return []db.GymMemberRow{}, 0, nil
	}
	var total int64
	if len(res[0].Total) > 0 {
		total = res[0].Total[0].N
	}
	if res[0].Rows == nil {
		res[0].Rows = []db.GymMemberRow{}
	}
	return res[0].Rows, total, nil
}

// GetRow returns the active membership of a user in a gym with profile fields, or nil.
func (r *GymMemberRepository) GetRow(ctx context.Context, gymID, userID string) (*db.GymMemberRow, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	pipeline := append([]bson.D{
		{{Key: "$match", Value: bson.M{"gym_id": gymID, "user_id": userID, "status": db.GymMemberStatusActive}}},
		{{Key: "$limit", Value: 1}},
	}, profileStages()...)
	cursor, err := r.members.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var rows []db.GymMemberRow
	if err := cursor.All(ctx, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// SetSeat assigns or releases the seat of an active coach or head coach row. Assigning sets seat_state seated and
// seat_assigned_at; releasing sets seat_state none and clears the grace. False means no such active row.
func (r *GymMemberRepository) SetSeat(ctx context.Context, gymID, userID string, assigned bool, at time.Time) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	filter := bson.M{"gym_id": gymID, "user_id": userID, "status": db.GymMemberStatusActive, "coach_slot": true}
	set := bson.M{"seat_assigned": assigned, "seat_state_at": at}
	unset := bson.M{"seat_grace_until": ""}
	if assigned {
		set["seat_state"], set["seat_assigned_at"] = db.SeatStateSeated, at
	} else {
		set["seat_state"] = db.SeatStateNone
		unset["seat_assigned_at"] = ""
	}
	res, err := r.members.UpdateOne(ctx, filter, bson.M{"$set": set, "$unset": unset})
	if err != nil {
		return false, err
	}
	return res.MatchedCount > 0, nil
}

// SetSeatState stores the reconciled seat state of an active row; graceUntil nil clears it. Idempotent. A grace
// deadline is written only while seat_grace_until is unset, so concurrent reconciles keep the first deadline.
func (r *GymMemberRepository) SetSeatState(ctx context.Context, gymID, userID string, state db.SeatState, graceUntil *time.Time, at time.Time) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	filter := bson.M{"gym_id": gymID, "user_id": userID, "status": db.GymMemberStatusActive, "coach_slot": true}
	upd := bson.M{"$set": bson.M{"seat_state": state, "seat_state_at": at}}
	if graceUntil != nil {
		unsetOnly := bson.M{"gym_id": gymID, "user_id": userID, "status": db.GymMemberStatusActive, "coach_slot": true,
			"seat_grace_until": bson.M{"$exists": false}}
		if _, err := r.members.UpdateOne(ctx, unsetOnly, bson.M{"$set": bson.M{"seat_grace_until": *graceUntil}}); err != nil {
			return false, err
		}
	} else {
		upd["$unset"] = bson.M{"seat_grace_until": ""}
	}
	res, err := r.members.UpdateOne(ctx, filter, upd)
	if err != nil {
		return false, err
	}
	return res.MatchedCount > 0, nil
}
