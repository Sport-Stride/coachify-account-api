package services

import (
	"context"
	"errors"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/zap"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
)

var (
	errNotACoach        = errors.New("user is not a coach")
	errHeadAccess       = errors.New("Head coach access required")
	errCoachNotFound    = errors.New("coach not found")
	errHeadNotFound     = errors.New("head coach not found")
	errInternal         = errors.New("internal error")
	errCoachHasTeam     = errors.New("coach belongs to a team, unassign first")
	errHeadAsMember     = errors.New("head coach cannot be a team member")
	errSameCoach        = errors.New("coach and head coach must differ")
	errNotHeadCoach     = errors.New("not a head coach")
	errTargetNotHead    = errors.New("target is not a head coach")
	errTargetNotActive  = errors.New("head coach is not active")
	errNoMembership     = errors.New("no membership")
	errCoachNotActive   = errors.New("coach is not active")
)

// TeamUserStore is the part of the user repository the team service needs.
type TeamUserStore interface {
	GetUserByExternalIdUpdate(ctx context.Context, userID string) (*db.User, *models.ApiError)
	SetHeadCoach(ctx context.Context, externalID string, flag bool, promotedBy string) error
	ListHeadCoaches(ctx context.Context, page, size int, q string) ([]db.HeadCoachRow, int, error)
}

// TeamStore is the part of the team repository the team service needs.
type TeamStore interface {
	Upsert(ctx context.Context, m *db.TeamMember) (*db.TeamMember, error)
	DeleteByCoach(ctx context.Context, coachID string) (int64, error)
	DeleteByHead(ctx context.Context, headID string) (int64, error)
	GetHeadOf(ctx context.Context, coachID string) (*db.TeamMember, error)
	IsMember(ctx context.Context, coachID string) (bool, error)
	CountByHead(ctx context.Context, headID string) (int, error)
	ListByHead(ctx context.Context, headID string, q db.TeamListQuery) ([]db.TeamCoachRow, int, error)
	GetRow(ctx context.Context, headID, coachID string) (*db.TeamCoachRow, error)
	Overview(ctx context.Context, headID string) (*db.TeamOverview, error)
	IDsByHead(ctx context.Context, headID string) ([]string, error)
	InsertAudit(ctx context.Context, e *db.TeamAuditEntry) error
	InsertAuditMany(ctx context.Context, entries []*db.TeamAuditEntry) error
	ListAudit(ctx context.Context, headCoachID, coachID string, page, size int) ([]db.TeamAuditEntry, int, error)
}

// HeadCoachMe is the answer of GET /head-coach/me.
type HeadCoachMe struct {
	IsHeadCoach    bool       `json:"is_head_coach"`
	HeadCoachSince *time.Time `json:"head_coach_since,omitempty"`
	TeamSize       int        `json:"team_size"`
}

// TeamHeadInfo is the public view of a coach's head coach.
type TeamHeadInfo struct {
	ExternalID     string `json:"external_id"`
	Firstname      string `json:"firstname"`
	Lastname       string `json:"lastname"`
	ProfilePicture string `json:"profile_picture,omitempty"`
}

// TeamService manages head coaches and their teams.
type TeamService interface {
	// Admin operations (the handler checks the admin role).
	ListHeadCoaches(ctx context.Context, page, size int, q string) ([]db.HeadCoachRow, int, *models.ApiError)
	Promote(ctx context.Context, adminID, coachID string) (*db.HeadCoachRow, *models.ApiError)
	Demote(ctx context.Context, adminID, headCoachID string) (int, *models.ApiError)
	ListTeam(ctx context.Context, headCoachID string, q db.TeamListQuery) ([]db.TeamCoachRow, int, *models.ApiError)
	Assign(ctx context.Context, adminID, coachID, headCoachID string) (*db.TeamMember, *models.ApiError)
	Unassign(ctx context.Context, adminID, coachID string) *models.ApiError
	ListAudit(ctx context.Context, headCoachID, coachID string, page, size int) ([]db.TeamAuditEntry, int, *models.ApiError)

	// Head coach operations (the service verifies the flag and status in the database).
	Me(ctx context.Context, callerID string) (*HeadCoachMe, *models.ApiError)
	Overview(ctx context.Context, callerID string) (*db.TeamOverview, *models.ApiError)
	ListMyCoaches(ctx context.Context, callerID string, q db.TeamListQuery) ([]db.TeamCoachRow, int, *models.ApiError)
	GetMyCoach(ctx context.Context, callerID, coachID string) (*db.TeamCoachRow, *models.ApiError)
	RemoveMyCoach(ctx context.Context, callerID, coachID string) *models.ApiError
	MyCoachIDs(ctx context.Context, callerID string) ([]string, *models.ApiError)

	// Any coach.
	MyHeadCoach(ctx context.Context, coachID string) (*TeamHeadInfo, *models.ApiError)
}

