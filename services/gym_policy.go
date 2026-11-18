package services

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
)

var (
	errPolicyRole       = errors.New("role must be head_coach or coach")
	errPolicyCapability = errors.New("unknown capability")
	errPolicyConflict   = errors.New("policy was changed by someone else, reload and retry")
	errGrantEffect      = errors.New("effect must be allow or deny")
	errGrantDuplicate   = errors.New("duplicate capability in grants")
	errGrantOwner       = errors.New("the gym owner cannot be targeted")
	errMemberNotFound   = errors.New("member not found")
	errAuthzActor       = errors.New("actor_id is required")
	errAuthzChecks      = errors.New("checks must contain 1 to 200 entries")
	errAuthzCapability  = errors.New("capability is required")
)

// MaxAuthzChecks is the limit of one internal check request.
const MaxAuthzChecks = 200

// AuthzTTLSeconds is how long callers may cache a decision. It is not above DefaultAuthzCacheTTL, so the
// revocation lag across services is at most about twice the cache TTL.
const AuthzTTLSeconds = 10

// GymPolicyStore is the part of the policy repository the policy service needs.
type GymPolicyStore interface {
	GymPolicyReader
	UpsertRolePolicy(ctx context.Context, p *db.GymRolePolicy, expectedVersion int64) (*db.GymRolePolicy, error)
	ReplaceGrants(ctx context.Context, gymID, userID string, grants []db.PermissionGrant) error
	BumpAuthzVersion(ctx context.Context, gymID string) (int64, error)
}

// GymNameStore reads gym names for the gym context.
type GymNameStore interface {
	GetGym(ctx context.Context, id string) (*db.Gym, error)
}

var _ GymPolicyStore = (*repositories.GymPolicyRepository)(nil)

// RolePolicyView is a role policy with the effective capabilities (stored overrides over the platform preset).
type RolePolicyView struct {
	GymID        string          `json:"gym_id"`
	Role         string          `json:"role"`
	Capabilities map[string]bool `json:"capabilities"`
	// Preset is the platform-preset value of each capability for the role (the lowest layer).
	Preset  map[string]bool `json:"preset"`
	Version int64           `json:"version"`
}

// GrantsView is the override set of one member.
type GrantsView struct {
	GymID  string               `json:"gym_id"`
	UserID string               `json:"user_id"`
	Grants []db.PermissionGrant `json:"grants"`
}

// GrantInput is one requested override.
type GrantInput struct {
	Capability string `json:"capability"`
	Effect     string `json:"effect"`
}

// GymMembershipView is one membership with its resolved capabilities.
type GymMembershipView struct {
	GymID        string          `json:"gym_id"`
	GymName      string          `json:"gym_name"`
	Role         string          `json:"role"`
	SupervisorID string          `json:"supervisor_id,omitempty"`
	SeatAssigned bool            `json:"seat_assigned"`
	Capabilities map[string]bool `json:"capabilities"`
	Version      int64           `json:"version"`
}

// GymContextView is the answer of GET /me/gym-context. Personal context means no gym: empty memberships,
// and no gym policy gates the caller's personal clients.
type GymContextView struct {
	Context     string              `json:"context"`
	Memberships []GymMembershipView `json:"memberships"`
	DefaultGym  string              `json:"default_gym_id"`
}

// AuthzCheck is one entry of an internal check request.
type AuthzCheck struct {
	Capability string `json:"capability"`
	GymID      string `json:"gym_id"`
	Resource   struct {
		Type    string `json:"type"`
		ID      string `json:"id"`
		Context string `json:"context"`
	} `json:"resource"`
}

// AuthzCheckDecision is one entry of an internal check response. In report mode a denial is returned as allowed
// with WouldDeny set, so callers can log what enforcement would do.
type AuthzCheckDecision struct {
	Allowed   bool   `json:"allowed"`
	Reason    string `json:"reason"`
	GymID     string `json:"gym_id,omitempty"`
	WouldDeny bool   `json:"would_deny,omitempty"`
}

// AuthzCheckResult is the answer of POST /internal/authz/check.
type AuthzCheckResult struct {
	Decisions  []AuthzCheckDecision `json:"decisions"`
	Version    int64                `json:"version"`
	TTLSeconds int                  `json:"ttl_seconds"`
	Mode       string               `json:"mode"`
}

