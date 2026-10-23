package repositories

import (
	"context"
	"errors"
	"regexp"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"coachify-account-api/models/db"
)

const (
	gymsCollection           = "gyms"
	gymHeadCoachesCollection = "gym_head_coaches"
	gymInvitationsCollection = "gym_invitations"
)

var (
	// ErrHeadCoachInGym is returned when a head coach already belongs to a gym.
	ErrHeadCoachInGym = errors.New("head coach already belongs to a gym")
	// ErrInvitationPending is returned when a pending invitation for the same gym and email exists.
	ErrInvitationPending = errors.New("a pending invitation already exists")
)

// GymRepository persists gyms, their head coaches and invitations. Audit and team lookups go through the team repository.
type GymRepository struct {
	gyms    *mongo.Collection
	heads   *mongo.Collection
	invites *mongo.Collection
	team    *TeamRepository
}

func NewGymRepository(database *mongo.Database, team *TeamRepository) *GymRepository {
	return &GymRepository{
		gyms:    database.Collection(gymsCollection),
		heads:   database.Collection(gymHeadCoachesCollection),
		invites: database.Collection(gymInvitationsCollection),
		team:    team,
	}
}

// bd builds an ordered bson document from key/value pairs.
func bd(kv ...interface{}) bson.D {
	out := make(bson.D, 0, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, bson.E{Key: kv[i].(string), Value: kv[i+1]})
	}
	return out
}

func ba(stages []bson.D) bson.A {
	out := make(bson.A, 0, len(stages))
	for _, s := range stages {
		out = append(out, s)
	}
	return out
}

// fullName joins the first elements of two $lookup result paths such as "$o.firstname".
func fullName(first, last string) bson.D {
	return bd("$trim", bd("input", bd("$concat", bson.A{
		bd("$ifNull", bson.A{bd("$first", first), ""}), " ", bd("$ifNull", bson.A{bd("$first", last), ""}),
	})))
}

func pageDefaults(page, size int) (int, int) {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	return page, size
}

func nameRegex(q string, fields ...string) bson.D {
	rx := bd("$regex", regexp.QuoteMeta(q), "$options", "i")
	or := make(bson.A, 0, len(fields))
	for _, f := range fields {
		or = append(or, bd(f, rx))
	}
	return bd("$or", or)
}

// facetPage wraps stages into a total count plus one page of data.
func facetPage(data bson.A) bson.D {
	return bd("$facet", bd(
		"total", bson.A{bd("$count", "n")},
		"data", data,
	))
}

type pagedResult[T any] struct {
	Total []struct {
		N int `bson:"n"`
	} `bson:"total"`
	Data []T `bson:"data"`
}

func runPaged[T any](ctx context.Context, coll *mongo.Collection, pipeline []bson.D) ([]T, int, error) {
	cursor, err := coll.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	var out []pagedResult[T]
	if err := cursor.All(ctx, &out); err != nil {
		return nil, 0, err
	}
	if len(out) == 0 {
		return []T{}, 0, nil
	}
	total := 0
	if len(out[0].Total) > 0 {
		total = out[0].Total[0].N
	}
	rows := out[0].Data
	if rows == nil {
		rows = []T{}
	}
	return rows, total, nil
}

func gymOID(id string) (primitive.ObjectID, bool) {
	oid, err := primitive.ObjectIDFromHex(id)
	return oid, err == nil
}

// ---------- Gyms ----------

func (r *GymRepository) CreateGym(ctx context.Context, g *db.Gym) error {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	res, err := r.gyms.InsertOne(ctx, g)
	if err != nil {
		return err
	}
	if oid, ok := res.InsertedID.(primitive.ObjectID); ok {
		g.ID = oid
	}
	return nil
}

// GetGym returns a gym, or nil when it does not exist (or the id is malformed).
func (r *GymRepository) GetGym(ctx context.Context, id string) (*db.Gym, error) {
	return r.findGym(ctx, id, "")
}

// GetOwnedGym returns a gym only when ownerID owns it.
func (r *GymRepository) GetOwnedGym(ctx context.Context, id, ownerID string) (*db.Gym, error) {
	return r.findGym(ctx, id, ownerID)
}

