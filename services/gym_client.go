package services

import (
	"context"
	"errors"
	"net/http"
	"time"

	"go.uber.org/zap"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
)

var (
	errClientsDisabled    = errors.New("not found")
	errClientNotFound     = errors.New("client not found")
	errClientAssignDeny   = errors.New("assigning clients requires the client.assign capability")
	errClientUnassignDeny = errors.New("unassigning clients requires the client.unassign capability")
	errClientRemoveDeny   = errors.New("Gym owner access required")
	errCoachRequired      = errors.New("coach_id is required")
	errClientRequired     = errors.New("client id is required")
	errCoachNotInGym      = errors.New("the coach must be an active member of the gym")
	errClientRace         = errors.New("client was changed by someone else, reload and retry")
	errConsentRequired    = errors.New("the client must consent to join the gym")
	errClientExists       = errors.New("client already belongs to the gym")
	errQuotaKind          = errors.New("unknown quota kind")
)

// GymClientStore is the part of the gym client repository the service needs.
type GymClientStore interface {
	Insert(ctx context.Context, c *db.GymClient) error
	Get(ctx context.Context, gymID, clientID string) (*db.GymClient, error)
	SetCoach(ctx context.Context, gymID, clientID, prevCoachID, coachID string, at time.Time) (bool, error)
	Leave(ctx context.Context, gymID, clientID string, at time.Time) (bool, error)
	ListByGym(ctx context.Context, gymID string, q db.GymClientQuery) ([]db.GymClientRow, int, error)
	ActiveByClient(ctx context.Context, clientID string) ([]db.GymClient, error)
}

var _ GymClientStore = (*repositories.GymClientRepository)(nil)

// GymClientAudit writes the team audit rows (coach_team_audit).
type GymClientAudit interface {
	InsertAudit(ctx context.Context, e *db.TeamAuditEntry) error
}

// SupervisionLookup lists the coaches a head coach supervises.
type SupervisionLookup interface {
	IDsByHead(ctx context.Context, headID string) ([]string, error)
}

// GymClientListQuery filters the gym client list.
type GymClientListQuery struct {
	Assigned *bool
	CoachID  string
	Q        string
	Sort     string
	Page     int
	Size     int
}

// DispatchResult is the answer of assign and unassign. CoachID is "" after an unassign; AssignedAt is nil then.
type DispatchResult struct {
	ClientID        string     `json:"client_id"`
	CoachID         string     `json:"coach_id"`
	PreviousCoachID string     `json:"previous_coach_id"`
	AssignedAt      *time.Time `json:"assigned_at"`
}

// JoinGymClientInput is a client becoming a gym client. Consent is the client's own agreement (redeeming a gym
// invite); CoachID is set when a coach created the link and "" puts the client in the pool.
type JoinGymClientInput struct {
	GymID    string
	ClientID string
	CoachID  string
	InviteID string
	Consent  bool
}

// GymClientService manages gym client relationships (CLIENT_GYM_RELATIONS). A gym client is separate from a personal
// coach_clients link: the gym never sees or reassigns personal clients, and a client may hold both.
type GymClientService interface {
	// ListClients: client.view_all sees every gym client (pool included); others see their own and supervised ones.
	ListClients(ctx context.Context, callerID, gymID string, q GymClientListQuery) ([]db.GymClientRow, int, *models.ApiError)
	// Assign sets or replaces the coach of a gym client (client.assign). The previous coach loses access at once;
	// chat conversations are left open.
	Assign(ctx context.Context, callerID, gymID, clientID, coachID string) (*DispatchResult, *models.ApiError)
	// Unassign returns a gym client to the pool (client.unassign).
	Unassign(ctx context.Context, callerID, gymID, clientID string) (*DispatchResult, *models.ApiError)
	// Remove ends the gym relationship (owner only).
	Remove(ctx context.Context, callerID, gymID, clientID string) *models.ApiError
	// Join creates the relationship after the client consented. It checks the gym quota and ignores personal links.
	Join(ctx context.Context, in JoinGymClientInput) (*db.GymClient, *models.ApiError)
	// Relationships answers how the actor reaches a client (internal, tracker/WMS data context): the personal link
	// and every gym where the actor may see the client as gym context. No relation is an empty answer, not an error.
	Relationships(ctx context.Context, actorID, clientID string) (*ClientRelationships, *models.ApiError)
}

// GymRelation is one gym through which the actor reaches the client.
type GymRelation struct {
	GymID           string `json:"gym_id"`
	ActorRole       string `json:"actor_role"`
	AssignedToActor bool   `json:"assigned_to_actor"`
}

