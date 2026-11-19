package services

import (
	"context"
	"errors"
	"net/http"
	"time"

	"go.uber.org/zap"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
)

var (
	errMembersDisabled   = errors.New("not found")
	errMembersAccess     = errors.New("access to the gym member list denied")
	errSupervisorAccess  = errors.New("Gym owner access required")
	errSupervisorTarget  = errors.New("only a coach can have a supervisor")
	errSupervisorInvalid = errors.New("supervisor must be an active head coach of the same gym")
	errSupervisorCycle   = errors.New("a member cannot supervise itself")
	errSupervisorRace    = errors.New("member or supervisor changed concurrently, reload and retry")
)

// GymMembersStore is the part of the gym member store the members service needs.
type GymMembersStore interface {
	Get(ctx context.Context, userID, gymID string) (*db.GymMember, error)
	SetSupervisor(ctx context.Context, gymID, userID, prevSupervisorID, supervisorID string) (bool, error)
	ListByGymPage(ctx context.Context, gymID, scopeUserID, role string, page, size int) ([]db.GymMemberRow, int64, error)
	GetRow(ctx context.Context, gymID, userID string) (*db.GymMemberRow, error)
}

// SupervisionTeamStore is the part of the team store that keeps the legacy TeamMember projection and audit in step.
type SupervisionTeamStore interface {
	Upsert(ctx context.Context, m *db.TeamMember) (*db.TeamMember, error)
	DeleteByCoach(ctx context.Context, coachID string) (int64, error)
	InsertAudit(ctx context.Context, e *db.TeamAuditEntry) error
}

// GymMemberView is one gym member as the list and supervisor endpoints return it. SupervisorID is null when none.
type GymMemberView struct {
	UserID         string    `json:"user_id"`
	Firstname      string    `json:"firstname"`
	Lastname       string    `json:"lastname"`
	ProfilePicture string    `json:"profile_picture,omitempty"`
	Role           string    `json:"role"`
	SupervisorID   *string   `json:"supervisor_id"`
	Status         string    `json:"status"`
	SeatAssigned   bool      `json:"seat_assigned"`
	JoinedAt       time.Time `json:"joined_at"`
}

func gymMemberView(r *db.GymMemberRow) GymMemberView {
	m := &r.GymMember
	v := GymMemberView{
		UserID: m.UserID, Firstname: r.Firstname, Lastname: r.Lastname, ProfilePicture: r.ProfilePicture,
		Role: string(m.Role), Status: string(m.Status), SeatAssigned: m.SeatAssigned, JoinedAt: m.JoinedAt,
	}
	if m.SupervisorID != "" {
		sup := m.SupervisorID
		v.SupervisorID = &sup
	}
	return v
}

// GymMembersService lists gym members and changes supervisors (flat hierarchy, GYM_FLAT_HIERARCHY).
type GymMembersService interface {
	// ListMembers: the owner sees every member, a head coach only himself and the coaches he supervises.
	ListMembers(ctx context.Context, callerID, gymID, role string, page, size int) ([]GymMemberView, int64, *models.ApiError)
	// SetSupervisor sets or clears (empty) the supervisor of a coach; owner only.
	SetSupervisor(ctx context.Context, callerID, gymID, userID, supervisorID string) (*GymMemberView, *models.ApiError)
}

type gymMembersService struct {
	authz   Authorizer
	members GymMembersStore
	team    SupervisionTeamStore
	enabled bool
}

// NewGymMembersService wires the service; enabled=false (flag off) answers 404 everywhere.
func NewGymMembersService(authz Authorizer, members GymMembersStore, team SupervisionTeamStore, enabled bool) GymMembersService {
	return &gymMembersService{authz: authz, members: members, team: team, enabled: enabled}
}

// memberScopes are the capabilities that grant a member list view, strongest first; scope maps the caller to the
// repository filter ("" means every member).
var memberScopes = []struct {
	cap   Capability
	scope func(callerID string) string
}{
	{CapGymManage, func(string) string { return "" }},
	{CapGymHead, func(callerID string) string { return callerID }},
}

func (s *gymMembersService) internal(op string, err error) *models.ApiError {
	zap.L().Error("gym members failure", zap.String("component", "gym_members"), zap.String("op", op), zap.Error(err))
	return teamErr(http.StatusInternalServerError, errInternal)
}

func (s *gymMembersService) ListMembers(ctx context.Context, callerID, gymID, role string, page, size int) ([]GymMemberView, int64, *models.ApiError) {
	if !s.enabled {
		return nil, 0, teamErr(http.StatusNotFound, errMembersDisabled)
	}
	for _, ms := range memberScopes {
		d, apiErr := s.authz.Can(ctx, Actor{ID: callerID}, ms.cap, Resource{Type: ResourceGym, ID: gymID, GymID: gymID})
		if apiErr != nil {
			return nil, 0, apiErr
		}
		if d.Reason == ReasonNotMember {
			return nil, 0, teamErr(http.StatusNotFound, errGymNotFound)
		}
		if !d.Allowed {
			continue
		}
		ctx, cancel := context.WithTimeout(ctx, authzTimeout)
		defer cancel()
		rows, total, err := s.members.ListByGymPage(ctx, gymID, ms.scope(callerID), role, page, size)
		if err != nil {
			return nil, 0, s.internal("ListMembers", err)
		}
		out := make([]GymMemberView, 0, len(rows))
		for i := range rows {
			out = append(out, gymMemberView(&rows[i]))
		}
		return out, total, nil
	}
	return nil, 0, teamErr(http.StatusForbidden, errMembersAccess)
}

