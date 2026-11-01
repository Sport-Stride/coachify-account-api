package services_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/mongo"

	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
	"coachify-account-api/pkg/payments"
	"coachify-account-api/repositories"
	"coachify-account-api/services"
)

// ---- fakes ----

type fakeEntStore struct {
	mu         sync.Mutex
	grants     map[string]*db.EntitlementGrant
	defaults   map[string]db.PlanDefault
	usage      map[string]*db.EntitlementUsage
	clients    map[string]int64
	grantReads int
}

func newFakeEntStore() *fakeEntStore {
	return &fakeEntStore{
		grants:   map[string]*db.EntitlementGrant{},
		defaults: map[string]db.PlanDefault{},
		usage:    map[string]*db.EntitlementUsage{},
		clients:  map[string]int64{},
	}
}

func (f *fakeEntStore) GetGrant(_ context.Context, id string) (*db.EntitlementGrant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.grantReads++
	if g, ok := f.grants[id]; ok {
		c := *g
		return &c, nil
	}
	return nil, nil
}

func (f *fakeEntStore) ReplaceGrant(_ context.Context, g *db.EntitlementGrant, prev int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur, ok := f.grants[g.SubjectID]
	if (!ok && prev != 0) || (ok && cur.Version != prev) {
		return repositories.ErrEntitlementVersionConflict
	}
	g.Version = prev + 1
	c := *g
	f.grants[g.SubjectID] = &c
	return nil
}

func (f *fakeEntStore) ListPlanDefaults(context.Context) ([]db.PlanDefault, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []db.PlanDefault{}
	for _, pd := range f.defaults {
		out = append(out, pd)
	}
	return out, nil
}

func (f *fakeEntStore) UpsertPlanDefault(_ context.Context, pd *db.PlanDefault) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.defaults[pd.PlanID] = *pd
	return nil
}

func (f *fakeEntStore) GetUsage(_ context.Context, ids []string) (map[string]*db.EntitlementUsage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]*db.EntitlementUsage{}
	for _, id := range ids {
		if u, ok := f.usage[id]; ok {
			out[id] = copyUsage(u)
		}
	}
	return out, nil
}

func copyUsage(u *db.EntitlementUsage) *db.EntitlementUsage {
	c := &db.EntitlementUsage{SubjectID: u.SubjectID, Counters: map[string]map[string]int64{}}
	for f, m := range u.Counters {
		c.Counters[f] = map[string]int64{}
		for k, v := range m {
			c.Counters[f][k] = v
		}
	}
	return c
}

// ConsumeUsage mirrors the repository contract: all-or-nothing under the lock.
func (f *fakeEntStore) ConsumeUsage(_ context.Context, id, feature string, deltas, maxes map[string]int64) (*db.EntitlementUsage, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.usage[id]
	if !ok {
		u = &db.EntitlementUsage{SubjectID: id, Counters: map[string]map[string]int64{feature: {}}}
		for c := range deltas {
			u.Counters[feature][c] = 0
		}
		f.usage[id] = u
	}
	if u.Counters[feature] == nil {
		u.Counters[feature] = map[string]int64{}
	}
	for c := range deltas {
		if _, ok := u.Counters[feature][c]; !ok {
			u.Counters[feature][c] = 0
		}
	}
	for c, max := range maxes {
		if u.Counters[feature][c] > max-deltas[c] {
			return nil, false, nil
		}
	}
	for c, d := range deltas {
		u.Counters[feature][c] += d
	}
	return copyUsage(u), true, nil
}

func (f *fakeEntStore) ReleaseUsage(_ context.Context, id, feature string, deltas map[string]int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.usage[id]; ok {
		for c, d := range deltas {
			if v := u.Counters[feature][c] - d; v > 0 {
				u.Counters[feature][c] = v
			} else {
				u.Counters[feature][c] = 0
			}
		}
	}
	return nil
}

func (f *fakeEntStore) CountClients(_ context.Context, coachID string) (int64, error) {
	return f.clients[coachID], nil
}

func (f *fakeEntStore) ListCoachAccess(context.Context, db.CoachAccessQuery) ([]db.CoachAccessRow, int, error) {
	return nil, 0, nil
}

func (f *fakeEntStore) ListClientAccess(context.Context, string, int, int, string) ([]db.ClientAccessRow, int, error) {
	return nil, 0, nil
}

type fakeEntUsers map[string]string // id -> role

func (f fakeEntUsers) GetUserById(_ context.Context, id string) (*db.User, *models.ApiError) {
	role, ok := f[id]
	if !ok {
		return nil, models.NewApiError(http.StatusNotFound, models.ErrUserNotFound)
	}
	return &db.User{ExternalID: id, UserRole: role, UserFirstname: "F", UserLastname: "L", UserEmail: id + "@x.io"}, nil
}

type fakeEntLinks map[string]string // client -> coach

func (f fakeEntLinks) GetCoachIDByClientID(_ context.Context, client string) (string, error) {
	if c, ok := f[client]; ok {
		return c, nil
	}
	return "", mongo.ErrNoDocuments
}

func (f fakeEntLinks) IsClientOfCoach(_ context.Context, coach, client string) (bool, error) {
	return f[client] == coach, nil
}

type fakeEntPlans struct {
	byUser map[string]payments.SubscriptionSummaryEntry
	err    error
	calls  int
}

func (f *fakeEntPlans) GetSubscriptionSummaries(_ context.Context, ids []string) ([]payments.SubscriptionSummaryEntry, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	var out []payments.SubscriptionSummaryEntry
	for _, id := range ids {
		if e, ok := f.byUser[id]; ok {
			out = append(out, e)
		}
	}
	return out, nil
}

type fakeEntAudit struct{ entries []db.AdminAuditEntry }

func (f *fakeEntAudit) Insert(_ context.Context, e *db.AdminAuditEntry) error {
	f.entries = append(f.entries, *e)
	return nil
}

func (f *fakeEntAudit) List(_ context.Context, flt db.AdminAuditFilter, _, _ int) ([]db.AdminAuditEntry, int, error) {
	var out []db.AdminAuditEntry
	for _, e := range f.entries {
		if flt.TargetID == "" || e.TargetID == flt.TargetID {
			out = append(out, e)
		}
	}
	return out, len(out), nil
}

