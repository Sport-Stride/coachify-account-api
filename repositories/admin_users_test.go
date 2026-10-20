package repositories

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"

	"coachify-account-api/models/db"
)

func TestUserSearchClause(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		_, ok := userSearchClause("   ")
		assert.False(t, ok)
	})

	t.Run("regex metacharacters are literal", func(t *testing.T) {
		e, ok := userSearchClause("  a.*  ")
		require.True(t, ok)
		assert.Equal(t, "$or", e.Key)
		alts := e.Value.(bson.A)
		require.Len(t, alts, 3)
		rx := alts[0].(bson.D)[0].Value.(bson.D)
		assert.Equal(t, `a\.\*`, rx[0].Value)
		assert.Equal(t, "i", rx[1].Value)
	})

	t.Run("capped at 64 characters", func(t *testing.T) {
		e, ok := userSearchClause(strings.Repeat("e", 100))
		require.True(t, ok)
		rx := e.Value.(bson.A)[0].(bson.D)[0].Value.(bson.D)[0].Value.(string)
		assert.Equal(t, 64, len([]rune(rx)))
	})
}

func TestAdminUserSort(t *testing.T) {
	assert.Equal(t, bson.D{{Key: "created_at", Value: -1}, {Key: "externalid", Value: 1}}, adminUserSort("created_at", "desc"))
	assert.Equal(t, bson.D{{Key: "lastname", Value: 1}, {Key: "firstname", Value: 1}, {Key: "externalid", Value: 1}}, adminUserSort(db.AdminUserSortName, "asc"))
	assert.Equal(t, bson.D{{Key: "last_login", Value: 1}, {Key: "externalid", Value: 1}}, adminUserSort("last_login", "asc"))
	assert.Equal(t, bson.D{{Key: "email", Value: -1}, {Key: "externalid", Value: 1}}, adminUserSort("email", "desc"))
}

func TestStatusUpdateDoc(t *testing.T) {
	now := time.Now()

	t.Run("non-active unsets tokens", func(t *testing.T) {
		for _, st := range []string{"Blocked", "Suspended", "Deactivated"} {
			u := statusUpdateDoc(st, now)
			require.Len(t, u, 2, st)
			assert.Equal(t, "$set", u[0].Key)
			assert.Equal(t, bson.D{{Key: "status", Value: st}, {Key: "updated_at", Value: now}}, u[0].Value)
			assert.Equal(t, "$unset", u[1].Key)
			assert.Equal(t, bson.D{{Key: "token", Value: ""}, {Key: "refresh_token", Value: ""}}, u[1].Value)
		}
	})

	t.Run("active keeps tokens", func(t *testing.T) {
		u := statusUpdateDoc("Active", now)
		require.Len(t, u, 1)
		assert.Equal(t, "$set", u[0].Key)
	})
}
