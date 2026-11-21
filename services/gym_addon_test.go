package services_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/pkg/payments"
	"coachify-account-api/services"
)

type fakeAddonUsers struct{ emailLookups int }

func (f *fakeAddonUsers) GetUsersByExternalIds(_ context.Context, ids []string) ([]db.UserResponse, *models.ApiError) {
	var out []db.UserResponse
	for _, id := range ids {
		out = append(out, db.UserResponse{ExternalID: id, Firstname: "First-" + id, Lastname: "Last", ProfilePicture: "pic-" + id})
	}
	return out, nil
}

func (f *fakeAddonUsers) GetUserByExternalIdUpdate(_ context.Context, id string) (*db.User, *models.ApiError) {
	f.emailLookups++
	return &db.User{UserEmail: id + "@example.test"}, nil
}

type fakeAddonNotifier struct {
	users []string
	last  map[string]any
	url   string
	eid   string
}

func (f *fakeAddonNotifier) Notify(_ context.Context, userID, _, _, entityID, url string, data map[string]any) error {
	f.users = append(f.users, userID)
	f.last, f.url, f.eid = data, url, entityID
	return nil
}

type fakeAddonMailer struct{ to []string }

func (f *fakeAddonMailer) SendGymAssignmentEmail(_ context.Context, email, _, _ string) error {
	f.to = append(f.to, email)
	return nil
}

type fakeAddonGyms struct{}

func (fakeAddonGyms) GetGym(context.Context, string) (*db.Gym, error) {
	return &db.Gym{OwnerID: "owner"}, nil
}

func TestGymAddonResolveCoaches(t *testing.T) {
	members := &fakeAuthzMembers{rows: []db.GymMember{
		member("owner", db.GymMemberRoleOwner),
		member("c1", db.GymMemberRoleCoach),
		member("c2", db.GymMemberRoleCoach),
		member("mgr", db.GymMemberRole("manager")),
		{GymID: gymA, UserID: "gone", Role: db.GymMemberRoleCoach, Status: db.GymMemberStatusLeft},
	}}
	svc := func(seats services.GymSeatStanding) services.GymAddonService {
		return services.NewGymAddonService(services.GymAddonDeps{Members: members, Users: &fakeAddonUsers{}, Seats: seats})
	}
	ctx := context.Background()

	got, apiErr := svc(nil).ResolveCoaches(ctx, gymA, []string{"owner", "c1", "mgr", "gone", "nobody", "c1", ""})
	require.Nil(t, apiErr)
	elig := map[string]bool{}
	for _, c := range got {
		elig[c.ID] = c.Eligible
	}
	assert.Len(t, got, 5, "deduplicated, empty ids dropped")
	assert.Equal(t, map[string]bool{"owner": true, "c1": true, "mgr": false, "gone": false, "nobody": false}, elig)
	assert.Equal(t, "First-c1 Last", got[1].Name)
	assert.Equal(t, "pic-c1", got[1].ProfilePicture)
	assert.Equal(t, "coach", got[1].Role)

	t.Run("seat refusal makes the coach ineligible", func(t *testing.T) {
		seats := seatStandingFake{st: standing(db.GymMemberRoleCoach, db.SeatStateReadOnly, false)}
		got, apiErr := svc(seats).ResolveCoaches(ctx, gymA, []string{"c1"})
		require.Nil(t, apiErr)
		assert.False(t, got[0].Eligible)
	})
	t.Run("cap and gym id", func(t *testing.T) {
		ids := make([]string, 51)
		for i := range ids {
			ids[i] = string(rune('a'+i%26)) + string(rune('A'+i/26))
		}
		_, apiErr := svc(nil).ResolveCoaches(ctx, gymA, ids)
		requireCode(t, apiErr, http.StatusBadRequest)
		_, apiErr = svc(nil).ResolveCoaches(ctx, "", []string{"c1"})
		requireCode(t, apiErr, http.StatusBadRequest)
	})
}

type addonFixture struct {
	*dispatchFixture
	svc      services.GymAddonService
	notifier *fakeAddonNotifier
	mailer   *fakeAddonMailer
	users    *fakeAddonUsers
}

func newAddonFixture(t *testing.T, seats services.GymSeatStanding) *addonFixture {
	t.Helper()
	d := newDispatchFixtureSeats(t, seats)
	n, m, u := &fakeAddonNotifier{}, &fakeAddonMailer{}, &fakeAddonUsers{}
	members := &fakeAuthzMembers{rows: []db.GymMember{member("owner", db.GymMemberRoleOwner), member("c1", db.GymMemberRoleCoach), member("c2", db.GymMemberRoleCoach)}}
	return &addonFixture{dispatchFixture: d, notifier: n, mailer: m, users: u, svc: services.NewGymAddonService(services.GymAddonDeps{
		Members: members, Users: u, Gyms: fakeAddonGyms{}, Assigner: d.svc, Notifier: n, Mailer: m,
	})}
}

