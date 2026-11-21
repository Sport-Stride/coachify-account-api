package services

import (
	"context"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
)

// Capability is one permission inside a gym.
type Capability string

const (
	CapClientAssign   Capability = db.CapClientAssign
	CapClientUnassign Capability = db.CapClientUnassign
	CapClientViewAll  Capability = db.CapClientViewAll
	CapMemberInvite   Capability = db.CapMemberInvite
	CapExerciseWrite  Capability = db.CapExerciseWrite
	CapWorkoutWrite   Capability = db.CapWorkoutWrite
	CapProgramWrite   Capability = db.CapProgramWrite
	CapLibraryRead    Capability = db.CapLibraryRead
	CapPlanManage     Capability = db.CapPlanManage

	// CapGymManage is owner-only and CapGymHead head-coach-only: structural checks that no policy or grant changes.
	CapGymManage Capability = db.CapGymManage
	CapGymHead   Capability = db.CapGymHead
)

// Resource types and contexts the authorizer understands.
const (
	ResourceGym    = "gym"
	ResourceClient = "client"
	ResourceMember = "member"
	ResourceInvite = "invite"
	ResourcePlan   = "plan"

	ContextPersonal = "personal"
	ContextGym      = "gym"
)

// Decision reasons.
const (
	ReasonOwner           = "owner"
	ReasonPlatformLocked  = "platform_locked"
	ReasonMemberDeny      = "member_deny"
	ReasonMemberAllow     = "member_allow"
	ReasonRolePolicy      = "role_policy"
	ReasonPlatformPreset  = "platform_preset"
	ReasonNoRule          = "no_rule"
	ReasonNotMember       = "not_a_member"
	ReasonGymRequired     = "gym_required"
	ReasonUnknownCap      = "unknown_capability"
	ReasonPersonalContext = "personal_context"
	ReasonClientNotInGym  = "client_not_in_gym"
	ReasonUnknownResource = "unknown_resource"
)

// Actor is the caller being authorized.
type Actor struct {
	ID   string
	Role string
}

// Resource is what the capability is exercised on. GymID may be empty: the authorizer then uses the actor's own gym.
type Resource struct {
	Type    string
	ID      string
	GymID   string
	Context string
}

// Decision is the answer of the authorizer.
type Decision struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
	GymID   string `json:"gym_id,omitempty"`
	Version int64  `json:"-"`
}

// Authorizer answers "may this actor exercise this capability on this resource".
type Authorizer interface {
	Can(ctx context.Context, a Actor, c Capability, r Resource) (Decision, *models.ApiError)
}

// AuthzInvalidator drops cached decisions of a gym after its policies, grants or members change.
type AuthzInvalidator interface {
	Invalidate(gymID string)
}

// CapabilityResolver lists every configurable capability of one membership at once.
type CapabilityResolver interface {
	Effective(ctx context.Context, m db.GymMember) (map[Capability]bool, int64, *models.ApiError)
}

var settableCaps = func() map[Capability]bool {
	m := make(map[Capability]bool, len(db.GymCapabilities))
	for _, c := range db.GymCapabilities {
		m[Capability(c)] = true
	}
	return m
}()

// IsSettableCapability reports whether a policy or grant may name the capability.
func IsSettableCapability(c string) bool { return settableCaps[Capability(c)] }

// platformPreset is the lowest chain layer, hard-coded (no DB seeding): write capabilities and plan.manage are off.
var platformPreset = map[db.GymMemberRole]map[Capability]bool{
	db.GymMemberRoleHeadCoach: {
		CapClientAssign: true, CapClientUnassign: true, CapClientViewAll: true, CapMemberInvite: true, CapLibraryRead: true,
		CapExerciseWrite: false, CapWorkoutWrite: false, CapProgramWrite: false, CapPlanManage: false,
		CapGymHead: true,
	},
	db.GymMemberRoleCoach: {
		CapLibraryRead:  true,
		CapClientAssign: false, CapClientUnassign: false, CapClientViewAll: false, CapMemberInvite: false,
		CapExerciseWrite: false, CapWorkoutWrite: false, CapProgramWrite: false, CapPlanManage: false,
	},
}
