package server

import (
	"net/http"
	"testing"
	"time"
)

func TestDefaultHTTPMiddleware_OrderAndOptionalTimeout(t *testing.T) {
	t.Parallel()

	withTimeout := DefaultHTTPMiddleware(HTTPMiddlewarePresetConfig{
		TracerName:     "dense-test",
		RequestTimeout: 5 * time.Second,
	})
	if len(withTimeout) != 5 {
		t.Fatalf("expected 5 middleware entries with timeout, got %d", len(withTimeout))
	}

	withoutTimeout := DefaultHTTPMiddleware(HTTPMiddlewarePresetConfig{TracerName: "dense-test"})
	if len(withoutTimeout) != 4 {
		t.Fatalf("expected 4 middleware entries without timeout, got %d", len(withoutTimeout))
	}

	order := make([]string, 0, len(withTimeout))
	var handler http.Handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for idx, mw := range withTimeout {
		name := "middleware"
		switch idx {
		case 0:
			name = "request_id"
		case 1:
			name = "recovery"
		case 2:
			name = "timeout"
		case 3:
			name = "tracing"
		case 4:
			name = "logging"
		}
		order = append(order, name)
		handler = mw(handler)
	}
	if handler == nil {
		t.Fatalf("expected middleware chain to produce a handler")
	}

	expected := []string{"request_id", "recovery", "timeout", "tracing", "logging"}
	for i := range expected {
		if order[i] != expected[i] {
			t.Fatalf("middleware order[%d] = %q, want %q", i, order[i], expected[i])
		}
	}
}
