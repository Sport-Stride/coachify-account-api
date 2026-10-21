package services

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/zap"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
)

const (
	maxGymNameLen = 100
	// invitesWindow is the rolling window of the per-gym invitation rate limit.
	invitesWindow = 24 * time.Hour
)

var (
	errGymOwnerAccess      = errors.New("Gym owner access required")
	errCoachAccess         = errors.New("Coach access required")
	errGymNotFound         = errors.New("gym not found")
	errGymArchived         = errors.New("gym is archived")
	errInviteNotFound      = errors.New("invitation not found")
	errAlreadyInvited      = errors.New("already invited")
	errInviteLimit         = errors.New("daily invitation limit reached")
	errInviteNotPending    = errors.New("invitation is no longer pending")
	errInviteExpired       = errors.New("invitation expired")
	errAlreadyInGym        = errors.New("coach already belongs to a gym")
	errHeadInOtherGym      = errors.New("head coach already belongs to another gym")
	errNotInGym            = errors.New("not in a gym")
	errInvalidEmail        = errors.New("invalid email")
	errInvalidGymName      = errors.New("name is required (max 100 characters)")
	errInvalidGymStatus    = errors.New("invalid status")
	errOwnerNotFound       = errors.New("owner not found")
	errOwnerInvalid        = errors.New("owner must be an active gym owner")
	errTargetNotActiveHead = errors.New("target is not an active head coach")
	errNothingToUpdate     = errors.New("nothing to update")
)

// GymUserStore is the part of the user repository the gym service needs.
type GymUserStore interface {
	GetUserByExternalIdUpdate(ctx context.Context, userID string) (*db.User, *models.ApiError)
	SetHeadCoach(ctx context.Context, externalID string, flag bool, promotedBy string) error
}

// GymStore is the part of the gym repository the gym service needs.
type GymStore interface {
	CreateGym(ctx context.Context, g *db.Gym) error
	GetGym(ctx context.Context, id string) (*db.Gym, error)
	GetOwnedGym(ctx context.Context, id, ownerID string) (*db.Gym, error)
	UpdateGym(ctx context.Context, id string, u db.GymUpdate) (bool, error)
	ListGymsByOwner(ctx context.Context, ownerID string) ([]db.GymSummary, error)
	ListGymsAdmin(ctx context.Context, page, size int, q, status string) ([]db.AdminGymRow, int, error)
	ListGymHeadCoachesAdmin(ctx context.Context, gymID string) ([]db.AdminGymHeadCoachRow, error)

	AddHeadCoach(ctx context.Context, h *db.GymHeadCoach, reason string) error
	GetGymOfHead(ctx context.Context, headID string) (*db.GymHeadCoach, error)
	RemoveHeadCoach(ctx context.Context, gymID, headID, reason string) (bool, error)
	ReleaseHead(ctx context.Context, headID, reason string) (string, bool, error)
	RemoveAllByGym(ctx context.Context, gymID, reason string) ([]string, error)

	Overview(ctx context.Context, gymID string) (*db.GymOverview, error)
	ListHeadCoachRows(ctx context.Context, gymID, q string, page, size int) ([]db.GymHeadCoachRow, int, error)
	ListCoaches(ctx context.Context, gymID string, q db.GymCoachQuery) ([]db.GymCoachRow, int, error)
	ListClients(ctx context.Context, gymID string, q db.GymClientQuery) ([]db.GymClientRow, int, error)

	InsertInvitation(ctx context.Context, inv *db.GymInvitation) error
	CountInvitationsSince(ctx context.Context, gymID string, since time.Time) (int, error)
	ExpireDue(ctx context.Context, gymID, email string, now time.Time) error
	GetInvitation(ctx context.Context, id string) (*db.GymInvitation, error)
	TransitionInvitation(ctx context.Context, id, from, to, inviteeID string, at time.Time) (bool, error)
	RevokePendingByGym(ctx context.Context, gymID string, at time.Time) error
	ListInvitations(ctx context.Context, gymID, status string, page, size int) ([]db.GymInvitation, int, error)
	ListPendingForEmail(ctx context.Context, email string, now time.Time) ([]db.GymInvitationView, error)

	IsTeamMember(ctx context.Context, coachID string) (bool, error)
	InsertAudit(ctx context.Context, e *db.TeamAuditEntry) error
	InsertAuditMany(ctx context.Context, entries []*db.TeamAuditEntry) error
	ListAudit(ctx context.Context, gymID string, page, size int) ([]db.TeamAuditEntry, int, error)
}

// Compile-time checks that the concrete repositories satisfy the stores.
var (
	_ GymUserStore          = (*repositories.UserRepository)(nil)
	_ GymStore              = (*repositories.GymRepository)(nil)
	_ GymMembershipReleaser = (*repositories.GymRepository)(nil)
)

// GymSettings are the invitation limits (from env, see utils.GymConfig).
type GymSettings struct {
	InviteTTL     time.Duration
	InvitesPerDay int
}

// GymOwnerMe is the answer of GET /gym-owner/me.
type GymOwnerMe struct {
	IsGymOwner bool            `json:"is_gym_owner"`
	Gyms       []db.GymSummary `json:"gyms"`
}

// GymView is a gym as returned by the owner and admin write endpoints.
type GymView struct {
	GymID           string     `json:"gym_id"`
	Name            string     `json:"name"`
	Status          string     `json:"status"`
	OwnerID         string     `json:"owner_id"`
	DefaultCurrency string     `json:"default_currency"`
	Timezone        string     `json:"timezone"`
	CreatedAt       time.Time  `json:"created_at"`
	ArchivedAt      *time.Time `json:"archived_at,omitempty"`
}