// GymPolicyService manages role policies and member overrides, serves the gym context and the internal check.
type GymPolicyService interface {
	GetRolePolicy(ctx context.Context, callerID, gymID, role string) (*RolePolicyView, *models.ApiError)
	PutRolePolicy(ctx context.Context, callerID, gymID, role string, caps map[string]bool, version int64) (*RolePolicyView, *models.ApiError)
	GetGrants(ctx context.Context, callerID, gymID, userID string) (*GrantsView, *models.ApiError)
	PutGrants(ctx context.Context, callerID, gymID, userID string, grants []GrantInput) (*GrantsView, *models.ApiError)
	GymContext(ctx context.Context, userID string) (*GymContextView, *models.ApiError)
	// Check answers the internal check; enforce=false is report mode.
	Check(ctx context.Context, actor Actor, checks []AuthzCheck) (*AuthzCheckResult, *models.ApiError)
}

type gymPolicyService struct {
	authz   Authorizer
	resolve CapabilityResolver
	members GymMembershipReader
	store   GymPolicyStore
	gyms    GymNameStore
	users   PolicyUserStore
	inval   AuthzInvalidator
	enforce bool
}

// PolicyUserStore is the part of the user repository the policy service needs.
type PolicyUserStore interface {
	GetUserByExternalIdUpdate(ctx context.Context, userID string) (*db.User, *models.ApiError)
}

// NewGymPolicyService wires the policy service. inval may be nil (no cache in front of the authorizer).
func NewGymPolicyService(authz Authorizer, resolve CapabilityResolver, members GymMembershipReader, store GymPolicyStore,
	gyms GymNameStore, users PolicyUserStore, inval AuthzInvalidator, enforce bool) GymPolicyService {
	return &gymPolicyService{authz: authz, resolve: resolve, members: members, store: store, gyms: gyms, users: users, inval: inval, enforce: enforce}
}

// requireOwner first re-checks, like the legacy requireOwner, that the caller is an Active gym owner account (403).
// It then answers 404 for a gym the caller is not a member of, 403 for a member who is not the owner.
func (s *gymPolicyService) requireOwner(ctx context.Context, callerID, gymID string) *models.ApiError {
	uctx, cancel := context.WithTimeout(ctx, authzTimeout)
	u, apiErr := s.users.GetUserByExternalIdUpdate(uctx, callerID)
	cancel()
	if apiErr != nil {
		if apiErr.Code == http.StatusNotFound {
			return teamErr(http.StatusForbidden, errGymOwnerAccess)
		}
		return authzInternal("requireOwner.user", apiErr.Error)
	}
	if u == nil || u.UserRole != db.RoleGymOwner || u.UserStatus != db.Active {
		return teamErr(http.StatusForbidden, errGymOwnerAccess)
	}
	d, apiErr := s.authz.Can(ctx, Actor{ID: callerID}, CapGymManage, Resource{Type: ResourceGym, ID: gymID, GymID: gymID})
	if apiErr != nil {
		return apiErr
	}
	if d.Allowed {
		return nil
	}
	if d.Reason == ReasonNotMember {
		return teamErr(http.StatusNotFound, errGymNotFound)
	}
	return teamErr(http.StatusForbidden, errGymOwnerAccess)
}

func policyRole(role string) (db.GymMemberRole, bool) {
	r := db.GymMemberRole(role)
	return r, r == db.GymMemberRoleHeadCoach || r == db.GymMemberRoleCoach
}

// effectiveCaps overlays the stored map on the platform preset for the nine configurable capabilities.
func effectiveCaps(role db.GymMemberRole, stored map[string]bool) map[string]bool {
	out := make(map[string]bool, len(db.GymCapabilities))
	for _, c := range db.GymCapabilities {
		out[c] = platformPreset[role][Capability(c)]
		if v, ok := stored[c]; ok {
			out[c] = v
		}
	}
	return out
}

func (s *gymPolicyService) invalidate(gymID string) {
	if s.inval != nil {
		s.inval.Invalidate(gymID)
	}
}

func (s *gymPolicyService) bump(ctx context.Context, op, gymID string) *models.ApiError {
	_, err := s.store.BumpAuthzVersion(ctx, gymID)
	// Invalidate even when the bump failed: the write before it already changed the policy.
	s.invalidate(gymID)
	if err != nil {
		return authzInternal(op+".bump", err)
	}
	return nil
}

