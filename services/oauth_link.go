package services

import (
	"strings"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
)

// normalizeClaimEmail makes provider-supplied emails comparable. Signup stores the address as typed and
// is not migrated here, so only the OAuth side is normalized.
func normalizeClaimEmail(e string) string {
	return strings.ToLower(strings.TrimSpace(e))
}

// oauthLinkPlan decides how a verified provider identity may attach to an existing local account.
// Inactive accounts are refused. An unconfirmed account, or one with a pending confirm/reset code,
// may have been pre-registered by someone who does not own the mailbox: it is taken over, meaning
// the password hash, codes and sessions are wiped before the provider identity is linked.
func oauthLinkPlan(u *db.User) (takeover bool, newStatus db.UserStatus, apiErr *models.ApiError) {
	if u.UserStatus.IsInactive() {
		return false, u.UserStatus, inactiveAccountError(u.UserStatus)
	}
	pending := (u.UserConfirmCode != nil && u.UserConfirmCode.Code != "") ||
		(u.UserResetPasswordCode != nil && u.UserResetPasswordCode.Code != "")
	if u.UserStatus != db.ToConfirm && !pending {
		return false, u.UserStatus, nil
	}
	newStatus = u.UserStatus
	if u.UserStatus == db.ToConfirm {
		if u.UserRole == "coach" {
			newStatus = db.ComReg1
		} else {
			newStatus = db.Active
		}
	}
	return true, newStatus, nil
}
