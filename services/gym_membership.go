package services

import (
	"context"
	"strings"
	"time"

	"go.uber.org/zap"

	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
)

// GymMembersReadNew selects the gym_members read path; any other GYM_MEMBERS_READ value means legacy.
const GymMembersReadNew = "new"

// GymMembershipReader answers "which gyms is this user in, and as what". Only active memberships are returned.
// MembershipOf returns nil, nil when the user is not an active member of the gym.
type GymMembershipReader interface {
	MembershipsOf(ctx context.Context, userID string) ([]db.GymMember, error)
	MembershipOf(ctx context.Context, userID, gymID string) (*db.GymMember, error)
}

// GymMemberStore is the part of the gym member repository the services need.
type GymMemberStore interface {
	UpsertActive(ctx context.Context, m *db.GymMember) error
	MarkLeft(ctx context.Context, gymID, userID string, at time.Time) (bool, error)
	MarkLeftCoach(ctx context.Context, userID string, at time.Time) (bool, error)
	MarkLeftBySupervisor(ctx context.Context, gymID, supervisorID string, at time.Time) (int64, error)
	MarkLeftHead(ctx context.Context, userID string, at time.Time) (bool, error)
	MarkLeftSupervised(ctx context.Context, supervisorID string, at time.Time) (int64, error)
	ListByUser(ctx context.Context, userID string) ([]db.GymMember, error)
	Get(ctx context.Context, userID, gymID string) (*db.GymMember, error)
}

// LegacyMembershipGyms is the part of the gym store the legacy adapter reads.
type LegacyMembershipGyms interface {
	ListGymsByOwner(ctx context.Context, ownerID string) ([]db.GymSummary, error)
	GetGym(ctx context.Context, id string) (*db.Gym, error)
	GetGymOfHead(ctx context.Context, headID string) (*db.GymHeadCoach, error)
}

// LegacyMembershipTeam is the part of the team store the legacy adapter reads.
type LegacyMembershipTeam interface {
	GetHeadOf(ctx context.Context, coachID string) (*db.TeamMember, error)
}

// Compile-time checks that the concrete repositories satisfy the stores.
var (
	_ GymMemberStore       = (*repositories.GymMemberRepository)(nil)
	_ LegacyMembershipGyms = (*repositories.GymRepository)(nil)
	_ LegacyMembershipTeam = (*repositories.TeamRepository)(nil)
)

// legacyMembershipAdapter derives memberships from gyms, gym_head_coaches and coach_team_members.
type legacyMembershipAdapter struct {
	gyms LegacyMembershipGyms
	team LegacyMembershipTeam
}

// NewLegacyMembershipAdapter reads memberships from the legacy collections.
func NewLegacyMembershipAdapter(gyms LegacyMembershipGyms, team LegacyMembershipTeam) GymMembershipReader {
	return &legacyMembershipAdapter{gyms: gyms, team: team}
}

func (a *legacyMembershipAdapter) MembershipsOf(ctx context.Context, userID string) ([]db.GymMember, error) {
	out := []db.GymMember{}
	owned, err := a.gyms.ListGymsByOwner(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, sum := range owned {
		if sum.Status != db.GymStatusActive {
			continue
		}
		g, err := a.gyms.GetGym(ctx, sum.GymID)
		if err != nil {
			return nil, err
		}
		if g != nil {
			out = append(out, ownerMember(g))
		}
	}
	h, err := a.gyms.GetGymOfHead(ctx, userID)
	if err != nil {
		return nil, err
	}
	if h != nil {
		out = append(out, headMember(h))
	}
	tm, err := a.team.GetHeadOf(ctx, userID)
	if err != nil {
		return nil, err
	}
	if tm != nil {
		hh, err := a.gyms.GetGymOfHead(ctx, tm.HeadCoachID)
		if err != nil {
			return nil, err
		}
		if hh != nil {
			out = append(out, coachMember(hh.GymID, tm))
		}
	}
	return dedupeMembers(out), nil
}

func (a *legacyMembershipAdapter) MembershipOf(ctx context.Context, userID, gymID string) (*db.GymMember, error) {
	all, err := a.MembershipsOf(ctx, userID)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].GymID == gymID {
			return &all[i], nil
		}
	}
	return nil, nil
}

