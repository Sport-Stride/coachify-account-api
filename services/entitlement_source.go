package services

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"time"

	"go.uber.org/zap"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/pkg/payments"
)

// Entitlement contexts (section 5.1): personal work uses the coach's own plan, gym work uses the gym subscription.
const (
	EntContextPersonal = "personal"
	EntContextGym      = "gym"

	// Layer sources of the context resolution.
	SourceGymOwner               = "gym_owner"
	SourceGymSeat                = "gym_seat"
	SourceGrandfather            = "grandfather"
	SourceNoPersonalSubscription = "no_personal_subscription"
	SourceNoGymSeat              = "no_gym_seat"
	SourcePersonalSubscription   = "personal_subscription"

	// Gym subscription summary statuses of payments-api.
	gymStatusActive = "active"
	gymStatusGrace  = "grace"
	gymStatusLapsed = "lapsed"
	gymStatusNone   = "none"

	gymSummaryTTL = 60 * time.Second
)

var (
	errEntContext = errors.New("context must be personal or gym")
	errEntGymID   = errors.New("gym_id is required for the gym context")
)

// EntContext names where a check runs: personal, or one gym.
type EntContext struct {
	Kind  string
	GymID string
}

// entCacheKey is the cache key of a context: personal:<user> or gym:<gym>. The context is part of the key, so a
// personal result is never served for a gym check and the other way round.
func entCacheKey(c EntContext, userID string) string {
	if c.Kind == EntContextGym {
		return EntContextGym + ":" + c.GymID
	}
	return EntContextPersonal + ":" + userID
}

// ContextEntitlements is the resolved access of one user in one context. Features are the enabled feature keys.
// SeatState is seated, grace, read_only or none (owners and the personal context report none).
type ContextEntitlements struct {
	Context   string   `json:"context"`
	Features  []string `json:"features"`
	SeatOnly  bool     `json:"seat_only"`
	SeatState string   `json:"seat_state"`
	Source    string   `json:"source"`
}

// EntitlementSource resolves a user's entitlements in the one context kind it serves (Strategy).
type EntitlementSource interface {
	Context() string
	Entitlements(ctx context.Context, userID string, c EntContext) (*ContextEntitlements, *models.ApiError)
}

// EntitlementSources dispatches by context kind through a map.
type EntitlementSources struct {
	sources map[string]EntitlementSource
}

// NewEntitlementSources registers each source under its context kind.
func NewEntitlementSources(srcs ...EntitlementSource) *EntitlementSources {
	m := make(map[string]EntitlementSource, len(srcs))
	for _, s := range srcs {
		m[s.Context()] = s
	}
	return &EntitlementSources{sources: m}
}

// Resolve runs the source of the context kind; an unknown kind is a 400.
func (r *EntitlementSources) Resolve(ctx context.Context, userID string, c EntContext) (*ContextEntitlements, *models.ApiError) {
	s, ok := r.sources[c.Kind]
	if !ok {
		return nil, models.NewApiError(http.StatusBadRequest, errEntContext)
	}
	return s.Entitlements(ctx, userID, c)
}

// ---- gym summary cache ----

// GymSummarySource reads gym subscription summaries; *payments.PaymentClient satisfies it.
type GymSummarySource interface {
	GymSummaries(ctx context.Context, gymIDs []string) ([]payments.GymSummary, error)
}

// GymSummaryCache reads the gym summary through singleflight and a short TTL cache keyed by the gym context.
// Errors are never cached.
type GymSummaryCache struct {
	src   GymSummarySource
	cache *readCache
}

// NewGymSummaryCache wraps src; a zero ttl uses 60 seconds.
func NewGymSummaryCache(src GymSummarySource, ttl time.Duration) *GymSummaryCache {
	if ttl <= 0 {
		ttl = gymSummaryTTL
	}
	return &GymSummaryCache{src: src, cache: newReadCache(ttl)}
}

// Invalidate drops every cached summary.
func (c *GymSummaryCache) Invalidate() { c.cache.purge() }

// Get returns the summary of a gym; a gym payments does not list is treated as "none". The result is shared:
// never mutate it.
func (c *GymSummaryCache) Get(ctx context.Context, gymID string) (*payments.GymSummary, *models.ApiError) {
	key := entCacheKey(EntContext{Kind: EntContextGym, GymID: gymID}, "")
	return cachedRead(c.cache, key, func() (*payments.GymSummary, *models.ApiError) {
		if c.src == nil {
			return noGymSummary(gymID), nil
		}
		list, err := c.src.GymSummaries(ctx, []string{gymID})
		if err != nil {
			return nil, entitlementUnavailable(err)
		}
		for i := range list {
			if list[i].GymID == gymID {
				s := list[i]
				return &s, nil
			}
		}
		return noGymSummary(gymID), nil
	})
}