func (r *GymRepository) findGym(ctx context.Context, id, ownerID string) (*db.Gym, error) {
	oid, ok := gymOID(id)
	if !ok {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	filter := bson.M{"_id": oid}
	if ownerID != "" {
		filter["owner_id"] = ownerID
	}
	var g db.Gym
	if err := r.gyms.FindOne(ctx, filter).Decode(&g); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	return &g, nil
}

// UpdateGym applies the non-nil fields; false when the gym does not exist.
func (r *GymRepository) UpdateGym(ctx context.Context, id string, u db.GymUpdate) (bool, error) {
	oid, ok := gymOID(id)
	if !ok {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()

	now := time.Now()
	set := bson.M{"updated_at": now}
	if u.Name != nil {
		set["name"] = *u.Name
	}
	if u.OwnerID != nil {
		set["owner_id"] = *u.OwnerID
	}
	update := bson.M{"$set": set}
	if u.Status != nil {
		set["status"] = *u.Status
		if *u.Status == db.GymStatusArchived {
			set["archived_at"] = now
		} else {
			update["$unset"] = bson.M{"archived_at": ""}
		}
	}
	res, err := r.gyms.UpdateOne(ctx, bson.M{"_id": oid}, update)
	if err != nil {
		return false, err
	}
	return res.MatchedCount > 0, nil
}

// gymCountStages adds head_coach_count (members of gym_head_coaches) to gym rows and a string gym_id.
func gymCountStages() []bson.D {
	return []bson.D{
		{{Key: "$addFields", Value: bd("gym_id", bd("$toString", "$_id"))}},
		{{Key: "$lookup", Value: bd("from", gymHeadCoachesCollection, "localField", "gym_id", "foreignField", "gym_id", "as", "hc")}},
		{{Key: "$addFields", Value: bd("head_coach_count", bd("$size", "$hc"))}},
	}
}

// ListGymsByOwner returns the owner's gyms with their head coach count, in one aggregation.
func (r *GymRepository) ListGymsByOwner(ctx context.Context, ownerID string) ([]db.GymSummary, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()

	pipeline := []bson.D{
		{{Key: "$match", Value: bd("owner_id", ownerID)}},
		{{Key: "$sort", Value: bd("created_at", 1, "_id", 1)}},
	}
	pipeline = append(pipeline, gymCountStages()...)
	pipeline = append(pipeline, bson.D{{Key: "$project", Value: bd("_id", 0, "gym_id", 1, "name", 1, "status", 1, "head_coach_count", 1)}})

	cursor, err := r.gyms.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	out := []db.GymSummary{}
	if err := cursor.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListGymsAdmin returns one page of gyms (optionally filtered by name and status) with owner and head coach count, in one aggregation.
func (r *GymRepository) ListGymsAdmin(ctx context.Context, page, size int, q, status string) ([]db.AdminGymRow, int, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	page, size = pageDefaults(page, size)

	pipeline := []bson.D{}
	filter := bson.D{}
	if q != "" {
		filter = append(filter, nameRegex(q, "name")...)
	}
	if status != "" {
		filter = append(filter, bson.E{Key: "status", Value: status})
	}
	if len(filter) > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: filter}})
	}
	pipeline = append(pipeline, bson.D{{Key: "$sort", Value: bd("created_at", -1, "_id", 1)}})

	data := bson.A{
		bd("$skip", int64((page-1)*size)),
		bd("$limit", int64(size)),
	}
	for _, st := range gymCountStages() {
		data = append(data, st)
	}
	data = append(data,
		bd("$lookup", bd(
			"from", r.team.usersName,
			"let", bd("oid", "$owner_id"),
			"pipeline", bson.A{
				bd("$match", bd("$expr", bd("$eq", bson.A{"$externalid", "$$oid"}))),
				bd("$project", bd("firstname", 1, "lastname", 1, "email", 1, "profile_picture", 1)),
			},
			"as", "o",
		)),
		bd("$project", bd(
			"_id", 0, "gym_id", 1, "name", 1, "status", 1, "owner_id", 1,
			"owner_name", fullName("$o.firstname", "$o.lastname"),
			"owner_email", bd("$ifNull", bson.A{bd("$first", "$o.email"), ""}),
			"owner_profile_picture", bd("$first", "$o.profile_picture"),
			"head_coach_count", 1, "created_at", 1, "archived_at", 1,
		)),
	)
	pipeline = append(pipeline, facetPage(data))
	return runPaged[db.AdminGymRow](ctx, r.gyms, pipeline)
}

