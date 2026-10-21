package services

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
)

const (
	// DefaultAnalyticsMaxCoaches caps the platform scope (ANALYTICS_SCOPE_MAX_COACHES).
	DefaultAnalyticsMaxCoaches = 5000
	// MaxLookupIDs is the largest id list accepted by the client lookup.
	MaxLookupIDs = 100

	maxClientSearchLen = 64
	maxClientPageSize  = 100
	lookupRowLimit     = 500
)

var (
	errAccessDenied  = errors.New("Access denied")
	errInvalidScope  = errors.New("invalid scope")
	errScopeTooLarge = errors.New("scope too large")
	errLookupIDs     = errors.New("ids are required (max 100)")
)

// AnalyticsUserStore is the part of the user repository the analytics service needs.
type AnalyticsUserStore interface {
	GetUserByExternalIdUpdate(ctx context.Context, userID string) (*db.User, *models.ApiError)
}

// AnalyticsStore is the part of the analytics repository the analytics service needs.
type AnalyticsStore interface {
	GetOwnedGym(ctx context.Context, gymID, ownerID string) (*db.Gym, error)
	GymHeadPeriods(ctx context.Context, gymID string) ([]db.MembershipPeriod, error)
	TeamPeriodsByHeads(ctx context.Context, headIDs []string) ([]db.MembershipPeriod, error)
	UserIdentities(ctx context.Context, ids []string) (map[string]db.UserIdentity, error)
	CoachIdentities(ctx context.Context, limit int) ([]db.UserIdentity, error)
	ClientsOfCoaches(ctx context.Context, coachIDs []string, f db.ScopeClientFilter) ([]db.ClientRowLinked, int, error)
}

var (
	_ AnalyticsUserStore = (*repositories.UserRepository)(nil)
	_ AnalyticsStore     = (*repositories.AnalyticsRepository)(nil)
)

// ScopeRequest selects an analytics scope and optional narrowing filters.
type ScopeRequest struct {
	Scope       string `json:"scope"`
	GymID       string `json:"gym_id"`
	HeadCoachID string `json:"head_coach_id"`
	CoachID     string `json:"coach_id"`
}

// HeadClientsQuery filters the clients of a head coach's team.
type HeadClientsQuery struct {
	CoachID string
	Q       string
	Sort    string // name | linked_at
	Page    int
	Size    int
}

// AnalyticsScopeService resolves who a caller may see. Every call re-authorizes from the database.
type AnalyticsScopeService interface {
	Scope(ctx context.Context, callerID string, req ScopeRequest) (*db.AnalyticsScope, *models.ApiError)
	LookupClients(ctx context.Context, callerID string, req ScopeRequest, ids []string) ([]db.ClientRow, *models.ApiError)
	ListHeadClients(ctx context.Context, callerID string, q HeadClientsQuery) ([]db.ClientRowLinked, int, *models.ApiError)
}

type analyticsServiceImpl struct {
	users      AnalyticsUserStore
	store      AnalyticsStore
	maxCoaches int
	now        func() time.Time
	builders   map[string]func(*analyticsServiceImpl, context.Context, string, ScopeRequest) (*db.AnalyticsScope, *models.ApiError)
}

func NewAnalyticsScopeService(users AnalyticsUserStore, store AnalyticsStore, maxCoaches int) AnalyticsScopeService {
	if maxCoaches <= 0 {
		maxCoaches = DefaultAnalyticsMaxCoaches
	}
	return &analyticsServiceImpl{
		users: users, store: store, maxCoaches: maxCoaches, now: time.Now,
		builders: map[string]func(*analyticsServiceImpl, context.Context, string, ScopeRequest) (*db.AnalyticsScope, *models.ApiError){
			db.AnalyticsScopeTeam:     (*analyticsServiceImpl).teamScope,
			db.AnalyticsScopeGym:      (*analyticsServiceImpl).gymScope,
			db.AnalyticsScopePlatform: (*analyticsServiceImpl).platformScope,
		},
	}
}

func (s *analyticsServiceImpl) internal(op string, err error) *models.ApiError {
	zap.L().Error("analytics service failure",
		zap.String("component", "analytics"),
		zap.String("op", op),
		zap.Error(err),
	)
	return teamErr(http.StatusInternalServerError, errInternal)
}

