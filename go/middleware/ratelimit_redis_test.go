package middleware

import (
	"crypto/tls"
	"strings"
	"testing"
	"time"
)

// Compile-time interface compliance check.
var _ RateLimiterInterface = (*RedisRateLimiter)(nil)
var _ KeyedRateLimiter = (*RedisRateLimiter)(nil)

func TestNormalizeRedisRateLimiterSettings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		threshold        int
		resetTimeout     time.Duration
		keyTTL           time.Duration
		wantThreshold    int
		wantResetTimeout time.Duration
		wantKeyTTL       int
	}{
		{
			name:             "zero values use defaults",
			threshold:        0,
			resetTimeout:     0,
			keyTTL:           0,
			wantThreshold:    3,
			wantResetTimeout: 30 * time.Second,
			wantKeyTTL:       3600,
		},
		{
			name:             "negative values use defaults",
			threshold:        -1,
			resetTimeout:     -1 * time.Second,
			keyTTL:           -1 * time.Second,
			wantThreshold:    3,
			wantResetTimeout: 30 * time.Second,
			wantKeyTTL:       3600,
		},
		{
			name:             "sub-second ttl is clamped to one second",
			threshold:        1,
			resetTimeout:     1 * time.Second,
			keyTTL:           500 * time.Millisecond,
			wantThreshold:    1,
			wantResetTimeout: 1 * time.Second,
			wantKeyTTL:       1,
		},
		{
			name:             "fractional-second ttl rounds up",
			threshold:        1,
			resetTimeout:     1 * time.Second,
			keyTTL:           1500 * time.Millisecond,
			wantThreshold:    1,
			wantResetTimeout: 1 * time.Second,
			wantKeyTTL:       2,
		},
		{
			name:             "custom values are preserved",
			threshold:        5,
			resetTimeout:     60 * time.Second,
			keyTTL:           90 * time.Second,
			wantThreshold:    5,
			wantResetTimeout: 60 * time.Second,
			wantKeyTTL:       90,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			threshold, resetTimeout, keyTTL := normalizeRedisRateLimiterSettings(RedisRateLimiterConfig{
				FailureThreshold: tt.threshold,
				ResetTimeout:     tt.resetTimeout,
				KeyTTL:           tt.keyTTL,
			})

			if threshold != tt.wantThreshold {
				t.Fatalf("threshold = %d, want %d", threshold, tt.wantThreshold)
			}
			if resetTimeout != tt.wantResetTimeout {
				t.Fatalf("resetTimeout = %v, want %v", resetTimeout, tt.wantResetTimeout)
			}
			if keyTTL != tt.wantKeyTTL {
				t.Fatalf("keyTTL = %d, want %d", keyTTL, tt.wantKeyTTL)
			}
		})
	}
}

func TestRedisClientOptions_TLSFromConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		url            string
		redisTLS       bool
		wantTLS        bool
		wantMinVersion uint16
	}{
		{
			name:           "redis url enables tls when RedisTLS is true",
			url:            "redis://localhost:6379/0",
			redisTLS:       true,
			wantTLS:        true,
			wantMinVersion: tls.VersionTLS12,
		},
		{
			name:     "redis url remains plaintext when RedisTLS is false",
			url:      "redis://localhost:6379/0",
			redisTLS: false,
			wantTLS:  false,
		},
		{
			name:           "rediss url stays tls even when RedisTLS is false",
			url:            "rediss://localhost:6379/0",
			redisTLS:       false,
			wantTLS:        true,
			wantMinVersion: tls.VersionTLS12,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts, err := redisClientOptions(RedisRateLimiterConfig{
				RedisURL: tt.url,
				RedisTLS: tt.redisTLS,
			})
			if err != nil {
				t.Fatalf("redisClientOptions() error = %v", err)
			}

			gotTLS := opts.TLSConfig != nil
			if gotTLS != tt.wantTLS {
				t.Fatalf("TLS enabled = %v, want %v", gotTLS, tt.wantTLS)
			}

			if tt.wantTLS && opts.TLSConfig.MinVersion < tt.wantMinVersion {
				t.Fatalf("TLS min version = %v, want >= %v", opts.TLSConfig.MinVersion, tt.wantMinVersion)
			}
		})
	}
}

