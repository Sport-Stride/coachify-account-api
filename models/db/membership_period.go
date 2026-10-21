package db

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

const (
	PeriodKindTeam = "team"
	PeriodKindGym  = "gym"

	PeriodReasonAssign       = "assign"
	PeriodReasonReassign     = "reassign"
	PeriodReasonUnassign     = "unassign"
	PeriodReasonDemote       = "demote"
	PeriodReasonAccountGone  = "account_deleted"
	PeriodReasonRemovedByHead = "removed_by_head_coach"
	PeriodReasonBackfill     = "backfill"
	PeriodReasonReconcile    = "reconcile"
	PeriodReasonGymAdd       = "gym_add"
	PeriodReasonGymRemove    = "gym_remove"
	PeriodReasonGymLeave     = "gym_leave"
	PeriodReasonGymArchive   = "gym_archive"
)

// MembershipPeriod is one stretch of time a member belonged to a parent. End nil means still open.
// kind=team: member = coach, parent = head coach. kind=gym: member = head coach, parent = gym.
type MembershipPeriod struct {
	ID       primitive.ObjectID `bson:"_id,omitempty" json:"-"`
	Kind     string             `bson:"kind" json:"kind"`
	MemberID string             `bson:"member_id" json:"member_id"`
	ParentID string             `bson:"parent_id" json:"parent_id"`
	Start    time.Time          `bson:"start" json:"start"`
	End      *time.Time         `bson:"end" json:"end"` // always stored (null when open): the partial unique index relies on it
	Reason   string             `bson:"reason" json:"reason"`
}

// PeriodRange is a membership interval; To nil means open.
type PeriodRange struct {
	From time.Time  `json:"from"`
	To   *time.Time `json:"to"`
}

// CoachPeriods lists every interval a coach spent in a head coach's team.
type CoachPeriods struct {
	CoachID string        `json:"coach_id"`
	Periods []PeriodRange `json:"periods"`
}

// HeadScope is the revenue-attribution scope of a head coach.
type HeadScope struct {
	CurrentCoachIDs []string       `json:"current_coach_ids"`
	Coaches         []CoachPeriods `json:"coaches"`
}
