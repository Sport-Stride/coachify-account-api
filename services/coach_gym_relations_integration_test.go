//go:build integration

package services_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"

	"coachify-account-api/models/db"
	"coachify-account-api/repositories"
	"coachify-account-api/services"
)

// Relationships and the ListCoachClients enrichment over real repositories and a real MongoDB.
func TestRelationshipsAndCoachListEnrichmentIntegration(t *testing.T) {
	e := newMemberEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	d := e.database
	now := time.Now()

	coachRepo := repositories.NewCoachRepository(d, "coach_clients", d.Collection("users"))
	gcRepo := repositories.NewGymClientRepository(d, "users")
	reader := services.NewRepoMembershipReader(e.members)

	member := func(gym, user string, role db.GymMemberRole) {
		m := &db.GymMember{GymID: gym, UserID: user, Role: role, Status: db.GymMemberStatusActive, JoinedAt: now, AddedBy: "x", Source: db.GymMemberSource("test")}
		require.NoError(t, e.members.UpsertActive(ctx, m))
	}
	gymRow := func(gym, client, coach string) {
		require.NoError(t, gcRepo.Insert(ctx, &db.GymClient{GymID: gym, ClientID: client, CoachID: coach,
			Status: db.GymClientStatusActive, JoinedAt: now, ConsentAt: now}))
	}
	personal := func(coach, client string) {
		_, err := d.Collection("coach_clients").InsertOne(ctx, bson.M{"coach_id": coach, "client_id": client, "created_at": now})
		require.NoError(t, err)
	}
	for _, c := range []string{"cPers", "cGym", "cDual", "cOther", "cLeft", "cMulti", "cOwned", "cNone"} {
		_, err := d.Collection("users").InsertOne(ctx, bson.M{"externalid": c, "firstname": c, "lastname": "T"})
		require.NoError(t, err)
	}

	// Gyms are inserted out of order on purpose: the expected output is sorted by gym id.
	member("gA", "plain", db.GymMemberRoleCoach)
	member("gB", "owner", db.GymMemberRoleOwner)
	member("gC", "owner", db.GymMemberRoleOwner)
	member("gA2", "owner", db.GymMemberRoleOwner)
	member("gH", "head", db.GymMemberRoleHeadCoach)
	member("gL", "leaver", db.GymMemberRoleCoach)
	ok, err := e.members.MarkLeft(ctx, "gL", "leaver", now)
	require.NoError(t, err)
	require.True(t, ok)

	personal("plain", "cPers")
	personal("plain", "cDual")
	gymRow("gA", "cGym", "plain")
	gymRow("gA", "cDual", "plain")
	gymRow("gA", "cOther", "someoneElse") // a plain coach must not see another coach's row
	gymRow("gL", "cLeft", "leaver")       // the coach has left the gym: row invisible
	personal("leaver", "cLeft")
	gymRow("gC", "cMulti", "x1")
	gymRow("gB", "cMulti", "")
	gymRow("gA2", "cMulti", "x2")
	gymRow("gH", "cOwned", "someoneElse") // a head coach sees any row of his gym

	gymSvc := services.NewGymClientService(services.GymClientDeps{Members: reader, Store: gcRepo, Links: coachRepo}, true)
	coachSvc := services.NewCoachService(coachRepo, nil, nil, nil, "").WithGymRelations(coachRepo, reader)

	type rel = services.GymRelation
	t.Run("Relationships", func(t *testing.T) {
		cases := []struct {
			name, actor, client string
			personal            bool
			gyms                []rel
		}{
			{"personal only", "plain", "cPers", true, []rel{}},
			{"gym only", "plain", "cGym", false, []rel{{GymID: "gA", ActorRole: "coach", AssignedToActor: true}}},
			{"dual", "plain", "cDual", true, []rel{{GymID: "gA", ActorRole: "coach", AssignedToActor: true}}},
			{"plain coach, other coach's row hidden", "plain", "cOther", false, []rel{}},
			{"left member: no gym, personal kept", "leaver", "cLeft", true, []rel{}},
			{"owner sees any row, sorted by gym id", "owner", "cMulti", false, []rel{
				{GymID: "gA2", ActorRole: "owner"}, {GymID: "gB", ActorRole: "owner"}, {GymID: "gC", ActorRole: "owner"}}},
			{"head coach sees any row", "head", "cOwned", false, []rel{{GymID: "gH", ActorRole: "head_coach"}}},
			{"no relation", "plain", "cNone", false, []rel{}},
		}
		for _, c := range cases {
			got, apiErr := gymSvc.Relationships(ctx, c.actor, c.client)
			require.Nil(t, apiErr, c.name)
			assert.Equal(t, c.personal, got.Personal, c.name)
			assert.Equal(t, c.gyms, got.Gyms, c.name)
		}
	})

	t.Run("ListCoachClients enrichment", func(t *testing.T) {
		relsOf := func(rows []map[string]interface{}) map[string][]map[string]string {
			out := map[string][]map[string]string{}
			for _, r := range rows {
				out[r["externalid"].(string)] = r["relationships"].([]map[string]string)
			}
			return out
		}
		gym := func(id string) map[string]string { return map[string]string{"kind": "gym", "gym_id": id} }
		pers := map[string]string{"kind": "personal"}
		q := func(coach, kind string) db.CoachClientListQuery {
			return db.CoachClientListQuery{CoachID: coach, Kind: kind, Page: 1, Size: 50}
		}

		rows, total, err := coachSvc.ListCoachClients(ctx, q("plain", ""))
		require.NoError(t, err)
		assert.Equal(t, 2, total)
		assert.Equal(t, map[string][]map[string]string{"cPers": {pers}, "cDual": {pers, gym("gA")}}, relsOf(rows))

		rows, total, err = coachSvc.ListCoachClients(ctx, q("plain", services.ClientKindGym))
		require.NoError(t, err)
		assert.Equal(t, 2, total)
		assert.Equal(t, map[string][]map[string]string{"cGym": {gym("gA")}, "cDual": {gym("gA"), pers}}, relsOf(rows))

		// a left member keeps the personal row only
		rows, _, err = coachSvc.ListCoachClients(ctx, q("leaver", ""))
		require.NoError(t, err)
		assert.Equal(t, map[string][]map[string]string{"cLeft": {pers}}, relsOf(rows))

		// owner of gA2/gB/gC with a personal link to cMulti: personal first, then gyms sorted by id
		personal("owner", "cMulti")
		rows, _, err = coachSvc.ListCoachClients(ctx, q("owner", ""))
		require.NoError(t, err)
		assert.Equal(t, map[string][]map[string]string{"cMulti": {pers, gym("gA2"), gym("gB"), gym("gC")}}, relsOf(rows))

		// head coach with a personal link to a client of another coach's gym row: any row of his gym shows
		personal("head", "cOwned")
		rows, _, err = coachSvc.ListCoachClients(ctx, q("head", ""))
		require.NoError(t, err)
		assert.Equal(t, map[string][]map[string]string{"cOwned": {pers, gym("gH")}}, relsOf(rows))

		// a plain coach with a personal link to cOther still must not see the other coach's gym row
		personal("plain", "cOther")
		rows, _, err = coachSvc.ListCoachClients(ctx, q("plain", ""))
		require.NoError(t, err)
		assert.Equal(t, []map[string]string{pers}, relsOf(rows)["cOther"])
	})
}