type entEnv struct {
	svc    services.EntitlementService
	guard  services.EntitlementGuard
	store  *fakeEntStore
	users  fakeEntUsers
	links  fakeEntLinks
	plans  *fakeEntPlans
	audit  *fakeEntAudit
	coach  string
	client string
}

func newEntEnv() *entEnv {
	e := &entEnv{
		store:  newFakeEntStore(),
		users:  fakeEntUsers{"c1": "coach", "k1": "client", "adm": "admin", "gym": "gym_owner", "weird": "visitor", "n1": "nutritionist"},
		links:  fakeEntLinks{"k1": "c1"},
		plans:  &fakeEntPlans{byUser: map[string]payments.SubscriptionSummaryEntry{}},
		audit:  &fakeEntAudit{},
		coach:  "c1",
		client: "k1",
	}
	impl := services.NewEntitlementService(e.store, e.users, e.links, e.plans, e.audit)
	e.svc, e.guard = impl, impl
	return e
}

func i64(v int64) *int64 { return &v }
func bptr(v bool) *bool  { return &v }

func entErr(t *testing.T, apiErr *models.ApiError) *models.EntitlementError {
	t.Helper()
	require.NotNil(t, apiErr)
	var ee *models.EntitlementError
	require.True(t, errors.As(apiErr.Error, &ee), "error is not an EntitlementError: %v", apiErr.Error)
	return ee
}

func eff(t *testing.T, e *entEnv, id string) *services.Effective {
	t.Helper()
	out, apiErr := e.svc.Effective(context.Background(), id)
	require.Nil(t, apiErr)
	return out
}

func enabledPatch(v bool) api.FeaturePatch {
	return api.FeaturePatch{Enabled: api.OptBool{Set: true, Value: bptr(v)}}
}

func limitPatch(l string, v *int64) api.FeaturePatch {
	return api.FeaturePatch{Limits: map[string]*int64{l: v}}
}

// ---- resolver ----

func TestResolveDefaults(t *testing.T) {
	e := newEntEnv()
	for _, id := range []string{"c1", "k1", "n1"} {
		got := eff(t, e, id)
		assert.Equal(t, 60, got.TTLSeconds)
		assert.Nil(t, got.Plan)
		for _, def := range services.Features() {
			st := got.Features[def.Key]
			applies := false
			for _, r := range def.AppliesTo {
				if (r == "coach") == (id != "k1") {
					applies = true
				}
			}
			if applies {
				assert.True(t, st.Enabled, "%s %s", id, def.Key)
				assert.Equal(t, services.SourceDefault, st.Source)
				for _, l := range def.Limits {
					v, present := st.Limits[l]
					assert.True(t, present)
					assert.Nil(t, v, "default limits are unlimited")
				}
			} else {
				assert.False(t, st.Enabled)
				assert.Equal(t, services.SourceNotApplic, st.Source)
			}
		}
	}
}

func TestResolveBypassAndNA(t *testing.T) {
	e := newEntEnv()
	for _, id := range []string{"adm", "gym"} {
		got := eff(t, e, id)
		for _, def := range services.Features() {
			assert.True(t, got.Features[def.Key].Enabled, "%s %s", id, def.Key)
			assert.Equal(t, services.SourceDefault, got.Features[def.Key].Source)
		}
	}
	got := eff(t, e, "weird")
	for _, def := range services.Features() {
		assert.False(t, got.Features[def.Key].Enabled)
		assert.Equal(t, services.SourceNotApplic, got.Features[def.Key].Source)
	}
	_, apiErr := e.svc.Effective(context.Background(), "ghost")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusNotFound, apiErr.Code)
}

