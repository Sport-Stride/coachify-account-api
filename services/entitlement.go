package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/zap"

	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
	"coachify-account-api/pkg/payments"
	"coachify-account-api/repositories"
)

const (
	entitlementTTLSeconds = 60
	entitlementCacheTTL   = 30 * time.Second
	entitlementPlanTTL    = 5 * time.Minute
	maxResolveIDs         = 100
	entitlementAuditLimit = 20

	auditActionEntitlementSet = "entitlement.set"
	auditActionPlanDefaultSet = "plan_default.set"
)

// PlanInfo is the subscription plan of a user as reported by payments-api.
type PlanInfo struct {
	PlanID           string     `json:"plan_id"`
	Name             string     `json:"name"`
	Status           string     `json:"status"`
	CurrentPeriodEnd *time.Time `json:"current_period_end,omitempty"`
}

// FeatureState is the resolved state of one feature. A nil limit is unlimited.
type FeatureState struct {
	Enabled bool              `json:"enabled"`
	Source  string            `json:"source"`
	Limits  map[string]*int64 `json:"limits"`
	Usage   map[string]int64  `json:"usage,omitempty"`
	// Month ("YYYY-MM") is the period of the monthly counters in Usage; it is emitted as usage.month.
	Month string `json:"-"`
}

// MarshalJSON keeps the usage object additive: monthly counters add a "month" string beside the numbers.
func (s FeatureState) MarshalJSON() ([]byte, error) {
	type plain FeatureState
	if s.Month == "" || s.Usage == nil {
		return json.Marshal(plain(s))
	}
	usage := make(map[string]interface{}, len(s.Usage)+1)
	for k, v := range s.Usage {
		usage[k] = v
	}
	usage["month"] = s.Month
	return json.Marshal(struct {
		plain
		Usage map[string]interface{} `json:"usage"`
	}{plain(s), usage})
}

// Effective is the resolved feature access of one user.
type Effective struct {
	UserID     string                  `json:"user_id"`
	Role       string                  `json:"role"`
	Plan       *PlanInfo               `json:"plan"`
	Features   map[string]FeatureState `json:"features"`
	TTLSeconds int                     `json:"ttl_seconds"`
}

// CoachAccessRow is one coach in the admin access views.
type CoachAccessRow struct {
	ExternalID  string                  `json:"external_id"`
	Firstname   string                  `json:"firstname"`
	Lastname    string                  `json:"lastname"`
	Email       string                  `json:"email"`
	Role        string                  `json:"role"`
	Status      string                  `json:"status"`
	IsHeadCoach bool                    `json:"is_head_coach"`
	LastLogin   *time.Time              `json:"last_login"`
	Plan        *PlanInfo               `json:"plan"`
	ClientCount int                     `json:"client_count"`
	Features    map[string]FeatureState `json:"features"`
}

// CoachAccessList is a page of coach access rows.
type CoachAccessList struct {
	Rows  []CoachAccessRow `json:"rows"`
	Total int              `json:"total"`
	Page  int              `json:"page"`
	Size  int              `json:"size"`
}

// CoachAccessDetail adds the raw overrides, the grant version and recent audit entries to a row.
type CoachAccessDetail struct {
	CoachAccessRow
	Overrides map[string]db.FeatureGrant `json:"overrides"`
	Version   int64                      `json:"version"`
	Audit     []db.AdminAuditEntry       `json:"audit"`
}

// ClientAccessRow is one linked client in the coach access views. Grantable holds the coach's
// effective values for features that apply to clients: the ceiling of what the coach may grant.
type ClientAccessRow struct {
	ExternalID string                  `json:"external_id"`
	Firstname  string                  `json:"firstname"`
	Lastname   string                  `json:"lastname"`
	Email      string                  `json:"email"`
	Status     string                  `json:"status"`
	LastLogin  *time.Time              `json:"last_login"`
	Plan       *PlanInfo               `json:"plan"`
	Features   map[string]FeatureState `json:"features"`
	Grantable  map[string]FeatureState `json:"grantable"`
}

// ClientAccessList is a page of client access rows.
type ClientAccessList struct {
	Rows      []ClientAccessRow       `json:"rows"`
	Total     int                     `json:"total"`
	Page      int                     `json:"page"`
	Size      int                     `json:"size"`
	Grantable map[string]FeatureState `json:"grantable"`
}

// ClientAccessDetail adds the raw overrides and grant version to a row.
type ClientAccessDetail struct {
	ClientAccessRow
	Overrides map[string]db.FeatureGrant `json:"overrides"`
	Version   int64                      `json:"version"`
}

// QuotaResult is the outcome of an allowed quota check.
type QuotaResult struct {
	Allowed bool   `json:"allowed"`
	Used    int64  `json:"used"`
	Limit   *int64 `json:"limit"`
}

// EntitlementService resolves, edits and enforces per-user feature access.
type EntitlementService interface {
	Features() []FeatureDef
	// Effective returns the resolved access of a user with usage attached.
	Effective(ctx context.Context, userID string) (*Effective, *models.ApiError)
	Resolve(ctx context.Context, userIDs []string) (map[string]*Effective, *models.ApiError)
	Consume(ctx context.Context, userID string, req api.UsageDeltaRequest) (map[string]int64, *models.ApiError)
	Release(ctx context.Context, userID string, req api.UsageDeltaRequest) *models.ApiError
	CheckQuota(ctx context.Context, userID string, req api.CheckQuotaRequest) (*QuotaResult, *models.ApiError)

	ListCoachAccess(ctx context.Context, q db.CoachAccessQuery) (*CoachAccessList, *models.ApiError)
	GetCoachAccess(ctx context.Context, coachID string) (*CoachAccessDetail, *models.ApiError)
	SetCoachAccess(ctx context.Context, actorID, coachID string, req api.UpdateAccessRequest) (*CoachAccessDetail, *models.ApiError)
	ListPlanDefaults(ctx context.Context) ([]db.PlanDefault, *models.ApiError)
	SetPlanDefault(ctx context.Context, actorID, planID string, req api.PlanDefaultRequest) (*db.PlanDefault, *models.ApiError)
	ListAudit(ctx context.Context, f db.AdminAuditFilter, page, size int) (*AdminAuditList, *models.ApiError)

	ListClientAccess(ctx context.Context, coachID string, page, size int, q string) (*ClientAccessList, *models.ApiError)
	GetClientAccess(ctx context.Context, coachID, clientID string) (*ClientAccessDetail, *models.ApiError)
	SetClientAccess(ctx context.Context, actorID, actorRole, coachID, clientID string, req api.UpdateAccessRequest) (*ClientAccessDetail, *models.ApiError)
}