type teamServiceImpl struct {
	userRepo TeamUserStore
	teamRepo TeamStore
	gyms     GymMembershipReleaser
	reads    *readCache
}

// TeamOption customises the team service.
type TeamOption func(*teamServiceImpl)

// WithGymMembership makes Demote also remove the head coach from its gym.
func WithGymMembership(g GymMembershipReleaser) TeamOption {
	return func(s *teamServiceImpl) { s.gyms = g }
}

// WithReadCache caches the hot read endpoints (Me, MyHeadCoach) for ttl; mutations in this service purge it.
func WithReadCache(ttl time.Duration) TeamOption {
	return func(s *teamServiceImpl) { s.reads = newReadCache(ttl) }
}

func NewTeamService(userRepo TeamUserStore, teamRepo TeamStore, opts ...TeamOption) TeamService {
	s := &teamServiceImpl{userRepo: userRepo, teamRepo: teamRepo}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Compile-time checks that the concrete repositories satisfy the stores.
var (
	_ TeamUserStore = (*repositories.UserRepository)(nil)
	_ TeamStore     = (*repositories.TeamRepository)(nil)
)

func teamErr(code int, err error) *models.ApiError {
	return &models.ApiError{Code: code, Error: err}
}

// internal logs the real error (no PII) and returns a generic 500.
func (s *teamServiceImpl) internal(op string, err error) *models.ApiError {
	zap.L().Error("team service failure",
		zap.String("component", "team"),
		zap.String("op", op),
		zap.Error(err),
	)
	return teamErr(http.StatusInternalServerError, errInternal)
}

// loadUser fetches a user; notFound is returned as 404 with the given error.
func (s *teamServiceImpl) loadUser(ctx context.Context, op, id string, notFound error) (*db.User, *models.ApiError) {
	u, apiErr := s.userRepo.GetUserByExternalIdUpdate(ctx, id)
	if apiErr != nil {
		if apiErr.Code == http.StatusNotFound {
			return nil, teamErr(http.StatusNotFound, notFound)
		}
		return nil, s.internal(op, apiErr.Error)
	}
	if u == nil {
		return nil, teamErr(http.StatusNotFound, notFound)
	}
	return u, nil
}

// requireHead verifies in the database that the caller is a coach flagged as head coach with an Active account.
func (s *teamServiceImpl) requireHead(ctx context.Context, callerID string) (*db.User, *models.ApiError) {
	u, apiErr := s.userRepo.GetUserByExternalIdUpdate(ctx, callerID)
	if apiErr != nil {
		if apiErr.Code == http.StatusNotFound {
			return nil, teamErr(http.StatusForbidden, errHeadAccess)
		}
		return nil, s.internal("requireHead", apiErr.Error)
	}
	if u == nil || u.UserRole != "coach" || !u.IsHeadCoach || u.UserStatus != db.Active {
		return nil, teamErr(http.StatusForbidden, errHeadAccess)
	}
	return u, nil
}

func (s *teamServiceImpl) audit(ctx context.Context, e *db.TeamAuditEntry) {
	if err := s.teamRepo.InsertAudit(ctx, e); err != nil {
		// The mutation already happened; the failure is logged, not surfaced.
		zap.L().Error("team audit write failed",
			zap.String("component", "team"),
			zap.String("action", e.Action),
			zap.Error(err),
		)
	}
}

// ---------- Admin ----------

func (s *teamServiceImpl) ListHeadCoaches(ctx context.Context, page, size int, q string) ([]db.HeadCoachRow, int, *models.ApiError) {
	rows, total, err := s.userRepo.ListHeadCoaches(ctx, page, size, q)
	if err != nil {
		return nil, 0, s.internal("ListHeadCoaches", err)
	}
	return rows, total, nil
}

func headCoachRow(u *db.User, teamSize int) *db.HeadCoachRow {
	return &db.HeadCoachRow{
		ExternalID:     u.ExternalID,
		Firstname:      u.UserFirstname,
		Lastname:       u.UserLastname,
		Email:          u.UserEmail,
		ProfilePicture: u.UserProfilePicture,
		Status:         string(u.UserStatus),
		HeadCoachSince: u.HeadCoachSince,
		TeamSize:       teamSize,
	}
}

func (s *teamServiceImpl) Promote(ctx context.Context, adminID, coachID string) (*db.HeadCoachRow, *models.ApiError) {
	defer s.reads.purge()
	u, apiErr := s.loadUser(ctx, "Promote.load", coachID, errCoachNotFound)
	if apiErr != nil {
		return nil, apiErr
	}
	if u.UserRole != "coach" {
		return nil, teamErr(http.StatusBadRequest, errNotACoach)
	}
	if u.UserStatus != db.Active {
		return nil, teamErr(http.StatusConflict, errCoachNotActive)
	}

	if u.IsHeadCoach { // idempotent
		size, err := s.teamRepo.CountByHead(ctx, coachID)
		if err != nil {
			return nil, s.internal("Promote.count", err)
		}
		return headCoachRow(u, size), nil
	}

	member, err := s.teamRepo.IsMember(ctx, coachID)
	if err != nil {
		return nil, s.internal("Promote.isMember", err)
	}
	if member {
		return nil, teamErr(http.StatusConflict, errCoachHasTeam)
	}

	if err := s.userRepo.SetHeadCoach(ctx, coachID, true, adminID); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, teamErr(http.StatusNotFound, errCoachNotFound)
		}
		return nil, s.internal("Promote.set", err)
	}
	// Close the race with a concurrent Assign: if the coach became a member, undo the flag.
	member, err = s.teamRepo.IsMember(ctx, coachID)
	if err != nil {
		return nil, s.internal("Promote.recheck", err)
	}
	if member {
		if err := s.userRepo.SetHeadCoach(ctx, coachID, false, adminID); err != nil {
			return nil, s.internal("Promote.rollback", err)
		}
		return nil, teamErr(http.StatusConflict, errCoachHasTeam)
	}
	s.audit(ctx, &db.TeamAuditEntry{
		Action:      db.TeamActionPromote,
		ActorID:     adminID,
		ActorRole:   db.TeamActorRoleAdmin,
		HeadCoachID: coachID,
	})

	now := time.Now()
	u.HeadCoachSince = &now
	return headCoachRow(u, 0), nil
}

