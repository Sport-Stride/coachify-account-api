package services

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/pkg/invitation"
)

var (
	errInviteKindUnknown   = errors.New("unknown invite kind")
	errInviteGymRequired   = errors.New("gym_id is required for this invite kind")
	errInviteCoachRequired = errors.New("only coaches can create personal client invites")
	errInviteClientOnly    = errors.New("only clients can redeem this invite")
	errInviteAlreadyLinked = errors.New("you are already linked to a coach")
	errInviteCreateDenied  = errors.New("this invite kind cannot be created here")
	errClientGymDisabled   = errors.New("gym client invites are not enabled")
	errInviteRoleDenied    = errors.New("your role cannot create this invite kind")
	errInviteEmail         = errors.New("email is not a valid address")
	errInviteSupervisor    = errors.New("supervisor_id must be an active member of the gym")
)

// Redeem statuses.
const (
	RedeemJoined            = "joined"
	RedeemAlreadyMember     = "already_member"
	RedeemPendingAcceptance = "pending_acceptance"
)

// CreateInvite is the request to create an invite link.
type CreateInvite struct {
	Kind           string `json:"kind"`
	GymID          string `json:"gym_id,omitempty"`
	Email          string `json:"email,omitempty"`
	SupervisorID   string `json:"supervisor_id,omitempty"` // member_coach only
	MaxUses        int    `json:"max_uses,omitempty"`
	ExpiresInHours int    `json:"expires_in_hours,omitempty"`
}

// InviteScope is what a kind decides about a new link once the creator is authorized.
type InviteScope struct {
	GymID         string
	TargetCoachID string
	CreatorRole   string
}