// ClientRelationships is the answer of Relationships.
type ClientRelationships struct {
	Personal bool          `json:"personal"`
	Gyms     []GymRelation `json:"gyms"`
}

type gymClientService struct {
	authz   Authorizer
	members GymMembershipReader
	store   GymClientStore
	team    SupervisionLookup
	links   CoachLinkStore
	audit   GymClientAudit
	quota   QuotaGuard
	inval   AuthzInvalidator
	bump    AuthzVersionBumper
	enabled bool
}

// GymClientDeps groups the collaborators of the service (constructor injection).
type GymClientDeps struct {
	Authz   Authorizer
	Members GymMembershipReader
	Store   GymClientStore
	Team    SupervisionLookup
	Links   CoachLinkStore // personal coach_clients lookup; nil reports no personal link
	Audit   GymClientAudit
	Quota   QuotaGuard
	Inval   AuthzInvalidator   // optional
	Bump    AuthzVersionBumper // optional
}

// NewGymClientService wires the service; enabled=false (flag off) answers 404 everywhere.
func NewGymClientService(d GymClientDeps, enabled bool) GymClientService {
	return &gymClientService{
		authz: d.Authz, members: d.Members, store: d.Store, team: d.Team, links: d.Links, audit: d.Audit,
		quota: d.Quota, inval: d.Inval, bump: d.Bump, enabled: enabled,
	}
}

// coachingRoles are the member roles that may hold gym clients. The owner is included on purpose: a gym owner
// may coach (P1), so owners can be assigned clients like head coaches and coaches. Any other role is refused.
var coachingRoles = map[db.GymMemberRole]bool{
	db.GymMemberRoleOwner:     true,
	db.GymMemberRoleHeadCoach: true,
	db.GymMemberRoleCoach:     true,
}

// canCoach reports whether m is an active member of the gym with a coaching role.
func canCoach(m *db.GymMember) bool {
	return m != nil && m.Status == db.GymMemberStatusActive && coachingRoles[m.Role]
}

// clientOps maps a dispatch operation to the capability it needs and the 403 message.
var clientOps = map[string]struct {
	cap    Capability
	denied error
	res    func(gymID, clientID string) Resource
}{
	"assign":   {CapClientAssign, errClientAssignDeny, clientResource},
	"unassign": {CapClientUnassign, errClientUnassignDeny, clientResource},
	"remove": {CapGymManage, errClientRemoveDeny, func(gymID, _ string) Resource {
		return Resource{Type: ResourceGym, ID: gymID, GymID: gymID}
	}},
}

// clientResource is a client in gym context: the gate only lets gym-derived grants through while the client has an
// active relationship with that gym, and personal-context data stays out of reach.
func clientResource(gymID, clientID string) Resource {
	return Resource{Type: ResourceClient, ID: clientID, GymID: gymID, Context: ContextGym}
}

func (s *gymClientService) internal(op string, err error) *models.ApiError {
	zap.L().Error("gym client failure", zap.String("component", "gym_client"), zap.String("op", op), zap.Error(err))
	return teamErr(http.StatusInternalServerError, errInternal)
}

// authorize runs the operation's capability check: 404 for a non-member or a client outside the gym, 403 without
// the capability.
func (s *gymClientService) authorize(ctx context.Context, op, callerID, gymID, clientID string) *models.ApiError {
	spec := clientOps[op]
	d, apiErr := s.authz.Can(ctx, Actor{ID: callerID}, spec.cap, spec.res(gymID, clientID))
	if apiErr != nil {
		return apiErr
	}
	if d.Allowed {
		return nil
	}
	switch d.Reason {
	case ReasonNotMember:
		return teamErr(http.StatusNotFound, errGymNotFound)
	case ReasonClientNotInGym:
		return teamErr(http.StatusNotFound, errClientNotFound)
	}
	return teamErr(http.StatusForbidden, spec.denied)
}

// actorRole maps the caller's membership to the audit actor role.
var actorRoles = map[db.GymMemberRole]string{
	db.GymMemberRoleOwner:     db.TeamActorRoleGymOwner,
	db.GymMemberRoleHeadCoach: db.TeamActorRoleHead,
	db.GymMemberRoleCoach:     db.TeamActorRoleCoach,
}

func (s *gymClientService) actorRole(ctx context.Context, callerID, gymID string) string {
	m, err := s.members.MembershipOf(ctx, callerID, gymID)
	if err != nil || m == nil {
		return ""
	}
	return actorRoles[m.Role]
}

