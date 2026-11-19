package services

import (
	"context"
	"errors"
	"net/http"
	"time"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
)

var errMemberInviteDenied = errors.New("not allowed to invite members")

// inviteSpec describes who creates an invitation and what it joins as.
type inviteSpec struct {
	inviterID    string
	actorRole    string
	role         string // "" head coach, db.GymInviteRoleCoach coach
	supervisorID string
}

// detachedContext outlives a cancelled request but not a slow database; compensations run on it.
func detachedContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), authzTimeout)
}

// inAnyGymAsCoach reports whether the user has an active head coach or coach row in any gym.
func (s *gymServiceImpl) inAnyGymAsCoach(ctx context.Context, userID string) (bool, error) {
	rows, err := s.set.Members.ListByUser(ctx, userID)
	if err != nil {
		return false, err
	}
	for i := range rows {
		if rows[i].Role == db.GymMemberRoleHeadCoach || rows[i].Role == db.GymMemberRoleCoach {
			return true, nil
		}
	}
	return false, nil
}

func (s *gymServiceImpl) flatEnabled() bool {
	return s.set.FlatHierarchy && s.set.Members != nil
}

// coachInviter authorizes a coach invitation with the member.invite capability (owner always, head coach by preset)
// and fixes the supervisor: a head coach supervises his own invitees, the owner may name a head coach of the gym.
func (s *gymServiceImpl) coachInviter(ctx context.Context, callerID, gymID, supervisorID string) (*db.Gym, inviteSpec, *models.ApiError) {
	spec := inviteSpec{inviterID: callerID, role: db.GymInviteRoleCoach}
	if !s.flatEnabled() || s.set.Authz == nil {
		return nil, spec, teamErr(http.StatusNotFound, errGymNotFound)
	}
	d, apiErr := s.set.Authz.Can(ctx, Actor{ID: callerID}, CapMemberInvite, Resource{Type: ResourceGym, ID: gymID, GymID: gymID})
	if apiErr != nil {
		return nil, spec, apiErr
	}
	if d.Reason == ReasonNotMember {
		return nil, spec, teamErr(http.StatusNotFound, errGymNotFound)
	}
	if !d.Allowed {
		return nil, spec, teamErr(http.StatusForbidden, errMemberInviteDenied)
	}
	g, err := s.gyms.GetGym(ctx, gymID)
	if err != nil {
		return nil, spec, s.internal("coachInviter.gym", err)
	}
	if g == nil {
		return nil, spec, teamErr(http.StatusNotFound, errGymNotFound)
	}
	row, err := s.set.Members.Get(ctx, callerID, gymID)
	if err != nil {
		return nil, spec, s.internal("coachInviter.member", err)
	}
	if row == nil {
		return nil, spec, teamErr(http.StatusNotFound, errGymNotFound)
	}
	switch {
	case row.Role == db.GymMemberRoleOwner:
		spec.actorRole = db.TeamActorRoleGymOwner
		spec.supervisorID = supervisorID
		if supervisorID != "" {
			sup, err := s.set.Members.Get(ctx, supervisorID, gymID)
			if err != nil {
				return nil, spec, s.internal("coachInviter.supervisor", err)
			}
			if sup == nil || sup.Role != db.GymMemberRoleHeadCoach {
				return nil, spec, teamErr(http.StatusBadRequest, errSupervisorInvalid)
			}
		}
	case row.Role == db.GymMemberRoleHeadCoach:
		if supervisorID != "" && supervisorID != callerID {
			return nil, spec, teamErr(http.StatusBadRequest, errSupervisorInvalid)
		}
		spec.actorRole, spec.supervisorID = db.TeamActorRoleHead, callerID
	default:
		if supervisorID != "" {
			return nil, spec, teamErr(http.StatusBadRequest, errSupervisorInvalid)
		}
		spec.actorRole = db.TeamActorRoleCoach
	}
	return g, spec, nil
}

func (s *gymServiceImpl) InviteCoach(ctx context.Context, callerID, gymID, email, supervisorID string) (*GymInviteResult, *models.ApiError) {
	g, spec, apiErr := s.coachInviter(ctx, callerID, gymID, supervisorID)
	if apiErr != nil {
		return nil, apiErr
	}
	return s.invite(ctx, g, email, spec)
}

func (s *gymServiceImpl) CreateCoachJoinLink(ctx context.Context, callerID, gymID, supervisorID string) (*JoinLinkResult, *models.ApiError) {
	g, spec, apiErr := s.coachInviter(ctx, callerID, gymID, supervisorID)
	if apiErr != nil {
		return nil, apiErr
	}
	return s.createJoinLink(ctx, g, spec)
}

