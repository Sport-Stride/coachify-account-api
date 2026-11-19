package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

// InitializeIndexes creates all necessary indexes for optimal query performance
func InitializeIndexes(ctx context.Context, db *mongo.Database) error {
	startTime := time.Now()
	zap.L().Info("starting index initialization", zap.String("component", "repository"))

	dropLegacyIndexes(ctx, db)

	return ensureIndexes(indexDefinitions(), func(collectionName string, models []mongo.IndexModel) error {
		return createIndexesForCollection(ctx, db, collectionName, models)
	}, startTime)
}

// ErrCriticalIndexes marks a failure on a collection whose unique indexes the app depends on at runtime
// (one personal coach per client, one registration link per coach). The caller must stop the startup on it.
var ErrCriticalIndexes = errors.New("critical index creation failed")

// criticalIndexCollections keep the old fatal guarantee their repository constructors had: a conflicting index
// (for example IndexOptionsConflict) must surface at startup instead of leaving the uniqueness unenforced.
var criticalIndexCollections = map[string]bool{"coach_clients": true, "registration_links": true}

// ensureIndexes creates the indexes of every collection through create. Failures are logged and the other
// collections still run, but a failure on a critical collection is returned wrapped in ErrCriticalIndexes.
func ensureIndexes(indexes map[string][]mongo.IndexModel, create func(collection string, models []mongo.IndexModel) error, startTime time.Time) error {
	var successCount, failCount int
	var critical []error
	for collectionName, indexModels := range indexes {
		if err := create(collectionName, indexModels); err != nil {
			zap.L().Error("failed to create indexes",
				zap.String("component", "repository"),
				zap.String("collection", collectionName),
				zap.Error(err),
			)
			failCount++
			if criticalIndexCollections[collectionName] {
				critical = append(critical, fmt.Errorf("%s: %w", collectionName, err))
			}
			// Continue with other collections instead of failing completely
			continue
		}
		successCount++
	}

	duration := time.Since(startTime)

	if len(critical) > 0 {
		return fmt.Errorf("%w: %w (%d succeeded, %d failed)", ErrCriticalIndexes, errors.Join(critical...), successCount, failCount)
	}
	if failCount > 0 {
		return fmt.Errorf("index initialization completed with errors: %d succeeded, %d failed (duration: %v)",
			successCount, failCount, duration)
	}

	zap.L().Info("index initialization completed",
		zap.String("component", "repository"),
		zap.Int("collections", successCount),
		zap.Duration("duration", duration),
	)
	return nil
}