func purchase(client, coach string) services.GymAddonPurchase {
	return services.GymAddonPurchase{GymID: gymA, ClientID: client, CoachID: coach, PlanID: "p1", PlanName: "Extra", SubscriptionID: "sub1"}
}

func TestGymAddonOnPurchasedAssignsAndNotifies(t *testing.T) {
	f := newAddonFixture(t, nil)
	ctx := context.Background()

	require.Nil(t, f.svc.OnPurchased(ctx, purchase("cl2", "c2")))
	assert.Equal(t, "c2", f.coachOf("cl2"))
	require.Len(t, f.tracker.transfers, 1)
	assert.Equal(t, "gym_addon_purchase", f.tracker.transfers[0].reason)
	require.Len(t, f.audit.entries, 1)
	assert.Equal(t, "system:gym_addon", f.audit.entries[0].ActorID)
	assert.Equal(t, []string{"c2", "owner"}, f.notifier.users, "coach and owner get the in-app notice")
	assert.Equal(t, "gym_addon_purchased", f.notifier.last["type"])
	assert.Equal(t, "sub1", f.notifier.eid)
	assert.NotEmpty(t, f.notifier.url)
	assert.ElementsMatch(t, []string{"c2@example.test", "owner@example.test"}, f.mailer.to)

	t.Run("same coach is a no-op assignment", func(t *testing.T) {
		txBefore := f.tx.txCalls
		require.Nil(t, f.svc.OnPurchased(ctx, purchase("cl2", "c2")))
		assert.Equal(t, txBefore, f.tx.txCalls, "no second assignment transaction")
		assert.Len(t, f.tracker.transfers, 1)
	})

	t.Run("another coach reassigns", func(t *testing.T) {
		require.Nil(t, f.svc.OnPurchased(ctx, purchase("cl2", "c1")))
		assert.Equal(t, "c1", f.coachOf("cl2"))
	})

	t.Run("required fields", func(t *testing.T) {
		in := purchase("cl2", "c2")
		in.SubscriptionID = ""
		requireCode(t, f.svc.OnPurchased(ctx, in), http.StatusBadRequest)
	})
}

func TestGymAddonOnPurchasedSeatRefusedStillNotifies(t *testing.T) {
	f := newAddonFixture(t, seatStandingFake{st: standing(db.GymMemberRoleCoach, db.SeatStateReadOnly, false)})
	require.Nil(t, f.svc.OnPurchased(context.Background(), purchase("cl2", "c2")))
	assert.Equal(t, "", f.coachOf("cl2"), "assignment refused by the seat rule")
	assert.Equal(t, []string{"c2", "owner"}, f.notifier.users)
	assert.Len(t, f.mailer.to, 2)
}

func TestManualAssignUnchangedByAddonPath(t *testing.T) {
	f := newDispatchFixture(t)
	ctx := context.Background()
	_, apiErr := f.svc.Assign(ctx, "owner", gymA, "cl2", "c2")
	require.Nil(t, apiErr)
	require.Len(t, f.tracker.transfers, 1)
	assert.Equal(t, "gym_assign", f.tracker.transfers[0].reason)
	_, apiErr = f.svc.Unassign(ctx, "owner", gymA, "cl2")
	require.Nil(t, apiErr)
	assert.Equal(t, "", f.coachOf("cl2"))
	_, apiErr = f.svc.Assign(ctx, "c1", gymA, "cl2", "c2")
	requireCode(t, apiErr, http.StatusForbidden)
}

func TestReleaseCoachClientsCallsCoachDeparted(t *testing.T) {
	f := newDispatchFixture(t)
	f.svc.ReleaseCoachClients(context.Background(), services.CoachRelease{GymID: gymA, CoachIDs: []string{"c1", "c2"}})
	assert.Equal(t, []string{gymA + "/c1,c2"}, f.billing.departed)

	// A coach without gym clients still ends his add-ons; empty gym id covers every gym.
	f.svc.ReleaseCoachClients(context.Background(), services.CoachRelease{CoachIDs: []string{"nobody"}})
	assert.Equal(t, "/nobody", f.billing.departed[1])

	// Failures are retried and never block the release.
	f.billing.departed = nil
	f.billing.departedErr = errors.New("payments down")
	f.svc.ReleaseCoachClients(context.Background(), services.CoachRelease{GymID: gymA, CoachIDs: []string{"c1"}})
	assert.Len(t, f.billing.departed, 3, "three attempts")
	assert.Equal(t, "", f.coachOf("cl1"), "the release still happened")

	f.billing.departed = nil
	f.svc.ReleaseCoachClients(context.Background(), services.CoachRelease{})
	assert.Empty(t, f.billing.departed, "no coaches, no call")
}

