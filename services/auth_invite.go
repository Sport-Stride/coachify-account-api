package services

import (
	"context"

	"go.uber.org/zap"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
)

// inviteSignupRedeemer is the part of the invite link service the signup paths use (INVITE_LINKS_V2).
type inviteSignupRedeemer interface {
	// PrepareSignup validates the token (hash first, then the legacy raw token) and runs the kind's guard.
	PrepareSignup(ctx context.Context, token, email string) (*db.InviteLink, *models.ApiError)
	// CompleteSignup redeems the link for the account that was just created.
	CompleteSignup(ctx context.Context, l *db.InviteLink, u *db.User) *models.ApiError
	// AbortSignup releases the use reserved by PrepareSignup when the signup fails.
	AbortSignup(ctx context.Context, l *db.InviteLink)
	// AcceptAfterConfirm accepts the invites the account signed up with once it confirmed its email.
	AcceptAfterConfirm(ctx context.Context, userID string)
}

// WithInviteLinks sets the invite link redeemer used by the client signup paths; nil keeps the legacy paths.
func (s *AuthServiceImpl) WithInviteLinks(r inviteSignupRedeemer) *AuthServiceImpl {
	s.invites = r
	return s
}

// completeInviteSignup redeems the link for a new account. The account already exists, so a failure is only logged
// (never with the token).
func (s *AuthServiceImpl) completeInviteSignup(ctx context.Context, l *db.InviteLink, u *db.User) {
	if apiErr := s.invites.CompleteSignup(ctx, l, u); apiErr != nil {
		zap.L().Warn("invite redeem after signup failed",
			zap.String("component", "auth"),
			zap.String("user_id", u.ExternalID),
			zap.String("kind", l.Kind),
			zap.Int("status", apiErr.Code),
		)
	}
}