func gymView(g *db.Gym) *GymView {
	return &GymView{
		GymID: g.ID.Hex(), Name: g.Name, Status: g.Status, OwnerID: g.OwnerID,
		DefaultCurrency: g.DefaultCurrency, Timezone: g.Timezone,
		CreatedAt: g.CreatedAt, ArchivedAt: g.ArchivedAt,
	}
}

// GymInviteResult is the neutral answer of an invitation: identical whether or not the email has an account.
type GymInviteResult struct {
	InvitationID string `json:"invitation_id"`
	Status       string `json:"status"`
}

// GymInvitationRow is an invitation as the gym owner sees it.
type GymInvitationRow struct {
	ID          string     `json:"id"`
	Email       string     `json:"email"`
	Status      string     `json:"status"`
	ExpiresAt   time.Time  `json:"expires_at"`
	CreatedAt   time.Time  `json:"created_at"`
	RespondedAt *time.Time `json:"responded_at,omitempty"`
}

// GymJoined is the answer of an accepted invitation.
type GymJoined struct {
	GymID string `json:"gym_id"`
	Name  string `json:"name"`
}

// GymOwnerName is the public view of a gym owner.
type GymOwnerName struct {
	Firstname string `json:"firstname"`
	Lastname  string `json:"lastname"`
}

// MyGymInfo is the gym of a head coach.
type MyGymInfo struct {
	GymID string       `json:"gym_id"`
	Name  string       `json:"name"`
	Owner GymOwnerName `json:"owner"`
}

// GymAdminUpdate lists the admin-patchable gym fields; nil means unchanged.
type GymAdminUpdate struct {
	Name    *string
	Status  *string
	OwnerID *string
}

// GymMembershipReleaser removes the gym membership of a head coach (used when a head coach is demoted).
type GymMembershipReleaser interface {
	ReleaseHead(ctx context.Context, headID, reason string) (string, bool, error)
}

var validInviteStatuses = map[string]bool{
	db.GymInvitePending: true, db.GymInviteAccepted: true, db.GymInviteDeclined: true,
	db.GymInviteRevoked: true, db.GymInviteExpired: true,
}

// IsValidGymInviteStatus reports whether s is a known invitation status.
func IsValidGymInviteStatus(s string) bool { return validInviteStatuses[s] }

// GymService manages gyms, their head coaches and invitations.
type GymService interface {
	// Gym owner operations (the service verifies role, status and ownership in the database).
	OwnerMe(ctx context.Context, callerID string) (*GymOwnerMe, *models.ApiError)
	Overview(ctx context.Context, callerID, gymID string) (*db.GymOverview, *models.ApiError)
	RenameGym(ctx context.Context, callerID, gymID, name string) (*GymView, *models.ApiError)
	ListHeadCoaches(ctx context.Context, callerID, gymID, q string, page, size int) ([]db.GymHeadCoachRow, int, *models.ApiError)
	RemoveHeadCoach(ctx context.Context, callerID, gymID, headID string) *models.ApiError
	ListCoaches(ctx context.Context, callerID, gymID string, q db.GymCoachQuery) ([]db.GymCoachRow, int, *models.ApiError)
	ListClients(ctx context.Context, callerID, gymID string, q db.GymClientQuery) ([]db.GymClientRow, int, *models.ApiError)
	Invite(ctx context.Context, callerID, gymID, email string) (*GymInviteResult, *models.ApiError)
	ListInvitations(ctx context.Context, callerID, gymID, status string, page, size int) ([]GymInvitationRow, int, *models.ApiError)
	RevokeInvitation(ctx context.Context, callerID, gymID, invitationID string) *models.ApiError

	// Coach self-service.
	ListMyInvitations(ctx context.Context, callerID string) ([]db.GymInvitationView, *models.ApiError)
	AcceptInvitation(ctx context.Context, callerID, invitationID string) (*GymJoined, *models.ApiError)
	DeclineInvitation(ctx context.Context, callerID, invitationID string) *models.ApiError

	// Head coach operations.
	MyGym(ctx context.Context, callerID string) (*MyGymInfo, *models.ApiError)
	LeaveGym(ctx context.Context, callerID string) *models.ApiError

	// Admin operations (the handler checks the admin role).
	AdminListGyms(ctx context.Context, page, size int, q, status string) ([]db.AdminGymRow, int, *models.ApiError)
	AdminListGymHeadCoaches(ctx context.Context, gymID string) ([]db.AdminGymHeadCoachRow, *models.ApiError)
	AdminCreateGym(ctx context.Context, adminID, name, ownerID string) (*GymView, *models.ApiError)
	AdminUpdateGym(ctx context.Context, adminID, gymID string, in GymAdminUpdate) (*GymView, *models.ApiError)
	AdminAddHeadCoach(ctx context.Context, adminID, gymID, headID string) *models.ApiError
	AdminRemoveHeadCoach(ctx context.Context, adminID, gymID, headID string) *models.ApiError
	AdminAudit(ctx context.Context, gymID string, page, size int) ([]db.TeamAuditEntry, int, *models.ApiError)
}

type gymServiceImpl struct {
	users GymUserStore
	gyms  GymStore
	set   GymSettings
}

func NewGymService(users GymUserStore, gyms GymStore, set GymSettings) GymService {
	if set.InviteTTL <= 0 {
		set.InviteTTL = 168 * time.Hour
	}
	if set.InvitesPerDay <= 0 {
		set.InvitesPerDay = 20
	}
	return &gymServiceImpl{users: users, gyms: gyms, set: set}
}

