package handlers_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"coachify-account-api/models/db"
	"coachify-account-api/services"
)

func TestListAdminUsers(t *testing.T) {
	t.Run("query forwarded", func(t *testing.T) {
		s := newSvcs()
		want := db.AdminUserQuery{
			Q: "a.*", Role: "coach", Status: "Active", IDs: []string{"x", "y"},
			Page: 2, Size: 50, Sort: "name", Order: "asc",
		}
		s.admin.On("ListUsers", mock.Anything, want).
			Return(&services.AdminUserList{Users: []db.AdminUserRow{{ExternalID: "x", Email: "x@x.io"}}, Total: 51, Page: 2, Size: 50}, nil)
		w := get(newAPIRouter(s, "a1", "admin"), "/admin/users?q=%20a.*%20&role=coach&status=Active&ids=x,%20y,,&page=2&size=50&sort=name&order=asc")
		require.Equal(t, http.StatusOK, w.Code)
		body := decode(t, w)
		assert.Equal(t, float64(51), body["total"])
		assert.Equal(t, float64(2), body["page"])
		assert.Equal(t, float64(50), body["size"])
		users := body["users"].([]interface{})
		require.Len(t, users, 1)
		assert.Equal(t, "x", users[0].(map[string]interface{})["external_id"])
		s.admin.AssertExpectations(t)
	})

	t.Run("pagination clamp", func(t *testing.T) {
		for _, tc := range []struct {
			qs               string
			wantPage, wantSz int
		}{
			{"", 1, 20},
			{"?page=0&size=0", 1, 20},
			{"?page=-3&size=101", 1, 20},
			{"?size=100", 1, 100},
		} {
			s := newSvcs()
			s.admin.On("ListUsers", mock.Anything, db.AdminUserQuery{Page: tc.wantPage, Size: tc.wantSz}).
				Return(&services.AdminUserList{Users: []db.AdminUserRow{}}, nil)
			w := get(newAPIRouter(s, "a1", "admin"), "/admin/users"+tc.qs)
			assert.Equal(t, http.StatusOK, w.Code, tc.qs)
			s.admin.AssertExpectations(t)
		}
	})

	t.Run("service 400s are returned verbatim", func(t *testing.T) {
		for _, msg := range []string{"invalid role", "invalid status", "invalid sort", "invalid order"} {
			s := newSvcs()
			s.admin.On("ListUsers", mock.Anything, mock.Anything).Return(nil, apiErr(http.StatusBadRequest, msg))
			w := get(newAPIRouter(s, "a1", "admin"), "/admin/users?sort=bogus")
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Equal(t, msg, decode(t, w)["error"])
		}
	})

	t.Run("internal error does not leak details", func(t *testing.T) {
		s := newSvcs()
		s.admin.On("ListUsers", mock.Anything, mock.Anything).Return(nil, apiErr(http.StatusInternalServerError, "mongo: secret"))
		w := get(newAPIRouter(s, "a1", "admin"), "/admin/users")
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Equal(t, "internal error", decode(t, w)["error"])
	})

	t.Run("row JSON has the contract keys and no secrets", func(t *testing.T) {
		s := newSvcs()
		s.admin.On("ListUsers", mock.Anything, mock.Anything).
			Return(&services.AdminUserList{Users: []db.AdminUserRow{{ExternalID: "x", CreatedAt: time.Now()}}, Total: 1, Page: 1, Size: 20}, nil)
		w := get(newAPIRouter(s, "a1", "admin"), "/admin/users")
		raw := w.Body.String()
		for _, k := range []string{
			"external_id", "firstname", "lastname", "email", "role", "status", "profile_picture",
			"verification_status", "matricule_fiscale_status", "is_head_coach", "created_at", "last_login",
		} {
			assert.Contains(t, raw, `"`+k+`"`)
		}
		for _, k := range []string{"password", "token", "confirm_code", "reset_password_code", "providers", `"matricule_fiscale"`} {
			assert.NotContains(t, raw, k)
		}
	})
}

