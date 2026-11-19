//go:build integration

package services_test

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
	"coachify-account-api/services"
)

// flatITUsers answers the gym service user lookups: every known id is an Active coach with the given email.
type flatITUsers struct{ coaches map[string]string }

func (u flatITUsers) GetUserByExternalIdUpdate(_ context.Context, id string) (*db.User, *models.ApiError) {
	if email, ok := u.coaches[id]; ok {
		return &db.User{ExternalID: id, UserRole: "coach", UserStatus: db.Active, UserEmail: email}, nil
	}
	return &db.User{ExternalID: id, UserRole: db.RoleGymOwner, UserStatus: db.Active}, nil
}

func (flatITUsers) SetHeadCoach(context.Context, string, bool, string) error { return nil }

type flatITEnv struct {
	*memberEnv
	policies *repositories.GymPolicyRepository
	gym      services.GymService
	members  services.GymMembersService
	gymID    string
}

// newFlatITEnv builds a gym with owner o1 and head coach h1 over real repositories, with the flat hierarchy on.
func newFlatITEnv(t *testing.T) *flatITEnv {
	t.Helper()
	e := newMemberEnv(t)
	policies := repositories.NewGymPolicyRepository(e.database)
	reader := services.NewRepoMembershipReader(e.members)
	caching := services.NewCachingAuthorizer(services.NewChainAuthorizer(reader, policies, nil), time.Minute)
	store := services.NewInvalidatingMemberStore(e.members, caching, policies)

	users := flatITUsers{coaches: map[string]string{"c1": "c1@example.test", "c2": "c2@example.test", "c3": "c3@example.test"}}
	gymSvc := services.NewGymService(users, e.gyms, services.GymSettings{
		ClientURL: "http://client.test", Members: store, Authz: caching, FlatHierarchy: true, Projector: e.team,
	})
	membersSvc := services.NewGymMembersService(caching, store, e.team, true)

	gymID := e.gym(t, "o1")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	now := time.Now()
	require.NoError(t, e.members.UpsertActive(ctx, &db.GymMember{GymID: gymID, UserID: "o1", Role: db.GymMemberRoleOwner,
		Status: db.GymMemberStatusActive, JoinedAt: now, AddedBy: "admin1", Source: db.GymMemberSourceAdmin}))
	require.NoError(t, e.members.UpsertActive(ctx, &db.GymMember{GymID: gymID, UserID: "h1", Role: db.GymMemberRoleHeadCoach,
		Status: db.GymMemberStatusActive, JoinedAt: now, AddedBy: "o1", Source: db.GymMemberSourceAdmin}))
	return &flatITEnv{memberEnv: e, policies: policies, gym: gymSvc, members: membersSvc, gymID: gymID}
}

// joinLinkToken creates an open coach join link as the owner and returns its raw token.
func (e *flatITEnv) joinLinkToken(t *testing.T, supervisor string) (string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, apiErr := e.gym.CreateCoachJoinLink(ctx, "o1", e.gymID, supervisor)
	require.Nil(t, apiErr)
	i := strings.Index(res.Link, "gym_join=")
	require.GreaterOrEqual(t, i, 0)
	return res.InvitationID, res.Link[i+len("gym_join="):]
}

func (e *flatITEnv) invitationStatus(t *testing.T, id string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	inv, err := e.gyms.GetInvitation(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, inv)
	return inv.Status
}