// internal logs the real error (ids only, no PII) and returns a generic 500.
func (s *gymServiceImpl) internal(op string, err error) *models.ApiError {
	zap.L().Error("gym service failure",
		zap.String("component", "gym"),
		zap.String("op", op),
		zap.Error(err),
	)
	return teamErr(http.StatusInternalServerError, errInternal)
}

// loadUser fetches a user; notFound is returned as 404 with the given error.
func (s *gymServiceImpl) loadUser(ctx context.Context, op, id string, notFound error) (*db.User, *models.ApiError) {
	u, apiErr := s.users.GetUserByExternalIdUpdate(ctx, id)
	if apiErr != nil {
		if apiErr.Code == http.StatusNotFound {
			return nil, teamErr(http.StatusNotFound, notFound)
		}
		return nil, s.internal(op, apiErr.Error)
	}
	if u == nil {
		return nil, teamErr(http.StatusNotFound, notFound)
	}
	return u, nil
}

// requireRole re-checks in the database that the caller has the role and an Active account.
func (s *gymServiceImpl) requireRole(ctx context.Context, op, callerID, role string, denied error, head bool) (*db.User, *models.ApiError) {
	u, apiErr := s.users.GetUserByExternalIdUpdate(ctx, callerID)
	if apiErr != nil {
		if apiErr.Code == http.StatusNotFound {
			return nil, teamErr(http.StatusForbidden, denied)
		}
		return nil, s.internal(op, apiErr.Error)
	}
	if u == nil || u.UserRole != role || u.UserStatus != db.Active || (head && !u.IsHeadCoach) {
		return nil, teamErr(http.StatusForbidden, denied)
	}
	return u, nil
}

func (s *gymServiceImpl) requireOwner(ctx context.Context, callerID string) *models.ApiError {
	_, apiErr := s.requireRole(ctx, "requireOwner", callerID, db.RoleGymOwner, errGymOwnerAccess, false)
	return apiErr
}

// ownedGym verifies the caller is an Active gym owner and owns the gym; a foreign or unknown gym is a 404.
func (s *gymServiceImpl) ownedGym(ctx context.Context, callerID, gymID string) (*db.Gym, *models.ApiError) {
	if apiErr := s.requireOwner(ctx, callerID); apiErr != nil {
		return nil, apiErr
	}
	g, err := s.gyms.GetOwnedGym(ctx, gymID, callerID)
	if err != nil {
		return nil, s.internal("ownedGym", err)
	}
	if g == nil {
		return nil, teamErr(http.StatusNotFound, errGymNotFound)
	}
	return g, nil
}

func (s *gymServiceImpl) audit(ctx context.Context, e *db.TeamAuditEntry) {
	if err := s.gyms.InsertAudit(ctx, e); err != nil {
		// The mutation already happened; the failure is logged, not surfaced.
		zap.L().Error("gym audit write failed",
			zap.String("component", "gym"),
			zap.String("action", e.Action),
			zap.String("gym_id", e.GymID),
			zap.Error(err),
		)
	}
}

func (s *gymServiceImpl) auditMany(ctx context.Context, entries []*db.TeamAuditEntry) {
	if err := s.gyms.InsertAuditMany(ctx, entries); err != nil {
		zap.L().Error("gym audit write failed",
			zap.String("component", "gym"),
			zap.Int("entries", len(entries)),
			zap.Error(err),
		)
	}
}

func validGymName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	n := len([]rune(name))
	return name, n > 0 && n <= maxGymNameLen
}

// ---------- Gym owner ----------

func (s *gymServiceImpl) OwnerMe(ctx context.Context, callerID string) (*GymOwnerMe, *models.ApiError) {
	u, apiErr := s.users.GetUserByExternalIdUpdate(ctx, callerID)
	if apiErr != nil {
		if apiErr.Code == http.StatusNotFound {
			return &GymOwnerMe{Gyms: []db.GymSummary{}}, nil
		}
		return nil, s.internal("OwnerMe.load", apiErr.Error)
	}
	if u == nil || u.UserRole != db.RoleGymOwner || u.UserStatus != db.Active {
		return &GymOwnerMe{Gyms: []db.GymSummary{}}, nil
	}
	gyms, err := s.gyms.ListGymsByOwner(ctx, callerID)
	if err != nil {
		return nil, s.internal("OwnerMe.list", err)
	}
	if gyms == nil {
		gyms = []db.GymSummary{}
	}
	return &GymOwnerMe{IsGymOwner: true, Gyms: gyms}, nil
}

func (s *gymServiceImpl) Overview(ctx context.Context, callerID, gymID string) (*db.GymOverview, *models.ApiError) {
	g, apiErr := s.ownedGym(ctx, callerID, gymID)
	if apiErr != nil {
		return nil, apiErr
	}
	o, err := s.gyms.Overview(ctx, g.ID.Hex())
	if err != nil {
		return nil, s.internal("Overview", err)
	}
	return o, nil
}

func (s *gymServiceImpl) RenameGym(ctx context.Context, callerID, gymID, name string) (*GymView, *models.ApiError) {
	g, apiErr := s.ownedGym(ctx, callerID, gymID)
	if apiErr != nil {
		return nil, apiErr
	}
	name, ok := validGymName(name)
	if !ok {
		return nil, teamErr(http.StatusBadRequest, errInvalidGymName)
	}
	if g.Status != db.GymStatusActive {
		return nil, teamErr(http.StatusConflict, errGymArchived)
	}
	found, err := s.gyms.UpdateGym(ctx, g.ID.Hex(), db.GymUpdate{Name: &name})
	if err != nil {
		return nil, s.internal("RenameGym", err)
	}
	if !found {
		return nil, teamErr(http.StatusNotFound, errGymNotFound)
	}
	g.Name = name
	return gymView(g), nil
}

