package handlers_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models"
	"coachify-account-api/models/api"
	"coachify-account-api/models/db"
	"coachify-account-api/services"
)

func entDenied(status int, ee *models.EntitlementError) *models.ApiError {
	return models.NewEntitlementApiError(status, ee)
}

// Every entitlement route is gated by role before the service is reached (the mocks have no
// expectations: a call would panic).
func TestEntitlementRoleGating(t *testing.T) {
	const body = `{"features":{"chat":{"enabled":false}}}`
	type route struct{ method, path, body string }
	adminRoutes := []route{
		{"GET", "/admin/coaches/access", ""},
		{"GET", "/admin/coaches/c1/access", ""},
		{"PUT", "/admin/coaches/c1/access", body},
		{"GET", "/admin/entitlements/features", ""},
		{"GET", "/admin/entitlements/plan-defaults", ""},
		{"PUT", "/admin/entitlements/plan-defaults/_coach", body},
		{"GET", "/admin/entitlements/audit", ""},
	}
	for _, rt := range adminRoutes {
		for _, role := range []string{"coach", "nutritionist", "client", "gym_owner", ""} {
			w := do(newAPIRouter(newSvcs(), "u1", role), rt.method, rt.path, rt.body)
			assert.Equal(t, http.StatusForbidden, w.Code, "%s %s as %q", rt.method, rt.path, role)
			assert.Equal(t, "Admin access required", decode(t, w)["error"])
		}
	}

	coachRoutes := []route{
		{"GET", "/coach/clients/access", ""},
		{"GET", "/coach/clients/k1/access", ""},
		{"PUT", "/coach/clients/k1/access", body},
	}
	for _, rt := range coachRoutes {
		for _, role := range []string{"client", "gym_owner", ""} {
			w := do(newAPIRouter(newSvcs(), "u1", role), rt.method, rt.path, rt.body)
			assert.Equal(t, http.StatusForbidden, w.Code, "%s %s as %q", rt.method, rt.path, role)
		}
		// an admin must say whose clients (the coach's id)
		w := do(newAPIRouter(newSvcs(), "adm", "admin"), rt.method, rt.path, rt.body)
		assert.Equal(t, http.StatusBadRequest, w.Code, "%s %s as admin without coach_id", rt.method, rt.path)
	}

	// /entitlements/me needs a token identity only
	assert.Equal(t, http.StatusUnauthorized, get(newAPIRouter(newSvcs(), "", ""), "/entitlements/me").Code)
}

