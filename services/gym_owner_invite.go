package services

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"go.uber.org/zap"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/utils"
)

const (
	auditActionOwnerInviteCreated = "gym_owner_invite.created"
	auditActionOwnerInviteRevoked = "gym_owner_invite.revoked"
	auditActionOwnerInviteResent  = "gym_owner_invite.resent"
	auditTargetOwnerInvite        = "gym_owner_invite"
)

// GymOwnerInviteStore is the invite persistence the service needs; *repositories.GymOwnerInviteRepository satisfies it.
type GymOwnerInviteStore interface {
	Insert(ctx context.Context, inv *db.GymOwnerInvite) error
	GetByTokenHash(ctx context.Context, hash string) (*db.GymOwnerInvite, error)
	GetByID(ctx context.Context, id string) (*db.GymOwnerInvite, error)
	List(ctx context.Context, status string, page, size int) ([]db.GymOwnerInvite, int, error)
	RevokePendingByEmail(ctx context.Context, email string, at time.Time) error
	Transition(ctx context.Context, id, from, to, userID string, at time.Time) (bool, error)
	Rotate(ctx context.Context, id, newHash string, newExpiry, at time.Time) (bool, error)
}

// OwnerEmailChecker reports whether an account already uses an email; *repositories.UserRepository satisfies it.
type OwnerEmailChecker interface {
	EmailExists(ctx context.Context, email string) (bool, error)
}

// OwnerInviteMailer sends the invite email; *notification.NotificationClient satisfies it.
type OwnerInviteMailer interface {
	SendGymOwnerInviteEmail(ctx context.Context, email, name, link string) error
}