// ownerMember, headMember and coachMember are the legacy-to-row mappings shared with the backfill migration.
func ownerMember(g *db.Gym) db.GymMember {
	return db.GymMember{
		GymID: g.ID.Hex(), UserID: g.OwnerID, Role: db.GymMemberRoleOwner, Status: db.GymMemberStatusActive,
		JoinedAt: g.CreatedAt, AddedBy: g.CreatedBy, Source: db.GymMemberSourceAdmin,
	}
}

func headMember(h *db.GymHeadCoach) db.GymMember {
	return db.GymMember{
		GymID: h.GymID, UserID: h.HeadCoachID, Role: db.GymMemberRoleHeadCoach, Status: db.GymMemberStatusActive,
		JoinedAt: h.AddedAt, AddedBy: h.AddedBy, Source: db.GymMemberSourceLegacyHead,
	}
}

func coachMember(gymID string, tm *db.TeamMember) db.GymMember {
	return db.GymMember{
		GymID: gymID, UserID: tm.CoachID, Role: db.GymMemberRoleCoach, SupervisorID: tm.HeadCoachID,
		Status: db.GymMemberStatusActive, JoinedAt: tm.AssignedAt, AddedBy: tm.AssignedBy, Source: db.GymMemberSourceLegacyTeam,
	}
}

// BackfillMembers maps legacy rows to gym_members rows (seat_assigned=false, grandfathered).
// Coaches whose head coach is not in a gym are skipped; a head coach in a gym is the coach's supervisor.
func BackfillMembers(gyms []db.Gym, heads []db.GymHeadCoach, team []db.TeamMember) []db.GymMember {
	out := make([]db.GymMember, 0, len(gyms)+len(heads)+len(team))
	for i := range gyms {
		if gyms[i].Status == db.GymStatusActive {
			out = append(out, ownerMember(&gyms[i]))
		}
	}
	gymOfHead := make(map[string]string, len(heads))
	for i := range heads {
		gymOfHead[heads[i].HeadCoachID] = heads[i].GymID
		out = append(out, headMember(&heads[i]))
	}
	for i := range team {
		if gid, ok := gymOfHead[team[i].HeadCoachID]; ok {
			out = append(out, coachMember(gid, &team[i]))
		}
	}
	return dedupeMembers(out)
}

// memberRank orders the roles one user can hold in one gym: the owner row wins over head coach over coach.
// An owner who also coaches is represented by the owner row only (a coaching superset).
func memberRank(r db.GymMemberRole) int {
	switch r {
	case db.GymMemberRoleOwner:
		return 3
	case db.GymMemberRoleHeadCoach:
		return 2
	}
	return 1
}

// dedupeMembers keeps one row per (gym, user), the highest-ranked, in first-seen order.
func dedupeMembers(rows []db.GymMember) []db.GymMember {
	pos := make(map[[2]string]int, len(rows))
	out := make([]db.GymMember, 0, len(rows))
	for _, r := range rows {
		k := [2]string{r.GymID, r.UserID}
		if i, ok := pos[k]; ok {
			if memberRank(r.Role) > memberRank(out[i].Role) {
				out[i] = r
			}
			continue
		}
		pos[k] = len(out)
		out = append(out, r)
	}
	return out
}

// repoMembershipReader reads the gym_members collection.
type repoMembershipReader struct {
	members GymMemberStore
}

// NewRepoMembershipReader reads memberships from gym_members.
func NewRepoMembershipReader(members GymMemberStore) GymMembershipReader {
	return &repoMembershipReader{members: members}
}

func (r *repoMembershipReader) MembershipsOf(ctx context.Context, userID string) ([]db.GymMember, error) {
	return r.members.ListByUser(ctx, userID)
}

