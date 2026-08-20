package telemetry

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var defaultHTTPMetricsBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// HTTPMetricsConfig configures DenseCloud's shared RED metrics.
type HTTPMetricsConfig struct {
	ServiceName string
	Buckets     []float64
	IgnorePaths []string
	PathLabeler func(*http.Request) string
	Collectors  []PrometheusCollector
}

type requestMetricKey struct {
	Method      string
	Path        string
	StatusClass string
}

type durationMetricKey struct {
	Method string
	Path   string
}

type durationMetricValue struct {
	buckets []uint64
	sum     float64
	count   uint64
}

// HTTPMetrics records RED-style HTTP metrics and exposes Prometheus text format.
type HTTPMetrics struct {
	serviceName string
	buckets     []float64
	pathLabeler func(*http.Request) string
	ignored     map[string]struct{}
	collectors  []PrometheusCollector

	inFlight atomic.Int64

	mu        sync.RWMutex
	requests  map[requestMetricKey]uint64
	errors    map[requestMetricKey]uint64
	durations map[durationMetricKey]*durationMetricValue
}

// NewHTTPMetrics creates a shared metrics collector for DenseCloud runtimes.
func NewHTTPMetrics(cfg HTTPMetricsConfig) *HTTPMetrics {
	buckets := cfg.Buckets
	if len(buckets) == 0 {
		buckets = append([]float64(nil), defaultHTTPMetricsBuckets...)
	}

	ignored := make(map[string]struct{}, len(cfg.IgnorePaths))
	for _, path := range cfg.IgnorePaths {
		if path == "" {
			continue
		}
		ignored[path] = struct{}{}
	}

	labeler := cfg.PathLabeler
	if labeler == nil {
		labeler = func(r *http.Request) string {
			if r == nil || r.URL == nil || r.URL.Path == "" {
				return "/"
			}
			return r.URL.Path
		}
	}

	return &HTTPMetrics{
		serviceName: cfg.ServiceName,
		buckets:     buckets,
		pathLabeler: labeler,
		ignored:     ignored,
		collectors:  append([]PrometheusCollector(nil), cfg.Collectors...),
		requests:    make(map[requestMetricKey]uint64),
		errors:      make(map[requestMetricKey]uint64),
		durations:   make(map[durationMetricKey]*durationMetricValue),
	}
}

// Middleware records request counts, errors, latency, and in-flight concurrency.
func (m *HTTPMetrics) Middleware() func(http.Handler) http.Handler {
	if m == nil {
		return func(next http.Handler) http.Handler { return next }
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := m.pathLabeler(r)
			if _, skip := m.ignored[path]; skip {
				next.ServeHTTP(w, r)
				return
			}

			start := time.Now()
			m.inFlight.Add(1)

			wrapped := &metricsResponseWriter{ResponseWriter: w}
			defer func() {
				statusCode := wrapped.StatusCode()
				if rec := recover(); rec != nil {
					if !wrapped.WroteHeader() {
						statusCode = http.StatusInternalServerError
					}
					m.observe(r.Method, path, statusCode, time.Since(start).Seconds())
					m.inFlight.Add(-1)
					panic(rec)
				}
				m.observe(r.Method, path, statusCode, time.Since(start).Seconds())
				m.inFlight.Add(-1)
			}()
			next.ServeHTTP(wrapped, r)
		})
	}
}

// Handler exposes metrics in Prometheus text exposition format.
func (m *HTTPMetrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if m == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = io.WriteString(w, m.renderPrometheus())
	})
}

func (m *HTTPMetrics) observe(method, path string, statusCode int, seconds float64) {
	statusClass := fmt.Sprintf("%dxx", statusCode/100)
	reqKey := requestMetricKey{Method: method, Path: path, StatusClass: statusClass}
	durKey := durationMetricKey{Method: method, Path: path}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.requests[reqKey]++
	if statusCode >= 400 {
		m.errors[reqKey]++
	}

	value := m.durations[durKey]
	if value == nil {
		value = &durationMetricValue{buckets: make([]uint64, len(m.buckets)+1)}
		m.durations[durKey] = value
	}
	idx := len(m.buckets)
	for i, upperBound := range m.buckets {
		if seconds <= upperBound {
			idx = i
			break
		}
	}
	value.buckets[idx]++
	value.sum += seconds
	value.count++
}

