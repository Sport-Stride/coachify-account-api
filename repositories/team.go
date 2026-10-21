package repositories

import (
	"context"
	"errors"
	"regexp"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"coachify-account-api/models/db"
)

const (
	teamMembersCollection = "coach_team_members"
	teamAuditCollection   = "coach_team_audit"
	repoOpTimeout         = 10 * time.Second
)

// TeamRepository persists head-coach team memberships and their audit trail.
type TeamRepository struct {
	members      *mongo.Collection
	audit        *mongo.Collection
	usersName    string
	clientsName  string
	periods      *MembershipPeriodRepository
	gymHeads     *mongo.Collection
	users        *mongo.Collection
	clients      *mongo.Collection
}

func NewTeamRepository(database *mongo.Database, usersColl, coachClientsColl string) *TeamRepository {
	return &TeamRepository{
		members:     database.Collection(teamMembersCollection),
		audit:       database.Collection(teamAuditCollection),
		usersName:   usersColl,
		clientsName: coachClientsColl,
		periods:     NewMembershipPeriodRepository(database),
		gymHeads:    database.Collection(gymHeadCoachesCollection),
		users:       database.Collection(usersColl),
		clients:     database.Collection(coachClientsColl),
	}
}

// Periods exposes the membership history repository (used by the startup reconciler).
func (r *TeamRepository) Periods() *MembershipPeriodRepository { return r.periods }

// periodFailed logs a failed history write with ids only; the membership write already succeeded.
func periodFailed(op string, err error, ids ...string) {
	zap.L().Error("membership period write failed",
		zap.String("component", "repository"),
		zap.String("op", op),
		zap.Strings("ids", ids),
		zap.Error(err),
	)
}

// ScopePeriodsForHead returns the coaches currently in a head coach's team and every coach that ever was, with their periods.
func (r *TeamRepository) ScopePeriodsForHead(ctx context.Context, headID string) (*db.HeadScope, error) {
	current, err := r.IDsByHead(ctx, headID)
	if err != nil {
		return nil, err
	}
	ps, err := r.periods.ListForParent(ctx, db.PeriodKindTeam, headID)
	if err != nil {
		return nil, err
	}
	return &db.HeadScope{CurrentCoachIDs: current, Coaches: GroupPeriodsByMember(ps)}, nil
}

// Upsert sets the head coach of a coach in a single atomic operation (unique index on coach_id).
// It returns the previous membership, or nil when the coach had none.
func (r *TeamRepository) Upsert(ctx context.Context, m *db.TeamMember) (*db.TeamMember, error) {
	opCtx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()

	opts := options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.Before)
	update := bson.M{"$set": bson.M{
		"head_coach_id":    m.HeadCoachID,
		"assigned_by":      m.AssignedBy,
		"assigned_by_role": m.AssignedByRole,
		"assigned_at":      m.AssignedAt,
	}}

	var prev db.TeamMember
	var err error
	// Two concurrent first-time upserts can race on the unique index: the loser retries as an update.
	for attempt := 0; attempt < 2; attempt++ {
		err = r.members.FindOneAndUpdate(opCtx, bson.M{"coach_id": m.CoachID}, update, opts).Decode(&prev)
		if err == nil {
			reason := db.PeriodReasonAssign
			if prev.HeadCoachID != m.HeadCoachID {
				reason = db.PeriodReasonReassign
			}
			r.openPeriod(ctx, m, reason)
			return &prev, nil
		}
		if errors.Is(err, mongo.ErrNoDocuments) {
			r.openPeriod(ctx, m, db.PeriodReasonAssign)
			return nil, nil
		}
		if !mongo.IsDuplicateKeyError(err) {
			break
		}
	}
	return nil, err
}

// openPeriod records the membership start after the membership write succeeded; failures are logged only.
func (r *TeamRepository) openPeriod(ctx context.Context, m *db.TeamMember, reason string) {
	at := m.AssignedAt
	if at.IsZero() {
		at = time.Now()
	}
	if err := r.periods.OpenPeriod(ctx, db.PeriodKindTeam, m.CoachID, m.HeadCoachID, reason, at); err != nil {
		periodFailed("openPeriod", err, m.CoachID, m.HeadCoachID)
	}
}

