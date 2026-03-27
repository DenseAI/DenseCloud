package telemetry

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