func TestGymFlatIT_CoachJoinsWithoutHeadCoach(t *testing.T) {
	e := newFlatITEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	invID, token := e.joinLinkToken(t, "")

	joined, apiErr := e.gym.AcceptByToken(ctx, "c1", token)
	require.Nil(t, apiErr)
	assert.Equal(t, e.gymID, joined.GymID)

	row, err := e.members.(interface{}), error(nil)
	_ = row
	_ = err
	m, gerr := e.memberEnv.members.Get(ctx, "c1", e.gymID)
	require.NoError(t, gerr)
	require.NotNil(t, m)
	assert.Equal(t, db.GymMemberRoleCoach, m.Role)
	assert.Empty(t, m.SupervisorID)
	assert.True(t, m.CoachSlot)
	assert.Equal(t, db.GymInviteAccepted, e.invitationStatus(t, invID))

	n, err := e.database.Collection("coach_team_members").CountDocuments(ctx, bson.M{"coach_id": "c1"})
	require.NoError(t, err)
	assert.EqualValues(t, 0, n, "no head coach, no legacy team row")

	n, err = e.database.Collection("coach_team_audit").CountDocuments(ctx, bson.M{"gym_id": e.gymID, "action": db.TeamActionMemberJoin, "coach_id": "c1"})
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
}

func TestGymFlatIT_JoinUnderSupervisorProjectsTeam(t *testing.T) {
	e := newFlatITEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, token := e.joinLinkToken(t, "h1")
	_, apiErr := e.gym.AcceptByToken(ctx, "c1", token)
	require.Nil(t, apiErr)

	m, err := e.memberEnv.members.Get(ctx, "c1", e.gymID)
	require.NoError(t, err)
	assert.Equal(t, "h1", m.SupervisorID)
	periods, err := e.team.Periods().ListForParent(ctx, db.PeriodKindTeam, "h1")
	require.NoError(t, err)
	require.Len(t, periods, 1)
	assert.Equal(t, "c1", periods[0].MemberID)
	assert.Nil(t, periods[0].End)
}

func TestGymFlatIT_SecondJoinElsewhereIs409(t *testing.T) {
	e := newFlatITEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, token1 := e.joinLinkToken(t, "")
	_, apiErr := e.gym.AcceptByToken(ctx, "c1", token1)
	require.Nil(t, apiErr)

	// second gym owned by o2
	g2 := e.memberEnv.gym(t, "o2")
	require.NoError(t, e.memberEnv.members.UpsertActive(ctx, &db.GymMember{GymID: g2, UserID: "o2", Role: db.GymMemberRoleOwner,
		Status: db.GymMemberStatusActive, JoinedAt: time.Now(), AddedBy: "admin1", Source: db.GymMemberSourceAdmin}))
	res, apiErr := e.gym.CreateCoachJoinLink(ctx, "o2", g2, "")
	require.Nil(t, apiErr)
	token2 := res.Link[strings.Index(res.Link, "gym_join=")+len("gym_join="):]

	_, apiErr = e.gym.AcceptByToken(ctx, "c1", token2)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusConflict, apiErr.Code)
	assert.Equal(t, db.GymInvitePending, e.invitationStatus(t, res.InvitationID), "failed join leaves the invitation usable")
	assert.EqualValues(t, 1, e.count(t, bson.M{"user_id": "c1", "status": "active"}))
}

func TestGymFlatIT_ConcurrentDoubleJoinOneWins(t *testing.T) {
	e := newFlatITEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	g2 := e.memberEnv.gym(t, "o2")
	require.NoError(t, e.memberEnv.members.UpsertActive(ctx, &db.GymMember{GymID: g2, UserID: "o2", Role: db.GymMemberRoleOwner,
		Status: db.GymMemberStatusActive, JoinedAt: time.Now(), AddedBy: "admin1", Source: db.GymMemberSourceAdmin}))
	invA, tokenA := e.joinLinkToken(t, "")
	resB, apiErr := e.gym.CreateCoachJoinLink(ctx, "o2", g2, "")
	require.Nil(t, apiErr)
	tokenB := resB.Link[strings.Index(resB.Link, "gym_join=")+len("gym_join="):]

	for round := 0; round < 1; round++ {
		var wg sync.WaitGroup
		start := make(chan struct{})
		codes := make([]int, 2)
		for i, tok := range []string{tokenA, tokenB} {
			wg.Add(1)
			go func(i int, tok string) {
				defer wg.Done()
				<-start
				if _, apiErr := e.gym.AcceptByToken(ctx, "c2", tok); apiErr != nil {
					codes[i] = apiErr.Code
				} else {
					codes[i] = http.StatusOK
				}
			}(i, tok)
		}
		close(start)
		wg.Wait()

		wins, losses := 0, 0
		for _, c := range codes {
			switch c {
			case http.StatusOK:
				wins++
			case http.StatusConflict:
				losses++
			default:
				t.Fatalf("unexpected status %d (codes %v)", c, codes)
			}
		}
		assert.Equal(t, 1, wins, "codes %v", codes)
		assert.Equal(t, 1, losses, "codes %v", codes)
	}

	assert.EqualValues(t, 1, e.count(t, bson.M{"user_id": "c2", "status": "active"}), "exactly one active membership")
	statuses := []string{e.invitationStatus(t, invA), e.invitationStatus(t, resB.InvitationID)}
	assert.ElementsMatch(t, []string{db.GymInviteAccepted, db.GymInvitePending}, statuses, "the loser's invitation is back to pending")
}