func TestResolveLayers(t *testing.T) {
	const planID = "64b64b64b64b64b64b64b64b"
	ctx := context.Background()

	t.Run("role default then plan then override, field level", func(t *testing.T) {
		e := newEntEnv()
		e.plans.byUser["c1"] = payments.SubscriptionSummaryEntry{UserID: "c1", PlanID: planID, PlanName: "Pro", Status: "active"}
		e.store.defaults["_coach"] = db.PlanDefault{PlanID: "_coach", Features: map[string]db.FeatureGrant{
			"workouts":     {Enabled: bptr(false)},
			"file_manager": {Limits: map[string]int64{"max_uploads": 10, "max_bytes": 1000}},
		}}
		e.store.defaults[planID] = db.PlanDefault{PlanID: planID, Features: map[string]db.FeatureGrant{
			"workouts":     {Enabled: bptr(true)}, // plan layer beats the role layer
			"file_manager": {Limits: map[string]int64{"max_bytes": 500}},
		}}
		e.store.grants["c1"] = &db.EntitlementGrant{SubjectID: "c1", Features: map[string]db.FeatureGrant{
			"file_manager": {Limits: map[string]int64{"max_uploads": 3}},
		}}
		got := eff(t, e, "c1")
		require.NotNil(t, got.Plan)
		assert.Equal(t, "Pro", got.Plan.Name)
		assert.True(t, got.Features["workouts"].Enabled)
		assert.Equal(t, services.SourcePlan, got.Features["workouts"].Source)
		fm := got.Features["file_manager"]
		assert.Equal(t, int64(3), *fm.Limits["max_uploads"], "override beats role default")
		assert.Equal(t, int64(500), *fm.Limits["max_bytes"], "limits merge independently: plan beats role default")
		assert.Equal(t, services.SourceOverride, fm.Source)
		assert.Equal(t, services.SourceDefault, got.Features["chat"].Source)
	})

	t.Run("override can disable", func(t *testing.T) {
		e := newEntEnv()
		e.store.grants["c1"] = &db.EntitlementGrant{SubjectID: "c1", Features: map[string]db.FeatureGrant{"chat": {Enabled: bptr(false)}}}
		st := eff(t, e, "c1").Features["chat"]
		assert.False(t, st.Enabled)
		assert.Equal(t, services.SourceOverride, st.Source)
	})

	t.Run("payments error falls back to the role layer", func(t *testing.T) {
		e := newEntEnv()
		e.plans.err = errors.New("payments down")
		e.store.defaults["_coach"] = db.PlanDefault{PlanID: "_coach", Features: map[string]db.FeatureGrant{"nutrition": {Enabled: bptr(false)}}}
		got := eff(t, e, "c1")
		assert.Nil(t, got.Plan)
		assert.False(t, got.Features["nutrition"].Enabled)
		assert.Equal(t, services.SourcePlan, got.Features["nutrition"].Source)
	})

	t.Run("client capped by coach: enabled and min limit", func(t *testing.T) {
		e := newEntEnv()
		e.store.grants["c1"] = &db.EntitlementGrant{SubjectID: "c1", Features: map[string]db.FeatureGrant{
			"chat":         {Enabled: bptr(false)},
			"file_manager": {Limits: map[string]int64{"max_bytes": 100}},
		}}
		e.store.grants["k1"] = &db.EntitlementGrant{SubjectID: "k1", CoachID: "c1", Features: map[string]db.FeatureGrant{
			"chat":         {Enabled: bptr(true)},
			"file_manager": {Limits: map[string]int64{"max_bytes": 500, "max_uploads": 7}},
		}}
		got := eff(t, e, "k1")
		assert.False(t, got.Features["chat"].Enabled)
		assert.Equal(t, services.SourceCoachCap, got.Features["chat"].Source)
		fm := got.Features["file_manager"]
		assert.Equal(t, int64(100), *fm.Limits["max_bytes"], "min(client, coach)")
		assert.Equal(t, int64(7), *fm.Limits["max_uploads"], "coach unlimited: client value stays")
		assert.Equal(t, services.SourceCoachCap, fm.Source)
	})

	t.Run("client below the coach limit is not marked capped", func(t *testing.T) {
		e := newEntEnv()
		e.store.grants["c1"] = &db.EntitlementGrant{SubjectID: "c1", Features: map[string]db.FeatureGrant{"file_manager": {Limits: map[string]int64{"max_bytes": 100}}}}
		e.store.grants["k1"] = &db.EntitlementGrant{SubjectID: "k1", CoachID: "c1", Features: map[string]db.FeatureGrant{"file_manager": {Limits: map[string]int64{"max_bytes": 40}}}}
		fm := eff(t, e, "k1").Features["file_manager"]
		assert.Equal(t, int64(40), *fm.Limits["max_bytes"])
		assert.Equal(t, services.SourceOverride, fm.Source)
	})

	t.Run("coach revoking a feature caps every client", func(t *testing.T) {
		e := newEntEnv()
		e.store.grants["c1"] = &db.EntitlementGrant{SubjectID: "c1", Features: map[string]db.FeatureGrant{"workouts": {Enabled: bptr(false)}}}
		st := eff(t, e, "k1").Features["workouts"]
		assert.False(t, st.Enabled)
		assert.Equal(t, services.SourceCoachCap, st.Source)
	})

	t.Run("stale coach_id grant is ignored", func(t *testing.T) {
		e := newEntEnv()
		e.store.grants["k1"] = &db.EntitlementGrant{SubjectID: "k1", CoachID: "old-coach", Features: map[string]db.FeatureGrant{"chat": {Enabled: bptr(false)}}}
		st := eff(t, e, "k1").Features["chat"]
		assert.True(t, st.Enabled)
		assert.Equal(t, services.SourceDefault, st.Source)
	})

	t.Run("client without a coach ignores its grant", func(t *testing.T) {
		e := newEntEnv()
		delete(e.links, "k1")
		e.store.grants["k1"] = &db.EntitlementGrant{SubjectID: "k1", CoachID: "c1", Features: map[string]db.FeatureGrant{"chat": {Enabled: bptr(false)}}}
		assert.True(t, eff(t, e, "k1").Features["chat"].Enabled)
	})

	t.Run("invitations does not apply to clients", func(t *testing.T) {
		e := newEntEnv()
		st := eff(t, e, "k1").Features["invitations"]
		assert.False(t, st.Enabled)
		assert.Equal(t, services.SourceNotApplic, st.Source)
	})

	t.Run("resolve is cached for 30s and a write purges it", func(t *testing.T) {
		e := newEntEnv()
		_, _ = e.svc.Resolve(ctx, []string{"c1"})
		_, _ = e.svc.Resolve(ctx, []string{"c1"})
		assert.Equal(t, 1, e.store.grantReads)
		_, apiErr := e.svc.SetCoachAccess(ctx, "adm", "c1", api.UpdateAccessRequest{Features: map[string]api.FeaturePatch{"chat": enabledPatch(false)}})
		require.Nil(t, apiErr)
		assert.False(t, eff(t, e, "c1").Features["chat"].Enabled, "purged on write")
	})

	t.Run("resolve batch skips unknown users and rejects >100", func(t *testing.T) {
		e := newEntEnv()
		out, apiErr := e.svc.Resolve(ctx, []string{"c1", "ghost"})
		require.Nil(t, apiErr)
		assert.Len(t, out, 1)
		many := make([]string, 101)
		_, apiErr = e.svc.Resolve(ctx, many)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code)
	})
}

func TestEffectiveAttachesUsage(t *testing.T) {
	e := newEntEnv()
	e.store.clients["c1"] = 4
	_, apiErr := e.svc.Consume(context.Background(), "c1", api.UsageDeltaRequest{Feature: "file_manager", Deltas: map[string]int64{"uploads": 2, "bytes": 50}})
	require.Nil(t, apiErr)
	got := eff(t, e, "c1")
	assert.Equal(t, map[string]int64{"uploads": 2, "bytes": 50}, got.Features["file_manager"].Usage)
	assert.Equal(t, map[string]int64{"clients": 4}, got.Features["invitations"].Usage)
	assert.Nil(t, got.Features["workouts"].Usage)
}

// ---- writes ----

