package middleware

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// tokenBucketScript is the Lua script for atomic token bucket operations.
// Returns 1 if request is allowed, 0 if denied.
//
//nolint:gosec // G101 false positive: this is a Lua script for Redis, not credentials
const tokenBucketScript = `
local key = KEYS[1]
local maxTokens = tonumber(ARGV[1])
local refillRate = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local keyTTL = tonumber(ARGV[4])

local data = redis.call('HMGET', key, 'tokens', 'lastRefill')
local tokens = tonumber(data[1]) or maxTokens
local lastRefill = tonumber(data[2]) or now

local elapsed = now - lastRefill
tokens = math.min(maxTokens, tokens + elapsed * refillRate)
local allowed = 0

if tokens >= 1 then
    tokens = tokens - 1
    allowed = 1
end

redis.call('HMSET', key, 'tokens', tokens, 'lastRefill', now)
redis.call('EXPIRE', key, keyTTL)
return allowed
`

// RedisRateLimiter provides distributed token bucket rate limiting.
// Falls back to in-memory limiting if Redis is unavailable (Circuit Breaker pattern).
type RedisRateLimiter struct {
	client        *redis.Client
	fallback      KeyedRateLimiter
	script        *redis.Script
	keyPrefix     string
	maxTokens     int
	refillRate    int
	keyTTLSeconds int

	// Circuit breaker state
	mu           sync.RWMutex
	failures     int
	lastFailure  time.Time
	circuitOpen  bool
	threshold    int           // failures before opening circuit
	resetTimeout time.Duration // time before retrying Redis
}

// RedisRateLimiterConfig holds configuration for the Redis rate limiter.
type RedisRateLimiterConfig struct {
	RedisURL      string
	RedisPassword string
	RedisDB       int
	RedisTLS      bool

	RequestsPerSecond int
	Burst             int
	KeyTTL            time.Duration // Default: 1h

	// Circuit breaker settings
	FailureThreshold int           // Default: 3
	ResetTimeout     time.Duration // Default: 30s
}

// NewRedisRateLimiter creates a new distributed rate limiter.
// Returns an error if Redis connection fails (caller should fall back to in-memory).
func NewRedisRateLimiter(cfg RedisRateLimiterConfig) (*RedisRateLimiter, error) {
	opts, err := redisClientOptions(cfg)
	if err != nil {
		return nil, err
	}

	client := redis.NewClient(opts)

	// Verify connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close() // Best-effort cleanup on connection failure
		return nil, err
	}

	// Set defaults for circuit breaker
	threshold, resetTimeout, keyTTLSeconds := normalizeRedisRateLimiterSettings(cfg)

	return &RedisRateLimiter{
		client:        client,
		fallback:      NewPartitionedRateLimiter(cfg.RequestsPerSecond, cfg.Burst, time.Duration(keyTTLSeconds)*time.Second),
		script:        redis.NewScript(tokenBucketScript),
		keyPrefix:     "ratelimit:",
		maxTokens:     cfg.Burst,
		refillRate:    cfg.RequestsPerSecond,
		keyTTLSeconds: keyTTLSeconds,
		threshold:     threshold,
		resetTimeout:  resetTimeout,
	}, nil
}

// Allow checks if a request is allowed using the token bucket algorithm.
// Uses the default global key for rate limiting.
// Satisfies RateLimiterInterface.
func (rl *RedisRateLimiter) Allow() bool {
	return rl.AllowKey("global")
}

