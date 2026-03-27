package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/sony/gobreaker/v2"
)

// CircuitBreakerConfig holds configuration for the circuit breaker middleware.
type CircuitBreakerConfig struct {
	Name        string
	MaxRequests uint32        // max requests allowed in half-open state
	Interval    time.Duration // cyclic period of closed state to clear counts
	Timeout     time.Duration // period of open state before moving to half-open
	ReadyToTrip uint32        // consecutive failures before opening circuit
}

// DefaultCircuitBreakerConfig returns sensible defaults.
func DefaultCircuitBreakerConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		Name:        "http",
		MaxRequests: 1,
		Interval:    60 * time.Second,
		Timeout:     30 * time.Second,
		ReadyToTrip: 5,
	}
}

func normalizeCircuitBreakerConfig(cfg CircuitBreakerConfig, fallbackName string) CircuitBreakerConfig {
	defaults := DefaultCircuitBreakerConfig()
	if cfg.Name == "" {
		cfg.Name = fallbackName
	}
	if cfg.MaxRequests == 0 {
		cfg.MaxRequests = defaults.MaxRequests
	}
	if cfg.Interval <= 0 {
		cfg.Interval = defaults.Interval
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaults.Timeout
	}
	if cfg.ReadyToTrip == 0 {
		cfg.ReadyToTrip = defaults.ReadyToTrip
	}
	return cfg
}

// CircuitBreaker returns HTTP middleware that wraps requests in a circuit breaker.
// Returns 503 Service Unavailable when the circuit is open.
// Counts 5xx responses as failures.
func CircuitBreaker(cfg CircuitBreakerConfig) func(http.Handler) http.Handler {
	cfg = normalizeCircuitBreakerConfig(cfg, "http")
	cb := gobreaker.NewCircuitBreaker[int](gobreaker.Settings{
		Name:        cfg.Name,
		MaxRequests: cfg.MaxRequests,
		Interval:    cfg.Interval,
		Timeout:     cfg.Timeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= cfg.ReadyToTrip
		},
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			slog.Warn("circuit breaker state change",
				slog.String("name", name),
				slog.String("from", from.String()),
				slog.String("to", to.String()),
			)
		},
	})

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			_, err := cb.Execute(func() (int, error) {
				next.ServeHTTP(wrapped, r)
				if wrapped.statusCode >= 500 {
					return wrapped.statusCode, &circuitError{code: wrapped.statusCode}
				}
				return wrapped.statusCode, nil
			})

			if err != nil {
				if _, ok := err.(*circuitError); ok {
					// Already written by the handler, just a failure signal.
					return
				}
				// Circuit is open.
				writeJSONError(w, http.StatusServiceUnavailable,
					"service_unavailable", "service temporarily unavailable")
			}
		})
	}
}

type circuitError struct {
	code int
}

func (e *circuitError) Error() string {
	return http.StatusText(e.code)
}
