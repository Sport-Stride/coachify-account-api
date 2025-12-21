package repositories

import (
	"context"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// InitializeIndexes creates all necessary indexes for optimal query performance
func InitializeIndexes(ctx context.Context, db *mongo.Database) error {
	startTime := time.Now()
	log.Println("Starting index initialization...")
	
	// Define indexes for each collection
	indexes := map[string][]mongo.IndexModel{
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
		},
		
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
			// Index for reverse lookup (finding coach by client)
			{
				Keys: bson.D{
					{Key: "client_id", Value: 1},
				},
				Options: options.Index().
					SetName("idx_coach_clients_client"),
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
	}
	
	// Create indexes for each collection
	var successCount, failCount int
	for collectionName, indexModels := range indexes {
		if err := createIndexesForCollection(ctx, db, collectionName, indexModels); err != nil {
			log.Printf("❌ Failed to create indexes for collection '%s': %v", collectionName, err)
			failCount++
			// Continue with other collections instead of failing completely
			continue
		}
		successCount++
	}
	
	duration := time.Since(startTime)
	
	if failCount > 0 {
		return fmt.Errorf("index initialization completed with errors: %d succeeded, %d failed (duration: %v)", 
			successCount, failCount, duration)
	}
	
	log.Printf("✅ Index initialization completed successfully: %d collections processed in %v", 
		successCount, duration)
	return nil
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
	
	log.Printf("✅ Created %d indexes for collection '%s': %v", 
		len(indexNames), collectionName, indexNames)
	
	return nil
}

// VerifyIndexes checks if all required indexes exist (useful for health checks)
func VerifyIndexes(ctx context.Context, db *mongo.Database) (bool, []string) {
	requiredIndexes := map[string][]string{
		"users": {
			"idx_users_externalid",
			"idx_users_email",
		},
		"coach_clients": {
			"idx_coach_clients_coach_created",
			"idx_coach_clients_unique",
		},
	}
	
	var missingIndexes []string
	
	for collectionName, expectedIndexes := range requiredIndexes {
		collection := db.Collection(collectionName)
		cursor, err := collection.Indexes().List(ctx)
		if err != nil {
			log.Printf("Failed to list indexes for %s: %v", collectionName, err)
			continue
		}
		
		var indexes []bson.M
		if err := cursor.All(ctx, &indexes); err != nil {
			log.Printf("Failed to decode indexes for %s: %v", collectionName, err)
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
	
	log.Printf("📋 Indexes for collection '%s':", collectionName)
	for i, index := range indexes {
		log.Printf("  %d. %v", i+1, index)
	}
	
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
			log.Printf("⚠️ Warning: failed to drop index %s: %v", indexName, err)
		} else {
			log.Printf("🗑️ Dropped index: %s", indexName)
			droppedCount++
		}
	}
	
	log.Printf("Dropped %d indexes from collection '%s'", droppedCount, collectionName)
	return nil
}