// ListGymHeadCoachesAdmin returns all current head coaches of a gym with team size, in one aggregation.
func (r *GymRepository) ListGymHeadCoachesAdmin(ctx context.Context, gymID string) ([]db.AdminGymHeadCoachRow, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()

	pipeline := []bson.D{
		{{Key: "$match", Value: bd("gym_id", gymID)}},
		{{Key: "$lookup", Value: bd(
			"from", r.team.usersName,
			"let", bd("hid", "$head_coach_id"),
			"pipeline", bson.A{
				bd("$match", bd("$expr", bd("$eq", bson.A{"$externalid", "$$hid"}))),
				bd("$project", bd("externalid", 1, "firstname", 1, "lastname", 1, "email", 1, "profile_picture", 1, "status", 1)),
			},
			"as", "u",
		)}},
		{{Key: "$unwind", Value: "$u"}},
		{{Key: "$lookup", Value: bd(
			"from", teamMembersCollection,
			"let", bd("hid", "$head_coach_id"),
			"pipeline", bson.A{
				bd("$match", bd("$expr", bd("$eq", bson.A{"$head_coach_id", "$$hid"}))),
				bd("$count", "n"),
			},
			"as", "t",
		)}},
		{{Key: "$project", Value: bd(
			"_id", 0,
			"externalid", "$u.externalid",
			"firstname", "$u.firstname",
			"lastname", "$u.lastname",
			"email", "$u.email",
			"profile_picture", "$u.profile_picture",
			"status", "$u.status",
			"added_at", 1,
			"added_by_role", 1,
			"team_size", bd("$ifNull", bson.A{bd("$first", "$t.n"), 0}),
		)}},
		{{Key: "$sort", Value: bd("firstname", 1, "lastname", 1, "externalid", 1)}},
	}
	cursor, err := r.heads.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	out := []db.AdminGymHeadCoachRow{}
	if err := cursor.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ---------- Gym head coaches ----------

// AddHeadCoach inserts the membership and opens the gym period. ErrHeadCoachInGym when the head coach already has a gym.
func (r *GymRepository) AddHeadCoach(ctx context.Context, h *db.GymHeadCoach, reason string) error {
	opCtx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	if h.AddedAt.IsZero() {
		h.AddedAt = time.Now()
	}
	if _, err := r.heads.InsertOne(opCtx, h); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return ErrHeadCoachInGym
		}
		return err
	}
	if err := r.team.periods.OpenPeriod(ctx, db.PeriodKindGym, h.HeadCoachID, h.GymID, reason, h.AddedAt); err != nil {
		periodFailed("openGymPeriod", err, h.HeadCoachID, h.GymID)
	}
	return nil
}

// GetGymOfHead returns the gym membership of a head coach, or nil.
func (r *GymRepository) GetGymOfHead(ctx context.Context, headID string) (*db.GymHeadCoach, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	var h db.GymHeadCoach
	if err := r.heads.FindOne(ctx, bson.M{"head_coach_id": headID}).Decode(&h); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	return &h, nil
}

func (r *GymRepository) closeHeadPeriod(ctx context.Context, headID, reason string) {
	if err := r.team.periods.ClosePeriod(ctx, db.PeriodKindGym, headID, reason, time.Now()); err != nil {
		periodFailed("closeGymPeriod", err, headID)
	}
}

// RemoveHeadCoach deletes the membership of a head coach in a given gym and closes the period.
func (r *GymRepository) RemoveHeadCoach(ctx context.Context, gymID, headID, reason string) (bool, error) {
	opCtx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	res, err := r.heads.DeleteOne(opCtx, bson.M{"gym_id": gymID, "head_coach_id": headID})
	if err != nil {
		return false, err
	}
	if res.DeletedCount == 0 {
		return false, nil
	}
	r.closeHeadPeriod(ctx, headID, reason)
	return true, nil
}

