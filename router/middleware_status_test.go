package router_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/router"
)

type fakeStatusLookup struct {
	mu     sync.Mutex
	status map[string]db.UserStatus
	err    error
	calls  int
}

func (f *fakeStatusLookup) GetStatusByExternalID(_ context.Context, id string) (db.UserStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	st, ok := f.status[id]
	if !ok {
		return "", models.ErrUserNotFound
	}
	return st, nil
}

func statusRouter(lookup router.UserStatusLookup, ttl time.Duration) *gin.Engine {
	r := gin.New()
	r.Use(router.AuthMiddlewareWithStatus(router.NewStatusGate(lookup, ttl)))
	r.GET("/p", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	return r
}

func TestAuthMiddlewareStatusRecheck(t *testing.T) {
	hdr := "Bearer " + makeToken(t, testSecret, validClaims("u1", "coach"))

	for _, st := range []db.UserStatus{db.Blocked, db.Suspended, db.Deactivated} {
		t.Run("rejects "+string(st), func(t *testing.T) {
			l := &fakeStatusLookup{status: map[string]db.UserStatus{"u1": st}}
			w := serve(statusRouter(l, time.Minute), "GET", "/p", hdr, "")
			assert.Equal(t, http.StatusForbidden, w.Code)
			assert.Contains(t, w.Body.String(), `"error"`)
		})
	}

	t.Run("active user passes and status is cached", func(t *testing.T) {
		l := &fakeStatusLookup{status: map[string]db.UserStatus{"u1": db.Active}}
		r := statusRouter(l, time.Minute)
		assert.Equal(t, http.StatusNoContent, serve(r, "GET", "/p", hdr, "").Code)
		assert.Equal(t, http.StatusNoContent, serve(r, "GET", "/p", hdr, "").Code)
		assert.Equal(t, 1, l.calls)
	})

	t.Run("block takes effect once the cache entry expires", func(t *testing.T) {
		l := &fakeStatusLookup{status: map[string]db.UserStatus{"u1": db.Active}}
		r := statusRouter(l, 10*time.Millisecond)
		require.Equal(t, http.StatusNoContent, serve(r, "GET", "/p", hdr, "").Code)
		l.mu.Lock()
		l.status["u1"] = db.Blocked
		l.mu.Unlock()
		time.Sleep(30 * time.Millisecond)
		assert.Equal(t, http.StatusForbidden, serve(r, "GET", "/p", hdr, "").Code)
	})

	t.Run("deleted user is unauthorized", func(t *testing.T) {
		l := &fakeStatusLookup{status: map[string]db.UserStatus{}}
		assert.Equal(t, http.StatusUnauthorized, serve(statusRouter(l, time.Minute), "GET", "/p", hdr, "").Code)
	})

	t.Run("lookup failure fails closed", func(t *testing.T) {
		l := &fakeStatusLookup{err: errors.New("db down")}
		w := serve(statusRouter(l, time.Minute), "GET", "/p", hdr, "")
		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
		assert.NotContains(t, w.Body.String(), "db down")
	})
}