func (s *teamServiceImpl) Demote(ctx context.Context, adminID, headCoachID string) (int, *models.ApiError) {
	defer s.reads.purge()
	u, apiErr := s.loadUser(ctx, "Demote.load", headCoachID, errCoachNotFound)
	if apiErr != nil {
		return 0, apiErr
	}
	if !u.IsHeadCoach {
		// Retry after a partial failure: the flag is already off, finish releasing leftover members.
		remaining, err := s.teamRepo.CountByHead(ctx, headCoachID)
		if err != nil {
			return 0, s.internal("Demote.count", err)
		}
		if remaining == 0 {
			return 0, teamErr(http.StatusConflict, errNotHeadCoach)
		}
	}

	// Cut access first, then release the team.
	if err := s.userRepo.SetHeadCoach(ctx, headCoachID, false, adminID); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return 0, teamErr(http.StatusNotFound, errCoachNotFound)
		}
		return 0, s.internal("Demote.set", err)
	}
	ids, err := s.teamRepo.IDsByHead(ctx, headCoachID)
	if err != nil {
		return 0, s.internal("Demote.ids", err)
	}
	released, err := s.teamRepo.DeleteByHead(ctx, headCoachID)
	if err != nil {
		return 0, s.internal("Demote.delete", err)
	}

	// A demoted head coach leaves its gym too (the gym owner cannot demote, only remove from the gym).
	gymID := ""
	if s.gyms != nil {
		gid, removed, gErr := s.gyms.ReleaseHead(ctx, headCoachID, db.PeriodReasonDemote)
		if gErr != nil {
			return 0, s.internal("Demote.gym", gErr)
		}
		if removed {
			gymID = gid
		}
	}

	entries := make([]*db.TeamAuditEntry, 0, len(ids)+1)
	entries = append(entries, &db.TeamAuditEntry{
		Action:      db.TeamActionDemote,
		ActorID:     adminID,
		ActorRole:   db.TeamActorRoleAdmin,
		HeadCoachID: headCoachID,
	})
	for _, id := range ids {
		entries = append(entries, &db.TeamAuditEntry{
			Action:      db.TeamActionUnassign,
			ActorID:     adminID,
			ActorRole:   db.TeamActorRoleAdmin,
			HeadCoachID: headCoachID,
			CoachID:     id,
		})
	}
	if gymID != "" {
		entries = append(entries, &db.TeamAuditEntry{
			Action:      db.TeamActionGymRemoveHead,
			ActorID:     adminID,
			ActorRole:   db.TeamActorRoleAdmin,
			HeadCoachID: headCoachID,
			GymID:       gymID,
		})
	}
	if err := s.teamRepo.InsertAuditMany(ctx, entries); err != nil {
		zap.L().Error("team audit write failed",
			zap.String("component", "team"),
			zap.String("action", db.TeamActionDemote),
			zap.Error(err),
		)
	}
	return int(released), nil
}

