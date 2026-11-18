package db

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Capabilities an owner can grant or revoke per role and per member.
const (
	CapClientAssign   = "client.assign"
	CapClientUnassign = "client.unassign"
	CapClientViewAll  = "client.view_all"
	CapMemberInvite   = "member.invite"
	CapExerciseWrite  = "exercise.write"
	CapWorkoutWrite   = "workout.write"
	CapProgramWrite   = "program.write"
	CapLibraryRead    = "library.read"
	CapPlanManage     = "plan.manage"
)

// Structural checks the authorizer answers but no policy or grant can change: owner-only and head-coach-only.
const (
	CapGymManage = "gym.manage"
	CapGymHead   = "gym.head"
)

// GymCapabilities lists the nine configurable capabilities.
var GymCapabilities = []string{
	CapClientAssign, CapClientUnassign, CapClientViewAll, CapMemberInvite,
	CapExerciseWrite, CapWorkoutWrite, CapProgramWrite, CapLibraryRead, CapPlanManage,
}

// GrantEffect is the effect of a per-member override.
type GrantEffect string

const (
	GrantAllow GrantEffect = "allow"
	GrantDeny  GrantEffect = "deny"
)

// GymRolePolicy is the owner-set capability defaults of one role in one gym (collection gym_role_policies).
// Capabilities holds only the keys the owner set; missing keys fall through to the platform preset.
type GymRolePolicy struct {
	ID           primitive.ObjectID `bson:"_id,omitempty" json:"-"`
	GymID        string             `bson:"gym_id" json:"gym_id"`
	Role         GymMemberRole      `bson:"role" json:"role"`
	Capabilities map[string]bool    `bson:"capabilities" json:"capabilities"`
	Version      int64              `bson:"version" json:"version"`
	UpdatedBy    string             `bson:"updated_by" json:"updated_by"`
	UpdatedAt    time.Time          `bson:"updated_at" json:"updated_at"`
}

// PermissionGrant is one per-member override (collection permission_grants).
type PermissionGrant struct {
	ID         primitive.ObjectID `bson:"_id,omitempty" json:"-"`
	GymID      string             `bson:"gym_id" json:"-"`
	UserID     string             `bson:"user_id" json:"-"`
	Capability string             `bson:"capability" json:"capability"`
	Effect     GrantEffect        `bson:"effect" json:"effect"`
	GrantedBy  string             `bson:"granted_by" json:"granted_by,omitempty"`
	CreatedAt  time.Time          `bson:"created_at" json:"created_at"`
}
