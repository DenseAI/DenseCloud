package middleware

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"
)

// RateLimiterInterface defines the contract for rate limiters.
// Both in-memory and Redis implementations satisfy this interface.
type RateLimiterInterface interface {
	Allow() bool
}

// KeyedRateLimiter defines keyed rate limiting for per-client or per-tenant flows.
type KeyedRateLimiter interface {
	AllowKey(key string) bool
}

// RateLimitKeyExtractor returns the logical rate-limit key for an HTTP request.
// The default keyed limiter uses the transport peer from RemoteAddr. Deployments
// behind a trusted proxy or post-auth tenant boundary should provide an
// explicit extractor instead of trusting client-controlled forwarding headers.
type RateLimitKeyExtractor func(*http.Request) string

// RateLimiter implements token bucket rate limiting.
type RateLimiter struct {
	mu         sync.Mutex
	tokens     float64
	maxTokens  float64
	refillRate float64 // tokens per second
	lastRefill time.Time
}

// NewRateLimiter creates a new rate limiter.
func NewRateLimiter(requestsPerSecond int, burst int) *RateLimiter {
	return &RateLimiter{
		tokens:     float64(burst),
		maxTokens:  float64(burst),
		refillRate: float64(requestsPerSecond),
		lastRefill: time.Now(),
	}
}

// Allow checks if a request is allowed.
func (rl *RateLimiter) Allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(rl.lastRefill).Seconds()
	rl.tokens += elapsed * rl.refillRate
	if rl.tokens > rl.maxTokens {
		rl.tokens = rl.maxTokens
	}
	rl.lastRefill = now

	if rl.tokens >= 1 {
		rl.tokens--
		return true
	}
	return false
}

// AllowKey satisfies KeyedRateLimiter using the shared in-memory bucket.
func (rl *RateLimiter) AllowKey(_ string) bool {
	return rl.Allow()
}

// RateLimit creates rate limiting middleware.
func RateLimit(limiter *RateLimiter) func(http.Handler) http.Handler {
	return RateLimitWithInterface(limiter)
}

// RateLimitWithInterface creates rate limiting middleware with any RateLimiterInterface.
func RateLimitWithInterface(limiter RateLimiterInterface) func(http.Handler) http.Handler {
	limiterNil := isNilRateLimiter(limiter)
	var nilLimiterLogOnce sync.Once

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if limiterNil {
				nilLimiterLogOnce.Do(func() {
					slog.Warn("rate limit middleware disabled: limiter is nil")
				})
				next.ServeHTTP(w, r)
				return
			}

			if !limiter.Allow() {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]string{
						"message": "Rate limit exceeded",
						"type":    "rate_limit_error",
						"code":    "rate_limit_exceeded",
					},
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimitWithKey creates keyed rate limiting middleware.
// When extractor is nil, DenseCloud uses the direct transport peer derived from
// RemoteAddr. Trusted proxy, tenant, or API-key based rate limits must supply a
// custom extractor after the proxy or auth layer has validated that identity.
func RateLimitWithKey(limiter KeyedRateLimiter, extractor RateLimitKeyExtractor) func(http.Handler) http.Handler {
	limiterNil := isNilRateLimiter(limiter)
	var nilLimiterLogOnce sync.Once

	if extractor == nil {
		extractor = defaultHTTPRateLimitKey
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if limiterNil {
				nilLimiterLogOnce.Do(func() {
					slog.Warn("keyed rate limit middleware disabled: limiter is nil")
				})
				next.ServeHTTP(w, r)
				return
			}

			key := extractor(r)
			if key == "" {
				key = "global"
			}

			if !limiter.AllowKey(key) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]string{
						"message": "Rate limit exceeded",
						"type":    "rate_limit_error",
						"code":    "rate_limit_exceeded",
					},
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func defaultHTTPRateLimitKey(r *http.Request) string {
	if r == nil {
		return "global"
	}
	if remoteIP := remoteAddrKey(r.RemoteAddr); remoteIP != "" {
		return remoteIP
	}
	return "global"
}

func isNilRateLimiter(limiter any) bool {
	if limiter == nil {
		return true
	}

	value := reflect.ValueOf(limiter)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func forwardedIPKey(forwardedFor string) string {
	if forwardedFor == "" {
		return ""
	}
	if idx := strings.Index(forwardedFor, ","); idx >= 0 {
		return strings.TrimSpace(forwardedFor[:idx])
	}
	return strings.TrimSpace(forwardedFor)
}

func remoteAddrKey(remoteAddr string) string {
	if remoteAddr == "" {
		return ""
	}

	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err == nil && host != "" {
		return host
	}

	return strings.TrimSpace(remoteAddr)
}
