package services

import (
	"context"
	"net/http"
	"time"

	"coachify-account-api/core"
	"coachify-account-api/models"
	"coachify-account-api/models/db"
)

// issuePlan is the outcome of planning a code regeneration for one account.
type issuePlan struct {
	ok      bool
	hadCode bool
	prev    time.Time
	budget  core.CodeBudget
	reset   bool
}

func planConfirmIssue(old *db.UserConfirmCode, now time.Time) issuePlan {
	if old == nil {
		b, reset, ok := core.PlanIssue(core.CodeBudget{}, now)
		return issuePlan{ok: ok, budget: b, reset: reset}
	}
	b, reset, ok := core.PlanIssue(core.CodeBudget{
		LastIssuedAt: old.LastIssuedAt, RegenWindowStart: old.RegenWindowStart,
		RegenCount: old.RegenCount, LockedUntil: old.LockedUntil,
	}, now)
	return issuePlan{ok: ok, hadCode: true, prev: old.LastIssuedAt, budget: b, reset: reset}
}

func planResetIssue(old db.UserResetPasswordCode, now time.Time) issuePlan {
	had := old.Code != "" || !old.ExpirationDate.IsZero() || !old.LastIssuedAt.IsZero()
	b, reset, ok := core.PlanIssue(core.CodeBudget{
		LastIssuedAt: old.LastIssuedAt, RegenWindowStart: old.RegenWindowStart,
		RegenCount: old.RegenCount, LockedUntil: old.LockedUntil,
	}, now)
	return issuePlan{ok: ok, hadCode: had, prev: old.LastIssuedAt, budget: b, reset: reset}
}

// issueConfirmCode generates and stores a new confirmation code unless the cooldown, hourly cap
// or a lockout forbids it. issued=false means the caller must answer with the normal generic
// success and send nothing. Attempts are kept, so resending never refills the guess budget.
func (s AuthServiceImpl) issueConfirmCode(ctx context.Context, email string, old *db.UserConfirmCode) (bool, *models.ApiError) {
	now := time.Now()
	p := planConfirmIssue(old, now)
	if !p.ok {
		return false, nil
	}
	issued, apiErr := s.userRepository.IssueConfirmCode(ctx, email, p.hadCode, p.prev,
		s.activationManager.GenerateCode(), now.Add(core.ConfirmCodeTTL), p.budget, p.reset)
	if apiErr != nil {
		return false, apiErr
	}
	return issued, nil
}

// issueResetCode is issueConfirmCode for the password-reset code; it returns the new code.
func (s AuthServiceImpl) issueResetCode(ctx context.Context, email string, old db.UserResetPasswordCode) (string, *models.ApiError) {
	now := time.Now()
	p := planResetIssue(old, now)
	if !p.ok {
		return "", nil
	}
	code := s.activationManager.GenerateCode()
	issued, apiErr := s.userRepository.IssueResetCode(ctx, email, p.hadCode, p.prev, code, now.Add(core.ResetCodeTTL), p.budget, p.reset)
	if apiErr != nil || !issued {
		return "", apiErr
	}
	return code, nil
}

// confirmOps and resetOps wire the repository into core.VerifyCode.
func (s AuthServiceImpl) confirmOps(ctx context.Context, email string) core.CodeOps {
	return core.CodeOps{
		Consume: func() (string, time.Time, int, bool, error) {
			c, apiErr := s.userRepository.ConsumeConfirmAttempt(ctx, email)
			if apiErr != nil {
				return "", time.Time{}, 0, false, apiErr.Error
			}
			if c == nil {
				return "", time.Time{}, 0, false, nil
			}
			return c.Code, c.ExpirationDate, c.Attempts, true, nil
		},
		Lock: func(until time.Time) error {
			if e := s.userRepository.LockConfirmCode(ctx, email, until); e != nil {
				return e.Error
			}
			return nil
		},
		Clear: func() error {
			if e := s.userRepository.ClearConfirmBudget(ctx, email); e != nil {
				return e.Error
			}
			return nil
		},
	}
}

func (s AuthServiceImpl) resetOps(ctx context.Context, email string) core.CodeOps {
	return core.CodeOps{
		Consume: func() (string, time.Time, int, bool, error) {
			c, apiErr := s.userRepository.ConsumeResetAttempt(ctx, email)
			if apiErr != nil {
				return "", time.Time{}, 0, false, apiErr.Error
			}
			if c == nil {
				return "", time.Time{}, 0, false, nil
			}
			return c.Code, c.ExpirationDate, c.Attempts, true, nil
		},
		Lock: func(until time.Time) error {
			if e := s.userRepository.LockResetCode(ctx, email, until); e != nil {
				return e.Error
			}
			return nil
		},
		Clear: func() error {
			if e := s.userRepository.ClearResetBudget(ctx, email); e != nil {
				return e.Error
			}
			return nil
		},
	}
}

func internalErr() *models.ApiError {
	return &models.ApiError{Code: http.StatusInternalServerError, Error: models.ErrInternalError}
}
