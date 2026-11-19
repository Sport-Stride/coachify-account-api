package services

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"time"

	"go.uber.org/zap"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/pkg/payments"
	"coachify-account-api/repositories"
)

// SeatStore is the gym member persistence the seat service needs; *repositories.GymMemberRepository satisfies it.
type SeatStore interface {
	ListByGym(ctx context.Context, gymID string) ([]db.GymMember, error)
	ListByUser(ctx context.Context, userID string) ([]db.GymMember, error)
	Get(ctx context.Context, userID, gymID string) (*db.GymMember, error)
	SetSeat(ctx context.Context, gymID, userID string, assigned bool, at time.Time) (bool, error)
	SetSeatState(ctx context.Context, gymID, userID string, state db.SeatState, graceUntil *time.Time, at time.Time) (bool, error)
}

var _ SeatStore = (*repositories.GymMemberRepository)(nil)

// SeatSettings configures the seat service.
type SeatSettings struct {
	// Enabled is GYM_SEATS_ENABLED; off: every method is a no-op or 404.
	Enabled bool
	// Grandfather is GYM_SEAT_GRANDFATHER.
	Grandfather bool
	// OverflowGrace is the grace of seats above the purchased total (GYM_SEAT_OVERFLOW_GRACE_DAYS).
	OverflowGrace time.Duration
	// Inval and Bump drop cached authz decisions and move the gym's authz_version on a seat change; both may be nil.
	Inval AuthzInvalidator
	Bump  AuthzVersionBumper
	// Purge drops the cached context entitlements on a seat change; may be nil.
	Purge func()
}

// SeatStanding is the reconciled seat situation of one user in one gym.
type SeatStanding struct {
	Member  *db.GymMember
	State   db.SeatState
	Summary *payments.GymSummary
	// Unlimited: grandfathered gym with no subscription, so no limits and every coach is seated.
	Unlimited bool
	// Grandfather mirrors GYM_SEAT_GRANDFATHER: an unseated member keeps his personal features.
	Grandfather bool
}

// SeatMemberView is one coach row of the seat overview.
type SeatMemberView struct {
	UserID         string     `json:"user_id"`
	Role           string     `json:"role"`
	SeatAssigned   bool       `json:"seat_assigned"`
	SeatState      string     `json:"seat_state"`
	SeatGraceUntil *time.Time `json:"seat_grace_until,omitempty"`
	SeatAssignedAt *time.Time `json:"seat_assigned_at,omitempty"`
	JoinedAt       time.Time  `json:"joined_at"`
}

// SeatOverview is GET /gyms/:gym_id/seats.
type SeatOverview struct {
	GymID         string           `json:"gym_id"`
	Status        string           `json:"status"`
	PlanID        string           `json:"plan_id,omitempty"`
	Purchased     int64            `json:"purchased"`
	IncludedSeats int64            `json:"included_seats"`
	ExtraSeats    int64            `json:"extra_seats"`
	Used          int64            `json:"used"`
	Free          int64            `json:"free"`
	Overflow      int64            `json:"overflow"`
	Enforced      bool             `json:"enforced"`
	GraceUntil    *time.Time       `json:"grace_until,omitempty"`
	Members       []SeatMemberView `json:"members"`
}

// SeatService accounts and enforces gym seats. The seat transitions (overflow grace, read-only, lapse) are computed
// lazily on every read and written back only when they changed, so repeating a read is idempotent.
type SeatService interface {
	SeatGuard
	// OnJoined assigns a free seat to a coach or head coach who just joined; no-op when disabled or unlimited.
	OnJoined(ctx context.Context, gymID, userID string)
	// Overview is owner only. Overview and SetSeat answer 404 for a foreign gym and while the flag is off.
	Overview(ctx context.Context, callerID, gymID string) (*SeatOverview, *models.ApiError)
	SetSeat(ctx context.Context, callerID, gymID, userID string, assigned bool) (*SeatMemberView, *models.ApiError)
	Standing(ctx context.Context, gymID, userID string) (*SeatStanding, *models.ApiError)
	InGymAsCoach(ctx context.Context, userID string) (bool, error)
}