// indexDefinitions declares the indexes of every collection.
func indexDefinitions() map[string][]mongo.IndexModel {
	return map[string][]mongo.IndexModel{
		// Users collection indexes
		"users": {
			// Unique index on externalid (primary lookup field)
			{
				Keys: bson.D{
					{Key: "externalid", Value: 1},
				},
				Options: options.Index().
					SetUnique(true).
					SetName("idx_users_externalid").
					SetBackground(false), // Create in foreground for immediate availability
			},
			// Unique index on email (for login/lookup)
			{
				Keys: bson.D{
					{Key: "email", Value: 1},
				},
				Options: options.Index().
					SetUnique(true).
					SetSparse(true). // Allow documents without email
					SetName("idx_users_email"),
			},
			// Index on status for filtering active/inactive users
			{
				Keys: bson.D{
					{Key: "status", Value: 1},
				},
				Options: options.Index().
					SetName("idx_users_status"),
			},
			// Compound index for common queries
			{
				Keys: bson.D{
					{Key: "status", Value: 1},
					{Key: "created_at", Value: -1},
				},
				Options: options.Index().
					SetName("idx_users_status_created"),
			},
			// Unique sparse index on matricule_fiscale
			{
				Keys: bson.D{
					{Key: "matricule_fiscale", Value: 1},
				},
				Options: options.Index().
					SetUnique(true).
					SetSparse(true).
					SetName("idx_users_matricule_fiscale"),
			},
			// Index on matricule_fiscale_status for admin review queue
			{
				Keys: bson.D{
					{Key: "matricule_fiscale_status", Value: 1},
					{Key: "matricule_fiscale_submitted_at", Value: -1},
				},
				Options: options.Index().
					SetName("idx_users_matricule_status_submitted"),
			},
			// Admin coach list / head-coach filters
			{
				Keys: bson.D{
					{Key: "role", Value: 1},
					{Key: "is_head_coach", Value: 1},
					{Key: "updated_at", Value: -1},
				},
				Options: options.Index().
					SetName("idx_users_role_headcoach_updated"),
			},
			// Admin user list: role/status filter sorted by signup date
			{
				Keys: bson.D{
					{Key: "role", Value: 1},
					{Key: "status", Value: 1},
					{Key: "created_at", Value: -1},
				},
				Options: options.Index().
					SetName("idx_users_role_status_created"),
			},
			// Admin user list: sort by last login
			{
				Keys: bson.D{{Key: "last_login", Value: -1}},
				Options: options.Index().
					SetName("idx_users_last_login"),
			},
		},

		// Admin action audit trail
		"admin_audit": {
			{
				Keys:    bson.D{{Key: "created_at", Value: -1}},
				Options: options.Index().SetName("idx_admin_audit_created"),
			},
			{
				Keys: bson.D{
					{Key: "target_id", Value: 1},
					{Key: "created_at", Value: -1},
				},
				Options: options.Index().SetName("idx_admin_audit_target_created"),
			},
			{
				Keys: bson.D{
					{Key: "actor_id", Value: 1},
					{Key: "created_at", Value: -1},
				},
				Options: options.Index().SetName("idx_admin_audit_actor_created"),
			},
		},

		// Head-coach teams: a coach belongs to at most one team
		"coach_team_members": {
			{
				Keys: bson.D{{Key: "coach_id", Value: 1}},
				Options: options.Index().
					SetUnique(true).
					SetName("idx_team_members_coach_unique"),
			},
			{
				Keys: bson.D{
					{Key: "head_coach_id", Value: 1},
					{Key: "assigned_at", Value: -1},
				},
				Options: options.Index().
					SetName("idx_team_members_head_assigned"),
			},
		},

		// Membership history (team now, gym later); at most one open period per member
		"membership_periods": {
			{
				Keys: bson.D{{Key: "kind", Value: 1}, {Key: "member_id", Value: 1}},
				Options: options.Index().
					SetUnique(true).
					SetPartialFilterExpression(bson.M{"end": bson.M{"$type": "null"}}).
					SetName("idx_periods_open_unique"),
			},
			{
				Keys: bson.D{
					{Key: "kind", Value: 1},
					{Key: "parent_id", Value: 1},
					{Key: "start", Value: 1},
				},
				Options: options.Index().SetName("idx_periods_parent_start"),
			},
			{
				Keys: bson.D{
					{Key: "kind", Value: 1},
					{Key: "member_id", Value: 1},
					{Key: "start", Value: 1},
				},
				Options: options.Index().SetName("idx_periods_member_start"),
			},
		},

		// Head-coach team audit trail
		"coach_team_audit": {
			{
				Keys: bson.D{
					{Key: "head_coach_id", Value: 1},
					{Key: "created_at", Value: -1},
				},
				Options: options.Index().
					SetName("idx_team_audit_head_created"),
			},
			{
				Keys: bson.D{
					{Key: "coach_id", Value: 1},
					{Key: "created_at", Value: -1},
				},
				Options: options.Index().
					SetName("idx_team_audit_coach_created"),
			},
			{
				Keys: bson.D{
					{Key: "gym_id", Value: 1},
					{Key: "created_at", Value: -1},
				},
				Options: options.Index().
					SetName("idx_team_audit_gym_created"),
			},
		},
		
		// Gyms
			"gyms": {
				{
					Keys:    bson.D{{Key: "owner_id", Value: 1}, {Key: "status", Value: 1}},
					Options: options.Index().SetName("idx_gyms_owner_status"),
				},
				{
					Keys:    bson.D{{Key: "status", Value: 1}, {Key: "created_at", Value: -1}},
					Options: options.Index().SetName("idx_gyms_status_created"),
				},
			},

			// A head coach belongs to at most one gym
			"gym_head_coaches": {
				{
					Keys: bson.D{{Key: "head_coach_id", Value: 1}},
					Options: options.Index().
						SetUnique(true).
						SetName("idx_gym_heads_head_unique"),
				},
				{
					Keys:    bson.D{{Key: "gym_id", Value: 1}, {Key: "added_at", Value: -1}},
					Options: options.Index().SetName("idx_gym_heads_gym_added"),
				},
			},

			// Gym invitations: one pending invitation per gym and email
			"gym_invitations": {
				{
					Keys: bson.D{{Key: "gym_id", Value: 1}, {Key: "email", Value: 1}},
					Options: options.Index().
						SetUnique(true).
						SetPartialFilterExpression(bson.M{"status": "pending", "email": bson.M{"$gt": ""}}).
						SetName("idx_gym_invites_pending_email_unique"),
				},
				{
					Keys: bson.D{{Key: "token_hash", Value: 1}},
					Options: options.Index().
						SetUnique(true).
						SetPartialFilterExpression(bson.M{"token_hash": bson.M{"$exists": true}}).
						SetName("idx_gym_invites_token_unique"),
				},
				{
					Keys:    bson.D{{Key: "email", Value: 1}, {Key: "status", Value: 1}},
					Options: options.Index().SetName("idx_gym_invites_email_status"),
				},
				{
					Keys:    bson.D{{Key: "gym_id", Value: 1}, {Key: "status", Value: 1}, {Key: "created_at", Value: -1}},
					Options: options.Index().SetName("idx_gym_invites_gym_status_created"),
				},
			},

			// Gym owner invites: token hash lookup, one pending invite per email
			"gym_owner_invites": {
				{
					Keys: bson.D{{Key: "token_hash", Value: 1}},
					Options: options.Index().
						SetUnique(true).
						SetName("idx_gym_owner_invites_token_unique"),
				},
				{
					Keys: bson.D{{Key: "email", Value: 1}},
					Options: options.Index().
						SetUnique(true).
						SetPartialFilterExpression(bson.M{"status": "pending"}).
						SetName("idx_gym_owner_invites_pending_email_unique"),
				},
				{
					Keys:    bson.D{{Key: "status", Value: 1}, {Key: "created_at", Value: -1}},
					Options: options.Index().SetName("idx_gym_owner_invites_status_created"),
				},
			},

			// Gym members: one active row per (gym, user); a head coach or coach belongs to one gym at a time
			gymMembersCollection: gymMemberIndexes(),

				// Owner-set role capability defaults and per-member overrides
				gymRolePoliciesCollection:  gymPolicyIndexes(),
				permissionGrantsCollection: permissionGrantIndexes(),

			// Coach clients collection indexes (CRITICAL for ListCoachClients performance)
		"coach_clients": {
			// Primary index: coach_id + created_at (for most common queries)
			// Supports: Finding all clients for a coach with sorting
			{
				Keys: bson.D{
					{Key: "coach_id", Value: 1},
					{Key: "created_at", Value: -1},
				},
				Options: options.Index().
					SetName("idx_coach_clients_coach_created").
					SetBackground(false),
			},
			// Compound index for filtered queries (coach + specific client + date range)
			// Supports: Finding specific client for a coach with date filtering
			{
				Keys: bson.D{
					{Key: "coach_id", Value: 1},
					{Key: "client_id", Value: 1},
					{Key: "created_at", Value: -1},
				},
				Options: options.Index().
					SetName("idx_coach_clients_coach_client_created"),
			},
			// One personal coach per client (GetCoachIDByClientID, payments CanSubscribe and the client /coach/client call
			// depend on it). It also serves the reverse lookup, so the redundant non-unique idx_coach_clients_client is
			// dropped (legacyIndexes). The name is the one live in production: the same key and options under another
			// name would fail with IndexOptionsConflict.
			{
				Keys: bson.D{
					{Key: "client_id", Value: 1},
				},
				Options: options.Index().
					SetUnique(true).
					SetName("client_id_1"),
			},
			// Unique compound index to prevent duplicate associations
			{
				Keys: bson.D{
					{Key: "coach_id", Value: 1},
					{Key: "client_id", Value: 1},
				},
				Options: options.Index().
					SetUnique(true).
					SetName("idx_coach_clients_unique"),
			},
		},
		
		// Gym client relationships (gym_clients), separate from the personal coach_clients above
		gymClientsCollection: gymClientIndexes(),

		// Coach registration links: one link per coach, token lookup. Live production names, kept as they are.
		"registration_links": {
			{
				Keys:    bson.D{{Key: "token", Value: 1}},
				Options: options.Index().SetUnique(true).SetName("token_1"),
			},
			{
				Keys:    bson.D{{Key: "coach_id", Value: 1}},
				Options: options.Index().SetUnique(true).SetName("coach_id_1"),
			},
		},

		// Unified invite links (new collection: first declaration of these names).
		inviteLinksCollection: inviteLinkIndexes(),

		// Coaches collection indexes (if you have a separate coaches collection)
		"coaches": {
			{
				Keys: bson.D{
					{Key: "externalid", Value: 1},
				},
				Options: options.Index().
					SetUnique(true).
					SetName("idx_coaches_externalid"),
			},
			{
				Keys: bson.D{
					{Key: "status", Value: 1},
				},
				Options: options.Index().
					SetName("idx_coaches_status"),
			},
			{
				Keys: bson.D{
					{Key: "created_at", Value: -1},
				},
				Options: options.Index().
					SetName("idx_coaches_created"),
			},
		},

		EntitlementGrantsColl: {
			{
				Keys:    bson.D{{Key: "subject_id", Value: 1}},
				Options: options.Index().SetUnique(true).SetName("idx_ent_grants_subject_unique"),
			},
			{
				Keys:    bson.D{{Key: "coach_id", Value: 1}},
				Options: options.Index().SetName("idx_ent_grants_coach"),
			},
		},
		EntitlementDefaultsColl: {
			{
				Keys:    bson.D{{Key: "plan_id", Value: 1}},
				Options: options.Index().SetUnique(true).SetName("idx_ent_defaults_plan_unique"),
			},
		},
		EntitlementUsageColl: {
			{
				Keys:    bson.D{{Key: "subject_id", Value: 1}},
				Options: options.Index().SetUnique(true).SetName("idx_ent_usage_subject_unique"),
			},
		},
		EntitlementAuditColl: {
			{
				Keys:    bson.D{{Key: "created_at", Value: -1}},
				Options: options.Index().SetName("idx_ent_audit_created"),
			},
			{
				Keys:    bson.D{{Key: "target_id", Value: 1}, {Key: "created_at", Value: -1}},
				Options: options.Index().SetName("idx_ent_audit_target_created"),
			},
			{
				Keys:    bson.D{{Key: "actor_id", Value: 1}, {Key: "created_at", Value: -1}},
				Options: options.Index().SetName("idx_ent_audit_actor_created"),
			},
		},
	}
}

