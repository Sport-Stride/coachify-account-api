package services_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/services"
)

type fakeGymQuotaStore struct {
	count    int64
	override *int
	err      error
}

func (f *fakeGymQuotaStore) CountActive(context.Context, string) (int64, error) { return f.count, f.err }
func (f *fakeGymQuotaStore) MaxGymClientsOverride(context.Context, string) (*int, error) {
	return f.override, f.err
}

type fakePersonalQuota struct {
	calls int
	err   *models.ApiError
}

func (f *fakePersonalQuota) CheckInvitations(context.Context, string) *models.ApiError {
	f.calls++
	return f.err
}

type fakePlanLimit struct{ limit *int64 }

func (f fakePlanLimit) MaxGymClients(context.Context, string) (*int64, error) { return f.limit, nil }

func intp(v int) *int       { return &v }
func int64p(v int64) *int64 { return &v }

func TestQuotaGuardGym(t *testing.T) {
	ctx := context.Background()

	t.Run("no override and no plan means unlimited", func(t *testing.T) {
		g := services.NewQuotaGuard(&fakePersonalQuota{}, &fakeGymQuotaStore{count: 1000}, nil)
		assert.Nil(t, g.Check(ctx, services.QuotaKindGym, "c1", gymA))
	})

	t.Run("override caps the gym", func(t *testing.T) {
		g := services.NewQuotaGuard(&fakePersonalQuota{}, &fakeGymQuotaStore{count: 5, override: intp(5)}, nil)
		requireCode(t, g.Check(ctx, services.QuotaKindGym, "c1", gymA), http.StatusForbidden)
		g = services.NewQuotaGuard(&fakePersonalQuota{}, &fakeGymQuotaStore{count: 4, override: intp(5)}, nil)
		assert.Nil(t, g.Check(ctx, services.QuotaKindGym, "c1", gymA))
	})

	t.Run("override wins over the plan cap", func(t *testing.T) {
		g := services.NewQuotaGuard(nil, &fakeGymQuotaStore{count: 10, override: intp(50)}, fakePlanLimit{int64p(3)})
		assert.Nil(t, g.Check(ctx, services.QuotaKindGym, "", gymA))
	})

	t.Run("plan cap applies without override", func(t *testing.T) {
		g := services.NewQuotaGuard(nil, &fakeGymQuotaStore{count: 3}, fakePlanLimit{int64p(3)})
		requireCode(t, g.Check(ctx, services.QuotaKindGym, "", gymA), http.StatusForbidden)
		g = services.NewQuotaGuard(nil, &fakeGymQuotaStore{count: 3}, fakePlanLimit{nil})
		assert.Nil(t, g.Check(ctx, services.QuotaKindGym, "", gymA))
	})

	t.Run("gym path never consults the personal quota", func(t *testing.T) {
		p := &fakePersonalQuota{err: &models.ApiError{Code: http.StatusForbidden, Error: errors.New("personal full")}}
		g := services.NewQuotaGuard(p, &fakeGymQuotaStore{count: 0, override: intp(5)}, nil)
		assert.Nil(t, g.Check(ctx, services.QuotaKindGym, "c1", gymA))
		assert.Zero(t, p.calls)
	})

	t.Run("store error is 500", func(t *testing.T) {
		g := services.NewQuotaGuard(nil, &fakeGymQuotaStore{err: errors.New("boom")}, nil)
		requireCode(t, g.Check(ctx, services.QuotaKindGym, "", gymA), http.StatusInternalServerError)
	})
}

func TestQuotaGuardPersonalAndUnknown(t *testing.T) {
	ctx := context.Background()
	p := &fakePersonalQuota{err: &models.ApiError{Code: http.StatusForbidden, Error: errors.New("personal full")}}
	g := services.NewQuotaGuard(p, &fakeGymQuotaStore{}, nil)
	requireCode(t, g.Check(ctx, services.QuotaKindPersonal, "c1", ""), http.StatusForbidden)
	assert.Equal(t, 1, p.calls)
	requireCode(t, g.Check(ctx, "other", "c1", gymA), http.StatusBadRequest)
}

type fakeCoachGymStore struct {
	listCalls   int
	lastQuery   db.CoachClientListQuery
	linkedCalls int
}

func (f *fakeCoachGymStore) ListCoachGymClients(_ context.Context, q db.CoachClientListQuery) ([]map[string]interface{}, int, error) {
	f.listCalls++
	f.lastQuery = q
	return []map[string]interface{}{{"externalid": "g1"}}, 1, nil
}

func (f *fakeCoachGymStore) LinkedClientIDsWithGym(context.Context, string, []string) ([]string, error) {
	f.linkedCalls++
	return []string{"g1"}, nil
}

func TestCoachClientKinds(t *testing.T) {
	ctx := context.Background()

	t.Run("flag off: kind gym is empty and touches nothing", func(t *testing.T) {
		svc := services.NewCoachService(nil, nil, nil, nil, "")
		rows, total, err := svc.ListCoachClients(ctx, db.CoachClientListQuery{CoachID: "c1", Kind: services.ClientKindGym})
		require.NoError(t, err)
		assert.Empty(t, rows)
		assert.Zero(t, total)
	})

	t.Run("flag on: kind gym reads the gym store", func(t *testing.T) {
		gym := &fakeCoachGymStore{}
		svc := services.NewCoachService(nil, nil, nil, nil, "").WithGymRelations(gym)
		rows, total, err := svc.ListCoachClients(ctx, db.CoachClientListQuery{CoachID: "c1", Kind: services.ClientKindGym})
		require.NoError(t, err)
		assert.Equal(t, 1, total)
		assert.Len(t, rows, 1)
		assert.Equal(t, 1, gym.listCalls)
		assert.Equal(t, "c1", gym.lastQuery.CoachID)
	})

	t.Run("kind validation", func(t *testing.T) {
		assert.True(t, services.IsValidClientKind(""))
		assert.True(t, services.IsValidClientKind("personal"))
		assert.True(t, services.IsValidClientKind("gym"))
		assert.False(t, services.IsValidClientKind("all"))
		assert.False(t, services.IsValidClientKind("Gym"))
	})

	t.Run("link check uses the union store only with the flag on", func(t *testing.T) {
		legacy, store := newLinkSvc()
		store.On("LinkedClientIDs", mock.Anything, "c1", []string{"g1"}).Return(nil, nil)
		links, apiErr := legacy.CheckClientLinks(ctx, "c1", []string{"g1"})
		require.Nil(t, apiErr)
		assert.False(t, links["g1"])

		gym := &fakeCoachGymStore{}
		svc := services.NewCoachService(nil, nil, nil, nil, "").WithGymRelations(gym)
		links, apiErr = svc.CheckClientLinks(ctx, "c1", []string{"g1", "p1"})
		require.Nil(t, apiErr)
		assert.Equal(t, map[string]bool{"g1": true, "p1": false}, links)
		assert.Equal(t, 1, gym.linkedCalls)
	})
}
