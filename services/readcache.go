package services

import (
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"coachify-account-api/models"
)

// readCache is a tiny TTL cache with request coalescing for hot read-only lookups.
// Only successful loads are stored. A zero TTL disables it: loads run directly.
type readCache struct {
	ttl time.Duration
	mu  sync.RWMutex
	m   map[string]readEntry
	sf  singleflight.Group
}

type readEntry struct {
	val     any
	expires time.Time
}

func newReadCache(ttl time.Duration) *readCache {
	return &readCache{ttl: ttl, m: make(map[string]readEntry)}
}

// purge drops every entry; mutations call it so same-process writers never read their own stale data.
func (c *readCache) purge() {
	if c == nil || c.ttl <= 0 {
		return
	}
	c.mu.Lock()
	c.m = make(map[string]readEntry)
	c.mu.Unlock()
}

// cachedRead returns the cached value for key or runs load once for all concurrent callers.
func cachedRead[T any](c *readCache, key string, load func() (T, *models.ApiError)) (T, *models.ApiError) {
	if c == nil || c.ttl <= 0 {
		return load()
	}
	now := time.Now()
	c.mu.RLock()
	e, ok := c.m[key]
	c.mu.RUnlock()
	if ok && now.Before(e.expires) {
		return e.val.(T), nil
	}
	type res struct {
		v   T
		err *models.ApiError
	}
	r, _, _ := c.sf.Do(key, func() (any, error) {
		v, apiErr := load()
		if apiErr == nil {
			c.mu.Lock()
			if len(c.m) > 4096 {
				c.m = make(map[string]readEntry)
			}
			c.m[key] = readEntry{val: v, expires: time.Now().Add(c.ttl)}
			c.mu.Unlock()
		}
		return res{v, apiErr}, nil
	})
	out := r.(res)
	return out.v, out.err
}