func (s *teamServiceImpl) ListTeam(ctx context.Context, headCoachID string, q db.TeamListQuery) ([]db.TeamCoachRow, int, *models.ApiError) {
	u, apiErr := s.loadUser(ctx, "ListTeam.load", headCoachID, errHeadNotFound)
	if apiErr != nil {
		return nil, 0, apiErr
	}
	if !u.IsHeadCoach {
		return nil, 0, teamErr(http.StatusNotFound, errHeadNotFound)
	}
	rows, total, err := s.teamRepo.ListByHead(ctx, headCoachID, q)
	if err != nil {
		return nil, 0, s.internal("ListTeam.list", err)
	}
	return rows, total, nil
}

func (s *teamServiceImpl) Assign(ctx context.Context, adminID, coachID, headCoachID string) (*db.TeamMember, *models.ApiError) {
	defer s.reads.purge()
	if coachID == "" || headCoachID == "" || coachID == headCoachID {
		return nil, teamErr(http.StatusBadRequest, errSameCoach)
	}

	coach, apiErr := s.loadUser(ctx, "Assign.coach", coachID, errCoachNotFound)
	if apiErr != nil {
		return nil, apiErr
	}
	if coach.UserRole != "coach" {
		return nil, teamErr(http.StatusBadRequest, errNotACoach)
	}
	if coach.IsHeadCoach {
		return nil, teamErr(http.StatusConflict, errHeadAsMember)
	}

	head, apiErr := s.loadUser(ctx, "Assign.head", headCoachID, errHeadNotFound)
	if apiErr != nil {
		return nil, apiErr
	}
	if head.UserRole != "coach" || !head.IsHeadCoach {
		return nil, teamErr(http.StatusConflict, errTargetNotHead)
	}
	if head.UserStatus != db.Active {
		return nil, teamErr(http.StatusConflict, errTargetNotActive)
	}

	m := &db.TeamMember{
		HeadCoachID:    headCoachID,
		CoachID:        coachID,
		AssignedBy:     adminID,
		AssignedByRole: db.TeamAssignerAdmin,
		AssignedAt:     time.Now(),
	}
	prev, err := s.teamRepo.Upsert(ctx, m)
	if err != nil {
		return nil, s.internal("Assign.upsert", err)
	}

	// The head may have been demoted or deactivated meanwhile: undo the assignment.
	cur, apiErr := s.loadUser(ctx, "Assign.recheck", headCoachID, errHeadNotFound)
	if apiErr != nil || !cur.IsHeadCoach || cur.UserStatus != db.Active {
		if _, dErr := s.teamRepo.DeleteByCoach(ctx, coachID); dErr != nil {
			return nil, s.internal("Assign.rollback", dErr)
		}
		if apiErr != nil && apiErr.Code != http.StatusNotFound {
			return nil, apiErr
		}
		return nil, teamErr(http.StatusConflict, errTargetNotHead)
	}

	switch {
	case prev == nil:
		s.audit(ctx, &db.TeamAuditEntry{
			Action: db.TeamActionAssign, ActorID: adminID, ActorRole: db.TeamActorRoleAdmin,
			HeadCoachID: headCoachID, CoachID: coachID,
		})
	case prev.HeadCoachID != headCoachID:
		s.audit(ctx, &db.TeamAuditEntry{
			Action: db.TeamActionReassign, ActorID: adminID, ActorRole: db.TeamActorRoleAdmin,
			HeadCoachID: headCoachID, CoachID: coachID, PrevHeadCoachID: prev.HeadCoachID,
		})
	}
	return m, nil
}

