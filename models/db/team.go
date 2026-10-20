package db

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

const (
	TeamAssignerAdmin     = "admin"
	TeamAssignerHeadCoach = "head_coach"

	TeamActionPromote    = "promote"
	TeamActionDemote     = "demote"
	TeamActionAssign     = "assign"
	TeamActionUnassign   = "unassign"
	TeamActionReassign   = "reassign"
	TeamActorRoleAdmin   = "admin"
	TeamActorRoleHead    = "head_coach"
)

// TeamMember links a coach to exactly one head coach (flat hierarchy, coach_id is unique).
type TeamMember struct {
	ID             primitive.ObjectID `bson:"_id,omitempty" json:"-"`
	HeadCoachID    string             `bson:"head_coach_id" json:"head_coach_id"`
	CoachID        string             `bson:"coach_id" json:"coach_id"`
	AssignedBy     string             `bson:"assigned_by" json:"assigned_by"`
	AssignedByRole string             `bson:"assigned_by_role" json:"assigned_by_role"` // admin | head_coach
	AssignedAt     time.Time          `bson:"assigned_at" json:"assigned_at"`
}

// TeamAuditEntry records every team mutation.
type TeamAuditEntry struct {
	ID              primitive.ObjectID `bson:"_id,omitempty" json:"-"`
	Action          string             `bson:"action" json:"action"` // promote|demote|assign|unassign|reassign
	ActorID         string             `bson:"actor_id" json:"actor_id"`
	ActorRole       string             `bson:"actor_role" json:"actor_role"`
	HeadCoachID     string             `bson:"head_coach_id,omitempty" json:"head_coach_id,omitempty"`
	CoachID         string             `bson:"coach_id,omitempty" json:"coach_id,omitempty"`
	PrevHeadCoachID string             `bson:"prev_head_coach_id,omitempty" json:"prev_head_coach_id,omitempty"`
	CreatedAt       time.Time          `bson:"created_at" json:"created_at"`
}

// TeamCoachRow holds only fields that are safe to expose to a head coach.
type TeamCoachRow struct {
	ExternalID        string     `bson:"externalid" json:"external_id"`
	Firstname         string     `bson:"firstname" json:"firstname"`
	Lastname          string     `bson:"lastname" json:"lastname"`
	Email             string     `bson:"email" json:"email"`
	ProfilePicture    string     `bson:"profile_picture,omitempty" json:"profile_picture,omitempty"`
	Status            string     `bson:"status" json:"status"`
	LastLogin         *time.Time `bson:"last_login,omitempty" json:"last_login,omitempty"`
	CreatedAt         *time.Time `bson:"created_at,omitempty" json:"created_at,omitempty"`
	JoinedTeamAt      time.Time  `bson:"joined_team_at" json:"joined_team_at"`
	ClientCount       int        `bson:"client_count" json:"client_count"`
	ActiveClientCount int        `bson:"active_client_count" json:"active_client_count"`
	NewClients30d     int        `bson:"new_clients_30d" json:"new_clients_30d"`
}

// TeamOverview aggregates the whole team.
type TeamOverview struct {
	TeamSize           int `bson:"team_size" json:"team_size"`
	ActiveCoaches      int `bson:"active_coaches" json:"active_coaches"`
	InactiveCoaches14d int `bson:"inactive_coaches_14d" json:"inactive_coaches_14d"`
	TotalClients       int `bson:"total_clients" json:"total_clients"`
	ActiveClients      int `bson:"active_clients" json:"active_clients"`
	NewClients30d      int `bson:"new_clients_30d" json:"new_clients_30d"`
}

// HeadCoachRow is a head coach with the size of their team.
type HeadCoachRow struct {
	ExternalID     string     `bson:"externalid" json:"external_id"`
	Firstname      string     `bson:"firstname" json:"firstname"`
	Lastname       string     `bson:"lastname" json:"lastname"`
	Email          string     `bson:"email" json:"email"`
	ProfilePicture string     `bson:"profile_picture,omitempty" json:"profile_picture,omitempty"`
	Status         string     `bson:"status" json:"status"`
	HeadCoachSince *time.Time `bson:"head_coach_since,omitempty" json:"head_coach_since,omitempty"`
	TeamSize       int        `bson:"team_size" json:"team_size"`
}

// AdminCoachListRow is a coach row for the admin list, enriched with team data.
type AdminCoachListRow struct {
	ExternalID      string    `bson:"externalid"`
	Firstname       string    `bson:"firstname"`
	Lastname        string    `bson:"lastname"`
	Email           string    `bson:"email"`
	Status          string    `bson:"status"`
	ProfilePicture  string    `bson:"profile_picture"`
	UpdatedAt       time.Time `bson:"updated_at"`
	IsHeadCoach     bool      `bson:"is_head_coach"`
	HeadCoachID     string    `bson:"head_coach_id"`
	HeadCoachName   string    `bson:"head_coach_name"`
	TeamSize        int       `bson:"team_size"`
}

// AdminCoachFilter filters the admin coach list. nil bool = no filter.
type AdminCoachFilter struct {
	HeadCoach  *bool
	Unassigned bool
	Q          string // case-insensitive literal match on firstname/lastname/email
}

// TeamListQuery paginates and filters a team's coaches.
type TeamListQuery struct {
	Page   int
	Size   int
	Q      string
	Status string
	Sort   string // name | clients | last_login
}