type seatService struct {
	authz     Authorizer
	store     SeatStore
	summaries *GymSummaryCache
	set       SeatSettings
	now       func() time.Time
}

// NewSeatService wires the service.
func NewSeatService(authz Authorizer, store SeatStore, summaries *GymSummaryCache, set SeatSettings) SeatService {
	return &seatService{authz: authz, store: store, summaries: summaries, set: set, now: time.Now}
}

func isSeatRole(r db.GymMemberRole) bool {
	return r == db.GymMemberRoleCoach || r == db.GymMemberRoleHeadCoach
}

func seatsExhausted(used, total int64, msg string) *models.ApiError {
	return models.NewEntitlementApiError(http.StatusConflict, &models.EntitlementError{
		Code: models.CodeSeatsExhausted, Message: msg, Limit: "seats", Used: &used, Max: &total,
	})
}

func (s *seatService) internal(op string, err error) *models.ApiError {
	zap.L().Error("gym seats failure", zap.String("component", "gym_seats"), zap.String("op", op), zap.Error(err))
	return teamErr(http.StatusInternalServerError, errInternal)
}

// seatDecision is the computed seat state of one coach row.
type seatDecision struct {
	Member     db.GymMember
	State      db.SeatState
	GraceUntil *time.Time
	// AutoSeat: a grandfathered member who never held a seat and is seated now that the gym subscribed.
	AutoSeat bool
}

func (d seatDecision) changed() bool {
	m := d.Member
	if m.SeatState != d.State {
		return true
	}
	switch {
	case m.SeatGraceUntil == nil && d.GraceUntil == nil:
		return false
	case m.SeatGraceUntil == nil || d.GraceUntil == nil:
		return true
	}
	return !m.SeatGraceUntil.Equal(*d.GraceUntil)
}

func seatOrderTime(m db.GymMember) time.Time {
	if m.SeatAssignedAt != nil {
		return *m.SeatAssignedAt
	}
	return m.JoinedAt
}

// isUnlimited: a grandfathered gym with no subscription has no seat limits.
func isUnlimited(sum *payments.GymSummary, grandfather bool) bool {
	return grandfather && sum.Status == gymStatusNone
}

// resolveSeats computes the seat state of every coach and head coach (the owner has none).
//   - unlimited (grandfather, no subscription): everybody is seated, nothing is persisted.
//   - a member without a seat has state none, except a grandfathered one who never had any seat state: once the gym
//     has an active (or grace) subscription he is auto-seated, oldest joined first, and the ones beyond total_seats
//     go to grace like any overflow.
//   - lapsed gym: seat holders are read_only (the owner keeps managing and subscribing).
//   - otherwise the oldest seat holders up to total_seats are seated; the newest beyond it are overflow: grace until
//     first detection + grace, then read_only. Status "grace" at payments keeps everybody seated.
func resolveSeats(members []db.GymMember, sum *payments.GymSummary, grandfather bool, grace time.Duration, now time.Time) []seatDecision {
	var coaches []db.GymMember
	for _, m := range members {
		if isSeatRole(m.Role) && m.Status == db.GymMemberStatusActive {
			coaches = append(coaches, m)
		}
	}
	sort.SliceStable(coaches, func(i, j int) bool {
		ti, tj := seatOrderTime(coaches[i]), seatOrderTime(coaches[j])
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		return coaches[i].UserID < coaches[j].UserID
	})
	unlimited := isUnlimited(sum, grandfather)
	var holders int64
	out := make([]seatDecision, 0, len(coaches))
	for _, m := range coaches {
		d := seatDecision{Member: m}
		subscribed := sum.Status == gymStatusActive || sum.Status == gymStatusGrace
		d.AutoSeat = grandfather && subscribed && !m.SeatAssigned && m.SeatState == ""
		switch {
		case unlimited:
			d.State = db.SeatStateSeated
		case !m.SeatAssigned && !d.AutoSeat:
			d.State = db.SeatStateNone
		case sum.Status == gymStatusLapsed:
			d.State = db.SeatStateReadOnly
		default:
			holders++
			if holders <= sum.TotalSeats {
				d.State = db.SeatStateSeated
				break
			}
			until := now.Add(grace)
			if m.SeatGraceUntil != nil {
				until = *m.SeatGraceUntil
			}
			d.GraceUntil = &until
			d.State = db.SeatStateGrace
			if !now.Before(until) {
				d.State = db.SeatStateReadOnly
			}
		}
		out = append(out, d)
	}
	return out
}

