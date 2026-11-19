package repositories

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"coachify-account-api/models/db"
)

// GymDispatchRepository runs the gym client dispatch writes as single Mongo transactions: the gym_clients change
// and its coach_team_audit rows commit together or not at all. Transactions need a replica set (or mongos).
//
// membership_periods of kind client_coach are deliberately not written: the open-period unique index
// idx_periods_open_unique is (kind, member_id) with no gym, so a client held by coaches in several gyms would
// collide. gym_clients.assigned_at plus the audit rows carry the history.
type GymDispatchRepository struct {
	database *mongo.Database
	clients  *mongo.Collection
	audit    *mongo.Collection
}

func NewGymDispatchRepository(database *mongo.Database) *GymDispatchRepository {
	return &GymDispatchRepository{
		database: database,
		clients:  database.Collection(gymClientsCollection),
		audit:    database.Collection(teamAuditCollection),
	}
}

// supportsTransactions reports whether a hello reply comes from a replica set member or a mongos.
func supportsTransactions(hello bson.M) bool {
	if name, ok := hello["setName"].(string); ok && name != "" {
		return true
	}
	msg, _ := hello["msg"].(string)
	return msg == "isdbgrid"
}

// SupportsTransactions detects whether the deployment accepts multi-document transactions.
func (r *GymDispatchRepository) SupportsTransactions(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	var hello bson.M
	if err := r.database.RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err != nil {
		return false, err
	}
	return supportsTransactions(hello), nil
}

// inTx runs fn in a transaction with the repository deadline.
func (r *GymDispatchRepository) inTx(ctx context.Context, fn func(sctx mongo.SessionContext) (interface{}, error)) (interface{}, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, err
	}
	defer session.EndSession(ctx)
	return session.WithTransaction(ctx, fn)
}

func stamp(e *db.TeamAuditEntry, at time.Time) {
	if e != nil && e.CreatedAt.IsZero() {
		e.CreatedAt = at
	}
}

// AssignTx sets the coach of an active gym client (compare-and-set on the previous coach, "" = pool) and writes the
// audit row in one transaction. False when the compare-and-set matched nothing; nothing is written then.
func (r *GymDispatchRepository) AssignTx(ctx context.Context, gymID, clientID, prevCoachID, coachID string, at time.Time, audit *db.TeamAuditEntry) (bool, error) {
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
	stamp(audit, at)
	return r.txWrite(ctx, r.clients, filter, update, audit)
}

// LeaveTx marks the active gym client relationship left and writes the audit row in one transaction. False when
// there was no active relationship.
func (r *GymDispatchRepository) LeaveTx(ctx context.Context, gymID, clientID string, at time.Time, audit *db.TeamAuditEntry) (bool, error) {
	stamp(audit, at)
	return r.txWrite(ctx, r.clients,
		bson.M{"gym_id": gymID, "client_id": clientID, "status": db.GymClientStatusActive},
		bson.M{"$set": bson.M{"status": db.GymClientStatusLeft, "left_at": at}}, audit)
}

// txWrite updates one document and inserts the audit row in a transaction; no match writes nothing.
func (r *GymDispatchRepository) txWrite(ctx context.Context, coll *mongo.Collection, filter, update bson.M, audit *db.TeamAuditEntry) (bool, error) {
	out, err := r.inTx(ctx, func(sctx mongo.SessionContext) (interface{}, error) {
		res, err := coll.UpdateOne(sctx, filter, update)
		if err != nil || res.MatchedCount == 0 {
			return false, err
		}
		if audit != nil {
			if _, err := r.audit.InsertOne(sctx, audit); err != nil {
				return nil, err
			}
		}
		return true, nil
	})
	if err != nil {
		return false, err
	}
	ok, _ := out.(bool)
	return ok, nil
}

// ReleaseCoachTx returns every active gym client of the coaches to the pool (one find, one update many and one audit
// insert many in a single transaction, not one round trip per client). gymID "" covers every gym. It returns the
// released rows, which are exactly the ones audited.
func (r *GymDispatchRepository) ReleaseCoachTx(ctx context.Context, coachIDs []string, gymID string, at time.Time, audit func(row db.GymClient) *db.TeamAuditEntry) ([]db.GymClient, error) {
	filter := bson.M{"coach_id": bson.M{"$in": coachIDs}, "status": db.GymClientStatusActive}
	if gymID != "" {
		filter["gym_id"] = gymID
	}
	out, err := r.inTx(ctx, func(sctx mongo.SessionContext) (interface{}, error) {
		cur, err := r.clients.Find(sctx, filter)
		if err != nil {
			return nil, err
		}
		var rows []db.GymClient
		if err := cur.All(sctx, &rows); err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return rows, nil
		}
		ids := make(bson.A, 0, len(rows))
		entries := make([]interface{}, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
			e := audit(row)
			stamp(e, at)
			entries = append(entries, e)
		}
		if _, err := r.clients.UpdateMany(sctx, bson.M{"_id": bson.M{"$in": ids}},
			bson.M{"$set": bson.M{"coach_id": ""}, "$unset": bson.M{"assigned_at": ""}}); err != nil {
			return nil, err
		}
		if _, err := r.audit.InsertMany(sctx, entries); err != nil {
			return nil, err
		}
		return rows, nil
	})
	if err != nil {
		return nil, err
	}
	rows, _ := out.([]db.GymClient)
	return rows, nil
}
