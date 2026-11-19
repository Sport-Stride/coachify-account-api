package repositories

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/mongo"

	"coachify-account-api/models/db"
)

// render prints a pipeline so substring assertions can inspect every stage, nested sub-pipelines included.
func render(v interface{}) string { return fmt.Sprintf("%v", v) }

func TestGymViewsNeverReadPersonalCoachClients(t *testing.T) {
	yes := true
	pipelines := map[string]string{
		"gym client list": render(gymClientRowPipeline("users", "g1", db.GymClientQuery{
			Assigned: &yes, CoachID: "c1", CoachIDs: []string{"c1"}, HeadCoachID: "h1", Q: "x", Sort: "linked_at",
		})),
		"head coach client list": render(gymClientRowPipeline("users", "g1", db.GymClientQuery{HeadCoachID: "h1"})),
		"clients of coaches":     render(clientsOfCoachesPipeline("users", []string{"c1"}, db.ScopeClientFilter{ClientIDs: []string{"a"}})),
		"client stats":           render((&TeamRepository{usersName: "users"}).clientStatsStages(time.Now())),
	}
	for name, p := range pipelines {
		assert.NotContains(t, p, "coach_clients", name)
		assert.Contains(t, p, string(db.GymClientStatusActive), name+" must only read active gym client rows")
	}
}

// The gym and head coach repositories must not even reference the personal collection.
func TestGymSourcesDoNotReferencePersonalClients(t *testing.T) {
	for _, file := range []string{"gym.go", "analytics.go", "gym_client.go"} {
		b, err := os.ReadFile(file)
		require.NoError(t, err)
		src := string(b)
		for _, line := range strings.Split(src, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			assert.NotContains(t, line, "clientsName", file)
			assert.NotContains(t, line, "team.clients", file)
			assert.NotContains(t, line, `"coach_clients"`, file)
		}
	}
}

func TestGymClientRowPipelineFilters(t *testing.T) {
	no := false
	yes := true

	t.Run("pool filter targets empty or missing coach", func(t *testing.T) {
		p := render(gymClientRowPipeline("users", "g1", db.GymClientQuery{Assigned: &no}))
		assert.Contains(t, p, "$in")
	})
	t.Run("assigned filter excludes the pool", func(t *testing.T) {
		p := render(gymClientRowPipeline("users", "g1", db.GymClientQuery{Assigned: &yes}))
		assert.Contains(t, p, "$nin")
	})
	t.Run("scope and head filters are applied", func(t *testing.T) {
		p := render(gymClientRowPipeline("users", "g1", db.GymClientQuery{CoachIDs: []string{"c1", "c2"}, HeadCoachID: "h1"}))
		assert.Contains(t, p, "c1")
		assert.Contains(t, p, "h1")
	})
	t.Run("only active rows of the gym", func(t *testing.T) {
		p := render(gymClientRowPipeline("users", "g1", db.GymClientQuery{}))
		assert.Contains(t, p, "g1")
		assert.Contains(t, p, string(db.GymClientStatusActive))
	})
}

func TestIndexDefinitions(t *testing.T) {
	defs := indexDefinitions()

	t.Run("client_id_1 keeps the live name and is unique", func(t *testing.T) {
		var found bool
		for _, m := range defs["coach_clients"] {
			if m.Options != nil && m.Options.Name != nil && *m.Options.Name == "client_id_1" {
				found = true
				require.NotNil(t, m.Options.Unique)
				assert.True(t, *m.Options.Unique)
			}
		}
		assert.True(t, found)
	})

	t.Run("registration link indexes keep their live names", func(t *testing.T) {
		names := map[string]bool{}
		for _, m := range defs["registration_links"] {
			names[*m.Options.Name] = true
		}
		assert.True(t, names["token_1"])
		assert.True(t, names["coach_id_1"])
	})

	t.Run("no declared index is a legacy one", func(t *testing.T) {
		for coll, legacy := range legacyIndexes {
			for _, m := range defs[coll] {
				for _, l := range legacy {
					assert.NotEqual(t, l, *m.Options.Name, coll)
				}
			}
		}
		assert.Contains(t, legacyIndexes["coach_clients"], "idx_coach_clients_client")
	})

	t.Run("gym_clients indexes are declared", func(t *testing.T) {
		assert.Len(t, defs[gymClientsCollection], 4)
	})
}

func TestEnsureIndexesCriticalCollectionsAreFatal(t *testing.T) {
	defs := map[string][]mongo.IndexModel{"coach_clients": nil, "registration_links": nil, "users": nil}
	conflict := errors.New("IndexOptionsConflict: index already exists with a different name")

	failing := func(bad string) func(string, []mongo.IndexModel) error {
		return func(coll string, _ []mongo.IndexModel) error {
			if coll == bad {
				return conflict
			}
			return nil
		}
	}
	for _, coll := range []string{"coach_clients", "registration_links"} {
		err := ensureIndexes(defs, failing(coll), time.Now())
		require.Error(t, err, coll)
		assert.ErrorIs(t, err, ErrCriticalIndexes, coll)
		assert.ErrorIs(t, err, conflict, "the root cause must surface")
	}

	// Other collections stay log-only: an error is returned but it is not critical.
	err := ensureIndexes(defs, failing("users"), time.Now())
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrCriticalIndexes)

	assert.NoError(t, ensureIndexes(defs, failing("none"), time.Now()))
}