func TestSetCoachAccessRules(t *testing.T) {
	ctx := context.Background()
	set := func(e *entEnv, req api.UpdateAccessRequest) (*services.CoachAccessDetail, *models.ApiError) {
		return e.svc.SetCoachAccess(ctx, "adm", "c1", req)
	}

	t.Run("unknown feature, limit and negative limit are 400", func(t *testing.T) {
		e := newEntEnv()
		for name, req := range map[string]api.UpdateAccessRequest{
			"feature":  {Features: map[string]api.FeaturePatch{"teleport": enabledPatch(true)}},
			"limit":    {Features: map[string]api.FeaturePatch{"file_manager": limitPatch("max_teleports", i64(1))}},
			"negative": {Features: map[string]api.FeaturePatch{"file_manager": limitPatch("max_bytes", i64(-1))}},
			"empty":    {},
		} {
			_, apiErr := set(e, req)
			require.NotNil(t, apiErr, name)
			assert.Equal(t, http.StatusBadRequest, apiErr.Code, name)
		}
		assert.Empty(t, e.audit.entries)
	})

	t.Run("non coach target is 400, unknown is 404", func(t *testing.T) {
		e := newEntEnv()
		_, apiErr := e.svc.SetCoachAccess(ctx, "adm", "k1", api.UpdateAccessRequest{Features: map[string]api.FeaturePatch{"chat": enabledPatch(false)}})
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code)
		_, apiErr = e.svc.SetCoachAccess(ctx, "adm", "ghost", api.UpdateAccessRequest{Features: map[string]api.FeaturePatch{"chat": enabledPatch(false)}})
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)
	})

	t.Run("write, null clears, audit before/after only changed", func(t *testing.T) {
		e := newEntEnv()
		d, apiErr := set(e, api.UpdateAccessRequest{
			Reason: "trial ended",
			Features: map[string]api.FeaturePatch{
				"chat":         enabledPatch(false),
				"workouts":     enabledPatch(true), // equals the effective default but is a real override entry
				"file_manager": limitPatch("max_bytes", i64(100)),
			},
		})
		require.Nil(t, apiErr)
		assert.Equal(t, int64(1), d.Version)
		assert.False(t, d.Features["chat"].Enabled)
		assert.Equal(t, services.SourceOverride, d.Features["chat"].Source)
		assert.Equal(t, int64(100), *d.Features["file_manager"].Limits["max_bytes"])
		assert.Len(t, d.Overrides, 3)

		require.Len(t, e.audit.entries, 1)
		a := e.audit.entries[0]
		assert.Equal(t, "entitlement.set", a.Action)
		assert.Equal(t, "coach", a.TargetType)
		assert.Equal(t, "c1", a.TargetID)
		assert.Equal(t, "adm", a.ActorID)
		assert.Equal(t, "admin", a.ActorRole)
		assert.Equal(t, "trial ended", a.Reason)
		assert.Contains(t, a.After, "chat")
		assert.Nil(t, a.Before["chat"])
		assert.NotContains(t, a.After, "nutrition", "untouched features are not audited")

		// null clears the override and the entry disappears
		d, apiErr = set(e, api.UpdateAccessRequest{Features: map[string]api.FeaturePatch{
			"chat":         {Enabled: api.OptBool{Set: true, Value: nil}},
			"file_manager": limitPatch("max_bytes", nil),
		}})
		require.Nil(t, apiErr)
		assert.True(t, d.Features["chat"].Enabled)
		assert.Equal(t, services.SourceDefault, d.Features["chat"].Source)
		assert.NotContains(t, d.Overrides, "chat")
		assert.NotContains(t, d.Overrides, "file_manager")
		assert.Equal(t, int64(2), d.Version)
		require.Len(t, e.audit.entries, 2)
		assert.Equal(t, map[string]interface{}{"enabled": false}, e.audit.entries[1].Before["chat"])
		assert.Nil(t, e.audit.entries[1].After["chat"])
	})

	t.Run("no-op writes nothing", func(t *testing.T) {
		e := newEntEnv()
		d, apiErr := set(e, api.UpdateAccessRequest{Features: map[string]api.FeaturePatch{"chat": {Enabled: api.OptBool{Set: true}}}})
		require.Nil(t, apiErr)
		assert.Equal(t, int64(0), d.Version)
		assert.Empty(t, e.audit.entries)
	})

	t.Run("version mismatch is 409", func(t *testing.T) {
		e := newEntEnv()
		req := api.UpdateAccessRequest{Features: map[string]api.FeaturePatch{"chat": enabledPatch(false)}}
		_, apiErr := set(e, req)
		require.Nil(t, apiErr)
		stale := int64(0)
		req.Features = map[string]api.FeaturePatch{"chat": enabledPatch(true)}
		req.Version = &stale
		_, apiErr = set(e, req)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusConflict, apiErr.Code)
		cur := int64(1)
		req.Version = &cur
		_, apiErr = set(e, req)
		assert.Nil(t, apiErr)
	})
}

