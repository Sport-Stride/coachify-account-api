package services_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models/db"
	"coachify-account-api/services"
	"coachify-account-api/services/mocks"
)

func newCachedTeamSvc(ttl time.Duration) (services.TeamService, *mocks.TeamUserStore, *mocks.TeamStore) {
	u, t := &mocks.TeamUserStore{}, &mocks.TeamStore{}
	return services.NewTeamService(u, t, services.WithReadCache(ttl)), u, t
}

func TestMeReadCache(t *testing.T) {
	ctx := context.Background()

	t.Run("same answer with and without cache", func(t *testing.T) {
		plain, pu, pt := newTeamSvc()
		cached, cu, ct := newCachedTeamSvc(time.Minute)
		for _, u := range []*mocks.TeamUserStore{pu, cu} {
			givenUser(u, coachUser("h1", true, db.Active))
		}
		pt.On("CountByHead", mock.Anything, "h1").Return(3, nil)
		ct.On("CountByHead", mock.Anything, "h1").Return(3, nil)

		want, werr := plain.Me(ctx, "h1")
		got, gerr := cached.Me(ctx, "h1")
		require.Nil(t, werr)
		require.Nil(t, gerr)
		assert.Equal(t, want, got)
	})

	t.Run("second call hits the cache", func(t *testing.T) {
		svc, u, ts := newCachedTeamSvc(time.Minute)
		givenUser(u, coachUser("h1", true, db.Active))
		ts.On("CountByHead", mock.Anything, "h1").Return(2, nil)

		for i := 0; i < 3; i++ {
			me, apiErr := svc.Me(ctx, "h1")
			require.Nil(t, apiErr)
			assert.Equal(t, 2, me.TeamSize)
		}
		u.AssertNumberOfCalls(t, "GetUserByExternalIdUpdate", 1)
		ts.AssertNumberOfCalls(t, "CountByHead", 1)
	})

	t.Run("errors are not cached", func(t *testing.T) {
		svc, u, ts := newCachedTeamSvc(time.Minute)
		givenUser(u, coachUser("h1", true, db.Active))
		ts.On("CountByHead", mock.Anything, "h1").Return(0, assert.AnError).Once()
		ts.On("CountByHead", mock.Anything, "h1").Return(1, nil).Once()

		_, apiErr := svc.Me(ctx, "h1")
		require.NotNil(t, apiErr)
		me, apiErr := svc.Me(ctx, "h1")
		require.Nil(t, apiErr)
		assert.Equal(t, 1, me.TeamSize)
	})

	t.Run("expires after ttl", func(t *testing.T) {
		svc, u, ts := newCachedTeamSvc(20 * time.Millisecond)
		givenUser(u, coachUser("h1", true, db.Active))
		ts.On("CountByHead", mock.Anything, "h1").Return(2, nil)

		_, _ = svc.Me(ctx, "h1")
		time.Sleep(40 * time.Millisecond)
		_, _ = svc.Me(ctx, "h1")
		ts.AssertNumberOfCalls(t, "CountByHead", 2)
	})

	t.Run("mutation purges", func(t *testing.T) {
		svc, u, ts := newCachedTeamSvc(time.Minute)
		givenUser(u, coachUser("h1", true, db.Active))
		ts.On("CountByHead", mock.Anything, "h1").Return(2, nil).Once()
		ts.On("CountByHead", mock.Anything, "h1").Return(3, nil).Once()
		ts.On("GetHeadOf", mock.Anything, "c9").Return((*db.TeamMember)(nil), nil)

		first, _ := svc.Me(ctx, "h1")
		assert.Equal(t, 2, first.TeamSize)
		_ = svc.RemoveMyCoach(ctx, "h1", "c9") // fails (not found) but still purges
		second, _ := svc.Me(ctx, "h1")
		assert.Equal(t, 3, second.TeamSize)
	})
}

func TestMyHeadCoachReadCache(t *testing.T) {
	ctx := context.Background()
	svc, u, ts := newCachedTeamSvc(time.Minute)
	ts.On("GetHeadOf", mock.Anything, "c1").Return(&db.TeamMember{HeadCoachID: "h1"}, nil)
	givenUser(u, &db.User{ExternalID: "h1", UserFirstname: "Hea", IsHeadCoach: true})

	for i := 0; i < 3; i++ {
		info, apiErr := svc.MyHeadCoach(ctx, "c1")
		require.Nil(t, apiErr)
		require.NotNil(t, info)
		assert.Equal(t, "Hea", info.Firstname)
	}
	ts.AssertNumberOfCalls(t, "GetHeadOf", 1)

	// "no head coach" (nil, nil) is cached too
	ts.On("GetHeadOf", mock.Anything, "c2").Return((*db.TeamMember)(nil), nil)
	for i := 0; i < 2; i++ {
		info, apiErr := svc.MyHeadCoach(ctx, "c2")
		require.Nil(t, apiErr)
		assert.Nil(t, info)
	}
	ts.AssertNumberOfCalls(t, "GetHeadOf", 2)
}

func BenchmarkMeCached(b *testing.B) {
	ctx := context.Background()
	svc, u, ts := newCachedTeamSvc(time.Minute)
	givenUser(u, coachUser("h1", true, db.Active))
	ts.On("CountByHead", mock.Anything, "h1").Return(5, nil)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = svc.Me(ctx, "h1")
		}
	})
}

func BenchmarkMeUncached(b *testing.B) {
	ctx := context.Background()
	svc, u, ts := newTeamSvc()
	givenUser(u, coachUser("h1", true, db.Active))
	ts.On("CountByHead", mock.Anything, "h1").Return(5, nil)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = svc.Me(ctx, "h1")
	}
}
