package services

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
)

// asyncObserverTimeout bounds one asynchronous observer run (detached from the request).
const asyncObserverTimeout = 15 * time.Second

// InviteRedeemed describes a completed redemption. It deliberately carries no token: observers can neither store nor
// log the credential.
type InviteRedeemed struct {
	LinkID string
	Kind   string
	GymID  string
	// CoachID is the coach the client is affiliated with ("" for the gym pool and for member kinds).
	CoachID string
	UserID  string
}

// RedeemObserver reacts to a redemption.
type RedeemObserver interface {
	Name() string
	OnRedeemed(ctx context.Context, e InviteRedeemed) error
}

// InviteObservers runs the observers of a redemption in two tiers. Synchronous observers run in order and the first
// error aborts the redemption. Asynchronous observers are best effort: a failure is logged (never with the token).
type InviteObservers struct {
	sync  []RedeemObserver
	async []RedeemObserver
	wg    sync.WaitGroup
}

func NewInviteObservers(syncObs, asyncObs []RedeemObserver) *InviteObservers {
	return &InviteObservers{sync: syncObs, async: asyncObs}
}

// Notify runs the synchronous tier, then starts the asynchronous one.
func (o *InviteObservers) Notify(ctx context.Context, e InviteRedeemed) error {
	if o == nil {
		return nil
	}
	for _, ob := range o.sync {
		if err := ob.OnRedeemed(ctx, e); err != nil {
			return fmt.Errorf("observer %s: %w", ob.Name(), err)
		}
	}
	bg := context.WithoutCancel(ctx)
	for _, ob := range o.async {
		o.wg.Add(1)
		go func(ob RedeemObserver) {
			defer o.wg.Done()
			defer func() {
				if r := recover(); r != nil {
					zap.L().Error("invite observer panicked", zap.String("component", "invite"), zap.String("observer", ob.Name()))
				}
			}()
			actx, cancel := context.WithTimeout(bg, asyncObserverTimeout)
			defer cancel()
			if err := ob.OnRedeemed(actx, e); err != nil {
				zap.L().Warn("invite observer failed",
					zap.String("component", "invite"),
					zap.String("observer", ob.Name()),
					zap.String("kind", e.Kind),
					zap.String("link_id", e.LinkID),
					zap.Error(err),
				)
			}
		}(ob)
	}
	return nil
}

// Wait blocks until the asynchronous observers started so far have finished (tests and shutdown).
func (o *InviteObservers) Wait() {
	if o != nil {
		o.wg.Wait()
	}
}

// conversationEnsurer opens (get-or-create) the coach-client conversation; *chat.ChatClient satisfies it.
type conversationEnsurer interface {
	EnsureConversation(ctx context.Context, adminID, peerID string) *models.ApiError
	EnsureGymConversation(ctx context.Context, adminID, peerID, gymID string) *models.ApiError
}

// ensureConversationObserver replaces the stored coach token chat calls: it authenticates with the internal API key.
type ensureConversationObserver struct {
	chat conversationEnsurer
}

// NewEnsureConversationObserver returns the asynchronous chat observer; a nil chat client makes it a no-op.
func NewEnsureConversationObserver(c conversationEnsurer) RedeemObserver {
	return &ensureConversationObserver{chat: c}
}

func (o *ensureConversationObserver) Name() string { return "ensure_conversation" }

func (o *ensureConversationObserver) OnRedeemed(ctx context.Context, e InviteRedeemed) error {
	if o.chat == nil || e.CoachID == "" || e.UserID == "" {
		return nil
	}
	if e.Kind != db.InviteKindClientPersonal && e.Kind != db.InviteKindClientGym {
		return nil
	}
	if e.Kind == db.InviteKindClientGym && e.GymID != "" {
		if apiErr := o.chat.EnsureGymConversation(ctx, e.CoachID, e.UserID, e.GymID); apiErr != nil {
			return apiErr.Error
		}
		return nil
	}
	if apiErr := o.chat.EnsureConversation(ctx, e.CoachID, e.UserID); apiErr != nil {
		return apiErr.Error
	}
	return nil
}