func (r *repoMembershipReader) MembershipOf(ctx context.Context, userID, gymID string) (*db.GymMember, error) {
	return r.members.Get(ctx, userID, gymID)
}

// NewGymMembershipReader picks the read path from GYM_MEMBERS_READ: "new" reads gym_members, anything else the legacy collections.
func NewGymMembershipReader(mode string, legacy, repoBacked GymMembershipReader) GymMembershipReader {
	if strings.EqualFold(strings.TrimSpace(mode), GymMembersReadNew) {
		return repoBacked
	}
	return legacy
}

// ---------- Dual-write (best effort: the legacy write already succeeded, a failure here is only logged) ----------

// mirrorTimeout bounds each mirror call so a hung Mongo cannot slow a legacy operation.
const mirrorTimeout = 2 * time.Second

// mirrorDo runs one mirror step on a context that outlives a cancelled request but not a slow database.
func mirrorDo(ctx context.Context, fn func(ctx context.Context)) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), mirrorTimeout)
	defer cancel()
	fn(ctx)
}

func mirrorFailed(op, gymID string, userIDs []string, err error) {
	zap.L().Error("gym member dual-write failed",
		zap.String("component", "gym_members"),
		zap.String("op", op),
		zap.String("gym_id", gymID),
		zap.Strings("user_ids", userIDs),
		zap.Error(err),
	)
}

// isGymOwner reports whether the user owns the gym according to gym_members. An owner is represented by the owner
// row only, so mirrors never write a head coach or coach row for the owner of the same gym.
func isGymOwner(ctx context.Context, store GymMemberStore, gymID, userID string) (bool, error) {
	row, err := store.Get(ctx, userID, gymID)
	if err != nil {
		return false, err
	}
	return row != nil && row.Role == db.GymMemberRoleOwner, nil
}

// mirrorHeadJoined upserts the active head coach row of a gym (not for the gym's owner: the owner row wins).
func mirrorHeadJoined(ctx context.Context, store GymMemberStore, gymID, headID, addedBy string, source db.GymMemberSource, at time.Time) {
	if store == nil {
		return
	}
	mirrorDo(ctx, func(ctx context.Context) {
		owner, err := isGymOwner(ctx, store, gymID, headID)
		if err != nil {
			mirrorFailed("headJoined.owner", gymID, []string{headID}, err)
			return
		}
		if owner {
			return
		}
		err = store.UpsertActive(ctx, &db.GymMember{
			GymID: gymID, UserID: headID, Role: db.GymMemberRoleHeadCoach, Status: db.GymMemberStatusActive,
			JoinedAt: at, AddedBy: addedBy, Source: source,
		})
		if err != nil {
			mirrorFailed("headJoined", gymID, []string{headID}, err)
		}
	})
}

// mirrorHeadLeft closes the head coach row and the rows of the coaches it supervises
// (the legacy team stays, but a team outside a gym is not a gym membership).
// The owner row of an owner who acted as head coach is kept: only a head_coach row is closed here.
func mirrorHeadLeft(ctx context.Context, store GymMemberStore, gymID, headID string) {
	if store == nil {
		return
	}
	now := time.Now()
	mirrorDo(ctx, func(ctx context.Context) {
		owner, err := isGymOwner(ctx, store, gymID, headID)
		if err != nil {
			mirrorFailed("headLeft.owner", gymID, []string{headID}, err)
		} else if !owner {
			if _, err := store.MarkLeft(ctx, gymID, headID, now); err != nil {
				mirrorFailed("headLeft", gymID, []string{headID}, err)
			}
		}
	})
	mirrorDo(ctx, func(ctx context.Context) {
		if _, err := store.MarkLeftBySupervisor(ctx, gymID, headID, now); err != nil {
			mirrorFailed("headLeftTeam", gymID, []string{headID}, err)
		}
	})
}

