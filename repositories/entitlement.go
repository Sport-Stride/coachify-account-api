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

// Entitlement collection names (db `users`).
const (
	EntitlementGrantsColl   = "entitlement_grants"
	EntitlementDefaultsColl = "entitlement_plan_defaults"
	EntitlementUsageColl    = "entitlement_usage"
	EntitlementAuditColl    = "entitlement_audit"
)

// ErrEntitlementVersionConflict means the grant changed since it was read.
var ErrEntitlementVersionConflict = errors.New("entitlement version conflict")

// EntitlementRepository persists grants, plan defaults and usage counters, and runs the
// admin/coach access aggregations. It is the only place that touches these collections.
type EntitlementRepository struct {
	grants       *mongo.Collection
	planDefaults *mongo.Collection
	usage        *mongo.Collection
	users        *mongo.Collection
	coachClients *mongo.Collection
}

func NewEntitlementRepository(database *mongo.Database, usersColl, coachClientsColl string) *EntitlementRepository {
	return &EntitlementRepository{
		grants:       database.Collection(EntitlementGrantsColl),
		planDefaults: database.Collection(EntitlementDefaultsColl),
		usage:        database.Collection(EntitlementUsageColl),
		users:        database.Collection(usersColl),
		coachClients: database.Collection(coachClientsColl),
	}
}

// GetGrant returns the grant of a subject, or nil when it has none.
func (r *EntitlementRepository) GetGrant(ctx context.Context, subjectID string) (*db.EntitlementGrant, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	var g db.EntitlementGrant
	err := r.grants.FindOne(ctx, bson.M{"subject_id": subjectID}).Decode(&g)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// ReplaceGrant stores g with Version prevVersion+1. prevVersion 0 means "no document yet".
// It returns ErrEntitlementVersionConflict when the stored version differs.
func (r *EntitlementRepository) ReplaceGrant(ctx context.Context, g *db.EntitlementGrant, prevVersion int64) error {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	g.Version = prevVersion + 1
	if prevVersion == 0 {
		_, err := r.grants.InsertOne(ctx, g)
		if mongo.IsDuplicateKeyError(err) {
			return ErrEntitlementVersionConflict
		}
		return err
	}
	res, err := r.grants.ReplaceOne(ctx, bson.M{"subject_id": g.SubjectID, "version": prevVersion}, g)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return ErrEntitlementVersionConflict
	}
	return nil
}