func TestGetAdminUser(t *testing.T) {
	t.Run("success shape", func(t *testing.T) {
		s := newSvcs()
		n := 3
		s.admin.On("GetUser", mock.Anything, "c1").Return(&db.AdminUserDetail{
			AdminUserRow: db.AdminUserRow{ExternalID: "c1", Role: "coach", MatriculeFiscaleStatus: "pending"},
			PhoneNumber:  "123",
			ClientsCount: &n,
		}, nil)
		w := get(newAPIRouter(s, "a1", "admin"), "/admin/users/c1")
		require.Equal(t, http.StatusOK, w.Code)
		u := decode(t, w)["user"].(map[string]interface{})
		assert.Equal(t, "c1", u["external_id"])
		assert.Equal(t, "pending", u["matricule_fiscale_status"])
		assert.Equal(t, "123", u["phone_number"])
		assert.Equal(t, float64(3), u["clients_count"])
		assert.Contains(t, u, "coach")
		assert.Nil(t, u["coach"])
		assert.Contains(t, u, "updated_at")
		for _, k := range []string{"password", "token", "confirm_code", "reset_password_code", "providers", `"matricule_fiscale"`} {
			assert.NotContains(t, w.Body.String(), k)
		}
	})

	t.Run("not found", func(t *testing.T) {
		s := newSvcs()
		s.admin.On("GetUser", mock.Anything, "nope").Return(nil, apiErr(http.StatusNotFound, "user not found"))
		w := get(newAPIRouter(s, "a1", "admin"), "/admin/users/nope")
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Equal(t, "user not found", decode(t, w)["error"])
	})
}

func TestGetAdminUserStats(t *testing.T) {
	for _, tc := range []struct {
		qs       string
		wantDays int
	}{
		{"", 30}, {"?days=7", 7}, {"?days=3", 7}, {"?days=500", 90}, {"?days=x", 30}, {"?days=45", 45},
	} {
		s := newSvcs()
		s.admin.On("UserStats", mock.Anything, tc.wantDays).Return(&db.AdminUserStats{
			ByRole:      map[string]int{"coach": 2},
			ByStatus:    map[string]int{"Active": 2},
			NewUsers:    db.AdminNewUsers{Last7d: 1, Last30d: 2},
			Signups:     []db.AdminSignupPoint{{Date: "2026-01-02", Role: "coach", Count: 2}},
			ToConfirm:   1,
			GeneratedAt: time.Now(),
		}, nil)
		w := get(newAPIRouter(s, "a1", "admin"), "/admin/stats/users"+tc.qs)
		require.Equal(t, http.StatusOK, w.Code, tc.qs)
		body := decode(t, w)
		for _, k := range []string{"by_role", "by_status", "new_users", "signups", "to_confirm", "pending_matricule", "generated_at"} {
			assert.Contains(t, body, k)
		}
		assert.Equal(t, float64(2), body["new_users"].(map[string]interface{})["last_30d"])
		s.admin.AssertExpectations(t)
	}

	t.Run("internal error", func(t *testing.T) {
		s := newSvcs()
		s.admin.On("UserStats", mock.Anything, 30).Return(nil, apiErr(http.StatusInternalServerError, "boom"))
		w := get(newAPIRouter(s, "a1", "admin"), "/admin/stats/users")
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Equal(t, "internal error", decode(t, w)["error"])
	})
}

func TestAdminWriteEndpointsNonAdmin403(t *testing.T) {
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPatch, "/admin/users/u1/status", `{"status":"Blocked","reason":"x"}`},
		{http.MethodPost, "/admin/users/u1/resend-confirmation", ""},
		{http.MethodPost, "/admin/users/u1/password-reset", ""},
		{http.MethodGet, "/admin/audit", ""},
	} {
		s := newSvcs()
		w := do(newAPIRouter(s, "c1", "coach"), tc.method, tc.path, tc.body)
		assert.Equal(t, http.StatusForbidden, w.Code, tc.path)
		assert.Empty(t, s.admin.Calls, tc.path)
	}
}

