package router_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models/db"
	"coachify-account-api/router"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func clockedRouter(l router.UserStatusLookup, clk *fakeClock) *gin.Engine {
	r := gin.New()
	r.Use(router.AuthMiddlewareWithStatus(router.NewStatusGateWithClock(l, 30*time.Second, clk.now)))
	r.GET("/p", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	return r
}

func TestStatusGate_ServesStaleOnLookupErrorUpToCap(t *testing.T) {
	hdr := "Bearer " + makeToken(t, testSecret, validClaims("u1", "coach"))
	clk := &fakeClock{t: time.Unix(1000, 0)}
	l := &fakeStatusLookup{status: map[string]db.UserStatus{"u1": db.Active}}
	r := clockedRouter(l, clk)
	require.Equal(t, http.StatusNoContent, serve(r, "GET", "/p", hdr, "").Code)

	l.mu.Lock()
	l.err = errors.New("db down")
	l.mu.Unlock()
	clk.add(2 * time.Minute) // expired but within cap
	assert.Equal(t, http.StatusNoContent, serve(r, "GET", "/p", hdr, "").Code)
	clk.add(4 * time.Minute) // beyond 5 minute cap
	assert.Equal(t, http.StatusServiceUnavailable, serve(r, "GET", "/p", hdr, "").Code)
}

func TestStatusGate_NoCacheFailsClosed(t *testing.T) {
	hdr := "Bearer " + makeToken(t, testSecret, validClaims("u1", "coach"))
	clk := &fakeClock{t: time.Unix(1000, 0)}
	r := clockedRouter(&fakeStatusLookup{err: errors.New("db down")}, clk)
	assert.Equal(t, http.StatusServiceUnavailable, serve(r, "GET", "/p", hdr, "").Code)
}

func TestStatusGate_NegativeCache30s(t *testing.T) {
	hdr := "Bearer " + makeToken(t, testSecret, validClaims("ghost", "coach"))
	clk := &fakeClock{t: time.Unix(1000, 0)}
	l := &fakeStatusLookup{status: map[string]db.UserStatus{}}
	r := clockedRouter(l, clk)
	assert.Equal(t, http.StatusUnauthorized, serve(r, "GET", "/p", hdr, "").Code)
	assert.Equal(t, http.StatusUnauthorized, serve(r, "GET", "/p", hdr, "").Code)
	assert.Equal(t, 1, l.calls)
	clk.add(31 * time.Second)
	serve(r, "GET", "/p", hdr, "")
	assert.Equal(t, 2, l.calls)
}

type slowLookup struct {
	mu      sync.Mutex
	calls   int
	release chan struct{}
}

func (s *slowLookup) GetStatusByExternalID(context.Context, string) (db.UserStatus, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	<-s.release
	return db.Active, nil
}

func TestStatusGate_SingleflightConcurrentMisses(t *testing.T) {
	hdr := "Bearer " + makeToken(t, testSecret, validClaims("u1", "coach"))
	l := &slowLookup{release: make(chan struct{})}
	r := clockedRouter(l, &fakeClock{t: time.Unix(1000, 0)})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); serve(r, "GET", "/p", hdr, "") }()
	}
	time.Sleep(200 * time.Millisecond)
	close(l.release)
	wg.Wait()
	assert.Equal(t, 1, l.calls)
}

func TestStatusGate_EvictsOldestNotEverything(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	l := &fakeStatusLookup{status: map[string]db.UserStatus{}}
	for i := 0; i < 12; i++ {
		l.status[fmt.Sprintf("u%d", i)] = db.Active
	}
	g := router.NewStatusGateWithClock(l, time.Minute, clk.now)
	g.SetMaxEntriesForTest(10)
	for i := 0; i < 12; i++ {
		_, err := g.Status(context.Background(), fmt.Sprintf("u%d", i))
		require.NoError(t, err)
	}
	before := l.calls
	_, _ = g.Status(context.Background(), "u11") // newest still cached
	assert.Equal(t, before, l.calls)
	_, _ = g.Status(context.Background(), "u0") // oldest evicted
	assert.Equal(t, before+1, l.calls)
}