func (s *gymServiceImpl) ListHeadCoaches(ctx context.Context, callerID, gymID, q string, page, size int) ([]db.GymHeadCoachRow, int, *models.ApiError) {
	g, apiErr := s.ownedGym(ctx, callerID, gymID)
	if apiErr != nil {
		return nil, 0, apiErr
	}
	rows, total, err := s.gyms.ListHeadCoachRows(ctx, g.ID.Hex(), q, page, size)
	if err != nil {
		return nil, 0, s.internal("ListHeadCoaches", err)
	}
	return rows, total, nil
}

// RemoveHeadCoach closes the gym membership only: the head coach flag and the team are kept (only an admin demotes).
func (s *gymServiceImpl) RemoveHeadCoach(ctx context.Context, callerID, gymID, headID string) *models.ApiError {
	g, apiErr := s.ownedGym(ctx, callerID, gymID)
	if apiErr != nil {
		return apiErr
	}
	removed, err := s.gyms.RemoveHeadCoach(ctx, g.ID.Hex(), headID, db.PeriodReasonGymRemove)
	if err != nil {
		return s.internal("RemoveHeadCoach", err)
	}
	if !removed {
		return teamErr(http.StatusNotFound, errHeadNotFound)
	}
	s.audit(ctx, &db.TeamAuditEntry{
		Action: db.TeamActionGymRemoveHead, ActorID: callerID, ActorRole: db.TeamActorRoleGymOwner,
		HeadCoachID: headID, GymID: g.ID.Hex(),
	})
	return nil
}

func (s *gymServiceImpl) ListCoaches(ctx context.Context, callerID, gymID string, q db.GymCoachQuery) ([]db.GymCoachRow, int, *models.ApiError) {
	g, apiErr := s.ownedGym(ctx, callerID, gymID)
	if apiErr != nil {
		return nil, 0, apiErr
	}
	rows, total, err := s.gyms.ListCoaches(ctx, g.ID.Hex(), q)
	if err != nil {
		return nil, 0, s.internal("ListCoaches", err)
	}
	return rows, total, nil
}

func (s *gymServiceImpl) ListClients(ctx context.Context, callerID, gymID string, q db.GymClientQuery) ([]db.GymClientRow, int, *models.ApiError) {
	g, apiErr := s.ownedGym(ctx, callerID, gymID)
	if apiErr != nil {
		return nil, 0, apiErr
	}
	rows, total, err := s.gyms.ListClients(ctx, g.ID.Hex(), q)
	if err != nil {
		return nil, 0, s.internal("ListClients", err)
	}
	return rows, total, nil
}

func normalizeEmail(email string) (string, bool) {
	email = strings.ToLower(strings.TrimSpace(email))
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return "", false
	}
	return email, true
}

// Invite creates a pending invitation. It never looks the email up, so the answer cannot reveal whether an account exists.
// No email is sent: the invited coach sees the invitation in the app (GET /coach/gym-invitations).
func (s *gymServiceImpl) Invite(ctx context.Context, callerID, gymID, email string) (*GymInviteResult, *models.ApiError) {
	g, apiErr := s.ownedGym(ctx, callerID, gymID)
	if apiErr != nil {
		return nil, apiErr
	}
	if g.Status != db.GymStatusActive {
		return nil, teamErr(http.StatusConflict, errGymArchived)
	}
	email, ok := normalizeEmail(email)
	if !ok {
		return nil, teamErr(http.StatusBadRequest, errInvalidEmail)
	}

	now := time.Now()
	gid := g.ID.Hex()
	n, err := s.gyms.CountInvitationsSince(ctx, gid, now.Add(-invitesWindow))
	if err != nil {
		return nil, s.internal("Invite.count", err)
	}
	if n >= s.set.InvitesPerDay {
		return nil, teamErr(http.StatusTooManyRequests, errInviteLimit)
	}

	// An expired pending invitation would otherwise block a new one through the unique index.
	if err := s.gyms.ExpireDue(ctx, gid, email, now); err != nil {
		return nil, s.internal("Invite.expire", err)
	}
	inv := &db.GymInvitation{
		GymID: gid, InviterID: callerID, Email: email, Status: db.GymInvitePending,
		ExpiresAt: now.Add(s.set.InviteTTL), CreatedAt: now,
	}
	if err := s.gyms.InsertInvitation(ctx, inv); err != nil {
		if errors.Is(err, repositories.ErrInvitationPending) {
			return nil, teamErr(http.StatusConflict, errAlreadyInvited)
		}
		return nil, s.internal("Invite.insert", err)
	}
	s.audit(ctx, &db.TeamAuditEntry{
		Action: db.TeamActionGymInvite, ActorID: callerID, ActorRole: db.TeamActorRoleGymOwner, GymID: gid,
	})
	return &GymInviteResult{InvitationID: inv.ID.Hex(), Status: db.GymInvitePending}, nil
}