// requireActive re-checks in the database that the caller has the role, an Active account and (optionally) the head coach flag.
func (s *analyticsServiceImpl) requireActive(ctx context.Context, op, callerID, role string, head bool) (*db.User, *models.ApiError) {
	u, apiErr := s.users.GetUserByExternalIdUpdate(ctx, callerID)
	if apiErr != nil {
		if apiErr.Code == http.StatusNotFound {
			return nil, teamErr(http.StatusForbidden, errAccessDenied)
		}
		return nil, s.internal(op, apiErr.Error)
	}
	if u == nil || u.UserRole != role || u.UserStatus != db.Active || (head && !u.IsHeadCoach) {
		return nil, teamErr(http.StatusForbidden, errAccessDenied)
	}
	return u, nil
}

// ---------- Scope ----------

func (s *analyticsServiceImpl) Scope(ctx context.Context, callerID string, req ScopeRequest) (*db.AnalyticsScope, *models.ApiError) {
	build, ok := s.builders[req.Scope]
	if !ok {
		return nil, teamErr(http.StatusBadRequest, errInvalidScope)
	}
	return build(s, ctx, callerID, req)
}

// scopeEntry is one (coach, head coach) pair before names are attached.
type scopeEntry struct {
	coachID string
	headID  string
	periods []db.PeriodRange
}

func (s *analyticsServiceImpl) teamScope(ctx context.Context, callerID string, req ScopeRequest) (*db.AnalyticsScope, *models.ApiError) {
	head, apiErr := s.requireActive(ctx, "teamScope.auth", callerID, "coach", true)
	if apiErr != nil {
		return nil, apiErr
	}
	if req.HeadCoachID != "" && req.HeadCoachID != head.ExternalID {
		return nil, teamErr(http.StatusNotFound, errHeadNotFound)
	}
	ps, err := s.store.TeamPeriodsByHeads(ctx, []string{head.ExternalID})
	if err != nil {
		return nil, s.internal("teamScope.periods", err)
	}

	since := head.UserCreatedAt
	if head.HeadCoachSince != nil {
		since = *head.HeadCoachSince
	}
	entries := []scopeEntry{{coachID: head.ExternalID, headID: head.ExternalID, periods: []db.PeriodRange{{From: since}}}}
	for _, m := range repositories.GroupPeriodsByMember(ps) {
		entries = append(entries, scopeEntry{coachID: m.CoachID, headID: head.ExternalID, periods: m.Periods})
	}

	key := "team:" + head.ExternalID
	own := db.UserIdentity{ExternalID: head.ExternalID, Firstname: head.UserFirstname, Lastname: head.UserLastname}
	return s.finish(ctx, "teamScope", key, req.CoachID, entries, map[string]db.UserIdentity{head.ExternalID: own})
}

