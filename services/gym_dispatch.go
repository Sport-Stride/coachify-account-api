package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/pkg/tracker"
	"coachify-account-api/repositories"
)

// Transfer reasons sent to the tracker.
const (
	dispatchReasonAssign   = "gym_assign"
	dispatchReasonReassign = "gym_reassign"
	dispatchReasonUnassign = "gym_unassign"
	dispatchReasonCoachGo  = "coach_left"
)

// Retry and budget settings of the best-effort side effects.
const (
	dispatchAttempts   = 3
	dispatchBudgetBase = 60 * time.Second
	dispatchBudgetPer  = 5 * time.Second
	dispatchBudgetCap  = 10 * time.Minute
)

// dispatchBackoff is the wait after a failed attempt (the tracker endpoints and the release are idempotent).
var dispatchBackoff = []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second}

// dispatchTrackerWorkers bounds the parallel tracker calls of a coach departure.
const dispatchTrackerWorkers = 4

// errDispatchNeedsRelations is the startup error when GYM_DISPATCH_ENABLED runs without CLIENT_GYM_RELATIONS.
var errDispatchNeedsRelations = errors.New("GYM_DISPATCH_ENABLED requires CLIENT_GYM_RELATIONS=true")

// errDispatchNeedsReplicaSet is the startup error when the database cannot run multi-document transactions.
var errDispatchNeedsReplicaSet = errors.New("GYM_DISPATCH_ENABLED requires a MongoDB replica set (or mongos): the deployment does not support transactions, refusing to run gym dispatch non-atomically")

// TransactionProbe tells whether the database supports multi-document transactions.
type TransactionProbe interface {
	SupportsTransactions(ctx context.Context) (bool, error)
}

// CheckDispatchReady is the fail-fast startup check of the dispatch flag: the relations flag must be on and the
// database must support transactions. A probe failure also refuses to start (it cannot be proven safe).
func CheckDispatchReady(ctx context.Context, probe TransactionProbe, relationsEnabled bool) error {
	if !relationsEnabled {
		return errDispatchNeedsRelations
	}
	ok, err := probe.SupportsTransactions(ctx)
	if err != nil {
		return fmt.Errorf("GYM_DISPATCH_ENABLED: cannot detect transaction support: %w", err)
	}
	if !ok {
		return errDispatchNeedsReplicaSet
	}
	return nil
}

// GymDispatchStore is the transactional part of the repository the dispatch service needs.
type GymDispatchStore interface {
	AssignTx(ctx context.Context, gymID, clientID, prevCoachID, coachID string, at time.Time, audit *db.TeamAuditEntry) (bool, error)
	LeaveTx(ctx context.Context, gymID, clientID string, at time.Time, audit *db.TeamAuditEntry) (bool, error)
	ReleaseCoachTx(ctx context.Context, coachIDs []string, gymID string, at time.Time, audit func(row db.GymClient) *db.TeamAuditEntry) ([]db.GymClient, error)
}

var _ GymDispatchStore = (*repositories.GymDispatchRepository)(nil)

// AssignmentTransferer moves or cancels the tracker assignments of a gym client; *tracker.TrackerClient satisfies it.
type AssignmentTransferer interface {
	Transfer(ctx context.Context, gymID, clientID, fromCoachID, toCoachID, reason string) (*tracker.BulkResult, error)
	CancelGym(ctx context.Context, gymID, clientID string, from *time.Time) (*tracker.BulkResult, error)
}

// GymMembershipBilling is the payments seam of a client leaving a gym (decision 17): the gym membership plan
// subscription ends at the period end. With GYM_MEMBERSHIP_PLANS_ENABLED it is the payments-api cancel call
// (NewPaymentsMembershipBilling); otherwise the default does nothing.
//
// Failure semantics: a failure never fails the departure (the gym_clients change is already committed). The call is
// retried, then logged as "gym dispatch billing cancel failed, replay needed" with gym_id and client_id. Operator
// replay: POST {PAYMENTS}/internal/gym-memberships/cancel (x-api-key) with {"gym_id","user_id":client_id}; it is
// idempotent. Until replayed the client keeps paying the gym plan to the next renewal.
type GymMembershipBilling interface {
	CancelAtPeriodEnd(ctx context.Context, gymID, clientID string) error
}

type noopMembershipBilling struct{}