func (s *gymServiceImpl) ListInvitations(ctx context.Context, callerID, gymID, status string, page, size int) ([]GymInvitationRow, int, *models.ApiError) {
	g, apiErr := s.ownedGym(ctx, callerID, gymID)
	if apiErr != nil {
		return nil, 0, apiErr
	}
	gid := g.ID.Hex()
	if err := s.gyms.ExpireDue(ctx, gid, "", time.Now()); err != nil {
		return nil, 0, s.internal("ListInvitations.expire", err)
	}
	invs, total, err := s.gyms.ListInvitations(ctx, gid, status, page, size)
	if err != nil {
		return nil, 0, s.internal("ListInvitations.list", err)
	}
	rows := make([]GymInvitationRow, 0, len(invs))
	for _, i := range invs {
		rows = append(rows, GymInvitationRow{
			ID: i.ID.Hex(), Email: i.Email, Status: i.Status,
			ExpiresAt: i.ExpiresAt, CreatedAt: i.CreatedAt, RespondedAt: i.RespondedAt,
		})
	}
	return rows, total, nil
}

func (s *gymServiceImpl) RevokeInvitation(ctx context.Context, callerID, gymID, invitationID string) *models.ApiError {
	g, apiErr := s.ownedGym(ctx, callerID, gymID)
	if apiErr != nil {
		return apiErr
	}
	gid := g.ID.Hex()
	inv, err := s.gyms.GetInvitation(ctx, invitationID)
	if err != nil {
		return s.internal("RevokeInvitation.get", err)
	}
	if inv == nil || inv.GymID != gid {
		return teamErr(http.StatusNotFound, errInviteNotFound)
	}
	ok, err := s.gyms.TransitionInvitation(ctx, invitationID, db.GymInvitePending, db.GymInviteRevoked, "", time.Now())
	if err != nil {
		return s.internal("RevokeInvitation.set", err)
	}
	if !ok {
		return teamErr(http.StatusConflict, errInviteNotPending)
	}
	s.audit(ctx, &db.TeamAuditEntry{
		Action: db.TeamActionGymInviteRevoke, ActorID: callerID, ActorRole: db.TeamActorRoleGymOwner, GymID: gid,
	})
	return nil
}

// ---------- Coach self-service ----------

func (s *gymServiceImpl) requireCoach(ctx context.Context, callerID string) (*db.User, *models.ApiError) {
	return s.requireRole(ctx, "requireCoach", callerID, "coach", errCoachAccess, false)
}

func (s *gymServiceImpl) ListMyInvitations(ctx context.Context, callerID string) ([]db.GymInvitationView, *models.ApiError) {
	u, apiErr := s.requireCoach(ctx, callerID)
	if apiErr != nil {
		return nil, apiErr
	}
	email := strings.ToLower(strings.TrimSpace(u.UserEmail))
	now := time.Now()
	if err := s.gyms.ExpireDue(ctx, "", email, now); err != nil {
		return nil, s.internal("ListMyInvitations.expire", err)
	}
	views, err := s.gyms.ListPendingForEmail(ctx, email, now)
	if err != nil {
		return nil, s.internal("ListMyInvitations.list", err)
	}
	if views == nil {
		views = []db.GymInvitationView{}
	}
	return views, nil
}

// ownInvitation loads an invitation addressed to the caller; any other case is a 404 so nothing leaks.
func (s *gymServiceImpl) ownInvitation(ctx context.Context, u *db.User, invitationID string) (*db.GymInvitation, *models.ApiError) {
	inv, err := s.gyms.GetInvitation(ctx, invitationID)
	if err != nil {
		return nil, s.internal("ownInvitation", err)
	}
	if inv == nil || inv.Email != strings.ToLower(strings.TrimSpace(u.UserEmail)) {
		return nil, teamErr(http.StatusNotFound, errInviteNotFound)
	}
	return inv, nil
}

// pendingInvitation checks the invitation can still be answered; an overdue one is marked expired.
func (s *gymServiceImpl) pendingInvitation(ctx context.Context, inv *db.GymInvitation, now time.Time) *models.ApiError {
	if inv.Status != db.GymInvitePending {
		return teamErr(http.StatusConflict, errInviteNotPending)
	}
	if !inv.ExpiresAt.After(now) {
		if _, err := s.gyms.TransitionInvitation(ctx, inv.ID.Hex(), db.GymInvitePending, db.GymInviteExpired, "", now); err != nil {
			return s.internal("pendingInvitation.expire", err)
		}
		return teamErr(http.StatusConflict, errInviteExpired)
	}
	return nil
}