func TestEntitlementRolesAllowed(t *testing.T) {
	ctx := mock.Anything

	t.Run("me for every role", func(t *testing.T) {
		for _, role := range []string{"client", "coach", "nutritionist", "admin", "gym_owner"} {
			s := newSvcs()
			s.ent.On("Effective", ctx, "u1").Return(&services.Effective{UserID: "u1", Role: role}, nil)
			w := get(newAPIRouter(s, "u1", role), "/entitlements/me")
			require.Equal(t, http.StatusOK, w.Code, role)
			assert.Equal(t, "u1", decode(t, w)["user_id"])
		}
	})

	t.Run("admin lists with filters", func(t *testing.T) {
		s := newSvcs()
		s.ent.On("ListCoachAccess", ctx, db.CoachAccessQuery{Page: 2, Size: 50, Q: "ann", Status: "Active", Feature: "chat", Disabled: true}).
			Return(&services.CoachAccessList{Rows: []services.CoachAccessRow{{ExternalID: "c1"}}, Total: 1, Page: 2, Size: 50}, nil)
		w := get(newAPIRouter(s, "adm", "admin"), "/admin/coaches/access?page=2&size=50&q=ann&status=Active&feature=chat&disabled=true")
		require.Equal(t, http.StatusOK, w.Code)
		rows := decode(t, w)["rows"].([]interface{})
		require.Len(t, rows, 1)
		assert.Equal(t, "c1", rows[0].(map[string]interface{})["external_id"])
		s.ent.AssertExpectations(t)

		w = get(newAPIRouter(newSvcs(), "adm", "admin"), "/admin/coaches/access?disabled=maybe")
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("admin put parses null clears and quotes the actor", func(t *testing.T) {
		s := newSvcs()
		var got api.UpdateAccessRequest
		s.ent.On("SetCoachAccess", ctx, "adm", "c1", mock.Anything).Run(func(a mock.Arguments) {
			got = a.Get(3).(api.UpdateAccessRequest)
		}).Return(&services.CoachAccessDetail{}, nil)
		w := do(newAPIRouter(s, "adm", "admin"), "PUT", "/admin/coaches/c1/access",
			`{"features":{"chat":{"enabled":null},"file_manager":{"enabled":true,"limits":{"max_bytes":null,"max_uploads":5}},"workouts":{}},"reason":"why","version":3}`)
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "why", got.Reason)
		require.NotNil(t, got.Version)
		assert.Equal(t, int64(3), *got.Version)
		chat := got.Features["chat"]
		assert.True(t, chat.Enabled.Set)
		assert.Nil(t, chat.Enabled.Value, "null clears")
		fm := got.Features["file_manager"]
		require.NotNil(t, fm.Enabled.Value)
		assert.True(t, *fm.Enabled.Value)
		v, present := fm.Limits["max_bytes"]
		assert.True(t, present)
		assert.Nil(t, v, "null limit clears")
		assert.Equal(t, int64(5), *fm.Limits["max_uploads"])
		assert.False(t, got.Features["workouts"].Enabled.Set, "absent means unchanged")

		assert.Equal(t, http.StatusBadRequest, do(newAPIRouter(newSvcs(), "adm", "admin"), "PUT", "/admin/coaches/c1/access", `{`).Code)
		assert.Equal(t, http.StatusBadRequest, do(newAPIRouter(newSvcs(), "adm", "admin"), "PUT", "/admin/coaches/c1/access", `{"features":{"chat":{"enabled":"yes"}}}`).Code)
	})

	t.Run("version conflict is 409 without a code", func(t *testing.T) {
		s := newSvcs()
		s.ent.On("SetCoachAccess", ctx, "adm", "c1", mock.Anything).Return(nil, apiErr(http.StatusConflict, "access was modified"))
		w := do(newAPIRouter(s, "adm", "admin"), "PUT", "/admin/coaches/c1/access", `{"features":{"chat":{"enabled":false}}}`)
		assert.Equal(t, http.StatusConflict, w.Code)
		body := decode(t, w)
		assert.Equal(t, "access was modified", body["error"])
		assert.NotContains(t, body, "code")
	})

	t.Run("coach, nutritionist and admin with coach_id manage clients", func(t *testing.T) {
		for _, tc := range []struct{ role, id, query, wantCoach string }{
			{"coach", "c1", "", "c1"},
			{"nutritionist", "n1", "", "n1"},
			{"admin", "adm", "?coach_id=c7", "c7"},
		} {
			s := newSvcs()
			s.ent.On("GetClientAccess", ctx, tc.wantCoach, "k1").Return(&services.ClientAccessDetail{}, nil)
			w := get(newAPIRouter(s, tc.id, tc.role), "/coach/clients/k1/access"+tc.query)
			assert.Equal(t, http.StatusOK, w.Code, tc.role)
			s.ent.AssertExpectations(t)
		}
	})

	t.Run("client put forwards the actor role", func(t *testing.T) {
		s := newSvcs()
		s.ent.On("SetClientAccess", ctx, "n1", "nutritionist", "n1", "k1", mock.Anything).Return(&services.ClientAccessDetail{}, nil)
		w := do(newAPIRouter(s, "n1", "nutritionist"), "PUT", "/coach/clients/k1/access", `{"features":{"chat":{"enabled":false}}}`)
		assert.Equal(t, http.StatusOK, w.Code)
		s.ent.AssertExpectations(t)
	})

	t.Run("not linked is a plain 403", func(t *testing.T) {
		s := newSvcs()
		s.ent.On("GetClientAccess", ctx, "c1", "k9").Return(nil, apiErr(http.StatusForbidden, "client is not linked to this coach"))
		w := get(newAPIRouter(s, "c1", "coach"), "/coach/clients/k9/access")
		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Equal(t, "client is not linked to this coach", decode(t, w)["error"])
	})

	t.Run("features registry and plan defaults", func(t *testing.T) {
		s := newSvcs()
		s.ent.On("Features").Return([]services.FeatureDef{{Key: "chat", AppliesTo: []string{"coach", "client"}, DefaultEnabled: true}})
		w := get(newAPIRouter(s, "adm", "admin"), "/admin/entitlements/features")
		require.Equal(t, http.StatusOK, w.Code)
		assert.Len(t, decode(t, w)["features"], 1)

		s.ent.On("ListPlanDefaults", ctx).Return([]db.PlanDefault{{PlanID: "_coach"}}, nil)
		w = get(newAPIRouter(s, "adm", "admin"), "/admin/entitlements/plan-defaults")
		require.Equal(t, http.StatusOK, w.Code)
		assert.Len(t, decode(t, w)["plan_defaults"], 1)

		s.ent.On("SetPlanDefault", ctx, "adm", "_coach", mock.Anything).Return(&db.PlanDefault{PlanID: "_coach"}, nil)
		w = do(newAPIRouter(s, "adm", "admin"), "PUT", "/admin/entitlements/plan-defaults/_coach", `{"features":{"chat":{"enabled":false}}}`)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("audit filters", func(t *testing.T) {
		s := newSvcs()
		s.ent.On("ListAudit", ctx, db.AdminAuditFilter{ActorID: "a", TargetID: "t"}, 3, 10).Return(&services.AdminAuditList{Page: 3, Size: 10}, nil)
		w := get(newAPIRouter(s, "adm", "admin"), "/admin/entitlements/audit?actor_id=a&target_id=t&page=3&size=10")
		assert.Equal(t, http.StatusOK, w.Code)
		s.ent.AssertExpectations(t)
	})
}

// The error body keeps the legacy "error" key and adds the stable code and details.
func TestEntitlementErrorBodyHasCode(t *testing.T) {
	ctx := mock.Anything
	used, max := int64(90), int64(100)

	t.Run("quota exceeded on consume", func(t *testing.T) {
		s := newSvcs()
		s.ent.On("Consume", ctx, "u1", mock.Anything).Return(nil, entDenied(http.StatusForbidden, &models.EntitlementError{
			Code: models.CodeQuotaExceeded, Message: "quota exceeded: max_bytes", Feature: "file_manager", Limit: "max_bytes", Used: &used, Max: &max,
		}))
		w := do(newAPIRouter(s, "", ""), "POST", "/internal/entitlements/u1/consume", `{"feature":"file_manager","deltas":{"bytes":20}}`)
		require.Equal(t, http.StatusForbidden, w.Code)
		assert.Equal(t, map[string]interface{}{
			"error": "quota exceeded: max_bytes", "code": "QUOTA_EXCEEDED", "feature": "file_manager", "limit": "max_bytes", "used": float64(90), "max": float64(100),
		}, decode(t, w))
	})

	t.Run("feature disabled has no limit fields", func(t *testing.T) {
		s := newSvcs()
		s.ent.On("CheckQuota", ctx, "u1", mock.Anything).Return(nil, entDenied(http.StatusForbidden, &models.EntitlementError{
			Code: models.CodeFeatureDisabled, Message: "feature disabled: invitations", Feature: "invitations",
		}))
		w := do(newAPIRouter(s, "", ""), "POST", "/internal/entitlements/u1/check-quota", `{"feature":"invitations","limit":"max_clients"}`)
		require.Equal(t, http.StatusForbidden, w.Code)
		assert.Equal(t, map[string]interface{}{"error": "feature disabled: invitations", "code": "FEATURE_DISABLED", "feature": "invitations"}, decode(t, w))
	})

	t.Run("grant exceeds coach", func(t *testing.T) {
		s := newSvcs()
		s.ent.On("SetClientAccess", ctx, "c1", "coach", "c1", "k1", mock.Anything).Return(nil, entDenied(http.StatusForbidden, &models.EntitlementError{
			Code: models.CodeGrantExceedsCoach, Message: "cannot enable a feature you do not have: chat", Feature: "chat",
		}))
		w := do(newAPIRouter(s, "c1", "coach"), "PUT", "/coach/clients/k1/access", `{"features":{"chat":{"enabled":true}}}`)
		require.Equal(t, http.StatusForbidden, w.Code)
		assert.Equal(t, "GRANT_EXCEEDS_COACH", decode(t, w)["code"])
		assert.Equal(t, "chat", decode(t, w)["feature"])
	})

	t.Run("entitlements unavailable is 503 with its message", func(t *testing.T) {
		s := newSvcs()
		s.ent.On("Effective", ctx, "u1").Return(nil, entDenied(http.StatusServiceUnavailable, &models.EntitlementError{
			Code: models.CodeEntitlementsDown, Message: "entitlements unavailable",
		}))
		w := get(newAPIRouter(s, "", ""), "/internal/entitlements/u1")
		require.Equal(t, http.StatusServiceUnavailable, w.Code)
		assert.Equal(t, map[string]interface{}{"error": "entitlements unavailable", "code": "ENTITLEMENTS_UNAVAILABLE"}, decode(t, w))
	})

	t.Run("registration endpoints carry the code", func(t *testing.T) {
		denial := entDenied(http.StatusForbidden, &models.EntitlementError{
			Code: models.CodeQuotaExceeded, Message: "quota exceeded: max_clients", Feature: "invitations", Limit: "max_clients", Used: &max, Max: &max,
		})
		s := newSvcs()
		s.reg.On("GetOrCreateLink", ctx, "c1").Return(nil, denial)
		w := do(newAPIRouter(s, "c1", "coach"), "POST", "/coach/registration-link", "")
		require.Equal(t, http.StatusForbidden, w.Code)
		assert.Equal(t, "QUOTA_EXCEEDED", decode(t, w)["code"])

		s = newSvcs()
		s.auth.On("RegisterViaLink", ctx, "tok", mock.Anything).Return(nil, denial)
		w = do(newAPIRouter(s, "", ""), "POST", "/coach/registration-link/tok/register", `{"email":"a@b.io","password":"Passw0rd!x","firstname":"a","lastname":"b"}`)
		require.Equal(t, http.StatusForbidden, w.Code)
		body := decode(t, w)
		assert.Equal(t, "QUOTA_EXCEEDED", body["code"])
		assert.Equal(t, "quota exceeded: max_clients", body["error"])
	})
}

func TestInternalEntitlementEndpoints(t *testing.T) {
	ctx := mock.Anything

	t.Run("resolve", func(t *testing.T) {
		s := newSvcs()
		s.ent.On("Resolve", ctx, []string{"a", "b"}).Return(map[string]*services.Effective{"a": {UserID: "a"}}, nil)
		w := do(newAPIRouter(s, "", ""), "POST", "/internal/entitlements/resolve", `{"user_ids":["a","b"]}`)
		require.Equal(t, http.StatusOK, w.Code)
		ents := decode(t, w)["entitlements"].(map[string]interface{})
		assert.Contains(t, ents, "a")

		s = newSvcs()
		s.ent.On("Resolve", ctx, mock.Anything).Return(nil, apiErr(http.StatusBadRequest, "user_ids must contain between 1 and 100 ids"))
		assert.Equal(t, http.StatusBadRequest, do(newAPIRouter(s, "", ""), "POST", "/internal/entitlements/resolve", `{"user_ids":[]}`).Code)
	})

	t.Run("consume and release", func(t *testing.T) {
		s := newSvcs()
		req := api.UsageDeltaRequest{Feature: "file_manager", Deltas: map[string]int64{"uploads": 1, "bytes": 5}}
		s.ent.On("Consume", ctx, "u1", req).Return(map[string]int64{"uploads": 1, "bytes": 5}, nil)
		w := do(newAPIRouter(s, "", ""), "POST", "/internal/entitlements/u1/consume", `{"feature":"file_manager","deltas":{"uploads":1,"bytes":5}}`)
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, map[string]interface{}{"uploads": float64(1), "bytes": float64(5)}, decode(t, w)["usage"])

		s.ent.On("Release", ctx, "u1", req).Return(nil)
		w = do(newAPIRouter(s, "", ""), "POST", "/internal/entitlements/u1/release", `{"feature":"file_manager","deltas":{"uploads":1,"bytes":5}}`)
		assert.Equal(t, http.StatusOK, w.Code)
		s.ent.AssertExpectations(t)
	})

	t.Run("check quota ok", func(t *testing.T) {
		s := newSvcs()
		limit := int64(5)
		s.ent.On("CheckQuota", ctx, "u1", api.CheckQuotaRequest{Feature: "invitations", Limit: "max_clients", Pending: 2}).
			Return(&services.QuotaResult{Allowed: true, Used: 3, Limit: &limit}, nil)
		w := do(newAPIRouter(s, "", ""), "POST", "/internal/entitlements/u1/check-quota", `{"feature":"invitations","limit":"max_clients","pending":2}`)
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, map[string]interface{}{"allowed": true, "used": float64(3), "limit": float64(5)}, decode(t, w))
	})
}