// EntitlementGuard is the in-process check used by registration flows (no HTTP).
type EntitlementGuard interface {
	// CheckInvitations returns a 403 ApiError when the coach may not take on another client.
	CheckInvitations(ctx context.Context, coachID string) *models.ApiError
}

// EntitlementStore is the persistence the entitlement service needs; *repositories.EntitlementRepository satisfies it.
type EntitlementStore interface {
	GetGrant(ctx context.Context, subjectID string) (*db.EntitlementGrant, error)
	ReplaceGrant(ctx context.Context, g *db.EntitlementGrant, prevVersion int64) error
	ListPlanDefaults(ctx context.Context) ([]db.PlanDefault, error)
	UpsertPlanDefault(ctx context.Context, pd *db.PlanDefault) error
	GetUsage(ctx context.Context, ids []string) (map[string]*db.EntitlementUsage, error)
	ConsumeUsage(ctx context.Context, subjectID, feature string, deltas, maxes map[string]int64) (*db.EntitlementUsage, bool, error)
	ReleaseUsage(ctx context.Context, subjectID, feature string, deltas map[string]int64) error
	CountClients(ctx context.Context, coachID string) (int64, error)
	ListCoachAccess(ctx context.Context, q db.CoachAccessQuery) ([]db.CoachAccessRow, int, error)
	ListClientAccess(ctx context.Context, coachID string, page, size int, q string) ([]db.ClientAccessRow, int, error)
}

// EntitlementUserStore looks users up by external id.
type EntitlementUserStore interface {
	GetUserById(ctx context.Context, userID string) (*db.User, *models.ApiError)
}

// EntitlementCoachLinks answers coach-client link questions; CoachService satisfies it.
type EntitlementCoachLinks interface {
	GetCoachIDByClientID(ctx context.Context, clientID string) (string, error)
	IsClientOfCoach(ctx context.Context, coachID, clientID string) (bool, error)
}

// EntitlementPlanSource returns subscription summaries; *payments.PaymentClient satisfies it.
type EntitlementPlanSource interface {
	GetSubscriptionSummaries(ctx context.Context, userIDs []string) ([]payments.SubscriptionSummaryEntry, error)
}

type planEntry struct {
	plan    *PlanInfo
	expires time.Time
}

type entitlementServiceImpl struct {
	store   EntitlementStore
	users   EntitlementUserStore
	links   EntitlementCoachLinks
	plans   EntitlementPlanSource
	audit   AdminAuditStore
	cache   *readCache
	defs    *readCache
	now     func() time.Time
	planMu  sync.Mutex
	planMap map[string]planEntry
}

// NewEntitlementService wires the service. plans may be nil (every user then has no plan).
func NewEntitlementService(store EntitlementStore, users EntitlementUserStore, links EntitlementCoachLinks, plans EntitlementPlanSource, audit AdminAuditStore) *entitlementServiceImpl {
	return &entitlementServiceImpl{
		store:   store,
		users:   users,
		links:   links,
		plans:   plans,
		audit:   audit,
		cache:   newReadCache(entitlementCacheTTL),
		defs:    newReadCache(entitlementCacheTTL),
		now:     time.Now,
		planMap: make(map[string]planEntry),
	}
}

func (s *entitlementServiceImpl) Features() []FeatureDef { return Features() }

// ---- errors ----

func entitlementDenied(code, msg, feature string) *models.ApiError {
	return models.NewEntitlementApiError(http.StatusForbidden, &models.EntitlementError{Code: code, Message: msg, Feature: feature})
}

func entitlementUnavailable(err error) *models.ApiError {
	zap.L().Error("entitlement dependency failed", zap.String("component", "entitlement"), zap.Error(err))
	return models.NewEntitlementApiError(http.StatusServiceUnavailable, &models.EntitlementError{
		Code: models.CodeEntitlementsDown, Message: "entitlements unavailable",
	})
}

func quotaExceeded(feature, limit string, used, max int64) *models.ApiError {
	return models.NewEntitlementApiError(http.StatusForbidden, &models.EntitlementError{
		Code: models.CodeQuotaExceeded, Message: "quota exceeded: " + limit, Feature: feature, Limit: limit, Used: &used, Max: &max,
	})
}

func versionConflict() *models.ApiError {
	return models.NewApiError(http.StatusConflict, errors.New("access was modified by someone else, reload and retry"))
}

// ---- resolution ----

type resolveInput struct {
	role     string
	planID   string
	defaults map[string]db.PlanDefault
	grant    *db.EntitlementGrant
	coach    *Effective
}

func newFeatureState(def FeatureDef, enabled bool, source string) FeatureState {
	limits := make(map[string]*int64, len(def.Limits))
	for _, l := range def.Limits {
		limits[l] = nil
	}
	return FeatureState{Enabled: enabled, Source: source, Limits: limits}
}

// computeByRole dispatches on the entitlement role; an unknown role has no applicable feature.
var computeByRole = map[string]func(in resolveInput) map[string]FeatureState{
	"bypass": func(resolveInput) map[string]FeatureState {
		out := make(map[string]FeatureState, len(featureRegistry))
		for k, def := range featureRegistry {
			out[k] = newFeatureState(def, true, SourceDefault)
		}
		return out
	},
	"coach":  func(in resolveInput) map[string]FeatureState { return computeLayered("coach", in) },
	"client": func(in resolveInput) map[string]FeatureState { return computeLayered("client", in) },
}

