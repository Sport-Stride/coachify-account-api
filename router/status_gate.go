package router

import (
	"container/list"
	"context"
	"errors"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
)

const (
	// statusMaxStale is how long past its fetch time a cached status may still be served when the
	// lookup fails. Beyond it (or with no entry) a lookup failure becomes a 503.
	statusMaxStale = 5 * time.Minute
	// statusNotFoundTTL is how long a missing user is remembered.
	statusNotFoundTTL = 30 * time.Second
	statusMaxEntries  = 10000
	statusLookupLimit = 5 * time.Second
)

// StatusGate rejects inactive accounts, caching each user's status for a short TTL so the
// database is not hit on every request. A lookup failure serves the last known status (up to
// statusMaxStale old) so a database blip does not take the whole API down.
type StatusGate struct {
	lookup     UserStatusLookup
	ttl        time.Duration
	now        func() time.Time
	maxEntries int
	sf         singleflight.Group

	mu    sync.Mutex
	cache map[string]*list.Element // value: *statusEntry; list is ordered oldest to newest
	order *list.List
}

type statusEntry struct {
	userID   string
	status   db.UserStatus
	fetched  time.Time
	notFound bool
}

// NewStatusGate returns nil when lookup is nil, which disables the check.
func NewStatusGate(lookup UserStatusLookup, ttl time.Duration) *StatusGate {
	return NewStatusGateWithClock(lookup, ttl, time.Now)
}

// NewStatusGateWithClock is NewStatusGate with an injectable clock for tests.
func NewStatusGateWithClock(lookup UserStatusLookup, ttl time.Duration, now func() time.Time) *StatusGate {
	if lookup == nil {
		return nil
	}
	return &StatusGate{
		lookup: lookup, ttl: ttl, now: now, maxEntries: statusMaxEntries,
		cache: make(map[string]*list.Element), order: list.New(),
	}
}

func (g *StatusGate) get(userID string) (statusEntry, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if el, ok := g.cache[userID]; ok {
		return *el.Value.(*statusEntry), true
	}
	return statusEntry{}, false
}

func (g *StatusGate) put(e statusEntry) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if el, ok := g.cache[e.userID]; ok {
		*el.Value.(*statusEntry) = e
		g.order.MoveToBack(el)
		return
	}
	g.cache[e.userID] = g.order.PushBack(&e)
	for g.order.Len() > g.maxEntries {
		oldest := g.order.Front()
		delete(g.cache, oldest.Value.(*statusEntry).userID)
		g.order.Remove(oldest)
	}
}

func (g *StatusGate) status(_ context.Context, userID string) (db.UserStatus, error) {
	now := g.now()
	prev, had := g.get(userID)
	if had {
		age := now.Sub(prev.fetched)
		if prev.notFound && age < statusNotFoundTTL {
			return "", models.ErrUserNotFound
		}
		if !prev.notFound && age < g.ttl {
			return prev.status, nil
		}
	}

	// One lookup per user no matter how many requests miss at once. The lookup is detached from
	// any single request so one cancelled caller does not fail the others.
	type result struct {
		st  db.UserStatus
		err error
	}
	v, _, _ := g.sf.Do(userID, func() (interface{}, error) {
		ctx, cancel := context.WithTimeout(context.Background(), statusLookupLimit)
		defer cancel()
		st, err := g.lookup.GetStatusByExternalID(ctx, userID)
		return result{st, err}, nil
	})
	res := v.(result)
	if res.err == nil {
		g.put(statusEntry{userID: userID, status: res.st, fetched: g.now()})
		return res.st, nil
	}
	if errors.Is(res.err, models.ErrUserNotFound) {
		g.put(statusEntry{userID: userID, notFound: true, fetched: g.now()})
		return "", res.err
	}
	if had && !prev.notFound && g.now().Sub(prev.fetched) < statusMaxStale {
		zap.L().Warn("auth status lookup failed, serving cached status",
			zap.String("component", "auth"), zap.Error(res.err))
		return prev.status, nil
	}
	return "", res.err
}