// InviteCreated is the answer of a creation; the raw token (inside Link) is returned only here.
type InviteCreated struct {
	ID        string     `json:"id"`
	Link      string     `json:"link"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// RedeemInput is one redemption. Token is the raw token, passed through only for kinds that delegate to a legacy
// token based flow; it must never be stored or logged. NewUser is a signup (the act of signing up with the link is the
// consent); Accepted is an explicit accept of an email-bound invite.
type RedeemInput struct {
	Link     *db.InviteLink
	User     *db.User
	Token    string
	NewUser  bool
	Accepted bool
	// Reserved: the use was already counted by PrepareSignup.
	Reserved bool
}

// RedeemResult is the answer of a redemption.
type RedeemResult struct {
	Kind     string `json:"kind"`
	Status   string `json:"status"`
	GymID    string `json:"gym_id,omitempty"`
	CoachID  string `json:"coach_id,omitempty"`
	InviteID string `json:"invite_id,omitempty"`
}

// InvitationKind is one kind of invite (Strategy). Guard is the pre-redeem check that may block (entitlement, quota,
// seats); Redeem writes the affiliation.
type InvitationKind interface {
	Name() string
	AuthorizeCreate(ctx context.Context, a Actor, req CreateInvite) (*InviteScope, *models.ApiError)
	Guard(ctx context.Context, l *db.InviteLink) *models.ApiError
	Redeem(ctx context.Context, in RedeemInput) (*RedeemResult, *models.ApiError)
}

// linkIssuer is implemented by kinds whose links live in another collection (member kinds delegate to the gym
// service): the invite_links row is not written.
type linkIssuer interface {
	Issue(ctx context.Context, a Actor, req CreateInvite) (*InviteCreated, *models.ApiError)
}

// InviteKindRegistry is the Factory of kinds.
type InviteKindRegistry struct {
	kinds map[string]InvitationKind
}

func NewInviteKindRegistry(kinds ...InvitationKind) *InviteKindRegistry {
	r := &InviteKindRegistry{kinds: map[string]InvitationKind{}}
	for _, k := range kinds {
		r.Register(k)
	}
	return r
}

func (r *InviteKindRegistry) Register(k InvitationKind) { r.kinds[k.Name()] = k }

func (r *InviteKindRegistry) Get(name string) (InvitationKind, bool) {
	k, ok := r.kinds[name]
	return k, ok
}

func isClientKind(kind string) bool {
	return kind == db.InviteKindClientPersonal || kind == db.InviteKindClientGym
}

// SeatGuard checks seat availability before a member redemption. P8a replaces the no-op.
type SeatGuard interface {
	Check(ctx context.Context, gymID string) *models.ApiError
}

type noSeatGuard struct{}

func (noSeatGuard) Check(context.Context, string) *models.ApiError { return nil }

// ---------- client_personal ----------

type coachClientAdder interface {
	AddCoachClient(ctx context.Context, coachID, clientID string) error
}

type clientPersonalKind struct {
	ent     EntitlementGuard // nil disables the check
	coaches coachClientAdder
}

// NewClientPersonalKind is the legacy registration link behaviour: coach_clients plus the coach's entitlement check.
func NewClientPersonalKind(ent EntitlementGuard, coaches coachClientAdder) InvitationKind {
	return &clientPersonalKind{ent: ent, coaches: coaches}
}

func (k *clientPersonalKind) Name() string { return db.InviteKindClientPersonal }

func (k *clientPersonalKind) AuthorizeCreate(ctx context.Context, a Actor, _ CreateInvite) (*InviteScope, *models.ApiError) {
	if !db.IsCoachingRole(a.Role) {
		return nil, teamErr(http.StatusForbidden, errInviteCoachRequired)
	}
	if k.ent != nil {
		if apiErr := k.ent.CheckInvitations(ctx, a.ID); apiErr != nil {
			return nil, apiErr
		}
	}
	return &InviteScope{CreatorRole: "coach"}, nil
}

func (k *clientPersonalKind) Guard(ctx context.Context, l *db.InviteLink) *models.ApiError {
	if k.ent == nil {
		return nil
	}
	return k.ent.CheckInvitations(ctx, l.CreatorID)
}

func (k *clientPersonalKind) Redeem(ctx context.Context, in RedeemInput) (*RedeemResult, *models.ApiError) {
	if !in.NewUser && in.User.UserRole != "client" {
		return nil, &models.ApiError{Code: http.StatusConflict, Error: models.ErrRegistrationLinkRole}
	}
	if err := k.coaches.AddCoachClient(ctx, in.Link.CreatorID, in.User.ExternalID); err != nil {
		if in.NewUser {
			// The account exists already: same as the legacy signup, which does not fail on the link write.
			zap.L().Warn("personal client link failed after signup", zap.String("component", "invite"), zap.String("link_id", in.Link.ID.Hex()))
		} else {
			return nil, teamErr(http.StatusConflict, errInviteAlreadyLinked)
		}
	}
	return &RedeemResult{Kind: db.InviteKindClientPersonal, Status: RedeemJoined, CoachID: in.Link.CreatorID, InviteID: hexID(in.Link)}, nil
}

func hexID(l *db.InviteLink) string {
	if l == nil || l.ID.IsZero() {
		return ""
	}
	return l.ID.Hex()
}

// ---------- client_gym ----------

type gymClientJoiner interface {
	Join(ctx context.Context, in JoinGymClientInput) (*db.GymClient, *models.ApiError)
}

type gymClientReader interface {
	Get(ctx context.Context, gymID, clientID string) (*db.GymClient, error)
}

type inviteGymReader interface {
	GetGym(ctx context.Context, id string) (*db.Gym, error)
}

type clientGymKind struct {
	authz   Authorizer
	members GymMembershipReader
	gyms    inviteGymReader
	joiner  gymClientJoiner
	reader  gymClientReader
	enabled bool // CLIENT_GYM_RELATIONS: without it Join answers 404, so such links are never issued or redeemed
}

// NewClientGymKind writes only gym_clients (through GymClientService.Join) and never touches coach_clients.
func NewClientGymKind(authz Authorizer, members GymMembershipReader, gyms inviteGymReader, joiner gymClientJoiner, reader gymClientReader, enabled bool) InvitationKind {
	return &clientGymKind{authz: authz, members: members, gyms: gyms, joiner: joiner, reader: reader, enabled: enabled}
}

func (k *clientGymKind) Name() string { return db.InviteKindClientGym }

// AuthorizeCreate: a coach member always links to himself; the owner and head coaches need member.invite and create a
// pool link.
func (k *clientGymKind) AuthorizeCreate(ctx context.Context, a Actor, req CreateInvite) (*InviteScope, *models.ApiError) {
	if !k.enabled {
		return nil, teamErr(http.StatusBadRequest, errClientGymDisabled)
	}
	if req.GymID == "" {
		return nil, teamErr(http.StatusBadRequest, errInviteGymRequired)
	}
	m, err := k.members.MembershipOf(ctx, a.ID, req.GymID)
	if err != nil {
		zap.L().Error("invite create membership lookup failed", zap.String("component", "invite"), zap.Error(err))
		return nil, teamErr(http.StatusInternalServerError, errInternal)
	}
	if m == nil || m.Status != db.GymMemberStatusActive {
		return nil, teamErr(http.StatusNotFound, errGymNotFound)
	}
	g, err := k.gyms.GetGym(ctx, req.GymID)
	if err != nil {
		zap.L().Error("invite create gym lookup failed", zap.String("component", "invite"), zap.Error(err))
		return nil, teamErr(http.StatusInternalServerError, errInternal)
	}
	if g == nil || g.Status != db.GymStatusActive {
		return nil, teamErr(http.StatusNotFound, errGymNotFound)
	}
	if m.Role == db.GymMemberRoleCoach {
		return &InviteScope{GymID: req.GymID, TargetCoachID: a.ID, CreatorRole: string(m.Role)}, nil
	}
	d, apiErr := k.authz.Can(ctx, Actor{ID: a.ID}, CapMemberInvite, Resource{Type: ResourceGym, ID: req.GymID, GymID: req.GymID})
	if apiErr != nil {
		return nil, apiErr
	}
	if !d.Allowed {
		return nil, teamErr(http.StatusForbidden, errMemberInviteDenied)
	}
	return &InviteScope{GymID: req.GymID, CreatorRole: string(m.Role)}, nil
}

// Guard is the pre-redeem check, also run for signups and accepts: gym client invites need CLIENT_GYM_RELATIONS and an
// active gym (an archived or deleted gym looks like an unknown token).
func (k *clientGymKind) Guard(ctx context.Context, l *db.InviteLink) *models.ApiError {
	if !k.enabled {
		return teamErr(http.StatusBadRequest, errClientGymDisabled)
	}
	g, err := k.gyms.GetGym(ctx, l.GymID)
	if err != nil {
		zap.L().Error("invite guard gym lookup failed", zap.String("component", "invite"), zap.Error(err))
		return teamErr(http.StatusInternalServerError, errInternal)
	}
	if g == nil || g.Status != db.GymStatusActive {
		return inviteNotFound()
	}
	return nil
}

func (k *clientGymKind) Redeem(ctx context.Context, in RedeemInput) (*RedeemResult, *models.ApiError) {
	l, u := in.Link, in.User
	if !in.NewUser && u.UserRole != "client" {
		return nil, &models.ApiError{Code: http.StatusConflict, Error: models.ErrRegistrationLinkRole}
	}
	res := &RedeemResult{Kind: db.InviteKindClientGym, GymID: l.GymID, CoachID: l.TargetCoachID, InviteID: hexID(l)}
	// An email-bound invite to an existing account is never redeemed implicitly: the client must accept it.
	if l.Email != "" && !in.NewUser && !in.Accepted {
		res.Status = RedeemPendingAcceptance
		return res, nil
	}
	cur, err := k.reader.Get(ctx, l.GymID, u.ExternalID)
	if err != nil {
		zap.L().Error("invite redeem membership lookup failed", zap.String("component", "invite"), zap.Error(err))
		return nil, teamErr(http.StatusInternalServerError, errInternal)
	}
	if cur != nil {
		res.Status = RedeemAlreadyMember
		res.CoachID = cur.CoachID
		return res, nil
	}
	if _, apiErr := k.joiner.Join(ctx, JoinGymClientInput{
		GymID: l.GymID, ClientID: u.ExternalID, CoachID: l.TargetCoachID, InviteID: hexID(l), Consent: true,
	}); apiErr != nil {
		if apiErr.Code == http.StatusConflict && errors.Is(apiErr.Error, errClientExists) {
			res.Status = RedeemAlreadyMember // lost a race with a parallel redemption of the same client
			return res, nil
		}
		return nil, apiErr
	}
	res.Status = RedeemJoined
	return res, nil
}

// ---------- member_coach / member_head_coach ----------

// memberJoiner is the gym join seam (the gymJoinRedeemer of the signup paths plus the signed-in accept).
type memberJoiner interface {
	gymJoinRedeemer
	AcceptByToken(ctx context.Context, callerID, token string) (*GymJoined, *models.ApiError)
}

// memberLinkCreator creates the GymInvitation based join links.
type memberLinkCreator interface {
	CreateJoinLink(ctx context.Context, callerID, gymID string) (*JoinLinkResult, *models.ApiError)
	CreateCoachJoinLink(ctx context.Context, callerID, gymID, supervisorID string) (*JoinLinkResult, *models.ApiError)
}

type memberKind struct {
	name    string
	joiner  memberJoiner
	maker   memberLinkCreator
	seats   SeatGuard
	members GymMembershipReader // validates supervisor_id; nil skips
}

// NewMemberKinds returns the member_coach and member_head_coach kinds. They are Adapters over the GymInvitation flows:
// the gym service owns creation (capability and one-gym rules) and the join. seats may be nil (no-op until P8a).
func NewMemberKinds(joiner memberJoiner, maker memberLinkCreator, seats SeatGuard, members GymMembershipReader) []InvitationKind {
	if seats == nil {
		seats = noSeatGuard{}
	}
	return []InvitationKind{
		&memberKind{name: db.InviteKindMemberCoach, joiner: joiner, maker: maker, seats: seats, members: members},
		&memberKind{name: db.InviteKindMemberHeadCoach, joiner: joiner, maker: maker, seats: seats, members: members},
	}
}

func (k *memberKind) Name() string { return k.name }

func (k *memberKind) AuthorizeCreate(_ context.Context, _ Actor, req CreateInvite) (*InviteScope, *models.ApiError) {
	if req.GymID == "" {
		return nil, teamErr(http.StatusBadRequest, errInviteGymRequired)
	}
	return &InviteScope{GymID: req.GymID}, nil
}

func (k *memberKind) Issue(ctx context.Context, a Actor, req CreateInvite) (*InviteCreated, *models.ApiError) {
	if _, apiErr := k.AuthorizeCreate(ctx, a, req); apiErr != nil {
		return nil, apiErr
	}
	if req.SupervisorID != "" && k.name == db.InviteKindMemberCoach && k.members != nil {
		m, err := k.members.MembershipOf(ctx, req.SupervisorID, req.GymID)
		if err != nil {
			zap.L().Error("invite supervisor lookup failed", zap.String("component", "invite"), zap.Error(err))
			return nil, teamErr(http.StatusInternalServerError, errInternal)
		}
		if m == nil || m.Status != db.GymMemberStatusActive {
			return nil, teamErr(http.StatusBadRequest, errInviteSupervisor)
		}
	}
	var (
		res    *JoinLinkResult
		apiErr *models.ApiError
	)
	if k.name == db.InviteKindMemberHeadCoach {
		res, apiErr = k.maker.CreateJoinLink(ctx, a.ID, req.GymID)
	} else {
		res, apiErr = k.maker.CreateCoachJoinLink(ctx, a.ID, req.GymID, req.SupervisorID)
	}
	if apiErr != nil {
		return nil, apiErr
	}
	exp := res.ExpiresAt
	return &InviteCreated{ID: res.InvitationID, Link: res.Link, ExpiresAt: &exp}, nil
}

func (k *memberKind) Guard(ctx context.Context, l *db.InviteLink) *models.ApiError {
	return k.seats.Check(ctx, l.GymID)
}

func (k *memberKind) Redeem(ctx context.Context, in RedeemInput) (*RedeemResult, *models.ApiError) {
	if in.Token == "" {
		return nil, inviteNotFound()
	}
	if in.NewUser {
		if apiErr := k.joiner.JoinOnSignup(ctx, in.User.ExternalID, in.Token); apiErr != nil {
			return nil, apiErr
		}
		return &RedeemResult{Kind: k.name, Status: RedeemJoined, GymID: in.Link.GymID}, nil
	}
	joined, apiErr := k.joiner.AcceptByToken(ctx, in.User.ExternalID, in.Token)
	if apiErr != nil {
		return nil, apiErr
	}
	gymID := in.Link.GymID
	if joined != nil && joined.GymID != "" {
		gymID = joined.GymID
	}
	return &RedeemResult{Kind: k.name, Status: RedeemJoined, GymID: gymID}, nil
}

// ---------- legacy_email ----------

type emailInviteAcceptor interface {
	AcceptInvitation(ctx context.Context, invitationID, email, userRefreshToken string) (*invitation.Invitation, *models.ApiError)
}

type legacyEmailKind struct {
	invites emailInviteAcceptor
}

// NewLegacyEmailKind is the Adapter over invitation-api email invites (pkg/invitation). They are created in
// invitation-api, so creating one here is refused.
func NewLegacyEmailKind(a emailInviteAcceptor) InvitationKind { return &legacyEmailKind{invites: a} }

const inviteKindLegacyEmail = "legacy_email"

func (k *legacyEmailKind) Name() string { return inviteKindLegacyEmail }

func (k *legacyEmailKind) AuthorizeCreate(context.Context, Actor, CreateInvite) (*InviteScope, *models.ApiError) {
	return nil, teamErr(http.StatusBadRequest, errInviteCreateDenied)
}

func (k *legacyEmailKind) Guard(context.Context, *db.InviteLink) *models.ApiError { return nil }

func (k *legacyEmailKind) Redeem(ctx context.Context, in RedeemInput) (*RedeemResult, *models.ApiError) {
	refresh := ""
	if in.User.UserRefreshToken != nil {
		refresh = *in.User.UserRefreshToken
	}
	if _, apiErr := k.invites.AcceptInvitation(ctx, in.User.ExternalID, strings.ToLower(strings.TrimSpace(in.User.UserEmail)), refresh); apiErr != nil {
		return nil, apiErr
	}
	return &RedeemResult{Kind: inviteKindLegacyEmail, Status: RedeemJoined}, nil
}