func TestSetClientAccessRules(t *testing.T) {
	ctx := context.Background()
	set := func(e *entEnv, role string, req api.UpdateAccessRequest) (*services.ClientAccessDetail, *models.ApiError) {
		return e.svc.SetClientAccess(ctx, "c1", role, "c1", "k1", req)
	}

	t.Run("client not linked is 403", func(t *testing.T) {
		e := newEntEnv()
		_, apiErr := e.svc.SetClientAccess(ctx, "n1", "nutritionist", "n1", "k1", api.UpdateAccessRequest{Features: map[string]api.FeaturePatch{"chat": enabledPatch(false)}})
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusForbidden, apiErr.Code)
		_, apiErr = e.svc.GetClientAccess(ctx, "n1", "k1")
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusForbidden, apiErr.Code)
	})

	t.Run("feature that does not apply to clients is 400", func(t *testing.T) {
		e := newEntEnv()
		_, apiErr := set(e, "coach", api.UpdateAccessRequest{Features: map[string]api.FeaturePatch{"invitations": enabledPatch(false)}})
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code)
	})

	t.Run("grant exceeding the coach is GRANT_EXCEEDS_COACH", func(t *testing.T) {
		e := newEntEnv()
		e.store.grants["c1"] = &db.EntitlementGrant{SubjectID: "c1", Features: map[string]db.FeatureGrant{
			"chat":         {Enabled: bptr(false)},
			"file_manager": {Limits: map[string]int64{"max_bytes": 100}},
		}}
		_, apiErr := set(e, "coach", api.UpdateAccessRequest{Features: map[string]api.FeaturePatch{"chat": enabledPatch(true)}})
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusForbidden, apiErr.Code)
		ee := entErr(t, apiErr)
		assert.Equal(t, models.CodeGrantExceedsCoach, ee.Code)
		assert.Equal(t, "chat", ee.Feature)

		_, apiErr = set(e, "coach", api.UpdateAccessRequest{Features: map[string]api.FeaturePatch{"file_manager": limitPatch("max_bytes", i64(101))}})
		ee = entErr(t, apiErr)
		assert.Equal(t, models.CodeGrantExceedsCoach, ee.Code)
		assert.Equal(t, "max_bytes", ee.Limit)
		require.NotNil(t, ee.Max)
		assert.Equal(t, int64(100), *ee.Max)

		// at the coach's value, or restricting, is fine
		_, apiErr = set(e, "coach", api.UpdateAccessRequest{Features: map[string]api.FeaturePatch{
			"file_manager": limitPatch("max_bytes", i64(100)), "chat": enabledPatch(false),
		}})
		assert.Nil(t, apiErr)
	})

	t.Run("write records the coach, audit with actor role, effective view is capped", func(t *testing.T) {
		e := newEntEnv()
		d, apiErr := set(e, "coach", api.UpdateAccessRequest{Reason: "r", Features: map[string]api.FeaturePatch{"nutrition": enabledPatch(false)}})
		require.Nil(t, apiErr)
		assert.False(t, d.Features["nutrition"].Enabled)
		assert.Equal(t, services.SourceOverride, d.Features["nutrition"].Source)
		assert.True(t, d.Grantable["nutrition"].Enabled, "grantable is the coach's own value")
		assert.Equal(t, "c1", e.store.grants["k1"].CoachID)
		require.Len(t, e.audit.entries, 1)
		assert.Equal(t, "client", e.audit.entries[0].TargetType)
		assert.Equal(t, "coach", e.audit.entries[0].ActorRole)
		assert.Equal(t, "k1", e.audit.entries[0].TargetID)
		// the client's own view reflects it
		assert.False(t, eff(t, e, "k1").Features["nutrition"].Enabled)
	})

	t.Run("a grant left by a previous coach is replaced, version still enforced", func(t *testing.T) {
		e := newEntEnv()
		e.store.grants["k1"] = &db.EntitlementGrant{SubjectID: "k1", CoachID: "old", Version: 3, Features: map[string]db.FeatureGrant{"chat": {Enabled: bptr(false)}}}
		d, apiErr := set(e, "coach", api.UpdateAccessRequest{Features: map[string]api.FeaturePatch{"workouts": enabledPatch(false)}})
		require.Nil(t, apiErr)
		assert.Equal(t, int64(4), d.Version)
		assert.NotContains(t, d.Overrides, "chat")
		assert.True(t, d.Features["chat"].Enabled)
	})
}

func TestSetPlanDefault(t *testing.T) {
	ctx := context.Background()
	e := newEntEnv()
	patch := map[string]api.FeaturePatch{"nutrition": enabledPatch(false)}

	_, apiErr := e.svc.SetPlanDefault(ctx, "adm", "not-a-plan", api.PlanDefaultRequest{Features: patch})
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.Code)
	_, apiErr = e.svc.SetPlanDefault(ctx, "adm", "_client", api.PlanDefaultRequest{Features: map[string]api.FeaturePatch{"invitations": enabledPatch(false)}})
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.Code)

	_ = eff(t, e, "c1") // warm the caches: the write must purge them
	pd, apiErr := e.svc.SetPlanDefault(ctx, "adm", "_coach", api.PlanDefaultRequest{Features: patch, Reason: "tier"})
	require.Nil(t, apiErr)
	assert.Equal(t, "_coach", pd.PlanID)
	assert.False(t, eff(t, e, "c1").Features["nutrition"].Enabled)
	assert.Equal(t, services.SourcePlan, eff(t, e, "c1").Features["nutrition"].Source)
	kn := eff(t, e, "k1").Features["nutrition"]
	assert.False(t, kn.Enabled, "the coach's value caps the client")
	assert.Equal(t, services.SourceCoachCap, kn.Source)
	require.Len(t, e.audit.entries, 1)
	assert.Equal(t, "plan_default.set", e.audit.entries[0].Action)
	assert.Equal(t, "plan", e.audit.entries[0].TargetType)
	assert.Equal(t, "_coach", e.audit.entries[0].TargetID)

	list, apiErr := e.svc.ListPlanDefaults(ctx)
	require.Nil(t, apiErr)
	assert.Len(t, list, 1)
}

// ---- consume / release / quota ----