func (s *analyticsServiceImpl) gymScope(ctx context.Context, callerID string, req ScopeRequest) (*db.AnalyticsScope, *models.ApiError) {
	if _, apiErr := s.requireActive(ctx, "gymScope.auth", callerID, db.RoleGymOwner, false); apiErr != nil {
		return nil, apiErr
	}
	if req.GymID == "" {
		return nil, teamErr(http.StatusNotFound, errGymNotFound)
	}
	g, err := s.store.GetOwnedGym(ctx, req.GymID, callerID)
	if err != nil {
		return nil, s.internal("gymScope.gym", err)
	}
	if g == nil {
		return nil, teamErr(http.StatusNotFound, errGymNotFound)
	}
	gymID := g.ID.Hex()

	gymPeriods, err := s.store.GymHeadPeriods(ctx, gymID)
	if err != nil {
		return nil, s.internal("gymScope.gymPeriods", err)
	}
	heads := repositories.GroupPeriodsByMember(gymPeriods) // CoachID is the head coach here
	if req.HeadCoachID != "" {
		kept := heads[:0:0]
		for _, h := range heads {
			if h.CoachID == req.HeadCoachID {
				kept = append(kept, h)
			}
		}
		if len(kept) == 0 {
			return nil, teamErr(http.StatusNotFound, errHeadNotFound)
		}
		heads = kept
	}

	headIDs := make([]string, 0, len(heads))
	for _, h := range heads {
		headIDs = append(headIDs, h.CoachID)
	}
	teamPeriods, err := s.store.TeamPeriodsByHeads(ctx, headIDs)
	if err != nil {
		return nil, s.internal("gymScope.teamPeriods", err)
	}
	byHead := map[string][]db.MembershipPeriod{}
	for _, p := range teamPeriods {
		byHead[p.ParentID] = append(byHead[p.ParentID], p)
	}

	entries := []scopeEntry{}
	for _, h := range heads {
		entries = append(entries, scopeEntry{coachID: h.CoachID, headID: h.CoachID, periods: h.Periods})
		for _, m := range repositories.GroupPeriodsByMember(byHead[h.CoachID]) {
			if in := intersectPeriods(m.Periods, h.Periods); len(in) > 0 {
				entries = append(entries, scopeEntry{coachID: m.CoachID, headID: h.CoachID, periods: in})
			}
		}
	}

	key := "gym:" + gymID
	if req.HeadCoachID != "" {
		key += ":hc=" + req.HeadCoachID
	}
	return s.finish(ctx, "gymScope", key, req.CoachID, entries, nil)
}

func (s *analyticsServiceImpl) platformScope(ctx context.Context, callerID string, req ScopeRequest) (*db.AnalyticsScope, *models.ApiError) {
	if _, apiErr := s.requireActive(ctx, "platformScope.auth", callerID, "admin", false); apiErr != nil {
		return nil, apiErr
	}
	coaches, err := s.store.CoachIdentities(ctx, s.maxCoaches+1)
	if err != nil {
		return nil, s.internal("platformScope.coaches", err)
	}
	if len(coaches) > s.maxCoaches {
		return nil, teamErr(http.StatusRequestEntityTooLarge, errScopeTooLarge)
	}
	entries := make([]scopeEntry, 0, len(coaches))
	ident := make(map[string]db.UserIdentity, len(coaches))
	for _, c := range coaches {
		ident[c.ExternalID] = c
		entries = append(entries, scopeEntry{coachID: c.ExternalID, periods: []db.PeriodRange{{From: c.CreatedAt}}})
	}
	return s.finish(ctx, "platformScope", "platform", req.CoachID, entries, ident)
}

// finish narrows by coach_id, attaches names and builds the response. Identities already known are passed in;
// the rest come from one query. Unknown (deleted) users keep their id with empty names.
func (s *analyticsServiceImpl) finish(ctx context.Context, op, key, coachID string, entries []scopeEntry, known map[string]db.UserIdentity) (*db.AnalyticsScope, *models.ApiError) {
	if coachID != "" {
		kept := make([]scopeEntry, 0, len(entries))
		for _, e := range entries {
			if e.coachID == coachID {
				kept = append(kept, e)
			}
		}
		if len(kept) == 0 {
			return nil, teamErr(http.StatusNotFound, errCoachNotFound)
		}
		entries = kept
		key += ":c=" + coachID
	}

	ident := make(map[string]db.UserIdentity, len(entries))
	for k, v := range known {
		ident[k] = v
	}
	missing := []string{}
	seen := map[string]bool{}
	for _, e := range entries {
		for _, id := range []string{e.coachID, e.headID} {
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			if _, ok := ident[id]; !ok {
				missing = append(missing, id)
			}
		}
	}
	if len(missing) > 0 {
		got, err := s.store.UserIdentities(ctx, missing)
		if err != nil {
			return nil, s.internal(op+".names", err)
		}
		for k, v := range got {
			ident[k] = v
		}
	}

	out := &db.AnalyticsScope{
		ScopeKey:        key,
		Coaches:         make([]db.ScopeCoach, 0, len(entries)),
		CurrentCoachIDs: []string{},
		GeneratedAt:     s.now().UTC(),
	}
	currentSeen := map[string]bool{}
	for _, e := range entries {
		c := ident[e.coachID]
		sc := db.ScopeCoach{
			CoachID: e.coachID, Firstname: c.Firstname, Lastname: c.Lastname,
			HeadCoachID: e.headID, Current: hasOpenPeriod(e.periods), Periods: e.periods,
		}
		if e.headID != "" {
			h := ident[e.headID]
			sc.HeadCoachName = strings.TrimSpace(h.Firstname + " " + h.Lastname)
		}
		out.Coaches = append(out.Coaches, sc)
		if sc.Current && !currentSeen[e.coachID] {
			currentSeen[e.coachID] = true
			out.CurrentCoachIDs = append(out.CurrentCoachIDs, e.coachID)
		}
	}
	return out, nil
}