func computeFeatures(in resolveInput) map[string]FeatureState {
	if fn, ok := computeByRole[in.role]; ok {
		return fn(in)
	}
	out := make(map[string]FeatureState, len(featureRegistry))
	for k, def := range featureRegistry {
		out[k] = newFeatureState(def, false, SourceNotApplic)
	}
	return out
}

// applyLayer merges one layer into st: each field is overridden independently, last layer wins.
func applyLayer(st *FeatureState, def FeatureDef, g db.FeatureGrant, source string) {
	changed := false
	if g.Enabled != nil {
		st.Enabled = *g.Enabled
		changed = true
	}
	for l, v := range g.Limits {
		if def.hasLimit(l) {
			v := v
			st.Limits[l] = &v
			changed = true
		}
	}
	if changed {
		st.Source = source
	}
}

// capByCoach lowers a client's state to the coach's: enabled = client && coach, limit = min.
func capByCoach(st *FeatureState, coach FeatureState) {
	if coach.Source == SourceNotApplic {
		return
	}
	capped := false
	if st.Enabled && !coach.Enabled {
		st.Enabled = false
		capped = true
	}
	for l, cv := range coach.Limits {
		if cv == nil {
			continue
		}
		if cur := st.Limits[l]; cur == nil || *cur > *cv {
			v := *cv
			st.Limits[l] = &v
			capped = true
		}
	}
	if capped {
		st.Source = SourceCoachCap
	}
}

func computeLayered(role string, in resolveInput) map[string]FeatureState {
	out := make(map[string]FeatureState, len(featureRegistry))
	roleDefault := db.PlanDefaultCoach
	if role == "client" {
		roleDefault = db.PlanDefaultClient
	}
	for key, def := range featureRegistry {
		if !def.appliesTo(role) {
			out[key] = newFeatureState(def, false, SourceNotApplic)
			continue
		}
		st := newFeatureState(def, def.DefaultEnabled, SourceDefault)
		if pd, ok := in.defaults[roleDefault]; ok {
			applyLayer(&st, def, pd.Features[key], SourcePlan)
		}
		if pd, ok := in.defaults[in.planID]; ok && in.planID != "" {
			applyLayer(&st, def, pd.Features[key], SourcePlan)
		}
		if in.grant != nil {
			applyLayer(&st, def, in.grant.Features[key], SourceOverride)
		}
		out[key] = st
	}
	// A child is enabled only while its parent is; applied before the coach cap.
	for key, def := range featureRegistry {
		st, ok := out[key]
		if !ok || def.Parent == "" || st.Source == SourceNotApplic {
			continue
		}
		if ps, ok := out[def.Parent]; ok && ps.Source != SourceNotApplic && st.Enabled && !ps.Enabled {
			st.Enabled = false
			st.Source = SourceParent
			out[key] = st
		}
	}
	if role == "client" && in.coach != nil {
		for key, st := range out {
			if st.Source == SourceNotApplic {
				continue
			}
			if cs, ok := in.coach.Features[key]; ok {
				capByCoach(&st, cs)
				out[key] = st
			}
		}
	}
	return out
}

func (s *entitlementServiceImpl) loadDefaults(ctx context.Context) (map[string]db.PlanDefault, *models.ApiError) {
	return cachedRead(s.defs, "defaults", func() (map[string]db.PlanDefault, *models.ApiError) {
		list, err := s.store.ListPlanDefaults(ctx)
		if err != nil {
			return nil, entitlementUnavailable(err)
		}
		m := make(map[string]db.PlanDefault, len(list))
		for _, pd := range list {
			m[pd.PlanID] = pd
		}
		return m, nil
	})
}

// plansFor returns each user's plan (nil when none). Entries are cached 5 minutes; a payments
// failure is never an error here: affected users simply have no plan this time.
func (s *entitlementServiceImpl) plansFor(ctx context.Context, ids []string) map[string]*PlanInfo {
	out := make(map[string]*PlanInfo, len(ids))
	if s.plans == nil || len(ids) == 0 {
		return out
	}
	now := s.now()
	var missing []string
	s.planMu.Lock()
	for _, id := range ids {
		if e, ok := s.planMap[id]; ok && now.Before(e.expires) {
			out[id] = e.plan
		} else {
			missing = append(missing, id)
		}
	}
	s.planMu.Unlock()

	for start := 0; start < len(missing); start += maxResolveIDs {
		end := start + maxResolveIDs
		if end > len(missing) {
			end = len(missing)
		}
		chunk := missing[start:end]
		summaries, err := s.plans.GetSubscriptionSummaries(ctx, chunk)
		if err != nil {
			zap.L().Warn("entitlement plan lookup failed, resolving without plan",
				zap.String("component", "entitlement"), zap.Error(err))
			continue
		}
		found := make(map[string]*PlanInfo, len(summaries))
		for _, sm := range summaries {
			if sm.UserID == "" || sm.PlanID == "" {
				continue
			}
			if _, dup := found[sm.UserID]; dup {
				continue // newest first: keep the first
			}
			found[sm.UserID] = &PlanInfo{PlanID: sm.PlanID, Name: sm.PlanName, Status: sm.Status, CurrentPeriodEnd: sm.CurrentPeriodEnd}
		}
		s.planMu.Lock()
		for _, id := range chunk {
			s.planMap[id] = planEntry{plan: found[id], expires: now.Add(entitlementPlanTTL)}
			out[id] = found[id]
		}
		s.planMu.Unlock()
	}
	return out
}

func planID(p *PlanInfo) string {
	if p == nil {
		return ""
	}
	return p.PlanID
}

// resolve returns the cached resolution (no usage) of a user. The result is shared: never mutate it.
func (s *entitlementServiceImpl) resolve(ctx context.Context, userID string) (*Effective, *models.ApiError) {
	return cachedRead(s.cache, userID, func() (*Effective, *models.ApiError) { return s.loadEffective(ctx, userID) })
}

