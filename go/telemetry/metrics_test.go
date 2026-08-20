package telemetry

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPMetricsMiddlewareAndHandler(t *testing.T) {
	t.Parallel()

	metrics := NewHTTPMetrics(HTTPMetricsConfig{
		ServiceName: "dense-test",
		IgnorePaths: []string{"/health", "/metrics"},
	})

	handler := metrics.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fail" {
			http.Error(w, "boom", http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))

	okReq := httptest.NewRequest(http.MethodPost, "/v1/models", nil)
	okRec := httptest.NewRecorder()
	handler.ServeHTTP(okRec, okReq)

	failReq := httptest.NewRequest(http.MethodGet, "/fail", nil)
	failRec := httptest.NewRecorder()
	handler.ServeHTTP(failRec, failReq)

	metricsRec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	body := metricsRec.Body.String()
	if !strings.Contains(body, `densecloud_http_requests_total{service="dense-test",method="GET",path="/fail",status_class="5xx"} 1`) {
		t.Fatalf("expected failed request metric, got %q", body)
	}
	if !strings.Contains(body, `densecloud_http_request_errors_total{service="dense-test",method="GET",path="/fail",status_class="5xx"} 1`) {
		t.Fatalf("expected error counter metric, got %q", body)
	}
	if !strings.Contains(body, `densecloud_http_request_duration_seconds_count{service="dense-test",method="POST",path="/v1/models"} 1`) {
		t.Fatalf("expected duration count metric, got %q", body)
	}
}

func TestHTTPMetricsMiddleware_RecordsPanicBeforeRepanic(t *testing.T) {
	t.Parallel()

	metrics := NewHTTPMetrics(HTTPMetricsConfig{ServiceName: "dense-test"})
	handler := metrics.Middleware()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	func() {
		defer func() {
			if rec := recover(); rec == nil {
				t.Fatalf("expected metrics middleware to re-panic")
			}
		}()
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/panic", nil))
	}()

	body := captureHTTPMetricsBody(metrics)
	if !strings.Contains(body, `densecloud_http_in_flight_requests{service="dense-test"} 0`) {
		t.Fatalf("expected in-flight gauge to return to zero, got %q", body)
	}
	if !strings.Contains(body, `densecloud_http_requests_total{service="dense-test",method="GET",path="/panic",status_class="5xx"} 1`) {
		t.Fatalf("expected panic request to be recorded as 5xx, got %q", body)
	}
	if !strings.Contains(body, `densecloud_http_request_duration_seconds_count{service="dense-test",method="GET",path="/panic"} 1`) {
		t.Fatalf("expected panic request duration to be recorded, got %q", body)
	}
}

func TestHTTPMetricsResponseWriterPreservesStreamingInterfaces(t *testing.T) {
	t.Parallel()

	rec := &metricsCapabilityRecorder{ResponseRecorder: httptest.NewRecorder()}
	wrapped := &metricsResponseWriter{ResponseWriter: rec}

	flusher, ok := any(wrapped).(http.Flusher)
	if !ok {
		t.Fatalf("expected metrics writer to expose http.Flusher")
	}
	flusher.Flush()
	if !rec.flushed {
		t.Fatalf("expected Flush to reach underlying writer")
	}

	hijacker, ok := any(wrapped).(http.Hijacker)
	if !ok {
		t.Fatalf("expected metrics writer to expose http.Hijacker")
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		t.Fatalf("Hijack() error = %v", err)
	}
	_ = conn.Close()
	if !rec.hijacked {
		t.Fatalf("expected Hijack to reach underlying writer")
	}

	pusher, ok := any(wrapped).(http.Pusher)
	if !ok {
		t.Fatalf("expected metrics writer to expose http.Pusher")
	}
	if err := pusher.Push("/asset.js", nil); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	if !rec.pushed {
		t.Fatalf("expected Push to reach underlying writer")
	}

	readerFrom, ok := any(wrapped).(io.ReaderFrom)
	if !ok {
		t.Fatalf("expected metrics writer to expose io.ReaderFrom")
	}
	if _, err := readerFrom.ReadFrom(strings.NewReader("data")); err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	if !rec.readFrom {
		t.Fatalf("expected ReadFrom to reach underlying writer")
	}
}

func TestHTTPMetricsHandler_AppendsGRPCCollectors(t *testing.T) {
	t.Parallel()

	grpcMetrics := NewGRPCMetrics(GRPCMetricsConfig{ServiceName: "dense-test"})
	grpcMetrics.BeginRPC()
	grpcMetrics.ObserveRPC("/dense.Service/Call", "unary", "OK", 0.02)

	metrics := NewHTTPMetrics(HTTPMetricsConfig{
		ServiceName: "dense-test",
		Collectors:  []PrometheusCollector{grpcMetrics},
	})

	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	body := rec.Body.String()
	if !strings.Contains(body, `densecloud_grpc_requests_total{service="dense-test",method="/dense.Service/Call",rpc_type="unary",code="OK"} 1`) {
		t.Fatalf("expected grpc counter metric, got %q", body)
	}
	if !strings.Contains(body, `densecloud_grpc_request_duration_seconds_count{service="dense-test",method="/dense.Service/Call",rpc_type="unary"} 1`) {
		t.Fatalf("expected grpc duration metric, got %q", body)
	}
}

func TestHTTPMetricsHandler_DoesNotHoldLockWhileAppendingCollectors(t *testing.T) {
	t.Parallel()

	metrics := NewHTTPMetrics(HTTPMetricsConfig{ServiceName: "dense-test"})
	metrics.collectors = []PrometheusCollector{reentrantCollector{metrics: metrics}}

	done := make(chan string, 1)
	go func() {
		done <- metrics.renderPrometheus()
	}()

	select {
	case body := <-done:
		if !strings.Contains(body, "reentrant_collector 1") {
			t.Fatalf("expected collector output, got %q", body)
		}
	case <-time.After(time.Second):
		t.Fatal("metrics rendering deadlocked while a collector recorded a metric")
	}
}

func captureHTTPMetricsBody(metrics *HTTPMetrics) string {
	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return rec.Body.String()
}

type metricsCapabilityRecorder struct {
	*httptest.ResponseRecorder
	flushed  bool
	hijacked bool
	pushed   bool
	readFrom bool
}

type reentrantCollector struct {
	metrics *HTTPMetrics
}

func (c reentrantCollector) AppendPrometheus(builder *strings.Builder) {
	c.metrics.observe(http.MethodGet, "/collector", http.StatusOK, 0)
	builder.WriteString("reentrant_collector 1\n")
}

func (r *metricsCapabilityRecorder) Flush() {
	r.flushed = true
}

func (r *metricsCapabilityRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	r.hijacked = true
	serverConn, clientConn := net.Pipe()
	_ = serverConn.Close()
	return clientConn, bufio.NewReadWriter(bufio.NewReader(strings.NewReader("")), bufio.NewWriter(io.Discard)), nil
}

func (r *metricsCapabilityRecorder) Push(string, *http.PushOptions) error {
	r.pushed = true
	return nil
}

func (r *metricsCapabilityRecorder) ReadFrom(src io.Reader) (int64, error) {
	r.readFrom = true
	return io.Copy(r.ResponseRecorder, src)
}
