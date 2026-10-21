//go:build integration

package repositories_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"

	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
)

const gymOwner = "gym-owner-1"

// gymFixture builds on the team fixture: a gym with head coaches hA (team c1 c2 c3) and hB (team c4).
type gymFixture struct {
	*fixture
	gyms *repositories.GymRepository
	gym  *db.Gym
}

func setupGym(t *testing.T) *gymFixture {
	t.Helper()
	f := setup(t)
	ctx := context.Background()
	_, err := f.database.Collection("users").InsertOne(ctx, bson.M{
		"externalid": gymOwner, "firstname": "Olga", "lastname": "Owner", "email": "olga@hctest",
		"profile_picture": "olga.png", "role": db.RoleGymOwner, "status": "Active", "created_at": f.now, "updated_at": f.now,
	})
	require.NoError(t, err)

	gf := &gymFixture{fixture: f, gyms: repositories.NewGymRepository(f.database, f.team)}
	gf.gym = &db.Gym{
		Name: "Iron Gym", OwnerID: gymOwner, Status: db.GymStatusActive,
		DefaultCurrency: "TND", Timezone: "Africa/Tunis", CreatedBy: "admin1", CreatedAt: f.now, UpdatedAt: f.now,
	}
	require.NoError(t, gf.gyms.CreateGym(ctx, gf.gym))
	require.False(t, gf.gym.ID.IsZero())
	for _, h := range []string{hA, hB} {
		require.NoError(t, gf.gyms.AddHeadCoach(ctx, &db.GymHeadCoach{
			GymID: gf.gym.ID.Hex(), HeadCoachID: h, AddedBy: "admin1", AddedByRole: db.GymAdderAdmin,
		}, db.PeriodReasonGymAdd))
	}
	return gf
}

func TestGymHeadCoachBelongsToOneGym(t *testing.T) {
	f := setupGym(t)
	ctx := context.Background()

	other := &db.Gym{Name: "Other", OwnerID: gymOwner, Status: db.GymStatusActive, CreatedAt: f.now}
	require.NoError(t, f.gyms.CreateGym(ctx, other))

	err := f.gyms.AddHeadCoach(ctx, &db.GymHeadCoach{GymID: other.ID.Hex(), HeadCoachID: hA, AddedBy: "admin1", AddedByRole: db.GymAdderAdmin}, db.PeriodReasonGymAdd)
	assert.ErrorIs(t, err, repositories.ErrHeadCoachInGym)

	// The opening of the membership opened exactly one gym period.
	p, err := f.team.Periods().OpenForMember(ctx, db.PeriodKindGym, hA)
	require.NoError(t, err)
	require.NotNil(t, p)
	assert.Equal(t, f.gym.ID.Hex(), p.ParentID)

	m, err := f.gyms.GetGymOfHead(ctx, hA)
	require.NoError(t, err)
	require.NotNil(t, m)
	assert.Equal(t, f.gym.ID.Hex(), m.GymID)

	removed, err := f.gyms.RemoveHeadCoach(ctx, other.ID.Hex(), hA, db.PeriodReasonGymRemove)
	require.NoError(t, err)
	assert.False(t, removed, "wrong gym removes nothing")

	removed, err = f.gyms.RemoveHeadCoach(ctx, f.gym.ID.Hex(), hA, db.PeriodReasonGymRemove)
	require.NoError(t, err)
	assert.True(t, removed)
	p, err = f.team.Periods().OpenForMember(ctx, db.PeriodKindGym, hA)
	require.NoError(t, err)
	assert.Nil(t, p, "period closed")
	ps, err := f.team.Periods().ListForMember(ctx, db.PeriodKindGym, hA)
	require.NoError(t, err)
	require.Len(t, ps, 1)
	assert.Equal(t, db.PeriodReasonGymRemove, ps[0].Reason)

	// Free again: the head coach can join another gym.
	require.NoError(t, f.gyms.AddHeadCoach(ctx, &db.GymHeadCoach{GymID: other.ID.Hex(), HeadCoachID: hA, AddedBy: "admin1", AddedByRole: db.GymAdderAdmin}, db.PeriodReasonGymAdd))
}

