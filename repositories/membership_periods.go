package repositories

import (
	"context"
	"errors"
	"sort"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"

	"coachify-account-api/models/db"
)

const (
	membershipPeriodsCollection = "membership_periods"
	reconcileBatch              = 500
)

type periodReasonKey struct{}

// WithPeriodReason overrides the default reason recorded by team mutations that close a period.
func WithPeriodReason(ctx context.Context, reason string) context.Context {
	return context.WithValue(ctx, periodReasonKey{}, reason)
}

func periodReason(ctx context.Context, def string) string {
	if r, ok := ctx.Value(periodReasonKey{}).(string); ok && r != "" {
		return r
	}
	return def
}

// MembershipPeriodRepository stores the history of memberships (team now, gym later).
type MembershipPeriodRepository struct {
	periods *mongo.Collection
	members *mongo.Collection
}

func NewMembershipPeriodRepository(database *mongo.Database) *MembershipPeriodRepository {
	return &MembershipPeriodRepository{
		periods: database.Collection(membershipPeriodsCollection),
		members: database.Collection(teamMembersCollection),
	}
}

// OpenForMember returns the open period of a member, or nil.
func (r *MembershipPeriodRepository) OpenForMember(ctx context.Context, kind, memberID string) (*db.MembershipPeriod, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	var p db.MembershipPeriod
	err := r.periods.FindOne(ctx, bson.M{"kind": kind, "member_id": memberID, "end": nil}).Decode(&p)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

// OpenPeriod opens a period. No-op when one is already open for the same parent;
// an open period for another parent is closed first.
func (r *MembershipPeriodRepository) OpenPeriod(ctx context.Context, kind, memberID, parentID, reason string, at time.Time) error {
	cur, err := r.OpenForMember(ctx, kind, memberID)
	if err != nil {
		return err
	}
	if cur != nil {
		if cur.ParentID == parentID {
			return nil
		}
		if err := r.ClosePeriod(ctx, kind, memberID, reason, at); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	_, err = r.periods.InsertOne(ctx, &db.MembershipPeriod{
		Kind: kind, MemberID: memberID, ParentID: parentID,
		Start: at.UTC(), Reason: reason,
	})
	if mongo.IsDuplicateKeyError(err) {
		return nil // a concurrent writer opened it first
	}
	return err
}

// ClosePeriod closes the open period of a member; no-op when none is open.
func (r *MembershipPeriodRepository) ClosePeriod(ctx context.Context, kind, memberID, reason string, at time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	_, err := r.periods.UpdateOne(ctx,
		bson.M{"kind": kind, "member_id": memberID, "end": nil},
		bson.M{"$set": bson.M{"end": at.UTC(), "reason": reason}})
	return err
}

// CloseByParent closes every open period of a parent.
func (r *MembershipPeriodRepository) CloseByParent(ctx context.Context, kind, parentID, reason string, at time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	_, err := r.periods.UpdateMany(ctx,
		bson.M{"kind": kind, "parent_id": parentID, "end": nil},
		bson.M{"$set": bson.M{"end": at.UTC(), "reason": reason}})
	return err
}

func (r *MembershipPeriodRepository) list(ctx context.Context, filter bson.M) ([]db.MembershipPeriod, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	cursor, err := r.periods.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "start", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	out := []db.MembershipPeriod{}
	if err := cursor.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListForParent returns every period of a parent sorted by start.
func (r *MembershipPeriodRepository) ListForParent(ctx context.Context, kind, parentID string) ([]db.MembershipPeriod, error) {
	return r.list(ctx, bson.M{"kind": kind, "parent_id": parentID})
}

// ListForParents returns every period of several parents sorted by start, in one query.
func (r *MembershipPeriodRepository) ListForParents(ctx context.Context, kind string, parentIDs []string) ([]db.MembershipPeriod, error) {
	if len(parentIDs) == 0 {
		return []db.MembershipPeriod{}, nil
	}
	return r.list(ctx, bson.M{"kind": kind, "parent_id": bson.M{"$in": parentIDs}})
}

// ListForMember returns every period of a member sorted by start.
func (r *MembershipPeriodRepository) ListForMember(ctx context.Context, kind, memberID string) ([]db.MembershipPeriod, error) {
	return r.list(ctx, bson.M{"kind": kind, "member_id": memberID})
}

// GroupPeriodsByMember groups periods (already sorted by start) per member, keeping first-seen member order.
func GroupPeriodsByMember(ps []db.MembershipPeriod) []db.CoachPeriods {
	idx := map[string]int{}
	out := []db.CoachPeriods{}
	for _, p := range ps {
		i, ok := idx[p.MemberID]
		if !ok {
			i = len(out)
			idx[p.MemberID] = i
			out = append(out, db.CoachPeriods{CoachID: p.MemberID, Periods: []db.PeriodRange{}})
		}
		out[i].Periods = append(out[i].Periods, db.PeriodRange{From: p.Start, To: p.End})
	}
	for i := range out {
		sort.SliceStable(out[i].Periods, func(a, b int) bool { return out[i].Periods[a].From.Before(out[i].Periods[b].From) })
	}
	return out
}

// ReconcileTeam aligns team periods with coach_team_members. Idempotent. Returns counts of opened and closed periods.
func (r *MembershipPeriodRepository) ReconcileTeam(ctx context.Context) (opened, closed int, err error) {
	now := time.Now().UTC()

	// 1. Membership rows without a matching open period.
	opCtx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	cur, err := r.members.Find(opCtx, bson.M{}, options.Find().SetBatchSize(reconcileBatch))
	if err != nil {
		cancel()
		return opened, closed, err
	}
	batch := make([]db.TeamMember, 0, reconcileBatch)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		defer func() { batch = batch[:0] }()
		ids := make([]string, 0, len(batch))
		for _, m := range batch {
			ids = append(ids, m.CoachID)
		}
		open, err := r.openByMembers(ctx, db.PeriodKindTeam, ids)
		if err != nil {
			return err
		}
		for _, m := range batch {
			if p, ok := open[m.CoachID]; ok && p.ParentID == m.HeadCoachID {
				continue
			}
			if err := r.OpenPeriod(ctx, db.PeriodKindTeam, m.CoachID, m.HeadCoachID, db.PeriodReasonBackfill, m.AssignedAt); err != nil {
				return err
			}
			opened++
		}
		return nil
	}
	for cur.Next(opCtx) {
		var m db.TeamMember
		if err := cur.Decode(&m); err != nil {
			cur.Close(opCtx)
			cancel()
			return opened, closed, err
		}
		batch = append(batch, m)
		if len(batch) >= reconcileBatch {
			if err := flush(); err != nil {
				cur.Close(opCtx)
				cancel()
				return opened, closed, err
			}
		}
	}
	curErr := cur.Err()
	cur.Close(opCtx)
	cancel()
	if curErr != nil {
		return opened, closed, curErr
	}
	if err := flush(); err != nil {
		return opened, closed, err
	}

	// 2. Open team periods whose membership row is gone.
	opCtx, cancel = context.WithTimeout(ctx, repoOpTimeout)
	pc, err := r.periods.Find(opCtx, bson.M{"kind": db.PeriodKindTeam, "end": nil}, options.Find().SetBatchSize(reconcileBatch))
	if err != nil {
		cancel()
		return opened, closed, err
	}
	var orphanCandidates []db.MembershipPeriod
	for pc.Next(opCtx) {
		var p db.MembershipPeriod
		if err := pc.Decode(&p); err != nil {
			pc.Close(opCtx)
			cancel()
			return opened, closed, err
		}
		orphanCandidates = append(orphanCandidates, p)
	}
	pcErr := pc.Err()
	pc.Close(opCtx)
	cancel()
	if pcErr != nil {
		return opened, closed, pcErr
	}
	for start := 0; start < len(orphanCandidates); start += reconcileBatch {
		end := start + reconcileBatch
		if end > len(orphanCandidates) {
			end = len(orphanCandidates)
		}
		chunk := orphanCandidates[start:end]
		ids := make([]string, 0, len(chunk))
		for _, p := range chunk {
			ids = append(ids, p.MemberID)
		}
		existing, err := r.existingCoaches(ctx, ids)
		if err != nil {
			return opened, closed, err
		}
		for _, p := range chunk {
			if _, ok := existing[p.MemberID]; ok {
				continue // parent mismatches were already fixed in step 1
			}
			if err := r.ClosePeriod(ctx, db.PeriodKindTeam, p.MemberID, db.PeriodReasonReconcile, now); err != nil {
				return opened, closed, err
			}
			closed++
		}
	}
	zap.L().Info("membership periods reconciled",
		zap.String("component", "repository"),
		zap.Int("opened", opened),
		zap.Int("closed", closed),
	)
	return opened, closed, nil
}

func (r *MembershipPeriodRepository) openByMembers(ctx context.Context, kind string, ids []string) (map[string]db.MembershipPeriod, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	cursor, err := r.periods.Find(ctx, bson.M{"kind": kind, "member_id": bson.M{"$in": ids}, "end": nil})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var ps []db.MembershipPeriod
	if err := cursor.All(ctx, &ps); err != nil {
		return nil, err
	}
	out := make(map[string]db.MembershipPeriod, len(ps))
	for _, p := range ps {
		out[p.MemberID] = p
	}
	return out, nil
}

// existingCoaches maps coach_id to head_coach_id for the given coaches that still have a membership row.
func (r *MembershipPeriodRepository) existingCoaches(ctx context.Context, ids []string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, repoOpTimeout)
	defer cancel()
	cursor, err := r.members.Find(ctx, bson.M{"coach_id": bson.M{"$in": ids}})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var ms []db.TeamMember
	if err := cursor.All(ctx, &ms); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(ms))
	for _, m := range ms {
		out[m.CoachID] = m.HeadCoachID
	}
	return out, nil
}