func (m *HTTPMetrics) renderPrometheus() string {
	m.mu.RLock()
	requests := cloneRequestMetrics(m.requests)
	errors := cloneRequestMetrics(m.errors)
	durations := cloneDurationMetrics(m.durations)
	buckets := append([]float64(nil), m.buckets...)
	collectors := append([]PrometheusCollector(nil), m.collectors...)
	m.mu.RUnlock()

	var builder strings.Builder

	builder.WriteString("# HELP densecloud_http_in_flight_requests Current number of in-flight HTTP requests.\n")
	builder.WriteString("# TYPE densecloud_http_in_flight_requests gauge\n")
	builder.WriteString("densecloud_http_in_flight_requests")
	builder.WriteString(m.serviceLabel())
	builder.WriteByte(' ')
	builder.WriteString(strconv.FormatInt(m.inFlight.Load(), 10))
	builder.WriteByte('\n')

	builder.WriteString("# HELP densecloud_http_requests_total Total number of HTTP requests completed by the DenseCloud runtime.\n")
	builder.WriteString("# TYPE densecloud_http_requests_total counter\n")
	for _, key := range sortedRequestMetricKeys(requests) {
		builder.WriteString("densecloud_http_requests_total")
		builder.WriteString(m.labels(
			"method", key.Method,
			"path", key.Path,
			"status_class", key.StatusClass,
		))
		builder.WriteByte(' ')
		builder.WriteString(strconv.FormatUint(requests[key], 10))
		builder.WriteByte('\n')
	}

	builder.WriteString("# HELP densecloud_http_request_errors_total Total number of HTTP requests that completed with client/server errors.\n")
	builder.WriteString("# TYPE densecloud_http_request_errors_total counter\n")
	for _, key := range sortedRequestMetricKeys(errors) {
		builder.WriteString("densecloud_http_request_errors_total")
		builder.WriteString(m.labels(
			"method", key.Method,
			"path", key.Path,
			"status_class", key.StatusClass,
		))
		builder.WriteByte(' ')
		builder.WriteString(strconv.FormatUint(errors[key], 10))
		builder.WriteByte('\n')
	}

	builder.WriteString("# HELP densecloud_http_request_duration_seconds HTTP request latency in seconds.\n")
	builder.WriteString("# TYPE densecloud_http_request_duration_seconds histogram\n")
	for _, key := range sortedDurationMetricKeys(durations) {
		value := durations[key]
		var cumulative uint64
		for i, upperBound := range buckets {
			cumulative += value.buckets[i]
			builder.WriteString("densecloud_http_request_duration_seconds_bucket")
			builder.WriteString(m.labels(
				"method", key.Method,
				"path", key.Path,
				"le", formatBucket(upperBound),
			))
			builder.WriteByte(' ')
			builder.WriteString(strconv.FormatUint(cumulative, 10))
			builder.WriteByte('\n')
		}
		cumulative += value.buckets[len(buckets)]
		builder.WriteString("densecloud_http_request_duration_seconds_bucket")
		builder.WriteString(m.labels(
			"method", key.Method,
			"path", key.Path,
			"le", "+Inf",
		))
		builder.WriteByte(' ')
		builder.WriteString(strconv.FormatUint(cumulative, 10))
		builder.WriteByte('\n')

		builder.WriteString("densecloud_http_request_duration_seconds_sum")
		builder.WriteString(m.labels(
			"method", key.Method,
			"path", key.Path,
		))
		builder.WriteByte(' ')
		builder.WriteString(strconv.FormatFloat(value.sum, 'f', -1, 64))
		builder.WriteByte('\n')

		builder.WriteString("densecloud_http_request_duration_seconds_count")
		builder.WriteString(m.labels(
			"method", key.Method,
			"path", key.Path,
		))
		builder.WriteByte(' ')
		builder.WriteString(strconv.FormatUint(value.count, 10))
		builder.WriteByte('\n')
	}

	for _, collector := range collectors {
		if collector == nil {
			continue
		}
		if builder.Len() > 0 && builder.String()[builder.Len()-1] != '\n' {
			builder.WriteByte('\n')
		}
		collector.AppendPrometheus(&builder)
	}

	return builder.String()
}