func TestConsumeAndRelease(t *testing.T) {
	ctx := context.Background()
	limited := func() *entEnv {
		e := newEntEnv()
		e.store.grants["c1"] = &db.EntitlementGrant{SubjectID: "c1", Features: map[string]db.FeatureGrant{
			"file_manager": {Limits: map[string]int64{"max_uploads": 3, "max_bytes": 100}},
		}}
		return e
	}
	consume := func(e *entEnv, uploads, bytes int64) (map[string]int64, *models.ApiError) {
		return e.svc.Consume(ctx, "c1", api.UsageDeltaRequest{Feature: "file_manager", Deltas: map[string]int64{"uploads": uploads, "bytes": bytes}})
	}

	t.Run("under, at and over the limit", func(t *testing.T) {
		e := limited()
		u, apiErr := consume(e, 2, 40)
		require.Nil(t, apiErr)
		assert.Equal(t, map[string]int64{"uploads": 2, "bytes": 40}, u)
		u, apiErr = consume(e, 1, 60) // exactly at both limits
		require.Nil(t, apiErr)
		assert.Equal(t, map[string]int64{"uploads": 3, "bytes": 100}, u)

		_, apiErr = consume(e, 1, 0)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusForbidden, apiErr.Code)
		ee := entErr(t, apiErr)
		assert.Equal(t, models.CodeQuotaExceeded, ee.Code)
		assert.Equal(t, "file_manager", ee.Feature)
		assert.Equal(t, "max_uploads", ee.Limit, "uploads 3/3 would exceed, bytes 100/100 would not")
	})

	t.Run("denial names the exceeded limit and does not change usage", func(t *testing.T) {
		e := limited()
		_, apiErr := consume(e, 1, 90)
		require.Nil(t, apiErr)
		_, apiErr = consume(e, 1, 11)
		ee := entErr(t, apiErr)
		assert.Equal(t, models.CodeQuotaExceeded, ee.Code)
		assert.Equal(t, "max_bytes", ee.Limit)
		require.NotNil(t, ee.Used)
		require.NotNil(t, ee.Max)
		assert.Equal(t, int64(90), *ee.Used)
		assert.Equal(t, int64(100), *ee.Max)
		assert.Equal(t, int64(90), e.store.usage["c1"].Counters["file_manager"]["bytes"])
		assert.Equal(t, int64(1), e.store.usage["c1"].Counters["file_manager"]["uploads"])
	})

	t.Run("unlimited feature always passes", func(t *testing.T) {
		e := newEntEnv()
		_, apiErr := consume(e, 1000, 1<<40)
		assert.Nil(t, apiErr)
	})

	t.Run("disabled feature is FEATURE_DISABLED", func(t *testing.T) {
		e := newEntEnv()
		e.store.grants["c1"] = &db.EntitlementGrant{SubjectID: "c1", Features: map[string]db.FeatureGrant{"file_manager": {Enabled: bptr(false)}}}
		_, apiErr := consume(e, 1, 1)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusForbidden, apiErr.Code)
		assert.Equal(t, models.CodeFeatureDisabled, entErr(t, apiErr).Code)
	})

	t.Run("validation", func(t *testing.T) {
		e := newEntEnv()
		for name, req := range map[string]api.UsageDeltaRequest{
			"unknown feature": {Feature: "teleport", Deltas: map[string]int64{"uploads": 1}},
			"no counters":     {Feature: "chat", Deltas: map[string]int64{"uploads": 1}},
			"unknown counter": {Feature: "file_manager", Deltas: map[string]int64{"gigabytes": 1}},
			"negative":        {Feature: "file_manager", Deltas: map[string]int64{"bytes": -5}},
			"empty":           {Feature: "file_manager"},
		} {
			_, apiErr := e.svc.Consume(ctx, "c1", req)
			require.NotNil(t, apiErr, name)
			assert.Equal(t, http.StatusBadRequest, apiErr.Code, name)
		}
		_, apiErr := e.svc.Consume(ctx, "ghost", api.UsageDeltaRequest{Feature: "file_manager", Deltas: map[string]int64{"uploads": 1}})
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.Code)
	})

	t.Run("release floors at zero and frees room", func(t *testing.T) {
		e := limited()
		_, apiErr := consume(e, 3, 100)
		require.Nil(t, apiErr)
		require.Nil(t, e.svc.Release(ctx, "c1", api.UsageDeltaRequest{Feature: "file_manager", Deltas: map[string]int64{"uploads": 1, "bytes": 30}}))
		_, apiErr = consume(e, 1, 30)
		assert.Nil(t, apiErr)
		require.Nil(t, e.svc.Release(ctx, "c1", api.UsageDeltaRequest{Feature: "file_manager", Deltas: map[string]int64{"uploads": 99, "bytes": 9999}}))
		assert.Equal(t, int64(0), e.store.usage["c1"].Counters["file_manager"]["uploads"])
		assert.Equal(t, int64(0), e.store.usage["c1"].Counters["file_manager"]["bytes"])
		// releasing for a user without usage is a no-op
		assert.Nil(t, e.svc.Release(ctx, "k1", api.UsageDeltaRequest{Feature: "file_manager", Deltas: map[string]int64{"uploads": 1}}))
	})

	t.Run("client is capped by the coach limit", func(t *testing.T) {
		e := limited()
		_, apiErr := e.svc.Consume(ctx, "k1", api.UsageDeltaRequest{Feature: "file_manager", Deltas: map[string]int64{"bytes": 101}})
		require.NotNil(t, apiErr)
		assert.Equal(t, models.CodeQuotaExceeded, entErr(t, apiErr).Code)
	})

	t.Run("concurrent consumes never exceed the limit", func(t *testing.T) {
		e := limited()
		var wg sync.WaitGroup
		var okMu sync.Mutex
		ok := 0
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, apiErr := consume(e, 1, 0); apiErr == nil {
					okMu.Lock()
					ok++
					okMu.Unlock()
				}
			}()
		}
		wg.Wait()
		assert.Equal(t, 3, ok)
	})
}

func TestCheckQuota(t *testing.T) {
	ctx := context.Background()
	req := func(pending int64) api.CheckQuotaRequest {
		return api.CheckQuotaRequest{Feature: "invitations", Limit: "max_clients", Pending: pending}
	}
	e := newEntEnv()
	e.store.grants["c1"] = &db.EntitlementGrant{SubjectID: "c1", Features: map[string]db.FeatureGrant{"invitations": {Limits: map[string]int64{"max_clients": 5}}}}
	e.store.clients["c1"] = 3

	res, apiErr := e.svc.CheckQuota(ctx, "c1", req(1))
	require.Nil(t, apiErr)
	assert.True(t, res.Allowed)
	assert.Equal(t, int64(4), res.Used)
	require.NotNil(t, res.Limit)
	assert.Equal(t, int64(5), *res.Limit)

	_, apiErr = e.svc.CheckQuota(ctx, "c1", req(2)) // 3 linked + 2 pending = 5: full
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusForbidden, apiErr.Code)
	ee := entErr(t, apiErr)
	assert.Equal(t, models.CodeQuotaExceeded, ee.Code)
	assert.Equal(t, "max_clients", ee.Limit)
	assert.Equal(t, int64(5), *ee.Used)
	assert.Equal(t, int64(5), *ee.Max)

	// unlimited
	e.store.clients["adm"] = 99
	res, apiErr = e.svc.CheckQuota(ctx, "adm", req(10))
	require.Nil(t, apiErr)
	assert.Nil(t, res.Limit)

	// feature disabled
	e.store.grants["n1"] = &db.EntitlementGrant{SubjectID: "n1", Features: map[string]db.FeatureGrant{"invitations": {Enabled: bptr(false)}}}
	_, apiErr = e.svc.CheckQuota(ctx, "n1", req(0))
	assert.Equal(t, models.CodeFeatureDisabled, entErr(t, apiErr).Code)

	// validation
	for _, bad := range []api.CheckQuotaRequest{
		{Feature: "chat", Limit: "max_clients"}, {Feature: "invitations", Limit: "max_bytes"}, req(-1), {Feature: "teleport"},
	} {
		_, apiErr = e.svc.CheckQuota(ctx, "c1", bad)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusBadRequest, apiErr.Code)
	}
}

