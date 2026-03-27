package middleware

import (
	"sync"
	"time"
)

type partitionEntry struct {
	limiter  *RateLimiter
	lastSeen time.Time
}

// PartitionedRateLimiter provides per-key in-memory rate limiting with TTL cleanup.
type PartitionedRateLimiter struct {
	mu         sync.Mutex
	requests   int
	burst      int
	entryTTL   time.Duration
	partitions map[string]*partitionEntry
}

// NewPartitionedRateLimiter creates a keyed in-memory rate limiter.
func NewPartitionedRateLimiter(requestsPerSecond int, burst int, entryTTL time.Duration) *PartitionedRateLimiter {
	if entryTTL <= 0 {
		entryTTL = 5 * time.Minute
	}
	return &PartitionedRateLimiter{
		requests:   requestsPerSecond,
		burst:      burst,
		entryTTL:   entryTTL,
		partitions: make(map[string]*partitionEntry),
	}
}

// AllowKey checks the per-key bucket and lazily evicts stale buckets.
func (rl *PartitionedRateLimiter) AllowKey(key string) bool {
	if key == "" {
		key = "global"
	}

	now := time.Now()

	rl.mu.Lock()
	defer rl.mu.Unlock()

	for candidate, entry := range rl.partitions {
		if now.Sub(entry.lastSeen) > rl.entryTTL {
			delete(rl.partitions, candidate)
		}
	}

	entry := rl.partitions[key]
	if entry == nil {
		entry = &partitionEntry{
			limiter:  NewRateLimiter(rl.requests, rl.burst),
			lastSeen: now,
		}
		rl.partitions[key] = entry
	}
	entry.lastSeen = now
	return entry.limiter.Allow()
}