func (s *gymPolicyService) GetRolePolicy(ctx context.Context, callerID, gymID, role string) (*RolePolicyView, *models.ApiError) {
	if apiErr := s.requireOwner(ctx, callerID, gymID); apiErr != nil {
		return nil, apiErr
	}
	r, ok := policyRole(role)
	if !ok {
		return nil, teamErr(http.StatusBadRequest, errPolicyRole)
	}
	ctx, cancel := context.WithTimeout(ctx, authzTimeout)
	defer cancel()
	p, err := s.store.GetRolePolicy(ctx, gymID, r)
	if err != nil {
		return nil, authzInternal("GetRolePolicy", err)
	}
	v := &RolePolicyView{GymID: gymID, Role: role}
	var stored map[string]bool
	if p != nil {
		stored, v.Version = p.Capabilities, p.Version
	}
	v.Capabilities, v.Preset = effectiveCaps(r, stored), effectiveCaps(r, nil)
	return v, nil
}

func (s *gymPolicyService) PutRolePolicy(ctx context.Context, callerID, gymID, role string, caps map[string]bool, version int64) (*RolePolicyView, *models.ApiError) {
	if apiErr := s.requireOwner(ctx, callerID, gymID); apiErr != nil {
		return nil, apiErr
	}
	r, ok := policyRole(role)
	if !ok {
		return nil, teamErr(http.StatusBadRequest, errPolicyRole)
	}
	for c := range caps {
		if !IsSettableCapability(c) {
			return nil, teamErr(http.StatusBadRequest, errPolicyCapability)
		}
	}
	if caps == nil {
		caps = map[string]bool{}
	}
	ctx, cancel := context.WithTimeout(ctx, authzTimeout)
	defer cancel()
	saved, err := s.store.UpsertRolePolicy(ctx,
		&db.GymRolePolicy{GymID: gymID, Role: r, Capabilities: caps, UpdatedBy: callerID}, version)
	if err != nil {
		if errors.Is(err, repositories.ErrPolicyVersionConflict) {
			return nil, teamErr(http.StatusConflict, errPolicyConflict)
		}
		s.invalidate(gymID)
		return nil, authzInternal("PutRolePolicy", err)
	}
	if apiErr := s.bump(ctx, "PutRolePolicy", gymID); apiErr != nil {
		return nil, apiErr
	}
	return &RolePolicyView{GymID: gymID, Role: role, Capabilities: effectiveCaps(r, saved.Capabilities), Preset: effectiveCaps(r, nil), Version: saved.Version}, nil
}

// grantTarget loads the target member: unknown is 404, the owner cannot be targeted.
func (s *gymPolicyService) grantTarget(ctx context.Context, gymID, userID string) *models.ApiError {
	ctx, cancel := context.WithTimeout(ctx, authzTimeout)
	defer cancel()
	m, err := s.members.MembershipOf(ctx, userID, gymID)
	if err != nil {
		return authzInternal("grantTarget", err)
	}
	if m == nil {
		return teamErr(http.StatusNotFound, errMemberNotFound)
	}
	if m.Role == db.GymMemberRoleOwner {
		return teamErr(http.StatusBadRequest, errGrantOwner)
	}
	return nil
}

func (s *gymPolicyService) GetGrants(ctx context.Context, callerID, gymID, userID string) (*GrantsView, *models.ApiError) {
	if apiErr := s.requireOwner(ctx, callerID, gymID); apiErr != nil {
		return nil, apiErr
	}
	if apiErr := s.grantTarget(ctx, gymID, userID); apiErr != nil {
		return nil, apiErr
	}
	ctx, cancel := context.WithTimeout(ctx, authzTimeout)
	defer cancel()
	rows, err := s.store.ListGrants(ctx, gymID, userID)
	if err != nil {
		return nil, authzInternal("GetGrants", err)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Capability < rows[j].Capability })
	return &GrantsView{GymID: gymID, UserID: userID, Grants: rows}, nil
}

