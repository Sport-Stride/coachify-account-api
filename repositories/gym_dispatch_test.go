package repositories

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson"
)

func TestSupportsTransactions(t *testing.T) {
	assert.True(t, supportsTransactions(bson.M{"setName": "rs0"}))
	assert.True(t, supportsTransactions(bson.M{"msg": "isdbgrid"}))
	assert.False(t, supportsTransactions(bson.M{"isWritablePrimary": true}), "standalone mongod")
	assert.False(t, supportsTransactions(bson.M{"setName": ""}))
}

func TestMyGymsPipelineFiltersActiveRowsAndGyms(t *testing.T) {
	p := myGymsPipeline("users", "cl1")
	m := p[0][0].Value.(bson.D)
	assert.Equal(t, bson.D{{Key: "client_id", Value: "cl1"}, {Key: "status", Value: "active"}}, m)
	assert.Contains(t, bson.Raw(mustMarshal(t, p[1])).String(), "active", "archived gyms are dropped in the gym lookup")
	assert.Equal(t, "$sort", p[len(p)-1][0].Key)
}

func mustMarshal(t *testing.T, d bson.D) []byte {
	b, err := bson.Marshal(d)
	assert.NoError(t, err)
	return b
}

func TestMyGymsPipelineProjectsOwnerAndJoinDate(t *testing.T) {
	p := myGymsPipeline("users", "cl1")
	raw := bson.Raw(mustMarshal(t, p[len(p)-2])).String()
	all := ""
	for _, st := range p {
		all += bson.Raw(mustMarshal(t, st)).String()
	}
	assert.Contains(t, all, `"as": "ow"`, "owner lookup feeds $ow used by the projection")
	assert.NotContains(t, all, `"$REMOVE"`)
	for _, f := range []string{"owner_id", "owner_name", "joined_at"} {
		assert.Contains(t, raw, f)
	}
}
