package services_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models/db"
	"coachify-account-api/services"
	"coachify-account-api/services/mocks"
)

func newTeamSvc() (services.TeamService, *mocks.TeamUserStore, *mocks.TeamStore) {
	u, t := &mocks.TeamUserStore{}, &mocks.TeamStore{}
	return services.NewTeamService(u, t), u, t
}

func coachUser(id string, head bool, status db.UserStatus) *db.User {
	return &db.User{ExternalID: id, UserRole: "coach", IsHeadCoach: head, UserStatus: status}
}

func givenUser(u *mocks.TeamUserStore, user *db.User) {
	u.On("GetUserByExternalIdUpdate", mock.Anything, user.ExternalID).Return(user, nil)
}

func auditAction(action string) interface{} {
	return mock.MatchedBy(func(e *db.TeamAuditEntry) bool { return e.Action == action })
}

func TestPromote(t *testing.T) {
	ctx := context.Background()

	t.Run("ok", func(t *testing.T) {
		svc, u, ts := newTeamSvc()
		givenUser(u, coachUser("c1", false, db.Active))
		ts.On("IsMember", mock.Anything, "c1").Return(false, nil)
		u.On("SetHeadCoach", mock.Anything, "c1", true, "admin1").Return(nil)
		ts.On("InsertAudit", mock.Anything, auditAction("promote")).Return(nil)

		row, apiErr := svc.Promote(ctx, "admin1", "c1")
		require.Nil(t, apiErr)
		assert.Equal(t, "c1", row.ExternalID)
		u.AssertExpectations(t)
		ts.AssertExpectations(t)
	})

	t.Run("idempotent", func(t *testing.T) {
		svc, u, ts := newTeamSvc()
		givenUser(u, coachUser("c1", true, db.Active))
		ts.On("CountByHead", mock.Anything, "c1").Return(3, nil)

		row, apiErr := svc.Promote(ctx, "admin1", "c1")
		require.Nil(t, apiErr)
		assert.Equal(t, 3, row.TeamSize)
		u.AssertNotCalled(t, "SetHeadCoach", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
		ts.AssertNotCalled(t, "InsertAudit", mock.Anything, mock.Anything)
	})

	t.Run("not a coach", func(t *testing.T) {
		svc, u, _ := newTeamSvc()
		givenUser(u, &db.User{ExternalID: "n1", UserRole: "nutritionist", UserStatus: db.Active})

		_, apiErr := svc.Promote(ctx, "admin1", "n1")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code)
		assert.EqualError(t, apiErr.Error, "user is not a coach")
	})

	t.Run("inactive coach", func(t *testing.T) {
		svc, u, _ := newTeamSvc()
		givenUser(u, coachUser("c1", false, db.Blocked))

		_, apiErr := svc.Promote(ctx, "admin1", "c1")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
		assert.EqualError(t, apiErr.Error, "coach is not active")
		u.AssertNotCalled(t, "SetHeadCoach", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("becomes member concurrently, flag rolled back", func(t *testing.T) {
		svc, u, ts := newTeamSvc()
		givenUser(u, coachUser("c1", false, db.Active))
		ts.On("IsMember", mock.Anything, "c1").Return(false, nil).Once()
		ts.On("IsMember", mock.Anything, "c1").Return(true, nil).Once()
		u.On("SetHeadCoach", mock.Anything, "c1", true, "admin1").Return(nil).Once()
		u.On("SetHeadCoach", mock.Anything, "c1", false, "admin1").Return(nil).Once()

		_, apiErr := svc.Promote(ctx, "admin1", "c1")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
		assert.EqualError(t, apiErr.Error, "coach belongs to a team, unassign first")
		u.AssertExpectations(t)
		ts.AssertNotCalled(t, "InsertAudit", mock.Anything, mock.Anything)
	})

	t.Run("assigned coach", func(t *testing.T) {
		svc, u, ts := newTeamSvc()
		givenUser(u, coachUser("c1", false, db.Active))
		ts.On("IsMember", mock.Anything, "c1").Return(true, nil)

		_, apiErr := svc.Promote(ctx, "admin1", "c1")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
		assert.EqualError(t, apiErr.Error, "coach belongs to a team, unassign first")
		u.AssertNotCalled(t, "SetHeadCoach", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})
}

func TestDemote(t *testing.T) {
	ctx := context.Background()

	t.Run("releases team, cuts access first", func(t *testing.T) {
		svc, u, ts := newTeamSvc()
		givenUser(u, coachUser("h1", true, db.Active))
		var order []string
		u.On("SetHeadCoach", mock.Anything, "h1", false, "admin1").
			Run(func(mock.Arguments) { order = append(order, "flag") }).Return(nil)
		ts.On("IDsByHead", mock.Anything, "h1").Return([]string{"c1", "c2"}, nil)
		ts.On("DeleteByHead", mock.Anything, "h1").
			Run(func(mock.Arguments) { order = append(order, "delete") }).Return(int64(2), nil)
		ts.On("InsertAuditMany", mock.Anything, mock.MatchedBy(func(es []*db.TeamAuditEntry) bool {
			return len(es) == 3 && es[0].Action == "demote" && es[1].Action == "unassign"
		})).Return(nil)

		released, apiErr := svc.Demote(ctx, "admin1", "h1")
		require.Nil(t, apiErr)
		assert.Equal(t, 2, released)
		assert.Equal(t, []string{"flag", "delete"}, order)
	})

	t.Run("retry with flag already off still releases members", func(t *testing.T) {
		svc, u, ts := newTeamSvc()
		givenUser(u, coachUser("h1", false, db.Active))
		ts.On("CountByHead", mock.Anything, "h1").Return(2, nil)
		u.On("SetHeadCoach", mock.Anything, "h1", false, "admin1").Return(nil)
		ts.On("IDsByHead", mock.Anything, "h1").Return([]string{"c1", "c2"}, nil)
		ts.On("DeleteByHead", mock.Anything, "h1").Return(int64(2), nil)
		ts.On("InsertAuditMany", mock.Anything, mock.MatchedBy(func(es []*db.TeamAuditEntry) bool {
			return len(es) == 3
		})).Return(nil)

		released, apiErr := svc.Demote(ctx, "admin1", "h1")
		require.Nil(t, apiErr)
		assert.Equal(t, 2, released)
		ts.AssertExpectations(t)
	})

	t.Run("not a head coach", func(t *testing.T) {
		svc, u, ts := newTeamSvc()
		givenUser(u, coachUser("h1", false, db.Active))
		ts.On("CountByHead", mock.Anything, "h1").Return(0, nil)

		_, apiErr := svc.Demote(ctx, "admin1", "h1")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
		assert.EqualError(t, apiErr.Error, "not a head coach")
		ts.AssertNotCalled(t, "DeleteByHead", mock.Anything, mock.Anything)
	})
}

func TestAssign(t *testing.T) {
	ctx := context.Background()

	t.Run("self", func(t *testing.T) {
		svc, _, _ := newTeamSvc()
		_, apiErr := svc.Assign(ctx, "admin1", "c1", "c1")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code)
	})

	t.Run("target is not a head coach", func(t *testing.T) {
		svc, u, ts := newTeamSvc()
		givenUser(u, coachUser("c1", false, db.Active))
		givenUser(u, coachUser("h1", false, db.Active))

		_, apiErr := svc.Assign(ctx, "admin1", "c1", "h1")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
		ts.AssertNotCalled(t, "Upsert", mock.Anything, mock.Anything)
	})

	t.Run("head coach cannot be a member", func(t *testing.T) {
		svc, u, ts := newTeamSvc()
		givenUser(u, coachUser("c1", true, db.Active))

		_, apiErr := svc.Assign(ctx, "admin1", "c1", "h1")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
		ts.AssertNotCalled(t, "Upsert", mock.Anything, mock.Anything)
	})

	t.Run("inactive head coach", func(t *testing.T) {
		svc, u, ts := newTeamSvc()
		givenUser(u, coachUser("c1", false, db.Active))
		givenUser(u, coachUser("h1", true, db.Blocked))

		_, apiErr := svc.Assign(ctx, "admin1", "c1", "h1")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
		ts.AssertNotCalled(t, "Upsert", mock.Anything, mock.Anything)
	})

	t.Run("new assignment", func(t *testing.T) {
		svc, u, ts := newTeamSvc()
		givenUser(u, coachUser("c1", false, db.Active))
		givenUser(u, coachUser("h1", true, db.Active))
		ts.On("Upsert", mock.Anything, mock.Anything).Return(nil, nil)
		ts.On("InsertAudit", mock.Anything, auditAction("assign")).Return(nil)

		m, apiErr := svc.Assign(ctx, "admin1", "c1", "h1")
		require.Nil(t, apiErr)
		assert.Equal(t, "h1", m.HeadCoachID)
		assert.Equal(t, "admin", m.AssignedByRole)
		ts.AssertExpectations(t)
	})

	t.Run("head demoted during assign is rolled back", func(t *testing.T) {
		svc, u, ts := newTeamSvc()
		givenUser(u, coachUser("c1", false, db.Active))
		u.On("GetUserByExternalIdUpdate", mock.Anything, "h1").Return(coachUser("h1", true, db.Active), nil).Once()
		u.On("GetUserByExternalIdUpdate", mock.Anything, "h1").Return(coachUser("h1", false, db.Active), nil)
		ts.On("Upsert", mock.Anything, mock.Anything).Return(nil, nil)
		ts.On("DeleteByCoach", mock.Anything, "c1").Return(int64(1), nil)

		_, apiErr := svc.Assign(ctx, "admin1", "c1", "h1")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
		ts.AssertNotCalled(t, "InsertAudit", mock.Anything, mock.Anything)
		ts.AssertExpectations(t)
	})

	t.Run("reassign is a single upsert", func(t *testing.T) {
		svc, u, ts := newTeamSvc()
		givenUser(u, coachUser("c1", false, db.Active))
		givenUser(u, coachUser("h2", true, db.Active))
		ts.On("Upsert", mock.Anything, mock.Anything).Return(&db.TeamMember{CoachID: "c1", HeadCoachID: "h1"}, nil).Once()
		ts.On("InsertAudit", mock.Anything, mock.MatchedBy(func(e *db.TeamAuditEntry) bool {
			return e.Action == "reassign" && e.PrevHeadCoachID == "h1" && e.HeadCoachID == "h2"
		})).Return(nil)

		_, apiErr := svc.Assign(ctx, "admin1", "c1", "h2")
		require.Nil(t, apiErr)
		ts.AssertNotCalled(t, "DeleteByCoach", mock.Anything, mock.Anything)
		ts.AssertExpectations(t)
	})
}

func TestHeadCoachAccess(t *testing.T) {
	ctx := context.Background()

	for name, user := range map[string]*db.User{
		"blocked head coach":   coachUser("h1", true, db.Blocked),
		"suspended head coach": coachUser("h1", true, db.Suspended),
		"not a head coach":     coachUser("h1", false, db.Active),
	} {
		t.Run(name+" gets 403", func(t *testing.T) {
			svc, u, ts := newTeamSvc()
			givenUser(u, user)

			_, apiErr := svc.Overview(ctx, "h1")
			require.NotNil(t, apiErr)
			assert.Equal(t, http.StatusForbidden, apiErr.Code)
			assert.EqualError(t, apiErr.Error, "Head coach access required")
			ts.AssertNotCalled(t, "Overview", mock.Anything, mock.Anything)
		})
	}

	t.Run("me is false for non-head", func(t *testing.T) {
		svc, u, _ := newTeamSvc()
		givenUser(u, coachUser("c1", false, db.Active))
		me, apiErr := svc.Me(ctx, "c1")
		require.Nil(t, apiErr)
		assert.False(t, me.IsHeadCoach)
	})
}

func TestHeadCoachOutsideTeam(t *testing.T) {
	ctx := context.Background()

	t.Run("remove coach of another team is 404", func(t *testing.T) {
		svc, u, ts := newTeamSvc()
		givenUser(u, coachUser("h1", true, db.Active))
		ts.On("GetHeadOf", mock.Anything, "c9").Return(&db.TeamMember{CoachID: "c9", HeadCoachID: "h2"}, nil)

		apiErr := svc.RemoveMyCoach(ctx, "h1", "c9")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)
		ts.AssertNotCalled(t, "DeleteByCoach", mock.Anything, mock.Anything)
	})

	t.Run("remove unassigned coach is 404", func(t *testing.T) {
		svc, u, ts := newTeamSvc()
		givenUser(u, coachUser("h1", true, db.Active))
		ts.On("GetHeadOf", mock.Anything, "c9").Return(nil, nil)

		apiErr := svc.RemoveMyCoach(ctx, "h1", "c9")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)
	})

	t.Run("get coach outside team is 404", func(t *testing.T) {
		svc, u, ts := newTeamSvc()
		givenUser(u, coachUser("h1", true, db.Active))
		ts.On("GetRow", mock.Anything, "h1", "c9").Return(nil, nil)

		_, apiErr := svc.GetMyCoach(ctx, "h1", "c9")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)
	})

	t.Run("remove own coach audits as head_coach", func(t *testing.T) {
		svc, u, ts := newTeamSvc()
		givenUser(u, coachUser("h1", true, db.Active))
		ts.On("GetHeadOf", mock.Anything, "c1").Return(&db.TeamMember{CoachID: "c1", HeadCoachID: "h1"}, nil)
		ts.On("DeleteByCoach", mock.Anything, "c1").Return(int64(1), nil)
		ts.On("InsertAudit", mock.Anything, mock.MatchedBy(func(e *db.TeamAuditEntry) bool {
			return e.Action == "unassign" && e.ActorRole == "head_coach" && e.ActorID == "h1"
		})).Return(nil)

		require.Nil(t, svc.RemoveMyCoach(ctx, "h1", "c1"))
		ts.AssertExpectations(t)
	})
}

func TestMyHeadCoachHidesDemotedHead(t *testing.T) {
	svc, u, ts := newTeamSvc()
	ts.On("GetHeadOf", mock.Anything, "c1").Return(&db.TeamMember{CoachID: "c1", HeadCoachID: "h1"}, nil)
	givenUser(u, coachUser("h1", false, db.Active))

	head, apiErr := svc.MyHeadCoach(context.Background(), "c1")
	require.Nil(t, apiErr)
	assert.Nil(t, head)
}

func TestInternalErrorsAreGeneric(t *testing.T) {
	svc, u, ts := newTeamSvc()
	givenUser(u, coachUser("h1", true, db.Active))
	ts.On("Overview", mock.Anything, "h1").Return(nil, errors.New("mongo: secret connection string"))

	_, apiErr := svc.Overview(context.Background(), "h1")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
	assert.EqualError(t, apiErr.Error, "internal error")
}