func (s *entitlementServiceImpl) loadEffective(ctx context.Context, userID string) (*Effective, *models.ApiError) {
	user, apiErr := s.users.GetUserById(ctx, userID)
	if apiErr != nil {
		if apiErr.Code == http.StatusNotFound {
			return nil, apiErr
		}
		return nil, entitlementUnavailable(apiErr.Error)
	}
	if user == nil {
		return nil, models.NewApiError(http.StatusNotFound, models.ErrUserNotFound)
	}
	role := entitlementRole(user.UserRole)
	eff := &Effective{UserID: userID, Role: user.UserRole, TTLSeconds: entitlementTTLSeconds}
	if role != "coach" && role != "client" {
		eff.Features = computeFeatures(resolveInput{role: role})
		return eff, nil
	}

	defaults, apiErr := s.loadDefaults(ctx)
	if apiErr != nil {
		return nil, apiErr
	}
	eff.Plan = s.plansFor(ctx, []string{userID})[userID]
	grant, err := s.store.GetGrant(ctx, userID)
	if err != nil {
		return nil, entitlementUnavailable(err)
	}

	in := resolveInput{role: role, planID: planID(eff.Plan), defaults: defaults, grant: grant}
	if role == "client" {
		coachID, err := s.links.GetCoachIDByClientID(ctx, userID)
		if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
			return nil, entitlementUnavailable(err)
		}
		if err == nil && coachID != "" {
			if coachEff, cerr := s.resolve(ctx, coachID); cerr == nil {
				in.coach = coachEff
			} else if cerr.Code != http.StatusNotFound {
				return nil, cerr
			}
		}
		// A client grant only counts while its granting coach is still the client's coach.
		if grant != nil && (coachID == "" || grant.CoachID != coachID) {
			in.grant = nil
		}
	}
	eff.Features = computeFeatures(in)
	return eff, nil
}

// withUsage returns a copy of eff with usage attached. Usage failures are logged and omitted.
func (s *entitlementServiceImpl) withUsage(ctx context.Context, eff *Effective) *Effective {
	out := *eff
	out.Features = make(map[string]FeatureState, len(eff.Features))
	for k, v := range eff.Features {
		out.Features[k] = v
	}
	usage, err := s.store.GetUsage(ctx, []string{eff.UserID})
	if err != nil {
		zap.L().Warn("entitlement usage lookup failed", zap.String("component", "entitlement"), zap.Error(err))
		return &out
	}
	attachCounterUsage(out.Features, usage[eff.UserID], s.now())
	if entitlementRole(eff.Role) == "coach" {
		if n, cerr := s.store.CountClients(ctx, eff.UserID); cerr == nil {
			attachClientUsage(out.Features, int(n))
		}
	}
	return &out
}

// attachCounterUsage adds the usage counters of every feature that has some (zero when none recorded).
// Monthly counters are read at the key of the current UTC month and reported as "<counter>_month".
func attachCounterUsage(features map[string]FeatureState, u *db.EntitlementUsage, now time.Time) {
	for key, def := range featureRegistry {
		st, ok := features[key]
		if !ok || st.Source == SourceNotApplic {
			continue
		}
		counters := counterNames(def)
		if len(counters) == 0 {
			continue
		}
		usage := make(map[string]int64, len(counters))
		for _, c := range counters {
			name := c
			if monthlyCounters[c] {
				name = c + "_month"
				st.Month = now.UTC().Format("2006-01")
			}
			if u != nil {
				usage[name] = u.Counters[key][CounterKey(c, now)]
			} else {
				usage[name] = 0
			}
		}
		st.Usage = usage
		features[key] = st
	}
}

func attachClientUsage(features map[string]FeatureState, clients int) {
	if st, ok := features[FeatureInvitations]; ok && st.Source != SourceNotApplic {
		st.Usage = map[string]int64{"clients": int64(clients)}
		features[FeatureInvitations] = st
	}
}