// legacyIndexes are replaced by differently named indexes on the same keys, which MongoDB refuses to create next to them.
var legacyIndexes = map[string][]string{
	"gym_invitations": {"idx_gym_invites_pending_unique"}, // replaced by idx_gym_invites_pending_email_unique (open join links have no email)
	"coach_clients":   {"idx_coach_clients_client"},       // non-unique and redundant next to the unique client_id_1
}

// dropLegacyIndexes removes superseded indexes; a missing index is fine, other failures are logged.
func dropLegacyIndexes(ctx context.Context, db *mongo.Database) {
	for collectionName, names := range legacyIndexes {
		for _, name := range names {
			dropCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			_, err := db.Collection(collectionName).Indexes().DropOne(dropCtx, name)
			cancel()
			var cmdErr mongo.CommandError
			if err != nil && !(errors.As(err, &cmdErr) && (cmdErr.Code == 27 || cmdErr.Code == 26)) { // 27 = IndexNotFound, 26 = NamespaceNotFound
				zap.L().Warn("failed to drop legacy index",
					zap.String("component", "repository"),
					zap.String("collection", collectionName),
					zap.String("index", name),
					zap.Error(err),
				)
			}
		}
	}
}

func createIndexesForCollection(ctx context.Context, db *mongo.Database, collectionName string, indexModels []mongo.IndexModel) error {
	if len(indexModels) == 0 {
		return nil
	}
	
	collection := db.Collection(collectionName)
	
	// Set a reasonable timeout for index creation
	indexCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	
	// Create indexes
	indexNames, err := collection.Indexes().CreateMany(indexCtx, indexModels)
	if err != nil {
		return fmt.Errorf("error creating indexes: %w", err)
	}
	
	zap.L().Info("indexes created",
		zap.String("component", "repository"),
		zap.String("collection", collectionName),
		zap.Int("count", len(indexNames)),
		zap.Strings("names", indexNames),
	)
	
	return nil
}

