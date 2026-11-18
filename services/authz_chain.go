package services

import (
	"context"
	"net/http"
	"time"

	"go.uber.org/zap"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
)

// authzTimeout bounds one authorization (membership, policy, grants and version reads).
const authzTimeout = 10 * time.Second

// GymPolicyReader is the part of the policy repository the chain reads.
type GymPolicyReader interface {
	GetRolePolicy(ctx context.Context, gymID string, role db.GymMemberRole) (*db.GymRolePolicy, error)
	ListGrants(ctx context.Context, gymID, userID string) ([]db.PermissionGrant, error)
	GetAuthzVersion(ctx context.Context, gymID string) (int64, error)
}

// AuthzQuery is what the links of the chain share for one decision. Policy and grants load lazily, once.
type AuthzQuery struct {
	Actor  Actor
	Cap    Capability
	Res    Resource
	GymID  string
	Member *db.GymMember

	policies GymPolicyReader
	policy   *db.GymRolePolicy
	grants   map[Capability]db.GrantEffect
	loaded   struct{ policy, grants bool }
	// Err is set by a link whose read failed; the chain stops and answers 500.
	Err error
}

func (q *AuthzQuery) rolePolicy(ctx context.Context) *db.GymRolePolicy {
	if !q.loaded.policy {
		q.loaded.policy = true
		p, err := q.policies.GetRolePolicy(ctx, q.GymID, q.Member.Role)
		if err != nil {
			q.Err = err
		}
		q.policy = p
	}
	return q.policy
}

func (q *AuthzQuery) memberGrants(ctx context.Context) map[Capability]db.GrantEffect {
	if !q.loaded.grants {
		q.loaded.grants = true
		rows, err := q.policies.ListGrants(ctx, q.GymID, q.Actor.ID)
		if err != nil {
			q.Err = err
		}
		q.grants = make(map[Capability]db.GrantEffect, len(rows))
		for _, g := range rows {
			q.grants[Capability(g.Capability)] = g.Effect
		}
	}
	return q.grants
}

// CapabilityLink is one handler of the Chain of Responsibility. handled=false passes the query on.
type CapabilityLink interface {
	Resolve(ctx context.Context, q *AuthzQuery) (Decision, bool)
}

// ownerLink grants everything inside the gym to its owner. It can never be revoked: it runs before every deny.
type ownerLink struct{}

func (ownerLink) Resolve(_ context.Context, q *AuthzQuery) (Decision, bool) {
	if q.Member.Role == db.GymMemberRoleOwner {
		return Decision{Allowed: true, Reason: ReasonOwner}, true
	}
	return Decision{}, false
}

type memberDenyLink struct{}

func (memberDenyLink) Resolve(ctx context.Context, q *AuthzQuery) (Decision, bool) {
	if q.memberGrants(ctx)[q.Cap] == db.GrantDeny {
		return Decision{Reason: ReasonMemberDeny}, true
	}
	return Decision{}, false
}

type memberAllowLink struct{}

func (memberAllowLink) Resolve(ctx context.Context, q *AuthzQuery) (Decision, bool) {
	if q.memberGrants(ctx)[q.Cap] == db.GrantAllow {
		return Decision{Allowed: true, Reason: ReasonMemberAllow}, true
	}
	return Decision{}, false
}

type rolePolicyLink struct{}

func (rolePolicyLink) Resolve(ctx context.Context, q *AuthzQuery) (Decision, bool) {
	if p := q.rolePolicy(ctx); p != nil {
		if v, ok := p.Capabilities[string(q.Cap)]; ok {
			return Decision{Allowed: v, Reason: ReasonRolePolicy}, true
		}
	}
	return Decision{}, false
}

type platformPresetLink struct{}

func (platformPresetLink) Resolve(_ context.Context, q *AuthzQuery) (Decision, bool) {
	if v, ok := platformPreset[q.Member.Role][q.Cap]; ok {
		return Decision{Allowed: v, Reason: ReasonPlatformPreset}, true
	}
	return Decision{}, false
}

// chainAuthorizer implements Authorizer and CapabilityResolver over the ordered links.
type chainAuthorizer struct {
	members  GymMembershipReader
	policies GymPolicyReader
	resolver ResourceResolver
	links    []CapabilityLink
}