// requireManage: 404 for a non-member, 403 for a member without gym.manage (the owner).
func (s *gymMembersService) requireManage(ctx context.Context, callerID, gymID string) *models.ApiError {
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

// validSupervisor checks the new supervisor: not the target itself, an active head coach row of the same gym.
func (s *gymMembersService) validSupervisor(ctx context.Context, gymID, userID, supervisorID string) *models.ApiError {
	if supervisorID == userID {
		return teamErr(http.StatusBadRequest, errSupervisorCycle)
	}
	sup, err := s.members.Get(ctx, supervisorID, gymID)
	if err != nil {
		return s.internal("SetSupervisor.supervisor", err)
	}
	if sup == nil || sup.Role != db.GymMemberRoleHeadCoach {
		return teamErr(http.StatusBadRequest, errSupervisorInvalid)
	}
	if sup.SupervisorID == userID {
		return teamErr(http.StatusBadRequest, errSupervisorCycle)
	}
	return nil
}

// project keeps coach_team_members (and its membership periods, closed and opened by the team repository) in step
// with supervisor_id: a supervisor means a TeamMember row, none means no row.
func (s *gymMembersService) project(ctx context.Context, callerID, userID, supervisorID string, at time.Time) error {
	if supervisorID == "" {
		_, err := s.team.DeleteByCoach(ctx, userID)
		return err
	}
	_, err := s.team.Upsert(ctx, &db.TeamMember{
		HeadCoachID: supervisorID, CoachID: userID, AssignedBy: callerID,
		AssignedByRole: db.TeamAssignerGymOwner, AssignedAt: at,
	})
	return err
}

func (s *gymMembersService) SetSupervisor(ctx context.Context, callerID, gymID, userID, supervisorID string) (*GymMemberView, *models.ApiError) {
	if !s.enabled {
		return nil, teamErr(http.StatusNotFound, errMembersDisabled)
	}
	if apiErr := s.requireManage(ctx, callerID, gymID); apiErr != nil {
		return nil, apiErr
	}
	ctx, cancel := context.WithTimeout(ctx, authzTimeout)
	defer cancel()
	target, err := s.members.Get(ctx, userID, gymID)
	if err != nil {
		return nil, s.internal("SetSupervisor.target", err)
	}
	if target == nil {
		return nil, teamErr(http.StatusNotFound, errMemberNotFound)
	}
	if target.Role != db.GymMemberRoleCoach {
		return nil, teamErr(http.StatusBadRequest, errSupervisorTarget)
	}
	if supervisorID != "" {
		if apiErr := s.validSupervisor(ctx, gymID, userID, supervisorID); apiErr != nil {
			return nil, apiErr
		}
	}
	prev := target.SupervisorID
	if prev == supervisorID {
		return s.memberView(ctx, gymID, target)
	}

	// Compare-and-set on the supervisor we read: a concurrent change makes it match nothing (409).
	ok, err := s.members.SetSupervisor(ctx, gymID, userID, prev, supervisorID)
	if err != nil {
		return nil, s.internal("SetSupervisor.set", err)
	}
	if !ok {
		return nil, teamErr(http.StatusConflict, errSupervisorRace)
	}
	if supervisorID != "" {
		// The supervisor may have left or been demoted between the check and the write.
		sup, err := s.members.Get(ctx, supervisorID, gymID)
		if err != nil {
			s.revertSupervisor(ctx, gymID, userID, supervisorID, prev)
			return nil, s.internal("SetSupervisor.recheck", err)
		}
		if sup == nil || sup.Role != db.GymMemberRoleHeadCoach {
			s.revertSupervisor(ctx, gymID, userID, supervisorID, prev)
			return nil, teamErr(http.StatusConflict, errSupervisorRace)
		}
	}
	now := time.Now()
	if err := s.project(ctx, callerID, userID, supervisorID, now); err != nil {
		// Restore the previous supervisor so gym_members and the TeamMember projection do not diverge.
		s.revertSupervisor(ctx, gymID, userID, supervisorID, prev)
		return nil, s.internal("SetSupervisor.project", err)
	}
	if err := s.team.InsertAudit(ctx, &db.TeamAuditEntry{
		Action: db.TeamActionSupervisorChange, ActorID: callerID, ActorRole: db.TeamActorRoleGymOwner,
		HeadCoachID: supervisorID, PrevHeadCoachID: prev, CoachID: userID, GymID: gymID,
	}); err != nil {
		// The change already happened; the failure is logged, not surfaced.
		zap.L().Error("supervisor audit write failed", zap.String("component", "gym_members"),
			zap.String("gym_id", gymID), zap.Error(err))
	}
	target.SupervisorID = supervisorID
	return s.memberView(ctx, gymID, target)
}

// revertSupervisor undoes our write, only while the row still holds the value we wrote (a concurrent owner change
// is kept). It runs on a detached context so a cancelled request cannot leave the change half applied.
func (s *gymMembersService) revertSupervisor(ctx context.Context, gymID, userID, written, prev string) {
	rctx, cancel := detachedContext(ctx)
	defer cancel()
	if _, err := s.members.SetSupervisor(rctx, gymID, userID, written, prev); err != nil {
		s.internal("SetSupervisor.revert", err)
	}
}

// memberView loads the member with its profile fields; when the row cannot be read the view falls back to the
// membership alone (the change itself already succeeded).
func (s *gymMembersService) memberView(ctx context.Context, gymID string, m *db.GymMember) (*GymMemberView, *models.ApiError) {
	row, err := s.members.GetRow(ctx, gymID, m.UserID)
	if err != nil || row == nil {
		if err != nil {
			s.internal("SetSupervisor.view", err)
		}
		row = &db.GymMemberRow{GymMember: *m}
	}
	v := gymMemberView(row)
	return &v, nil
}