// ReleaseHead removes the gym membership of a head coach wherever it is; returns the gym id it was in.
func (r *GymRepository) ReleaseHead(ctx context.Context, headID, reason string) (string, bool, error) {
	opCtx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	var h db.GymHeadCoach
	err := r.heads.FindOneAndDelete(opCtx, bson.M{"head_coach_id": headID}).Decode(&h)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return "", false, nil
		}
		return "", false, err
	}
	r.closeHeadPeriod(ctx, headID, reason)
	return h.GymID, true, nil
}

// RemoveAllByGym releases every head coach of a gym and closes their periods; returns the released ids.
func (r *GymRepository) RemoveAllByGym(ctx context.Context, gymID, reason string) ([]string, error) {
	opCtx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	cursor, err := r.heads.Find(opCtx, bson.M{"gym_id": gymID}, options.Find().SetProjection(bson.M{"head_coach_id": 1}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(opCtx)
	var docs []struct {
		HeadCoachID string `bson:"head_coach_id"`
	}
	if err := cursor.All(opCtx, &docs); err != nil {
		return nil, err
	}
	if _, err := r.heads.DeleteMany(opCtx, bson.M{"gym_id": gymID}); err != nil {
		return nil, err
	}
	if err := r.team.periods.CloseByParent(ctx, db.PeriodKindGym, gymID, reason, time.Now()); err != nil {
		periodFailed("closeGymByParent", err, gymID)
	}
	ids := make([]string, 0, len(docs))
	for _, d := range docs {
		ids = append(ids, d.HeadCoachID)
	}
	return ids, nil
}

// ---------- Gym reads ----------

// gymCoachStages lists one row per coach of the teams of the gym's head coaches (optionally one head coach).
func (r *GymRepository) gymCoachStages(gymID, headID string) []bson.D {
	match := bd("gym_id", gymID)
	if headID != "" {
		match = append(match, bson.E{Key: "head_coach_id", Value: headID})
	}
	return []bson.D{
		{{Key: "$match", Value: match}},
		{{Key: "$lookup", Value: bd("from", teamMembersCollection, "localField", "head_coach_id", "foreignField", "head_coach_id", "as", "m")}},
		{{Key: "$unwind", Value: "$m"}},
		{{Key: "$lookup", Value: bd(
			"from", r.team.usersName,
			"let", bd("cid", "$m.coach_id"),
			"pipeline", bson.A{
				bd("$match", bd("$expr", bd("$eq", bson.A{"$externalid", "$$cid"}))),
				bd("$project", bd("externalid", 1, "firstname", 1, "lastname", 1, "email", 1,
					"profile_picture", 1, "status", 1, "last_login", 1, "created_at", 1)),
			},
			"as", "u",
		)}},
		{{Key: "$unwind", Value: "$u"}},
		{{Key: "$lookup", Value: bd(
			"from", r.team.usersName,
			"let", bd("hid", "$head_coach_id"),
			"pipeline", bson.A{
				bd("$match", bd("$expr", bd("$eq", bson.A{"$externalid", "$$hid"}))),
				bd("$project", bd("firstname", 1, "lastname", 1)),
			},
			"as", "h",
		)}},
		{{Key: "$project", Value: bd(
			"_id", 0,
			"externalid", "$u.externalid",
			"firstname", "$u.firstname",
			"lastname", "$u.lastname",
			"email", "$u.email",
			"profile_picture", "$u.profile_picture",
			"status", "$u.status",
			"last_login", "$u.last_login",
			"created_at", "$u.created_at",
			"joined_team_at", "$m.assigned_at",
			"head_coach_id", 1,
			"head_coach_name", fullName("$h.firstname", "$h.lastname"),
		)}},
	}
}

// ListCoaches returns one page of the gym's team coaches with client stats, in a single aggregation.
func (r *GymRepository) ListCoaches(ctx context.Context, gymID string, q db.GymCoachQuery) ([]db.GymCoachRow, int, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	q.Page, q.Size = pageDefaults(q.Page, q.Size)
	cutoff := time.Now().AddDate(0, 0, -30)

	pipeline := r.gymCoachStages(gymID, q.HeadCoachID)
	filter := bson.D{}
	if q.Status != "" {
		filter = append(filter, bson.E{Key: "status", Value: q.Status})
	}
	if q.Q != "" {
		filter = append(filter, nameRegex(q.Q, "firstname", "lastname", "email")...)
	}
	if len(filter) > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: filter}})
	}

	var sortDoc bson.D
	switch q.Sort {
	case "clients":
		sortDoc = bd("client_count", -1, "externalid", 1)
		pipeline = append(pipeline, r.team.clientStatsStages(cutoff)...)
	case "last_login":
		sortDoc = bd("last_login", -1, "externalid", 1)
	default:
		sortDoc = bd("firstname", 1, "lastname", 1, "externalid", 1)
	}
	data := bson.A{
		bd("$sort", sortDoc),
		bd("$skip", int64((q.Page-1)*q.Size)),
		bd("$limit", int64(q.Size)),
	}
	if q.Sort != "clients" {
		for _, st := range r.team.clientStatsStages(cutoff) {
			data = append(data, st)
		}
	}
	pipeline = append(pipeline, facetPage(data))
	return runPaged[db.GymCoachRow](ctx, r.heads, pipeline)
}

