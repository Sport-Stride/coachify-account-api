package handlers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func idList(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("c%d", i)
	}
	return ids
}

func TestCheckClientLinks(t *testing.T) {
	post := func(s *apiSvcs, userID, role, body string) *httptest.ResponseRecorder {
		return do(newAPIRouter(s, userID, role), http.MethodPost, "/coach/clients/check", body)
	}
	notCalled := func(s *apiSvcs) {
		s.coach.AssertNotCalled(t, "CheckClientLinks", mock.Anything, mock.Anything, mock.Anything)
	}

	t.Run("no token claims", func(t *testing.T) {
		s := newSvcs()
		w := post(s, "", "", `{"client_ids":["c1"]}`)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		notCalled(s)
	})

	longJSON, _ := json.Marshal(map[string][]string{"client_ids": idList(201)})
	for _, tc := range []struct{ name, body string }{
		{"malformed json", `{`},
		{"missing field", `{}`},
		{"empty list", `{"client_ids":[]}`},
		{"more than 200", string(longJSON)},
		{"blank id", `{"client_ids":["c1","  "]}`},
		{"empty id", `{"client_ids":[""]}`},
		{"duplicate ids", `{"client_ids":["c1","c1"]}`},
	} {
		t.Run("400 "+tc.name, func(t *testing.T) {
			s := newSvcs()
			w := post(s, "coach1", "coach", tc.body)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, decode(t, w), "error")
			notCalled(s)
		})
	}

	t.Run("exactly 200 ids accepted", func(t *testing.T) {
		s := newSvcs()
		ids := idList(200)
		s.coach.On("CheckClientLinks", mock.Anything, "coach1", ids).Return(map[string]bool{}, nil)
		b, _ := json.Marshal(map[string][]string{"client_ids": ids})
		assert.Equal(t, http.StatusOK, post(s, "coach1", "coach", string(b)).Code)
	})

	for _, role := range []string{"client", "gym_owner", "", "unknown"} {
		t.Run("403 role "+role, func(t *testing.T) {
			s := newSvcs()
			w := post(s, "u1", role, `{"client_ids":["c1"]}`)
			assert.Equal(t, http.StatusForbidden, w.Code)
			assert.Contains(t, decode(t, w), "error")
			notCalled(s)
		})
	}

	t.Run("admin gets all true without lookup", func(t *testing.T) {
		s := newSvcs()
		w := post(s, "admin1", "admin", `{"client_ids":["c1","c2"]}`)
		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		body := decode(t, w)
		assert.Equal(t, "admin1", body["coach_id"])
		assert.Equal(t, map[string]interface{}{"c1": true, "c2": true}, body["links"])
		notCalled(s)
	})

	for _, role := range []string{"coach", "nutritionist", "head_coach"} {
		t.Run("mixed linked and unlinked as "+role, func(t *testing.T) {
			s := newSvcs()
			s.coach.On("CheckClientLinks", mock.Anything, "u1", []string{"c1", "c2"}).
				Return(map[string]bool{"c1": true, "c2": false}, nil)
			w := post(s, "u1", role, `{"client_ids":["c1","c2"],"coach_id":"attacker"}`)
			require.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			body := decode(t, w)
			assert.Equal(t, "u1", body["coach_id"])
			assert.Equal(t, map[string]interface{}{"c1": true, "c2": false}, body["links"])
			s.coach.AssertExpectations(t)
		})
	}

	t.Run("service error", func(t *testing.T) {
		s := newSvcs()
		s.coach.On("CheckClientLinks", mock.Anything, "coach1", mock.Anything).
			Return(nil, apiErr(http.StatusInternalServerError, "internal server error"))
		w := post(s, "coach1", "coach", `{"client_ids":["c1"]}`)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Equal(t, map[string]interface{}{"error": "internal server error"}, decode(t, w))
	})
}
