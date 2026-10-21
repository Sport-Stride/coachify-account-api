//go:build integration

package repositories_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
)

func TestIntegrationClientsOfCoaches(t *testing.T) {
	f := setup(t) // skips without MONGO_TEST_URI
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo := repositories.NewAnalyticsRepository(f.team, nil)

	t.Run("clients of several coaches, missing client users dropped", func(t *testing.T) {
		rows, total, err := repo.ClientsOfCoaches(ctx, []string{c1, c2}, db.ScopeClientFilter{})
		require.NoError(t, err)
		assert.Equal(t, 5, total) // ghostclient has no user document
		assert.Len(t, rows, 5)
		for _, r := range rows {
			assert.NotEmpty(t, r.CoachName)
			assert.False(t, r.LinkedAt.IsZero())
		}
		assert.Equal(t, "cl1", rows[0].ExternalID) // sorted by client name: Client cl1..cl5
		assert.Equal(t, "Zed Alpha", rows[0].CoachName)
		assert.Equal(t, "Dana Beta", rows[4].CoachName)
	})

	t.Run("restricted to the given client ids and coaches", func(t *testing.T) {
		rows, total, err := repo.ClientsOfCoaches(ctx, []string{c1, c2}, db.ScopeClientFilter{ClientIDs: []string{"cl1", "cl6", "cl7"}})
		require.NoError(t, err)
		assert.Equal(t, 1, total) // cl6 belongs to c4, cl7 to c5
		assert.Equal(t, "cl1", rows[0].ExternalID)
		assert.Equal(t, c1, rows[0].CoachID)
	})

	t.Run("no coaches means no rows", func(t *testing.T) {
		rows, total, err := repo.ClientsOfCoaches(ctx, nil, db.ScopeClientFilter{})
		require.NoError(t, err)
		assert.Zero(t, total)
		assert.Empty(t, rows)
	})

	t.Run("pagination and total", func(t *testing.T) {
		rows, total, err := repo.ClientsOfCoaches(ctx, []string{c1, c2}, db.ScopeClientFilter{Page: 2, Size: 2})
		require.NoError(t, err)
		assert.Equal(t, 5, total)
		assert.Len(t, rows, 2)
		rows, _, err = repo.ClientsOfCoaches(ctx, []string{c1, c2}, db.ScopeClientFilter{Page: 3, Size: 2})
		require.NoError(t, err)
		assert.Len(t, rows, 1)
	})

	t.Run("q is a literal match", func(t *testing.T) {
		rows, total, err := repo.ClientsOfCoaches(ctx, []string{c1, c2}, db.ScopeClientFilter{Q: "cl1@"})
		require.NoError(t, err)
		assert.Equal(t, 1, total)
		assert.Equal(t, "cl1", rows[0].ExternalID)

		_, total, err = repo.ClientsOfCoaches(ctx, []string{c1, c2}, db.ScopeClientFilter{Q: ".*"})
		require.NoError(t, err)
		assert.Zero(t, total) // metacharacters are escaped
	})

	t.Run("sort by linked_at is newest first", func(t *testing.T) {
		rows, _, err := repo.ClientsOfCoaches(ctx, []string{c1, c2}, db.ScopeClientFilter{Sort: "linked_at"})
		require.NoError(t, err)
		require.Len(t, rows, 5)
		assert.Equal(t, "cl1", rows[0].ExternalID) // linked 5 days ago
		for i := 1; i < len(rows); i++ {
			assert.False(t, rows[i].LinkedAt.After(rows[i-1].LinkedAt))
		}
	})

	t.Run("identities and coach listing", func(t *testing.T) {
		ids, err := repo.UserIdentities(ctx, []string{c1, "missing"})
		require.NoError(t, err)
		assert.Equal(t, "Zed", ids[c1].Firstname)
		assert.NotContains(t, ids, "missing")

		coaches, err := repo.CoachIdentities(ctx, 3)
		require.NoError(t, err)
		assert.Len(t, coaches, 3)
		for _, c := range coaches {
			assert.NotEqual(t, "admin1", c.ExternalID)
		}
	})
}
