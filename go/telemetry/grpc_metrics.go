package telemetry

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// PrometheusCollector appends metrics in Prometheus text exposition format.
type PrometheusCollector interface {
	AppendPrometheus(*strings.Builder)
}

// GRPCMetricsConfig configures DenseCloud's shared gRPC metrics collector.
type GRPCMetricsConfig struct {
	ServiceName string
	Buckets     []float64
}

type grpcRequestMetricKey struct {
	Method  string
	RPCType string
	Code    string
}

type grpcDurationMetricKey struct {
	Method  string
	RPCType string
}

// GRPCMetrics records RED-style gRPC metrics and can be appended to `/metrics`.
type GRPCMetrics struct {
	serviceName string
	buckets     []float64

	inFlight atomic.Int64

	mu        sync.RWMutex
	requests  map[grpcRequestMetricKey]uint64
	errors    map[grpcRequestMetricKey]uint64
	durations map[grpcDurationMetricKey]*durationMetricValue
}

// NewGRPCMetrics creates a shared metrics collector for DenseCloud gRPC runtimes.
func NewGRPCMetrics(cfg GRPCMetricsConfig) *GRPCMetrics {
	buckets := cfg.Buckets
	if len(buckets) == 0 {
		buckets = append([]float64(nil), defaultHTTPMetricsBuckets...)
	}

	return &GRPCMetrics{
		serviceName: cfg.ServiceName,
		buckets:     buckets,
		requests:    make(map[grpcRequestMetricKey]uint64),
		errors:      make(map[grpcRequestMetricKey]uint64),
		durations:   make(map[grpcDurationMetricKey]*durationMetricValue),
	}
}

// BeginRPC marks the beginning of an in-flight gRPC request.
func (m *GRPCMetrics) BeginRPC() {
	if m == nil {
		return
	}
	m.inFlight.Add(1)
}

