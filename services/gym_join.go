package services

import (
	"context"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/utils"
)

// joinLink builds the client URL of a join link. The link is a credential: never log it.
func (s *gymServiceImpl) joinLink(raw string) string {
	return s.set.ClientURL + "/auth/signup?gym_join=" + raw
}

// sendJoinLink emails the join link to an email-bound invitation. A failure is logged (no token, link or address) and swallowed.
func (s *gymServiceImpl) sendJoinLink(ctx context.Context, inv *db.GymInvitation, gymName, raw string) {
	if s.set.Mailer == nil || inv.Email == "" {
		return
	}
	if err := s.set.Mailer.SendGymJoinInviteEmail(ctx, inv.Email, gymName, s.joinLink(raw)); err != nil {
		zap.L().Warn("gym join invite email failed",
			zap.String("component", "gym"),
			zap.String("invitation_id", inv.ID.Hex()),
			zap.Error(err),
		)
	}
}

// CreateJoinLink creates a single-use open invitation (no email) and returns its link; the raw token is returned only here.
func (s *gymServiceImpl) CreateJoinLink(ctx context.Context, callerID, gymID string) (*JoinLinkResult, *models.ApiError) {
	g, apiErr := s.ownedGym(ctx, callerID, gymID)
	if apiErr != nil {
		return nil, apiErr
	}
	if g.Status != db.GymStatusActive {
		return nil, teamErr(http.StatusConflict, errGymArchived)
	}
	now := time.Now()
	gid := g.ID.Hex()
	n, err := s.gyms.CountInvitationsSince(ctx, gid, now.Add(-invitesWindow))
	if err != nil {
		return nil, s.internal("CreateJoinLink.count", err)
	}
	if n >= s.set.InvitesPerDay {
		return nil, teamErr(http.StatusTooManyRequests, errInviteLimit)
	}
	raw, hash, err := utils.NewURLToken()
	if err != nil {
		return nil, s.internal("CreateJoinLink.token", err)
	}
	inv := &db.GymInvitation{
		GymID: gid, InviterID: callerID, TokenHash: hash, Status: db.GymInvitePending,
		ExpiresAt: now.Add(s.set.InviteTTL), CreatedAt: now,
	}
	if err := s.gyms.InsertInvitation(ctx, inv); err != nil {
		return nil, s.internal("CreateJoinLink.insert", err)
	}
	s.audit(ctx, &db.TeamAuditEntry{
		Action: db.TeamActionGymInvite, ActorID: callerID, ActorRole: db.TeamActorRoleGymOwner, GymID: gid,
	})
	return &JoinLinkResult{InvitationID: inv.ID.Hex(), Link: s.joinLink(raw), ExpiresAt: inv.ExpiresAt}, nil
}

// invitationByToken loads the invitation behind a raw join token; an unknown token is a 404.
func (s *gymServiceImpl) invitationByToken(ctx context.Context, op, token string) (*db.GymInvitation, *models.ApiError) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, teamErr(http.StatusNotFound, errInviteNotFound)
	}
	inv, err := s.gyms.GetInvitationByTokenHash(ctx, utils.HashToken(token))
	if err != nil {
		return nil, s.internal(op, err)
	}
	if inv == nil {
		return nil, teamErr(http.StatusNotFound, errInviteNotFound)
	}
	return inv, nil
}

// LookupJoin tells the join page which gym a token is for. Invalid, expired, revoked, used and archived-gym tokens give the same 404.
func (s *gymServiceImpl) LookupJoin(ctx context.Context, token string) (*JoinLookup, *models.ApiError) {
	inv, apiErr := s.invitationByToken(ctx, "LookupJoin.get", token)
	if apiErr != nil {
		return nil, apiErr
	}
	if inv.Status != db.GymInvitePending || !inv.ExpiresAt.After(time.Now()) {
		return nil, teamErr(http.StatusNotFound, errInviteNotFound)
	}
	g, err := s.gyms.GetGym(ctx, inv.GymID)
	if err != nil {
		return nil, s.internal("LookupJoin.gym", err)
	}
	if g == nil || g.Status != db.GymStatusActive {
		return nil, teamErr(http.StatusNotFound, errInviteNotFound)
	}
	return &JoinLookup{GymName: g.Name, Email: inv.Email}, nil
}

// AcceptByToken lets a coach join through a join link. An email-bound link only works for that email; an open link binds to the caller.
func (s *gymServiceImpl) AcceptByToken(ctx context.Context, callerID, token string) (*GymJoined, *models.ApiError) {
	u, apiErr := s.requireCoach(ctx, callerID)
	if apiErr != nil {
		return nil, apiErr
	}
	return s.acceptByToken(ctx, u, token)
}

// JoinOnSignup is AcceptByToken for a coach that has just signed up: the account is a coach but not necessarily Active yet.
func (s *gymServiceImpl) JoinOnSignup(ctx context.Context, callerID, token string) *models.ApiError {
	u, apiErr := s.loadUser(ctx, "JoinOnSignup.load", callerID, errCoachAccess)
	if apiErr != nil {
		return apiErr
	}
	if u.UserRole != "coach" || u.UserStatus.IsInactive() {
		return teamErr(http.StatusForbidden, errCoachAccess)
	}
	_, apiErr = s.acceptByToken(ctx, u, token)
	return apiErr
}

func (s *gymServiceImpl) acceptByToken(ctx context.Context, u *db.User, token string) (*GymJoined, *models.ApiError) {
	inv, apiErr := s.invitationByToken(ctx, "AcceptByToken.get", token)
	if apiErr != nil {
		return nil, apiErr
	}
	if inv.Email != "" && inv.Email != strings.ToLower(strings.TrimSpace(u.UserEmail)) {
		return nil, teamErr(http.StatusForbidden, models.ErrInviteEmailMismatch)
	}
	now := time.Now()
	if apiErr := s.pendingInvitation(ctx, inv, now); apiErr != nil {
		return nil, apiErr
	}
	return s.joinGym(ctx, u, inv, now)
}