func TestCheckInvitationsGuard(t *testing.T) {
	ctx := context.Background()
	e := newEntEnv()
	assert.Nil(t, e.guard.CheckInvitations(ctx, "c1"), "no grants: allowed")
	assert.Nil(t, e.guard.CheckInvitations(ctx, "ghost"), "unknown coach: never blocks")

	e.store.clients["c1"] = 1
	_, apiErr := e.svc.SetCoachAccess(ctx, "adm", "c1", api.UpdateAccessRequest{Features: map[string]api.FeaturePatch{"invitations": limitPatch("max_clients", i64(2))}})
	require.Nil(t, apiErr)
	assert.Nil(t, e.guard.CheckInvitations(ctx, "c1"), "1 of 2 clients")
	e.store.clients["c1"] = 2
	denied := e.guard.CheckInvitations(ctx, "c1")
	require.NotNil(t, denied)
	assert.Equal(t, http.StatusForbidden, denied.Code)
	assert.Equal(t, models.CodeQuotaExceeded, entErr(t, denied).Code)

	_, apiErr = e.svc.SetCoachAccess(ctx, "adm", "c1", api.UpdateAccessRequest{Features: map[string]api.FeaturePatch{"invitations": enabledPatch(false)}})
	require.Nil(t, apiErr)
	denied = e.guard.CheckInvitations(ctx, "c1")
	require.NotNil(t, denied)
	assert.Equal(t, models.CodeFeatureDisabled, entErr(t, denied).Code)
}

func TestRegistrationLinkServiceDeniedByGuard(t *testing.T) {
	e := newEntEnv()
	e.store.grants["c1"] = &db.EntitlementGrant{SubjectID: "c1", Features: map[string]db.FeatureGrant{"invitations": {Enabled: bptr(false)}}}
	// Repositories are nil: a denial must return before touching them.
	svc := services.NewRegistrationLinkService(nil, nil).WithEntitlements(e.guard)
	link, apiErr := svc.GetOrCreateLink(context.Background(), "c1")
	assert.Nil(t, link)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusForbidden, apiErr.Code)
	assert.Equal(t, models.CodeFeatureDisabled, entErr(t, apiErr).Code)
}

// ---- plan lookup ----

func TestPlanCacheAndFailureNotCached(t *testing.T) {
	ctx := context.Background()
	e := newEntEnv()
	e.plans.byUser["c1"] = payments.SubscriptionSummaryEntry{UserID: "c1", PlanID: "64b64b64b64b64b64b64b64b", PlanName: "Pro", Status: "active"}
	e.plans.err = errors.New("timeout")
	assert.Nil(t, eff(t, e, "c1").Plan)

	e.plans.err = nil
	_, apiErr := e.svc.SetCoachAccess(ctx, "adm", "c1", api.UpdateAccessRequest{Features: map[string]api.FeaturePatch{"chat": enabledPatch(false)}}) // purges the resolve cache
	require.Nil(t, apiErr)
	got := eff(t, e, "c1")
	require.NotNil(t, got.Plan, "a failed lookup is retried, not cached")
	assert.Equal(t, "Pro", got.Plan.Name)
	calls := e.plans.calls
	_, _ = e.svc.SetCoachAccess(ctx, "adm", "c1", api.UpdateAccessRequest{Features: map[string]api.FeaturePatch{"chat": enabledPatch(true)}})
	_ = eff(t, e, "c1")
	assert.Equal(t, calls, e.plans.calls, "plans are cached for 5 minutes")
}

// ---- parent master switch ----

func TestParentMasterSwitch(t *testing.T) {
	children := map[string][]string{
		"workouts":  {"exercises", "workout_templates", "programs", "tasks"},
		"nutrition": {"recipes", "ingredients", "meal_plans", "recipe_books", "meal_programs"},
	}
	t.Run("parent off disables every child with source parent", func(t *testing.T) {
		for parent, kids := range children {
			e := newEntEnv()
			e.store.grants["c1"] = &db.EntitlementGrant{SubjectID: "c1", Features: map[string]db.FeatureGrant{parent: {Enabled: bptr(false)}}}
			got := eff(t, e, "c1")
			assert.Equal(t, services.SourceOverride, got.Features[parent].Source)
			for _, k := range kids {
				assert.False(t, got.Features[k].Enabled, k)
				assert.Equal(t, services.SourceParent, got.Features[k].Source, k)
			}
		}
	})

	t.Run("parent on, child off: only the child is off", func(t *testing.T) {
		e := newEntEnv()
		e.store.grants["c1"] = &db.EntitlementGrant{SubjectID: "c1", Features: map[string]db.FeatureGrant{"recipes": {Enabled: bptr(false)}}}
		got := eff(t, e, "c1")
		assert.False(t, got.Features["recipes"].Enabled)
		assert.Equal(t, services.SourceOverride, got.Features["recipes"].Source)
		assert.True(t, got.Features["nutrition"].Enabled)
		assert.True(t, got.Features["meal_plans"].Enabled)
	})

	t.Run("child enabled override cannot beat a disabled parent", func(t *testing.T) {
		e := newEntEnv()
		e.store.defaults["_coach"] = db.PlanDefault{PlanID: "_coach", Features: map[string]db.FeatureGrant{"workouts": {Enabled: bptr(false)}}}
		e.store.grants["c1"] = &db.EntitlementGrant{SubjectID: "c1", Features: map[string]db.FeatureGrant{"programs": {Enabled: bptr(true)}}}
		got := eff(t, e, "c1")
		assert.False(t, got.Features["programs"].Enabled)
		assert.Equal(t, services.SourceParent, got.Features["programs"].Source)
	})

	t.Run("client: parent rule before coach cap", func(t *testing.T) {
		e := newEntEnv()
		e.store.grants["k1"] = &db.EntitlementGrant{SubjectID: "k1", CoachID: "c1", Features: map[string]db.FeatureGrant{"nutrition": {Enabled: bptr(false)}}}
		got := eff(t, e, "k1")
		assert.False(t, got.Features["recipes"].Enabled)
		assert.Equal(t, services.SourceParent, got.Features["recipes"].Source)

		e = newEntEnv()
		e.store.grants["c1"] = &db.EntitlementGrant{SubjectID: "c1", Features: map[string]db.FeatureGrant{"workouts": {Enabled: bptr(false)}}}
		got = eff(t, e, "k1")
		assert.False(t, got.Features["tasks"].Enabled)
		assert.Equal(t, services.SourceCoachCap, got.Features["tasks"].Source, "coach child is off, client child is on: cap applies")
		assert.True(t, got.Features["recipes"].Enabled)
	})

	t.Run("registry lists parents before children and accepts child keys in patches", func(t *testing.T) {
		pos := map[string]int{}
		for i, d := range services.Features() {
			pos[d.Key] = i
			if d.Parent != "" {
				assert.Contains(t, pos, d.Parent, "parent listed before %s", d.Key)
			}
		}
		assert.Contains(t, pos, "meal_programs")
		e := newEntEnv()
		_, apiErr := e.svc.SetCoachAccess(context.Background(), "adm", "c1", api.UpdateAccessRequest{Features: map[string]api.FeaturePatch{"workout_templates": enabledPatch(false)}})
		require.Nil(t, apiErr)
	})
}