func (s *teamServiceImpl) Unassign(ctx context.Context, adminID, coachID string) *models.ApiError {
	defer s.reads.purge()
	prev, err := s.teamRepo.GetHeadOf(ctx, coachID)
	if err != nil {
		return s.internal("Unassign.get", err)
	}
	if prev == nil {
		return teamErr(http.StatusNotFound, errNoMembership)
	}
	n, err := s.teamRepo.DeleteByCoach(ctx, coachID)
	if err != nil {
		return s.internal("Unassign.delete", err)
	}
	if n == 0 {
		return teamErr(http.StatusNotFound, errNoMembership)
	}
	s.audit(ctx, &db.TeamAuditEntry{
		Action: db.TeamActionUnassign, ActorID: adminID, ActorRole: db.TeamActorRoleAdmin,
		HeadCoachID: prev.HeadCoachID, CoachID: coachID,
	})
	return nil
}

func (s *teamServiceImpl) ListAudit(ctx context.Context, headCoachID, coachID string, page, size int) ([]db.TeamAuditEntry, int, *models.ApiError) {
	entries, total, err := s.teamRepo.ListAudit(ctx, headCoachID, coachID, page, size)
	if err != nil {
		return nil, 0, s.internal("ListAudit", err)
	}
	return entries, total, nil
}

// ---------- Head coach ----------

func (s *teamServiceImpl) Me(ctx context.Context, callerID string) (*HeadCoachMe, *models.ApiError) {
	return cachedRead(s.reads, "me:"+callerID, func() (*HeadCoachMe, *models.ApiError) { return s.me(ctx, callerID) })
}

func (s *teamServiceImpl) me(ctx context.Context, callerID string) (*HeadCoachMe, *models.ApiError) {
	u, apiErr := s.loadUser(ctx, "Me.load", callerID, errCoachNotFound)
	if apiErr != nil {
		return nil, apiErr
	}
	if u.UserRole != "coach" || !u.IsHeadCoach || u.UserStatus != db.Active {
		return &HeadCoachMe{}, nil
	}
	size, err := s.teamRepo.CountByHead(ctx, callerID)
	if err != nil {
		return nil, s.internal("Me.count", err)
	}
	return &HeadCoachMe{IsHeadCoach: true, HeadCoachSince: u.HeadCoachSince, TeamSize: size}, nil
}

func (s *teamServiceImpl) Overview(ctx context.Context, callerID string) (*db.TeamOverview, *models.ApiError) {
	if _, apiErr := s.requireHead(ctx, callerID); apiErr != nil {
		return nil, apiErr
	}
	o, err := s.teamRepo.Overview(ctx, callerID)
	if err != nil {
		return nil, s.internal("Overview", err)
	}
	return o, nil
}