// mirrorCoachAssigned upserts the coach row under the head coach's gym; when the head coach is in no gym
// the coach has no gym membership. The owner of that gym keeps the owner row only.
func mirrorCoachAssigned(ctx context.Context, store GymMemberStore, headID, coachID, assignedBy string, at time.Time) {
	if store == nil {
		return
	}
	mirrorDo(ctx, func(ctx context.Context) {
		rows, err := store.ListByUser(ctx, headID)
		if err != nil {
			mirrorFailed("coachAssigned.head", "", []string{headID, coachID}, err)
			return
		}
		gymID := ""
		for i := range rows {
			if rows[i].Role == db.GymMemberRoleHeadCoach || rows[i].Role == db.GymMemberRoleOwner {
				gymID = rows[i].GymID
				if rows[i].Role == db.GymMemberRoleHeadCoach {
					break
				}
			}
		}
		if gymID == "" {
			if _, err := store.MarkLeftCoach(ctx, coachID, at); err != nil {
				mirrorFailed("coachAssigned.leave", "", []string{coachID}, err)
			}
			return
		}
		owner, err := isGymOwner(ctx, store, gymID, coachID)
		if err != nil {
			mirrorFailed("coachAssigned.owner", gymID, []string{coachID}, err)
			return
		}
		if owner {
			return
		}
		err = store.UpsertActive(ctx, &db.GymMember{
			GymID: gymID, UserID: coachID, Role: db.GymMemberRoleCoach, SupervisorID: headID, Status: db.GymMemberStatusActive,
			JoinedAt: at, AddedBy: assignedBy, Source: db.GymMemberSourceAdmin,
		})
		if err != nil {
			mirrorFailed("coachAssigned", gymID, []string{headID, coachID}, err)
		}
	})
}

// mirrorCoachUnassigned closes the coach row of a coach removed from a team.
func mirrorCoachUnassigned(ctx context.Context, store GymMemberStore, coachID string) {
	if store == nil {
		return
	}
	mirrorDo(ctx, func(ctx context.Context) {
		if _, err := store.MarkLeftCoach(ctx, coachID, time.Now()); err != nil {
			mirrorFailed("coachUnassigned", "", []string{coachID}, err)
		}
	})
}

// mirrorUserGone closes every gym membership a user held as head coach, the coach rows it supervised, and its
// coach rows, when the account is deleted or changes role. The three closes need no gym id, so no lookup can fail
// and leave rows open. Owner rows are left alone.
func mirrorUserGone(ctx context.Context, store GymMemberStore, userID string) {
	if store == nil {
		return
	}
	now := time.Now()
	mirrorDo(ctx, func(ctx context.Context) {
		if _, err := store.MarkLeftHead(ctx, userID, now); err != nil {
			mirrorFailed("userGone.head", "", []string{userID}, err)
		}
	})
	mirrorDo(ctx, func(ctx context.Context) {
		if _, err := store.MarkLeftSupervised(ctx, userID, now); err != nil {
			mirrorFailed("userGone.team", "", []string{userID}, err)
		}
	})
	mirrorCoachUnassigned(ctx, store, userID)
}

// GymTeamLister lists the coaches of a head coach; *repositories.TeamRepository satisfies it.
type GymTeamLister interface {
	IDsByHead(ctx context.Context, headID string) ([]string, error)
}

var _ GymTeamLister = (*repositories.TeamRepository)(nil)

// mirrorOwnerSet upserts the owner row of a gym and, on a transfer, closes the previous owner's row.
func mirrorOwnerSet(ctx context.Context, store GymMemberStore, gymID, ownerID, prevOwnerID, addedBy string, at time.Time) {
	if store == nil {
		return
	}
	mirrorDo(ctx, func(ctx context.Context) {
		err := store.UpsertActive(ctx, &db.GymMember{
			GymID: gymID, UserID: ownerID, Role: db.GymMemberRoleOwner, Status: db.GymMemberStatusActive,
			JoinedAt: at, AddedBy: addedBy, Source: db.GymMemberSourceAdmin,
		})
		if err != nil {
			mirrorFailed("ownerSet", gymID, []string{ownerID}, err)
			return
		}
		if prevOwnerID != "" && prevOwnerID != ownerID {
			if _, err := store.MarkLeft(ctx, gymID, prevOwnerID, at); err != nil {
				mirrorFailed("ownerPrevLeft", gymID, []string{prevOwnerID}, err)
			}
		}
	})
}