func TestGymFlatIT_SupervisorChangeWritesAuditPeriodsAndBumpsVersion(t *testing.T) {
	e := newFlatITEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, token := e.joinLinkToken(t, "")
	_, apiErr := e.gym.AcceptByToken(ctx, "c1", token)
	require.Nil(t, apiErr)

	v0, err := e.policies.GetAuthzVersion(ctx, e.gymID)
	require.NoError(t, err)

	view, apiErr := e.members.SetSupervisor(ctx, "o1", e.gymID, "c1", "h1")
	require.Nil(t, apiErr)
	require.NotNil(t, view.SupervisorID)
	assert.Equal(t, "h1", *view.SupervisorID)

	v1, err := e.policies.GetAuthzVersion(ctx, e.gymID)
	require.NoError(t, err)
	assert.Greater(t, v1, v0, "authz_version bumped")

	audit, _, err := e.gyms.ListAudit(ctx, e.gymID, 1, 50)
	require.NoError(t, err)
	var found *db.TeamAuditEntry
	for i := range audit {
		if audit[i].Action == db.TeamActionSupervisorChange {
			found = &audit[i]
		}
	}
	require.NotNil(t, found, "supervisor_change audit row")
	assert.Equal(t, "o1", found.ActorID)
	assert.Equal(t, "c1", found.CoachID)
	assert.Equal(t, "h1", found.HeadCoachID)
	assert.Empty(t, found.PrevHeadCoachID)

	periods, err := e.team.Periods().ListForParent(ctx, db.PeriodKindTeam, "h1")
	require.NoError(t, err)
	require.Len(t, periods, 1)
	assert.Nil(t, periods[0].End)

	// clear: team row removed, period closed, second audit row, version bumped again
	_, apiErr = e.members.SetSupervisor(ctx, "o1", e.gymID, "c1", "")
	require.Nil(t, apiErr)
	periods, err = e.team.Periods().ListForParent(ctx, db.PeriodKindTeam, "h1")
	require.NoError(t, err)
	require.Len(t, periods, 1)
	assert.NotNil(t, periods[0].End, "period closed")
	n, err := e.database.Collection("coach_team_members").CountDocuments(ctx, bson.M{"coach_id": "c1"})
	require.NoError(t, err)
	assert.EqualValues(t, 0, n)
	v2, err := e.policies.GetAuthzVersion(ctx, e.gymID)
	require.NoError(t, err)
	assert.Greater(t, v2, v1)
	audit, _, err = e.gyms.ListAudit(ctx, e.gymID, 1, 50)
	require.NoError(t, err)
	changes := 0
	for _, a := range audit {
		if a.Action == db.TeamActionSupervisorChange {
			changes++
		}
	}
	assert.Equal(t, 2, changes)

	// guards: a coach cannot be their own supervisor, a non-head cannot supervise, only the owner may change
	_, apiErr = e.members.SetSupervisor(ctx, "o1", e.gymID, "c1", "c1")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.Code)
	_, apiErr = e.members.SetSupervisor(ctx, "o1", e.gymID, "c1", "o1")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.Code)
	_, apiErr = e.members.SetSupervisor(ctx, "h1", e.gymID, "c1", "h1")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusForbidden, apiErr.Code)
}