// Overview aggregates the gym's head coaches, coaches and clients in a single pipeline.
func (r *GymRepository) Overview(ctx context.Context, gymID string) (*db.GymOverview, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	now := time.Now()

	stages := r.gymCoachStages(gymID, "")
	agg := append([]bson.D{}, stages[1:]...)
	agg = append(agg, r.team.clientStatsStages(now.AddDate(0, 0, -30))...)
	agg = append(agg, bson.D{{Key: "$group", Value: bd(
		"_id", nil,
		"coaches", bd("$sum", 1),
		"active_coaches", bd("$sum", bd("$cond", bson.A{bd("$eq", bson.A{"$status", "Active"}), 1, 0})),
		"inactive_coaches_14d", bd("$sum", bd("$cond", bson.A{
			bd("$lt", bson.A{bd("$ifNull", bson.A{"$last_login", time.Time{}}), now.AddDate(0, 0, -14)}), 1, 0})),
		"total_clients", bd("$sum", "$client_count"),
		"active_clients", bd("$sum", "$active_client_count"),
		"new_clients_30d", bd("$sum", "$new_clients_30d"),
	)}})
	pipeline := []bson.D{
		stages[0],
		{{Key: "$facet", Value: bd(
			"hc", bson.A{bd("$count", "n")},
			"agg", ba(agg),
		)}},
	}

	cursor, err := r.heads.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var out []struct {
		HC []struct {
			N int `bson:"n"`
		} `bson:"hc"`
		Agg []db.GymOverview `bson:"agg"`
	}
	if err := cursor.All(ctx, &out); err != nil {
		return nil, err
	}
	o := &db.GymOverview{}
	if len(out) > 0 {
		if len(out[0].Agg) > 0 {
			*o = out[0].Agg[0]
		}
		if len(out[0].HC) > 0 {
			o.HeadCoaches = out[0].HC[0].N
		}
	}
	return o, nil
}