// changed drops cached decisions of the gym and bumps its authz_version.
func (s *gymClientService) changed(ctx context.Context, gymID string) {
	if s.inval != nil {
		s.inval.Invalidate(gymID)
	}
	if s.bump != nil {
		bctx, cancel := detachedContext(ctx)
		defer cancel()
		if _, err := s.bump.BumpAuthzVersion(bctx, gymID); err != nil {
			zap.L().Error("authz version bump failed", zap.String("component", "gym_client"), zap.String("gym_id", gymID), zap.Error(err))
		}
	}
}

// record writes the audit row; the change already happened, so a failure is logged and not surfaced.
func (s *gymClientService) record(ctx context.Context, e *db.TeamAuditEntry) {
	if s.audit == nil {
		return
	}
	if err := s.audit.InsertAudit(ctx, e); err != nil {
		zap.L().Error("gym client audit write failed", zap.String("component", "gym_client"),
			zap.String("action", e.Action), zap.String("gym_id", e.GymID), zap.Error(err))
	}
}

func (s *gymClientService) ListClients(ctx context.Context, callerID, gymID string, q GymClientListQuery) ([]db.GymClientRow, int, *models.ApiError) {
	if !s.enabled {
		return nil, 0, teamErr(http.StatusNotFound, errClientsDisabled)
	}
	d, apiErr := s.authz.Can(ctx, Actor{ID: callerID}, CapClientViewAll, Resource{Type: ResourceGym, ID: gymID, GymID: gymID})
	if apiErr != nil {
		return nil, 0, apiErr
	}
	if d.Reason == ReasonNotMember {
		return nil, 0, teamErr(http.StatusNotFound, errGymNotFound)
	}
	query := db.GymClientQuery{Assigned: q.Assigned, CoachID: q.CoachID, Q: q.Q, Sort: q.Sort, Page: q.Page, Size: q.Size}
	if r := []rune(query.Q); len(r) > maxClientSearchLen {
		query.Q = string(r[:maxClientSearchLen])
	}
	if query.Size < 1 || query.Size > maxClientPageSize {
		query.Size = 20
	}
	if !d.Allowed {
		// Without client.view_all: his own gym clients and those of the coaches he supervises, never the pool.
		ctx, cancel := context.WithTimeout(ctx, authzTimeout)
		defer cancel()
		supervised, err := s.team.IDsByHead(ctx, callerID)
		if err != nil {
			return nil, 0, s.internal("ListClients.scope", err)
		}
		query.CoachIDs = append([]string{callerID}, supervised...)
	}
	ctx, cancel := context.WithTimeout(ctx, authzTimeout)
	defer cancel()
	rows, total, err := s.store.ListByGym(ctx, gymID, query)
	if err != nil {
		return nil, 0, s.internal("ListClients", err)
	}
	return rows, total, nil
}

func (s *gymClientService) Assign(ctx context.Context, callerID, gymID, clientID, coachID string) (*DispatchResult, *models.ApiError) {
	if !s.enabled {
		return nil, teamErr(http.StatusNotFound, errClientsDisabled)
	}
	if clientID == "" {
		return nil, teamErr(http.StatusBadRequest, errClientRequired)
	}
	if coachID == "" {
		return nil, teamErr(http.StatusBadRequest, errCoachRequired)
	}
	if apiErr := s.authorize(ctx, "assign", callerID, gymID, clientID); apiErr != nil {
		return nil, apiErr
	}
	ctx, cancel := context.WithTimeout(ctx, authzTimeout)
	defer cancel()
	row, err := s.store.Get(ctx, gymID, clientID)
	if err != nil {
		return nil, s.internal("Assign.get", err)
	}
	if row == nil {
		return nil, teamErr(http.StatusNotFound, errClientNotFound)
	}
	target, err := s.members.MembershipOf(ctx, coachID, gymID)
	if err != nil {
		return nil, s.internal("Assign.coach", err)
	}
	if !canCoach(target) {
		return nil, teamErr(http.StatusBadRequest, errCoachNotInGym)
	}
	prev := row.CoachID
	if prev == coachID {
		return &DispatchResult{ClientID: clientID, CoachID: coachID, PreviousCoachID: prev, AssignedAt: row.AssignedAt}, nil
	}
	now := time.Now()
	ok, err := s.store.SetCoach(ctx, gymID, clientID, prev, coachID, now)
	if err != nil {
		return nil, s.internal("Assign.set", err)
	}
	if !ok {
		return nil, teamErr(http.StatusConflict, errClientRace)
	}
	// The target may have left the gym between the check and the write: re-verify and put the client back.
	if again, err := s.members.MembershipOf(ctx, coachID, gymID); err != nil || !canCoach(again) {
		if _, cerr := s.store.SetCoach(ctx, gymID, clientID, coachID, prev, now); cerr != nil {
			zap.L().Error("gym client assign compensation failed", zap.String("component", "gym_client"),
				zap.String("gym_id", gymID), zap.Error(cerr))
		}
		if err != nil {
			return nil, s.internal("Assign.recheck", err)
		}
		return nil, teamErr(http.StatusConflict, errCoachNotInGym)
	}
	action := db.TeamActionClientAssign
	if prev != "" {
		action = db.TeamActionClientReassign
	}
	s.record(ctx, &db.TeamAuditEntry{
		Action: action, ActorID: callerID, ActorRole: s.actorRole(ctx, callerID, gymID),
		CoachID: coachID, PrevCoachID: prev, ClientID: clientID, GymID: gymID,
	})
	s.changed(ctx, gymID)
	return &DispatchResult{ClientID: clientID, CoachID: coachID, PreviousCoachID: prev, AssignedAt: &now}, nil
}

