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
	"coachify-account-api/repositories"
	"coachify-account-api/utils"
)

const (
	inviteMaxTTLHours = 24 * 90
	inviteMaxUses     = 10000
	inviteListLimit   = 200
)

var (
	errInviteDisabled   = errors.New("invite links are not enabled")
	errInviteStatus     = errors.New("status must be active, revoked, exhausted or declined")
	errInviteToken      = errors.New("token is required")
	errInviteUnverified = errors.New("confirm your email address before accepting this invitation")
)

// kindCreators maps a kind to the account roles that may create it (a gym kind is further authorized per gym).
var kindCreators = map[string]func(role string) bool{
	db.InviteKindClientPersonal:  db.IsCoachingRole,
	db.InviteKindClientGym:       isGymCreatorRole,
	db.InviteKindMemberCoach:     isGymCreatorRole,
	db.InviteKindMemberHeadCoach: isGymCreatorRole,
}

func isGymCreatorRole(role string) bool { return role == "coach" || role == db.RoleGymOwner }

var inviteStatuses = map[string]bool{
	"": true, db.InviteStatusActive: true, db.InviteStatusRevoked: true,
	db.InviteStatusExhausted: true, db.InviteStatusDeclined: true,
}

// InviteLinkStore is the part of the invite link repository the service needs.
type InviteLinkStore interface {
	Insert(ctx context.Context, l *db.InviteLink) error
	GetByHash(ctx context.Context, hash string) (*db.InviteLink, error)
	GetByID(ctx context.Context, id string) (*db.InviteLink, error)
	List(ctx context.Context, f repositories.InviteLinkFilter, limit int64) ([]db.InviteLink, error)
	ListPendingByEmail(ctx context.Context, email, kind string, now time.Time) ([]db.InviteLink, error)
	SetStatus(ctx context.Context, id, from, to string) (bool, error)
	ConsumeUse(ctx context.Context, id string, now time.Time) (bool, error)
	ReleaseUse(ctx context.Context, id string) error
}

var _ InviteLinkStore = (*repositories.InviteLinkRepository)(nil)

// inviteUsers is the part of the user repository the service needs.
type inviteUsers interface {
	GetUserByExternalIdUpdate(ctx context.Context, userID string) (*db.User, *models.ApiError)
	GetUserNameByExternalID(ctx context.Context, externalID string) (string, *models.ApiError)
}

// InviteLookup is what the public lookup may learn about a token.
type InviteLookup struct {
	Kind       string `json:"kind"`
	GymName    string `json:"gym_name,omitempty"`
	CoachName  string `json:"coach_name,omitempty"`
	EmailBound bool   `json:"email_bound"`
}