// AllowKey checks if a request is allowed for a specific key (IP, API key, etc.).
func (rl *RedisRateLimiter) AllowKey(key string) bool {
	if key == "" {
		key = "global"
	}

	// Check circuit breaker state
	if rl.isCircuitOpen() {
		return rl.fallback.AllowKey(key)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	now := float64(time.Now().Unix())
	result, err := rl.script.Run(ctx, rl.client, []string{rl.keyPrefix + key},
		rl.maxTokens, rl.refillRate, now, rl.keyTTLSeconds).Int()

	if err != nil {
		rl.recordFailure()
		slog.Warn("redis rate limiter error, using fallback",
			slog.String("component", "ratelimit"),
			slog.String("error", err.Error()),
		)
		return rl.fallback.AllowKey(key)
	}

	rl.recordSuccess()
	return result == 1
}

// isCircuitOpen checks if the circuit breaker is open.
func (rl *RedisRateLimiter) isCircuitOpen() bool {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	if !rl.circuitOpen {
		return false
	}

	// Check if it's time to try again (half-open state)
	if time.Since(rl.lastFailure) > rl.resetTimeout {
		return false // Allow a trial request
	}

	return true
}

// recordFailure records a Redis failure and potentially opens the circuit.
func (rl *RedisRateLimiter) recordFailure() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.failures++
	rl.lastFailure = time.Now()

	if rl.failures >= rl.threshold {
		if !rl.circuitOpen {
			slog.Warn("redis rate limiter circuit breaker OPEN",
				slog.String("component", "ratelimit"),
				slog.Int("failures", rl.failures),
			)
		}
		rl.circuitOpen = true
	}
}

// recordSuccess records a successful Redis operation and resets the circuit.
func (rl *RedisRateLimiter) recordSuccess() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if rl.circuitOpen {
		slog.Info("redis rate limiter circuit breaker CLOSED, Redis recovered",
			slog.String("component", "ratelimit"),
		)
	}
	rl.failures = 0
	rl.circuitOpen = false
}

// Close closes the Redis connection.
func (rl *RedisRateLimiter) Close() error {
	if rl.client != nil {
		return rl.client.Close()
	}
	return nil
}

// HealthCheck verifies Redis connectivity.
func (rl *RedisRateLimiter) HealthCheck(ctx context.Context) error {
	return rl.client.Ping(ctx).Err()
}

func redisClientOptions(cfg RedisRateLimiterConfig) (*redis.Options, error) {
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return nil, err
	}

	if cfg.RedisPassword != "" {
		opts.Password = cfg.RedisPassword
	}
	opts.DB = cfg.RedisDB

	if cfg.RedisTLS {
		if opts.TLSConfig == nil {
			opts.TLSConfig = &tls.Config{}
		}
		if opts.TLSConfig.MinVersion == 0 {
			opts.TLSConfig.MinVersion = tls.VersionTLS12
		}
	}

	return opts, nil
}

func normalizeRedisRateLimiterSettings(cfg RedisRateLimiterConfig) (int, time.Duration, int) {
	threshold := cfg.FailureThreshold
	if threshold <= 0 {
		threshold = 3
	}

	resetTimeout := cfg.ResetTimeout
	if resetTimeout <= 0 {
		resetTimeout = 30 * time.Second
	}

	keyTTL := cfg.KeyTTL
	if keyTTL <= 0 {
		keyTTL = time.Hour
	}
	keyTTLSeconds := int((keyTTL + time.Second - 1) / time.Second)

	return threshold, resetTimeout, keyTTLSeconds
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// NewRedisOrPartitionedRateLimiter returns a Redis-backed keyed limiter when available
// and falls back to a partitioned in-memory keyed limiter during bootstrap failures.
func NewRedisOrPartitionedRateLimiter(cfg RedisRateLimiterConfig, entryTTL time.Duration) (KeyedRateLimiter, io.Closer, error) {
	if strings.TrimSpace(cfg.RedisURL) == "" {
		return NewPartitionedRateLimiter(cfg.RequestsPerSecond, cfg.Burst, entryTTL), nopCloser{}, nil
	}

	limiter, err := NewRedisRateLimiter(cfg)
	if err != nil {
		slog.Warn("redis rate limiter bootstrap failed, using partitioned in-memory fallback",
			slog.String("component", "ratelimit"),
			slog.String("error", err.Error()),
		)
		return NewPartitionedRateLimiter(cfg.RequestsPerSecond, cfg.Burst, entryTTL), nopCloser{}, nil
	}
	return limiter, limiter, nil
}