// DeleteByCoach removes a coach's membership; the period reason defaults to unassign (see WithPeriodReason).
func (r *TeamRepository) DeleteByCoach(ctx context.Context, coachID string) (int64, error) {
	opCtx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	res, err := r.members.DeleteOne(opCtx, bson.M{"coach_id": coachID})
	if err != nil {
		return 0, err
	}
	if res.DeletedCount > 0 {
		if err := r.periods.ClosePeriod(ctx, db.PeriodKindTeam, coachID, periodReason(ctx, db.PeriodReasonUnassign), time.Now()); err != nil {
			periodFailed("closePeriod", err, coachID)
		}
	}
	return res.DeletedCount, nil
}

func (r *TeamRepository) DeleteByHead(ctx context.Context, headID string) (int64, error) {
	opCtx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	res, err := r.members.DeleteMany(opCtx, bson.M{"head_coach_id": headID})
	if err != nil {
		return 0, err
	}
	if err := r.periods.CloseByParent(ctx, db.PeriodKindTeam, headID, periodReason(ctx, db.PeriodReasonDemote), time.Now()); err != nil {
		periodFailed("closeByParent", err, headID)
	}
	return res.DeletedCount, nil
}

// DeleteByAny removes every membership the user takes part in, as member or as head.
func (r *TeamRepository) DeleteByAny(ctx context.Context, userID string) (int64, error) {
	opCtx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	res, err := r.members.DeleteMany(opCtx, bson.M{"$or": bson.A{
		bson.M{"coach_id": userID},
		bson.M{"head_coach_id": userID},
	}})
	if err != nil {
		return 0, err
	}
	now := time.Now()
	reason := periodReason(ctx, db.PeriodReasonAccountGone)
	if err := r.periods.ClosePeriod(ctx, db.PeriodKindTeam, userID, reason, now); err != nil {
		periodFailed("closePeriod", err, userID)
	}
	if err := r.periods.CloseByParent(ctx, db.PeriodKindTeam, userID, reason, now); err != nil {
		periodFailed("closeByParent", err, userID)
	}

	// A head coach that goes away also leaves its gym.
	gymCtx, gymCancel := context.WithTimeout(ctx, repoOpTimeout)
	defer gymCancel()
	gres, err := r.gymHeads.DeleteOne(gymCtx, bson.M{"head_coach_id": userID})
	if err != nil {
		return res.DeletedCount, err
	}
	if gres.DeletedCount > 0 {
		if err := r.periods.ClosePeriod(ctx, db.PeriodKindGym, userID, reason, now); err != nil {
			periodFailed("closeGymPeriod", err, userID)
		}
	}
	return res.DeletedCount, nil
}

// GetHeadOf returns the membership of a coach, or nil when the coach has no team.
func (r *TeamRepository) GetHeadOf(ctx context.Context, coachID string) (*db.TeamMember, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	var m db.TeamMember
	err := r.members.FindOne(ctx, bson.M{"coach_id": coachID}).Decode(&m)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
}

func (r *TeamRepository) IsMember(ctx context.Context, coachID string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	n, err := r.members.CountDocuments(ctx, bson.M{"coach_id": coachID}, options.Count().SetLimit(1))
	return n > 0, err
}

func (r *TeamRepository) CountByHead(ctx context.Context, headID string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	// Same lookup+unwind as ListByHead, so memberships whose coach user is gone are not counted.
	pipeline := r.teamRowStages(bson.D{{Key: "head_coach_id", Value: headID}})
	pipeline = append(pipeline, bson.D{{Key: "$count", Value: "n"}})
	cursor, err := r.members.Aggregate(ctx, pipeline)
	if err != nil {
		return 0, err
	}
	defer cursor.Close(ctx)

	var out []struct {
		N int `bson:"n"`
	}
	if err := cursor.All(ctx, &out); err != nil {
		return 0, err
	}
	if len(out) == 0 {
		return 0, nil
	}
	return out[0].N, nil
}