// VerifyIndexes checks if all required indexes exist (useful for health checks)
func VerifyIndexes(ctx context.Context, db *mongo.Database) (bool, []string) {
	requiredIndexes := map[string][]string{
		"users": {
			"idx_users_externalid",
			"idx_users_email",
			"idx_users_role_headcoach_updated",
			"idx_users_role_status_created",
			"idx_users_last_login",
		},
		"admin_audit": {
			"idx_admin_audit_created",
			"idx_admin_audit_target_created",
			"idx_admin_audit_actor_created",
		},
		"coach_team_members": {
			"idx_team_members_coach_unique",
			"idx_team_members_head_assigned",
		},
		"membership_periods": {
			"idx_periods_open_unique",
			"idx_periods_parent_start",
			"idx_periods_member_start",
		},
		"coach_team_audit": {
			"idx_team_audit_head_created",
			"idx_team_audit_coach_created",
				"idx_team_audit_gym_created",
		},
		"gyms": {
			"idx_gyms_owner_status",
			"idx_gyms_status_created",
		},
		"gym_head_coaches": {
			"idx_gym_heads_head_unique",
			"idx_gym_heads_gym_added",
		},
		"gym_invitations": {
			"idx_gym_invites_pending_email_unique",
			"idx_gym_invites_token_unique",
			"idx_gym_invites_email_status",
			"idx_gym_invites_gym_status_created",
		},
		"gym_owner_invites": {
			"idx_gym_owner_invites_token_unique",
			"idx_gym_owner_invites_pending_email_unique",
			"idx_gym_owner_invites_status_created",
		},
		gymMembersCollection: {
			"idx_gym_members_gym_user_unique",
			"idx_gym_members_coach_unique",
			"idx_gym_members_gym_role_status",
			"idx_gym_members_supervisor",
		},
		gymRolePoliciesCollection: {"idx_gym_role_policies_gym_role_unique"},
		permissionGrantsCollection: {
			"idx_permission_grants_gym_user_cap_unique",
			"idx_permission_grants_gym_user",
		},
		"coach_clients": {
			"idx_coach_clients_coach_created",
			"idx_coach_clients_unique",
			"client_id_1",
		},
		"registration_links": {"token_1", "coach_id_1"},
		gymClientsCollection: {
			"idx_gym_clients_gym_client_unique",
			"idx_gym_clients_client_status",
			"idx_gym_clients_gym_coach_joined",
			"idx_gym_clients_coach_status_joined",
		},
		inviteLinksCollection: {
			"idx_invite_links_token_hash_unique",
			"idx_invite_links_creator_status_created",
			"idx_invite_links_gym_status_created",
			"idx_invite_links_email_status",
		},
		EntitlementGrantsColl:   {"idx_ent_grants_subject_unique", "idx_ent_grants_coach"},
		EntitlementDefaultsColl: {"idx_ent_defaults_plan_unique"},
		EntitlementUsageColl:    {"idx_ent_usage_subject_unique"},
		EntitlementAuditColl:    {"idx_ent_audit_created", "idx_ent_audit_target_created", "idx_ent_audit_actor_created"},
	}
	
	var missingIndexes []string
	
	for collectionName, expectedIndexes := range requiredIndexes {
		collection := db.Collection(collectionName)
		cursor, err := collection.Indexes().List(ctx)
		if err != nil {
			zap.L().Warn("failed to list indexes",
				zap.String("component", "repository"),
				zap.String("collection", collectionName),
				zap.Error(err),
			)
			continue
		}

		var indexes []bson.M
		if err := cursor.All(ctx, &indexes); err != nil {
			zap.L().Warn("failed to decode indexes",
				zap.String("component", "repository"),
				zap.String("collection", collectionName),
				zap.Error(err),
			)
			continue
		}
		
		existingIndexNames := make(map[string]bool)
		for _, index := range indexes {
			if name, ok := index["name"].(string); ok {
				existingIndexNames[name] = true
			}
		}
		
		for _, requiredIndex := range expectedIndexes {
			if !existingIndexNames[requiredIndex] {
				missingIndexes = append(missingIndexes, fmt.Sprintf("%s.%s", collectionName, requiredIndex))
			}
		}
	}
	
	return len(missingIndexes) == 0, missingIndexes
}