// ObserveRPC records a completed gRPC request.
func (m *GRPCMetrics) ObserveRPC(method, rpcType, code string, seconds float64) {
	if m == nil {
		return
	}

	m.inFlight.Add(-1)

	reqKey := grpcRequestMetricKey{Method: method, RPCType: rpcType, Code: code}
	durKey := grpcDurationMetricKey{Method: method, RPCType: rpcType}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.requests[reqKey]++
	if code != "OK" {
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

// AppendPrometheus emits gRPC metrics in Prometheus text exposition format.
func (m *GRPCMetrics) AppendPrometheus(builder *strings.Builder) {
	if m == nil || builder == nil {
		return
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	builder.WriteString("# HELP densecloud_grpc_in_flight_requests Current number of in-flight gRPC requests.\n")
	builder.WriteString("# TYPE densecloud_grpc_in_flight_requests gauge\n")
	builder.WriteString("densecloud_grpc_in_flight_requests")
	builder.WriteString(m.serviceLabel())
	builder.WriteByte(' ')
	builder.WriteString(strconv.FormatInt(m.inFlight.Load(), 10))
	builder.WriteByte('\n')

	builder.WriteString("# HELP densecloud_grpc_requests_total Total number of gRPC requests completed by the DenseCloud runtime.\n")
	builder.WriteString("# TYPE densecloud_grpc_requests_total counter\n")
	for _, key := range sortedGRPCRequestMetricKeys(m.requests) {
		builder.WriteString("densecloud_grpc_requests_total")
		builder.WriteString(m.labels(
			"method", key.Method,
			"rpc_type", key.RPCType,
			"code", key.Code,
		))
		builder.WriteByte(' ')
		builder.WriteString(strconv.FormatUint(m.requests[key], 10))
		builder.WriteByte('\n')
	}

	builder.WriteString("# HELP densecloud_grpc_request_errors_total Total number of gRPC requests that completed with non-OK status.\n")
	builder.WriteString("# TYPE densecloud_grpc_request_errors_total counter\n")
	for _, key := range sortedGRPCRequestMetricKeys(m.errors) {
		builder.WriteString("densecloud_grpc_request_errors_total")
		builder.WriteString(m.labels(
			"method", key.Method,
			"rpc_type", key.RPCType,
			"code", key.Code,
		))
		builder.WriteByte(' ')
		builder.WriteString(strconv.FormatUint(m.errors[key], 10))
		builder.WriteByte('\n')
	}

	builder.WriteString("# HELP densecloud_grpc_request_duration_seconds gRPC request latency in seconds.\n")
	builder.WriteString("# TYPE densecloud_grpc_request_duration_seconds histogram\n")
	for _, key := range sortedGRPCDurationMetricKeys(m.durations) {
		value := m.durations[key]
		var cumulative uint64
		for i, upperBound := range m.buckets {
			cumulative += value.buckets[i]
			builder.WriteString("densecloud_grpc_request_duration_seconds_bucket")
			builder.WriteString(m.labels(
				"method", key.Method,
				"rpc_type", key.RPCType,
				"le", formatBucket(upperBound),
			))
			builder.WriteByte(' ')
			builder.WriteString(strconv.FormatUint(cumulative, 10))
			builder.WriteByte('\n')
		}
		cumulative += value.buckets[len(m.buckets)]
		builder.WriteString("densecloud_grpc_request_duration_seconds_bucket")
		builder.WriteString(m.labels(
			"method", key.Method,
			"rpc_type", key.RPCType,
			"le", "+Inf",
		))
		builder.WriteByte(' ')
		builder.WriteString(strconv.FormatUint(cumulative, 10))
		builder.WriteByte('\n')

		builder.WriteString("densecloud_grpc_request_duration_seconds_sum")
		builder.WriteString(m.labels(
			"method", key.Method,
			"rpc_type", key.RPCType,
		))
		builder.WriteByte(' ')
		builder.WriteString(strconv.FormatFloat(value.sum, 'f', -1, 64))
		builder.WriteByte('\n')

		builder.WriteString("densecloud_grpc_request_duration_seconds_count")
		builder.WriteString(m.labels(
			"method", key.Method,
			"rpc_type", key.RPCType,
		))
		builder.WriteByte(' ')
		builder.WriteString(strconv.FormatUint(value.count, 10))
		builder.WriteByte('\n')
	}
}

func (m *GRPCMetrics) serviceLabel() string {
	if m.serviceName == "" {
		return ""
	}
	return `{service="` + escapePrometheusLabelValue(m.serviceName) + `"}`
}

func (m *GRPCMetrics) labels(kv ...string) string {
	pairs := make([]string, 0, len(kv)/2+1)
	if m.serviceName != "" {
		pairs = append(pairs, `service="`+escapePrometheusLabelValue(m.serviceName)+`"`)
	}
	for i := 0; i+1 < len(kv); i += 2 {
		pairs = append(pairs, kv[i]+`="`+escapePrometheusLabelValue(kv[i+1])+`"`)
	}
	return "{" + strings.Join(pairs, ",") + "}"
}

func sortedGRPCRequestMetricKeys(values map[grpcRequestMetricKey]uint64) []grpcRequestMetricKey {
	keys := make([]grpcRequestMetricKey, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Method != keys[j].Method {
			return keys[i].Method < keys[j].Method
		}
		if keys[i].RPCType != keys[j].RPCType {
			return keys[i].RPCType < keys[j].RPCType
		}
		return keys[i].Code < keys[j].Code
	})
	return keys
}

func sortedGRPCDurationMetricKeys(values map[grpcDurationMetricKey]*durationMetricValue) []grpcDurationMetricKey {
	keys := make([]grpcDurationMetricKey, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Method != keys[j].Method {
			return keys[i].Method < keys[j].Method
		}
		return keys[i].RPCType < keys[j].RPCType
	})
	return keys
}