func (s *gymServiceImpl) AcceptInvitation(ctx context.Context, callerID, invitationID string) (*GymJoined, *models.ApiError) {
	u, apiErr := s.requireCoach(ctx, callerID)
	if apiErr != nil {
		return nil, apiErr
	}
	inv, apiErr := s.ownInvitation(ctx, u, invitationID)
	if apiErr != nil {
		return nil, apiErr
	}
	now := time.Now()
	if apiErr := s.pendingInvitation(ctx, inv, now); apiErr != nil {
		return nil, apiErr
	}

	member, err := s.gyms.IsTeamMember(ctx, callerID)
	if err != nil {
		return nil, s.internal("Accept.isMember", err)
	}
	if member {
		return nil, teamErr(http.StatusConflict, errCoachHasTeam)
	}
	cur, err := s.gyms.GetGymOfHead(ctx, callerID)
	if err != nil {
		return nil, s.internal("Accept.gymOf", err)
	}
	if cur != nil {
		return nil, teamErr(http.StatusConflict, errAlreadyInGym)
	}
	g, err := s.gyms.GetGym(ctx, inv.GymID)
	if err != nil {
		return nil, s.internal("Accept.gym", err)
	}
	if g == nil || g.Status != db.GymStatusActive {
		return nil, teamErr(http.StatusNotFound, errInviteNotFound)
	}
	gid := g.ID.Hex()

	// Becoming a head coach reuses the promote path: the flag is set with the gym owner as promoter.
	setFlag := !u.IsHeadCoach
	rollbackFlag := func() {
		if !setFlag {
			return
		}
		if err := s.users.SetHeadCoach(ctx, callerID, false, g.OwnerID); err != nil {
			s.internal("Accept.rollbackFlag", err)
		}
	}
	if setFlag {
		if err := s.users.SetHeadCoach(ctx, callerID, true, g.OwnerID); err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				return nil, teamErr(http.StatusNotFound, errInviteNotFound)
			}
			return nil, s.internal("Accept.setHead", err)
		}
		// Close the race with a concurrent team assignment.
		member, err = s.gyms.IsTeamMember(ctx, callerID)
		if err != nil {
			rollbackFlag()
			return nil, s.internal("Accept.recheck", err)
		}
		if member {
			rollbackFlag()
			return nil, teamErr(http.StatusConflict, errCoachHasTeam)
		}
	}

	err = s.gyms.AddHeadCoach(ctx, &db.GymHeadCoach{
		GymID: gid, HeadCoachID: callerID, AddedBy: inv.InviterID, AddedByRole: db.GymAdderGymOwner,
		InvitationID: inv.ID.Hex(), AddedAt: now,
	}, db.PeriodReasonGymAdd)
	if err != nil {
		rollbackFlag()
		if errors.Is(err, repositories.ErrHeadCoachInGym) {
			return nil, teamErr(http.StatusConflict, errAlreadyInGym)
		}
		return nil, s.internal("Accept.add", err)
	}

	ok, err := s.gyms.TransitionInvitation(ctx, inv.ID.Hex(), db.GymInvitePending, db.GymInviteAccepted, callerID, now)
	if err != nil || !ok {
		// Undo: the invitation was revoked or declined meanwhile, or the write failed.
		if _, _, rErr := s.gyms.ReleaseHead(ctx, callerID, db.PeriodReasonGymRemove); rErr != nil {
			s.internal("Accept.undoMembership", rErr)
		}
		rollbackFlag()
		if err != nil {
			return nil, s.internal("Accept.transition", err)
		}
		return nil, teamErr(http.StatusConflict, errInviteNotPending)
	}

	entries := make([]*db.TeamAuditEntry, 0, 2)
	if setFlag {
		entries = append(entries, &db.TeamAuditEntry{
			Action: db.TeamActionPromote, ActorID: g.OwnerID, ActorRole: db.TeamActorRoleGymOwner,
			HeadCoachID: callerID, GymID: gid,
		})
	}
	entries = append(entries, &db.TeamAuditEntry{
		Action: db.TeamActionGymInviteAccept, ActorID: callerID, ActorRole: db.TeamActorRoleHead,
		HeadCoachID: callerID, GymID: gid,
	})
	s.auditMany(ctx, entries)
	return &GymJoined{GymID: gid, Name: g.Name}, nil
}

func (s *gymServiceImpl) DeclineInvitation(ctx context.Context, callerID, invitationID string) *models.ApiError {
	u, apiErr := s.requireCoach(ctx, callerID)
	if apiErr != nil {
		return apiErr
	}
	inv, apiErr := s.ownInvitation(ctx, u, invitationID)
	if apiErr != nil {
		return apiErr
	}
	now := time.Now()
	if apiErr := s.pendingInvitation(ctx, inv, now); apiErr != nil {
		return apiErr
	}
	ok, err := s.gyms.TransitionInvitation(ctx, inv.ID.Hex(), db.GymInvitePending, db.GymInviteDeclined, callerID, now)
	if err != nil {
		return s.internal("Decline.set", err)
	}
	if !ok {
		return teamErr(http.StatusConflict, errInviteNotPending)
	}
	s.audit(ctx, &db.TeamAuditEntry{
		Action: db.TeamActionGymInviteDecline, ActorID: callerID, ActorRole: db.TeamActorRoleHead,
		GymID: inv.GymID,
	})
	return nil
}

// ---------- Head coach ----------

func (s *gymServiceImpl) requireHead(ctx context.Context, callerID string) *models.ApiError {
	_, apiErr := s.requireRole(ctx, "requireHead", callerID, "coach", errHeadAccess, true)
	return apiErr
}

func (s *gymServiceImpl) MyGym(ctx context.Context, callerID string) (*MyGymInfo, *models.ApiError) {
	if apiErr := s.requireHead(ctx, callerID); apiErr != nil {
		return nil, apiErr
	}
	m, err := s.gyms.GetGymOfHead(ctx, callerID)
	if err != nil {
		return nil, s.internal("MyGym.member", err)
	}
	if m == nil {
		return nil, nil
	}
	g, err := s.gyms.GetGym(ctx, m.GymID)
	if err != nil {
		return nil, s.internal("MyGym.gym", err)
	}
	if g == nil {
		return nil, nil
	}
	info := &MyGymInfo{GymID: g.ID.Hex(), Name: g.Name}
	owner, apiErr := s.users.GetUserByExternalIdUpdate(ctx, g.OwnerID)
	if apiErr != nil && apiErr.Code != http.StatusNotFound {
		return nil, s.internal("MyGym.owner", apiErr.Error)
	}
	if apiErr == nil && owner != nil {
		info.Owner = GymOwnerName{Firstname: owner.UserFirstname, Lastname: owner.UserLastname}
	}
	return info, nil
}