// ListHeadCoachRows returns one page of the gym's head coaches with team size and client count, in one aggregation.
func (r *GymRepository) ListHeadCoachRows(ctx context.Context, gymID, q string, page, size int) ([]db.GymHeadCoachRow, int, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	page, size = pageDefaults(page, size)

	pipeline := []bson.D{
		{{Key: "$match", Value: bd("gym_id", gymID)}},
		{{Key: "$lookup", Value: bd(
			"from", r.team.usersName,
			"let", bd("hid", "$head_coach_id"),
			"pipeline", bson.A{
				bd("$match", bd("$expr", bd("$eq", bson.A{"$externalid", "$$hid"}))),
				bd("$project", bd("externalid", 1, "firstname", 1, "lastname", 1, "email", 1,
					"profile_picture", 1, "status", 1, "head_coach_since", 1)),
			},
			"as", "u",
		)}},
		{{Key: "$unwind", Value: "$u"}},
		{{Key: "$project", Value: bd(
			"_id", 0,
			"externalid", "$u.externalid",
			"firstname", "$u.firstname",
			"lastname", "$u.lastname",
			"email", "$u.email",
			"profile_picture", "$u.profile_picture",
			"status", "$u.status",
			"head_coach_since", "$u.head_coach_since",
			"added_at", 1,
		)}},
	}
	if q != "" {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: nameRegex(q, "firstname", "lastname", "email")}})
	}
	data := bson.A{
		bd("$sort", bd("firstname", 1, "lastname", 1, "externalid", 1)),
		bd("$skip", int64((page-1)*size)),
		bd("$limit", int64(size)),
		bd("$lookup", bd(
			"from", teamMembersCollection,
			"let", bd("hid", "$externalid"),
			"pipeline", bson.A{
				bd("$match", bd("$expr", bd("$eq", bson.A{"$head_coach_id", "$$hid"}))),
				bd("$lookup", bd(
					"from", r.team.clientsName,
					"let", bd("cid", "$coach_id"),
					"pipeline", bson.A{
						bd("$match", bd("$expr", bd("$eq", bson.A{"$coach_id", "$$cid"}))),
						bd("$count", "n"),
					},
					"as", "cc",
				)),
				bd("$group", bd(
					"_id", nil,
					"team_size", bd("$sum", 1),
					"client_count", bd("$sum", bd("$ifNull", bson.A{bd("$first", "$cc.n"), 0})),
				)),
			},
			"as", "t",
		)),
		bd("$addFields", bd(
			"team_size", bd("$ifNull", bson.A{bd("$first", "$t.team_size"), 0}),
			"client_count", bd("$ifNull", bson.A{bd("$first", "$t.client_count"), 0}),
		)),
		bd("$project", bd("t", 0)),
	}
	pipeline = append(pipeline, facetPage(data))
	return runPaged[db.GymHeadCoachRow](ctx, r.heads, pipeline)
}

// ListClients returns one page of the clients of the gym's coaches (safe fields only), in a single aggregation.
func (r *GymRepository) ListClients(ctx context.Context, gymID string, q db.GymClientQuery) ([]db.GymClientRow, int, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	q.Page, q.Size = pageDefaults(q.Page, q.Size)

	match := bd("gym_id", gymID)
	if q.HeadCoachID != "" {
		match = append(match, bson.E{Key: "head_coach_id", Value: q.HeadCoachID})
	}
	pipeline := []bson.D{
		{{Key: "$match", Value: match}},
		{{Key: "$lookup", Value: bd("from", teamMembersCollection, "localField", "head_coach_id", "foreignField", "head_coach_id", "as", "m")}},
		{{Key: "$unwind", Value: "$m"}},
	}
	if q.CoachID != "" {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: bd("m.coach_id", q.CoachID)}})
	}
	pipeline = append(pipeline,
		bson.D{{Key: "$lookup", Value: bd(
			"from", r.team.clientsName,
			"let", bd("cid", "$m.coach_id"),
			"pipeline", bson.A{
				bd("$match", bd("$expr", bd("$eq", bson.A{"$coach_id", "$$cid"}))),
				bd("$project", bd("client_id", 1, "created_at", 1)),
			},
			"as", "cc",
		)}},
		bson.D{{Key: "$unwind", Value: "$cc"}},
		bson.D{{Key: "$lookup", Value: bd(
			"from", r.team.usersName,
			"let", bd("client", "$cc.client_id"),
			"pipeline", bson.A{
				bd("$match", bd("$expr", bd("$eq", bson.A{"$externalid", "$$client"}))),
				bd("$project", bd("externalid", 1, "firstname", 1, "lastname", 1, "email", 1, "profile_picture", 1, "status", 1)),
			},
			"as", "u",
		)}},
		bson.D{{Key: "$unwind", Value: "$u"}},
		bson.D{{Key: "$lookup", Value: bd(
			"from", r.team.usersName,
			"let", bd("cid", "$m.coach_id"),
			"pipeline", bson.A{
				bd("$match", bd("$expr", bd("$eq", bson.A{"$externalid", "$$cid"}))),
				bd("$project", bd("firstname", 1, "lastname", 1)),
			},
			"as", "co",
		)}},
		bson.D{{Key: "$project", Value: bd(
			"_id", 0,
			"externalid", "$u.externalid",
			"firstname", "$u.firstname",
			"lastname", "$u.lastname",
			"email", "$u.email",
			"profile_picture", "$u.profile_picture",
			"status", "$u.status",
			"coach_id", "$m.coach_id",
			"coach_name", fullName("$co.firstname", "$co.lastname"),
			"head_coach_id", 1,
			"linked_at", "$cc.created_at",
		)}},
	)
	if q.Q != "" {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: nameRegex(q.Q, "firstname", "lastname", "email")}})
	}
	sortDoc := bd("firstname", 1, "lastname", 1, "externalid", 1)
	if q.Sort == "linked_at" {
		sortDoc = bd("linked_at", -1, "externalid", 1)
	}
	pipeline = append(pipeline, facetPage(bson.A{
		bd("$sort", sortDoc),
		bd("$skip", int64((q.Page-1)*q.Size)),
		bd("$limit", int64(q.Size)),
	}))
	return runPaged[db.GymClientRow](ctx, r.heads, pipeline)
}

