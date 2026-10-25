package services_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"coachify-account-api/services"
	"coachify-account-api/services/mocks"
)

func newLinkSvc() (*services.CoachServiceImpl, *mocks.CoachLinkStore) {
	store := &mocks.CoachLinkStore{}
	return services.NewCoachService(nil, nil, nil, nil, "").WithLinkStore(store), store
}

func TestCheckClientLinks(t *testing.T) {
	t.Run("mixed linked and unlinked", func(t *testing.T) {
		svc, store := newLinkSvc()
		store.On("LinkedClientIDs", mock.Anything, "coach1", []string{"c1", "c2", "c3"}).Return([]string{"c1", "c3"}, nil)
		links, apiErr := svc.CheckClientLinks(context.Background(), "coach1", []string{"c1", "c2", "c3"})
		require.Nil(t, apiErr)
		assert.Equal(t, map[string]bool{"c1": true, "c2": false, "c3": true}, links)
	})

	t.Run("ids returned by store but not requested are ignored", func(t *testing.T) {
		svc, store := newLinkSvc()
		store.On("LinkedClientIDs", mock.Anything, "coach1", []string{"c1"}).Return([]string{"zzz"}, nil)
		links, apiErr := svc.CheckClientLinks(context.Background(), "coach1", []string{"c1"})
		require.Nil(t, apiErr)
		assert.Equal(t, map[string]bool{"c1": false}, links)
	})

	t.Run("no links at all", func(t *testing.T) {
		svc, store := newLinkSvc()
		store.On("LinkedClientIDs", mock.Anything, "coach1", mock.Anything).Return(nil, nil)
		links, apiErr := svc.CheckClientLinks(context.Background(), "coach1", []string{"c1", "c2"})
		require.Nil(t, apiErr)
		assert.Equal(t, map[string]bool{"c1": false, "c2": false}, links)
	})

	t.Run("empty coach id rejected", func(t *testing.T) {
		svc, store := newLinkSvc()
		_, apiErr := svc.CheckClientLinks(context.Background(), "", []string{"c1"})
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code)
		store.AssertNotCalled(t, "LinkedClientIDs", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("repo error becomes 500 without leaking cause", func(t *testing.T) {
		svc, store := newLinkSvc()
		store.On("LinkedClientIDs", mock.Anything, "coach1", mock.Anything).Return(nil, errors.New("mongo: secret host down"))
		links, apiErr := svc.CheckClientLinks(context.Background(), "coach1", []string{"c1"})
		assert.Nil(t, links)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
		assert.NotContains(t, apiErr.Error.Error(), "secret")
	})
}
