package services

import (
	"context"
	"net/http"
	"strings"

	"go.uber.org/zap"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
)

// gymJoinRedeemer is the part of the gym service the signup paths use for gym join links.
type gymJoinRedeemer interface {
	LookupJoin(ctx context.Context, token string) (*JoinLookup, *models.ApiError)
	JoinOnSignup(ctx context.Context, callerID, token string) *models.ApiError
}

// WithGymJoins sets the join link redeemer used by signup with a gym join token; nil disables those paths.
func (s *AuthServiceImpl) WithGymJoins(r gymJoinRedeemer) *AuthServiceImpl {
	s.gymJoins = r
	return s
}

// checkGymJoin validates a join token for a signup with the given email. It returns the email the link is bound to ("" for an open link).
func (s *AuthServiceImpl) checkGymJoin(ctx context.Context, token, email string) (string, *models.ApiError) {
	if s.gymJoins == nil {
		return "", inviteNotFound()
	}
	lk, apiErr := s.gymJoins.LookupJoin(ctx, token)
	if apiErr != nil {
		return "", apiErr
	}
	if lk == nil {
		return "", inviteNotFound()
	}
	if lk.Email != "" && !strings.EqualFold(strings.TrimSpace(email), lk.Email) {
		return "", &models.ApiError{Code: http.StatusForbidden, Error: models.ErrInviteEmailMismatch}
	}
	return lk.Email, nil
}

// joinGymAfterSignup joins the new coach to the gym and reports whether it worked. The account already exists, so a
// failure is only logged; the caller falls back to the default plan when it had skipped it.
func (s *AuthServiceImpl) joinGymAfterSignup(ctx context.Context, userID, token string) bool {
	if apiErr := s.gymJoins.JoinOnSignup(ctx, userID, token); apiErr != nil {
		zap.L().Warn("gym join after signup failed",
			zap.String("component", "auth"),
			zap.String("user_id", userID),
			zap.Int("status", apiErr.Code),
		)
		return false
	}
	return true
}

// WithSeatOnlySignup makes member-invite signups skip the default plan subscription (GYM_SEATS_ENABLED), so the new
// coach starts seat-only (plan 5.4). Off: every signup subscribes as before.
func (s *AuthServiceImpl) WithSeatOnlySignup(on bool) *AuthServiceImpl {
	s.seatOnlySignup = on
	return s
}

// skipDefaultPlan is true for a signup through a gym member invite (gym join link or member_coach/member_head_coach
// invite link) while seats are on.
func (s *AuthServiceImpl) skipDefaultPlan(gymJoin bool, link *db.InviteLink) bool {
	if !s.seatOnlySignup {
		return false
	}
	return gymJoin || (link != nil && (link.Kind == db.InviteKindMemberCoach || link.Kind == db.InviteKindMemberHeadCoach))
}