func (r *TeamRepository) IDsByHead(ctx context.Context, headID string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	cursor, err := r.members.Find(ctx, bson.M{"head_coach_id": headID},
		options.Find().SetProjection(bson.M{"coach_id": 1}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var docs []struct {
		CoachID string `bson:"coach_id"`
	}
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(docs))
	for _, d := range docs {
		ids = append(ids, d.CoachID)
	}
	return ids, nil
}

// teamRowStages joins members with their user document (safe fields only) and flattens the row.
func (r *TeamRepository) teamRowStages(match bson.D) []bson.D {
	return []bson.D{
		{{Key: "$match", Value: match}},
		{{Key: "$lookup", Value: bson.D{
			{Key: "from", Value: r.usersName},
			{Key: "let", Value: bson.D{{Key: "cid", Value: "$coach_id"}}},
			{Key: "pipeline", Value: bson.A{
				bson.D{{Key: "$match", Value: bson.D{{Key: "$expr", Value: bson.D{{Key: "$eq", Value: bson.A{"$externalid", "$$cid"}}}}}}},
				bson.D{{Key: "$project", Value: bson.D{
					{Key: "externalid", Value: 1},
					{Key: "firstname", Value: 1},
					{Key: "lastname", Value: 1},
					{Key: "email", Value: 1},
					{Key: "profile_picture", Value: 1},
					{Key: "status", Value: 1},
					{Key: "last_login", Value: 1},
					{Key: "created_at", Value: 1},
				}}},
			}},
			{Key: "as", Value: "u"},
		}}},
		{{Key: "$unwind", Value: "$u"}},
		{{Key: "$project", Value: bson.D{
			{Key: "_id", Value: 0},
			{Key: "externalid", Value: "$u.externalid"},
			{Key: "firstname", Value: "$u.firstname"},
			{Key: "lastname", Value: "$u.lastname"},
			{Key: "email", Value: "$u.email"},
			{Key: "profile_picture", Value: "$u.profile_picture"},
			{Key: "status", Value: "$u.status"},
			{Key: "last_login", Value: "$u.last_login"},
			{Key: "created_at", Value: "$u.created_at"},
			{Key: "joined_team_at", Value: "$assigned_at"},
		}}},
	}
}

// clientStatsStages adds client_count / active_client_count / new_clients_30d to each row.
// A client counts as active when its user status is "Active"; new clients are links created after cutoff.
func (r *TeamRepository) clientStatsStages(cutoff time.Time) []bson.D {
	return []bson.D{
		{{Key: "$lookup", Value: bson.D{
			{Key: "from", Value: r.clientsName},
			{Key: "let", Value: bson.D{{Key: "cid", Value: "$externalid"}}},
			{Key: "pipeline", Value: bson.A{
				bson.D{{Key: "$match", Value: bson.D{{Key: "$expr", Value: bson.D{{Key: "$eq", Value: bson.A{"$coach_id", "$$cid"}}}}}}},
				bson.D{{Key: "$lookup", Value: bson.D{
					{Key: "from", Value: r.usersName},
					{Key: "let", Value: bson.D{{Key: "client", Value: "$client_id"}}},
					{Key: "pipeline", Value: bson.A{
						bson.D{{Key: "$match", Value: bson.D{{Key: "$expr", Value: bson.D{{Key: "$eq", Value: bson.A{"$externalid", "$$client"}}}}}}},
						bson.D{{Key: "$project", Value: bson.D{{Key: "status", Value: 1}}}},
					}},
					{Key: "as", Value: "cu"},
				}}},
				bson.D{{Key: "$group", Value: bson.D{
					{Key: "_id", Value: nil},
					{Key: "client_count", Value: bson.D{{Key: "$sum", Value: 1}}},
					{Key: "active_client_count", Value: bson.D{{Key: "$sum", Value: bson.D{{Key: "$cond", Value: bson.A{
						bson.D{{Key: "$in", Value: bson.A{"Active", "$cu.status"}}}, 1, 0}}}}}},
					{Key: "new_clients_30d", Value: bson.D{{Key: "$sum", Value: bson.D{{Key: "$cond", Value: bson.A{
						bson.D{{Key: "$gte", Value: bson.A{"$created_at", cutoff}}}, 1, 0}}}}}},
				}}},
			}},
			{Key: "as", Value: "cstats"},
		}}},
		{{Key: "$addFields", Value: bson.D{
			{Key: "client_count", Value: bson.D{{Key: "$ifNull", Value: bson.A{bson.D{{Key: "$first", Value: "$cstats.client_count"}}, 0}}}},
			{Key: "active_client_count", Value: bson.D{{Key: "$ifNull", Value: bson.A{bson.D{{Key: "$first", Value: "$cstats.active_client_count"}}, 0}}}},
			{Key: "new_clients_30d", Value: bson.D{{Key: "$ifNull", Value: bson.A{bson.D{{Key: "$first", Value: "$cstats.new_clients_30d"}}, 0}}}},
		}}},
		{{Key: "$project", Value: bson.D{{Key: "cstats", Value: 0}}}},
	}
}

// ListByHead returns one page of a team's coaches with client stats, in a single aggregation.
// Pagination happens before the client lookup, except for sort=clients which needs the counts first.
func (r *TeamRepository) ListByHead(ctx context.Context, headID string, q db.TeamListQuery) ([]db.TeamCoachRow, int, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()

	if q.Page < 1 {
		q.Page = 1
	}
	if q.Size < 1 {
		q.Size = 20
	}
	cutoff := time.Now().AddDate(0, 0, -30)

	pipeline := r.teamRowStages(bson.D{{Key: "head_coach_id", Value: headID}})

	filter := bson.D{}
	if q.Status != "" {
		filter = append(filter, bson.E{Key: "status", Value: q.Status})
	}
	if q.Q != "" {
		rx := bson.D{{Key: "$regex", Value: regexp.QuoteMeta(q.Q)}, {Key: "$options", Value: "i"}}
		filter = append(filter, bson.E{Key: "$or", Value: bson.A{
			bson.D{{Key: "firstname", Value: rx}},
			bson.D{{Key: "lastname", Value: rx}},
			bson.D{{Key: "email", Value: rx}},
		}})
	}
	if len(filter) > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: filter}})
	}

	var sortDoc bson.D
	switch q.Sort {
	case "clients":
		sortDoc = bson.D{{Key: "client_count", Value: -1}, {Key: "externalid", Value: 1}}
		pipeline = append(pipeline, r.clientStatsStages(cutoff)...)
	case "last_login":
		sortDoc = bson.D{{Key: "last_login", Value: -1}, {Key: "externalid", Value: 1}}
	default:
		sortDoc = bson.D{{Key: "firstname", Value: 1}, {Key: "lastname", Value: 1}, {Key: "externalid", Value: 1}}
	}

	dataStages := bson.A{
		bson.D{{Key: "$sort", Value: sortDoc}},
		bson.D{{Key: "$skip", Value: int64((q.Page - 1) * q.Size)}},
		bson.D{{Key: "$limit", Value: int64(q.Size)}},
	}
	if q.Sort != "clients" {
		for _, st := range r.clientStatsStages(cutoff) {
			dataStages = append(dataStages, st)
		}
	}
	pipeline = append(pipeline, bson.D{{Key: "$facet", Value: bson.D{
		{Key: "total", Value: bson.A{bson.D{{Key: "$count", Value: "n"}}}},
		{Key: "data", Value: dataStages},
	}}})

	cursor, err := r.members.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	var out []struct {
		Total []struct {
			N int `bson:"n"`
		} `bson:"total"`
		Data []db.TeamCoachRow `bson:"data"`
	}
	if err := cursor.All(ctx, &out); err != nil {
		return nil, 0, err
	}
	if len(out) == 0 {
		return []db.TeamCoachRow{}, 0, nil
	}
	total := 0
	if len(out[0].Total) > 0 {
		total = out[0].Total[0].N
	}
	rows := out[0].Data
	if rows == nil {
		rows = []db.TeamCoachRow{}
	}
	return rows, total, nil
}

