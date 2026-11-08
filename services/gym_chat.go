package services

import (
	"context"
	"time"

	"go.uber.org/zap"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
)

// gymChatTimeout bounds one background provisioning run.
const gymChatTimeout = 60 * time.Second

// ChatOpener get-or-creates a conversation with adminID as the admin side; *chat.ChatClient satisfies it.
type ChatOpener interface {
	EnsureConversation(ctx context.Context, adminID, peerID string) *models.ApiError
}

// GymChatStore reads the gym relations the provisioner needs.
type GymChatStore interface {
	OwnerOfHead(ctx context.Context, headID string) (string, error)
	IDsByHead(ctx context.Context, headID string) ([]string, error)
	ListGymChatPeers(ctx context.Context, gymID string) ([]db.GymChatPeers, error)
}

type gymChatOwnerSource interface {
	OwnerOfHead(ctx context.Context, headID string) (string, error)
	ListGymChatPeers(ctx context.Context, gymID string) ([]db.GymChatPeers, error)
}

type gymChatTeamSource interface {
	IDsByHead(ctx context.Context, headID string) ([]string, error)
}

// gymChatStore joins the gym repository (owner lookups) and the team repository (team members).
type gymChatStore struct {
	gymChatOwnerSource
	gymChatTeamSource
}

// NewGymChatStore builds a GymChatStore from the gym and team repositories.
func NewGymChatStore(gyms gymChatOwnerSource, team gymChatTeamSource) GymChatStore {
	return gymChatStore{gyms, team}
}

// GymChatProvisioner opens chat conversations between a gym owner (admin side) and the gym's
// head coaches and their team coaches. Failures are logged and never reach the caller.
type GymChatProvisioner struct {
	store GymChatStore
	chat  ChatOpener
	spawn func(func())
}

// NewGymChatProvisioner builds a provisioner; a nil spawn runs hooks in a goroutine.
func NewGymChatProvisioner(store GymChatStore, chat ChatOpener, spawn func(func())) *GymChatProvisioner {
	if spawn == nil {
		spawn = func(f func()) { go f() }
	}
	return &GymChatProvisioner{store: store, chat: chat, spawn: spawn}
}

// background runs f off the request path with a context that outlives the request.
func (p *GymChatProvisioner) background(ctx context.Context, f func(ctx context.Context)) {
	detached := context.WithoutCancel(ctx)
	p.spawn(func() {
		ctx, cancel := context.WithTimeout(detached, gymChatTimeout)
		defer cancel()
		f(ctx)
	})
}

// ensure opens owner+peer, skipping empty ids and self pairs. It reports whether the call succeeded.
func (p *GymChatProvisioner) ensure(ctx context.Context, ownerID, peerID string) bool {
	if ownerID == "" || peerID == "" || ownerID == peerID {
		return false
	}
	if apiErr := p.chat.EnsureConversation(ctx, ownerID, peerID); apiErr != nil {
		zap.L().Warn("gym chat conversation failed",
			zap.String("component", "gym_chat"),
			zap.String("owner_id", ownerID),
			zap.String("peer_id", peerID),
			zap.Error(apiErr.Error),
		)
		return false
	}
	return true
}

// HeadJoined connects the owner with a new head coach and with every coach already on its team.
func (p *GymChatProvisioner) HeadJoined(ctx context.Context, ownerID, headID string) {
	if p == nil {
		return
	}
	p.background(ctx, func(ctx context.Context) {
		p.ensure(ctx, ownerID, headID)
		coaches, err := p.store.IDsByHead(ctx, headID)
		if err != nil {
			zap.L().Warn("gym chat team lookup failed",
				zap.String("component", "gym_chat"), zap.String("head_id", headID), zap.Error(err))
			return
		}
		for _, id := range coaches {
			p.ensure(ctx, ownerID, id)
		}
	})
}

// CoachAssigned connects the owner of the head coach's gym with a newly assigned team coach.
func (p *GymChatProvisioner) CoachAssigned(ctx context.Context, headID, coachID string) {
	if p == nil {
		return
	}
	p.background(ctx, func(ctx context.Context) {
		ownerID, err := p.store.OwnerOfHead(ctx, headID)
		if err != nil {
			zap.L().Warn("gym chat owner lookup failed",
				zap.String("component", "gym_chat"), zap.String("head_id", headID), zap.Error(err))
			return
		}
		if ownerID == "" {
			return
		}
		p.ensure(ctx, ownerID, coachID)
	})
}

// GymOwnerChanged connects the gym's current owner with every head coach and team coach.
func (p *GymChatProvisioner) GymOwnerChanged(ctx context.Context, gymID string) {
	if p == nil {
		return
	}
	p.background(ctx, func(ctx context.Context) {
		gyms, err := p.store.ListGymChatPeers(ctx, gymID)
		if err != nil {
			zap.L().Warn("gym chat peers lookup failed",
				zap.String("component", "gym_chat"), zap.String("gym_id", gymID), zap.Error(err))
			return
		}
		for _, g := range gyms {
			for _, id := range gymChatPeerIDs(g) {
				p.ensure(ctx, g.OwnerID, id)
			}
		}
	})
}

// Backfill connects every active gym owner with its heads and coaches (all gyms when gymID is empty).
// It runs synchronously; dryRun only counts the pairs. Calls are sequential because chat-side dedupe is find-then-insert.
func (p *GymChatProvisioner) Backfill(ctx context.Context, gymID string, dryRun bool) (pairs, created, failed int) {
	if p == nil {
		return 0, 0, 0
	}
	gyms, err := p.store.ListGymChatPeers(ctx, gymID)
	if err != nil {
		zap.L().Warn("gym chat backfill lookup failed",
			zap.String("component", "gym_chat"), zap.String("gym_id", gymID), zap.Error(err))
		return 0, 0, 0
	}
	for _, g := range gyms {
		for _, id := range gymChatPeerIDs(g) {
			if g.OwnerID == "" || id == g.OwnerID {
				continue
			}
			pairs++
			if dryRun {
				continue
			}
			if p.ensure(ctx, g.OwnerID, id) {
				created++
			} else {
				failed++
			}
		}
	}
	return pairs, created, failed
}

// gymChatPeerIDs returns the distinct non-empty head and coach ids of a gym.
func gymChatPeerIDs(g db.GymChatPeers) []string {
	seen := make(map[string]struct{}, len(g.HeadIDs)+len(g.CoachIDs))
	out := make([]string, 0, len(g.HeadIDs)+len(g.CoachIDs))
	for _, id := range append(append([]string{}, g.HeadIDs...), g.CoachIDs...) {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