func hasOpenPeriod(ps []db.PeriodRange) bool {
	for _, p := range ps {
		if p.To == nil {
			return true
		}
	}
	return false
}

// intersectPeriods returns the overlap of two interval lists; a nil To is open-ended. Empty overlaps are dropped.
func intersectPeriods(a, b []db.PeriodRange) []db.PeriodRange {
	out := []db.PeriodRange{}
	for _, x := range a {
		for _, y := range b {
			from := x.From
			if y.From.After(from) {
				from = y.From
			}
			var to *time.Time
			switch {
			case x.To == nil:
				to = y.To
			case y.To == nil || x.To.Before(*y.To):
				to = x.To
			default:
				to = y.To
			}
			if to != nil && !from.Before(*to) {
				continue
			}
			out = append(out, db.PeriodRange{From: from, To: to})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].From.Before(out[j].From) })
	return out
}

// ---------- Clients ----------

func (s *analyticsServiceImpl) LookupClients(ctx context.Context, callerID string, req ScopeRequest, ids []string) ([]db.ClientRow, *models.ApiError) {
	if len(ids) == 0 || len(ids) > MaxLookupIDs {
		return nil, teamErr(http.StatusBadRequest, errLookupIDs)
	}
	scope, apiErr := s.Scope(ctx, callerID, req)
	if apiErr != nil {
		return nil, apiErr
	}
	items := []db.ClientRow{}
	if len(scope.CurrentCoachIDs) == 0 {
		return items, nil
	}
	rows, _, err := s.store.ClientsOfCoaches(ctx, scope.CurrentCoachIDs, db.ScopeClientFilter{ClientIDs: ids, Page: 1, Size: lookupRowLimit})
	if err != nil {
		return nil, s.internal("LookupClients", err)
	}
	for _, r := range rows {
		items = append(items, r.ClientRow)
	}
	return items, nil
}

func (s *analyticsServiceImpl) ListHeadClients(ctx context.Context, callerID string, q HeadClientsQuery) ([]db.ClientRowLinked, int, *models.ApiError) {
	head, apiErr := s.requireActive(ctx, "ListHeadClients.auth", callerID, "coach", true)
	if apiErr != nil {
		return nil, 0, apiErr
	}
	ps, err := s.store.TeamPeriodsByHeads(ctx, []string{head.ExternalID})
	if err != nil {
		return nil, 0, s.internal("ListHeadClients.periods", err)
	}
	coachIDs := []string{head.ExternalID}
	for _, m := range repositories.GroupPeriodsByMember(ps) {
		if hasOpenPeriod(m.Periods) {
			coachIDs = append(coachIDs, m.CoachID)
		}
	}
	if q.CoachID != "" {
		found := false
		for _, id := range coachIDs {
			if id == q.CoachID {
				found = true
				break
			}
		}
		if !found {
			return nil, 0, teamErr(http.StatusNotFound, errCoachNotFound)
		}
		coachIDs = []string{q.CoachID}
	}
	if r := []rune(q.Q); len(r) > maxClientSearchLen {
		q.Q = string(r[:maxClientSearchLen])
	}
	if q.Size < 1 || q.Size > maxClientPageSize {
		q.Size = 20
	}
	if q.Page < 1 {
		q.Page = 1
	}
	rows, total, err := s.store.ClientsOfCoaches(ctx, coachIDs, db.ScopeClientFilter{Q: q.Q, Sort: q.Sort, Page: q.Page, Size: q.Size})
	if err != nil {
		return nil, 0, s.internal("ListHeadClients.list", err)
	}
	return rows, total, nil
}