// ListIndexes lists all indexes for a collection (useful for debugging)
func ListIndexes(ctx context.Context, db *mongo.Database, collectionName string) error {
	collection := db.Collection(collectionName)

	cursor, err := collection.Indexes().List(ctx)
	if err != nil {
		return fmt.Errorf("failed to list indexes: %w", err)
	}
	defer cursor.Close(ctx)

	var indexes []bson.M
	if err := cursor.All(ctx, &indexes); err != nil {
		return fmt.Errorf("failed to decode indexes: %w", err)
	}

	fields := make([]zap.Field, 0, len(indexes)+1)
	fields = append(fields, zap.String("collection", collectionName))
	for i, index := range indexes {
		fields = append(fields, zap.Any(fmt.Sprintf("index_%d", i+1), index))
	}
	zap.L().Info("collection indexes", fields...)

	return nil
}

// DropAllIndexes removes all indexes from a collection (⚠️ USE WITH CAUTION - Development only!)
func DropAllIndexes(ctx context.Context, db *mongo.Database, collectionName string) error {
	collection := db.Collection(collectionName)
	
	indexView := collection.Indexes()
	cursor, err := indexView.List(ctx)
	if err != nil {
		return fmt.Errorf("failed to list indexes: %w", err)
	}
	defer cursor.Close(ctx)
	
	var indexes []bson.M
	if err := cursor.All(ctx, &indexes); err != nil {
		return fmt.Errorf("failed to decode indexes: %w", err)
	}
	
	droppedCount := 0
	for _, index := range indexes {
		indexName := index["name"].(string)
		// Skip the default _id index (cannot be dropped)
		if indexName == "_id_" {
			continue
		}
		
		if _, err := indexView.DropOne(ctx, indexName); err != nil {
			zap.L().Warn("failed to drop index",
				zap.String("component", "repository"),
				zap.String("index", indexName),
				zap.Error(err),
			)
		} else {
			zap.L().Debug("dropped index",
				zap.String("component", "repository"),
				zap.String("index", indexName),
			)
			droppedCount++
		}
	}

	zap.L().Info("drop indexes completed",
		zap.String("component", "repository"),
		zap.String("collection", collectionName),
		zap.Int("dropped", droppedCount),
	)
	return nil
}