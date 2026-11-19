package services_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/services"
)

type fakeStanding struct {
	calls int
	apiE  *models.ApiError
	until *time.Time
	state db.SeatState
}

func (f *fakeStanding) Standing(_ context.Context, _, userID string) (*services.SeatStanding, *models.ApiError) {
	f.calls++
	if f.apiE != nil {
		return nil, f.apiE
	}
	return &services.SeatStanding{State: f.state, Member: &db.GymMember{UserID: userID, SeatGraceUntil: f.until}}, nil
}

func gymContextSvc(opts ...services.GymPolicyOption) services.GymPolicyService {
	m, s, chain := authzFixture()
	cache := services.NewCachingAuthorizer(chain, time.Hour)
	return services.NewGymPolicyService(cache, chain, m, s, fakeGymNames{}, fakePolicyUsers{}, cache, true, opts...)
}

func TestGymContextSeatFields(t *testing.T) {
	ctx := context.Background()
	until := time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC)

	// grace: state and deadline
	st := &fakeStanding{state: db.SeatStateGrace, until: &until}
	v, apiErr := gymContextSvc(services.WithSeatStanding(st)).GymContext(ctx, "coach")
	require.Nil(t, apiErr)
	require.Len(t, v.Memberships, 1)
	assert.Equal(t, "grace", v.Memberships[0].SeatState)
	require.NotNil(t, v.Memberships[0].SeatGraceUntil)
	assert.True(t, v.Memberships[0].SeatGraceUntil.Equal(until))

	// seated: no deadline
	st = &fakeStanding{state: db.SeatStateSeated, until: &until}
	v, _ = gymContextSvc(services.WithSeatStanding(st)).GymContext(ctx, "head")
	assert.Equal(t, "seated", v.Memberships[0].SeatState)
	assert.Nil(t, v.Memberships[0].SeatGraceUntil)

	// owner: omitted, and no standing lookup
	st = &fakeStanding{state: db.SeatStateSeated}
	v, _ = gymContextSvc(services.WithSeatStanding(st)).GymContext(ctx, "owner")
	assert.Equal(t, "", v.Memberships[0].SeatState)
	assert.Zero(t, st.calls)

	// error: fail soft, the rest of the payload stays
	st = &fakeStanding{apiE: models.NewApiError(503, assert.AnError)}
	v, apiErr = gymContextSvc(services.WithSeatStanding(st)).GymContext(ctx, "coach")
	require.Nil(t, apiErr)
	assert.Equal(t, "", v.Memberships[0].SeatState)
	assert.NotEmpty(t, v.Memberships[0].Capabilities)

	// flag off (no seat option): omitted
	v, _ = gymContextSvc().GymContext(ctx, "coach")
	assert.Equal(t, "", v.Memberships[0].SeatState)
	assert.Nil(t, v.Memberships[0].SeatGraceUntil)
}
