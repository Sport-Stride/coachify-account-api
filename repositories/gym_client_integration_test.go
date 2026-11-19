//go:build integration

package repositories_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"coachify-account-api/repositories"
)

// The live production names: InitializeIndexes must be a no-op on them (same key and options under another name
// would fail with IndexOptionsConflict).
func TestInitializeIndexesIdempotentOnLiveNames(t *testing.T) {
	database := gymTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// The full index state production has today.
	_, err := database.Collection("coach_clients").Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "coach_id", Value: 1}, {Key: "client_id", Value: 1}, {Key: "created_at", Value: -1}}, Options: options.Index().SetName("idx_coach_clients_coach_client_created")},
		{Keys: bson.D{{Key: "client_id", Value: 1}}, Options: options.Index().SetName("idx_coach_clients_client")},
		{Keys: bson.D{{Key: "coach_id", Value: 1}, {Key: "client_id", Value: 1}}, Options: options.Index().SetUnique(true).SetName("idx_coach_clients_unique")},
		{Keys: bson.D{{Key: "client_id", Value: 1}}, Options: options.Index().SetUnique(true).SetName("client_id_1")},
		{Keys: bson.D{{Key: "coach_id", Value: 1}, {Key: "created_at", Value: -1}}, Options: options.Index().SetName("idx_coach_clients_coach_created")},
	})
	require.NoError(t, err)
	_, err = database.Collection("registration_links").Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "token", Value: 1}}, Options: options.Index().SetUnique(true).SetName("token_1")},
		{Keys: bson.D{{Key: "coach_id", Value: 1}}, Options: options.Index().SetUnique(true).SetName("coach_id_1")},
	})
	require.NoError(t, err)

	require.NoError(t, repositories.InitializeIndexes(ctx, database))
	first := indexNames(t, ctx, database, "coach_clients")
	require.NoError(t, repositories.InitializeIndexes(ctx, database), "second run must be a no-op")
	assert.ElementsMatch(t, first, indexNames(t, ctx, database, "coach_clients"), "second run changes nothing")

	names := indexNames(t, ctx, database, "coach_clients")
	assert.NotContains(t, names, "idx_coach_clients_client", "the legacy index is dropped")
	for _, n := range []string{"idx_coach_clients_coach_client_created", "idx_coach_clients_unique", "client_id_1", "idx_coach_clients_coach_created"} {
		assert.Contains(t, names, n)
	}
	assert.True(t, indexUnique(t, ctx, database, "coach_clients", "client_id_1"))
	assert.True(t, indexUnique(t, ctx, database, "coach_clients", "idx_coach_clients_unique"))

	reg := indexNames(t, ctx, database, "registration_links")
	assert.Contains(t, reg, "token_1")
	assert.Contains(t, reg, "coach_id_1")
	assert.True(t, indexUnique(t, ctx, database, "registration_links", "token_1"))
	assert.True(t, indexUnique(t, ctx, database, "registration_links", "coach_id_1"))

	ok, missing := repositories.VerifyIndexes(ctx, database)
	assert.True(t, ok, "missing: %v", missing)
}

func indexNames(t *testing.T, ctx context.Context, d *mongo.Database, coll string) []string {
	t.Helper()
	cur, err := d.Collection(coll).Indexes().List(ctx)
	require.NoError(t, err)
	var docs []bson.M
	require.NoError(t, cur.All(ctx, &docs))
	var out []string
	for _, d := range docs {
		out = append(out, d["name"].(string))
	}
	return out
}

func indexUnique(t *testing.T, ctx context.Context, d *mongo.Database, coll, name string) bool {
	t.Helper()
	cur, err := d.Collection(coll).Indexes().List(ctx)
	require.NoError(t, err)
	var docs []bson.M
	require.NoError(t, cur.All(ctx, &docs))
	for _, d := range docs {
		if d["name"] == name {
			u, _ := d["unique"].(bool)
			return u
		}
	}
	return false
}