// GymOwnerInviteView is the admin-facing shape of an invite. The token hash never leaves the service.
type GymOwnerInviteView struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Firstname string    `json:"firstname"`
	Lastname  string    `json:"lastname"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	SentAt    time.Time `json:"sent_at"`
	EmailSent *bool     `json:"email_sent,omitempty"`
}

// GymOwnerInviteList is a page of invites.
type GymOwnerInviteList struct {
	Invites []GymOwnerInviteView `json:"invites"`
	Total   int                  `json:"total"`
	Page    int                  `json:"page"`
	Size    int                  `json:"size"`
}

// GymOwnerInviteLookup is what the public signup page may learn about an invite.
type GymOwnerInviteLookup struct {
	Email     string    `json:"email"`
	Firstname string    `json:"firstname"`
	Lastname  string    `json:"lastname"`
	ExpiresAt time.Time `json:"expires_at"`
}

type GymOwnerInviteService interface {
	Create(ctx context.Context, adminID, firstname, lastname, email string) (*GymOwnerInviteView, *models.ApiError)
	List(ctx context.Context, status string, page, size int) (*GymOwnerInviteList, *models.ApiError)
	Revoke(ctx context.Context, adminID, id string) *models.ApiError
	Resend(ctx context.Context, adminID, id string) (*GymOwnerInviteView, *models.ApiError)
	Lookup(ctx context.Context, rawToken string) (*GymOwnerInviteLookup, *models.ApiError)
	Redeem(ctx context.Context, rawToken, email string) (*db.GymOwnerInvite, *models.ApiError)
	MarkAccepted(ctx context.Context, id, userID string) *models.ApiError
}

// gymOwnerInviteRedeemer is the part of the invite service the signup paths use.
type gymOwnerInviteRedeemer interface {
	Redeem(ctx context.Context, rawToken, email string) (*db.GymOwnerInvite, *models.ApiError)
	MarkAccepted(ctx context.Context, id, userID string) *models.ApiError
}

type gymOwnerInviteServiceImpl struct {
	store     GymOwnerInviteStore
	users     OwnerEmailChecker
	audit     AdminAuditStore
	mailer    OwnerInviteMailer
	ttl       time.Duration
	clientURL string
	now       func() time.Time
}

func NewGymOwnerInviteService(store GymOwnerInviteStore, users OwnerEmailChecker, audit AdminAuditStore, mailer OwnerInviteMailer, ttl time.Duration, clientURL string) GymOwnerInviteService {
	return &gymOwnerInviteServiceImpl{
		store: store, users: users, audit: audit, mailer: mailer,
		ttl: ttl, clientURL: strings.TrimRight(clientURL, "/"), now: time.Now,
	}
}

func inviteNotFound() *models.ApiError {
	return models.NewApiError(http.StatusNotFound, models.ErrInviteNotFound)
}

func inviteNotUsable() *models.ApiError {
	return models.NewApiError(http.StatusConflict, models.ErrInviteNotUsable)
}

func (s *gymOwnerInviteServiceImpl) logErr(msg string, err error) *models.ApiError {
	zap.L().Error(msg, zap.String("component", "gym_owner_invite"), zap.Error(err))
	return internalError()
}

func (s *gymOwnerInviteServiceImpl) recordAudit(ctx context.Context, actorID, action, targetID string, after map[string]interface{}) {
	if s.audit == nil {
		return
	}
	e := &db.AdminAuditEntry{ActorID: actorID, Action: action, TargetID: targetID, TargetType: auditTargetOwnerInvite, After: after}
	if err := s.audit.Insert(ctx, e); err != nil {
		zap.L().Error("gym owner invite audit insert failed",
			zap.String("component", "gym_owner_invite"),
			zap.String("action", action),
			zap.String("actor_id", actorID),
			zap.String("target_id", targetID),
			zap.Error(err))
	}
}

// sendInvite emails the signup link; the link is never logged. A failure is logged and reported as false.
func (s *gymOwnerInviteServiceImpl) sendInvite(ctx context.Context, inv *db.GymOwnerInvite, raw string) bool {
	link := s.clientURL + "/auth/signup?invite=" + raw
	if err := s.mailer.SendGymOwnerInviteEmail(ctx, inv.Email, inv.Firstname+" "+inv.Lastname, link); err != nil {
		zap.L().Error("gym owner invite email failed",
			zap.String("component", "gym_owner_invite"),
			zap.String("invite_id", inv.ID.Hex()),
			zap.Error(err))
		return false
	}
	return true
}

func inviteView(inv *db.GymOwnerInvite) GymOwnerInviteView {
	return GymOwnerInviteView{
		ID: inv.ID.Hex(), Email: inv.Email, Firstname: inv.Firstname, Lastname: inv.Lastname,
		Status: inv.Status, CreatedAt: inv.CreatedAt, ExpiresAt: inv.ExpiresAt, SentAt: inv.SentAt,
	}
}

func (s *gymOwnerInviteServiceImpl) Create(ctx context.Context, adminID, firstname, lastname, email string) (*GymOwnerInviteView, *models.ApiError) {
	firstname, lastname, email = strings.TrimSpace(firstname), strings.TrimSpace(lastname), strings.TrimSpace(email)
	if firstname == "" || lastname == "" {
		return nil, badRequest("firstname and lastname are required")
	}
	if addr, err := mail.ParseAddress(email); err != nil || addr.Address != email {
		return nil, badRequest("invalid email")
	}
	email = strings.ToLower(email)

	exists, err := s.users.EmailExists(ctx, email)
	if err != nil {
		return nil, s.logErr("gym owner invite: email check failed", err)
	}
	if exists {
		return nil, models.NewApiError(http.StatusConflict, models.ErrEmailAlreadyExists)
	}

	raw, hash, err := utils.NewURLToken()
	if err != nil {
		return nil, s.logErr("gym owner invite: token generation failed", err)
	}
	now := s.now()
	// A new invite replaces any pending one for the same email.
	if err := s.store.RevokePendingByEmail(ctx, email, now); err != nil {
		return nil, s.logErr("gym owner invite: revoke previous failed", err)
	}
	inv := &db.GymOwnerInvite{
		Email: email, Firstname: firstname, Lastname: lastname, TokenHash: hash,
		Status: db.GymOwnerInvitePending, CreatedBy: adminID,
		CreatedAt: now, ExpiresAt: now.Add(s.ttl), SentAt: now,
	}
	if err := s.store.Insert(ctx, inv); err != nil {
		if errors.Is(err, models.ErrOwnerInvitePending) {
			return nil, models.NewApiError(http.StatusConflict, models.ErrOwnerInvitePending)
		}
		return nil, s.logErr("gym owner invite: insert failed", err)
	}

	sent := s.sendInvite(ctx, inv, raw)
	s.recordAudit(ctx, adminID, auditActionOwnerInviteCreated, inv.ID.Hex(), map[string]interface{}{"status": inv.Status})
	v := inviteView(inv)
	v.EmailSent = &sent
	return &v, nil
}

func (s *gymOwnerInviteServiceImpl) List(ctx context.Context, status string, page, size int) (*GymOwnerInviteList, *models.ApiError) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	rows, total, err := s.store.List(ctx, status, page, size)
	if err != nil {
		return nil, s.logErr("gym owner invite: list failed", err)
	}
	out := make([]GymOwnerInviteView, 0, len(rows))
	for i := range rows {
		out = append(out, inviteView(&rows[i]))
	}
	return &GymOwnerInviteList{Invites: out, Total: total, Page: page, Size: size}, nil
}

func (s *gymOwnerInviteServiceImpl) Revoke(ctx context.Context, adminID, id string) *models.ApiError {
	inv, err := s.store.GetByID(ctx, id)
	if err != nil {
		return s.logErr("gym owner invite: load failed", err)
	}
	if inv == nil {
		return inviteNotFound()
	}
	ok, err := s.store.Transition(ctx, id, db.GymOwnerInvitePending, db.GymOwnerInviteRevoked, "", s.now())
	if err != nil {
		return s.logErr("gym owner invite: revoke failed", err)
	}
	if !ok {
		return inviteNotUsable()
	}
	s.recordAudit(ctx, adminID, auditActionOwnerInviteRevoked, id, nil)
	return nil
}

// Resend rotates the token so the previously emailed link stops working, resets the expiry and emails the new link.
func (s *gymOwnerInviteServiceImpl) Resend(ctx context.Context, adminID, id string) (*GymOwnerInviteView, *models.ApiError) {
	inv, err := s.store.GetByID(ctx, id)
	if err != nil {
		return nil, s.logErr("gym owner invite: load failed", err)
	}
	if inv == nil {
		return nil, inviteNotFound()
	}
	if inv.Status != db.GymOwnerInvitePending {
		return nil, inviteNotUsable()
	}
	raw, hash, err := utils.NewURLToken()
	if err != nil {
		return nil, s.logErr("gym owner invite: token generation failed", err)
	}
	now := s.now()
	expiry := now.Add(s.ttl)
	ok, err := s.store.Rotate(ctx, id, hash, expiry, now)
	if err != nil {
		return nil, s.logErr("gym owner invite: rotate failed", err)
	}
	if !ok {
		return nil, inviteNotUsable()
	}
	inv.TokenHash, inv.ExpiresAt, inv.SentAt = hash, expiry, now
	sent := s.sendInvite(ctx, inv, raw)
	s.recordAudit(ctx, adminID, auditActionOwnerInviteResent, id, nil)
	v := inviteView(inv)
	v.EmailSent = &sent
	return &v, nil
}

// usable loads the invite behind a raw token. Invalid, expired, revoked and used all give the same 404.
func (s *gymOwnerInviteServiceImpl) usable(ctx context.Context, rawToken string) (*db.GymOwnerInvite, *models.ApiError) {
	if rawToken == "" {
		return nil, inviteNotFound()
	}
	inv, err := s.store.GetByTokenHash(ctx, utils.HashToken(rawToken))
	if err != nil {
		return nil, s.logErr("gym owner invite: lookup failed", err)
	}
	if inv == nil || inv.Status != db.GymOwnerInvitePending {
		return nil, inviteNotFound()
	}
	if !s.now().Before(inv.ExpiresAt) {
		if _, err := s.store.Transition(ctx, inv.ID.Hex(), db.GymOwnerInvitePending, db.GymOwnerInviteExpired, "", s.now()); err != nil {
			zap.L().Warn("gym owner invite: expire failed", zap.String("component", "gym_owner_invite"), zap.Error(err))
		}
		return nil, inviteNotFound()
	}
	return inv, nil
}

func (s *gymOwnerInviteServiceImpl) Lookup(ctx context.Context, rawToken string) (*GymOwnerInviteLookup, *models.ApiError) {
	inv, apiErr := s.usable(ctx, rawToken)
	if apiErr != nil {
		return nil, apiErr
	}
	return &GymOwnerInviteLookup{Email: inv.Email, Firstname: inv.Firstname, Lastname: inv.Lastname, ExpiresAt: inv.ExpiresAt}, nil
}

// Redeem validates the token and that the signing-up email is the invited one. It does not consume the invite.
func (s *gymOwnerInviteServiceImpl) Redeem(ctx context.Context, rawToken, email string) (*db.GymOwnerInvite, *models.ApiError) {
	inv, apiErr := s.usable(ctx, rawToken)
	if apiErr != nil {
		return nil, apiErr
	}
	if !strings.EqualFold(strings.TrimSpace(email), inv.Email) {
		return nil, models.NewApiError(http.StatusForbidden, models.ErrInviteEmailMismatch)
	}
	return inv, nil
}

func (s *gymOwnerInviteServiceImpl) MarkAccepted(ctx context.Context, id, userID string) *models.ApiError {
	ok, err := s.store.Transition(ctx, id, db.GymOwnerInvitePending, db.GymOwnerInviteAccepted, userID, s.now())
	if err != nil {
		return s.logErr("gym owner invite: mark accepted failed", err)
	}
	if !ok {
		return inviteNotUsable()
	}
	return nil
}
