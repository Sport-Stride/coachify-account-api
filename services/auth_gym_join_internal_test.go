package services

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models"
)

type stubJoinRedeemer struct {
	lk  *JoinLookup
	err *models.ApiError
}

func (s stubJoinRedeemer) LookupJoin(context.Context, string) (*JoinLookup, *models.ApiError) {
	return s.lk, s.err
}
func (s stubJoinRedeemer) JoinOnSignup(context.Context, string, string) *models.ApiError { return nil }

func TestCheckGymJoin(t *testing.T) {
	ctx := context.Background()

	t.Run("disabled is 404", func(t *testing.T) {
		_, apiErr := (&AuthServiceImpl{}).checkGymJoin(ctx, "t", "a@x.tn")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)
	})
	t.Run("lookup error is passed through", func(t *testing.T) {
		s := (&AuthServiceImpl{}).WithGymJoins(stubJoinRedeemer{err: inviteNotFound()})
		_, apiErr := s.checkGymJoin(ctx, "t", "a@x.tn")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)
	})
	t.Run("open link accepts any email", func(t *testing.T) {
		s := (&AuthServiceImpl{}).WithGymJoins(stubJoinRedeemer{lk: &JoinLookup{GymName: "G"}})
		email, apiErr := s.checkGymJoin(ctx, "t", "any@x.tn")
		require.Nil(t, apiErr)
		assert.Equal(t, "", email)
	})
	t.Run("bound link matches case-insensitively, mismatch is 403", func(t *testing.T) {
		s := (&AuthServiceImpl{}).WithGymJoins(stubJoinRedeemer{lk: &JoinLookup{Email: "c@x.tn"}})
		email, apiErr := s.checkGymJoin(ctx, "t", " C@X.tn ")
		require.Nil(t, apiErr)
		assert.Equal(t, "c@x.tn", email)

		_, apiErr = s.checkGymJoin(ctx, "t", "other@x.tn")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusForbidden, apiErr.Code)
	})
}
