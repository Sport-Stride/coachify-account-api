package utils

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetry(t *testing.T) {
	t.Run("success first try", func(t *testing.T) {
		calls := 0
		assert.NoError(t, Retry(3, time.Millisecond, func() error { calls++; return nil }))
		assert.Equal(t, 1, calls)
	})
	t.Run("succeeds after retries", func(t *testing.T) {
		calls := 0
		err := Retry(3, time.Millisecond, func() error {
			calls++
			if calls < 3 {
				return errors.New("boom")
			}
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, 3, calls)
	})
	t.Run("exhausts attempts", func(t *testing.T) {
		calls := 0
		want := errors.New("boom")
		err := Retry(2, time.Millisecond, func() error { calls++; return want })
		assert.Equal(t, want, err)
		assert.Equal(t, 2, calls)
	})
	t.Run("StopRetry unwraps and stops", func(t *testing.T) {
		calls := 0
		want := errors.New("fatal")
		err := Retry(5, time.Millisecond, func() error { calls++; return StopRetry{want} })
		assert.Equal(t, want, err)
		assert.Equal(t, 1, calls)
	})
}

func TestCircuitBreaker_OpensAfterThreshold(t *testing.T) {
	cb := NewCircuitBreaker("test-open")
	boom := errors.New("boom")
	for i := 0; i < 5; i++ {
		assert.Equal(t, boom, cb.Execute(func() error { return boom }))
	}
	assert.Equal(t, BreakerOpen, cb.State())

	called := false
	err := cb.Execute(func() error { called = true; return nil })
	assert.ErrorIs(t, err, ErrCircuitOpen)
	assert.False(t, called, "open breaker must not run fn")
}

func TestCircuitBreaker_HalfOpenRecovery(t *testing.T) {
	cb := NewCircuitBreaker("test-half")
	for i := 0; i < 5; i++ {
		_ = cb.Execute(func() error { return errors.New("x") })
	}
	require.Equal(t, BreakerOpen, cb.State())

	cb.mu.Lock()
	cb.lastFailure = time.Now().Add(-time.Minute)
	cb.mu.Unlock()
	assert.NoError(t, cb.Execute(func() error { return nil }))
	assert.Equal(t, BreakerClosed, cb.State())
}

func TestCircuitBreaker_SuccessResetsFailures(t *testing.T) {
	cb := NewCircuitBreaker("test-reset")
	for i := 0; i < 4; i++ {
		_ = cb.Execute(func() error { return errors.New("x") })
	}
	assert.NoError(t, cb.Execute(func() error { return nil }))
	_ = cb.Execute(func() error { return errors.New("x") })
	assert.Equal(t, BreakerClosed, cb.State())
}

func TestInstrumentedDo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			time.Sleep(200 * time.Millisecond)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	newReq := func(path string) *http.Request {
		req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		require.NoError(t, err)
		return req
	}

	t.Run("success", func(t *testing.T) {
		resp, err := InstrumentedDo(context.Background(), srv.Client(), newReq("/ok"), "t", NewCircuitBreaker("ido-ok"), time.Second)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
	t.Run("timeout", func(t *testing.T) {
		resp, err := InstrumentedDo(context.Background(), srv.Client(), newReq("/slow"), "t", NewCircuitBreaker("ido-to"), 20*time.Millisecond)
		if resp != nil {
			resp.Body.Close()
		}
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})
	t.Run("circuit open", func(t *testing.T) {
		cb := NewCircuitBreaker("ido-open")
		cb.mu.Lock()
		cb.state = BreakerOpen
		cb.lastFailure = time.Now()
		cb.mu.Unlock()
		_, err := InstrumentedDo(context.Background(), srv.Client(), newReq("/ok"), "t", cb, time.Second)
		assert.ErrorIs(t, err, ErrCircuitOpen)
	})
	t.Run("transport error", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:1/", nil)
		_, err := InstrumentedDo(context.Background(), http.DefaultClient, req, "t", NewCircuitBreaker("ido-err"), time.Second)
		assert.Error(t, err)
	})
}
