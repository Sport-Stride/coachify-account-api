package repositories

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"

	"coachify-account-api/models/db"
)

// AnalyticsRepository reads the data behind the analytics scope endpoints.
type AnalyticsRepository struct {
	team *TeamRepository
	gyms *GymRepository
}

func NewAnalyticsRepository(team *TeamRepository, gyms *GymRepository) *AnalyticsRepository {
	return &AnalyticsRepository{team: team, gyms: gyms}
}

func (r *AnalyticsRepository) GetOwnedGym(ctx context.Context, gymID, ownerID string) (*db.Gym, error) {
	return r.gyms.GetOwnedGym(ctx, gymID, ownerID)
}

// GymHeadPeriods returns every period (open or closed) of the head coaches of a gym.
func (r *AnalyticsRepository) GymHeadPeriods(ctx context.Context, gymID string) ([]db.MembershipPeriod, error) {
	return r.team.periods.ListForParent(ctx, db.PeriodKindGym, gymID)
}

// TeamPeriodsByHeads returns every team period of the given head coaches in one query.
func (r *AnalyticsRepository) TeamPeriodsByHeads(ctx context.Context, headIDs []string) ([]db.MembershipPeriod, error) {
	return r.team.periods.ListForParents(ctx, db.PeriodKindTeam, headIDs)
}

// UserIdentities maps user ids to names and dates (names only, nothing else leaves the users collection).
func (r *AnalyticsRepository) UserIdentities(ctx context.Context, ids []string) (map[string]db.UserIdentity, error) {
	out := make(map[string]db.UserIdentity, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	cursor, err := r.team.users.Find(ctx, bson.M{"externalid": bson.M{"$in": ids}},
		options.Find().SetProjection(bson.M{"externalid": 1, "firstname": 1, "lastname": 1, "created_at": 1, "head_coach_since": 1}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var docs []db.UserIdentity
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, err
	}
	for _, d := range docs {
		out[d.ExternalID] = d
	}
	return out, nil
}

// CoachIdentities returns up to limit users with role coach.
func (r *AnalyticsRepository) CoachIdentities(ctx context.Context, limit int) ([]db.UserIdentity, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	cursor, err := r.team.users.Find(ctx, bson.M{"role": "coach"},
		options.Find().
			SetProjection(bson.M{"externalid": 1, "firstname": 1, "lastname": 1, "created_at": 1}).
			SetSort(bson.D{{Key: "created_at", Value: 1}, {Key: "externalid", Value: 1}}).
			SetLimit(int64(limit)))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	out := []db.UserIdentity{}
	if err := cursor.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ClientsOfCoaches returns one page of the clients linked to the given coaches (safe fields only),
// in a single aggregation: coach_clients -> $lookup users -> $facet total/data.
func (r *AnalyticsRepository) ClientsOfCoaches(ctx context.Context, coachIDs []string, f db.ScopeClientFilter) ([]db.ClientRowLinked, int, error) {
	if len(coachIDs) == 0 {
		return []db.ClientRowLinked{}, 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	f.Page, f.Size = pageDefaults(f.Page, f.Size)

	match := bd("coach_id", bd("$in", coachIDs))
	if len(f.ClientIDs) > 0 {
		match = append(match, bson.E{Key: "client_id", Value: bd("$in", f.ClientIDs)})
	}
	pipeline := []bson.D{
		{{Key: "$match", Value: match}},
		{{Key: "$lookup", Value: bd(
			"from", r.team.usersName,
			"let", bd("client", "$client_id"),
			"pipeline", bson.A{
				bd("$match", bd("$expr", bd("$eq", bson.A{"$externalid", "$$client"}))),
				bd("$project", bd("externalid", 1, "firstname", 1, "lastname", 1, "email", 1, "profile_picture", 1, "status", 1)),
			},
			"as", "u",
		)}},
		{{Key: "$unwind", Value: "$u"}},
		{{Key: "$lookup", Value: bd(
			"from", r.team.usersName,
			"let", bd("cid", "$coach_id"),
			"pipeline", bson.A{
				bd("$match", bd("$expr", bd("$eq", bson.A{"$externalid", "$$cid"}))),
				bd("$project", bd("firstname", 1, "lastname", 1)),
			},
			"as", "co",
		)}},
		{{Key: "$project", Value: bd(
			"_id", 0,
			"externalid", "$u.externalid",
			"firstname", "$u.firstname",
			"lastname", "$u.lastname",
			"email", "$u.email",
			"profile_picture", "$u.profile_picture",
			"status", "$u.status",
			"coach_id", 1,
			"coach_name", fullName("$co.firstname", "$co.lastname"),
			"linked_at", "$created_at",
		)}},
	}
	if f.Q != "" {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: nameRegex(f.Q, "firstname", "lastname", "email")}})
	}
	sortDoc := bd("firstname", 1, "lastname", 1, "externalid", 1, "coach_id", 1)
	if f.Sort == "linked_at" {
		sortDoc = bd("linked_at", -1, "externalid", 1, "coach_id", 1)
	}
	pipeline = append(pipeline, facetPage(bson.A{
		bd("$sort", sortDoc),
		bd("$skip", int64((f.Page-1)*f.Size)),
		bd("$limit", int64(f.Size)),
	}))
	return runPaged[db.ClientRowLinked](ctx, r.team.clients, pipeline)
}