// reconcile loads the gym, computes the seat states and writes back only the rows that changed (unless unlimited).
func (s *seatService) reconcile(ctx context.Context, gymID string) ([]seatDecision, *payments.GymSummary, *models.ApiError) {
	sum, apiErr := s.summaries.Get(ctx, gymID)
	if apiErr != nil {
		return nil, nil, apiErr
	}
	members, err := s.store.ListByGym(ctx, gymID)
	if err != nil {
		return nil, nil, s.internal("reconcile.list", err)
	}
	now := s.now()
	ds := resolveSeats(members, sum, s.set.Grandfather, s.set.OverflowGrace, now)
	if isUnlimited(sum, s.set.Grandfather) {
		return ds, sum, nil
	}
	wrote := false
	for i := range ds {
		if ds[i].AutoSeat {
			joined := ds[i].Member.JoinedAt
			if _, err := s.store.SetSeat(ctx, gymID, ds[i].Member.UserID, true, joined); err != nil {
				zap.L().Warn("gym seat auto-assign failed", zap.String("component", "gym_seats"), zap.String("gym_id", gymID), zap.Error(err))
				continue
			}
			wrote = true
			ds[i].Member.SeatAssigned, ds[i].Member.SeatAssignedAt, ds[i].Member.SeatState = true, &joined, db.SeatStateSeated
			ds[i].Member.SeatGraceUntil = nil
		}
		if !ds[i].changed() {
			continue
		}
		if _, err := s.store.SetSeatState(ctx, gymID, ds[i].Member.UserID, ds[i].State, ds[i].GraceUntil, now); err != nil {
			zap.L().Warn("gym seat state write failed", zap.String("component", "gym_seats"), zap.String("gym_id", gymID), zap.Error(err))
			continue
		}
		wrote = true
		ds[i].Member.SeatState, ds[i].Member.SeatGraceUntil = ds[i].State, ds[i].GraceUntil
	}
	if wrote {
		s.changed(ctx, gymID)
	}
	return ds, sum, nil
}

func countHolders(ds []seatDecision) int64 {
	var n int64
	for _, d := range ds {
		if d.Member.SeatAssigned {
			n++
		}
	}
	return n
}

// Check is the pre-redeem guard: a coach or head coach may join only while a seat is free. Unlimited gyms pass.
func (s *seatService) Check(ctx context.Context, gymID string) *models.ApiError {
	if !s.set.Enabled {
		return nil
	}
	sum, apiErr := s.summaries.Get(ctx, gymID)
	if apiErr != nil {
		return apiErr
	}
	if isUnlimited(sum, s.set.Grandfather) {
		return nil
	}
	members, err := s.store.ListByGym(ctx, gymID)
	if err != nil {
		return s.internal("check.list", err)
	}
	var used int64
	for _, m := range members {
		if isSeatRole(m.Role) && m.SeatAssigned {
			used++
		}
	}
	if sum.Status == gymStatusLapsed {
		return seatsExhausted(used, sum.TotalSeats, "gym subscription lapsed")
	}
	if used >= sum.TotalSeats {
		return seatsExhausted(used, sum.TotalSeats, "no free seat in this gym")
	}
	return nil
}