func (s *gymServiceImpl) LeaveGym(ctx context.Context, callerID string) *models.ApiError {
	if apiErr := s.requireHead(ctx, callerID); apiErr != nil {
		return apiErr
	}
	gymID, removed, err := s.gyms.ReleaseHead(ctx, callerID, db.PeriodReasonGymLeave)
	if err != nil {
		return s.internal("LeaveGym", err)
	}
	if !removed {
		return teamErr(http.StatusNotFound, errNotInGym)
	}
	s.audit(ctx, &db.TeamAuditEntry{
		Action: db.TeamActionGymLeave, ActorID: callerID, ActorRole: db.TeamActorRoleHead,
		HeadCoachID: callerID, GymID: gymID,
	})
	return nil
}

// ---------- Admin ----------

func (s *gymServiceImpl) AdminListGyms(ctx context.Context, page, size int, q, status string) ([]db.AdminGymRow, int, *models.ApiError) {
	rows, total, err := s.gyms.ListGymsAdmin(ctx, page, size, q, status)
	if err != nil {
		return nil, 0, s.internal("AdminListGyms", err)
	}
	return rows, total, nil
}

// AdminListGymHeadCoaches returns the gym's current head coaches; 404 when the gym does not exist.
func (s *gymServiceImpl) AdminListGymHeadCoaches(ctx context.Context, gymID string) ([]db.AdminGymHeadCoachRow, *models.ApiError) {
	g, err := s.gyms.GetGym(ctx, gymID)
	if err != nil {
		return nil, s.internal("AdminListGymHeadCoaches.get", err)
	}
	if g == nil {
		return nil, teamErr(http.StatusNotFound, errGymNotFound)
	}
	rows, err := s.gyms.ListGymHeadCoachesAdmin(ctx, g.ID.Hex())
	if err != nil {
		return nil, s.internal("AdminListGymHeadCoaches", err)
	}
	return rows, nil
}

// activeOwner loads a user and verifies it is an Active gym owner.
func (s *gymServiceImpl) activeOwner(ctx context.Context, op, ownerID string) *models.ApiError {
	o, apiErr := s.loadUser(ctx, op, ownerID, errOwnerNotFound)
	if apiErr != nil {
		return apiErr
	}
	if o.UserRole != db.RoleGymOwner || o.UserStatus != db.Active {
		return teamErr(http.StatusBadRequest, errOwnerInvalid)
	}
	return nil
}

