package handlers_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"coachify-account-api/handlers"
	"coachify-account-api/models"
	"coachify-account-api/services"
)

type fakeSeatSvc struct {
	services.SeatService
	assigned *bool
	err      *models.ApiError
}

func (f *fakeSeatSvc) Overview(_ context.Context, _, gym string) (*services.SeatOverview, *models.ApiError) {
	if f.err != nil {
		return nil, f.err
	}
	return &services.SeatOverview{GymID: gym, Purchased: 2, Used: 1, Free: 1, Members: []services.SeatMemberView{}}, nil
}

func (f *fakeSeatSvc) SetSeat(_ context.Context, _, _, user string, assigned bool) (*services.SeatMemberView, *models.ApiError) {
	f.assigned = &assigned
	if f.err != nil {
		return nil, f.err
	}
	return &services.SeatMemberView{UserID: user, SeatAssigned: assigned, SeatState: "seated"}, nil
}

func seatRouter(svc services.SeatService, userID string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if userID != "" {
			c.Set("userID", userID)
		}
		c.Next()
	})
	r.GET("/gyms/:gym_id/seats", handlers.GetGymSeats(svc))
	r.PUT("/gyms/:gym_id/members/:user_id/seat", handlers.PutGymMemberSeat(svc))
	return r
}

func seatCall(r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestSeatHandlers(t *testing.T) {
	t.Run("overview", func(t *testing.T) {
		w := seatCall(seatRouter(&fakeSeatSvc{}, "owner"), "GET", "/gyms/g1/seats", "")
		require.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `"purchased":2`)
	})
	t.Run("unauthenticated", func(t *testing.T) {
		assert.Equal(t, http.StatusUnauthorized, seatCall(seatRouter(&fakeSeatSvc{}, ""), "GET", "/gyms/g1/seats", "").Code)
	})
	t.Run("foreign gym is 404", func(t *testing.T) {
		svc := &fakeSeatSvc{err: models.NewApiError(http.StatusNotFound, errors.New("gym not found"))}
		w := seatCall(seatRouter(svc, "stranger"), "GET", "/gyms/g1/seats", "")
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.Contains(t, w.Body.String(), `"error"`)
	})
	t.Run("assign needs the assigned key", func(t *testing.T) {
		svc := &fakeSeatSvc{}
		assert.Equal(t, http.StatusBadRequest, seatCall(seatRouter(svc, "owner"), "PUT", "/gyms/g1/members/c1/seat", `{}`).Code)
		assert.Nil(t, svc.assigned)
	})
	t.Run("assign and release", func(t *testing.T) {
		svc := &fakeSeatSvc{}
		w := seatCall(seatRouter(svc, "owner"), "PUT", "/gyms/g1/members/c1/seat", `{"assigned":false}`)
		require.Equal(t, http.StatusOK, w.Code)
		require.NotNil(t, svc.assigned)
		assert.False(t, *svc.assigned)
	})
	t.Run("SEATS_EXHAUSTED shape", func(t *testing.T) {
		used, total := int64(2), int64(2)
		svc := &fakeSeatSvc{err: models.NewEntitlementApiError(http.StatusConflict, &models.EntitlementError{
			Code: models.CodeSeatsExhausted, Message: "no free seat in this gym", Limit: "seats", Used: &used, Max: &total})}
		w := seatCall(seatRouter(svc, "owner"), "PUT", "/gyms/g1/members/c1/seat", `{"assigned":true}`)
		require.Equal(t, http.StatusConflict, w.Code)
		assert.JSONEq(t, `{"error":"no free seat in this gym","code":"SEATS_EXHAUSTED","limit":"seats","used":2,"max":2}`, w.Body.String())
	})
}

type fakeCtxResolver struct{}

func (fakeCtxResolver) Resolve(_ context.Context, _ string, c services.EntContext) (*services.ContextEntitlements, *models.ApiError) {
	if c.Kind == "x" {
		return nil, models.NewApiError(http.StatusBadRequest, errors.New("context must be personal or gym"))
	}
	return &services.ContextEntitlements{Context: c.Kind, Features: []string{"workouts"}, SeatOnly: true, SeatState: "none", Source: "no_personal_subscription"}, nil
}

func TestInternalUserEntitlementsHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	run := func(svc services.ContextEntitlementResolver, path string) *httptest.ResponseRecorder {
		r := gin.New()
		r.GET("/internal/users/:id/entitlements", handlers.GetInternalUserEntitlements(svc))
		return seatCall(r, "GET", path, "")
	}
	w := run(fakeCtxResolver{}, "/internal/users/u1/entitlements?context=personal")
	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"context":"personal","features":["workouts"],"seat_only":true,"seat_state":"none","source":"no_personal_subscription"}`, w.Body.String())
	assert.Equal(t, http.StatusBadRequest, run(fakeCtxResolver{}, "/internal/users/u1/entitlements?context=x").Code)
	assert.Equal(t, http.StatusNotFound, run(nil, "/internal/users/u1/entitlements").Code, "seats off: 404")
}