func (s *seatService) OnJoined(ctx context.Context, gymID, userID string) {
	if !s.set.Enabled {
		return
	}
	// A failed summary lookup (retried once) must not leave the joiner without a seat: assign it anyway and let the
	// lazy reconcile move any overflow into grace.
	sum, apiErr := s.summaries.Get(ctx, gymID)
	if apiErr != nil {
		sum, apiErr = s.summaries.Get(ctx, gymID)
	}
	if apiErr != nil {
		zap.L().Warn("gym seat assign on join: summary unavailable, seating anyway", zap.String("component", "gym_seats"), zap.String("gym_id", gymID))
	} else if isUnlimited(sum, s.set.Grandfather) {
		return
	}
	if _, err := s.store.SetSeat(ctx, gymID, userID, true, s.now()); err != nil {
		s.internal("onJoined.set", err)
		return
	}
	s.changed(ctx, gymID)
}

// changed drops the gym's cached authz decisions, bumps its authz_version and purges the cached context
// entitlements. The gym summary cache is left alone: it holds payments data, not seat data.
func (s *seatService) changed(ctx context.Context, gymID string) {
	if s.set.Purge != nil {
		s.set.Purge()
	}
	if s.set.Inval != nil {
		s.set.Inval.Invalidate(gymID)
	}
	if s.set.Bump != nil {
		if _, err := s.set.Bump.BumpAuthzVersion(ctx, gymID); err != nil {
			zap.L().Warn("gym seat authz bump failed", zap.String("component", "gym_seats"), zap.String("gym_id", gymID), zap.Error(err))
		}
	}
}

func (s *seatService) requireManage(ctx context.Context, callerID, gymID string) *models.ApiError {
	d, apiErr := s.authz.Can(ctx, Actor{ID: callerID}, CapGymManage, Resource{Type: ResourceGym, ID: gymID, GymID: gymID})
	if apiErr != nil {
		return apiErr
	}
	if d.Allowed {
		return nil
	}
	if d.Reason == ReasonNotMember {
		return teamErr(http.StatusNotFound, errGymNotFound)
	}
	return teamErr(http.StatusForbidden, errSupervisorAccess)
}

func seatMemberView(d seatDecision) SeatMemberView {
	m := d.Member
	return SeatMemberView{
		UserID: m.UserID, Role: string(m.Role), SeatAssigned: m.SeatAssigned, SeatState: string(d.State),
		SeatGraceUntil: d.GraceUntil, SeatAssignedAt: m.SeatAssignedAt, JoinedAt: m.JoinedAt,
	}
}

func (s *seatService) Overview(ctx context.Context, callerID, gymID string) (*SeatOverview, *models.ApiError) {
	if !s.set.Enabled {
		return nil, teamErr(http.StatusNotFound, errMembersDisabled)
	}
	if apiErr := s.requireManage(ctx, callerID, gymID); apiErr != nil {
		return nil, apiErr
	}
	ds, sum, apiErr := s.reconcile(ctx, gymID)
	if apiErr != nil {
		return nil, apiErr
	}
	used := countHolders(ds)
	unlimited := isUnlimited(sum, s.set.Grandfather)
	ov := &SeatOverview{
		GymID: gymID, Status: sum.Status, PlanID: sum.PlanID, Purchased: sum.TotalSeats,
		IncludedSeats: sum.IncludedSeats, ExtraSeats: sum.ExtraSeats, Used: used, Enforced: !unlimited,
		GraceUntil: sum.GraceUntil, Members: make([]SeatMemberView, 0, len(ds)),
	}
	if !unlimited {
		if used < sum.TotalSeats {
			ov.Free = sum.TotalSeats - used
		} else {
			ov.Overflow = used - sum.TotalSeats
		}
	}
	for _, d := range ds {
		ov.Members = append(ov.Members, seatMemberView(d))
	}
	return ov, nil
}

