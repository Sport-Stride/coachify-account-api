package utils

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// ExternalCallDuration tracks latency of outbound HTTP calls to downstream services.
var ExternalCallDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "account_external_call_duration_seconds",
	Help:    "Duration of external service calls from account-api",
	Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
}, []string{"target", "status"})

// CircuitBreakerState exposes the circuit breaker state per target: 0=closed, 1=half-open, 2=open.
var CircuitBreakerState = promauto.NewGaugeVec(prometheus.GaugeOpts{
	Name: "account_circuit_breaker_state",
	Help: "Circuit breaker state: 0=closed, 1=half-open, 2=open",
}, []string{"target"})
