package handlers_test

import (
	"errors"
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

func TestAdminRoutesRequireAdminRole(t *testing.T) {
	for _, role := range []string{"coach", "client", "nutritionist", ""} {
		s := newSvcs()
		r := newAPIRouter(s, "u1", role)
		for _, tc := range []struct{ method, path string }{
			{"GET", "/admin/coaches"},
			{"GET", "/admin/coaches/c1/clients"},
			{"GET", "/admin/matricule-fiscale"},
			{"POST", "/admin/matricule-fiscale/u2/approve"},
			{"POST", "/admin/matricule-fiscale/u2/reject"},
		} {
			w := do(r, tc.method, tc.path, "")
			assert.Equal(t, http.StatusForbidden, w.Code, "role=%q %s %s", role, tc.method, tc.path)
		}
		s.admin.AssertNotCalled(t, "ListCoaches", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
		s.admin.AssertNotCalled(t, "ListCoachClients", mock.Anything, mock.Anything)
		s.auth.AssertNotCalled(t, "ApproveMatriculeFiscale", mock.Anything, mock.Anything, mock.Anything)
		s.auth.AssertNotCalled(t, "RejectMatriculeFiscale", mock.Anything, mock.Anything, mock.Anything)
	}
}

func TestListAdminCoaches(t *testing.T) {
	yes := true

	t.Run("pagination params", func(t *testing.T) {
		for _, tc := range []struct {
			name, qs         string
			wantPage, wantSz int
		}{
			{"defaults", "", 1, 20},
			{"explicit", "?page=3&size=50", 3, 50},
			{"page below 1 clamps", "?page=0", 1, 20},
			{"negative page clamps", "?page=-4", 1, 20},
			{"size over 100 resets to default", "?size=101", 1, 20},
			{"size zero resets to default", "?size=0", 1, 20},
			{"non numeric falls back", "?page=x&size=y", 1, 20},
			{"size 100 allowed", "?size=100", 1, 100},
		} {
			t.Run(tc.name, func(t *testing.T) {
				s := newSvcs()
				s.admin.On("ListCoaches", mock.Anything, tc.wantPage, tc.wantSz, db.AdminCoachFilter{}).
					Return([]services.AdminCoachRow{{ExternalID: "c1"}}, 1, nil)
				w := get(newAPIRouter(s, "a1", "admin"), "/admin/coaches"+tc.qs)
				require.Equal(t, http.StatusOK, w.Code)
				body := decode(t, w)
				assert.Equal(t, float64(tc.wantPage), body["page"])
				assert.Equal(t, float64(tc.wantSz), body["size"])
				assert.Equal(t, float64(1), body["total"])
				s.admin.AssertExpectations(t)
			})
		}
	})

	t.Run("filters", func(t *testing.T) {
		s := newSvcs()
		s.admin.On("ListCoaches", mock.Anything, 1, 20, db.AdminCoachFilter{HeadCoach: &yes, Unassigned: true}).Return(nil, 0, nil)
		w := get(newAPIRouter(s, "a1", "admin"), "/admin/coaches?head_coach=true&unassigned=1")
		assert.Equal(t, http.StatusOK, w.Code)
		s.admin.AssertExpectations(t)
	})

	t.Run("invalid filters", func(t *testing.T) {
		for _, qs := range []string{"?head_coach=maybe", "?unassigned=maybe"} {
			s := newSvcs()
			w := get(newAPIRouter(s, "a1", "admin"), "/admin/coaches"+qs)
			assert.Equal(t, http.StatusBadRequest, w.Code, qs)
			s.admin.AssertNotCalled(t, "ListCoaches", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
		}
	})

	t.Run("internal error does not leak details", func(t *testing.T) {
		s := newSvcs()
		s.admin.On("ListCoaches", mock.Anything, 1, 20, db.AdminCoachFilter{}).Return(nil, 0, errors.New("mongo: secret connection detail"))
		w := get(newAPIRouter(s, "a1", "admin"), "/admin/coaches")
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Equal(t, "internal error", decode(t, w)["error"])
	})
}

func TestListAdminCoachClients(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		s := newSvcs()
		s.admin.On("ListCoachClients", mock.Anything, "c1").Return([]services.AdminClientRow{{ExternalID: "x"}, {ExternalID: "y"}}, nil)
		w := get(newAPIRouter(s, "a1", "admin"), "/admin/coaches/c1/clients")
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, float64(2), decode(t, w)["total"])
	})
	t.Run("internal error", func(t *testing.T) {
		s := newSvcs()
		s.admin.On("ListCoachClients", mock.Anything, "c1").Return(nil, errors.New("secret"))
		w := get(newAPIRouter(s, "a1", "admin"), "/admin/coaches/c1/clients")
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Equal(t, "internal error", decode(t, w)["error"])
	})
}

func TestAdminMatriculeFiscale(t *testing.T) {
	t.Run("list: query forwarded, defaults 1/10", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("GetMatriculeFiscaleApplications", mock.Anything, "pending", 2, 5).
			Return([]api.MatriculeFiscaleApplication{{UserID: "u1"}}, 6, nil)
		s.auth.On("GetMatriculeFiscaleApplications", mock.Anything, "", 1, 10).Return(nil, 0, nil)
		r := newAPIRouter(s, "a1", "admin")

		w := get(r, "/admin/matricule-fiscale?status=pending&page=2&limit=5")
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, float64(6), decode(t, w)["total"])
		assert.Equal(t, http.StatusOK, get(r, "/admin/matricule-fiscale").Code)
		s.auth.AssertExpectations(t)
	})
	t.Run("list: service error", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("GetMatriculeFiscaleApplications", mock.Anything, "", 1, 10).Return(nil, 0, models.NewApiError(http.StatusBadRequest, models.ErrMatriculeFiscaleInvalidStatus))
		w := get(newAPIRouter(s, "a1", "admin"), "/admin/matricule-fiscale")
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
	t.Run("approve and reject use token admin id", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("ApproveMatriculeFiscale", mock.Anything, "u2", "a1").Return(nil)
		s.auth.On("RejectMatriculeFiscale", mock.Anything, "u2", "a1").Return(nil)
		r := newAPIRouter(s, "a1", "admin")
		assert.Equal(t, http.StatusOK, do(r, "POST", "/admin/matricule-fiscale/u2/approve", "").Code)
		assert.Equal(t, http.StatusOK, do(r, "POST", "/admin/matricule-fiscale/u2/reject", "").Code)
		s.auth.AssertExpectations(t)
	})
	t.Run("approve service error", func(t *testing.T) {
		s := newSvcs()
		s.auth.On("ApproveMatriculeFiscale", mock.Anything, "u2", "a1").Return(models.NewApiError(http.StatusNotFound, models.ErrUserNotFound))
		w := do(newAPIRouter(s, "a1", "admin"), "POST", "/admin/matricule-fiscale/u2/approve", "")
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
	t.Run("no claims", func(t *testing.T) {
		r := newAPIRouter(newSvcs(), "", "")
		assert.Equal(t, http.StatusUnauthorized, do(r, "POST", "/admin/matricule-fiscale/u2/approve", "").Code)
		assert.Equal(t, http.StatusUnauthorized, do(r, "POST", "/admin/matricule-fiscale/u2/reject", "").Code)
	})
}