func TestGymReleaseAndArchive(t *testing.T) {
	f := setupGym(t)
	ctx := context.Background()

	gid, ok, err := f.gyms.ReleaseHead(ctx, hB, db.PeriodReasonGymLeave)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, f.gym.ID.Hex(), gid)
	_, ok, err = f.gyms.ReleaseHead(ctx, hB, db.PeriodReasonGymLeave)
	require.NoError(t, err)
	assert.False(t, ok)

	ids, err := f.gyms.RemoveAllByGym(ctx, f.gym.ID.Hex(), db.PeriodReasonGymArchive)
	require.NoError(t, err)
	assert.Equal(t, []string{hA}, ids)
	p, err := f.team.Periods().OpenForMember(ctx, db.PeriodKindGym, hA)
	require.NoError(t, err)
	assert.Nil(t, p)
}

func TestAccountDeletionLeavesGym(t *testing.T) {
	f := setupGym(t)
	ctx := context.Background()

	_, err := f.team.DeleteByAny(ctx, hA)
	require.NoError(t, err)
	m, err := f.gyms.GetGymOfHead(ctx, hA)
	require.NoError(t, err)
	assert.Nil(t, m)
	ps, err := f.team.Periods().ListForMember(ctx, db.PeriodKindGym, hA)
	require.NoError(t, err)
	require.Len(t, ps, 1)
	assert.Equal(t, db.PeriodReasonAccountGone, ps[0].Reason)
	assert.NotNil(t, ps[0].End)
}

func TestGymInvitationUniquePending(t *testing.T) {
	f := setupGym(t)
	ctx := context.Background()
	gid := f.gym.ID.Hex()
	newInv := func(email string) *db.GymInvitation {
		return &db.GymInvitation{GymID: gid, InviterID: gymOwner, Email: email, Status: db.GymInvitePending,
			ExpiresAt: f.now.Add(time.Hour), CreatedAt: f.now}
	}

	first := newInv("a@x.tn")
	require.NoError(t, f.gyms.InsertInvitation(ctx, first))
	assert.ErrorIs(t, f.gyms.InsertInvitation(ctx, newInv("a@x.tn")), repositories.ErrInvitationPending)
	require.NoError(t, f.gyms.InsertInvitation(ctx, newInv("b@x.tn")), "other email")

	// Not pending anymore: a new pending one is allowed (partial unique index).
	ok, err := f.gyms.TransitionInvitation(ctx, first.ID.Hex(), db.GymInvitePending, db.GymInviteRevoked, "", f.now)
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = f.gyms.TransitionInvitation(ctx, first.ID.Hex(), db.GymInvitePending, db.GymInviteAccepted, "c1", f.now)
	require.NoError(t, err)
	assert.False(t, ok, "only a pending invitation can move")
	require.NoError(t, f.gyms.InsertInvitation(ctx, newInv("a@x.tn")))

	n, err := f.gyms.CountInvitationsSince(ctx, gid, f.now.Add(-time.Minute))
	require.NoError(t, err)
	assert.Equal(t, 3, n)

	// Expiry is enforced by the service through ExpireDue.
	old := newInv("old@x.tn")
	old.ExpiresAt = f.now.Add(-time.Minute)
	require.NoError(t, f.gyms.InsertInvitation(ctx, old))
	require.NoError(t, f.gyms.ExpireDue(ctx, "", "old@x.tn", f.now))
	got, err := f.gyms.GetInvitation(ctx, old.ID.Hex())
	require.NoError(t, err)
	assert.Equal(t, db.GymInviteExpired, got.Status)
}

func TestGymPendingForEmail(t *testing.T) {
	f := setupGym(t)
	ctx := context.Background()
	gid := f.gym.ID.Hex()

	require.NoError(t, f.gyms.InsertInvitation(ctx, &db.GymInvitation{GymID: gid, InviterID: gymOwner, Email: "me@x.tn",
		Status: db.GymInvitePending, ExpiresAt: f.now.Add(time.Hour), CreatedAt: f.now}))
	require.NoError(t, f.gyms.InsertInvitation(ctx, &db.GymInvitation{GymID: gid, InviterID: gymOwner, Email: "late@x.tn",
		Status: db.GymInvitePending, ExpiresAt: f.now.Add(-time.Hour), CreatedAt: f.now}))

	views, err := f.gyms.ListPendingForEmail(ctx, "me@x.tn", f.now)
	require.NoError(t, err)
	require.Len(t, views, 1)
	assert.Equal(t, "Iron Gym", views[0].GymName)
	assert.Equal(t, "Olga Owner", views[0].OwnerName)
	assert.NotEmpty(t, views[0].ID)

	views, err = f.gyms.ListPendingForEmail(ctx, "late@x.tn", f.now)
	require.NoError(t, err)
	assert.Empty(t, views, "expired invitations are not listed")

	archived := db.GymStatusArchived
	_, err = f.gyms.UpdateGym(ctx, gid, db.GymUpdate{Status: &archived})
	require.NoError(t, err)
	views, err = f.gyms.ListPendingForEmail(ctx, "me@x.tn", f.now)
	require.NoError(t, err)
	assert.Empty(t, views, "archived gym")
}