// ListPlanDefaults returns every plan default (a handful of documents).
func (r *EntitlementRepository) ListPlanDefaults(ctx context.Context) ([]db.PlanDefault, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	cur, err := r.planDefaults.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "plan_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	out := []db.PlanDefault{}
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// UpsertPlanDefault replaces the default document of pd.PlanID.
func (r *EntitlementRepository) UpsertPlanDefault(ctx context.Context, pd *db.PlanDefault) error {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	_, err := r.planDefaults.ReplaceOne(ctx, bson.M{"plan_id": pd.PlanID}, pd, options.Replace().SetUpsert(true))
	return err
}

// GetUsage returns the usage documents of the given subjects keyed by subject id.
func (r *EntitlementRepository) GetUsage(ctx context.Context, ids []string) (map[string]*db.EntitlementUsage, error) {
	out := make(map[string]*db.EntitlementUsage, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	cur, err := r.usage.Find(ctx, bson.M{"subject_id": bson.M{"$in": ids}})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var docs []db.EntitlementUsage
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	for i := range docs {
		out[docs[i].SubjectID] = &docs[i]
	}
	return out, nil
}

// ConsumeUsage atomically adds deltas to the feature counters when no counter in maxes would
// exceed its maximum. deltas must list every counter of the feature (zero allowed) so a new
// document starts with all counters at zero. It returns the new usage, or ok=false when a
// maximum would be exceeded. A counter absent from maxes is unlimited.
func (r *EntitlementRepository) ConsumeUsage(ctx context.Context, subjectID, feature string, deltas, maxes map[string]int64) (*db.EntitlementUsage, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()

	onInsert := bson.M{"subject_id": subjectID, "updated_at": time.Now()}
	inc := bson.M{}
	for counter, d := range deltas {
		onInsert["counters."+feature+"."+counter] = int64(0)
		inc["counters."+feature+"."+counter] = d
	}
	// A concurrent first upsert loses with a duplicate key; the document exists either way.
	_, err := r.usage.UpdateOne(ctx, bson.M{"subject_id": subjectID},
		bson.M{"$setOnInsert": onInsert}, options.Update().SetUpsert(true))
	if err != nil && !mongo.IsDuplicateKeyError(err) {
		return nil, false, err
	}

	filter := bson.M{"subject_id": subjectID}
	for counter, max := range maxes {
		filter["counters."+feature+"."+counter] = bson.M{"$lte": max - deltas[counter]}
	}
	var out db.EntitlementUsage
	err = r.usage.FindOneAndUpdate(ctx, filter,
		bson.M{"$inc": inc, "$set": bson.M{"updated_at": time.Now()}},
		options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&out)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &out, true, nil
}

// ReleaseUsage subtracts deltas from the feature counters, never going below zero.
func (r *EntitlementRepository) ReleaseUsage(ctx context.Context, subjectID, feature string, deltas map[string]int64) error {
	if len(deltas) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	set := bson.M{"updated_at": time.Now()}
	for counter, d := range deltas {
		path := "counters." + feature + "." + counter
		set[path] = bson.M{"$max": bson.A{int64(0), bson.M{"$subtract": bson.A{bson.M{"$ifNull": bson.A{"$" + path, int64(0)}}, d}}}}
	}
	_, err := r.usage.UpdateOne(ctx, bson.M{"subject_id": subjectID}, mongo.Pipeline{{{Key: "$set", Value: set}}})
	return err
}

// CountClients returns the number of clients linked to a coach.
func (r *EntitlementRepository) CountClients(ctx context.Context, coachID string) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	return r.coachClients.CountDocuments(ctx, bson.M{"coach_id": coachID})
}

// ListCoachAccess returns one page of coaches with client count, grant and usage in a single aggregation.
func (r *EntitlementRepository) ListCoachAccess(ctx context.Context, q db.CoachAccessQuery) ([]db.CoachAccessRow, int, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Size < 1 {
		q.Size = 20
	}
	match := bson.D{{Key: "role", Value: bson.D{{Key: "$in", Value: bson.A{"coach", "nutritionist"}}}}}
	if q.Status != "" {
		match = append(match, bson.E{Key: "status", Value: q.Status})
	}
	if clause, ok := userSearchClause(q.Q); ok {
		match = append(match, clause)
	}

	pipeline := mongo.Pipeline{{{Key: "$match", Value: match}}}
	if q.Feature != "" && q.Disabled {
		// The override lives on the grant document; join it before filtering. Feature is validated by the caller.
		pipeline = append(pipeline,
			bson.D{{Key: "$lookup", Value: bson.D{
				{Key: "from", Value: r.grants.Name()},
				{Key: "localField", Value: "externalid"},
				{Key: "foreignField", Value: "subject_id"},
				{Key: "as", Value: "g"},
			}}},
			bson.D{{Key: "$match", Value: bson.D{{Key: "g.features." + q.Feature + ".enabled", Value: false}}}},
		)
	}
	pipeline = append(pipeline, bson.D{{Key: "$facet", Value: bson.D{
		{Key: "total", Value: bson.A{bson.D{{Key: "$count", Value: "n"}}}},
		{Key: "data", Value: bson.A{
			bson.D{{Key: "$sort", Value: bson.D{{Key: "lastname", Value: 1}, {Key: "firstname", Value: 1}, {Key: "externalid", Value: 1}}}},
			bson.D{{Key: "$skip", Value: int64((q.Page - 1) * q.Size)}},
			bson.D{{Key: "$limit", Value: int64(q.Size)}},
			bson.D{{Key: "$lookup", Value: bson.D{
				{Key: "from", Value: r.coachClients.Name()},
				{Key: "let", Value: bson.D{{Key: "id", Value: "$externalid"}}},
				{Key: "pipeline", Value: bson.A{
					bson.D{{Key: "$match", Value: bson.D{{Key: "$expr", Value: bson.D{{Key: "$eq", Value: bson.A{"$coach_id", "$$id"}}}}}}},
					bson.D{{Key: "$count", Value: "n"}},
				}},
				{Key: "as", Value: "cc"},
			}}},
			bson.D{{Key: "$lookup", Value: bson.D{
				{Key: "from", Value: r.grants.Name()},
				{Key: "localField", Value: "externalid"},
				{Key: "foreignField", Value: "subject_id"},
				{Key: "as", Value: "gr"},
			}}},
			bson.D{{Key: "$lookup", Value: bson.D{
				{Key: "from", Value: r.usage.Name()},
				{Key: "localField", Value: "externalid"},
				{Key: "foreignField", Value: "subject_id"},
				{Key: "as", Value: "us"},
			}}},
			bson.D{{Key: "$project", Value: bson.D{
				{Key: "_id", Value: 0},
				{Key: "externalid", Value: 1},
				{Key: "firstname", Value: 1},
				{Key: "lastname", Value: 1},
				{Key: "email", Value: 1},
				{Key: "role", Value: 1},
				{Key: "status", Value: 1},
				{Key: "is_head_coach", Value: bson.D{{Key: "$eq", Value: bson.A{"$is_head_coach", true}}}},
				{Key: "last_login", Value: 1},
				{Key: "client_count", Value: bson.D{{Key: "$ifNull", Value: bson.A{bson.D{{Key: "$first", Value: "$cc.n"}}, 0}}}},
				{Key: "grant", Value: bson.D{{Key: "$first", Value: "$gr"}}},
				{Key: "usage", Value: bson.D{{Key: "$first", Value: "$us"}}},
			}}},
		}},
	}}})

	cur, err := r.users.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, 0, err
	}
	defer cur.Close(ctx)
	var out []struct {
		Total []struct {
			N int `bson:"n"`
		} `bson:"total"`
		Data []db.CoachAccessRow `bson:"data"`
	}
	if err := cur.All(ctx, &out); err != nil {
		return nil, 0, err
	}
	if len(out) == 0 {
		return []db.CoachAccessRow{}, 0, nil
	}
	total := 0
	if len(out[0].Total) > 0 {
		total = out[0].Total[0].N
	}
	if out[0].Data == nil {
		out[0].Data = []db.CoachAccessRow{}
	}
	return out[0].Data, total, nil
}

