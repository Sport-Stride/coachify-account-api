package db

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// AdminAuditEntry records one admin action on a target (user, ...), with before/after snapshots.
type AdminAuditEntry struct {
	ID         primitive.ObjectID     `bson:"_id,omitempty" json:"id"`
	ActorID    string                 `bson:"actor_id" json:"actor_id"`
	Action     string                 `bson:"action" json:"action"`
	TargetType string                 `bson:"target_type" json:"target_type"`
	TargetID   string                 `bson:"target_id" json:"target_id"`
	Before     map[string]interface{} `bson:"before,omitempty" json:"before,omitempty"`
	After      map[string]interface{} `bson:"after,omitempty" json:"after,omitempty"`
	Reason     string                 `bson:"reason,omitempty" json:"reason,omitempty"`
	CreatedAt  time.Time              `bson:"created_at" json:"created_at"`
}

// AdminAuditFilter narrows the audit list. Zero values mean no filter; From is inclusive, To exclusive.
type AdminAuditFilter struct {
	ActorID  string
	TargetID string
	Action   string
	From     time.Time
	To       time.Time
}
