package repositories

import (
	"context"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/bson"

	"coachify-account-api/core"
	"coachify-account-api/models"
	"coachify-account-api/models/db"
)

// issueCodeQuery builds the filter and update that store a new code at field without touching
// the attempt budget. prevIssued guards against two concurrent regenerations both passing the
// cooldown: the update only matches while the stored lastissuedat is still the one the caller read.
// hadCode=false (no usable prior code) writes the whole sub-document, since a null field cannot
// take dot-notation sets.
func issueCodeQuery(email, field string, hadCode bool, prevIssued time.Time, code string, expiry time.Time, b core.CodeBudget, resetAttempts bool, now time.Time) (bson.M, bson.M) {
	if !hadCode {
		return bson.M{"email": email}, bson.M{"$set": bson.M{
			field: bson.M{
				"code": code, "expirationdate": expiry, "attempts": 0, "lockeduntil": time.Time{},
				"lastissuedat": b.LastIssuedAt, "regenwindowstart": b.RegenWindowStart, "regencount": b.RegenCount,
			},
			"updated_at": now,
		}}
	}
	filter := bson.M{"email": email, "$or": []bson.M{
		{field + ".lastissuedat": prevIssued},
		{field + ".lastissuedat": bson.M{"$exists": false}},
	}}
	set := bson.M{
		field + ".code":             code,
		field + ".expirationdate":   expiry,
		field + ".lastissuedat":     b.LastIssuedAt,
		field + ".regenwindowstart": b.RegenWindowStart,
		field + ".regencount":       b.RegenCount,
		"updated_at":                now,
	}
	if resetAttempts {
		set[field+".attempts"] = 0
		set[field+".lockeduntil"] = time.Time{}
	}
	return filter, bson.M{"$set": set}
}

func (r *UserRepository) issueCode(ctx context.Context, email, field string, hadCode bool, prevIssued time.Time, code string, expiry time.Time, b core.CodeBudget, resetAttempts bool) (bool, *models.ApiError) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	filter, update := issueCodeQuery(email, field, hadCode, prevIssued, code, expiry, b, resetAttempts, time.Now())
	res, err := r.collection.UpdateOne(ctx, filter, update)
	if err != nil {
		return false, &models.ApiError{Code: http.StatusInternalServerError, Error: models.ErrInternalError}
	}
	return res.MatchedCount > 0, nil
}

// IssueConfirmCode stores a new confirmation code, keeping the attempt budget. It returns false
// when a concurrent regeneration won the race.
func (r *UserRepository) IssueConfirmCode(ctx context.Context, email string, hadCode bool, prev time.Time, code string, expiry time.Time, b core.CodeBudget, resetAttempts bool) (bool, *models.ApiError) {
	return r.issueCode(ctx, email, "confirm_code", hadCode, prev, code, expiry, b, resetAttempts)
}

// IssueResetCode is IssueConfirmCode for the password-reset code.
func (r *UserRepository) IssueResetCode(ctx context.Context, email string, hadCode bool, prev time.Time, code string, expiry time.Time, b core.CodeBudget, resetAttempts bool) (bool, *models.ApiError) {
	return r.issueCode(ctx, email, "reset_password_code", hadCode, prev, code, expiry, b, resetAttempts)
}

// clearBudget resets the attempt counter and lockout after a successful check.
func (r *UserRepository) clearBudget(ctx context.Context, email, field string) *models.ApiError {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	update := bson.M{"$set": bson.M{field + ".attempts": 0, field + ".lockeduntil": time.Time{}}}
	if _, err := r.collection.UpdateOne(ctx, bson.M{"email": email, field: bson.M{"$ne": nil}}, update); err != nil {
		return &models.ApiError{Code: http.StatusInternalServerError, Error: models.ErrInternalError}
	}
	return nil
}

func (r *UserRepository) ClearResetBudget(ctx context.Context, email string) *models.ApiError {
	return r.clearBudget(ctx, email, "reset_password_code")
}

func (r *UserRepository) ClearConfirmBudget(ctx context.Context, email string) *models.ApiError {
	return r.clearBudget(ctx, email, "confirm_code")
}

// ResetUnverifiedForOAuth wipes everything an unverified registrant could hold (password hash,
// codes, sessions) and marks the account verified, before a verified provider identity is linked.
func (r *UserRepository) ResetUnverifiedForOAuth(ctx context.Context, email string, status db.UserStatus) *models.ApiError {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	update := bson.M{"$set": bson.M{
		"password":            "",
		"confirm_code":        nil,
		"reset_password_code": nil,
		"refresh_token":       nil,
		"token":               nil,
		"verification_status": true,
		"status":              status,
		"updated_at":          time.Now(),
	}}
	res, err := r.collection.UpdateOne(ctx, bson.M{"email": email}, update)
	if err != nil {
		return &models.ApiError{Code: http.StatusInternalServerError, Error: models.ErrUpdateUser}
	}
	if res.MatchedCount == 0 {
		return &models.ApiError{Code: http.StatusNotFound, Error: models.ErrUserNotFound}
	}
	return nil
}