func TestChangeAdminUserStatus(t *testing.T) {
	t.Run("success shape, actor from token", func(t *testing.T) {
		s := newSvcs()
		s.admin.On("UpdateUserStatus", mock.Anything, "a1", "u1", "Blocked", "abuse").
			Return(&db.AdminUserRow{ExternalID: "u1", Status: "Blocked"}, nil)
		w := do(newAPIRouter(s, "a1", "admin"), http.MethodPatch, "/admin/users/u1/status",
			`{"status":"Blocked","reason":"abuse","actor_id":"evil"}`)
		require.Equal(t, http.StatusOK, w.Code)
		u := decode(t, w)["user"].(map[string]interface{})
		assert.Equal(t, "u1", u["external_id"])
		assert.Equal(t, "Blocked", u["status"])
		s.admin.AssertExpectations(t)
	})

	t.Run("bad body", func(t *testing.T) {
		s := newSvcs()
		w := do(newAPIRouter(s, "a1", "admin"), http.MethodPatch, "/admin/users/u1/status", `{nope`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, "invalid request body", decode(t, w)["error"])
	})

	t.Run("service errors passed through", func(t *testing.T) {
		for _, tc := range []struct {
			code int
			msg  string
		}{
			{http.StatusBadRequest, "invalid status"},
			{http.StatusForbidden, "cannot change own status"},
			{http.StatusNotFound, "user not found"},
			{http.StatusConflict, "status unchanged"},
		} {
			s := newSvcs()
			s.admin.On("UpdateUserStatus", mock.Anything, "a1", "u1", "Blocked", "r").Return(nil, apiErr(tc.code, tc.msg))
			w := do(newAPIRouter(s, "a1", "admin"), http.MethodPatch, "/admin/users/u1/status", `{"status":"Blocked","reason":"r"}`)
			assert.Equal(t, tc.code, w.Code)
			assert.Equal(t, tc.msg, decode(t, w)["error"])
		}
	})

	t.Run("internal error does not leak details", func(t *testing.T) {
		s := newSvcs()
		s.admin.On("UpdateUserStatus", mock.Anything, "a1", "u1", "Blocked", "r").Return(nil, apiErr(http.StatusInternalServerError, "mongo: secret"))
		w := do(newAPIRouter(s, "a1", "admin"), http.MethodPatch, "/admin/users/u1/status", `{"status":"Blocked","reason":"r"}`)
		assert.Equal(t, "internal error", decode(t, w)["error"])
	})
}

