package middleware

import (
	"net/http"
	"net/http/httptest"
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
