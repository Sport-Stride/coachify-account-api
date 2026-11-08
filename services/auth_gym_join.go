package services

import (
	"context"
	"net/http"
	"strings"

	"go.uber.org/zap"

	"coachify-account-api/models"
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

// joinGymAfterSignup joins the new coach to the gym. The account already exists, so a failure is only logged.
func (s *AuthServiceImpl) joinGymAfterSignup(ctx context.Context, userID, token string) {
	if apiErr := s.gymJoins.JoinOnSignup(ctx, userID, token); apiErr != nil {
		zap.L().Warn("gym join after signup failed",
			zap.String("component", "auth"),
			zap.String("user_id", userID),
			zap.Int("status", apiErr.Code),
		)
	}
}