func (noopMembershipBilling) CancelAtPeriodEnd(context.Context, string, string) error { return nil }

// DispatchMailer sends the assignment notice; *notification.NotificationClient satisfies it.
type DispatchMailer interface {
	SendGymAssignmentEmail(ctx context.Context, email, subject, message string) error
}

// DispatchUsers resolves the email of a user.
type DispatchUsers interface {
	GetUserByExternalIdUpdate(ctx context.Context, userID string) (*db.User, *models.ApiError)
}

// GymClientLeaver lets a client leave a gym (POST /me/gyms/:gym_id/leave).
type GymClientLeaver interface {
	// LeaveGym ends the caller's gym relationship. Idempotent: no active relationship is a success.
	LeaveGym(ctx context.Context, clientID, gymID string) *models.ApiError
}

// CoachRelease describes coaches that stopped being gym members. GymID "" covers every gym of the coaches.
// ActorID and ActorRole (and HeadCoachID when known) are written to the audit rows.
type CoachRelease struct {
	GymID       string
	CoachIDs    []string
	ActorID     string
	ActorRole   string
	HeadCoachID string
}

// CoachDepartures is the hook of the flows where coaches stop being gym members: their gym clients go to the pool.
// It never fails the caller; problems are logged. It is idempotent: a repeat of the departure (or of an unassign)
// finishes a release that failed.
type CoachDepartures interface {
	ReleaseCoachClients(ctx context.Context, rel CoachRelease)
}

// GymDispatchDeps are the collaborators of the dispatch service beyond the gym client ones.
type GymDispatchDeps struct {
	Repo    GymDispatchStore
	Tracker AssignmentTransferer // optional
	Chat    ChatOpener           // optional
	Mailer  DispatchMailer       // optional
	Users   DispatchUsers        // optional (needed with Mailer)
	Billing GymMembershipBilling // optional, defaults to a no-op
	Spawn   func(func())         // optional, defaults to a goroutine
	Sleep   func(context.Context, time.Duration) // optional, defaults to a context-aware sleep (tests inject a fake)
}

// GymDispatch is the GymClientService used while GYM_DISPATCH_ENABLED is on: assign, unassign and remove commit
// the gym_clients change and its audit row in one Mongo transaction, then run best-effort side effects (chat,
// notice, authz bump, tracker). The other methods are the P4 service's.
type GymDispatch struct {
	*gymClientService
	repo    GymDispatchStore
	tracker AssignmentTransferer
	chat    ChatOpener
	mailer  DispatchMailer
	users   DispatchUsers
	billing GymMembershipBilling
	spawn   func(func())
	sleep   func(context.Context, time.Duration)
}

var (
	_ GymClientService = (*GymDispatch)(nil)
	_ GymClientLeaver  = (*GymDispatch)(nil)
	_ CoachDepartures  = (*GymDispatch)(nil)
)

// NewGymDispatch builds the dispatch service on top of the P4 gym client service (always enabled: the caller only
// builds it when both CLIENT_GYM_RELATIONS and GYM_DISPATCH_ENABLED are on).
func NewGymDispatch(cd GymClientDeps, d GymDispatchDeps) *GymDispatch {
	base := NewGymClientService(cd, true).(*gymClientService)
	if d.Billing == nil {
		d.Billing = noopMembershipBilling{}
	}
	if d.Spawn == nil {
		d.Spawn = func(f func()) { go f() }
	}
	if d.Sleep == nil {
		d.Sleep = func(ctx context.Context, wait time.Duration) {
			t := time.NewTimer(wait)
			defer t.Stop()
			select {
			case <-t.C:
			case <-ctx.Done():
			}
		}
	}
	return &GymDispatch{
		sleep: d.Sleep, gymClientService: base, repo: d.Repo, tracker: d.Tracker, chat: d.Chat, mailer: d.Mailer,
		users: d.Users, billing: d.Billing, spawn: d.Spawn,
	}
}

// background runs f off the request path with a context that outlives the request. The budget grows with the
// work: 60s plus 5s per client row, capped at 10 minutes. A panic is logged and never takes the process down.
func (s *GymDispatch) background(ctx context.Context, work int, f func(ctx context.Context)) {
	detached := context.WithoutCancel(ctx)
	budget := dispatchBudgetBase + time.Duration(work)*dispatchBudgetPer
	if budget > dispatchBudgetCap {
		budget = dispatchBudgetCap
	}
	s.spawn(func() {
		defer recoverLog("background")
		ctx, cancel := context.WithTimeout(detached, budget)
		defer cancel()
		f(ctx)
	})
}