// counterNames lists the usage counters a feature tracks, sorted.
func counterNames(def FeatureDef) []string {
	var out []string
	for _, l := range def.Limits {
		if c, ok := limitCounters[l]; ok {
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}

func (s *entitlementServiceImpl) Effective(ctx context.Context, userID string) (*Effective, *models.ApiError) {
	eff, apiErr := s.resolve(ctx, userID)
	if apiErr != nil {
		return nil, apiErr
	}
	return s.withUsage(ctx, eff), nil
}

func (s *entitlementServiceImpl) Resolve(ctx context.Context, userIDs []string) (map[string]*Effective, *models.ApiError) {
	if len(userIDs) == 0 || len(userIDs) > maxResolveIDs {
		return nil, badRequest("user_ids must contain between 1 and 100 ids")
	}
	s.plansFor(ctx, userIDs) // one batch call warms the plan cache for the loop below
	out := make(map[string]*Effective, len(userIDs))
	for _, id := range userIDs {
		eff, apiErr := s.resolve(ctx, id)
		if apiErr != nil {
			if apiErr.Code == http.StatusNotFound {
				continue
			}
			return nil, apiErr
		}
		out[id] = eff
	}
	return out, nil
}

// ---- in-process guard ----

func (s *entitlementServiceImpl) CheckInvitations(ctx context.Context, coachID string) *models.ApiError {
	eff, apiErr := s.resolve(ctx, coachID)
	if apiErr != nil {
		// Never block registration because entitlements cannot be read.
		zap.L().Warn("entitlement_lookup_failed", zap.String("component", "entitlement"), zap.String("user_id", coachID), zap.Int("status", apiErr.Code))
		return nil
	}
	st := eff.Features[FeatureInvitations]
	if !st.Enabled {
		return entitlementDenied(models.CodeFeatureDisabled, "feature disabled: "+FeatureInvitations, FeatureInvitations)
	}
	limit := st.Limits[LimitMaxClients]
	if limit == nil {
		return nil
	}
	used, err := s.store.CountClients(ctx, coachID)
	if err != nil {
		zap.L().Warn("entitlement_lookup_failed", zap.String("component", "entitlement"), zap.String("user_id", coachID), zap.Error(err))
		return nil
	}
	if used >= *limit {
		return quotaExceeded(FeatureInvitations, LimitMaxClients, used, *limit)
	}
	return nil
}

// ---- internal consume / release / check-quota ----

// parseDeltas validates a usage request and returns the deltas with every counter of the feature present.
func parseDeltas(req api.UsageDeltaRequest) (FeatureDef, map[string]int64, *models.ApiError) {
	def, ok := featureRegistry[req.Feature]
	if !ok {
		return def, nil, badRequest("unknown feature: " + req.Feature)
	}
	counters := counterNames(def)
	if len(counters) == 0 {
		return def, nil, badRequest("feature has no usage counters: " + req.Feature)
	}
	if len(req.Deltas) == 0 {
		return def, nil, badRequest("deltas are required")
	}
	deltas := make(map[string]int64, len(counters))
	for _, c := range counters {
		deltas[c] = 0
	}
	for c, d := range req.Deltas {
		if _, ok := deltas[c]; !ok {
			return def, nil, badRequest("unknown counter: " + c)
		}
		if d < 0 {
			return def, nil, badRequest("deltas must not be negative")
		}
		deltas[c] = d
	}
	return def, deltas, nil
}

func (s *entitlementServiceImpl) Consume(ctx context.Context, userID string, req api.UsageDeltaRequest) (map[string]int64, *models.ApiError) {
	_, deltas, apiErr := parseDeltas(req)
	if apiErr != nil {
		return nil, apiErr
	}
	now := s.now()
	eff, apiErr := s.resolve(ctx, userID)
	if apiErr != nil {
		return nil, apiErr
	}
	st := eff.Features[req.Feature]
	if !st.Enabled {
		return nil, entitlementDenied(models.CodeFeatureDisabled, "feature disabled: "+req.Feature, req.Feature)
	}
	maxes := make(map[string]int64)
	for counter := range deltas {
		if lv := st.Limits[counterLimits[counter]]; lv != nil {
			maxes[counter] = *lv
		}
	}
	usage, ok, err := s.store.ConsumeUsage(ctx, userID, req.Feature, storageKeys(deltas, now), storageKeys(maxes, now))
	if err != nil {
		return nil, entitlementUnavailable(err)
	}
	if !ok {
		return nil, s.quotaDenial(ctx, userID, req.Feature, deltas, maxes, now)
	}
	out := make(map[string]int64, len(deltas))
	for counter := range deltas {
		out[counter] = usage.Counters[req.Feature][CounterKey(counter, now)]
	}
	return out, nil
}

// storageKeys renames counter-keyed values to their storage keys (monthly counters carry the month).
func storageKeys(in map[string]int64, now time.Time) map[string]int64 {
	out := make(map[string]int64, len(in))
	for counter, v := range in {
		out[CounterKey(counter, now)] = v
	}
	return out
}

// quotaDenial names the first counter that the denied consume would have pushed over its maximum.
func (s *entitlementServiceImpl) quotaDenial(ctx context.Context, userID, feature string, deltas, maxes map[string]int64, now time.Time) *models.ApiError {
	usage, err := s.store.GetUsage(ctx, []string{userID})
	if err != nil {
		return entitlementUnavailable(err)
	}
	counters := make([]string, 0, len(maxes))
	for c := range maxes {
		counters = append(counters, c)
	}
	sort.Strings(counters)
	for _, c := range counters {
		var used int64
		if u := usage[userID]; u != nil {
			used = u.Counters[feature][CounterKey(c, now)]
		}
		if used+deltas[c] > maxes[c] {
			return quotaExceeded(feature, counterLimits[c], used, maxes[c])
		}
	}
	// A concurrent release made room between the failed update and this read.
	return quotaExceeded(feature, counterLimits[counters[0]], 0, maxes[counters[0]])
}

func (s *entitlementServiceImpl) Release(ctx context.Context, userID string, req api.UsageDeltaRequest) *models.ApiError {
	_, deltas, apiErr := parseDeltas(req)
	if apiErr != nil {
		return apiErr
	}
	if err := s.store.ReleaseUsage(ctx, userID, req.Feature, storageKeys(deltas, s.now())); err != nil {
		return entitlementUnavailable(err)
	}
	return nil
}

func (s *entitlementServiceImpl) CheckQuota(ctx context.Context, userID string, req api.CheckQuotaRequest) (*QuotaResult, *models.ApiError) {
	def, ok := featureRegistry[req.Feature]
	if !ok {
		return nil, badRequest("unknown feature: " + req.Feature)
	}
	if !def.hasLimit(req.Limit) || req.Limit != LimitMaxClients {
		return nil, badRequest("unsupported limit: " + req.Limit)
	}
	if req.Pending < 0 {
		return nil, badRequest("pending must not be negative")
	}
	eff, apiErr := s.resolve(ctx, userID)
	if apiErr != nil {
		return nil, apiErr
	}
	st := eff.Features[req.Feature]
	if !st.Enabled {
		return nil, entitlementDenied(models.CodeFeatureDisabled, "feature disabled: "+req.Feature, req.Feature)
	}
	linked, err := s.store.CountClients(ctx, userID)
	if err != nil {
		return nil, entitlementUnavailable(err)
	}
	used := linked + req.Pending
	limit := st.Limits[req.Limit]
	if limit != nil && used >= *limit {
		return nil, quotaExceeded(req.Feature, req.Limit, used, *limit)
	}
	return &QuotaResult{Allowed: true, Used: used, Limit: limit}, nil
}

// ---- writes ----

func cloneFeatures(in map[string]db.FeatureGrant) map[string]db.FeatureGrant {
	out := make(map[string]db.FeatureGrant, len(in))
	for k, v := range in {
		c := db.FeatureGrant{}
		if v.Enabled != nil {
			b := *v.Enabled
			c.Enabled = &b
		}
		if len(v.Limits) > 0 {
			c.Limits = make(map[string]int64, len(v.Limits))
			for l, n := range v.Limits {
				c.Limits[l] = n
			}
		}
		out[k] = c
	}
	return out
}

func grantEntryMap(g db.FeatureGrant, present bool) interface{} {
	if !present {
		return nil
	}
	m := map[string]interface{}{}
	if g.Enabled != nil {
		m["enabled"] = *g.Enabled
	}
	if len(g.Limits) > 0 {
		m["limits"] = g.Limits
	}
	return m
}

// applyPatch validates patch against the registry (and role, when not empty) and returns the new
// features plus the before/after snapshots of the entries that changed. changed is empty for a no-op.
func applyPatch(current map[string]db.FeatureGrant, patch map[string]api.FeaturePatch, role string) (next map[string]db.FeatureGrant, before, after map[string]interface{}, apiErr *models.ApiError) {
	if len(patch) == 0 {
		return nil, nil, nil, badRequest("features are required")
	}
	next = cloneFeatures(current)
	before, after = map[string]interface{}{}, map[string]interface{}{}
	keys := make([]string, 0, len(patch))
	for k := range patch {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		p := patch[key]
		def, ok := featureRegistry[key]
		if !ok {
			return nil, nil, nil, badRequest("unknown feature: " + key)
		}
		if role != "" && !def.appliesTo(role) {
			return nil, nil, nil, badRequest(fmt.Sprintf("feature %s does not apply to %s", key, role))
		}
		old, hadOld := current[key]
		cur := cloneFeatures(map[string]db.FeatureGrant{key: old})[key]
		if p.Enabled.Set {
			cur.Enabled = p.Enabled.Value
		}
		for l, v := range p.Limits {
			if !def.hasLimit(l) {
				return nil, nil, nil, badRequest(fmt.Sprintf("unknown limit %s for feature %s", l, key))
			}
			if v == nil {
				delete(cur.Limits, l)
				continue
			}
			if *v < 0 {
				return nil, nil, nil, badRequest("limits must not be negative")
			}
			if cur.Limits == nil {
				cur.Limits = map[string]int64{}
			}
			cur.Limits[l] = *v
		}
		if len(cur.Limits) == 0 {
			cur.Limits = nil
		}
		empty := cur.Enabled == nil && cur.Limits == nil
		if empty {
			delete(next, key)
		} else {
			next[key] = cur
		}
		if !reflect.DeepEqual(old, cur) && (hadOld || !empty) {
			before[key] = grantEntryMap(old, hadOld)
			after[key] = grantEntryMap(cur, !empty)
		}
	}
	return next, before, after, nil
}

func (s *entitlementServiceImpl) purge() {
	s.cache.purge()
	s.defs.purge()
}

func (s *entitlementServiceImpl) recordAudit(ctx context.Context, e *db.AdminAuditEntry) {
	if s.audit == nil {
		return
	}
	if err := s.audit.Insert(ctx, e); err != nil {
		zap.L().Error("entitlement audit insert failed", zap.String("component", "entitlement"),
			zap.String("action", e.Action), zap.String("target_id", e.TargetID), zap.Error(err))
	}
}

// writeGrant stores the new features for a subject with optimistic locking and records the audit entry.
func (s *entitlementServiceImpl) writeGrant(ctx context.Context, existing *db.EntitlementGrant, g *db.EntitlementGrant, version *int64, before, after map[string]interface{}, targetType, actorID, actorRole, reason string) (*db.EntitlementGrant, *models.ApiError) {
	var prev int64
	if existing != nil {
		prev = existing.Version
	}
	if version != nil && *version != prev {
		return nil, versionConflict()
	}
	if len(after) == 0 && len(before) == 0 {
		if existing != nil {
			return existing, nil
		}
		return &db.EntitlementGrant{SubjectID: g.SubjectID, SubjectRole: g.SubjectRole, CoachID: g.CoachID, Features: map[string]db.FeatureGrant{}}, nil
	}
	g.UpdatedBy, g.UpdatedByRole, g.UpdatedAt = actorID, actorRole, s.now()
	if err := s.store.ReplaceGrant(ctx, g, prev); err != nil {
		if errors.Is(err, repositories.ErrEntitlementVersionConflict) {
			return nil, versionConflict()
		}
		return nil, entitlementUnavailable(err)
	}
	s.purge()
	s.recordAudit(ctx, &db.AdminAuditEntry{
		ActorID: actorID, ActorRole: actorRole, Action: auditActionEntitlementSet,
		TargetType: targetType, TargetID: g.SubjectID, Before: before, After: after, Reason: reason,
	})
	return g, nil
}

func (s *entitlementServiceImpl) SetCoachAccess(ctx context.Context, actorID, coachID string, req api.UpdateAccessRequest) (*CoachAccessDetail, *models.ApiError) {
	user, apiErr := s.users.GetUserById(ctx, coachID)
	if apiErr != nil {
		return nil, apiErr
	}
	if user == nil || entitlementRole(user.UserRole) != "coach" {
		return nil, badRequest("user is not a coach")
	}
	existing, err := s.store.GetGrant(ctx, coachID)
	if err != nil {
		return nil, entitlementUnavailable(err)
	}
	var current map[string]db.FeatureGrant
	if existing != nil {
		current = existing.Features
	}
	next, before, after, apiErr := applyPatch(current, req.Features, "coach")
	if apiErr != nil {
		return nil, apiErr
	}
	g := &db.EntitlementGrant{SubjectID: coachID, SubjectRole: db.EntitlementRoleCoach, Features: next}
	if _, apiErr := s.writeGrant(ctx, existing, g, req.Version, before, after, db.EntitlementRoleCoach, actorID, "admin", req.Reason); apiErr != nil {
		return nil, apiErr
	}
	return s.GetCoachAccess(ctx, coachID)
}

// checkGrantable rejects patches that give a client more than the coach has.
func checkGrantable(patch map[string]api.FeaturePatch, coach *Effective) *models.ApiError {
	if coach == nil {
		return nil
	}
	keys := make([]string, 0, len(patch))
	for k := range patch {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		p := patch[key]
		cs, ok := coach.Features[key]
		if !ok || cs.Source == SourceNotApplic {
			continue
		}
		if p.Enabled.Set && p.Enabled.Value != nil && *p.Enabled.Value && !cs.Enabled {
			return entitlementDenied(models.CodeGrantExceedsCoach, "cannot enable a feature you do not have: "+key, key)
		}
		for l, v := range p.Limits {
			if cv := cs.Limits[l]; v != nil && cv != nil && *v > *cv {
				return models.NewEntitlementApiError(http.StatusForbidden, &models.EntitlementError{
					Code: models.CodeGrantExceedsCoach, Message: "limit exceeds your own limit: " + l, Feature: key, Limit: l, Max: cv,
				})
			}
		}
	}
	return nil
}

func (s *entitlementServiceImpl) SetClientAccess(ctx context.Context, actorID, actorRole, coachID, clientID string, req api.UpdateAccessRequest) (*ClientAccessDetail, *models.ApiError) {
	if apiErr := s.requireLink(ctx, coachID, clientID); apiErr != nil {
		return nil, apiErr
	}
	existing, err := s.store.GetGrant(ctx, clientID)
	if err != nil {
		return nil, entitlementUnavailable(err)
	}
	// A grant left by a previous coach is replaced, not merged.
	var current map[string]db.FeatureGrant
	if existing != nil && existing.CoachID == coachID {
		current = existing.Features
	}
	next, before, after, apiErr := applyPatch(current, req.Features, "client")
	if apiErr != nil {
		return nil, apiErr
	}
	coachEff, apiErr := s.resolve(ctx, coachID)
	if apiErr != nil {
		return nil, apiErr
	}
	if apiErr := checkGrantable(req.Features, coachEff); apiErr != nil {
		return nil, apiErr
	}
	g := &db.EntitlementGrant{SubjectID: clientID, SubjectRole: db.EntitlementRoleClient, CoachID: coachID, Features: next}
	if _, apiErr := s.writeGrant(ctx, existing, g, req.Version, before, after, db.EntitlementRoleClient, actorID, actorRole, req.Reason); apiErr != nil {
		return nil, apiErr
	}
	return s.GetClientAccess(ctx, coachID, clientID)
}

func (s *entitlementServiceImpl) requireLink(ctx context.Context, coachID, clientID string) *models.ApiError {
	linked, err := s.links.IsClientOfCoach(ctx, coachID, clientID)
	if err != nil {
		return entitlementUnavailable(err)
	}
	if !linked {
		return models.NewApiError(http.StatusForbidden, errors.New("client is not linked to this coach"))
	}
	return nil
}

func validPlanID(id string) (role string, ok bool) {
	switch id {
	case db.PlanDefaultCoach:
		return "coach", true
	case db.PlanDefaultClient:
		return "client", true
	}
	return "", primitive.IsValidObjectID(id)
}

func (s *entitlementServiceImpl) ListPlanDefaults(ctx context.Context) ([]db.PlanDefault, *models.ApiError) {
	list, err := s.store.ListPlanDefaults(ctx)
	if err != nil {
		return nil, entitlementUnavailable(err)
	}
	return list, nil
}

func (s *entitlementServiceImpl) SetPlanDefault(ctx context.Context, actorID, planID string, req api.PlanDefaultRequest) (*db.PlanDefault, *models.ApiError) {
	role, ok := validPlanID(planID)
	if !ok {
		return nil, badRequest("invalid plan id")
	}
	list, err := s.store.ListPlanDefaults(ctx)
	if err != nil {
		return nil, entitlementUnavailable(err)
	}
	pd := db.PlanDefault{PlanID: planID}
	for _, existing := range list {
		if existing.PlanID == planID {
			pd = existing
		}
	}
	next, before, after, apiErr := applyPatch(pd.Features, req.Features, role)
	if apiErr != nil {
		return nil, apiErr
	}
	pd.Features, pd.UpdatedBy, pd.UpdatedAt = next, actorID, s.now()
	if len(before) == 0 && len(after) == 0 {
		return &pd, nil
	}
	if err := s.store.UpsertPlanDefault(ctx, &pd); err != nil {
		return nil, entitlementUnavailable(err)
	}
	s.purge()
	s.recordAudit(ctx, &db.AdminAuditEntry{
		ActorID: actorID, ActorRole: "admin", Action: auditActionPlanDefaultSet,
		TargetType: "plan", TargetID: planID, Before: before, After: after, Reason: req.Reason,
	})
	return &pd, nil
}

func (s *entitlementServiceImpl) ListAudit(ctx context.Context, f db.AdminAuditFilter, page, size int) (*AdminAuditList, *models.ApiError) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	entries, total, err := s.audit.List(ctx, f, page, size)
	if err != nil {
		return nil, entitlementUnavailable(err)
	}
	return &AdminAuditList{Entries: entries, Total: total, Page: page, Size: size}, nil
}

// ---- admin / coach views ----

func lastLogin(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func (s *entitlementServiceImpl) coachRow(r db.CoachAccessRow, plan *PlanInfo, defaults map[string]db.PlanDefault) CoachAccessRow {
	features := computeFeatures(resolveInput{role: entitlementRole(r.Role), planID: planID(plan), defaults: defaults, grant: r.Grant})
	attachCounterUsage(features, r.Usage, s.now())
	attachClientUsage(features, r.ClientCount)
	return CoachAccessRow{
		ExternalID: r.ExternalID, Firstname: r.Firstname, Lastname: r.Lastname, Email: r.Email, Role: r.Role,
		Status: r.Status, IsHeadCoach: r.IsHeadCoach, LastLogin: lastLogin(r.LastLogin), Plan: plan,
		ClientCount: r.ClientCount, Features: features,
	}
}

func (s *entitlementServiceImpl) ListCoachAccess(ctx context.Context, q db.CoachAccessQuery) (*CoachAccessList, *models.ApiError) {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Size < 1 || q.Size > 100 {
		q.Size = 20
	}
	if q.Feature != "" {
		if _, ok := featureRegistry[q.Feature]; !ok {
			return nil, badRequest("unknown feature: " + q.Feature)
		}
	}
	rows, total, err := s.store.ListCoachAccess(ctx, q)
	if err != nil {
		return nil, entitlementUnavailable(err)
	}
	defaults, apiErr := s.loadDefaults(ctx)
	if apiErr != nil {
		return nil, apiErr
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ExternalID)
	}
	plans := s.plansFor(ctx, ids)
	out := make([]CoachAccessRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, s.coachRow(r, plans[r.ExternalID], defaults))
	}
	return &CoachAccessList{Rows: out, Total: total, Page: q.Page, Size: q.Size}, nil
}