// GetRow returns one team coach row (with client stats), or nil when the coach is not in that team.
func (r *TeamRepository) GetRow(ctx context.Context, headID, coachID string) (*db.TeamCoachRow, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()

	pipeline := r.teamRowStages(bson.D{{Key: "head_coach_id", Value: headID}, {Key: "coach_id", Value: coachID}})
	pipeline = append(pipeline, r.clientStatsStages(time.Now().AddDate(0, 0, -30))...)

	cursor, err := r.members.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var rows []db.TeamCoachRow
	if err := cursor.All(ctx, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// Overview aggregates a team's coaches and clients in a single pipeline.
func (r *TeamRepository) Overview(ctx context.Context, headID string) (*db.TeamOverview, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()

	now := time.Now()
	inactiveCutoff := now.AddDate(0, 0, -14)

	pipeline := r.teamRowStages(bson.D{{Key: "head_coach_id", Value: headID}})
	pipeline = append(pipeline, r.clientStatsStages(now.AddDate(0, 0, -30))...)
	pipeline = append(pipeline, bson.D{{Key: "$group", Value: bson.D{
		{Key: "_id", Value: nil},
		{Key: "team_size", Value: bson.D{{Key: "$sum", Value: 1}}},
		{Key: "active_coaches", Value: bson.D{{Key: "$sum", Value: bson.D{{Key: "$cond", Value: bson.A{
			bson.D{{Key: "$eq", Value: bson.A{"$status", "Active"}}}, 1, 0}}}}}},
		{Key: "inactive_coaches_14d", Value: bson.D{{Key: "$sum", Value: bson.D{{Key: "$cond", Value: bson.A{
			bson.D{{Key: "$lt", Value: bson.A{bson.D{{Key: "$ifNull", Value: bson.A{"$last_login", time.Time{}}}}, inactiveCutoff}}}, 1, 0}}}}}},
		{Key: "total_clients", Value: bson.D{{Key: "$sum", Value: "$client_count"}}},
		{Key: "active_clients", Value: bson.D{{Key: "$sum", Value: "$active_client_count"}}},
		{Key: "new_clients_30d", Value: bson.D{{Key: "$sum", Value: "$new_clients_30d"}}},
	}}})

	cursor, err := r.members.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var out []db.TeamOverview
	if err := cursor.All(ctx, &out); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return &db.TeamOverview{}, nil
	}
	return &out[0], nil
}

func (r *TeamRepository) InsertAudit(ctx context.Context, e *db.TeamAuditEntry) error {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	_, err := r.audit.InsertOne(ctx, e)
	return err
}

func (r *TeamRepository) InsertAuditMany(ctx context.Context, entries []*db.TeamAuditEntry) error {
	if len(entries) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	docs := make([]interface{}, 0, len(entries))
	now := time.Now()
	for _, e := range entries {
		if e.CreatedAt.IsZero() {
			e.CreatedAt = now
		}
		docs = append(docs, e)
	}
	_, err := r.audit.InsertMany(ctx, docs)
	return err
}

// ListAudit returns audit entries newest first, optionally filtered by head coach and/or coach.
func (r *TeamRepository) ListAudit(ctx context.Context, headCoachID, coachID string, page, size int) ([]db.TeamAuditEntry, int, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()

	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	filter := bson.M{}
	if headCoachID != "" {
		filter["head_coach_id"] = headCoachID
	}
	if coachID != "" {
		filter["coach_id"] = coachID
	}

	total, err := r.audit.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	cursor, err := r.audit.Find(ctx, filter, options.Find().
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
