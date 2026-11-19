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

const gymClientsCollection = "gym_clients"

// ErrGymClientExists is returned when the client already has an active relationship with the gym.
var ErrGymClientExists = errors.New("client already belongs to the gym")

// GymClientRepository persists gym client relationships (collection gym_clients). It never reads coach_clients:
// personal relationships are invisible to the gym.
type GymClientRepository struct {
	clients   *mongo.Collection
	gyms      *mongo.Collection
	usersName string
}

func NewGymClientRepository(database *mongo.Database, usersColl string) *GymClientRepository {
	return &GymClientRepository{
		clients:   database.Collection(gymClientsCollection),
		gyms:      database.Collection(gymsCollection),
		usersName: usersColl,
	}
}

func gymClientIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{
		{
			Keys: bson.D{{Key: "gym_id", Value: 1}, {Key: "client_id", Value: 1}},
			Options: options.Index().
				SetUnique(true).
				SetPartialFilterExpression(bson.M{"status": string(db.GymClientStatusActive)}).
				SetName("idx_gym_clients_gym_client_unique"),
		},
		{
			Keys:    bson.D{{Key: "client_id", Value: 1}, {Key: "status", Value: 1}},
			Options: options.Index().SetName("idx_gym_clients_client_status"),
		},
		{
			Keys:    bson.D{{Key: "gym_id", Value: 1}, {Key: "coach_id", Value: 1}, {Key: "joined_at", Value: -1}},
			Options: options.Index().SetName("idx_gym_clients_gym_coach_joined"),
		},
		// Coach-side reads (client lists, link check, team stats) match coach_id + status across gyms.
		{
			Keys:    bson.D{{Key: "coach_id", Value: 1}, {Key: "status", Value: 1}, {Key: "joined_at", Value: -1}},
			Options: options.Index().SetName("idx_gym_clients_coach_status_joined"),
		},
	}
}

// poolFilter matches an unassigned client: coach_id is "" (or, defensively, missing).
var poolFilter = bson.M{"$in": bson.A{"", nil}}

// Insert creates an active relationship. ErrGymClientExists when one is already active.
func (r *GymClientRepository) Insert(ctx context.Context, c *db.GymClient) error {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	if _, err := r.clients.InsertOne(ctx, c); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return ErrGymClientExists
		}
		return err
	}
	return nil
}

// Get returns the active relationship of a client with a gym, or nil.
func (r *GymClientRepository) Get(ctx context.Context, gymID, clientID string) (*db.GymClient, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	var c db.GymClient
	err := r.clients.FindOne(ctx, bson.M{"gym_id": gymID, "client_id": clientID, "status": db.GymClientStatusActive}).Decode(&c)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

// ActiveByClient lists every active gym relationship of a client.
func (r *GymClientRepository) ActiveByClient(ctx context.Context, clientID string) ([]db.GymClient, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	cursor, err := r.clients.Find(ctx, bson.M{"client_id": clientID, "status": db.GymClientStatusActive})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	out := []db.GymClient{}
	if err := cursor.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// SetCoach is a compare-and-set of the coach of an active relationship: it matches only while the coach is still
// prevCoachID ("" = pool). It returns false when nothing matched (the row changed or left). An empty coachID
// moves the client back to the pool.
func (r *GymClientRepository) SetCoach(ctx context.Context, gymID, clientID, prevCoachID, coachID string, at time.Time) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	filter := bson.M{"gym_id": gymID, "client_id": clientID, "status": db.GymClientStatusActive}
	if prevCoachID == "" {
		filter["coach_id"] = poolFilter
	} else {
		filter["coach_id"] = prevCoachID
	}
	update := bson.M{"$set": bson.M{"coach_id": coachID, "assigned_at": at}}
	if coachID == "" {
		update = bson.M{"$set": bson.M{"coach_id": ""}, "$unset": bson.M{"assigned_at": ""}}
	}
	res, err := r.clients.UpdateOne(ctx, filter, update)
	if err != nil {
		return false, err
	}
	return res.MatchedCount > 0, nil
}

// Leave marks the active relationship left. False when there was none.
func (r *GymClientRepository) Leave(ctx context.Context, gymID, clientID string, at time.Time) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	res, err := r.clients.UpdateOne(ctx,
		bson.M{"gym_id": gymID, "client_id": clientID, "status": db.GymClientStatusActive},
		bson.M{"$set": bson.M{"status": db.GymClientStatusLeft, "left_at": at}})
	if err != nil {
		return false, err
	}
	return res.MatchedCount > 0, nil
}

// CountActive counts the active gym clients of a gym, assigned or not (each client once).
func (r *GymClientRepository) CountActive(ctx context.Context, gymID string) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	return r.clients.CountDocuments(ctx, bson.M{"gym_id": gymID, "status": db.GymClientStatusActive})
}