// mirrorOwnerLeft closes the owner row of a gym (an archived gym gives no owner membership).
func mirrorOwnerLeft(ctx context.Context, store GymMemberStore, gymID, ownerID string) {
	if store == nil {
		return
	}
	mirrorDo(ctx, func(ctx context.Context) {
		if _, err := store.MarkLeft(ctx, gymID, ownerID, time.Now()); err != nil {
			mirrorFailed("ownerLeft", gymID, []string{ownerID}, err)
		}
	})
}

// mirrorExistingTeam adds coach rows for the team a head coach already has when it joins a gym.
func mirrorExistingTeam(ctx context.Context, store GymMemberStore, team GymTeamLister, gymID, headID, addedBy string, at time.Time) {
	if store == nil || team == nil {
		return
	}
	var ids []string
	failed := false
	mirrorDo(ctx, func(ctx context.Context) {
		var err error
		if ids, err = team.IDsByHead(ctx, headID); err != nil {
			mirrorFailed("existingTeam.list", gymID, []string{headID}, err)
			failed = true
		}
	})
	if failed {
		return
	}
	// One time budget for the whole loop, so a hung database costs one mirrorTimeout, not one per coach.
	mirrorDo(ctx, func(ctx context.Context) {
		for _, id := range ids {
			owner, err := isGymOwner(ctx, store, gymID, id)
			if err != nil {
				mirrorFailed("existingTeam.owner", gymID, []string{headID, id}, err)
				continue
			}
			if owner {
				continue
			}
			err = store.UpsertActive(ctx, &db.GymMember{
				GymID: gymID, UserID: id, Role: db.GymMemberRoleCoach, SupervisorID: headID, Status: db.GymMemberStatusActive,
				JoinedAt: at, AddedBy: addedBy, Source: db.GymMemberSourceLegacyTeam,
			})
			if err != nil {
				mirrorFailed("existingTeam", gymID, []string{headID, id}, err)
				if ctx.Err() != nil {
					return
				}
			}
		}
	})
}

// MemberPlan is what a reconcile run must do, in this order: Close, Fix, Create.
type MemberPlan struct {
	// Close: active rows with no legacy counterpart (same gym and user).
	Close []db.GymMember
	// Fix: legacy rows whose active gym_members row differs in role or supervisor_id; corrected in place.
	Fix []db.GymMember
	// Create: legacy rows with no active gym_members row.
	Create []db.GymMember
}

// ReconcileMembers compares the active gym_members rows with the rows derived from the legacy collections.
// A row that matches on (gym, user), role and supervisor_id is never touched, so the plan is empty once in sync.
func ReconcileMembers(existing, desired []db.GymMember) MemberPlan {
	desired = dedupeMembers(desired)
	type key [2]string
	have := make(map[key]*db.GymMember, len(existing))
	for i := range existing {
		if existing[i].Status == db.GymMemberStatusActive {
			have[key{existing[i].GymID, existing[i].UserID}] = &existing[i]
		}
	}
	plan := MemberPlan{Close: []db.GymMember{}, Fix: []db.GymMember{}, Create: []db.GymMember{}}
	want := make(map[key]bool, len(desired))
	for i := range desired {
		k := key{desired[i].GymID, desired[i].UserID}
		want[k] = true
		cur, ok := have[k]
		switch {
		case !ok:
			plan.Create = append(plan.Create, desired[i])
		case cur.Role != desired[i].Role || cur.SupervisorID != desired[i].SupervisorID:
			plan.Fix = append(plan.Fix, desired[i])
		}
	}
	for i := range existing {
		if existing[i].Status == db.GymMemberStatusActive && !want[key{existing[i].GymID, existing[i].UserID}] {
			plan.Close = append(plan.Close, existing[i])
		}
	}
	return plan
}