// recoverLog swallows a panic of a side effect and logs it (no payload, no PII).
func recoverLog(where string) {
	if r := recover(); r != nil {
		zap.L().Error("gym dispatch panic recovered", zap.String("component", "gym_dispatch"),
			zap.String("where", where), zap.Any("panic", r))
	}
}

// retry runs op up to dispatchAttempts times with backoff between failed attempts.
func (s *GymDispatch) retry(ctx context.Context, op func() error) error {
	var err error
	for i := 0; i < dispatchAttempts; i++ {
		if err = op(); err == nil {
			return nil
		}
		if i < dispatchAttempts-1 {
			s.sleep(ctx, dispatchBackoff[i])
			if ctx.Err() != nil {
				return err
			}
		}
	}
	return err
}

func (s *GymDispatch) auditEntry(ctx context.Context, action, callerID, gymID, clientID, prev, coachID string) *db.TeamAuditEntry {
	return &db.TeamAuditEntry{
		Action: action, ActorID: callerID, ActorRole: s.actorRole(ctx, callerID, gymID),
		CoachID: coachID, PrevCoachID: prev, ClientID: clientID, GymID: gymID,
	}
}

// transfer asks the tracker to move the gym-context assignments, retrying a failure. The endpoint is idempotent.
// After the last failure the ids are logged for an operator replay; the commit stays.
func (s *GymDispatch) transfer(ctx context.Context, gymID, clientID, from, to, reason string) error {
	if s.tracker == nil {
		return nil
	}
	err := s.retry(ctx, func() error {
		_, err := s.tracker.Transfer(ctx, gymID, clientID, from, to, reason)
		return err
	})
	if err != nil {
		zap.L().Error("gym dispatch tracker transfer failed, replay needed", zap.String("component", "gym_dispatch"),
			zap.String("gym_id", gymID), zap.String("client_id", clientID), zap.String("reason", reason), zap.Error(err))
	}
	return err
}

// cancelGym asks the tracker to cancel the future gym assignments, with the same retry and replay log.
func (s *GymDispatch) cancelGym(ctx context.Context, gymID, clientID string, from *time.Time) {
	if s.tracker == nil {
		return
	}
	err := s.retry(ctx, func() error {
		_, err := s.tracker.CancelGym(ctx, gymID, clientID, from)
		return err
	})
	if err != nil {
		zap.L().Error("gym dispatch tracker cancel failed, replay needed", zap.String("component", "gym_dispatch"),
			zap.String("gym_id", gymID), zap.String("client_id", clientID), zap.Error(err))
	}
}

// notify mails a user; the email is looked up here and never logged.
func (s *GymDispatch) notify(ctx context.Context, userID, subject, message string) {
	if s.mailer == nil || s.users == nil || userID == "" {
		return
	}
	u, apiErr := s.users.GetUserByExternalIdUpdate(ctx, userID)
	if apiErr != nil || u == nil || u.UserEmail == "" {
		return
	}
	if err := s.mailer.SendGymAssignmentEmail(ctx, u.UserEmail, subject, message); err != nil {
		zap.L().Warn("gym dispatch notification failed", zap.String("component", "gym_dispatch"), zap.Error(err))
	}
}