// ---------- Invitations ----------

// InsertInvitation stores a pending invitation. ErrInvitationPending when one exists for the same gym and email.
func (r *GymRepository) InsertInvitation(ctx context.Context, inv *db.GymInvitation) error {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	res, err := r.invites.InsertOne(ctx, inv)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return ErrInvitationPending
		}
		return err
	}
	if oid, ok := res.InsertedID.(primitive.ObjectID); ok {
		inv.ID = oid
	}
	return nil
}

// CountInvitationsSince counts invitations a gym created since the given time (any status).
func (r *GymRepository) CountInvitationsSince(ctx context.Context, gymID string, since time.Time) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	n, err := r.invites.CountDocuments(ctx, bson.M{"gym_id": gymID, "created_at": bson.M{"$gte": since}})
	return int(n), err
}

// ExpireDue marks pending invitations past their expiry as expired, scoped by gym and/or email (empty = any).
func (r *GymRepository) ExpireDue(ctx context.Context, gymID, email string, now time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	filter := bson.M{"status": db.GymInvitePending, "expires_at": bson.M{"$lte": now}}
	if gymID != "" {
		filter["gym_id"] = gymID
	}
	if email != "" {
		filter["email"] = email
	}
	_, err := r.invites.UpdateMany(ctx, filter, bson.M{"$set": bson.M{"status": db.GymInviteExpired}})
	return err
}