func (s *gymServiceImpl) AdminCreateGym(ctx context.Context, adminID, name, ownerID string) (*GymView, *models.ApiError) {
	name, ok := validGymName(name)
	if !ok {
		return nil, teamErr(http.StatusBadRequest, errInvalidGymName)
	}
	if ownerID == "" {
		return nil, teamErr(http.StatusBadRequest, errOwnerNotFound)
	}
	if apiErr := s.activeOwner(ctx, "AdminCreateGym.owner", ownerID); apiErr != nil {
		return nil, apiErr
	}
	now := time.Now()
	g := &db.Gym{
		Name: name, OwnerID: ownerID, Status: db.GymStatusActive,
		DefaultCurrency: db.GymDefaultCurrency, Timezone: db.GymDefaultTimezone,
		CreatedBy: adminID, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.gyms.CreateGym(ctx, g); err != nil {
		return nil, s.internal("AdminCreateGym", err)
	}
	s.audit(ctx, &db.TeamAuditEntry{
		Action: db.TeamActionGymCreate, ActorID: adminID, ActorRole: db.TeamActorRoleAdmin,
		GymID: g.ID.Hex(), OwnerID: ownerID,
	})
	return gymView(g), nil
}

func (s *gymServiceImpl) AdminUpdateGym(ctx context.Context, adminID, gymID string, in GymAdminUpdate) (*GymView, *models.ApiError) {
	if in.Name == nil && in.Status == nil && in.OwnerID == nil {
		return nil, teamErr(http.StatusBadRequest, errNothingToUpdate)
	}
	g, err := s.gyms.GetGym(ctx, gymID)
	if err != nil {
		return nil, s.internal("AdminUpdateGym.get", err)
	}
	if g == nil {
		return nil, teamErr(http.StatusNotFound, errGymNotFound)
	}
	gid := g.ID.Hex()

	var upd db.GymUpdate
	if in.Name != nil {
		name, ok := validGymName(*in.Name)
		if !ok {
			return nil, teamErr(http.StatusBadRequest, errInvalidGymName)
		}
		if name != g.Name {
			upd.Name = &name
		}
	}
	archiveRequested := false
	if in.Status != nil {
		if *in.Status != db.GymStatusActive && *in.Status != db.GymStatusArchived {
			return nil, teamErr(http.StatusBadRequest, errInvalidGymStatus)
		}
		archiveRequested = *in.Status == db.GymStatusArchived
		if *in.Status != g.Status {
			st := *in.Status
			upd.Status = &st
		}
	}
	if in.OwnerID != nil && *in.OwnerID != g.OwnerID {
		if apiErr := s.activeOwner(ctx, "AdminUpdateGym.owner", *in.OwnerID); apiErr != nil {
			return nil, apiErr
		}
		oid := *in.OwnerID
		upd.OwnerID = &oid
	}

	if upd.Name != nil || upd.Status != nil || upd.OwnerID != nil {
		found, err := s.gyms.UpdateGym(ctx, gid, upd)
		if err != nil {
			return nil, s.internal("AdminUpdateGym.update", err)
		}
		if !found {
			return nil, teamErr(http.StatusNotFound, errGymNotFound)
		}
	}

	if upd.OwnerID != nil {
		s.audit(ctx, &db.TeamAuditEntry{
			Action: db.TeamActionGymTransfer, ActorID: adminID, ActorRole: db.TeamActorRoleAdmin,
			GymID: gid, OwnerID: *upd.OwnerID, PrevOwnerID: g.OwnerID,
		})
	}

	// Archiving also runs when the gym is already archived, so a retry after a partial failure finishes the release.
	if archiveRequested {
		released, err := s.gyms.RemoveAllByGym(ctx, gid, db.PeriodReasonGymArchive)
		if err != nil {
			return nil, s.internal("AdminUpdateGym.release", err)
		}
		if err := s.gyms.RevokePendingByGym(ctx, gid, time.Now()); err != nil {
			return nil, s.internal("AdminUpdateGym.revoke", err)
		}
		entries := make([]*db.TeamAuditEntry, 0, len(released)+1)
		if upd.Status != nil {
			entries = append(entries, &db.TeamAuditEntry{
				Action: db.TeamActionGymArchive, ActorID: adminID, ActorRole: db.TeamActorRoleAdmin, GymID: gid,
			})
		}
		for _, id := range released {
			entries = append(entries, &db.TeamAuditEntry{
				Action: db.TeamActionGymRemoveHead, ActorID: adminID, ActorRole: db.TeamActorRoleAdmin,
				HeadCoachID: id, GymID: gid,
			})
		}
		if len(entries) > 0 {
			s.auditMany(ctx, entries)
		}
	}

	if upd.Name != nil {
		g.Name = *upd.Name
	}
	if upd.OwnerID != nil {
		g.OwnerID = *upd.OwnerID
	}
	if upd.Status != nil {
		g.Status = *upd.Status
		if g.Status == db.GymStatusArchived {
			now := time.Now()
			g.ArchivedAt = &now
		} else {
			g.ArchivedAt = nil
		}
	}
	return gymView(g), nil
}

func (s *gymServiceImpl) AdminAddHeadCoach(ctx context.Context, adminID, gymID, headID string) *models.ApiError {
	g, err := s.gyms.GetGym(ctx, gymID)
	if err != nil {
		return s.internal("AdminAddHeadCoach.gym", err)
	}
	if g == nil {
		return teamErr(http.StatusNotFound, errGymNotFound)
	}
	if g.Status != db.GymStatusActive {
		return teamErr(http.StatusConflict, errGymArchived)
	}
	gid := g.ID.Hex()

	head, apiErr := s.loadUser(ctx, "AdminAddHeadCoach.head", headID, errHeadNotFound)
	if apiErr != nil {
		return apiErr
	}
	if head.UserRole != "coach" || !head.IsHeadCoach || head.UserStatus != db.Active {
		return teamErr(http.StatusConflict, errTargetNotActiveHead)
	}
	cur, err := s.gyms.GetGymOfHead(ctx, headID)
	if err != nil {
		return s.internal("AdminAddHeadCoach.gymOf", err)
	}
	if cur != nil {
		if cur.GymID == gid { // idempotent
			return nil
		}
		return teamErr(http.StatusConflict, errHeadInOtherGym)
	}
	err = s.gyms.AddHeadCoach(ctx, &db.GymHeadCoach{
		GymID: gid, HeadCoachID: headID, AddedBy: adminID, AddedByRole: db.GymAdderAdmin, AddedAt: time.Now(),
	}, db.PeriodReasonGymAdd)
	if err != nil {
		if errors.Is(err, repositories.ErrHeadCoachInGym) {
			return teamErr(http.StatusConflict, errHeadInOtherGym)
		}
		return s.internal("AdminAddHeadCoach.add", err)
	}
	s.audit(ctx, &db.TeamAuditEntry{
		Action: db.TeamActionGymAddHead, ActorID: adminID, ActorRole: db.TeamActorRoleAdmin,
		HeadCoachID: headID, GymID: gid,
	})
	return nil
}

func (s *gymServiceImpl) AdminRemoveHeadCoach(ctx context.Context, adminID, gymID, headID string) *models.ApiError {
	g, err := s.gyms.GetGym(ctx, gymID)
	if err != nil {
		return s.internal("AdminRemoveHeadCoach.gym", err)
	}
	if g == nil {
		return teamErr(http.StatusNotFound, errGymNotFound)
	}
	gid := g.ID.Hex()
	removed, err := s.gyms.RemoveHeadCoach(ctx, gid, headID, db.PeriodReasonGymRemove)
	if err != nil {
		return s.internal("AdminRemoveHeadCoach", err)
	}
	if !removed {
		return teamErr(http.StatusNotFound, errHeadNotFound)
	}
	s.audit(ctx, &db.TeamAuditEntry{
		Action: db.TeamActionGymRemoveHead, ActorID: adminID, ActorRole: db.TeamActorRoleAdmin,
		HeadCoachID: headID, GymID: gid,
	})
	return nil
}

func (s *gymServiceImpl) AdminAudit(ctx context.Context, gymID string, page, size int) ([]db.TeamAuditEntry, int, *models.ApiError) {
	g, err := s.gyms.GetGym(ctx, gymID)
	if err != nil {
		return nil, 0, s.internal("AdminAudit.gym", err)
	}
	if g == nil {
		return nil, 0, teamErr(http.StatusNotFound, errGymNotFound)
	}
	entries, total, err := s.gyms.ListAudit(ctx, g.ID.Hex(), page, size)
	if err != nil {
		return nil, 0, s.internal("AdminAudit.list", err)
	}
	return entries, total, nil
}
