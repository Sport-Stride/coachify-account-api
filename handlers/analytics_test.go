package handlers_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/handlers"
	"coachify-account-api/models"
	"coachify-account-api/models/db"
	"coachify-account-api/services"
)

// fakeAnalytics answers the service calls the handler tests use.
type fakeAnalytics struct {
	services.AnalyticsScopeService
	err       *models.ApiError
	lastReq   services.ScopeRequest
	lastIDs   []string
	lastQuery services.HeadClientsQuery
	calls     int
}

func (f *fakeAnalytics) Scope(_ context.Context, _ string, req services.ScopeRequest) (*db.AnalyticsScope, *models.ApiError) {
	f.calls++
	f.lastReq = req
	if f.err != nil {
		return nil, f.err
	}
	return &db.AnalyticsScope{
		ScopeKey:        "team:h1",
		Coaches:         []db.ScopeCoach{{CoachID: "c1", HeadCoachID: "h1", Current: true, Periods: []db.PeriodRange{{}}}},
		CurrentCoachIDs: []string{"c1"},
	}, nil
}

func (f *fakeAnalytics) LookupClients(_ context.Context, _ string, req services.ScopeRequest, ids []string) ([]db.ClientRow, *models.ApiError) {
	f.calls++
	f.lastReq, f.lastIDs = req, ids
	if f.err != nil {
		return nil, f.err
	}
	return []db.ClientRow{{ExternalID: "k1", CoachID: "c1"}}, nil
}

func (f *fakeAnalytics) ListHeadClients(_ context.Context, _ string, q services.HeadClientsQuery) ([]db.ClientRowLinked, int, *models.ApiError) {
	f.calls++
	f.lastQuery = q
	if f.err != nil {
		return nil, 0, f.err
	}
	return []db.ClientRowLinked{{ClientRow: db.ClientRow{ExternalID: "k1"}}}, 7, nil
}

func newAnalyticsRouter(svc services.AnalyticsScopeService, userID, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if userID != "" {
			c.Set("userID", userID)
			c.Set("userRole", role)
		}
		c.Next()
	})
	r.GET("/analytics/scope", handlers.GetAnalyticsScope(svc))
	r.POST("/analytics/clients/lookup", handlers.LookupAnalyticsClients(svc))
	r.GET("/head-coach/clients", handlers.ListHeadCoachClients(svc))
	return r
}

func TestAnalyticsScopeHandler(t *testing.T) {
	t.Run("ok shape and params", func(t *testing.T) {
		f := &fakeAnalytics{}
		w := get(newAnalyticsRouter(f, "u1", "client"), "/analytics/scope?scope=gym&gym_id=g1&head_coach_id=h1&coach_id=c1")
		require.Equal(t, http.StatusOK, w.Code)
		body := decode(t, w)
		assert.Equal(t, "team:h1", body["scope_key"])
		assert.Contains(t, body, "coaches")
		assert.Contains(t, body, "current_coach_ids")
		assert.Contains(t, body, "generated_at")
		coach := body["coaches"].([]interface{})[0].(map[string]interface{})
		for _, k := range []string{"coach_id", "firstname", "lastname", "head_coach_id", "head_coach_name", "current", "periods"} {
			assert.Contains(t, coach, k)
		}
		assert.Equal(t, services.ScopeRequest{Scope: "gym", GymID: "g1", HeadCoachID: "h1", CoachID: "c1"}, f.lastReq)
	})

	t.Run("401 without token claims", func(t *testing.T) {
		f := &fakeAnalytics{}
		w := get(newAnalyticsRouter(f, "", ""), "/analytics/scope?scope=team")
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, decode(t, w), "error")
		assert.Zero(t, f.calls)
	})

	for name, code := range map[string]int{"forbidden": 403, "not found": 404, "bad scope": 400, "too large": 413, "internal": 500} {
		t.Run(name, func(t *testing.T) {
			msg := "msg"
			if code == 500 {
				msg = "internal error"
			}
			f := &fakeAnalytics{err: apiErr(code, msg)}
			w := get(newAnalyticsRouter(f, "u1", "coach"), "/analytics/scope?scope=team")
			assert.Equal(t, code, w.Code)
			assert.Equal(t, msg, decode(t, w)["error"])
		})
	}
}