func (s *entitlementServiceImpl) GetCoachAccess(ctx context.Context, coachID string) (*CoachAccessDetail, *models.ApiError) {
	user, apiErr := s.users.GetUserById(ctx, coachID)
	if apiErr != nil {
		return nil, apiErr
	}
	if user == nil || entitlementRole(user.UserRole) != "coach" {
		return nil, models.NewApiError(http.StatusNotFound, errors.New("coach not found"))
	}
	grant, err := s.store.GetGrant(ctx, coachID)
	if err != nil {
		return nil, entitlementUnavailable(err)
	}
	usage, err := s.store.GetUsage(ctx, []string{coachID})
	if err != nil {
		return nil, entitlementUnavailable(err)
	}
	clients, err := s.store.CountClients(ctx, coachID)
	if err != nil {
		return nil, entitlementUnavailable(err)
	}
	defaults, apiErr := s.loadDefaults(ctx)
	if apiErr != nil {
		return nil, apiErr
	}
	plan := s.plansFor(ctx, []string{coachID})[coachID]
	row := s.coachRow(db.CoachAccessRow{
		ExternalID: user.ExternalID, Firstname: user.UserFirstname, Lastname: user.UserLastname, Email: user.UserEmail,
		Role: user.UserRole, Status: string(user.UserStatus), IsHeadCoach: user.IsHeadCoach, LastLogin: user.UserLastLogin,
		ClientCount: int(clients), Grant: grant, Usage: usage[coachID],
	}, plan, defaults)

	detail := &CoachAccessDetail{CoachAccessRow: row, Overrides: map[string]db.FeatureGrant{}, Audit: []db.AdminAuditEntry{}}
	if grant != nil {
		detail.Overrides, detail.Version = grant.Features, grant.Version
	}
	if s.audit != nil {
		entries, _, aerr := s.audit.List(ctx, db.AdminAuditFilter{TargetID: coachID}, 1, entitlementAuditLimit)
		if aerr != nil {
			zap.L().Warn("entitlement audit read failed", zap.String("component", "entitlement"), zap.Error(aerr))
		} else if entries != nil {
			detail.Audit = entries
		}
	}
	return detail, nil
}