// InviteLinkView is a link as its creator sees it. It never carries the token or its hash.
type InviteLinkView struct {
	ID            string     `json:"id"`
	Kind          string     `json:"kind"`
	GymID         string     `json:"gym_id,omitempty"`
	TargetCoachID string     `json:"target_coach_id,omitempty"`
	Email         string     `json:"email,omitempty"`
	MaxUses       int        `json:"max_uses"`
	Uses          int        `json:"uses"`
	Status        string     `json:"status"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// MyInvite is an email-bound gym invite waiting for the signed-in client's decision.
type MyInvite struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind"`
	GymID     string     `json:"gym_id"`
	GymName   string     `json:"gym_name,omitempty"`
	CoachID   string     `json:"coach_id,omitempty"`
	CoachName string     `json:"coach_name,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// InviteLinkService is the unified invite link aggregate (INVITE_LINKS_V2). Every method answers 404 while the flag is off.
type InviteLinkService interface {
	Create(ctx context.Context, a Actor, req CreateInvite) (*InviteCreated, *models.ApiError)
	List(ctx context.Context, actorID, gymID, status string) ([]InviteLinkView, *models.ApiError)
	Revoke(ctx context.Context, actorID, id string) *models.ApiError
	Lookup(ctx context.Context, token string) (*InviteLookup, *models.ApiError)
	Redeem(ctx context.Context, userID, token string) (*RedeemResult, *models.ApiError)
	ListMine(ctx context.Context, userID string) ([]MyInvite, *models.ApiError)
	Accept(ctx context.Context, userID, id string) (*RedeemResult, *models.ApiError)
	Decline(ctx context.Context, userID, id string) *models.ApiError
	// PrepareSignup and CompleteSignup are the signup seam (inviteSignupRedeemer) used by services/auth.go.
	PrepareSignup(ctx context.Context, token, email string) (*db.InviteLink, *models.ApiError)
	CompleteSignup(ctx context.Context, l *db.InviteLink, u *db.User) *models.ApiError
	// AbortSignup releases the use reserved by PrepareSignup when the signup fails.
	AbortSignup(ctx context.Context, l *db.InviteLink)
}

// InviteLinkDeps groups the collaborators of the service (constructor injection).
type InviteLinkDeps struct {
	Store      InviteLinkStore
	RegLinks   registrationLinkLookup // legacy raw-token registration links
	Users      inviteUsers
	Gyms       inviteGymReader
	Members    GymMembershipReader
	Authz      Authorizer
	Joiner     memberJoiner // legacy GymInvitation lookup (member kinds)
	Registry   *InviteKindRegistry
	Observers  *InviteObservers
	ClientURL  string
	DefaultTTL time.Duration
}

type inviteLinkService struct {
	InviteLinkDeps
	enabled bool
}

// NewInviteLinkService wires the service; enabled=false (flag off) answers 404 everywhere.
func NewInviteLinkService(d InviteLinkDeps, enabled bool) InviteLinkService {
	if d.DefaultTTL <= 0 {
		d.DefaultTTL = 7 * 24 * time.Hour
	}
	return &inviteLinkService{InviteLinkDeps: d, enabled: enabled}
}

func (s *inviteLinkService) internal(op string, err error) *models.ApiError {
	zap.L().Error("invite link failure", zap.String("component", "invite"), zap.String("op", op), zap.Error(err))
	return teamErr(http.StatusInternalServerError, errInternal)
}

func (s *inviteLinkService) off() *models.ApiError {
	if s.enabled {
		return nil
	}
	return teamErr(http.StatusNotFound, errInviteDisabled)
}

// usable reports whether a link can still be redeemed.
func usableLink(l *db.InviteLink, now time.Time) bool {
	if l.Status != db.InviteStatusActive {
		return false
	}
	if l.ExpiresAt != nil && !l.ExpiresAt.After(now) {
		return false
	}
	return l.MaxUses <= 0 || l.Uses < l.MaxUses
}

func linkView(l *db.InviteLink) InviteLinkView {
	return InviteLinkView{
		ID: l.ID.Hex(), Kind: l.Kind, GymID: l.GymID, TargetCoachID: l.TargetCoachID, Email: l.Email,
		MaxUses: l.MaxUses, Uses: l.Uses, Status: l.Status, ExpiresAt: l.ExpiresAt, CreatedAt: l.CreatedAt,
	}
}

func (s *inviteLinkService) Create(ctx context.Context, a Actor, req CreateInvite) (*InviteCreated, *models.ApiError) {
	if apiErr := s.off(); apiErr != nil {
		return nil, apiErr
	}
	kind, ok := s.Registry.Get(req.Kind)
	if !ok {
		return nil, teamErr(http.StatusBadRequest, errInviteKindUnknown)
	}
	// The kind must fit the creator's role: refuse before any lookup.
	if allowed := kindCreators[req.Kind]; allowed != nil && !allowed(a.Role) {
		return nil, teamErr(http.StatusForbidden, errInviteRoleDenied)
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email != "" {
		if addr, err := mail.ParseAddress(email); err != nil || addr.Address != email {
			return nil, teamErr(http.StatusBadRequest, errInviteEmail)
		}
	}
	if iss, ok := kind.(linkIssuer); ok {
		return iss.Issue(ctx, a, req)
	}
	scope, apiErr := kind.AuthorizeCreate(ctx, a, req)
	if apiErr != nil {
		return nil, apiErr
	}
	raw, hash, err := utils.NewURLToken()
	if err != nil {
		return nil, s.internal("Create.token", err)
	}
	now := time.Now().UTC()
	ttl := s.DefaultTTL
	if req.ExpiresInHours > 0 {
		h := req.ExpiresInHours
		if h > inviteMaxTTLHours {
			h = inviteMaxTTLHours
		}
		ttl = time.Duration(h) * time.Hour
	}
	exp := now.Add(ttl)
	maxUses := req.MaxUses
	if maxUses < 0 {
		maxUses = 0
	}
	if maxUses > inviteMaxUses {
		maxUses = inviteMaxUses
	}
	if email != "" {
		maxUses = 1
	}
	l := &db.InviteLink{
		Kind: kind.Name(), TokenHash: hash, CreatorID: a.ID, CreatorRole: scope.CreatorRole, GymID: scope.GymID,
		TargetCoachID: scope.TargetCoachID, Email: email, MaxUses: maxUses, Status: db.InviteStatusActive,
		ExpiresAt: &exp, CreatedAt: now,
	}
	if err := s.Store.Insert(ctx, l); err != nil {
		return nil, s.internal("Create.insert", err)
	}
	return &InviteCreated{ID: l.ID.Hex(), Link: strings.TrimRight(s.ClientURL, "/") + "/invite/" + raw, ExpiresAt: l.ExpiresAt}, nil
}

// canManageGym reports whether the actor holds member.invite in the gym (404 for a non-member).
func (s *inviteLinkService) canManageGym(ctx context.Context, actorID, gymID string) (bool, *models.ApiError) {
	d, apiErr := s.Authz.Can(ctx, Actor{ID: actorID}, CapMemberInvite, Resource{Type: ResourceGym, ID: gymID, GymID: gymID})
	if apiErr != nil {
		return false, apiErr
	}
	if d.Reason == ReasonNotMember {
		return false, teamErr(http.StatusNotFound, errGymNotFound)
	}
	return d.Allowed, nil
}

// List covers invite_links rows only. member_coach and member_head_coach links are GymInvitation rows listed by the gym
// service (GET /gym-owner/gyms/:gym_id/invitations, which needs a gym id); there is no id-only equivalent to fall through to.
func (s *inviteLinkService) List(ctx context.Context, actorID, gymID, status string) ([]InviteLinkView, *models.ApiError) {
	if apiErr := s.off(); apiErr != nil {
		return nil, apiErr
	}
	if !inviteStatuses[status] {
		return nil, teamErr(http.StatusBadRequest, errInviteStatus)
	}
	f := repositories.InviteLinkFilter{Status: status, GymID: gymID}
	// Without member.invite (or without a gym) the caller sees only his own links.
	f.CreatorID = actorID
	if gymID != "" {
		all, apiErr := s.canManageGym(ctx, actorID, gymID)
		if apiErr != nil {
			return nil, apiErr
		}
		if all {
			f.CreatorID = ""
		}
	}
	rows, err := s.Store.List(ctx, f, inviteListLimit)
	if err != nil {
		return nil, s.internal("List", err)
	}
	out := make([]InviteLinkView, 0, len(rows))
	for i := range rows {
		out = append(out, linkView(&rows[i]))
	}
	return out, nil
}

// Revoke covers invite_links rows only. A member link id is a GymInvitation id: the gym service revokes it
// (DELETE /gym-owner/gyms/:gym_id/invitations/:id), which needs the gym id this route does not carry, so such an id is a 404 here.
func (s *inviteLinkService) Revoke(ctx context.Context, actorID, id string) *models.ApiError {
	if apiErr := s.off(); apiErr != nil {
		return apiErr
	}
	l, err := s.Store.GetByID(ctx, id)
	if err != nil {
		return s.internal("Revoke.get", err)
	}
	if l == nil {
		return inviteNotFound()
	}
	if l.CreatorID != actorID {
		if l.GymID == "" {
			return inviteNotFound()
		}
		ok, apiErr := s.canManageGym(ctx, actorID, l.GymID)
		if apiErr != nil || !ok {
			return inviteNotFound()
		}
	}
	changed, err := s.Store.SetStatus(ctx, id, db.InviteStatusActive, db.InviteStatusRevoked)
	if err != nil {
		return s.internal("Revoke.set", err)
	}
	if !changed {
		return inviteNotUsable()
	}
	return nil
}

// resolvedInvite is a link found by token plus data the legacy adapters know about it.
type resolvedInvite struct {
	link    *db.InviteLink
	gymName string
}

// resolve finds the link behind a raw token: the hashed invite_links row first, then the legacy raw-token
// registration link, then a legacy GymInvitation join link (through the gymJoinRedeemer seam). Unknown, expired,
// used and revoked tokens give the same 404.
func (s *inviteLinkService) resolve(ctx context.Context, token string) (*resolvedInvite, *models.ApiError) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, inviteNotFound()
	}
	now := time.Now()
	l, err := s.Store.GetByHash(ctx, utils.HashToken(token))
	if err != nil {
		return nil, s.internal("resolve.hash", err)
	}
	if l != nil {
		if !usableLink(l, now) {
			return nil, inviteNotFound()
		}
		return &resolvedInvite{link: l}, nil
	}
	if s.RegLinks != nil {
		rl, err := s.RegLinks.GetByToken(ctx, token)
		if err != nil {
			return nil, s.internal("resolve.legacy", err)
		}
		if rl != nil {
			return &resolvedInvite{link: &db.InviteLink{
				Kind: db.InviteKindClientPersonal, CreatorID: rl.CoachID, CreatorRole: "coach",
				Status: db.InviteStatusActive, CreatedAt: rl.CreatedAt, LegacyRef: "registration_links:" + rl.ID.Hex(),
			}}, nil
		}
	}
	if s.Joiner != nil {
		lk, apiErr := s.Joiner.LookupJoin(ctx, token)
		if apiErr != nil && apiErr.Code != http.StatusNotFound {
			return nil, apiErr
		}
		if apiErr == nil && lk != nil {
			kind := db.InviteKindMemberHeadCoach
			if lk.Role == db.GymInviteRoleCoach {
				kind = db.InviteKindMemberCoach
			}
			return &resolvedInvite{
				link:    &db.InviteLink{Kind: kind, Email: strings.ToLower(lk.Email), Status: db.InviteStatusActive},
				gymName: lk.GymName,
			}, nil
		}
	}
	return nil, inviteNotFound()
}

func (s *inviteLinkService) Lookup(ctx context.Context, token string) (*InviteLookup, *models.ApiError) {
	if !s.enabled {
		// The public lookup must not reveal the flag: same answer as an unknown token.
		return nil, inviteNotFound()
	}
	r, apiErr := s.resolve(ctx, token)
	if apiErr != nil {
		return nil, apiErr
	}
	l := r.link
	out := &InviteLookup{Kind: l.Kind, GymName: r.gymName, EmailBound: l.Email != ""}
	if l.GymID != "" && s.Gyms != nil {
		if g, err := s.Gyms.GetGym(ctx, l.GymID); err == nil && g != nil && g.Status == db.GymStatusActive {
			out.GymName = g.Name
		} else if err == nil {
			return nil, inviteNotFound() // archived or deleted gym
		}
	}
	coachID := l.TargetCoachID
	if l.Kind == db.InviteKindClientPersonal {
		coachID = l.CreatorID
	}
	if coachID != "" && s.Users != nil {
		if name, nErr := s.Users.GetUserNameByExternalID(ctx, coachID); nErr == nil {
			out.CoachName = name
		}
	}
	return out, nil
}

func (s *inviteLinkService) Redeem(ctx context.Context, userID, token string) (*RedeemResult, *models.ApiError) {
	if apiErr := s.off(); apiErr != nil {
		return nil, apiErr
	}
	if strings.TrimSpace(token) == "" {
		return nil, teamErr(http.StatusBadRequest, errInviteToken)
	}
	r, apiErr := s.resolve(ctx, token)
	if apiErr != nil {
		return nil, apiErr
	}
	u, apiErr := s.Users.GetUserByExternalIdUpdate(ctx, userID)
	if apiErr != nil {
		return nil, apiErr
	}
	return s.redeem(ctx, RedeemInput{Link: r.link, User: u, Token: strings.TrimSpace(token)})
}

// redeem is the shared redemption: email binding, pre-redeem guard, atomic use accounting, the kind's affiliation write,
// then the observers. A use is given back when nothing was redeemed (error, already a member, pending acceptance) so
// a failed or repeated attempt never burns a single-use link.
func (s *inviteLinkService) redeem(ctx context.Context, in RedeemInput) (*RedeemResult, *models.ApiError) {
	l, u := in.Link, in.User
	// Legacy rows (zero ID) have no use accounting; their own collections enforce single use where needed.
	tracked := !l.ID.IsZero()
	release := func() {
		if !tracked {
			return
		}
		rctx, cancel := detachedContext(ctx)
		defer cancel()
		if err := s.Store.ReleaseUse(rctx, l.ID.Hex()); err != nil {
			zap.L().Error("invite use release failed", zap.String("component", "invite"), zap.String("link_id", l.ID.Hex()), zap.Error(err))
		}
	}
	// A signup reserved its use in PrepareSignup already (in.Reserved): do not count it twice, and settle it on every
	// failure path.
	fail := func(e *models.ApiError) (*RedeemResult, *models.ApiError) {
		if in.Reserved {
			release()
		}
		return nil, e
	}
	kind, ok := s.Registry.Get(l.Kind)
	if !ok {
		return fail(inviteNotFound())
	}
	if l.Email != "" && !strings.EqualFold(strings.TrimSpace(u.UserEmail), l.Email) {
		return fail(teamErr(http.StatusForbidden, models.ErrInviteEmailMismatch))
	}
	// An email-bound link proves no mailbox ownership: a client account whose email is not confirmed never redeems it
	// (no gym_clients write, no consent), not even to see a pending acceptance.
	if l.Email != "" && isClientKind(l.Kind) && !accountVerified(u) {
		return fail(teamErr(http.StatusForbidden, errInviteUnverified))
	}
	if apiErr := kind.Guard(ctx, l); apiErr != nil {
		return fail(apiErr)
	}
	if tracked && !in.Reserved {
		ok, err := s.Store.ConsumeUse(ctx, l.ID.Hex(), time.Now())
		if err != nil {
			return nil, s.internal("redeem.consume", err)
		}
		if !ok {
			return nil, inviteNotFound()
		}
	}
	res, apiErr := kind.Redeem(ctx, in)
	if apiErr != nil {
		release()
		return nil, apiErr
	}
	if res.Status != RedeemJoined {
		release()
		return res, nil
	}
	if err := s.Observers.Notify(ctx, InviteRedeemed{
		LinkID: hexID(l), Kind: l.Kind, GymID: res.GymID, CoachID: res.CoachID, UserID: u.ExternalID,
	}); err != nil {
		// Why this compensation and not a rollback: sync observers run after the kind's affiliation write (the plan's
		// ordered affiliation observer is the kind's own Redeem, so ordering is kept). The affiliation is therefore
		// already written and is not undone; instead the use is released and the call fails, and a retry finds the
		// membership and answers already_member (idempotent), running the observers again.
		release()
		return nil, s.internal("redeem.observer", err)
	}
	return res, nil
}

func (s *inviteLinkService) ListMine(ctx context.Context, userID string) ([]MyInvite, *models.ApiError) {
	if apiErr := s.off(); apiErr != nil {
		return nil, apiErr
	}
	u, apiErr := s.Users.GetUserByExternalIdUpdate(ctx, userID)
	if apiErr != nil {
		return nil, apiErr
	}
	email := strings.ToLower(strings.TrimSpace(u.UserEmail))
	if email == "" || !accountVerified(u) {
		return []MyInvite{}, nil // invites show up once the account is confirmed
	}
	rows, err := s.Store.ListPendingByEmail(ctx, email, db.InviteKindClientGym, time.Now())
	if err != nil {
		return nil, s.internal("ListMine", err)
	}
	out := make([]MyInvite, 0, len(rows))
	for i := range rows {
		l := &rows[i]
		if !usableLink(l, time.Now()) {
			continue
		}
		inv := MyInvite{ID: l.ID.Hex(), Kind: l.Kind, GymID: l.GymID, CoachID: l.TargetCoachID, ExpiresAt: l.ExpiresAt, CreatedAt: l.CreatedAt}
		if g, gErr := s.Gyms.GetGym(ctx, l.GymID); gErr == nil && g != nil && g.Status == db.GymStatusActive {
			inv.GymName = g.Name
		} else {
			continue // archived gym: nothing to accept
		}
		if l.TargetCoachID != "" {
			if name, nErr := s.Users.GetUserNameByExternalID(ctx, l.TargetCoachID); nErr == nil {
				inv.CoachName = name
			}
		}
		out = append(out, inv)
	}
	return out, nil
}

// ownInvite loads an email-bound gym invite addressed to the user; anything else is the same 404.
func (s *inviteLinkService) ownInvite(ctx context.Context, userID, id string) (*db.InviteLink, *db.User, *models.ApiError) {
	u, apiErr := s.Users.GetUserByExternalIdUpdate(ctx, userID)
	if apiErr != nil {
		return nil, nil, apiErr
	}
	l, err := s.Store.GetByID(ctx, id)
	if err != nil {
		return nil, nil, s.internal("ownInvite.get", err)
	}
	if l == nil || l.Kind != db.InviteKindClientGym || l.Email == "" ||
		!strings.EqualFold(strings.TrimSpace(u.UserEmail), l.Email) || !usableLink(l, time.Now()) {
		return nil, nil, inviteNotFound()
	}
	return l, u, nil
}

func (s *inviteLinkService) Accept(ctx context.Context, userID, id string) (*RedeemResult, *models.ApiError) {
	if apiErr := s.off(); apiErr != nil {
		return nil, apiErr
	}
	l, u, apiErr := s.ownInvite(ctx, userID, id)
	if apiErr != nil {
		return nil, apiErr
	}
	return s.redeem(ctx, RedeemInput{Link: l, User: u, Accepted: true})
}

func (s *inviteLinkService) Decline(ctx context.Context, userID, id string) *models.ApiError {
	if apiErr := s.off(); apiErr != nil {
		return apiErr
	}
	l, _, apiErr := s.ownInvite(ctx, userID, id)
	if apiErr != nil {
		return apiErr
	}
	changed, err := s.Store.SetStatus(ctx, l.ID.Hex(), db.InviteStatusActive, db.InviteStatusDeclined)
	if err != nil {
		return s.internal("Decline", err)
	}
	if !changed {
		return inviteNotFound()
	}
	return nil
}

func (s *inviteLinkService) PrepareSignup(ctx context.Context, token, email string) (*db.InviteLink, *models.ApiError) {
	if apiErr := s.off(); apiErr != nil {
		return nil, apiErr
	}
	r, apiErr := s.resolve(ctx, token)
	if apiErr != nil {
		return nil, apiErr
	}
	l := r.link
	if !isClientKind(l.Kind) {
		return nil, inviteNotFound()
	}
	if l.Email != "" && !strings.EqualFold(strings.TrimSpace(email), l.Email) {
		return nil, teamErr(http.StatusForbidden, models.ErrInviteEmailMismatch)
	}
	kind, ok := s.Registry.Get(l.Kind)
	if !ok {
		return nil, inviteNotFound()
	}
	if apiErr := kind.Guard(ctx, l); apiErr != nil {
		return nil, apiErr
	}
	// Reserve the use so concurrent signups on a single-use link cannot create more accounts than uses. The caller
	// ends the signup with CompleteSignup or AbortSignup, which settles the reservation.
	if !l.ID.IsZero() {
		ok, err := s.Store.ConsumeUse(ctx, l.ID.Hex(), time.Now())
		if err != nil {
			return nil, s.internal("PrepareSignup.consume", err)
		}
		if !ok {
			return nil, inviteNotFound()
		}
	}
	return l, nil
}

// AbortSignup gives back the use reserved by PrepareSignup (the signup failed).
func (s *inviteLinkService) AbortSignup(ctx context.Context, l *db.InviteLink) {
	if l == nil || l.ID.IsZero() {
		return
	}
	rctx, cancel := detachedContext(ctx)
	defer cancel()
	if err := s.Store.ReleaseUse(rctx, l.ID.Hex()); err != nil {
		zap.L().Error("invite use release failed", zap.String("component", "invite"), zap.String("link_id", l.ID.Hex()), zap.Error(err))
	}
}

// CompleteSignup settles the reservation of PrepareSignup. An open link joins the new account at once (signing up
// with it is the consent). An email-bound link is not redeemed at signup: the mailbox is not verified yet, so the
// reservation is released and the invite stays pending for /me/invites once the account is confirmed.
func (s *inviteLinkService) CompleteSignup(ctx context.Context, l *db.InviteLink, u *db.User) *models.ApiError {
	if l.Email != "" {
		s.AbortSignup(ctx, l)
		return nil
	}
	_, apiErr := s.redeem(ctx, RedeemInput{Link: l, User: u, NewUser: true, Reserved: true})
	return apiErr
}

// accountVerified reports whether the account confirmed its email (status Active).
func accountVerified(u *db.User) bool { return u != nil && u.UserStatus == db.Active }