func cloneRequestMetrics(source map[requestMetricKey]uint64) map[requestMetricKey]uint64 {
	cloned := make(map[requestMetricKey]uint64, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func cloneDurationMetrics(source map[durationMetricKey]*durationMetricValue) map[durationMetricKey]*durationMetricValue {
	cloned := make(map[durationMetricKey]*durationMetricValue, len(source))
	for key, value := range source {
		if value == nil {
			continue
		}
		cloned[key] = &durationMetricValue{
			buckets: append([]uint64(nil), value.buckets...),
			sum:     value.sum,
			count:   value.count,
		}
	}
	return cloned
}

func (m *HTTPMetrics) serviceLabel() string {
	if m.serviceName == "" {
		return ""
	}
	return `{service="` + escapePrometheusLabelValue(m.serviceName) + `"}`
}

func (m *HTTPMetrics) labels(kv ...string) string {
	pairs := make([]string, 0, len(kv)/2+1)
	if m.serviceName != "" {
		pairs = append(pairs, `service="`+escapePrometheusLabelValue(m.serviceName)+`"`)
	}
	for i := 0; i+1 < len(kv); i += 2 {
		pairs = append(pairs, kv[i]+`="`+escapePrometheusLabelValue(kv[i+1])+`"`)
	}
	return "{" + strings.Join(pairs, ",") + "}"
}

func sortedRequestMetricKeys(values map[requestMetricKey]uint64) []requestMetricKey {
	keys := make([]requestMetricKey, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Method != keys[j].Method {
			return keys[i].Method < keys[j].Method
		}
		if keys[i].Path != keys[j].Path {
			return keys[i].Path < keys[j].Path
		}
		return keys[i].StatusClass < keys[j].StatusClass
	})
	return keys
}

func sortedDurationMetricKeys(values map[durationMetricKey]*durationMetricValue) []durationMetricKey {
	keys := make([]durationMetricKey, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Method != keys[j].Method {
			return keys[i].Method < keys[j].Method
		}
		return keys[i].Path < keys[j].Path
	})
	return keys
}

func formatBucket(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func escapePrometheusLabelValue(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`)
	return replacer.Replace(value)
}

type metricsResponseWriter struct {
	http.ResponseWriter
	statusCode int
	written    int64
	wrote      bool
}

func (rw *metricsResponseWriter) WriteHeader(code int) {
	if rw.wrote {
		return
	}
	rw.wrote = true
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *metricsResponseWriter) Write(b []byte) (int, error) {
	if !rw.wrote {
		rw.WriteHeader(http.StatusOK)
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.written += int64(n)
	return n, err
}

func (rw *metricsResponseWriter) StatusCode() int {
	if rw.wrote {
		return rw.statusCode
	}
	return http.StatusOK
}

func (rw *metricsResponseWriter) WroteHeader() bool {
	return rw.wrote
}

func (rw *metricsResponseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (rw *metricsResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := rw.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("hijacker not supported")
	}
	return h.Hijack()
}

func (rw *metricsResponseWriter) Push(target string, opts *http.PushOptions) error {
	if p, ok := rw.ResponseWriter.(http.Pusher); ok {
		return p.Push(target, opts)
	}
	return http.ErrNotSupported
}

func (rw *metricsResponseWriter) ReadFrom(src io.Reader) (int64, error) {
	if !rw.wrote {
		rw.WriteHeader(http.StatusOK)
	}
	if rf, ok := rw.ResponseWriter.(io.ReaderFrom); ok {
		n, err := rf.ReadFrom(src)
		rw.written += n
		return n, err
	}
	n, err := io.Copy(rw.ResponseWriter, src)
	rw.written += n
	return n, err
}
