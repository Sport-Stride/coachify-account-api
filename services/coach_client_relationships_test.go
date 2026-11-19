package services_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models/db"
	"coachify-account-api/services"
)

type fakeCoachList struct {
	rows []map[string]interface{}
	err  error
}

func (f fakeCoachList) ListCoachClients(context.Context, db.CoachClientListQuery) ([]map[string]interface{}, int, error) {
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.rows, len(f.rows), nil
}

type fakeRelGymStore struct {
	fakeCoachGymStore
	gymRows []map[string]interface{}
	active  []db.GymClient
	err     error
	calls   int
}

func (f *fakeRelGymStore) ListCoachGymClients(context.Context, db.CoachClientListQuery) ([]map[string]interface{}, int, error) {
	return f.gymRows, len(f.gymRows), nil
}

func (f *fakeRelGymStore) ActiveGymClientsByClients(context.Context, []string) ([]db.GymClient, error) {
	f.calls++
	return f.active, f.err
}

func relRows(ids ...string) []map[string]interface{} {
	out := []map[string]interface{}{}
	for _, id := range ids {
		out = append(out, map[string]interface{}{"externalid": id})
	}
	return out
}

func gymRow(client, coach string) db.GymClient {
	return db.GymClient{GymID: gymA, ClientID: client, CoachID: coach, Status: db.GymClientStatusActive}
}

func TestCoachClientListRelationships(t *testing.T) {
	ctx := context.Background()
	members := &fakeAuthzMembers{rows: []db.GymMember{member("c1", db.GymMemberRoleCoach)}}
	personalOnly := []map[string]string{{"kind": "personal"}}
	q := func(kind string) db.CoachClientListQuery { return db.CoachClientListQuery{CoachID: "c1", Kind: kind} }

	t.Run("personal only", func(t *testing.T) {
		gym := &fakeRelGymStore{}
		svc := services.NewCoachService(nil, nil, nil, nil, "").WithListStore(fakeCoachList{rows: relRows("a")}).WithGymRelations(gym, members)
		rows, _, err := svc.ListCoachClients(ctx, q(""))
		require.NoError(t, err)
		assert.Equal(t, personalOnly, rows[0]["relationships"])
		assert.Equal(t, 1, gym.calls)
	})

	t.Run("gym only", func(t *testing.T) {
		gym := &fakeRelGymStore{gymRows: []map[string]interface{}{{"externalid": "a", "gym_id": gymA}}, active: []db.GymClient{gymRow("a", "c1")}}
		svc := services.NewCoachService(nil, nil, nil, nil, "").WithLinkStore(fakeLinks{}).WithGymRelations(gym, members)
		rows, _, err := svc.ListCoachClients(ctx, q(services.ClientKindGym))
		require.NoError(t, err)
		assert.Equal(t, []map[string]string{{"kind": "gym", "gym_id": gymA}}, rows[0]["relationships"])
	})

	t.Run("dual from the default list, other coaches gym rows hidden", func(t *testing.T) {
		gym := &fakeRelGymStore{active: []db.GymClient{gymRow("a", "c1"), gymRow("b", "c2")}}
		svc := services.NewCoachService(nil, nil, nil, nil, "").WithListStore(fakeCoachList{rows: relRows("a", "b")}).WithGymRelations(gym, members)
		rows, _, err := svc.ListCoachClients(ctx, q(""))
		require.NoError(t, err)
		assert.Equal(t, []map[string]string{{"kind": "personal"}, {"kind": "gym", "gym_id": gymA}}, rows[0]["relationships"])
		assert.Equal(t, personalOnly, rows[1]["relationships"])
		assert.Equal(t, 1, gym.calls, "one batched gym lookup per page")
	})

	t.Run("dual from the gym list adds the personal link", func(t *testing.T) {
		gym := &fakeRelGymStore{gymRows: []map[string]interface{}{{"externalid": "a", "gym_id": gymA}}, active: []db.GymClient{gymRow("a", "c1")}}
		svc := services.NewCoachService(nil, nil, nil, nil, "").WithLinkStore(fakeLinks{linked: map[string]bool{"c1|a": true}}).WithGymRelations(gym, members)
		rows, _, err := svc.ListCoachClients(ctx, q(services.ClientKindGym))
		require.NoError(t, err)
		assert.Equal(t, []map[string]string{{"kind": "gym", "gym_id": gymA}, {"kind": "personal"}}, rows[0]["relationships"])
	})

	t.Run("flag off: legacy payload, no relationships key", func(t *testing.T) {
		for _, kind := range []string{"", services.ClientKindPersonal} {
			svc := services.NewCoachService(nil, nil, nil, nil, "").WithListStore(fakeCoachList{rows: relRows("a")})
			rows, _, err := svc.ListCoachClients(ctx, q(kind))
			require.NoError(t, err)
			assert.NotContains(t, rows[0], "relationships")
		}
	})

	t.Run("missing list store is an error, not a panic", func(t *testing.T) {
		svc := services.NewCoachService(nil, nil, nil, nil, "").WithListStore(nil)
		_, _, err := svc.ListCoachClients(ctx, q(""))
		require.Error(t, err)
	})

	t.Run("memberships error degrades to personal only", func(t *testing.T) {
		gym := &fakeRelGymStore{active: []db.GymClient{gymRow("a", "c1")}}
		svc := services.NewCoachService(nil, nil, nil, nil, "").WithListStore(fakeCoachList{rows: relRows("a")}).
			WithGymRelations(gym, failingMembers{})
		rows, _, err := svc.ListCoachClients(ctx, q(""))
		require.NoError(t, err)
		assert.Equal(t, personalOnly, rows[0]["relationships"])
	})

	t.Run("gym entries sorted by gym id, one membership read per page", func(t *testing.T) {
		mem := &fakeAuthzMembers{rows: []db.GymMember{
			{GymID: "gz", UserID: "c1", Role: db.GymMemberRoleCoach, Status: db.GymMemberStatusActive},
			{GymID: "ga", UserID: "c1", Role: db.GymMemberRoleCoach, Status: db.GymMemberStatusActive},
		}}
		gym := &fakeRelGymStore{active: []db.GymClient{
			{GymID: "gz", ClientID: "a", CoachID: "c1", Status: db.GymClientStatusActive},
			{GymID: "ga", ClientID: "a", CoachID: "c1", Status: db.GymClientStatusActive},
		}}
		svc := services.NewCoachService(nil, nil, nil, nil, "").WithListStore(fakeCoachList{rows: relRows("a")}).WithGymRelations(gym, mem)
		rows, _, err := svc.ListCoachClients(ctx, q(""))
		require.NoError(t, err)
		assert.Equal(t, []map[string]string{{"kind": "personal"}, {"kind": "gym", "gym_id": "ga"}, {"kind": "gym", "gym_id": "gz"}}, rows[0]["relationships"])
	})

	t.Run("repo error", func(t *testing.T) {
		gym := &fakeRelGymStore{err: errors.New("boom")}
		svc := services.NewCoachService(nil, nil, nil, nil, "").WithListStore(fakeCoachList{rows: relRows("a")}).WithGymRelations(gym, members)
		_, _, err := svc.ListCoachClients(ctx, q(""))
		require.Error(t, err)
	})
}

type failingMembers struct{}

func (failingMembers) MembershipsOf(context.Context, string) ([]db.GymMember, error) {
	return nil, errors.New("boom")
}

func (failingMembers) MembershipOf(context.Context, string, string) (*db.GymMember, error) {
	return nil, errors.New("boom")
}