func TestGymFlatIT_ListMembersHeadCoachScope(t *testing.T) {
	e := newFlatITEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, tokenSup := e.joinLinkToken(t, "h1")
	_, tokenFree := e.joinLinkToken(t, "")
	for _, j := range []struct{ user, tok string }{{"c1", tokenSup}, {"c2", tokenFree}} {
		_, apiErr := e.gym.AcceptByToken(ctx, j.user, j.tok)
		require.Nil(t, apiErr)
	}
	_, err := e.database.Collection("users").InsertOne(ctx, bson.M{"externalid": "c1", "firstname": "Cara", "lastname": "One",
		"email": "c1@example.test", "phone_number": "+216"})
	require.NoError(t, err)

	ids := func(v []services.GymMemberView) []string {
		out := []string{}
		for _, m := range v {
			out = append(out, m.UserID)
		}
		return out
	}

	owner, total, apiErr := e.members.ListMembers(ctx, "o1", e.gymID, "", 1, 50)
	require.Nil(t, apiErr)
	assert.EqualValues(t, 4, total)
	assert.ElementsMatch(t, []string{"o1", "h1", "c1", "c2"}, ids(owner))

	head, total, apiErr := e.members.ListMembers(ctx, "h1", e.gymID, "", 1, 50)
	require.Nil(t, apiErr)
	assert.EqualValues(t, 2, total)
	assert.ElementsMatch(t, []string{"h1", "c1"}, ids(head), "head coach sees himself and his own coaches only")
	for _, m := range head {
		if m.UserID == "c1" {
			assert.Equal(t, "Cara", m.Firstname)
			require.NotNil(t, m.SupervisorID)
			assert.Equal(t, "h1", *m.SupervisorID)
		}
	}

	_, _, apiErr = e.members.ListMembers(ctx, "c1", e.gymID, "", 1, 50)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusForbidden, apiErr.Code, "a plain coach has no list access")

	_, _, apiErr = e.members.ListMembers(ctx, "stranger", e.gymID, "", 1, 50)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusNotFound, apiErr.Code, "a non-member learns nothing")
}

func TestGymFlatIT_ReconcileLeavesFlatRowsUntouched(t *testing.T) {
	e := newFlatITEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// c1 joins directly (source flat, no legacy rows behind it); c2 is supervised by h1 through the owner
	_, t1 := e.joinLinkToken(t, "")
	_, t2 := e.joinLinkToken(t, "")
	for _, j := range []struct{ user, tok string }{{"c1", t1}, {"c2", t2}} {
		_, apiErr := e.gym.AcceptByToken(ctx, j.user, j.tok)
		require.Nil(t, apiErr)
	}
	_, apiErr := e.members.SetSupervisor(ctx, "o1", e.gymID, "c2", "h1")
	require.Nil(t, apiErr)

	snapshot := func() map[string]bson.M {
		cur, err := e.database.Collection("gym_members").Find(ctx, bson.M{"user_id": bson.M{"$in": bson.A{"c1", "c2"}}})
		require.NoError(t, err)
		var rows []bson.M
		require.NoError(t, cur.All(ctx, &rows))
		out := map[string]bson.M{}
		for _, r := range rows {
			out[r["user_id"].(string)] = r
		}
		return out
	}
	before := snapshot()
	require.Len(t, before, 2)
	for _, r := range before {
		assert.Equal(t, "active", r["status"])
	}
	assert.Equal(t, "flat", before["c1"]["source"])
	assert.Equal(t, "flat", before["c2"]["source"])

	for i := 0; i < 2; i++ {
		rep := e.run(t, false)
		assert.Equal(t, 0, rep.Failed)
	}
	assert.Equal(t, before, snapshot(), "rows identical after reconcile")
}
