package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type flusherRecorder struct {
	*httptest.ResponseRecorder
	flushed bool
}

func newFlusherRecorder() *flusherRecorder {
	return &flusherRecorder{ResponseRecorder: httptest.NewRecorder()}
}

func (r *flusherRecorder) Flush() {
	r.flushed = true
}

func varySet(header http.Header) map[string]struct{} {
	set := make(map[string]struct{})
	for _, value := range header.Values("Vary") {
		for _, part := range strings.Split(value, ",") {
			token := strings.TrimSpace(part)
			if token == "" {
				continue
			}
			set[strings.ToLower(token)] = struct{}{}
		}
	}
	return set
}

func requireVaryContains(t *testing.T, header http.Header, fields ...string) {
	t.Helper()
	set := varySet(header)
	for _, field := range fields {
		if _, ok := set[strings.ToLower(field)]; !ok {
			t.Fatalf("expected Vary header to include %q, got %v", field, header.Values("Vary"))
		}
	}
}

func decodeJSONLogLines(t *testing.T, data []byte) []map[string]any {
	t.Helper()

	dec := json.NewDecoder(bytes.NewReader(data))
	var records []map[string]any
	for {
		var rec map[string]any
		err := dec.Decode(&rec)
		if err == nil {
			records = append(records, rec)
			continue
		}
		if err == io.EOF {
			break
		}
		t.Fatalf("failed to decode slog output: %v", err)
	}
	return records
}

func TestChainPreservesFlusher(t *testing.T) {
	flusherSeen := false

	handler := Chain(
		Recovery(),
		RequestID(),
		Logging(),
	)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("data: ping\n\n"))
		if f, ok := w.(http.Flusher); ok {
			flusherSeen = true
			f.Flush()
		}
	}))

	rec := newFlusherRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	handler.ServeHTTP(rec, req)

	if !flusherSeen {
		t.Fatalf("expected wrapped response writer to implement http.Flusher")
	}
	if !rec.flushed {
		t.Fatalf("expected flush to reach underlying writer")
	}
}

func TestCircuitBreakerPreservesFlusher(t *testing.T) {
	cfg := DefaultCircuitBreakerConfig()
	cfg.ReadyToTrip = 100

	flusherSeen := false
	handler := Chain(
		CircuitBreaker(cfg),
		Logging(),
	)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("data: ping\n\n"))
		if f, ok := w.(http.Flusher); ok {
			flusherSeen = true
			f.Flush()
		}
	}))

	rec := newFlusherRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if !flusherSeen {
		t.Fatalf("expected circuit breaker wrapped writer to implement http.Flusher")
	}
	if !rec.flushed {
		t.Fatalf("expected flush to reach underlying writer")
	}
}

func TestRecovery_LogsRequestIDWhenWrappingRequestID(t *testing.T) {
	original := slog.Default()
	var logs bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(original)

	handler := Chain(
		Recovery(),
		RequestID(),
	)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500 from recovery, got %d", rec.Code)
	}
	responseRequestID := rec.Header().Get("X-Request-ID")
	if responseRequestID == "" {
		t.Fatalf("expected recovery response to include X-Request-ID")
	}

	records := decodeJSONLogLines(t, logs.Bytes())
	found := false
	for _, record := range records {
		msg, _ := record["msg"].(string)
		if msg != "panic recovered" {
			continue
		}
		found = true
		gotRequestID, _ := record["request_id"].(string)
		if gotRequestID == "" {
			t.Fatalf("expected panic log to include request_id, got empty; log=%v", record)
		}
		if gotRequestID != responseRequestID {
			t.Fatalf("expected panic log request_id %q to match response header %q", gotRequestID, responseRequestID)
		}
	}
	if !found {
		t.Fatalf("expected panic recovered log record, got logs: %s", logs.String())
	}
}

func TestCORS_PreflightAllowedOrigin_ShortCircuitsWith204(t *testing.T) {
	called := false
	handler := CORS([]string{"https://app.example.com"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/v1/models", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if called {
		t.Fatalf("expected preflight to short-circuit before next handler")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("expected Access-Control-Allow-Origin header, got %q", got)
	}
	requireVaryContains(t, rec.Header(),
		"Origin",
		"Access-Control-Request-Method",
		"Access-Control-Request-Headers",
	)
}

func TestCORS_OptionsWithoutPreflight_PassesThrough(t *testing.T) {
	called := false
	handler := CORS([]string{"*"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusAccepted)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/v1/models", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Fatalf("expected non-preflight OPTIONS to reach next handler")
	}
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status 202 from next handler, got %d", rec.Code)
	}
	requireVaryContains(t, rec.Header(),
		"Origin",
		"Access-Control-Request-Method",
		"Access-Control-Request-Headers",
	)
}

func TestCORS_PreflightDisallowedOrigin_PassesThrough(t *testing.T) {
	called := false
	handler := CORS([]string{"https://allowed.example.com"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/v1/models", nil)
	req.Header.Set("Origin", "https://blocked.example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Fatalf("expected disallowed-origin preflight to reach next handler")
	}
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405 from next handler, got %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("did not expect Access-Control-Allow-Origin header, got %q", got)
	}
	requireVaryContains(t, rec.Header(),
		"Origin",
		"Access-Control-Request-Method",
		"Access-Control-Request-Headers",
	)
}

func TestCORS_SimpleRequest_AddsOnlyOriginVary(t *testing.T) {
	handler := CORS([]string{"https://allowed.example.com"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	requireVaryContains(t, rec.Header(), "Origin")

	set := varySet(rec.Header())
	if _, ok := set[strings.ToLower("Access-Control-Request-Method")]; ok {
		t.Fatalf("did not expect Access-Control-Request-Method in Vary for non-OPTIONS request")
	}
	if _, ok := set[strings.ToLower("Access-Control-Request-Headers")]; ok {
		t.Fatalf("did not expect Access-Control-Request-Headers in Vary for non-OPTIONS request")
	}
}