func (s *gymClientService) Unassign(ctx context.Context, callerID, gymID, clientID string) (*DispatchResult, *models.ApiError) {
	if !s.enabled {
		return nil, teamErr(http.StatusNotFound, errClientsDisabled)
	}
	if clientID == "" {
		return nil, teamErr(http.StatusBadRequest, errClientRequired)
	}
	if apiErr := s.authorize(ctx, "unassign", callerID, gymID, clientID); apiErr != nil {
		return nil, apiErr
	}
	ctx, cancel := context.WithTimeout(ctx, authzTimeout)
	defer cancel()
	row, err := s.store.Get(ctx, gymID, clientID)
	if err != nil {
		return nil, s.internal("Unassign.get", err)
	}
	if row == nil {
		return nil, teamErr(http.StatusNotFound, errClientNotFound)
	}
	prev := row.CoachID
	if prev == "" {
		return &DispatchResult{ClientID: clientID}, nil
	}
	ok, err := s.store.SetCoach(ctx, gymID, clientID, prev, "", time.Now())
	if err != nil {
		return nil, s.internal("Unassign.set", err)
	}
	if !ok {
		return nil, teamErr(http.StatusConflict, errClientRace)
	}
	s.record(ctx, &db.TeamAuditEntry{
		Action: db.TeamActionClientUnassign, ActorID: callerID, ActorRole: s.actorRole(ctx, callerID, gymID),
		PrevCoachID: prev, ClientID: clientID, GymID: gymID,
	})
	s.changed(ctx, gymID)
	return &DispatchResult{ClientID: clientID, PreviousCoachID: prev}, nil
}

func (s *gymClientService) Remove(ctx context.Context, callerID, gymID, clientID string) *models.ApiError {
	if !s.enabled {
		return teamErr(http.StatusNotFound, errClientsDisabled)
	}
	if clientID == "" {
		return teamErr(http.StatusBadRequest, errClientRequired)
	}
	if apiErr := s.authorize(ctx, "remove", callerID, gymID, clientID); apiErr != nil {
		return apiErr
	}
	ctx, cancel := context.WithTimeout(ctx, authzTimeout)
	defer cancel()
	row, err := s.store.Get(ctx, gymID, clientID)
	if err != nil {
		return s.internal("Remove.get", err)
	}
	if row == nil {
		return teamErr(http.StatusNotFound, errClientNotFound)
	}
	ok, err := s.store.Leave(ctx, gymID, clientID, time.Now())
	if err != nil {
		return s.internal("Remove.leave", err)
	}
	if !ok {
		return teamErr(http.StatusNotFound, errClientNotFound)
	}
	s.record(ctx, &db.TeamAuditEntry{
		Action: db.TeamActionClientLeaveGym, ActorID: callerID, ActorRole: s.actorRole(ctx, callerID, gymID),
		PrevCoachID: row.CoachID, ClientID: clientID, GymID: gymID,
	})
	s.changed(ctx, gymID)
	return nil
}