// ---- chat monthly messages ----

func TestChatMonthlyMessages(t *testing.T) {
	ctx := context.Background()
	limited := func(max int64) *entEnv {
		e := newEntEnv()
		e.store.grants["c1"] = &db.EntitlementGrant{SubjectID: "c1", Features: map[string]db.FeatureGrant{
			"chat": {Limits: map[string]int64{"max_messages_month": max}},
		}}
		return e
	}
	send := func(e *entEnv, n int64) (map[string]int64, *models.ApiError) {
		return e.svc.Consume(ctx, "c1", api.UsageDeltaRequest{Feature: "chat", Deltas: map[string]int64{"messages": n}})
	}

	t.Run("under, at and over the limit", func(t *testing.T) {
		e := limited(3)
		u, apiErr := send(e, 2)
		require.Nil(t, apiErr)
		assert.Equal(t, map[string]int64{"messages": 2}, u)
		_, apiErr = send(e, 1)
		require.Nil(t, apiErr)
		_, apiErr = send(e, 1)
		ee := entErr(t, apiErr)
		assert.Equal(t, http.StatusForbidden, apiErr.Code)
		assert.Equal(t, models.CodeQuotaExceeded, ee.Code)
		assert.Equal(t, "chat", ee.Feature)
		assert.Equal(t, "max_messages_month", ee.Limit)
		require.NotNil(t, ee.Used)
		assert.Equal(t, int64(3), *ee.Used)
		assert.Equal(t, int64(3), *ee.Max)
	})

	t.Run("unlimited by default", func(t *testing.T) {
		e := newEntEnv()
		_, apiErr := send(e, 1000)
		require.Nil(t, apiErr)
	})

	t.Run("release floors at zero and unknown counter is 400", func(t *testing.T) {
		e := limited(5)
		_, apiErr := send(e, 2)
		require.Nil(t, apiErr)
		require.Nil(t, e.svc.Release(ctx, "c1", api.UsageDeltaRequest{Feature: "chat", Deltas: map[string]int64{"messages": 99}}))
		u, apiErr := send(e, 1)
		require.Nil(t, apiErr)
		assert.Equal(t, int64(1), u["messages"])
		_, apiErr = e.svc.Consume(ctx, "c1", api.UsageDeltaRequest{Feature: "chat", Deltas: map[string]int64{"bytes": 1}})
		assert.Equal(t, http.StatusBadRequest, apiErr.Code)
	})

	t.Run("effective usage exposes messages_month and month, other usage unchanged", func(t *testing.T) {
		e := limited(10)
		_, apiErr := send(e, 4)
		require.Nil(t, apiErr)
		got := eff(t, e, "c1")
		chat := got.Features["chat"]
		assert.Equal(t, map[string]int64{"messages_month": 4}, chat.Usage)
		assert.Equal(t, time.Now().UTC().Format("2006-01"), chat.Month)
		raw, err := json.Marshal(chat)
		require.NoError(t, err)
		var m map[string]interface{}
		require.NoError(t, json.Unmarshal(raw, &m))
		usage := m["usage"].(map[string]interface{})
		assert.Equal(t, float64(4), usage["messages_month"])
		assert.Equal(t, chat.Month, usage["month"])
		fm, _ := json.Marshal(got.Features["file_manager"])
		assert.NotContains(t, string(fm), "month")
	})

	t.Run("a previous month's count does not apply", func(t *testing.T) {
		e := limited(3)
		prev := time.Now().UTC().AddDate(0, -1, -time.Now().UTC().Day()+1)
		e.store.usage["c1"] = &db.EntitlementUsage{SubjectID: "c1", Counters: map[string]map[string]int64{
			"chat": {services.CounterKey("messages", prev): 3},
		}}
		u, apiErr := send(e, 3)
		require.Nil(t, apiErr)
		assert.Equal(t, int64(3), u["messages"])
		assert.Equal(t, int64(0), eff(t, e, "k1").Features["chat"].Usage["messages_month"], "other users unaffected")
	})
}

func TestCounterKeyMonthRollover(t *testing.T) {
	for name, tc := range map[string]struct {
		counter string
		at      time.Time
		want    string
	}{
		"monthly counter":       {"messages", time.Date(2026, 11, 30, 23, 59, 59, 0, time.UTC), "messages_202611"},
		"rolls over on the 1st": {"messages", time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), "messages_202612"},
		"uses UTC":              {"messages", time.Date(2026, 11, 30, 23, 0, 0, 0, time.FixedZone("x", -2*3600)), "messages_202612"},
		"year rollover":         {"messages", time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), "messages_202701"},
		"lifetime counter":      {"bytes", time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), "bytes"},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, services.CounterKey(tc.counter, tc.at))
		})
	}
}
