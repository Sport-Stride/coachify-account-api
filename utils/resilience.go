package utils

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ErrCircuitOpen is returned when the circuit breaker is open and rejecting calls.
var ErrCircuitOpen = errors.New("circuit breaker is open")

// BreakerState represents the state of a circuit breaker.
type BreakerState int

const (
	BreakerClosed   BreakerState = 0
	BreakerHalfOpen BreakerState = 1
	BreakerOpen     BreakerState = 2
)

// CircuitBreaker implements a minimal circuit breaker with 5-failure threshold and 30s reset.
type CircuitBreaker struct {
	mu           sync.Mutex
	name         string
	failures     int
	threshold    int
	state        BreakerState
	lastFailure  time.Time
	resetTimeout time.Duration
}

// NewCircuitBreaker creates a circuit breaker for the named downstream target.
func NewCircuitBreaker(name string) *CircuitBreaker {
	return &CircuitBreaker{
		name:         name,
		threshold:    5,
		resetTimeout: 30 * time.Second,
	}
}

// Execute runs fn inside the circuit breaker. Returns ErrCircuitOpen if the breaker is open.
func (cb *CircuitBreaker) Execute(fn func() error) error {
	cb.mu.Lock()
	if cb.state == BreakerOpen {
		if time.Since(cb.lastFailure) > cb.resetTimeout {
			cb.transition(BreakerHalfOpen)
			cb.mu.Unlock()
		} else {
			cb.mu.Unlock()
			return ErrCircuitOpen
		}
	} else {
		cb.mu.Unlock()
	}

	err := fn()

	cb.mu.Lock()
	defer cb.mu.Unlock()
	if err != nil {
		cb.failures++
		cb.lastFailure = time.Now()
		if cb.failures >= cb.threshold {
			cb.transition(BreakerOpen)
		}
		return err
	}
	if cb.state == BreakerHalfOpen {
		cb.transition(BreakerClosed)
	}
	cb.failures = 0
	return nil
}

// State returns the current breaker state.
func (cb *CircuitBreaker) State() BreakerState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

func (cb *CircuitBreaker) transition(to BreakerState) {
	if cb.state != to {
		cb.state = to
		CircuitBreakerState.WithLabelValues(cb.name).Set(float64(to))
		if to == BreakerOpen {
			zap.L().Error("circuit breaker open", zap.String("target", cb.name))
		} else if to == BreakerClosed {
			zap.L().Info("circuit breaker closed", zap.String("target", cb.name))
		}
	}
}

// InstrumentedDo wraps an HTTP call with context timeout, circuit breaker, and Prometheus metrics.
func InstrumentedDo(ctx context.Context, client *http.Client, req *http.Request, target string, breaker *CircuitBreaker, timeout time.Duration) (*http.Response, error) {
	start := time.Now()

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req = req.WithContext(callCtx)

	var resp *http.Response
	err := breaker.Execute(func() error {
		var doErr error
		resp, doErr = client.Do(req)
		return doErr
	})

	status := "success"
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			status = "timeout"
		} else if errors.Is(err, ErrCircuitOpen) {
			status = "circuit_open"
		} else {
			status = "error"
		}
	}
	ExternalCallDuration.WithLabelValues(target, status).Observe(time.Since(start).Seconds())

	return resp, err
}
