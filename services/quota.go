package services

import (
	"context"
	"net/http"

	"go.uber.org/zap"

	"coachify-account-api/models"
	"coachify-account-api/repositories"
)

// Client quota kinds. A personal client counts against the coach's own max_clients; a gym client counts only against
// the gym quota, assigned or not, and never against the coach's personal limit (a dual client counts once in each).
const (
	QuotaKindPersonal = "personal"
	QuotaKindGym      = "gym"

	// LimitMaxGymClients names the gym-level limit in quota errors.
	LimitMaxGymClients = "max_gym_clients"
)

// QuotaGuard answers whether one more client of the given kind fits. It is a pre-redeem guard: it must be able to
// block the redemption, so it is not an observer.
type QuotaGuard interface {
	Check(ctx context.Context, kind, coachID, gymID string) *models.ApiError
}

// PersonalQuotaChecker is the personal quota: coach_clients counted by the unchanged CountClients behind
// CheckInvitations.
type PersonalQuotaChecker interface {
	CheckInvitations(ctx context.Context, coachID string) *models.ApiError
}

// GymQuotaStore reads what the gym quota needs: the active gym client count and the admin override of the cap.
type GymQuotaStore interface {
	CountActive(ctx context.Context, gymID string) (int64, error)
	MaxGymClientsOverride(ctx context.Context, gymID string) (*int, error)
}

var _ GymQuotaStore = (*repositories.GymClientRepository)(nil)

// GymClientLimit supplies the cap that comes from the gym's subscription plan (payments-api). nil result means the
// plan sets no cap. It is optional: until payments exposes it, only the admin override applies.
type GymClientLimit interface {
	MaxGymClients(ctx context.Context, gymID string) (*int64, error)
}

// quotaGuard is the Composite: one guard per kind, dispatched through a map.
type quotaGuard struct {
	guards map[string]func(ctx context.Context, coachID, gymID string) *models.ApiError
}

// NewQuotaGuard composes the personal and the gym quota. plan may be nil.
func NewQuotaGuard(personal PersonalQuotaChecker, gym GymQuotaStore, plan GymClientLimit) QuotaGuard {
	g := &gymQuota{store: gym, plan: plan}
	return &quotaGuard{guards: map[string]func(context.Context, string, string) *models.ApiError{
		QuotaKindPersonal: func(ctx context.Context, coachID, _ string) *models.ApiError {
			if personal == nil {
				return nil
			}
			return personal.CheckInvitations(ctx, coachID)
		},
		QuotaKindGym: func(ctx context.Context, _, gymID string) *models.ApiError { return g.check(ctx, gymID) },
	}}
}

func (q *quotaGuard) Check(ctx context.Context, kind, coachID, gymID string) *models.ApiError {
	fn, ok := q.guards[kind]
	if !ok {
		return teamErr(http.StatusBadRequest, errQuotaKind)
	}
	return fn(ctx, coachID, gymID)
}

type gymQuota struct {
	store GymQuotaStore
	plan  GymClientLimit
}

// effectiveLimit is the admin override when set, else the plan cap; nil means unlimited.
func (g *gymQuota) effectiveLimit(ctx context.Context, gymID string) (*int64, error) {
	override, err := g.store.MaxGymClientsOverride(ctx, gymID)
	if err != nil {
		return nil, err
	}
	if override != nil {
		v := int64(*override)
		return &v, nil
	}
	if g.plan == nil {
		return nil, nil
	}
	return g.plan.MaxGymClients(ctx, gymID)
}

func (g *gymQuota) check(ctx context.Context, gymID string) *models.ApiError {
	limit, err := g.effectiveLimit(ctx, gymID)
	if err != nil {
		zap.L().Error("gym quota lookup failed", zap.String("component", "quota"), zap.String("gym_id", gymID), zap.Error(err))
		return teamErr(http.StatusInternalServerError, errInternal)
	}
	if limit == nil {
		return nil
	}
	used, err := g.store.CountActive(ctx, gymID)
	if err != nil {
		zap.L().Error("gym quota count failed", zap.String("component", "quota"), zap.String("gym_id", gymID), zap.Error(err))
		return teamErr(http.StatusInternalServerError, errInternal)
	}
	if used >= *limit {
		return quotaExceeded(FeatureInvitations, LimitMaxGymClients, used, *limit)
	}
	return nil
}
