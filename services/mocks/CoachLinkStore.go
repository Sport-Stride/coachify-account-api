// Hand-written in mockery style (mockery unavailable): mock of services.CoachLinkStore.

package mocks

import (
	context "context"

	mock "github.com/stretchr/testify/mock"
)

// CoachLinkStore is a mock type for the CoachLinkStore type
type CoachLinkStore struct {
	mock.Mock
}

func (_m *CoachLinkStore) LinkedClientIDs(ctx context.Context, coachID string, clientIDs []string) ([]string, error) {
	ret := _m.Called(ctx, coachID, clientIDs)
	var r0 []string
	if ret.Get(0) != nil {
		r0 = ret.Get(0).([]string)
	}
	return r0, ret.Error(1)
}