func TestAnalyticsLookupHandler(t *testing.T) {
	r := func(f *fakeAnalytics) *gin.Engine { return newAnalyticsRouter(f, "u1", "coach") }

	t.Run("ok", func(t *testing.T) {
		f := &fakeAnalytics{}
		w := do(r(f), "POST", "/analytics/clients/lookup", `{"scope":"team","coach_id":"c1","ids":["k1","k2"]}`)
		require.Equal(t, http.StatusOK, w.Code)
		items := decode(t, w)["items"].([]interface{})
		require.Len(t, items, 1)
		assert.Equal(t, "k1", items[0].(map[string]interface{})["external_id"])
		assert.Equal(t, []string{"k1", "k2"}, f.lastIDs)
		assert.Equal(t, services.ScopeRequest{Scope: "team", CoachID: "c1"}, f.lastReq)
	})

	t.Run("401", func(t *testing.T) {
		w := do(newAnalyticsRouter(&fakeAnalytics{}, "", ""), "POST", "/analytics/clients/lookup", `{"scope":"team","ids":["k1"]}`)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("400 bad body, empty or too many ids", func(t *testing.T) {
		f := &fakeAnalytics{}
		ids := `"x"`
		for i := 0; i < 100; i++ {
			ids += `,"x"`
		}
		for _, body := range []string{`not json`, `{"scope":"team"}`, `{"scope":"team","ids":[]}`, `{"scope":"team","ids":[` + ids + `]}`} {
			w := do(r(f), "POST", "/analytics/clients/lookup", body)
			assert.Equal(t, http.StatusBadRequest, w.Code, body)
			assert.Contains(t, decode(t, w), "error")
		}
		assert.Zero(t, f.calls)
	})

	t.Run("service errors pass through", func(t *testing.T) {
		for _, code := range []int{403, 404} {
			w := do(r(&fakeAnalytics{err: apiErr(code, "x")}), "POST", "/analytics/clients/lookup", `{"scope":"gym","ids":["k1"]}`)
			assert.Equal(t, code, w.Code)
		}
	})
}

func TestHeadCoachClientsHandler(t *testing.T) {
	t.Run("ok shape and params", func(t *testing.T) {
		f := &fakeAnalytics{}
		w := get(newAnalyticsRouter(f, "h1", "coach"), "/head-coach/clients?coach_id=c1&q=%20ann%20&sort=linked_at&page=2&size=500")
		require.Equal(t, http.StatusOK, w.Code)
		body := decode(t, w)
		assert.EqualValues(t, 7, body["total"])
		assert.EqualValues(t, 2, body["page"])
		assert.EqualValues(t, 20, body["size"]) // out-of-range size falls back to the default
		assert.Len(t, body["items"], 1)
		assert.Equal(t, services.HeadClientsQuery{CoachID: "c1", Q: "ann", Sort: "linked_at", Page: 2, Size: 20}, f.lastQuery)
	})

	t.Run("401 and 403", func(t *testing.T) {
		assert.Equal(t, http.StatusUnauthorized, get(newAnalyticsRouter(&fakeAnalytics{}, "", ""), "/head-coach/clients").Code)
		assert.Equal(t, http.StatusForbidden, get(newAnalyticsRouter(&fakeAnalytics{}, "u1", "client"), "/head-coach/clients").Code)
	})

	t.Run("400 invalid sort", func(t *testing.T) {
		w := get(newAnalyticsRouter(&fakeAnalytics{}, "h1", "coach"), "/head-coach/clients?sort=bogus")
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("404 and 403 from the service", func(t *testing.T) {
		assert.Equal(t, http.StatusNotFound, get(newAnalyticsRouter(&fakeAnalytics{err: apiErr(404, "coach not found")}, "h1", "coach"), "/head-coach/clients?coach_id=zz").Code)
		assert.Equal(t, http.StatusForbidden, get(newAnalyticsRouter(&fakeAnalytics{err: apiErr(403, "Access denied")}, "h1", "coach"), "/head-coach/clients").Code)
	})
}