func TestAdminUserEmailActions(t *testing.T) {
	t.Run("resend confirmation 202", func(t *testing.T) {
		s := newSvcs()
		s.admin.On("ResendConfirmation", mock.Anything, "a1", "u1").Return(nil)
		w := do(newAPIRouter(s, "a1", "admin"), http.MethodPost, "/admin/users/u1/resend-confirmation", "")
		require.Equal(t, http.StatusAccepted, w.Code)
		assert.Equal(t, "confirmation email sent", decode(t, w)["message"])
	})

	t.Run("resend confirmation 409", func(t *testing.T) {
		s := newSvcs()
		s.admin.On("ResendConfirmation", mock.Anything, "a1", "u1").Return(apiErr(http.StatusConflict, "user already confirmed"))
		w := do(newAPIRouter(s, "a1", "admin"), http.MethodPost, "/admin/users/u1/resend-confirmation", "")
		assert.Equal(t, http.StatusConflict, w.Code)
		assert.Equal(t, "user already confirmed", decode(t, w)["error"])
	})

	t.Run("password reset 202 never exposes a code", func(t *testing.T) {
		s := newSvcs()
		s.admin.On("SendPasswordReset", mock.Anything, "a1", "u1").Return(nil)
		w := do(newAPIRouter(s, "a1", "admin"), http.MethodPost, "/admin/users/u1/password-reset", "")
		require.Equal(t, http.StatusAccepted, w.Code)
		assert.Equal(t, map[string]interface{}{"message": "password reset email sent"}, decode(t, w))
		assert.NotContains(t, w.Body.String(), "code")
	})

	t.Run("password reset 404", func(t *testing.T) {
		s := newSvcs()
		s.admin.On("SendPasswordReset", mock.Anything, "a1", "nope").Return(apiErr(http.StatusNotFound, "user not found"))
		w := do(newAPIRouter(s, "a1", "admin"), http.MethodPost, "/admin/users/nope/password-reset", "")
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

func TestListAdminAudit(t *testing.T) {
	t.Run("filters forwarded, success shape", func(t *testing.T) {
		s := newSvcs()
		from := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		to := time.Date(2026, 2, 2, 3, 4, 5, 0, time.UTC)
		s.admin.On("ListAudit", mock.Anything, db.AdminAuditFilter{ActorID: "a1", TargetID: "u1", Action: "user.status_changed", From: from, To: to}, 2, 50).
			Return(&services.AdminAuditList{
				Entries: []db.AdminAuditEntry{{
					ActorID: "a1", Action: "user.status_changed", TargetType: "user", TargetID: "u1",
					Before: map[string]interface{}{"status": "Active"}, After: map[string]interface{}{"status": "Blocked"},
					Reason: "abuse", CreatedAt: from,
				}},
				Total: 1, Page: 2, Size: 50,
			}, nil)
		w := get(newAPIRouter(s, "a1", "admin"),
			"/admin/audit?actor_id=a1&target_id=u1&action=user.status_changed&from=2026-01-02T03:04:05Z&to=2026-02-02T03:04:05Z&page=2&size=50")
		require.Equal(t, http.StatusOK, w.Code)
		body := decode(t, w)
		assert.Equal(t, float64(1), body["total"])
		assert.Equal(t, float64(2), body["page"])
		assert.Equal(t, float64(50), body["size"])
		e := body["entries"].([]interface{})[0].(map[string]interface{})
		for _, k := range []string{"id", "actor_id", "action", "target_type", "target_id", "before", "after", "reason", "created_at"} {
			assert.Contains(t, e, k)
		}
		assert.Equal(t, "Blocked", e["after"].(map[string]interface{})["status"])
		s.admin.AssertExpectations(t)
	})

	t.Run("bad dates 400", func(t *testing.T) {
		for _, qs := range []string{"from=yesterday", "to=2026-01-01"} {
			s := newSvcs()
			w := get(newAPIRouter(s, "a1", "admin"), "/admin/audit?"+qs)
			assert.Equal(t, http.StatusBadRequest, w.Code, qs)
			assert.NotEmpty(t, decode(t, w)["error"])
			assert.Empty(t, s.admin.Calls)
		}
	})
}

func TestCreateAdminGymOwner(t *testing.T) {
	const path = "/admin/gym-owners"
	const body = `{"firstname":"Ada","lastname":"L","email":"ada@gym.io"}`

	t.Run("non admin 403", func(t *testing.T) {
		for _, role := range []string{"coach", "client", "gym_owner", ""} {
			s := newSvcs()
			w := do(newAPIRouter(s, "c1", role), http.MethodPost, path, body)
			assert.Equal(t, http.StatusForbidden, w.Code, role)
			assert.Empty(t, s.admin.Calls, role)
		}
	})

	t.Run("201, actor from token, never exposes a code", func(t *testing.T) {
		s := newSvcs()
		s.admin.On("CreateGymOwner", mock.Anything, "a1", "Ada", "L", "ada@gym.io").
			Return(&db.AdminUserRow{ExternalID: "g1", Role: "gym_owner", Status: "Active"}, nil)
		w := do(newAPIRouter(s, "a1", "admin"), http.MethodPost, path, body)
		require.Equal(t, http.StatusCreated, w.Code)
		u := decode(t, w)["user"].(map[string]interface{})
		assert.Equal(t, "gym_owner", u["role"])
		assert.NotContains(t, w.Body.String(), "code")
		s.admin.AssertExpectations(t)
	})

	t.Run("bad body 400", func(t *testing.T) {
		s := newSvcs()
		w := do(newAPIRouter(s, "a1", "admin"), http.MethodPost, path, `{nope`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Empty(t, s.admin.Calls)
	})

	t.Run("service errors passed through, 500 hidden", func(t *testing.T) {
		for _, tc := range []struct {
			code      int
			msg, want string
		}{
			{http.StatusBadRequest, "invalid email", "invalid email"},
			{http.StatusConflict, "email already exists", "email already exists"},
			{http.StatusInternalServerError, "mongo: secret", "internal error"},
		} {
			s := newSvcs()
			s.admin.On("CreateGymOwner", mock.Anything, "a1", "Ada", "L", "ada@gym.io").Return(nil, apiErr(tc.code, tc.msg))
			w := do(newAPIRouter(s, "a1", "admin"), http.MethodPost, path, body)
			assert.Equal(t, tc.code, w.Code)
			assert.Equal(t, tc.want, decode(t, w)["error"])
		}
	})
}

func TestGymOwnerCannotGenerateRegistrationLink(t *testing.T) {
	s := newSvcs()
	w := do(newAPIRouter(s, "g1", "gym_owner"), http.MethodPost, "/coach/registration-link", "")
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Empty(t, s.reg.Calls)
}