func (s *gymPolicyService) PutGrants(ctx context.Context, callerID, gymID, userID string, in []GrantInput) (*GrantsView, *models.ApiError) {
	if apiErr := s.requireOwner(ctx, callerID, gymID); apiErr != nil {
		return nil, apiErr
	}
	if apiErr := s.grantTarget(ctx, gymID, userID); apiErr != nil {
		return nil, apiErr
	}
	now := time.Now()
	seen := make(map[string]bool, len(in))
	rows := make([]db.PermissionGrant, 0, len(in))
	for _, g := range in {
		effect := db.GrantEffect(strings.ToLower(g.Effect))
		switch {
		case !IsSettableCapability(g.Capability):
			return nil, teamErr(http.StatusBadRequest, errPolicyCapability)
		case effect != db.GrantAllow && effect != db.GrantDeny:
			return nil, teamErr(http.StatusBadRequest, errGrantEffect)
		case seen[g.Capability]:
			return nil, teamErr(http.StatusBadRequest, errGrantDuplicate)
		}
		seen[g.Capability] = true
		rows = append(rows, db.PermissionGrant{Capability: g.Capability, Effect: effect, GrantedBy: callerID, CreatedAt: now})
	}
	ctx, cancel := context.WithTimeout(ctx, authzTimeout)
	defer cancel()
	if err := s.store.ReplaceGrants(ctx, gymID, userID, rows); err != nil {
		// A failed bulk write may have applied part of the set: drop cached decisions anyway.
		s.invalidate(gymID)
		return nil, authzInternal("PutGrants", err)
	}
	if apiErr := s.bump(ctx, "PutGrants", gymID); apiErr != nil {
		return nil, apiErr
	}
	for i := range rows {
		rows[i].GymID, rows[i].UserID = gymID, userID
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Capability < rows[j].Capability })
	return &GrantsView{GymID: gymID, UserID: userID, Grants: rows}, nil
}

func (s *gymPolicyService) GymContext(ctx context.Context, userID string) (*GymContextView, *models.ApiError) {
	ctx, cancel := context.WithTimeout(ctx, authzTimeout)
	defer cancel()
	rows, err := s.members.MembershipsOf(ctx, userID)
	if err != nil {
		return nil, authzInternal("GymContext.memberships", err)
	}
	view := &GymContextView{Context: ContextPersonal, Memberships: []GymMembershipView{}}
	for _, m := range rows {
		caps, ver, apiErr := s.resolve.Effective(ctx, m)
		if apiErr != nil {
			return nil, apiErr
		}
		mv := GymMembershipView{
			GymID: m.GymID, Role: string(m.Role), SupervisorID: m.SupervisorID, SeatAssigned: m.SeatAssigned,
			Capabilities: make(map[string]bool, len(caps)), Version: ver,
		}
		for c, v := range caps {
			mv.Capabilities[string(c)] = v
		}
		if g, err := s.gyms.GetGym(ctx, m.GymID); err != nil {
			return nil, authzInternal("GymContext.gym", err)
		} else if g != nil {
			mv.GymName = g.Name
		}
		view.Memberships = append(view.Memberships, mv)
	}
	if best := pickMembership(rows); best != nil {
		view.Context, view.DefaultGym = ContextGym, best.GymID
	}
	return view, nil
}

func (s *gymPolicyService) Check(ctx context.Context, actor Actor, checks []AuthzCheck) (*AuthzCheckResult, *models.ApiError) {
	if actor.ID == "" {
		return nil, teamErr(http.StatusBadRequest, errAuthzActor)
	}
	if len(checks) == 0 || len(checks) > MaxAuthzChecks {
		return nil, teamErr(http.StatusBadRequest, errAuthzChecks)
	}
	out := &AuthzCheckResult{Decisions: make([]AuthzCheckDecision, 0, len(checks)), TTLSeconds: AuthzTTLSeconds, Mode: "enforce"}
	if !s.enforce {
		out.Mode = "report"
	}
	for _, ck := range checks {
		if ck.Capability == "" {
			return nil, teamErr(http.StatusBadRequest, errAuthzCapability)
		}
		d, apiErr := s.authz.Can(ctx, actor, Capability(ck.Capability),
			Resource{Type: ck.Resource.Type, ID: ck.Resource.ID, GymID: ck.GymID, Context: ck.Resource.Context})
		if apiErr != nil {
			return nil, apiErr
		}
		if d.Version > out.Version {
			out.Version = d.Version
		}
		cd := AuthzCheckDecision{Allowed: d.Allowed, Reason: d.Reason, GymID: d.GymID}
		if !s.enforce && !d.Allowed {
			cd.Allowed, cd.WouldDeny = true, true
		}
		out.Decisions = append(out.Decisions, cd)
	}
	return out, nil
}
