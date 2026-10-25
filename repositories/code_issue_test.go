package repositories

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson"

	"coachify-account-api/core"
)

func TestIssueCodeQuery_PreservesAttempts(t *testing.T) {
	now := time.Now()
	prev := now.Add(-2 * time.Minute)
	b := core.CodeBudget{LastIssuedAt: now, RegenWindowStart: now, RegenCount: 2}
	f, u := issueCodeQuery("a@b.c", "confirm_code", true, prev, "123456", now.Add(time.Hour), b, false, now)
	set := u["$set"].(bson.M)
	assert.NotContains(t, set, "confirm_code.attempts")
	assert.NotContains(t, set, "confirm_code.lockeduntil")
	assert.Equal(t, "123456", set["confirm_code.code"])
	assert.Equal(t, 2, set["confirm_code.regencount"])
	assert.Contains(t, f, "$or")
}

func TestIssueCodeQuery_ResetsOnlyAfterLockout(t *testing.T) {
	now := time.Now()
	_, u := issueCodeQuery("a@b.c", "reset_password_code", true, now, "1", now, core.CodeBudget{}, true, now)
	set := u["$set"].(bson.M)
	assert.Equal(t, 0, set["reset_password_code.attempts"])
}

func TestIssueCodeQuery_NoPriorCodeWritesWholeDoc(t *testing.T) {
	now := time.Now()
	f, u := issueCodeQuery("a@b.c", "confirm_code", false, time.Time{}, "1", now, core.CodeBudget{RegenCount: 1}, false, now)
	assert.Equal(t, bson.M{"email": "a@b.c"}, f)
	assert.IsType(t, bson.M{}, u["$set"].(bson.M)["confirm_code"])
}