// NewChainAuthorizer builds the chain: owner > member deny > member allow > role policy > platform preset > deny.
// A nil resolver registers the default one (no client relationships).
func NewChainAuthorizer(members GymMembershipReader, policies GymPolicyReader, resolver ResourceResolver) *chainAuthorizer {
	if resolver == nil {
		resolver = NewResourceResolver(nil)
	}
	return &chainAuthorizer{
		members: members, policies: policies, resolver: resolver,
		links: []CapabilityLink{ownerLink{}, memberDenyLink{}, memberAllowLink{}, rolePolicyLink{}, platformPresetLink{}},
	}
}

func authzInternal(op string, err error) *models.ApiError {
	zap.L().Error("authz failure", zap.String("component", "authz"), zap.String("op", op), zap.Error(err))
	return teamErr(http.StatusInternalServerError, errInternal)
}

// pickMembership resolves the gym of an actor that did not name one: the highest-ranked membership
// (a coach or head coach belongs to one gym, an owner row wins over the others).
func pickMembership(rows []db.GymMember) *db.GymMember {
	var best *db.GymMember
	for i := range rows {
		if best == nil || memberRank(rows[i].Role) > memberRank(best.Role) {
			best = &rows[i]
		}
	}
	return best
}

func (a *chainAuthorizer) Can(ctx context.Context, actor Actor, c Capability, r Resource) (Decision, *models.ApiError) {
	if !settableCaps[c] && c != CapGymManage && c != CapGymHead {
		return Decision{Reason: ReasonUnknownCap}, nil
	}
	if r.Context == ContextPersonal || (r.Type == ResourceClient && r.Context != ContextGym) {
		return Decision{Reason: ReasonPersonalContext, GymID: r.GymID}, nil
	}
	if r.Type != "" && r.Type != ResourceGym {
		if _, ok := a.resolver[r.Type]; !ok {
			return Decision{Reason: ReasonUnknownResource, GymID: r.GymID}, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, authzTimeout)
	defer cancel()

	var m *db.GymMember
	if r.GymID != "" {
		row, err := a.members.MembershipOf(ctx, actor.ID, r.GymID)
		if err != nil {
			return Decision{}, authzInternal("Can.membership", err)
		}
		m = row
	} else {
		rows, err := a.members.MembershipsOf(ctx, actor.ID)
		if err != nil {
			return Decision{}, authzInternal("Can.memberships", err)
		}
		m = pickMembership(rows)
	}
	if m == nil {
		return Decision{Reason: ReasonNotMember, GymID: r.GymID}, nil
	}
	gymID := m.GymID
	ver, err := a.policies.GetAuthzVersion(ctx, gymID)
	if err != nil {
		return Decision{}, authzInternal("Can.version", err)
	}
	if r.Type == ResourceClient {
		d, handled, err := a.resolver.clientGate(ctx, r, gymID)
		if err != nil {
			return Decision{}, authzInternal("Can.clientScope", err)
		}
		if handled {
			d.Version = ver
			return d, nil
		}
	}
	q := &AuthzQuery{Actor: actor, Cap: c, Res: r, GymID: gymID, Member: m, policies: a.policies}
	d := a.run(ctx, q)
	if q.Err != nil {
		return Decision{}, authzInternal("Can.link", q.Err)
	}
	d.GymID, d.Version = gymID, ver
	return d, nil
}

// run walks the links, then the terminal deny.
func (a *chainAuthorizer) run(ctx context.Context, q *AuthzQuery) Decision {
	for _, l := range a.links {
		d, handled := l.Resolve(ctx, q)
		if q.Err != nil {
			return Decision{}
		}
		if handled {
			return d
		}
	}
	return Decision{Reason: ReasonNoRule}
}

// Effective resolves every configurable capability of a membership with one policy and one grants read.
func (a *chainAuthorizer) Effective(ctx context.Context, m db.GymMember) (map[Capability]bool, int64, *models.ApiError) {
	ctx, cancel := context.WithTimeout(ctx, authzTimeout)
	defer cancel()
	ver, err := a.policies.GetAuthzVersion(ctx, m.GymID)
	if err != nil {
		return nil, 0, authzInternal("Effective.version", err)
	}
	q := &AuthzQuery{Actor: Actor{ID: m.UserID}, GymID: m.GymID, Member: &m, policies: a.policies}
	out := make(map[Capability]bool, len(db.GymCapabilities))
	for _, c := range db.GymCapabilities {
		q.Cap = Capability(c)
		d := a.run(ctx, q)
		if q.Err != nil {
			return nil, 0, authzInternal("Effective.link", q.Err)
		}
		out[q.Cap] = d.Allowed
	}
	return out, ver, nil
}