func TestTokenBucketScript_UsesConfigurableTTL(t *testing.T) {
	t.Parallel()

	if !strings.Contains(tokenBucketScript, "local keyTTL = tonumber(ARGV[4])") {
		t.Fatal("token bucket script does not read key TTL from ARGV[4]")
	}
	if strings.Count(tokenBucketScript, "redis.call('EXPIRE', key, keyTTL)") != 1 {
		t.Fatal("token bucket script should set EXPIRE once with keyTTL")
	}
	if strings.Contains(tokenBucketScript, "3600") {
		t.Fatal("token bucket script should not hardcode 3600 seconds")
	}
}

func TestRedisRateLimiter_CircuitBreakerStateTransitions(t *testing.T) {
	t.Parallel()

	// Create a RedisRateLimiter with zero-value fields to test circuit
	// breaker logic in isolation (no Redis connection needed).
	rl := &RedisRateLimiter{
		threshold:    3,
		resetTimeout: 50 * time.Millisecond,
		fallback:     NewRateLimiter(100, 100),
	}

	// Initially circuit is closed
	if rl.isCircuitOpen() {
		t.Fatal("expected circuit to be closed initially")
	}

	// Record failures below threshold
	rl.recordFailure()
	rl.recordFailure()
	if rl.isCircuitOpen() {
		t.Fatal("expected circuit to remain closed below threshold")
	}

	// Third failure should open the circuit
	rl.recordFailure()
	if !rl.isCircuitOpen() {
		t.Fatal("expected circuit to be open after reaching threshold")
	}

	// Success should close the circuit
	rl.recordSuccess()
	if rl.isCircuitOpen() {
		t.Fatal("expected circuit to be closed after success")
	}

	// Open circuit again then wait for reset timeout
	rl.recordFailure()
	rl.recordFailure()
	rl.recordFailure()
	if !rl.isCircuitOpen() {
		t.Fatal("expected circuit to be open")
	}

	// Wait for reset timeout (half-open state)
	time.Sleep(60 * time.Millisecond)
	if rl.isCircuitOpen() {
		t.Fatal("expected circuit to be half-open after reset timeout")
	}
}

func TestNewRedisOrPartitionedRateLimiter_EmptyURLFallsBackToPartitioned(t *testing.T) {
	t.Parallel()

	limiter, closer, err := NewRedisOrPartitionedRateLimiter(RedisRateLimiterConfig{
		RequestsPerSecond: 1,
		Burst:             1,
	}, time.Minute)
	if err != nil {
		t.Fatalf("NewRedisOrPartitionedRateLimiter() error = %v", err)
	}
	defer func() { _ = closer.Close() }()

	if _, ok := limiter.(*PartitionedRateLimiter); !ok {
		t.Fatalf("expected partitioned fallback, got %T", limiter)
	}
}

func TestRedisRateLimiter_AllowKey_UsesPerKeyFallbackWhenCircuitOpen(t *testing.T) {
	t.Parallel()

	rl := &RedisRateLimiter{
		fallback:      NewPartitionedRateLimiter(1, 1, time.Minute),
		circuitOpen:   true,
		lastFailure:   time.Now(),
		resetTimeout:  time.Minute,
		threshold:     1,
		keyPrefix:     "ratelimit:",
		maxTokens:     1,
		refillRate:    1,
		keyTTLSeconds: 60,
	}

	if !rl.AllowKey("tenant-a") {
		t.Fatal("expected first tenant-a request to pass")
	}
	if rl.AllowKey("tenant-a") {
		t.Fatal("expected second tenant-a request to be limited")
	}
	if !rl.AllowKey("tenant-b") {
		t.Fatal("expected tenant-b to retain an independent fallback bucket")
	}
}