// MaxGymClientsOverride returns the admin override of the gym client cap, or nil when none is set.
func (r *GymClientRepository) MaxGymClientsOverride(ctx context.Context, gymID string) (*int, error) {
	oid, ok := gymOID(gymID)
	if !ok {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	var g struct {
		Max *int `bson:"max_gym_clients_override"`
	}
	err := r.gyms.FindOne(ctx, bson.M{"_id": oid}, options.FindOne().SetProjection(bson.M{"max_gym_clients_override": 1})).Decode(&g)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	return g.Max, nil
}

// ListByGym returns one page of the gym's active clients (safe fields only), one aggregation over gym_clients.
func (r *GymClientRepository) ListByGym(ctx context.Context, gymID string, q db.GymClientQuery) ([]db.GymClientRow, int, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	return runPaged[db.GymClientRow](ctx, r.clients, gymClientRowPipeline(r.usersName, gymID, q))
}

// gymClientRowPipeline builds the gym client list over gym_clients. It is the only source of the gym and head coach
// client views: it never touches coach_clients, so a personal-only client cannot appear in it.
func gymClientRowPipeline(usersName, gymID string, q db.GymClientQuery) []bson.D {
	q.Page, q.Size = pageDefaults(q.Page, q.Size)

	conds := bson.A{bd("gym_id", gymID, "status", string(db.GymClientStatusActive))}
	if q.CoachID != "" {
		conds = append(conds, bd("coach_id", q.CoachID))
	}
	if q.CoachIDs != nil {
		conds = append(conds, bd("coach_id", bd("$in", q.CoachIDs)))
	}
	if q.Assigned != nil {
		if *q.Assigned {
			conds = append(conds, bd("coach_id", bd("$nin", bson.A{"", nil})))
		} else {
			conds = append(conds, bd("coach_id", poolFilter))
		}
	}
	pipeline := []bson.D{
		{{Key: "$match", Value: bd("$and", conds)}},
		{{Key: "$lookup", Value: bd(
			"from", usersName,
			"let", bd("client", "$client_id"),
			"pipeline", bson.A{
				bd("$match", bd("$expr", bd("$eq", bson.A{"$externalid", "$$client"}))),
				bd("$project", bd("externalid", 1, "firstname", 1, "lastname", 1, "email", 1, "profile_picture", 1, "status", 1)),
			},
			"as", "u",
		)}},
		{{Key: "$unwind", Value: "$u"}},
		{{Key: "$lookup", Value: bd(
			"from", usersName,
			"let", bd("cid", "$coach_id"),
			"pipeline", bson.A{
				bd("$match", bd("$expr", bd("$eq", bson.A{"$externalid", "$$cid"}))),
				bd("$project", bd("firstname", 1, "lastname", 1)),
			},
			"as", "co",
		)}},
		{{Key: "$lookup", Value: bd("from", teamMembersCollection, "localField", "coach_id", "foreignField", "coach_id", "as", "tm")}},
		{{Key: "$lookup", Value: bd("from", gymHeadCoachesCollection, "localField", "coach_id", "foreignField", "head_coach_id", "as", "hc")}},
		// The head coach of a client's coach: the team head, or the coach himself when he is a head coach.
		{{Key: "$addFields", Value: bd("head_id", bd("$ifNull", bson.A{
			bd("$first", "$tm.head_coach_id"),
			bd("$cond", bson.A{bd("$gt", bson.A{bd("$size", "$hc"), 0}), "$coach_id", ""}),
		}))}},
	}
	if q.HeadCoachID != "" {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: bd("head_id", q.HeadCoachID)}})
	}
	pipeline = append(pipeline, bson.D{{Key: "$project", Value: bd(
		"_id", 0,
		"externalid", "$u.externalid",
		"firstname", "$u.firstname",
		"lastname", "$u.lastname",
		"email", "$u.email",
		"profile_picture", "$u.profile_picture",
		"status", "$u.status",
		"coach_id", bd("$ifNull", bson.A{"$coach_id", ""}),
		"coach_name", fullName("$co.firstname", "$co.lastname"),
		"head_coach_id", "$head_id",
		"linked_at", "$joined_at",
	)}})
	if q.Q != "" {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: nameRegex(q.Q, "firstname", "lastname", "email")}})
	}
	sortDoc := bd("firstname", 1, "lastname", 1, "externalid", 1)
	if q.Sort == "linked_at" {
		sortDoc = bd("linked_at", -1, "externalid", 1)
	}
	return append(pipeline, facetPage(bson.A{
		bd("$sort", sortDoc),
		bd("$skip", int64((q.Page-1)*q.Size)),
		bd("$limit", int64(q.Size)),
	}))
}

// activeGymClientsOf is the $match of a gym_clients sub-pipeline: the active gym clients of the coach in the let variable.
func activeGymClientsOf(coachVar string) bson.D {
	return bd("$match", bd("$expr", bd("$and", bson.A{
		bd("$eq", bson.A{"$coach_id", coachVar}),
		bd("$eq", bson.A{"$status", string(db.GymClientStatusActive)}),
	})))
}