func noGymSummary(gymID string) *payments.GymSummary {
	return &payments.GymSummary{GymID: gymID, Status: gymStatusNone, Features: []string{}}
}

// ---- personal source ----

// personalResolver is the existing personal resolution (entitlementServiceImpl.resolve).
type personalResolver interface {
	resolve(ctx context.Context, userID string) (*Effective, *models.ApiError)
}

// GymCoachLookup tells whether a user is an active coach or head coach of some gym.
type GymCoachLookup interface {
	InGymAsCoach(ctx context.Context, userID string) (bool, error)
}

// PersonalSource is the personal context: the existing layered logic. A coach with no personal plan who sits in a gym
// is seat-only and gets nothing here (no_personal_subscription).
type PersonalSource struct {
	resolver personalResolver
	gyms     GymCoachLookup
}

// NewPersonalSource wires the source; gyms may be nil (nobody is seat-only).
func NewPersonalSource(r personalResolver, gyms GymCoachLookup) *PersonalSource {
	return &PersonalSource{resolver: r, gyms: gyms}
}

func (s *PersonalSource) Context() string { return EntContextPersonal }

// enabledFeatures lists the enabled feature keys, sorted.
func enabledFeatures(m map[string]FeatureState) []string {
	out := []string{}
	for k, st := range m {
		if st.Enabled {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// isSeatOnly is true for a coach (not a gym owner) in a gym who holds no personal plan.
func isSeatOnly(ctx context.Context, gyms GymCoachLookup, userID string, eff *Effective) (bool, *models.ApiError) {
	// planUnknown: payments failed, so "no plan" is not known. Fail open rather than deny.
	if eff.Plan != nil || eff.planUnknown || gyms == nil || eff.Role == db.RoleGymOwner || entitlementRole(eff.Role) != "coach" {
		return false, nil
	}
	in, err := gyms.InGymAsCoach(ctx, userID)
	if err != nil {
		return false, entitlementUnavailable(err)
	}
	return in, nil
}

func (s *PersonalSource) Entitlements(ctx context.Context, userID string, _ EntContext) (*ContextEntitlements, *models.ApiError) {
	eff, apiErr := s.resolver.resolve(ctx, userID)
	if apiErr != nil {
		return nil, apiErr
	}
	only, apiErr := isSeatOnly(ctx, s.gyms, userID, eff)
	if apiErr != nil {
		return nil, apiErr
	}
	if only {
		return &ContextEntitlements{Context: EntContextPersonal, Features: []string{}, SeatOnly: true,
			SeatState: string(db.SeatStateNone), Source: SourceNoPersonalSubscription}, nil
	}
	return &ContextEntitlements{Context: EntContextPersonal, Features: enabledFeatures(eff.Features),
		SeatState: string(db.SeatStateNone), Source: SourcePersonalSubscription}, nil
}

// ---- gym source ----

// GymSeatStanding is what the gym source needs from the seat service.
type GymSeatStanding interface {
	Standing(ctx context.Context, gymID, userID string) (*SeatStanding, *models.ApiError)
}

// GymPersonalFunc returns a user's personal features and whether he holds a personal plan (true when unknown).
type GymPersonalFunc func(ctx context.Context, userID string) (features []string, hasPlan bool, apiErr *models.ApiError)

// GymDefaultsFunc loads the plan defaults (layer 2 of the gym features).
type GymDefaultsFunc func(ctx context.Context) (map[string]db.PlanDefault, *models.ApiError)

// GymSource is the gym:<id> context: the owner gets the gym plan features and consumes no seat, a seated coach gets
// them while seated or in grace, a grandfathered member keeps his personal features, anyone else gets nothing.
type GymSource struct {
	seats    GymSeatStanding
	personal GymPersonalFunc
	defaults GymDefaultsFunc
}

// NewGymSource wires the source; personal supplies the grandfathered personal features (nil: none) and defaults the
// plan defaults (nil: the payments summary features are used).
func NewGymSource(seats GymSeatStanding, personal GymPersonalFunc, defaults GymDefaultsFunc) *GymSource {
	return &GymSource{seats: seats, personal: personal, defaults: defaults}
}

// gymFeatures resolves the gym features: the plan default of the gym plan id, else the admin-editable _gym_seat
// default, else the features payments reports in the summary.
func (s *GymSource) gymFeatures(ctx context.Context, sum *payments.GymSummary) []string {
	if s.defaults != nil {
		defs, apiErr := s.defaults(ctx)
		if apiErr != nil {
			zap.L().Warn("gym plan defaults unavailable, using summary features", zap.String("component", "gym_entitlements"), zap.String("gym_id", sum.GymID))
		} else {
			key := ""
			if _, ok := defs[sum.PlanID]; ok && sum.PlanID != "" {
				key = sum.PlanID
			} else if _, ok := defs[db.PlanDefaultGymSeat]; ok {
				key = db.PlanDefaultGymSeat
			}
			if key != "" {
				return enabledFeatures(computeFeatures(resolveInput{role: "coach", planID: key, defaults: defs}))
			}
		}
	}
	return append([]string{}, sum.Features...)
}

// grandfathered fills the personal features of a member whose gym gives him no seat; he keeps them only with a
// personal plan.
func (s *GymSource) grandfathered(ctx context.Context, userID string, out *ContextEntitlements) *models.ApiError {
	if s.personal == nil {
		return nil
	}
	f, hasPlan, apiErr := s.personal(ctx, userID)
	if apiErr != nil {
		return apiErr
	}
	if hasPlan {
		out.Source = SourceGrandfather
		out.Features = f
	}
	return nil
}

func (s *GymSource) Context() string { return EntContextGym }

func (s *GymSource) Entitlements(ctx context.Context, userID string, c EntContext) (*ContextEntitlements, *models.ApiError) {
	if c.GymID == "" {
		return nil, models.NewApiError(http.StatusBadRequest, errEntGymID)
	}
	out := &ContextEntitlements{Context: EntContextGym + ":" + c.GymID, Features: []string{},
		SeatState: string(db.SeatStateNone), Source: SourceNoGymSeat}
	st, apiErr := s.seats.Standing(ctx, c.GymID, userID)
	if apiErr != nil {
		return nil, apiErr
	}
	if st == nil || st.Member == nil {
		return out, nil
	}
	sum := st.Summary
	if st.Member.Role == db.GymMemberRoleOwner {
		out.Source = SourceGymOwner
		if sum.Status == gymStatusActive || sum.Status == gymStatusGrace {
			out.Features = s.gymFeatures(ctx, sum)
		}
		return out, nil
	}
	out.SeatState = string(st.State)
	switch {
	case st.Unlimited:
		if apiErr := s.grandfathered(ctx, userID, out); apiErr != nil {
			return nil, apiErr
		}
	case st.State == db.SeatStateSeated || st.State == db.SeatStateGrace:
		out.Source = SourceGymSeat
		out.Features = s.gymFeatures(ctx, sum)
	case st.State == db.SeatStateReadOnly:
		// No write feature is granted; the state tells the consumer it may still allow reads.
		out.Source = SourceGymSeat
	case st.Grandfather:
		// Unseated: keeps the personal features while the grandfather flag is on.
		if apiErr := s.grandfathered(ctx, userID, out); apiErr != nil {
			return nil, apiErr
		}
	}
	return out, nil
}

// ContextEntitlementResolver is what the internal entitlements endpoint serves; *EntitlementSources satisfies it.
type ContextEntitlementResolver interface {
	Resolve(ctx context.Context, userID string, c EntContext) (*ContextEntitlements, *models.ApiError)
}

const contextEntitlementsTTL = 15 * time.Second

// CachedContextResolver caches the gym context result per gym and user for a short TTL. Errors are never cached.
// The personal context is not cached here: the personal resolver has its own cache. A seat change purges it.
type CachedContextResolver struct {
	next  ContextEntitlementResolver
	cache *readCache
}

// NewCachedContextResolver wraps next; a zero ttl uses 15 seconds.
func NewCachedContextResolver(next ContextEntitlementResolver, ttl time.Duration) *CachedContextResolver {
	if ttl <= 0 {
		ttl = contextEntitlementsTTL
	}
	return &CachedContextResolver{next: next, cache: newReadCache(ttl)}
}

// Invalidate drops every cached result.
func (c *CachedContextResolver) Invalidate() { c.cache.purge() }

// Resolve serves the gym context from the cache keyed gym:<id>:<user>; other contexts pass through.
func (c *CachedContextResolver) Resolve(ctx context.Context, userID string, ec EntContext) (*ContextEntitlements, *models.ApiError) {
	if ec.Kind != EntContextGym || ec.GymID == "" {
		return c.next.Resolve(ctx, userID, ec)
	}
	key := entCacheKey(ec, "") + ":" + userID
	return cachedRead(c.cache, key, func() (*ContextEntitlements, *models.ApiError) {
		return c.next.Resolve(ctx, userID, ec)
	})
}