// ListClientAccess returns one page of a coach's linked clients with their grant rows in one aggregation.
func (r *EntitlementRepository) ListClientAccess(ctx context.Context, coachID string, page, size int, q string) ([]db.ClientAccessRow, int, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.D{{Key: "coach_id", Value: coachID}}}},
		{{Key: "$lookup", Value: bson.D{
			{Key: "from", Value: r.users.Name()},
			{Key: "localField", Value: "client_id"},
			{Key: "foreignField", Value: "externalid"},
			{Key: "pipeline", Value: bson.A{bson.D{{Key: "$project", Value: bson.D{
				{Key: "_id", Value: 0}, {Key: "externalid", Value: 1}, {Key: "firstname", Value: 1}, {Key: "lastname", Value: 1},
				{Key: "email", Value: 1}, {Key: "status", Value: 1}, {Key: "last_login", Value: 1},
			}}}}},
			{Key: "as", Value: "u"},
		}}},
		{{Key: "$unwind", Value: "$u"}},
		{{Key: "$replaceRoot", Value: bson.D{{Key: "newRoot", Value: "$u"}}}},
	}
	if clause, ok := userSearchClause(q); ok {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.D{clause}}})
	}
	pipeline = append(pipeline, bson.D{{Key: "$facet", Value: bson.D{
		{Key: "total", Value: bson.A{bson.D{{Key: "$count", Value: "n"}}}},
		{Key: "data", Value: bson.A{
			bson.D{{Key: "$sort", Value: bson.D{{Key: "lastname", Value: 1}, {Key: "firstname", Value: 1}, {Key: "externalid", Value: 1}}}},
			bson.D{{Key: "$skip", Value: int64((page - 1) * size)}},
			bson.D{{Key: "$limit", Value: int64(size)}},
			bson.D{{Key: "$lookup", Value: bson.D{
				{Key: "from", Value: r.grants.Name()},
				{Key: "localField", Value: "externalid"},
				{Key: "foreignField", Value: "subject_id"},
				{Key: "as", Value: "gr"},
			}}},
			bson.D{{Key: "$addFields", Value: bson.D{{Key: "grant", Value: bson.D{{Key: "$first", Value: "$gr"}}}}}},
			bson.D{{Key: "$project", Value: bson.D{{Key: "gr", Value: 0}}}},
		}},
	}}})

	cur, err := r.coachClients.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, 0, err
	}
	defer cur.Close(ctx)
	var out []struct {
		Total []struct {
			N int `bson:"n"`
		} `bson:"total"`
		Data []db.ClientAccessRow `bson:"data"`
	}
	if err := cur.All(ctx, &out); err != nil {
		return nil, 0, err
	}
	if len(out) == 0 {
		return []db.ClientAccessRow{}, 0, nil
	}
	total := 0
	if len(out[0].Total) > 0 {
		total = out[0].Total[0].N
	}
	if out[0].Data == nil {
		out[0].Data = []db.ClientAccessRow{}
	}
	return out[0].Data, total, nil
}