func TestGymAggregations(t *testing.T) {
	f := setupGym(t)
	ctx := context.Background()
	gid := f.gym.ID.Hex()

	t.Run("overview", func(t *testing.T) {
		o, err := f.gyms.Overview(ctx, gid)
		require.NoError(t, err)
		assert.Equal(t, 2, o.HeadCoaches)
		assert.Equal(t, 4, o.Coaches, "c1 c2 c3 c4; the orphan membership has no user")
		assert.Equal(t, 3, o.ActiveCoaches)
		assert.Equal(t, 2, o.InactiveCoaches14d)
		assert.Equal(t, 7, o.TotalClients)
		assert.Equal(t, 4, o.ActiveClients)
		assert.Equal(t, 5, o.NewClients30d)
	})

	t.Run("overview of an unknown gym is empty", func(t *testing.T) {
		o, err := f.gyms.Overview(ctx, "000000000000000000000000")
		require.NoError(t, err)
		assert.Equal(t, &db.GymOverview{}, o)
	})

	t.Run("coaches", func(t *testing.T) {
		rows, total, err := f.gyms.ListCoaches(ctx, gid, db.GymCoachQuery{})
		require.NoError(t, err)
		assert.Equal(t, 4, total)
		require.Len(t, rows, 4)
		for _, r := range rows {
			assert.NotEmpty(t, r.HeadCoachID)
			assert.NotEmpty(t, r.HeadCoachName)
		}

		rows, total, err = f.gyms.ListCoaches(ctx, gid, db.GymCoachQuery{HeadCoachID: hB})
		require.NoError(t, err)
		assert.Equal(t, 1, total)
		assert.Equal(t, c4, rows[0].ExternalID)
		assert.Equal(t, "Bob Boss", rows[0].HeadCoachName)
		assert.Equal(t, 1, rows[0].ClientCount)

		rows, _, err = f.gyms.ListCoaches(ctx, gid, db.GymCoachQuery{Sort: "clients", Size: 1})
		require.NoError(t, err)
		require.Len(t, rows, 1)
		assert.Equal(t, 3, rows[0].ClientCount)

		rows, total, err = f.gyms.ListCoaches(ctx, gid, db.GymCoachQuery{Q: "ZED"})
		require.NoError(t, err)
		assert.Equal(t, 1, total)
		assert.Equal(t, c1, rows[0].ExternalID)

		rows, total, err = f.gyms.ListCoaches(ctx, gid, db.GymCoachQuery{Status: "Inactive"})
		require.NoError(t, err)
		assert.Equal(t, 1, total)
		assert.Equal(t, c2, rows[0].ExternalID)
	})

	t.Run("head coaches", func(t *testing.T) {
		rows, total, err := f.gyms.ListHeadCoachRows(ctx, gid, "", 1, 20)
		require.NoError(t, err)
		assert.Equal(t, 2, total)
		require.Len(t, rows, 2)
		assert.Equal(t, hA, rows[0].ExternalID, "Alice before Bob")
		assert.Equal(t, 6, rows[0].ClientCount)
		assert.Equal(t, 1, rows[1].ClientCount)
		assert.False(t, rows[0].AddedAt.IsZero())

		rows, total, err = f.gyms.ListHeadCoachRows(ctx, gid, "bob", 1, 20)
		require.NoError(t, err)
		assert.Equal(t, 1, total)
		assert.Equal(t, hB, rows[0].ExternalID)
	})

	t.Run("clients", func(t *testing.T) {
		rows, total, err := f.gyms.ListClients(ctx, gid, db.GymClientQuery{})
		require.NoError(t, err)
		assert.Equal(t, 6, total, "the link to a missing client user is skipped")
		assert.Len(t, rows, 6)
		for _, r := range rows {
			assert.NotEmpty(t, r.CoachID)
			assert.NotEmpty(t, r.CoachName)
			assert.NotEmpty(t, r.HeadCoachID)
			assert.False(t, r.LinkedAt.IsZero())
		}

		rows, total, err = f.gyms.ListClients(ctx, gid, db.GymClientQuery{CoachID: c1, Sort: "linked_at"})
		require.NoError(t, err)
		assert.Equal(t, 3, total)
		assert.Equal(t, "cl1", rows[0].ExternalID, "newest link first")

		rows, total, err = f.gyms.ListClients(ctx, gid, db.GymClientQuery{HeadCoachID: hB})
		require.NoError(t, err)
		assert.Equal(t, 1, total)
		assert.Equal(t, "cl6", rows[0].ExternalID)

		rows, total, err = f.gyms.ListClients(ctx, gid, db.GymClientQuery{Page: 2, Size: 4})
		require.NoError(t, err)
		assert.Equal(t, 6, total)
		assert.Len(t, rows, 2)

		// A coach outside the gym's teams yields nothing.
		_, total, err = f.gyms.ListClients(ctx, gid, db.GymClientQuery{CoachID: c5})
		require.NoError(t, err)
		assert.Equal(t, 0, total)
	})

	t.Run("owner and admin lists", func(t *testing.T) {
		mine, err := f.gyms.ListGymsByOwner(ctx, gymOwner)
		require.NoError(t, err)
		require.Len(t, mine, 1)
		assert.Equal(t, gid, mine[0].GymID)
		assert.Equal(t, 2, mine[0].HeadCoachCount)

		rows, total, err := f.gyms.ListGymsAdmin(ctx, 1, 20, "iron", "")
		require.NoError(t, err)
		assert.Equal(t, 1, total)
		assert.Equal(t, "Olga Owner", rows[0].OwnerName)
		assert.Equal(t, "olga@hctest", rows[0].OwnerEmail)
		assert.Equal(t, "olga.png", rows[0].OwnerPicture)

		// status filter applies to the rows and the total.
		_, total, err = f.gyms.ListGymsAdmin(ctx, 1, 20, "", db.GymStatusArchived)
		require.NoError(t, err)
		assert.Equal(t, 0, total)
		rows, total, err = f.gyms.ListGymsAdmin(ctx, 1, 20, "", db.GymStatusActive)
		require.NoError(t, err)
		assert.Equal(t, 1, total)
		assert.Len(t, rows, 1)
		assert.Equal(t, 2, rows[0].HeadCoachCount)

		_, total, err = f.gyms.ListGymsAdmin(ctx, 1, 20, "nomatch", "")
		require.NoError(t, err)
		assert.Equal(t, 0, total)
	})

	t.Run("admin gym head coaches", func(t *testing.T) {
		hcs, err := f.gyms.ListGymHeadCoachesAdmin(ctx, gid)
		require.NoError(t, err)
		require.Len(t, hcs, 2)
		byID := map[string]db.AdminGymHeadCoachRow{}
		for _, h := range hcs {
			byID[h.ExternalID] = h
		}
		assert.Equal(t, 4, byID[hA].TeamSize)
		assert.Equal(t, 1, byID[hB].TeamSize)
		assert.Equal(t, db.GymAdderAdmin, byID[hA].AddedByRole)
		assert.False(t, byID[hA].AddedAt.IsZero())
		assert.NotEmpty(t, byID[hA].Email)

		none, err := f.gyms.ListGymHeadCoachesAdmin(ctx, "no-such-gym")
		require.NoError(t, err)
		assert.Empty(t, none)

		_, err = f.gyms.RemoveHeadCoach(ctx, gid, hB, db.PeriodReasonGymRemove)
		require.NoError(t, err)
		hcs, err = f.gyms.ListGymHeadCoachesAdmin(ctx, gid)
		require.NoError(t, err)
		require.Len(t, hcs, 1, "only current head coaches")
		assert.Equal(t, hA, hcs[0].ExternalID)
	})

	t.Run("admin head coach list carries the gym", func(t *testing.T) {
		rows, _, err := f.users.ListHeadCoaches(ctx, 1, 20, "")
		require.NoError(t, err)
		require.NotEmpty(t, rows)
		for _, r := range rows {
			if r.ExternalID == hA {
				assert.Equal(t, gid, r.GymID)
				assert.Equal(t, "Iron Gym", r.GymName)
			}
			if r.ExternalID == hB {
				assert.Empty(t, r.GymID, "removed from the gym")
				assert.Empty(t, r.GymName)
			}
		}
	})

	t.Run("audit by gym", func(t *testing.T) {
		require.NoError(t, f.gyms.InsertAudit(ctx, &db.TeamAuditEntry{
			Action: db.TeamActionGymCreate, ActorID: "admin1", ActorRole: db.TeamActorRoleAdmin, GymID: gid}))
		entries, total, err := f.gyms.ListAudit(ctx, gid, 1, 20)
		require.NoError(t, err)
		assert.Equal(t, 1, total)
		assert.Equal(t, db.TeamActionGymCreate, entries[0].Action)
	})
}