// grantable is the coach's effective value of each feature that applies to clients.
func grantable(coach *Effective) map[string]FeatureState {
	out := make(map[string]FeatureState)
	for key, def := range featureRegistry {
		if !def.appliesTo("client") {
			continue
		}
		if st, ok := coach.Features[key]; ok {
			st.Usage = nil
			out[key] = st
		}
	}
	return out
}

func (s *entitlementServiceImpl) clientRow(r db.ClientAccessRow, coachID string, plan *PlanInfo, defaults map[string]db.PlanDefault, coach *Effective, grantableMap map[string]FeatureState) ClientAccessRow {
	grant := r.Grant
	if grant != nil && grant.CoachID != coachID {
		grant = nil
	}
	return ClientAccessRow{
		ExternalID: r.ExternalID, Firstname: r.Firstname, Lastname: r.Lastname, Email: r.Email, Status: r.Status,
		LastLogin: lastLogin(r.LastLogin), Plan: plan,
		Features:  computeFeatures(resolveInput{role: "client", planID: planID(plan), defaults: defaults, grant: grant, coach: coach}),
		Grantable: grantableMap,
	}
}

func (s *entitlementServiceImpl) ListClientAccess(ctx context.Context, coachID string, page, size int, q string) (*ClientAccessList, *models.ApiError) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	coach, apiErr := s.resolve(ctx, coachID)
	if apiErr != nil {
		return nil, apiErr
	}
	rows, total, err := s.store.ListClientAccess(ctx, coachID, page, size, q)
	if err != nil {
		return nil, entitlementUnavailable(err)
	}
	defaults, apiErr := s.loadDefaults(ctx)
	if apiErr != nil {
		return nil, apiErr
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ExternalID)
	}
	plans := s.plansFor(ctx, ids)
	g := grantable(coach)
	out := make([]ClientAccessRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, s.clientRow(r, coachID, plans[r.ExternalID], defaults, coach, g))
	}
	return &ClientAccessList{Rows: out, Total: total, Page: page, Size: size, Grantable: g}, nil
}