func (s *seatService) SetSeat(ctx context.Context, callerID, gymID, userID string, assigned bool) (*SeatMemberView, *models.ApiError) {
	if !s.set.Enabled {
		return nil, teamErr(http.StatusNotFound, errMembersDisabled)
	}
	if apiErr := s.requireManage(ctx, callerID, gymID); apiErr != nil {
		return nil, apiErr
	}
	target, err := s.store.Get(ctx, userID, gymID)
	if err != nil {
		return nil, s.internal("setSeat.get", err)
	}
	if target == nil {
		return nil, teamErr(http.StatusNotFound, errMemberNotFound)
	}
	if !isSeatRole(target.Role) {
		return nil, teamErr(http.StatusBadRequest, errSeatOwner)
	}
	if assigned != target.SeatAssigned {
		if assigned {
			if apiErr := s.Check(ctx, gymID); apiErr != nil {
				return nil, apiErr
			}
		}
		if _, err := s.store.SetSeat(ctx, gymID, userID, assigned, s.now()); err != nil {
			return nil, s.internal("setSeat.set", err)
		}
		s.changed(ctx, gymID)
	}
	ds, _, apiErr := s.reconcile(ctx, gymID)
	if apiErr != nil {
		return nil, apiErr
	}
	for _, d := range ds {
		if d.Member.UserID == userID {
			v := seatMemberView(d)
			return &v, nil
		}
	}
	return nil, teamErr(http.StatusNotFound, errMemberNotFound)
}

func (s *seatService) Standing(ctx context.Context, gymID, userID string) (*SeatStanding, *models.ApiError) {
	m, err := s.store.Get(ctx, userID, gymID)
	if err != nil {
		return nil, s.internal("standing.get", err)
	}
	if m == nil {
		return nil, nil
	}
	ds, sum, apiErr := s.reconcile(ctx, gymID)
	if apiErr != nil {
		return nil, apiErr
	}
	st := &SeatStanding{Member: m, State: db.SeatStateNone, Summary: sum,
		Unlimited: isUnlimited(sum, s.set.Grandfather), Grandfather: s.set.Grandfather}
	for _, d := range ds {
		if d.Member.UserID == userID {
			st.State = d.State
			st.Member = &d.Member
		}
	}
	return st, nil
}

func (s *seatService) InGymAsCoach(ctx context.Context, userID string) (bool, error) {
	ms, err := s.store.ListByUser(ctx, userID)
	if err != nil {
		return false, err
	}
	for _, m := range ms {
		if isSeatRole(m.Role) {
			return true, nil
		}
	}
	return false, nil
}

// gymPlanLimit is the GymClientLimit of the gym subscription: max_gym_clients of the summary. Grandfathered gyms
// with no subscription are unlimited; active or grace plans with no cap (0) are unlimited; a gym with no
// subscription (grandfather off) or a lapsed one admits no new gym client.
type gymPlanLimit struct {
	summaries   *GymSummaryCache
	grandfather bool
}

func (l *gymPlanLimit) MaxGymClients(ctx context.Context, gymID string) (*int64, error) {
	sum, apiErr := l.summaries.Get(ctx, gymID)
	if apiErr != nil {
		if l.grandfather {
			// Grandfathered gyms fail open on a payments outage rather than blocking gym client work.
			zap.L().Warn("gym client limit: summary unavailable, not limiting", zap.String("component", "gym_seats"), zap.String("gym_id", gymID))
			return nil, nil
		}
		return nil, apiErr.Error
	}
	switch {
	case isUnlimited(sum, l.grandfather):
		return nil, nil
	case sum.Status == gymStatusActive || sum.Status == gymStatusGrace:
		if sum.MaxGymClients <= 0 {
			return nil, nil
		}
	default:
		zero := int64(0)
		return &zero, nil
	}
	v := sum.MaxGymClients
	return &v, nil
}

var errSeatOwner = errors.New("the owner does not consume a seat")
