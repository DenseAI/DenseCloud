package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DenseAI/DenseCloud/go/telemetry"
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

func TestRequestTimeoutAllowsSSEFlushBeforeContextDeadline(t *testing.T) {
	ctxDone := make(chan error, 1)
	handler := Chain(
		RequestID(),
		RequestTimeout(10*time.Millisecond),
		Logging(),
	)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: ready\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
		ctxDone <- r.Context().Err()
	}))

	rec := newFlusherRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/events", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if !rec.flushed {
		t.Fatalf("expected SSE flush to reach the underlying writer")
	}
	if got := rec.Body.String(); got != "data: ready\n\n" {
		t.Fatalf("unexpected SSE body %q", got)
	}
	select {
	case err := <-ctxDone:
		if err != context.DeadlineExceeded {
			t.Fatalf("expected context deadline exceeded, got %v", err)
		}
	default:
		t.Fatalf("expected handler to observe timeout cancellation")
	}
}

func TestLoggingRecordsStreamingStatusAndBytes(t *testing.T) {
	original := slog.Default()
	var logs bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(original)

	chunks := []string{"event: token\n", "data: hello\n\n"}
	handler := Chain(
		RequestID(),
		Logging(),
	)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range chunks {
			_, _ = w.Write([]byte(chunk))
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	}))

	rec := newFlusherRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/events", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if !rec.flushed {
		t.Fatalf("expected streaming response to flush")
	}
	wantBytes := len(strings.Join(chunks, ""))

	records := decodeJSONLogLines(t, logs.Bytes())
	if len(records) != 1 {
		t.Fatalf("expected one request log, got %d: %s", len(records), logs.String())
	}
	record := records[0]
	if got, _ := record["msg"].(string); got != "request completed" {
		t.Fatalf("unexpected log message %q", got)
	}
	if got, _ := record["request_id"].(string); got == "" {
		t.Fatalf("expected request_id in streaming log, got %v", record)
	}
	if got, _ := record["status_code"].(float64); int(got) != http.StatusOK {
		t.Fatalf("expected logged status 200, got %v", record["status_code"])
	}
	if got, _ := record["bytes_written"].(float64); int(got) != wantBytes {
		t.Fatalf("expected logged bytes %d, got %v", wantBytes, record["bytes_written"])
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

func TestHTTPMetricsWithRecovery_RecordsPanicAs500(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		chain func(*telemetry.HTTPMetrics, http.Handler) http.Handler
	}{
		{
			name: "metrics inside recovery",
			chain: func(metrics *telemetry.HTTPMetrics, next http.Handler) http.Handler {
				return Recovery()(metrics.Middleware()(next))
			},
		},
		{
			name: "metrics outside recovery",
			chain: func(metrics *telemetry.HTTPMetrics, next http.Handler) http.Handler {
				return metrics.Middleware()(Recovery()(next))
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			metrics := telemetry.NewHTTPMetrics(telemetry.HTTPMetricsConfig{ServiceName: "dense-test"})
			handler := tt.chain(metrics, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				panic("boom")
			}))

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/panic", nil))
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("expected recovered response status 500, got %d", rec.Code)
			}

			body := captureHTTPMetrics(t, metrics)
			requireHTTPMetric(t, body, `densecloud_http_in_flight_requests{service="dense-test"} 0`)
			requireHTTPMetric(t, body, `densecloud_http_requests_total{service="dense-test",method="GET",path="/v1/panic",status_class="5xx"} 1`)
			requireHTTPMetric(t, body, `densecloud_http_request_errors_total{service="dense-test",method="GET",path="/v1/panic",status_class="5xx"} 1`)
			requireHTTPMetric(t, body, `densecloud_http_request_duration_seconds_count{service="dense-test",method="GET",path="/v1/panic"} 1`)
		})
	}
}

func TestContentTypeForPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		middleware func(http.Handler) http.Handler
		path       string
		want       string
	}{
		{
			name:       "default v1 behavior",
			middleware: ContentType("application/json"),
			path:       "/v1/models",
			want:       "application/json",
		},
		{
			name:       "direct v1 callers still cover exact base path",
			middleware: ContentType("application/json"),
			path:       "/v1",
			want:       "application/json",
		},
		{
			name:       "custom api prefix",
			middleware: ContentTypeForPrefix("/api", "application/json"),
			path:       "/api/models",
			want:       "application/json",
		},
		{
			name:       "non api path unaffected",
			middleware: ContentTypeForPrefix("/api", "application/json"),
			path:       "/health/ready",
			want:       "",
		},
		{
			name:       "similar prefix unaffected",
			middleware: ContentTypeForPrefix("/api", "application/json"),
			path:       "/apix/models",
			want:       "",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler := tt.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if got := rec.Header().Get("Content-Type"); got != tt.want {
				t.Fatalf("Content-Type = %q, want %q", got, tt.want)
			}
		})
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

func captureHTTPMetrics(t *testing.T, metrics *telemetry.HTTPMetrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return rec.Body.String()
}

func requireHTTPMetric(t *testing.T, body, want string) {
	t.Helper()
	if !strings.Contains(body, want) {
		t.Fatalf("expected metric %q, got %q", want, body)
	}
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