func (s *GymDispatch) Assign(ctx context.Context, callerID, gymID, clientID, coachID string) (*DispatchResult, *models.ApiError) {
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
		return nil, s.internal("Dispatch.Assign.get", err)
	}
	if row == nil {
		return nil, teamErr(http.StatusNotFound, errClientNotFound)
	}
	target, err := s.members.MembershipOf(ctx, coachID, gymID)
	if err != nil {
		return nil, s.internal("Dispatch.Assign.coach", err)
	}
	if !canCoach(target) {
		return nil, teamErr(http.StatusBadRequest, errCoachNotInGym)
	}
	prev := row.CoachID
	if prev == coachID {
		return &DispatchResult{ClientID: clientID, CoachID: coachID, PreviousCoachID: prev, AssignedAt: row.AssignedAt}, nil
	}
	if apiErr := s.requireCoachSeat(ctx, gymID, coachID); apiErr != nil {
		return nil, apiErr
	}
	action := db.TeamActionClientAssign
	reason := dispatchReasonAssign
	if prev != "" {
		action, reason = db.TeamActionClientReassign, dispatchReasonReassign
	}
	now := time.Now()
	ok, err := s.repo.AssignTx(ctx, gymID, clientID, prev, coachID, now,
		s.auditEntry(ctx, action, callerID, gymID, clientID, prev, coachID))
	if err != nil {
		return nil, s.internal("Dispatch.Assign.tx", err)
	}
	if !ok {
		return nil, teamErr(http.StatusConflict, errClientRace)
	}
	// The target may have left the gym between the check and the commit: put the client back (a second
	// transaction, so the compensation is audited too).
	if again, err := s.members.MembershipOf(ctx, coachID, gymID); err != nil || !canCoach(again) {
		back := s.auditEntry(ctx, db.TeamActionClientReassign, callerID, gymID, clientID, coachID, prev)
		if _, cerr := s.repo.AssignTx(ctx, gymID, clientID, coachID, prev, now, back); cerr != nil {
			zap.L().Error("gym dispatch assign compensation failed", zap.String("component", "gym_dispatch"),
				zap.String("gym_id", gymID), zap.Error(cerr))
		}
		if err != nil {
			return nil, s.internal("Dispatch.Assign.recheck", err)
		}
		return nil, teamErr(http.StatusConflict, errCoachNotInGym)
	}
	s.changed(ctx, gymID)
	s.background(ctx, 1, func(ctx context.Context) {
		if s.chat != nil {
			if apiErr := s.chat.EnsureGymConversation(ctx, coachID, clientID, gymID); apiErr != nil {
				zap.L().Warn("gym dispatch conversation failed", zap.String("component", "gym_dispatch"),
					zap.String("gym_id", gymID), zap.Error(apiErr.Error))
			}
		}
		_ = s.transfer(ctx, gymID, clientID, prev, coachID, reason)
		s.notify(ctx, coachID, "New gym client", "A client of your gym has been assigned to you on Coachify.")
		s.notify(ctx, clientID, "Your coach changed", "Your coach in your gym has changed on Coachify.")
	})
	return &DispatchResult{ClientID: clientID, CoachID: coachID, PreviousCoachID: prev, AssignedAt: &now}, nil
}

func (s *GymDispatch) Unassign(ctx context.Context, callerID, gymID, clientID string) (*DispatchResult, *models.ApiError) {
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
		return nil, s.internal("Dispatch.Unassign.get", err)
	}
	if row == nil {
		return nil, teamErr(http.StatusNotFound, errClientNotFound)
	}
	prev := row.CoachID
	if prev == "" {
		return &DispatchResult{ClientID: clientID}, nil
	}
	ok, err := s.repo.AssignTx(ctx, gymID, clientID, prev, "", time.Now(),
		s.auditEntry(ctx, db.TeamActionClientUnassign, callerID, gymID, clientID, prev, ""))
	if err != nil {
		return nil, s.internal("Dispatch.Unassign.tx", err)
	}
	if !ok {
		return nil, teamErr(http.StatusConflict, errClientRace)
	}
	s.changed(ctx, gymID)
	s.background(ctx, 1, func(ctx context.Context) {
		_ = s.transfer(ctx, gymID, clientID, prev, "", dispatchReasonUnassign)
	})
	return &DispatchResult{ClientID: clientID, PreviousCoachID: prev}, nil
}

func (s *GymDispatch) Remove(ctx context.Context, callerID, gymID, clientID string) *models.ApiError {
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
		return s.internal("Dispatch.Remove.get", err)
	}
	if row == nil {
		return teamErr(http.StatusNotFound, errClientNotFound)
	}
	if apiErr := s.endRelationship(ctx, callerID, row); apiErr != nil {
		return apiErr
	}
	return nil
}

func (s *GymDispatch) LeaveGym(ctx context.Context, clientID, gymID string) *models.ApiError {
	if clientID == "" || gymID == "" {
		return teamErr(http.StatusBadRequest, errClientRequired)
	}
	ctx, cancel := context.WithTimeout(ctx, authzTimeout)
	defer cancel()
	row, err := s.store.Get(ctx, gymID, clientID)
	if err != nil {
		return s.internal("Dispatch.Leave.get", err)
	}
	if row == nil {
		return nil // already left (or never joined): idempotent
	}
	return s.endRelationship(ctx, clientID, row)
}

