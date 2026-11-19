package services

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
)

// DefaultAuthzCacheTTL bounds how long another process can serve a decision after a change made here.
// The internal check response advertises a ttl_seconds no longer than this.
const DefaultAuthzCacheTTL = 10 * time.Second

// cachingAuthorizer is a Decorator over an Authorizer. Keys include a per-gym version that Invalidate bumps,
// so a write in this process is visible at once; other processes converge within the TTL.
// Only successful decisions are cached.
type cachingAuthorizer struct {
	inner Authorizer
	cache *readCache

	mu       sync.RWMutex
	versions map[string]int64
}

// NewCachingAuthorizer wraps inner; a zero ttl disables caching.
func NewCachingAuthorizer(inner Authorizer, ttl time.Duration) *cachingAuthorizer {
	return &cachingAuthorizer{inner: inner, cache: newReadCache(ttl), versions: map[string]int64{}}
}

func (c *cachingAuthorizer) version(gymID string) int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.versions[gymID]
}

// Invalidate makes every cached decision of the gym unreachable. Decisions whose request named no gym are
// resolved from membership, so the "" version moves too (Invalidate("") therefore drops every decision).
func (c *cachingAuthorizer) Invalidate(gymID string) {
	c.mu.Lock()
	c.versions[gymID]++
	c.versions[""]++
	c.mu.Unlock()
}

// authzKey length-prefixes every field so ids containing the separator cannot collide.
func authzKey(fields ...string) string {
	var b strings.Builder
	for _, f := range fields {
		b.WriteString(strconv.Itoa(len(f)))
		b.WriteByte(':')
		b.WriteString(f)
	}
	return b.String()
}

func (c *cachingAuthorizer) Can(ctx context.Context, a Actor, cap Capability, r Resource) (Decision, *models.ApiError) {
	key := authzKey(a.ID, string(cap), r.Type, r.ID, r.Context, r.GymID,
		strconv.FormatInt(c.version(r.GymID), 10), strconv.FormatInt(c.version(""), 10))
	return cachedRead(c.cache, key, func() (Decision, *models.ApiError) {
		return c.inner.Can(ctx, a, cap, r)
	})
}

// AuthzVersionBumper moves the persisted per-gym authz_version.
type AuthzVersionBumper interface {
	BumpAuthzVersion(ctx context.Context, gymID string) (int64, error)
}

// invalidatingMemberStore is a Decorator over GymMemberStore: every membership mutation, successful or not
// (a failed write may still have changed rows), drops the cached decisions and bumps the gym's authz_version.
// A mutation whose gym is not known (by user) invalidates every gym. Nil invalidator and bumper are skipped,
// so the wrapped behavior is otherwise unchanged.
type invalidatingMemberStore struct {
	GymMemberStore
	inval AuthzInvalidator
	bump  AuthzVersionBumper
}

// NewInvalidatingMemberStore wraps store; both inval and bump may be nil.
func NewInvalidatingMemberStore(store GymMemberStore, inval AuthzInvalidator, bump AuthzVersionBumper) GymMemberStore {
	return &invalidatingMemberStore{GymMemberStore: store, inval: inval, bump: bump}
}

func (s *invalidatingMemberStore) changed(ctx context.Context, gymID string) {
	if s.inval != nil {
		s.inval.Invalidate(gymID)
	}
	if s.bump != nil && gymID != "" {
		mirrorDo(ctx, func(ctx context.Context) {
			if _, err := s.bump.BumpAuthzVersion(ctx, gymID); err != nil {
				mirrorFailed("authzVersionBump", gymID, nil, err)
			}
		})
	}
}

func (s *invalidatingMemberStore) UpsertActive(ctx context.Context, m *db.GymMember) error {
	err := s.GymMemberStore.UpsertActive(ctx, m)
	gym := ""
	if m != nil {
		gym = m.GymID
	}
	s.changed(ctx, gym)
	return err
}

func (s *invalidatingMemberStore) MarkLeft(ctx context.Context, gymID, userID string, at time.Time) (bool, error) {
	ok, err := s.GymMemberStore.MarkLeft(ctx, gymID, userID, at)
	s.changed(ctx, gymID)
	return ok, err
}

func (s *invalidatingMemberStore) MarkLeftCoach(ctx context.Context, userID string, at time.Time) (bool, error) {
	ok, err := s.GymMemberStore.MarkLeftCoach(ctx, userID, at)
	s.changed(ctx, "")
	return ok, err
}

func (s *invalidatingMemberStore) MarkLeftBySupervisor(ctx context.Context, gymID, supervisorID string, at time.Time) (int64, error) {
	n, err := s.GymMemberStore.MarkLeftBySupervisor(ctx, gymID, supervisorID, at)
	s.changed(ctx, gymID)
	return n, err
}

func (s *invalidatingMemberStore) MarkLeftHead(ctx context.Context, userID string, at time.Time) (bool, error) {
	ok, err := s.GymMemberStore.MarkLeftHead(ctx, userID, at)
	s.changed(ctx, "")
	return ok, err
}

func (s *invalidatingMemberStore) MarkLeftSupervised(ctx context.Context, supervisorID string, at time.Time) (int64, error) {
	n, err := s.GymMemberStore.MarkLeftSupervised(ctx, supervisorID, at)
	s.changed(ctx, "")
	return n, err
}

func (s *invalidatingMemberStore) AddActive(ctx context.Context, m *db.GymMember) error {
	err := s.GymMemberStore.AddActive(ctx, m)
	gym := ""
	if m != nil {
		gym = m.GymID
	}
	s.changed(ctx, gym)
	return err
}

func (s *invalidatingMemberStore) SetSupervisor(ctx context.Context, gymID, userID, prevSupervisorID, supervisorID string) (bool, error) {
	ok, err := s.GymMemberStore.SetSupervisor(ctx, gymID, userID, prevSupervisorID, supervisorID)
	s.changed(ctx, gymID)
	return ok, err
}