func (s *teamServiceImpl) ListMyCoaches(ctx context.Context, callerID string, q db.TeamListQuery) ([]db.TeamCoachRow, int, *models.ApiError) {
	if _, apiErr := s.requireHead(ctx, callerID); apiErr != nil {
		return nil, 0, apiErr
	}
	rows, total, err := s.teamRepo.ListByHead(ctx, callerID, q)
	if err != nil {
		return nil, 0, s.internal("ListMyCoaches", err)
	}
	return rows, total, nil
}

func (s *teamServiceImpl) GetMyCoach(ctx context.Context, callerID, coachID string) (*db.TeamCoachRow, *models.ApiError) {
	if _, apiErr := s.requireHead(ctx, callerID); apiErr != nil {
		return nil, apiErr
	}
	row, err := s.teamRepo.GetRow(ctx, callerID, coachID)
	if err != nil {
		return nil, s.internal("GetMyCoach", err)
	}
	if row == nil { // not in the caller's team: do not leak existence
		return nil, teamErr(http.StatusNotFound, errCoachNotFound)
	}
	return row, nil
}

func (s *teamServiceImpl) RemoveMyCoach(ctx context.Context, callerID, coachID string) *models.ApiError {
	defer s.reads.purge()
	if _, apiErr := s.requireHead(ctx, callerID); apiErr != nil {
		return apiErr
	}
	m, err := s.teamRepo.GetHeadOf(ctx, coachID)
	if err != nil {
		return s.internal("RemoveMyCoach.get", err)
	}
	if m == nil || m.HeadCoachID != callerID {
		return teamErr(http.StatusNotFound, errCoachNotFound)
	}
	n, err := s.teamRepo.DeleteByCoach(repositories.WithPeriodReason(ctx, db.PeriodReasonRemovedByHead), coachID)
	if err != nil {
		return s.internal("RemoveMyCoach.delete", err)
	}
	if n == 0 {
		return teamErr(http.StatusNotFound, errCoachNotFound)
	}
	s.audit(ctx, &db.TeamAuditEntry{
		Action: db.TeamActionUnassign, ActorID: callerID, ActorRole: db.TeamActorRoleHead,
		HeadCoachID: callerID, CoachID: coachID,
	})
	return nil
}

func (s *teamServiceImpl) MyCoachIDs(ctx context.Context, callerID string) ([]string, *models.ApiError) {
	if _, apiErr := s.requireHead(ctx, callerID); apiErr != nil {
		return nil, apiErr
	}
	ids, err := s.teamRepo.IDsByHead(ctx, callerID)
	if err != nil {
		return nil, s.internal("MyCoachIDs", err)
	}
	return ids, nil
}

// ---------- Any coach ----------

func (s *teamServiceImpl) MyHeadCoach(ctx context.Context, coachID string) (*TeamHeadInfo, *models.ApiError) {
	return cachedRead(s.reads, "head:"+coachID, func() (*TeamHeadInfo, *models.ApiError) { return s.myHeadCoach(ctx, coachID) })
}

func (s *teamServiceImpl) myHeadCoach(ctx context.Context, coachID string) (*TeamHeadInfo, *models.ApiError) {
	m, err := s.teamRepo.GetHeadOf(ctx, coachID)
	if err != nil {
		return nil, s.internal("MyHeadCoach.get", err)
	}
	if m == nil {
		return nil, nil
	}
	head, apiErr := s.userRepo.GetUserByExternalIdUpdate(ctx, m.HeadCoachID)
	if apiErr != nil {
		if apiErr.Code == http.StatusNotFound {
			return nil, nil
		}
		return nil, s.internal("MyHeadCoach.head", apiErr.Error)
	}
	if head == nil || !head.IsHeadCoach {
		return nil, nil
	}
	return &TeamHeadInfo{
		ExternalID:     head.ExternalID,
		Firstname:      head.UserFirstname,
		Lastname:       head.UserLastname,
		ProfilePicture: head.UserProfilePicture,
	}, nil
}