// endRelationship marks the gym relationship left with its audit row in one transaction (a lost race means someone
// else ended it first, a success), then cancels the future gym assignments and the gym membership billing. The
// personal coach_clients link is never touched.
func (s *GymDispatch) endRelationship(ctx context.Context, actorID string, row *db.GymClient) *models.ApiError {
	now := time.Now()
	audit := s.auditEntry(ctx, db.TeamActionClientLeaveGym, actorID, row.GymID, row.ClientID, row.CoachID, "")
	if actorID == row.ClientID {
		audit.ActorRole = db.TeamActorRoleClient
	}
	ok, err := s.repo.LeaveTx(ctx, row.GymID, row.ClientID, now, audit)
	if err != nil {
		return s.internal("Dispatch.Leave.tx", err)
	}
	if !ok {
		return nil
	}
	s.changed(ctx, row.GymID)
	gymID, clientID := row.GymID, row.ClientID
	s.background(ctx, 1, func(ctx context.Context) {
		s.cancelGym(ctx, gymID, clientID, &now)
		err := s.retry(ctx, func() error { return s.billing.CancelAtPeriodEnd(ctx, gymID, clientID) })
		if err != nil {
			zap.L().Error("gym dispatch billing cancel failed, replay needed", zap.String("component", "gym_dispatch"),
				zap.String("gym_id", gymID), zap.String("client_id", clientID), zap.Error(err))
		}
	})
	return nil
}

// ReleaseCoachClients returns the gym clients of departing coaches to the pool in one transaction (retried, then
// logged with the ids; the caller still succeeds and a repeat of the departure finishes it), then moves their tracker
// assignments to the pool with a few parallel calls. The coaches personal clients are untouched.
func (s *GymDispatch) ReleaseCoachClients(ctx context.Context, rel CoachRelease) {
	if len(rel.CoachIDs) == 0 {
		return
	}
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), authzTimeout)
	defer cancel()
	var rows []db.GymClient
	err := s.retry(dctx, func() error {
		var err error
		rows, err = s.repo.ReleaseCoachTx(dctx, rel.CoachIDs, rel.GymID, time.Now(), func(row db.GymClient) *db.TeamAuditEntry {
			return &db.TeamAuditEntry{
				Action: db.TeamActionClientUnassign, ActorID: rel.ActorID, ActorRole: rel.ActorRole,
				HeadCoachID: rel.HeadCoachID, PrevCoachID: row.CoachID, ClientID: row.ClientID, GymID: row.GymID,
			}
		})
		return err
	})
	if err != nil {
		zap.L().Error("gym dispatch coach release failed, repeat the departure to finish", zap.String("component", "gym_dispatch"),
			zap.String("gym_id", rel.GymID), zap.Strings("coach_ids", rel.CoachIDs), zap.Error(err))
		return
	}
	if len(rows) == 0 {
		return
	}
	gyms := map[string]bool{}
	for _, r := range rows {
		if !gyms[r.GymID] {
			gyms[r.GymID] = true
			s.changed(dctx, r.GymID)
		}
	}
	s.background(ctx, len(rows), func(ctx context.Context) {
		jobs := make(chan db.GymClient)
		var wg sync.WaitGroup
		var failed int32
		for i := 0; i < dispatchTrackerWorkers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				// A panic is recovered per job so the worker keeps draining the queue.
				for r := range jobs {
					func() {
						ok := false
						defer func() {
							if !ok {
								atomic.AddInt32(&failed, 1)
							}
						}()
						defer recoverLog("release worker")
						ok = s.transfer(ctx, r.GymID, r.ClientID, r.CoachID, "", dispatchReasonCoachGo) == nil
					}()
				}
			}()
		}
		for _, r := range rows {
			jobs <- r
		}
		close(jobs)
		wg.Wait()
		if n := atomic.LoadInt32(&failed); n > 0 {
			zap.L().Error("gym dispatch coach release: tracker transfers failed", zap.String("component", "gym_dispatch"),
				zap.Int32("failed", n), zap.Int("total", len(rows)))
		}
	})
}
