package repositories

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestBd(t *testing.T) {
	assert.Equal(t, bson.D{{Key: "a", Value: 1}, {Key: "b", Value: "x"}}, bd("a", 1, "b", "x"))
	assert.Empty(t, bd())
	assert.Len(t, bd("a", 1, "dangling"), 1, "an odd trailing key is ignored")
}

func TestBa(t *testing.T) {
	got := ba([]bson.D{{{Key: "$match", Value: 1}}, {{Key: "$sort", Value: 2}}})
	require.Len(t, got, 2)
	assert.Equal(t, bson.D{{Key: "$match", Value: 1}}, got[0])
	assert.Empty(t, ba(nil))
}

func TestPageDefaults(t *testing.T) {
	for _, tc := range []struct{ inP, inS, wantP, wantS int }{
		{0, 0, 1, 20}, {-5, -1, 1, 20}, {3, 50, 3, 50}, {2, 0, 2, 20},
	} {
		p, s := pageDefaults(tc.inP, tc.inS)
		assert.Equal(t, tc.wantP, p)
		assert.Equal(t, tc.wantS, s)
	}
}

func TestNameRegexEscapesInput(t *testing.T) {
	d := nameRegex("a.*(b)", "firstname", "email")
	require.Equal(t, "$or", d[0].Key)
	alts := d[0].Value.(bson.A)
	require.Len(t, alts, 2)
	rx := alts[0].(bson.D)[0].Value.(bson.D)
	assert.Equal(t, `a\.\*\(b\)`, rx[0].Value)
	assert.Equal(t, "i", rx[1].Value)
	assert.Equal(t, "email", alts[1].(bson.D)[0].Key)
}

func TestFacetPageAndFullName(t *testing.T) {
	d := facetPage(bson.A{bd("$skip", 0)})
	require.Equal(t, "$facet", d[0].Key)
	f := d[0].Value.(bson.D)
	assert.Equal(t, "total", f[0].Key)
	assert.Equal(t, "data", f[1].Key)

	fn := fullName("$o.firstname", "$o.lastname")
	assert.Equal(t, "$trim", fn[0].Key)
}

func TestFirstOr(t *testing.T) {
	d := firstOr("$x.y", 0)
	require.Equal(t, "$ifNull", d[0].Key)
	arr := d[0].Value.(bson.A)
	assert.Equal(t, 0, arr[1])
}

func TestGymOID(t *testing.T) {
	oid := primitive.NewObjectID()
	got, ok := gymOID(oid.Hex())
	assert.True(t, ok)
	assert.Equal(t, oid, got)

	for _, bad := range []string{"", "nothex", "123", oid.Hex() + "0"} {
		_, ok := gymOID(bad)
		assert.False(t, ok, bad)
	}
}

func TestPeriodReason(t *testing.T) {
	assert.Equal(t, "default", periodReason(context.Background(), "default"))
	assert.Equal(t, "custom", periodReason(WithPeriodReason(context.Background(), "custom"), "default"))
	assert.Equal(t, "default", periodReason(WithPeriodReason(context.Background(), ""), "default"), "empty override falls back")
}
