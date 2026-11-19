package services

import "context"

// ScopeGym is one gym relationship of a client: the gym and its assigned coach ("" = unassigned pool).
type ScopeGym struct {
	GymID   string
	CoachID string
}

// ResourceScope describes the tenancy of a resource. For a client it is the relationship set.
type ResourceScope struct {
	PersonalCoachID string
	Gyms            []ScopeGym
}

// InGym reports whether the scope has an active relationship with the gym.
func (s *ResourceScope) InGym(gymID string) bool {
	if s == nil {
		return false
	}
	for _, g := range s.Gyms {
		if g.GymID == gymID {
			return true
		}
	}
	return false
}

// ClientScopeSource reads the relationship set of a client (personal coach_clients row, active gym_clients rows).
// P2 has no gym_clients yet, so the authorizer runs without a source and every client is in no gym.
type ClientScopeSource interface {
	ClientScope(ctx context.Context, clientID string) (*ResourceScope, error)
}

// ResourceResolver maps a resource type to the function that loads its scope.
type ResourceResolver map[string]func(ctx context.Context, id string) (*ResourceScope, error)

func emptyScope(context.Context, string) (*ResourceScope, error) { return &ResourceScope{}, nil }

// NewResourceResolver registers client, member, invite and plan. Only client has tenancy of its own; the others
// are scoped by the gym id in the Resource. A nil source resolves every client to no relationships.
func NewResourceResolver(clients ClientScopeSource) ResourceResolver {
	r := ResourceResolver{
		ResourceMember: emptyScope,
		ResourceInvite: emptyScope,
		ResourcePlan:   emptyScope,
		ResourceClient: emptyScope,
	}
	if clients != nil {
		r[ResourceClient] = clients.ClientScope
	}
	return r
}

// gym-derived grants (owner, client.view_all, supervisor) apply to a client only in gym context and only when the
// client has an active relationship with that gym. Personal-context data is reachable only through the coach_clients
// link, which no chain link grants, not even the owner link.
func (r ResourceResolver) clientGate(ctx context.Context, res Resource, gymID string) (Decision, bool, error) {
	if res.Context != ContextGym {
		return Decision{Reason: ReasonPersonalContext, GymID: gymID}, true, nil
	}
	scope, err := r[ResourceClient](ctx, res.ID)
	if err != nil {
		return Decision{}, false, err
	}
	if !scope.InGym(gymID) {
		return Decision{Reason: ReasonClientNotInGym, GymID: gymID}, true, nil
	}
	return Decision{}, false, nil
}