func (s *gymClientService) Join(ctx context.Context, in JoinGymClientInput) (*db.GymClient, *models.ApiError) {
	if !s.enabled {
		return nil, teamErr(http.StatusNotFound, errClientsDisabled)
	}
	if in.GymID == "" || in.ClientID == "" {
		return nil, teamErr(http.StatusBadRequest, errClientRequired)
	}
	if !in.Consent {
		return nil, teamErr(http.StatusBadRequest, errConsentRequired)
	}
	ctx, cancel := context.WithTimeout(ctx, authzTimeout)
	defer cancel()
	if in.CoachID != "" {
		m, err := s.members.MembershipOf(ctx, in.CoachID, in.GymID)
		if err != nil {
			return nil, s.internal("Join.coach", err)
		}
		if !canCoach(m) {
			return nil, teamErr(http.StatusBadRequest, errCoachNotInGym)
		}
	}
	// Only the gym quota counts a gym client: the coach's personal max_clients is never consulted, and a personal
	// link of the same client is neither required nor checked (dual affiliation).
	if apiErr := s.quota.Check(ctx, QuotaKindGym, in.CoachID, in.GymID); apiErr != nil {
		return nil, apiErr
	}
	now := time.Now()
	row := &db.GymClient{
		GymID: in.GymID, ClientID: in.ClientID, CoachID: in.CoachID, Status: db.GymClientStatusActive,
		JoinedAt: now, InviteID: in.InviteID, ConsentAt: now,
	}
	if in.CoachID != "" {
		row.AssignedAt = &now
	}
	if err := s.store.Insert(ctx, row); err != nil {
		if errors.Is(err, repositories.ErrGymClientExists) {
			return nil, teamErr(http.StatusConflict, errClientExists)
		}
		return nil, s.internal("Join.insert", err)
	}
	s.record(ctx, &db.TeamAuditEntry{
		Action: db.TeamActionClientJoinGym, ActorID: in.ClientID, ActorRole: db.TeamActorRoleClient,
		CoachID: in.CoachID, ClientID: in.ClientID, GymID: in.GymID,
	})
	s.changed(ctx, in.GymID)
	return row, nil
}

// gymClientScopeSource reads the relationship set of a client for the authorizer: the active gym_clients rows.
// The personal coach is deliberately not part of it: no gym-derived grant may depend on a personal link.
type gymClientScopeSource struct {
	store interface {
		ActiveByClient(ctx context.Context, clientID string) ([]db.GymClient, error)
	}
}

// NewClientScopeSource builds the ClientScopeSource of the resource resolver from gym_clients.
func NewClientScopeSource(store interface {
	ActiveByClient(ctx context.Context, clientID string) ([]db.GymClient, error)
}) ClientScopeSource {
	return &gymClientScopeSource{store: store}
}

func (g *gymClientScopeSource) ClientScope(ctx context.Context, clientID string) (*ResourceScope, error) {
	rows, err := g.store.ActiveByClient(ctx, clientID)
	if err != nil {
		return nil, err
	}
	scope := &ResourceScope{Gyms: make([]ScopeGym, 0, len(rows))}
	for _, r := range rows {
		scope.Gyms = append(scope.Gyms, ScopeGym{GymID: r.GymID, CoachID: r.CoachID})
	}
	return scope, nil
}

func (s *gymClientService) Relationships(ctx context.Context, actorID, clientID string) (*ClientRelationships, *models.ApiError) {
	if actorID == "" || clientID == "" {
		return nil, teamErr(http.StatusBadRequest, errClientRequired)
	}
	out := &ClientRelationships{Gyms: []GymRelation{}}
	ctx, cancel := context.WithTimeout(ctx, authzTimeout)
	defer cancel()
	if s.links != nil {
		linked, err := s.links.LinkedClientIDs(ctx, actorID, []string{clientID})
		if err != nil {
			return nil, s.internal("Relationships.personal", err)
		}
		for _, id := range linked {
			if id == clientID {
				out.Personal = true
			}
		}
	}
	if !s.enabled {
		return out, nil
	}
	rows, err := s.store.ActiveByClient(ctx, clientID)
	if err != nil {
		return nil, s.internal("Relationships.rows", err)
	}
	if len(rows) == 0 {
		return out, nil
	}
	memberships, err := s.members.MembershipsOf(ctx, actorID)
	if err != nil {
		return nil, s.internal("Relationships.members", err)
	}
	roles := map[string]db.GymMemberRole{}
	for i := range memberships {
		if canCoach(&memberships[i]) {
			roles[memberships[i].GymID] = memberships[i].Role
		}
	}
	for _, row := range rows {
		role, ok := roles[row.GymID]
		if !ok {
			continue
		}
		assigned := row.CoachID == actorID
		// The owner and head coaches see every client row of their gym (a head coach therefore also covers the
		// coaches he supervises); a plain coach sees only the rows assigned to him.
		if !assigned && role != db.GymMemberRoleOwner && role != db.GymMemberRoleHeadCoach {
			continue
		}
		out.Gyms = append(out.Gyms, GymRelation{GymID: row.GymID, ActorRole: string(role), AssignedToActor: assigned})
	}
	return out, nil
}