// joinGymAsCoach adds the coach to the gym directly (no head coach needed) and consumes the invitation.
// One gym per coach is enforced by the unique index: a duplicate is a 409, never a 500.
func (s *gymServiceImpl) joinGymAsCoach(ctx context.Context, u *db.User, inv *db.GymInvitation, now time.Time) (*GymJoined, *models.ApiError) {
	if !s.flatEnabled() {
		return nil, teamErr(http.StatusNotFound, errInviteNotFound)
	}
	callerID := u.ExternalID
	if u.IsHeadCoach {
		return nil, teamErr(http.StatusConflict, errHeadAsMember)
	}
	member, err := s.gyms.IsTeamMember(ctx, callerID)
	if err != nil {
		return nil, s.internal("AcceptCoach.isMember", err)
	}
	if member {
		return nil, teamErr(http.StatusConflict, errCoachHasTeam)
	}
	inGym, err := s.inAnyGymAsCoach(ctx, callerID)
	if err != nil {
		return nil, s.internal("AcceptCoach.memberships", err)
	}
	if inGym {
		return nil, teamErr(http.StatusConflict, errAlreadyInGym)
	}
	g, err := s.gyms.GetGym(ctx, inv.GymID)
	if err != nil {
		return nil, s.internal("AcceptCoach.gym", err)
	}
	if g == nil || g.Status != db.GymStatusActive {
		return nil, teamErr(http.StatusNotFound, errInviteNotFound)
	}
	gid := g.ID.Hex()
	// No free seat: the join fails and the invitation stays pending.
	if s.set.Seats != nil {
		if apiErr := s.set.Seats.Check(ctx, gid); apiErr != nil {
			return nil, apiErr
		}
	}

	// The supervisor named at invitation time may have left since: join without one then.
	supervisor := inv.SupervisorID
	if supervisor != "" {
		sup, err := s.set.Members.Get(ctx, supervisor, gid)
		if err != nil {
			return nil, s.internal("AcceptCoach.supervisor", err)
		}
		if sup == nil || sup.Role != db.GymMemberRoleHeadCoach {
			supervisor = ""
		}
	}

	claimed, err := s.gyms.TransitionInvitation(ctx, inv.ID.Hex(), db.GymInvitePending, db.GymInviteAccepted, callerID, now)
	if err != nil {
		return nil, s.internal("AcceptCoach.claim", err)
	}
	if !claimed {
		return nil, teamErr(http.StatusConflict, errInviteNotPending)
	}
	// revert undoes the claim and, when added, the membership; it runs on a detached context.
	revert := func(added bool) {
		rctx, cancel := detachedContext(ctx)
		defer cancel()
		if added {
			if _, err := s.set.Members.MarkLeft(rctx, gid, callerID, time.Now()); err != nil {
				s.internal("AcceptCoach.revertMember", err)
			}
		}
		if _, err := s.gyms.TransitionInvitation(rctx, inv.ID.Hex(), db.GymInviteAccepted, db.GymInvitePending, "", now); err != nil {
			s.internal("AcceptCoach.revertClaim", err)
		}
	}

	err = s.set.Members.AddActive(ctx, &db.GymMember{
		GymID: gid, UserID: callerID, Role: db.GymMemberRoleCoach, SupervisorID: supervisor,
		JoinedAt: now, AddedBy: inv.InviterID, Source: db.GymMemberSourceFlat,
	})
	if err != nil {
		revert(false)
		if errors.Is(err, repositories.ErrAlreadyMember) {
			return nil, teamErr(http.StatusConflict, errAlreadyInGym)
		}
		return nil, s.internal("AcceptCoach.add", err)
	}

	// Close the race with a concurrent team assignment (before our own projection creates a team row).
	member, err = s.gyms.IsTeamMember(ctx, callerID)
	if err != nil {
		revert(true)
		return nil, s.internal("AcceptCoach.recheck", err)
	}
	if member {
		revert(true)
		return nil, teamErr(http.StatusConflict, errCoachHasTeam)
	}

	if supervisor != "" && s.set.Projector != nil {
		_, err := s.set.Projector.Upsert(ctx, &db.TeamMember{
			HeadCoachID: supervisor, CoachID: callerID, AssignedBy: inv.InviterID,
			AssignedByRole: db.TeamAssignerGymOwner, AssignedAt: now,
		})
		if err != nil {
			revert(true)
			return nil, s.internal("AcceptCoach.project", err)
		}
	}
	if s.set.Seats != nil {
		s.set.Seats.OnJoined(ctx, gid, callerID)
	}
	s.auditMany(ctx, []*db.TeamAuditEntry{
		{Action: db.TeamActionMemberJoin, ActorID: callerID, ActorRole: db.TeamActorRoleCoach, CoachID: callerID, HeadCoachID: supervisor, GymID: gid},
		{Action: db.TeamActionGymInviteAccept, ActorID: callerID, ActorRole: db.TeamActorRoleCoach, CoachID: callerID, GymID: gid},
	})
	return &GymJoined{GymID: gid, Name: g.Name}, nil
}