func (s *entitlementServiceImpl) GetClientAccess(ctx context.Context, coachID, clientID string) (*ClientAccessDetail, *models.ApiError) {
	if apiErr := s.requireLink(ctx, coachID, clientID); apiErr != nil {
		return nil, apiErr
	}
	user, apiErr := s.users.GetUserById(ctx, clientID)
	if apiErr != nil {
		return nil, apiErr
	}
	if user == nil {
		return nil, models.NewApiError(http.StatusNotFound, models.ErrUserNotFound)
	}
	coach, apiErr := s.resolve(ctx, coachID)
	if apiErr != nil {
		return nil, apiErr
	}
	grant, err := s.store.GetGrant(ctx, clientID)
	if err != nil {
		return nil, entitlementUnavailable(err)
	}
	defaults, apiErr := s.loadDefaults(ctx)
	if apiErr != nil {
		return nil, apiErr
	}
	plan := s.plansFor(ctx, []string{clientID})[clientID]
	row := s.clientRow(db.ClientAccessRow{
		ExternalID: user.ExternalID, Firstname: user.UserFirstname, Lastname: user.UserLastname, Email: user.UserEmail,
		Status: string(user.UserStatus), LastLogin: user.UserLastLogin, Grant: grant,
	}, coachID, plan, defaults, coach, grantable(coach))

	detail := &ClientAccessDetail{ClientAccessRow: row, Overrides: map[string]db.FeatureGrant{}}
	if grant != nil {
		// A grant left by a previous coach is hidden but still carries the version a write must quote.
		detail.Version = grant.Version
		if grant.CoachID == coachID {
			detail.Overrides = grant.Features
		}
	}
	return detail, nil
}
