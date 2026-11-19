package services_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
	"coachify-account-api/pkg/payments"
	"coachify-account-api/services"
)

func ownerAsCoach(t *testing.T, on bool) {
	t.Helper()
	viper.Set(db.GymOwnerAsCoachFlag, on)
	t.Cleanup(func() { viper.Set(db.GymOwnerAsCoachFlag, nil) })
}

func TestGymOwnerEffectiveFlagOffBypass(t *testing.T) {
	ownerAsCoach(t, false)
	e := newEntEnv()
	got := eff(t, e, "gym")
	for _, def := range services.Features() {
		st := got.Features[def.Key]
		assert.True(t, st.Enabled, def.Key)
		assert.Equal(t, services.SourceDefault, st.Source)
		for _, l := range def.Limits {
			assert.Nil(t, st.Limits[l], "bypass has no limits: %s.%s", def.Key, l)
		}
	}
}

func TestGymOwnerEffectiveFlagOnMirrorsCoach(t *testing.T) {
	ownerAsCoach(t, true)
	e := newEntEnv()
	e.store.defaults["_coach"] = db.PlanDefault{PlanID: "_coach", Features: map[string]db.FeatureGrant{
		"nutrition": {Enabled: bptr(false)},
	}}
	assert.Equal(t, eff(t, e, "c1").Features, eff(t, e, "gym").Features, "no _gym_owner row: same as coach")
	assert.False(t, eff(t, e, "gym").Features["nutrition"].Enabled)

	// _gym_owner layers over _coach, field level
	_, apiErr := e.svc.SetPlanDefault(context.Background(), "adm", "_gym_owner", api.PlanDefaultRequest{Features: map[string]api.FeaturePatch{"nutrition": enabledPatch(true)}})
	require.Nil(t, apiErr)
	assert.True(t, eff(t, e, "gym").Features["nutrition"].Enabled)
	assert.False(t, eff(t, e, "c1").Features["nutrition"].Enabled, "coach untouched by _gym_owner")
}

func TestGymOwnerPlanDefaultAdminEditable(t *testing.T) {
	ownerAsCoach(t, true)
	ctx := context.Background()
	patch := map[string]api.FeaturePatch{"nutrition": enabledPatch(false)}

	e := newEntEnv()
	pd, apiErr := e.svc.SetPlanDefault(ctx, "adm", "_gym_owner", api.PlanDefaultRequest{Features: patch, Reason: "tier"})
	require.Nil(t, apiErr)
	assert.Equal(t, "_gym_owner", pd.PlanID)
	_, ok := e.store.defaults["_coach"]
	assert.False(t, ok, "editing _gym_owner never writes _coach")

	// a second identical edit is a no-op and keeps the admin-edited value
	_, apiErr = e.svc.SetPlanDefault(ctx, "adm", "_gym_owner", api.PlanDefaultRequest{Features: patch})
	require.Nil(t, apiErr)
	require.NotNil(t, e.store.defaults["_gym_owner"].Features["nutrition"].Enabled)
	assert.False(t, *e.store.defaults["_gym_owner"].Features["nutrition"].Enabled)

	// an unknown id is still rejected (_gym_seat became valid in P8a)
	_, apiErr = e.svc.SetPlanDefault(ctx, "adm", "_unknown_key", api.PlanDefaultRequest{Features: patch})
	require.NotNil(t, apiErr)
}

func TestGymOwnerCoachAccessAdminGate(t *testing.T) {
	ctx := context.Background()
	ownerAsCoach(t, false)
	e := newEntEnv()
	_, apiErr := e.svc.GetCoachAccess(ctx, "gym")
	require.NotNil(t, apiErr, "flag off: owner is not a coach")
	ownerAsCoach(t, true)
	_, apiErr = e.svc.GetCoachAccess(ctx, "gym")
	assert.Nil(t, apiErr)
}

func TestGymOwnerPlanDefaultRejectedWhileFlagOff(t *testing.T) {
	ownerAsCoach(t, false)
	e := newEntEnv()
	_, apiErr := e.svc.SetPlanDefault(context.Background(), "adm", "_gym_owner",
		api.PlanDefaultRequest{Features: map[string]api.FeaturePatch{"nutrition": enabledPatch(false)}})
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.Code)
	assert.Contains(t, apiErr.Error.Error(), "invalid plan id")
	_, ok := e.store.defaults["_gym_owner"]
	assert.False(t, ok)
}

// Layer order for a gym owner acting as coach: _coach < _gym_owner < plan < grant.
func TestGymOwnerLayerOrder(t *testing.T) {
	const planID = "64b64b64b64b64b64b64b64b"
	ownerAsCoach(t, true)
	e := newEntEnv()
	e.plans.byUser["gym"] = payments.SubscriptionSummaryEntry{UserID: "gym", PlanID: planID, PlanName: "Pro", Status: "active"}
	e.store.defaults["_coach"] = db.PlanDefault{PlanID: "_coach", Features: map[string]db.FeatureGrant{
		"workouts":  {Enabled: bptr(false)},
		"nutrition": {Enabled: bptr(false)},
		"chat":      {Enabled: bptr(false)},
	}}
	e.store.defaults["_gym_owner"] = db.PlanDefault{PlanID: "_gym_owner", Features: map[string]db.FeatureGrant{
		"workouts":  {Enabled: bptr(true)},  // beats _coach
		"nutrition": {Enabled: bptr(true)},  // beaten by the plan
		"chat":      {Enabled: bptr(false)}, // beaten by the grant
	}}
	e.store.defaults[planID] = db.PlanDefault{PlanID: planID, Features: map[string]db.FeatureGrant{
		"nutrition": {Enabled: bptr(false)},
		"chat":      {Enabled: bptr(true)},
	}}
	e.store.grants["gym"] = &db.EntitlementGrant{SubjectID: "gym", Features: map[string]db.FeatureGrant{
		"chat": {Enabled: bptr(false)},
	}}
	f := eff(t, e, "gym").Features
	assert.True(t, f["workouts"].Enabled, "_gym_owner over _coach")
	assert.False(t, f["nutrition"].Enabled, "plan over _gym_owner")
	assert.False(t, f["chat"].Enabled, "grant over plan")
	assert.Equal(t, services.SourceOverride, f["chat"].Source)
}
