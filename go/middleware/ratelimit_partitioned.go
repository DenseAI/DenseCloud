package middleware

import (
	"sync"
	"time"
)

const (
	defaultPartitionEntryTTL        = 5 * time.Minute
	defaultPartitionMaxEntries      = 4096
	defaultPartitionCleanupInterval = time.Minute
)

// PartitionedRateLimiterConfig configures the bounded in-memory keyed limiter.
type PartitionedRateLimiterConfig struct {
	RequestsPerSecond int
	Burst             int
	EntryTTL          time.Duration
	MaxPartitions     int
	CleanupInterval   time.Duration
}

type partitionEntry struct {
	limiter  *RateLimiter
	lastSeen time.Time
}

// PartitionedRateLimiter provides bounded per-key in-memory rate limiting with
// periodic stale cleanup. Once MaxPartitions is reached, unseen keys share a
// single overflow bucket instead of evicting active buckets.
type PartitionedRateLimiter struct {
	mu              sync.Mutex
	requests        int
	burst           int
	entryTTL        time.Duration
	maxPartitions   int
	cleanupInterval time.Duration
	nextCleanup     time.Time
	partitions      map[string]*partitionEntry
	overflow        *partitionEntry
}

// NewPartitionedRateLimiter creates a keyed in-memory rate limiter.
func NewPartitionedRateLimiter(requestsPerSecond int, burst int, entryTTL time.Duration) *PartitionedRateLimiter {
	return NewPartitionedRateLimiterWithConfig(PartitionedRateLimiterConfig{
		RequestsPerSecond: requestsPerSecond,
		Burst:             burst,
		EntryTTL:          entryTTL,
	})
}

// NewPartitionedRateLimiterWithConfig creates a keyed in-memory limiter with
// explicit bounds and cleanup settings.
func NewPartitionedRateLimiterWithConfig(cfg PartitionedRateLimiterConfig) *PartitionedRateLimiter {
	cfg = normalizePartitionedRateLimiterConfig(cfg)
	now := time.Now()
	return &PartitionedRateLimiter{
		requests:        cfg.RequestsPerSecond,
		burst:           cfg.Burst,
		entryTTL:        cfg.EntryTTL,
		maxPartitions:   cfg.MaxPartitions,
		cleanupInterval: cfg.CleanupInterval,
		nextCleanup:     now.Add(cfg.CleanupInterval),
		partitions:      make(map[string]*partitionEntry),
		overflow:        newPartitionEntry(cfg.RequestsPerSecond, cfg.Burst, now),
	}
}

// AllowKey checks the per-key bucket and periodically evicts stale buckets.
// When the partition budget is exhausted, unseen keys fall back to the shared
// overflow bucket instead of resetting active buckets through eviction churn.
func (rl *PartitionedRateLimiter) AllowKey(key string) bool {
	if key == "" {
		key = "global"
	}

	now := time.Now()
	entry := rl.partitionForKey(now, key)
	return entry.limiter.Allow()
}

func (rl *PartitionedRateLimiter) partitionForKey(now time.Time, key string) *partitionEntry {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.cleanupStaleLocked(now)

	if entry := rl.partitions[key]; entry != nil {
		entry.lastSeen = now
		return entry
	}

	if len(rl.partitions) < rl.maxPartitions {
		entry := newPartitionEntry(rl.requests, rl.burst, now)
		rl.partitions[key] = entry
		return entry
	}

	rl.overflow.lastSeen = now
	return rl.overflow
}

func (rl *PartitionedRateLimiter) cleanupStaleLocked(now time.Time) {
	if now.Before(rl.nextCleanup) {
		return
	}
	for key, entry := range rl.partitions {
		if now.Sub(entry.lastSeen) >= rl.entryTTL {
			delete(rl.partitions, key)
		}
	}
	rl.nextCleanup = now.Add(rl.cleanupInterval)
}

func newPartitionEntry(requestsPerSecond int, burst int, now time.Time) *partitionEntry {
	return &partitionEntry{
		limiter:  NewRateLimiter(requestsPerSecond, burst),
		lastSeen: now,
	}
}

func normalizePartitionedRateLimiterConfig(cfg PartitionedRateLimiterConfig) PartitionedRateLimiterConfig {
	if cfg.RequestsPerSecond <= 0 {
		cfg.RequestsPerSecond = 1
	}
	if cfg.Burst <= 0 {
		cfg.Burst = 1
	}
	if cfg.EntryTTL <= 0 {
		cfg.EntryTTL = defaultPartitionEntryTTL
	}
	if cfg.MaxPartitions <= 0 {
		cfg.MaxPartitions = defaultPartitionMaxEntries
	}
	if cfg.CleanupInterval <= 0 {
		cfg.CleanupInterval = defaultPartitionCleanupInterval
		if cfg.EntryTTL < cfg.CleanupInterval {
			cfg.CleanupInterval = cfg.EntryTTL
		}
	}
	if cfg.CleanupInterval > cfg.EntryTTL {
		cfg.CleanupInterval = cfg.EntryTTL
	}
	if cfg.CleanupInterval <= 0 {
		cfg.CleanupInterval = time.Second
	}
	return cfg
}
