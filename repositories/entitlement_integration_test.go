//go:build integration

package repositories_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
)

func entitlementRepo(t *testing.T) *repositories.EntitlementRepository {
	t.Helper()
	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		t.Skip("MONGO_TEST_URI not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	require.NoError(t, err)
	database := client.Database(fmt.Sprintf("ent_it_%d", time.Now().UnixNano()))
	t.Cleanup(func() {
		dctx, dcancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer dcancel()
		_ = database.Drop(dctx)
		_ = client.Disconnect(dctx)
	})
	require.NoError(t, repositories.InitializeIndexes(ctx, database))
	ok, missing := repositories.VerifyIndexes(ctx, database)
	require.True(t, ok, "missing indexes: %v", missing)
	return repositories.NewEntitlementRepository(database, "users", "coach_clients")
}

// 50 parallel consumes against a limit of 7 uploads: exactly 7 win, the counter ends at 7.
func TestConsumeUsageIsAtomic(t *testing.T) {
	repo := entitlementRepo(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	var mu sync.Mutex
	won := 0
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, ok, err := repo.ConsumeUsage(ctx, "u1", "file_manager",
				map[string]int64{"uploads": 1, "bytes": 10}, map[string]int64{"uploads": 7})
			assert.NoError(t, err)
			if ok {
				mu.Lock()
				won++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, 7, won)
	usage, err := repo.GetUsage(ctx, []string{"u1"})
	require.NoError(t, err)
	assert.Equal(t, int64(7), usage["u1"].Counters["file_manager"]["uploads"])
	assert.Equal(t, int64(70), usage["u1"].Counters["file_manager"]["bytes"])

	require.NoError(t, repo.ReleaseUsage(ctx, "u1", "file_manager", map[string]int64{"uploads": 100, "bytes": 5}))
	usage, err = repo.GetUsage(ctx, []string{"u1"})
	require.NoError(t, err)
	assert.Equal(t, int64(0), usage["u1"].Counters["file_manager"]["uploads"], "floored at zero")
	assert.Equal(t, int64(65), usage["u1"].Counters["file_manager"]["bytes"])
}

func TestReplaceGrantOptimisticLocking(t *testing.T) {
	repo := entitlementRepo(t)
	ctx := context.Background()
	g := &db.EntitlementGrant{SubjectID: "c1", SubjectRole: "coach", Features: map[string]db.FeatureGrant{}}
	require.NoError(t, repo.ReplaceGrant(ctx, g, 0))
	assert.Equal(t, int64(1), g.Version)
	dup := &db.EntitlementGrant{SubjectID: "c1", SubjectRole: "coach"}
	assert.ErrorIs(t, repo.ReplaceGrant(ctx, dup, 0), repositories.ErrEntitlementVersionConflict)
	assert.ErrorIs(t, repo.ReplaceGrant(ctx, dup, 5), repositories.ErrEntitlementVersionConflict)
	require.NoError(t, repo.ReplaceGrant(ctx, dup, 1))
	assert.Equal(t, int64(2), dup.Version)
}