type fakePremiumSource struct {
	calls   [][]string
	clients []payments.GymAddonPremiumClient
	err     error
}

func (f *fakePremiumSource) GymAddonPremium(_ context.Context, _ string, ids []string) ([]payments.GymAddonPremiumClient, error) {
	f.calls = append(f.calls, ids)
	return f.clients, f.err
}

func premiumSource() *fakePremiumSource {
	since := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	return &fakePremiumSource{clients: []payments.GymAddonPremiumClient{
		{ClientID: "cl1", Since: &since, Addons: []payments.GymAddonPremiumAddon{{PlanID: "p1", PlanName: "Extra", CoachID: "c2"}}},
	}}
}

func TestPremiumAnnotatorFailOpen(t *testing.T) {
	ctx := context.Background()
	src := premiumSource()
	a := services.NewPremiumAnnotator(src, &fakeAddonUsers{})
	got := a.Lookup(ctx, gymA, []string{"cl1", "cl2"})
	require.Contains(t, got, "cl1")
	assert.NotContains(t, got, "cl2")
	assert.Equal(t, "First-c2 Last", got["cl1"].Addons[0].CoachName)
	assert.Equal(t, "c2", got["cl1"].Addons[0].CoachID)
	assert.Len(t, src.calls, 1, "one payments call for the page")

	src.err = errors.New("payments down")
	assert.Nil(t, a.Lookup(ctx, gymA, []string{"cl1"}), "payments error omits the fields")
}

func TestPremiumOnOwnerAndCoachLists(t *testing.T) {
	ctx := context.Background()

	t.Run("gym client list", func(t *testing.T) {
		f := newGymClientFixture(t, true)
		src := premiumSource()
		authz := services.NewChainAuthorizer(f.members, f.policy, services.NewResourceResolver(services.NewClientScopeSource(f.store)))
		svc := services.NewGymClientService(services.GymClientDeps{
			Authz: authz, Members: f.members, Store: f.store, Audit: f.audit, Quota: f.quota,
			Premium: services.NewPremiumAnnotator(src, &fakeAddonUsers{}),
		}, true)
		rows, _, apiErr := svc.ListClients(ctx, "owner", gymA, services.GymClientListQuery{})
		require.Nil(t, apiErr)
		require.Len(t, rows, 1)
		assert.True(t, rows[0].Premium)
		assert.NotNil(t, rows[0].PremiumSince)
		require.Len(t, rows[0].PremiumAddons, 1)
		assert.Equal(t, "First-c2 Last", rows[0].PremiumAddons[0].CoachName)

		src.err = errors.New("payments down")
		rows, _, apiErr = svc.ListClients(ctx, "owner", gymA, services.GymClientListQuery{})
		require.Nil(t, apiErr, "payments failure never fails the list")
		assert.False(t, rows[0].Premium)
	})

	t.Run("coach gym list", func(t *testing.T) {
		members := &fakeAuthzMembers{rows: []db.GymMember{member("c1", db.GymMemberRoleCoach)}}
		gym := &fakeRelGymStore{gymRows: []map[string]interface{}{
			{"externalid": "cl1", "gym_id": gymA}, {"externalid": "cl2", "gym_id": gymA},
		}}
		src := premiumSource()
		svc := services.NewCoachService(nil, nil, nil, nil, "").WithLinkStore(fakeLinks{}).WithGymRelations(gym, members).
			WithPremium(services.NewPremiumAnnotator(src, &fakeAddonUsers{}))
		rows, _, err := svc.ListCoachClients(ctx, db.CoachClientListQuery{CoachID: "c1", Kind: services.ClientKindGym})
		require.NoError(t, err)
		assert.Equal(t, true, rows[0]["premium"])
		assert.Contains(t, rows[0], "premium_since")
		assert.NotContains(t, rows[1], "premium")
		assert.Len(t, src.calls, 1, "one lookup per gym of the page")

		src.err = errors.New("payments down")
		gym.gymRows = []map[string]interface{}{{"externalid": "cl1", "gym_id": gymA}} // fresh rows: the fake hands out shared maps
		rows, _, err = svc.ListCoachClients(ctx, db.CoachClientListQuery{CoachID: "c1", Kind: services.ClientKindGym})
		require.NoError(t, err)
		assert.NotContains(t, rows[0], "premium")
	})
}