// GetInvitation returns an invitation, or nil when it does not exist (or the id is malformed).
func (r *GymRepository) GetInvitation(ctx context.Context, id string) (*db.GymInvitation, error) {
	oid, ok := gymOID(id)
	if !ok {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	var inv db.GymInvitation
	if err := r.invites.FindOne(ctx, bson.M{"_id": oid}).Decode(&inv); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	return &inv, nil
}

// TransitionInvitation moves an invitation from one status to another atomically; false when it was not in `from`.
func (r *GymRepository) TransitionInvitation(ctx context.Context, id, from, to, inviteeID string, at time.Time) (bool, error) {
	oid, ok := gymOID(id)
	if !ok {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	set := bson.M{"status": to, "responded_at": at}
	if inviteeID != "" {
		set["invitee_id"] = inviteeID
	}
	res, err := r.invites.UpdateOne(ctx, bson.M{"_id": oid, "status": from}, bson.M{"$set": set})
	if err != nil {
		return false, err
	}
	return res.MatchedCount > 0, nil
}

// RevokePendingByGym revokes every pending invitation of a gym.
func (r *GymRepository) RevokePendingByGym(ctx context.Context, gymID string, at time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	_, err := r.invites.UpdateMany(ctx,
		bson.M{"gym_id": gymID, "status": db.GymInvitePending},
		bson.M{"$set": bson.M{"status": db.GymInviteRevoked, "responded_at": at}})
	return err
}

// ListInvitations returns a gym's invitations newest first, optionally filtered by status.
func (r *GymRepository) ListInvitations(ctx context.Context, gymID, status string, page, size int) ([]db.GymInvitation, int, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	page, size = pageDefaults(page, size)

	filter := bson.M{"gym_id": gymID}
	if status != "" {
		filter["status"] = status
	}
	total, err := r.invites.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	cursor, err := r.invites.Find(ctx, filter, options.Find().
		SetSort(bson.D{{Key: "created_at", Value: -1}}).
		SetSkip(int64((page-1)*size)).
		SetLimit(int64(size)))
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)
	out := []db.GymInvitation{}
	if err := cursor.All(ctx, &out); err != nil {
		return nil, 0, err
	}
	return out, int(total), nil
}

// ListPendingForEmail returns the unexpired pending invitations addressed to an email, with gym and owner names.
func (r *GymRepository) ListPendingForEmail(ctx context.Context, email string, now time.Time) ([]db.GymInvitationView, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()

	pipeline := []bson.D{
		{{Key: "$match", Value: bd("email", email, "status", db.GymInvitePending, "expires_at", bd("$gt", now))}},
		{{Key: "$sort", Value: bd("created_at", -1)}},
		{{Key: "$limit", Value: int64(100)}},
		// gym_id is stored as a string; convert once so the join hits the _id index (a computed $expr match cannot).
		{{Key: "$addFields", Value: bd("gid_oid", bd("$convert", bd("input", "$gym_id", "to", "objectId", "onError", nil, "onNull", nil)))}},
		{{Key: "$lookup", Value: bd(
			"from", gymsCollection,
			"localField", "gid_oid",
			"foreignField", "_id",
			"pipeline", bson.A{
				bd("$match", bd("status", db.GymStatusActive)),
				bd("$project", bd("name", 1, "owner_id", 1)),
			},
			"as", "g",
		)}},
		{{Key: "$unwind", Value: "$g"}},
		{{Key: "$lookup", Value: bd(
			"from", r.team.usersName,
			"localField", "g.owner_id",
			"foreignField", "externalid",
			"pipeline", bson.A{
				bd("$project", bd("firstname", 1, "lastname", 1)),
			},
			"as", "o",
		)}},
		{{Key: "$project", Value: bd(
			"_id", 0,
			"id", bd("$toString", "$_id"),
			"gym_name", "$g.name",
			"owner_name", fullName("$o.firstname", "$o.lastname"),
			"expires_at", 1,
		)}},
	}
	cursor, err := r.invites.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	out := []db.GymInvitationView{}
	if err := cursor.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ---------- Team lookups and audit (delegated to the team repository) ----------

func (r *GymRepository) IsTeamMember(ctx context.Context, coachID string) (bool, error) {
	return r.team.IsMember(ctx, coachID)
}

func (r *GymRepository) InsertAudit(ctx context.Context, e *db.TeamAuditEntry) error {
	return r.team.InsertAudit(ctx, e)
}

func (r *GymRepository) InsertAuditMany(ctx context.Context, entries []*db.TeamAuditEntry) error {
	return r.team.InsertAuditMany(ctx, entries)
}

// ListAudit returns a gym's audit entries newest first.
func (r *GymRepository) ListAudit(ctx context.Context, gymID string, page, size int) ([]db.TeamAuditEntry, int, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	page, size = pageDefaults(page, size)

	filter := bson.M{"gym_id": gymID}
	total, err := r.team.audit.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	cursor, err := r.team.audit.Find(ctx, filter, options.Find().
		SetSort(bson.D{{Key: "created_at", Value: -1}}).
		SetSkip(int64((page-1)*size)).
		SetLimit(int64(size)))
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)
	entries := []db.TeamAuditEntry{}
	if err := cursor.All(ctx, &entries); err != nil {
		return nil, 0, err
	}
	return entries, int(total), nil
}
