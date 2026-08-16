package middleware

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestRateLimitWithInterface_NilLimiter_AllowsRequest(t *testing.T) {
	called := false
	handler := RateLimitWithInterface(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Fatalf("expected request to reach next handler when limiter is nil")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status %d, got %d", http.StatusNoContent, rec.Code)
	}
}

func TestRateLimitWithInterface_TypedNilLimiter_AllowsRequest(t *testing.T) {
	var limiter *RateLimiter

	called := false
	handler := RateLimitWithInterface(limiter)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusAccepted)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Fatalf("expected request to reach next handler when limiter is typed nil")
	}
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, rec.Code)
	}
}

func TestRateLimitWithKey_UsesExtractor(t *testing.T) {
	limiter := NewPartitionedRateLimiter(1, 1, time.Minute)
	handler := RateLimitWithKey(limiter, func(r *http.Request) string {
		return r.Header.Get("X-Api-Key")
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	firstReq := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	firstReq.Header.Set("X-Api-Key", "tenant-a")
	firstRec := httptest.NewRecorder()
	handler.ServeHTTP(firstRec, firstReq)
	if firstRec.Code != http.StatusNoContent {
		t.Fatalf("expected first keyed request to pass, got %d", firstRec.Code)
	}

	secondReq := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	secondReq.Header.Set("X-Api-Key", "tenant-a")
	secondRec := httptest.NewRecorder()
	handler.ServeHTTP(secondRec, secondReq)
	if secondRec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected second keyed request to be rate limited, got %d", secondRec.Code)
	}

	otherTenantReq := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	otherTenantReq.Header.Set("X-Api-Key", "tenant-b")
	otherTenantRec := httptest.NewRecorder()
	handler.ServeHTTP(otherTenantRec, otherTenantReq)
	if otherTenantRec.Code != http.StatusNoContent {
		t.Fatalf("expected separate tenant bucket, got %d", otherTenantRec.Code)
	}
}

func TestRateLimitWithKey_DefaultExtractorStripsRemotePort(t *testing.T) {
	t.Parallel()

	limiter := NewPartitionedRateLimiter(1, 1, time.Minute)
	handler := RateLimitWithKey(limiter, nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	firstReq := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	firstReq.RemoteAddr = "203.0.113.5:40123"
	firstRec := httptest.NewRecorder()
	handler.ServeHTTP(firstRec, firstReq)
	if firstRec.Code != http.StatusNoContent {
		t.Fatalf("expected first direct request to pass, got %d", firstRec.Code)
	}

	secondReq := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	secondReq.RemoteAddr = "203.0.113.5:40124"
	secondRec := httptest.NewRecorder()
	handler.ServeHTTP(secondRec, secondReq)
	if secondRec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected second direct request from same IP to be rate limited, got %d", secondRec.Code)
	}
}

func TestRateLimitWithKey_DefaultExtractorIgnoresSpoofedForwardedFor(t *testing.T) {
	t.Parallel()

	limiter := NewPartitionedRateLimiter(1, 1, time.Minute)
	handler := RateLimitWithKey(limiter, nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	firstReq := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	firstReq.RemoteAddr = "203.0.113.9:41001"
	firstReq.Header.Set("X-Forwarded-For", "198.51.100.10")
	firstRec := httptest.NewRecorder()
	handler.ServeHTTP(firstRec, firstReq)
	if firstRec.Code != http.StatusNoContent {
		t.Fatalf("expected first request to pass, got %d", firstRec.Code)
	}

	secondReq := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	secondReq.RemoteAddr = "203.0.113.9:41002"
	secondReq.Header.Set("X-Forwarded-For", "198.51.100.11")
	secondRec := httptest.NewRecorder()
	handler.ServeHTTP(secondRec, secondReq)
	if secondRec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected second request with rotated X-Forwarded-For to be limited, got %d", secondRec.Code)
	}
}

func TestPartitionedRateLimiterIndependentBuckets(t *testing.T) {
	t.Parallel()

	limiter := NewPartitionedRateLimiterWithConfig(PartitionedRateLimiterConfig{
		RequestsPerSecond: 1,
		Burst:             1,
		EntryTTL:          time.Minute,
		MaxPartitions:     4,
		CleanupInterval:   time.Minute,
	})

	if !limiter.AllowKey("tenant-a") {
		t.Fatalf("expected tenant-a first request to pass")
	}
	if limiter.AllowKey("tenant-a") {
		t.Fatalf("expected tenant-a second request to be limited")
	}
	if !limiter.AllowKey("tenant-b") {
		t.Fatalf("expected tenant-b first request to use an independent bucket")
	}
}

func TestPartitionedRateLimiterStaleCleanup(t *testing.T) {
	t.Parallel()

	limiter := NewPartitionedRateLimiterWithConfig(PartitionedRateLimiterConfig{
		RequestsPerSecond: 1,
		Burst:             1,
		EntryTTL:          20 * time.Millisecond,
		MaxPartitions:     4,
		CleanupInterval:   10 * time.Millisecond,
	})

	if !limiter.AllowKey("tenant-a") {
		t.Fatalf("expected initial request to pass")
	}
	time.Sleep(30 * time.Millisecond)
	if !limiter.AllowKey("tenant-b") {
		t.Fatalf("expected new bucket request to pass")
	}

	limiter.mu.Lock()
	_, hasA := limiter.partitions["tenant-a"]
	_, hasB := limiter.partitions["tenant-b"]
	partitionCount := len(limiter.partitions)
	limiter.mu.Unlock()

	if hasA {
		t.Fatalf("expected stale tenant-a bucket to be cleaned up")
	}
	if !hasB || partitionCount != 1 {
		t.Fatalf("expected only the fresh tenant-b bucket to remain, count=%d hasB=%v", partitionCount, hasB)
	}
}

func TestPartitionedRateLimiterMaxPartitionsUsesOverflowBucket(t *testing.T) {
	t.Parallel()

	limiter := NewPartitionedRateLimiterWithConfig(PartitionedRateLimiterConfig{
		RequestsPerSecond: 1,
		Burst:             1,
		EntryTTL:          time.Minute,
		MaxPartitions:     2,
		CleanupInterval:   time.Minute,
	})

	if !limiter.AllowKey("tenant-a") || !limiter.AllowKey("tenant-b") {
		t.Fatalf("expected two dedicated buckets before hitting the bound")
	}
	if !limiter.AllowKey("tenant-c") {
		t.Fatalf("expected first overflow-bucket request to pass")
	}
	if limiter.AllowKey("tenant-d") {
		t.Fatalf("expected second unseen key to share and exhaust the overflow bucket")
	}

	limiter.mu.Lock()
	partitionCount := len(limiter.partitions)
	limiter.mu.Unlock()
	if partitionCount != 2 {
		t.Fatalf("expected dedicated partition count to stay bounded at 2, got %d", partitionCount)
	}
}

func TestPartitionedRateLimiterKeyChurnDoesNotResetOverflowBucket(t *testing.T) {
	t.Parallel()

	limiter := NewPartitionedRateLimiterWithConfig(PartitionedRateLimiterConfig{
		RequestsPerSecond: 1,
		Burst:             1,
		EntryTTL:          time.Minute,
		MaxPartitions:     1,
		CleanupInterval:   time.Minute,
	})

	if !limiter.AllowKey("stable") {
		t.Fatalf("expected stable key to get the dedicated bucket")
	}
	if !limiter.AllowKey("churn-1") {
		t.Fatalf("expected first overflow request to pass")
	}
	if limiter.AllowKey("churn-2") {
		t.Fatalf("expected rotated overflow key to stay limited")
	}
	if limiter.AllowKey("churn-3") {
		t.Fatalf("expected additional churn keys to share the same overflow limiter")
	}
}

func TestPartitionedRateLimiterConcurrentAccess(t *testing.T) {
	t.Parallel()

	limiter := NewPartitionedRateLimiterWithConfig(PartitionedRateLimiterConfig{
		RequestsPerSecond: 1000,
		Burst:             1000,
		EntryTTL:          time.Minute,
		MaxPartitions:     8,
		CleanupInterval:   time.Minute,
	})

	keys := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				limiter.AllowKey(keys[(offset+j)%len(keys)])
			}
		}(i)
	}
	wg.Wait()

	limiter.mu.Lock()
	partitionCount := len(limiter.partitions)
	limiter.mu.Unlock()
	if partitionCount > limiter.maxPartitions {
		t.Fatalf("expected bounded partitions, got %d > %d", partitionCount, limiter.maxPartitions)
	}
}

func TestNewPartitionedRateLimiterWithConfigNormalizesInvalidValues(t *testing.T) {
	t.Parallel()

	limiter := NewPartitionedRateLimiterWithConfig(PartitionedRateLimiterConfig{})
	if limiter.requests != 1 || limiter.burst != 1 {
		t.Fatalf("expected invalid request and burst settings to normalize to 1, got requests=%d burst=%d", limiter.requests, limiter.burst)
	}
	if limiter.entryTTL <= 0 || limiter.cleanupInterval <= 0 || limiter.maxPartitions <= 0 {
		t.Fatalf("expected normalized positive config, got ttl=%s cleanup=%s max=%d", limiter.entryTTL, limiter.cleanupInterval, limiter.maxPartitions)
	}
}
