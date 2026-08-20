package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DenseAI/DenseCloud/go/telemetry"
)

func TestHealthRegistryLifecycle(t *testing.T) {
	t.Parallel()

	registry := NewHealthRegistry()
	registry.RegisterReadiness("db", func(context.Context) error { return nil })
	registry.RegisterStartup("warmup", func(context.Context) error { return nil })

	readyReq := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	readyRec := httptest.NewRecorder()
	registry.phaseHandler("ready").ServeHTTP(readyRec, readyReq)
	if readyRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected readiness to fail before startup, got %d", readyRec.Code)
	}

	registry.MarkStarted()

	startupRec := httptest.NewRecorder()
	registry.phaseHandler("startup").ServeHTTP(startupRec, readyReq)
	if startupRec.Code != http.StatusOK {
		t.Fatalf("expected startup to succeed after MarkStarted, got %d", startupRec.Code)
	}

	registry.MarkShuttingDown()
	readyRec = httptest.NewRecorder()
	registry.phaseHandler("ready").ServeHTTP(readyRec, readyReq)
	if readyRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected readiness to fail during shutdown, got %d", readyRec.Code)
	}
}

func TestNewHTTPRuntime_WiresHealthMetricsAndExtensions(t *testing.T) {
	replaceRuntimeExtensionsForTest(t)

	started := false
	stopped := false
	name := "runtime-test-ext-" + time.Now().Format("150405.000000")

	RegisterRuntimeExtension(runtimeTestExtension{
		name: name,
		startup: func(context.Context) error {
			started = true
			return nil
		},
		shutdown: func(context.Context) error {
			stopped = true
			return nil
		},
		registerRoutes: func(rootMux, apiMux *http.ServeMux) {
			rootMux.HandleFunc("/ext", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			})
			apiMux.HandleFunc("/hello", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"ok":true}`))
			})
		},
	})

	runtime, err := NewHTTPRuntime(HTTPRuntimeConfig{ServiceName: "dense-test"})
	if err != nil {
		t.Fatalf("NewHTTPRuntime() error = %v", err)
	}

	if err := runtime.Startup(context.Background()); err != nil {
		t.Fatalf("Startup() error = %v", err)
	}
	if !started {
		t.Fatalf("expected extension startup hook to run")
	}

	rootReq := httptest.NewRequest(http.MethodGet, "/ext", nil)
	rootRec := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(rootRec, rootReq)
	if rootRec.Code != http.StatusNoContent {
		t.Fatalf("expected extension route to be registered, got %d", rootRec.Code)
	}

	apiReq := httptest.NewRequest(http.MethodGet, "/v1/hello", nil)
	apiRec := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(apiRec, apiReq)
	if apiRec.Code != http.StatusOK {
		t.Fatalf("expected api route to be mounted, got %d", apiRec.Code)
	}

	metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsRec := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(metricsRec, metricsReq)
	if metricsRec.Code != http.StatusOK {
		t.Fatalf("expected metrics route to be registered, got %d", metricsRec.Code)
	}
	if !strings.Contains(metricsRec.Body.String(), "densecloud_http_requests_total") {
		t.Fatalf("expected prometheus metrics output, got %q", metricsRec.Body.String())
	}

	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if !stopped {
		t.Fatalf("expected extension shutdown hook to run")
	}

	readyReq := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	readyRec := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(readyRec, readyReq)
	if readyRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected readiness to fail after shutdown, got %d", readyRec.Code)
	}
}

func TestHTTPRuntime_RecordsRecoveredPanicMetricsWithDefaultMiddlewareOrder(t *testing.T) {
	t.Parallel()

	apiMux := http.NewServeMux()
	apiMux.HandleFunc("/panic", func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})

	runtime, err := NewHTTPRuntime(HTTPRuntimeConfig{
		ServiceName:                 "dense-test",
		APIMux:                      apiMux,
		MiddlewarePreset:            &HTTPMiddlewarePresetConfig{},
		DisableRegisteredExtensions: true,
	})
	if err != nil {
		t.Fatalf("NewHTTPRuntime() error = %v", err)
	}

	rec := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/panic", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected recovered panic status 500, got %d", rec.Code)
	}
	if requestID := rec.Header().Get("X-Request-ID"); requestID == "" {
		t.Fatalf("expected runtime middleware preset to set a request ID")
	}

	metricsRec := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metricsRec.Body.String()
	if !strings.Contains(body, `densecloud_http_requests_total{service="dense-test",method="GET",path="/v1/*",status_class="5xx"} 1`) {
		t.Fatalf("expected panic request metric, got %q", body)
	}
	if !strings.Contains(body, `densecloud_http_request_duration_seconds_count{service="dense-test",method="GET",path="/v1/*"} 1`) {
		t.Fatalf("expected panic duration metric, got %q", body)
	}
}

func TestHTTPRuntime_CustomMetricsPathLabelerCanPreserveRouteDetail(t *testing.T) {
	t.Parallel()

	apiMux := http.NewServeMux()
	apiMux.HandleFunc("/panic", func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})

	runtime, err := NewHTTPRuntime(HTTPRuntimeConfig{
		ServiceName:                 "dense-test",
		APIMux:                      apiMux,
		RootMiddleware:              DefaultHTTPMiddleware(HTTPMiddlewarePresetConfig{}),
		MetricsPathLabeler:          func(r *http.Request) string { return r.URL.Path },
		DisableRegisteredExtensions: true,
	})
	if err != nil {
		t.Fatalf("NewHTTPRuntime() error = %v", err)
	}

	rec := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/panic", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected recovered panic status 500, got %d", rec.Code)
	}

	metricsRec := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metricsRec.Body.String()
	if !strings.Contains(body, `densecloud_http_requests_total{service="dense-test",method="GET",path="/v1/panic",status_class="5xx"} 1`) {
		t.Fatalf("expected custom path labeler to preserve route detail, got %q", body)
	}
}

func TestHTTPRuntime_DefaultMetricsPathLabelerBoundsAPICardinality(t *testing.T) {
	t.Parallel()

	apiMux := http.NewServeMux()
	apiMux.HandleFunc("/models/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})

	runtime, err := NewHTTPRuntime(HTTPRuntimeConfig{
		ServiceName:                 "dense-test",
		APIMux:                      apiMux,
		DisableRegisteredExtensions: true,
	})
	if err != nil {
		t.Fatalf("NewHTTPRuntime() error = %v", err)
	}

	for _, path := range []string{"/v1/models/alpha", "/v1/models/beta"} {
		rec := httptest.NewRecorder()
		runtime.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
		if rec.Code != http.StatusAccepted {
			t.Fatalf("request %s status = %d, want %d", path, rec.Code, http.StatusAccepted)
		}
	}

	metricsRec := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metricsRec.Body.String()
	if !strings.Contains(body, `densecloud_http_requests_total{service="dense-test",method="POST",path="/v1/*",status_class="2xx"} 2`) {
		t.Fatalf("expected API requests to collapse to bounded label, got %q", body)
	}
	if strings.Contains(body, `path="/v1/models/alpha"`) || strings.Contains(body, `path="/v1/models/beta"`) {
		t.Fatalf("did not expect raw API paths in default runtime metrics, got %q", body)
	}
}

func TestHTTPRuntime_DefaultMetricsIncludesAdditionalCollectors(t *testing.T) {
	t.Parallel()

	grpcMetrics := telemetry.NewGRPCMetrics(telemetry.GRPCMetricsConfig{ServiceName: "dense-test"})
	grpcMetrics.BeginRPC()
	grpcMetrics.ObserveRPC("/dense.Test/Check", "unary", "OK", 0.01)

	runtime, err := NewHTTPRuntime(HTTPRuntimeConfig{
		ServiceName:                 "dense-test",
		MetricsCollectors:           []telemetry.PrometheusCollector{grpcMetrics},
		DisableRegisteredExtensions: true,
	})
	if err != nil {
		t.Fatalf("NewHTTPRuntime() error = %v", err)
	}

	rec := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected metrics status 200, got %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, `densecloud_grpc_requests_total{service="dense-test",method="/dense.Test/Check",rpc_type="unary",code="OK"} 1`) {
		t.Fatalf("expected gRPC collector on default metrics endpoint, got %q", body)
	}
}

func TestHTTPRuntime_RecordsRootMiddlewareEarlyRejections(t *testing.T) {
	t.Parallel()

	runtime, err := NewHTTPRuntime(HTTPRuntimeConfig{
		ServiceName:      "dense-test",
		MiddlewarePreset: &HTTPMiddlewarePresetConfig{},
		RootMiddleware: []func(http.Handler) http.Handler{
			func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Error(w, "blocked", http.StatusUnauthorized)
				})
			},
		},
		DisableRegisteredExtensions: true,
	})
	if err != nil {
		t.Fatalf("NewHTTPRuntime() error = %v", err)
	}
	if err := runtime.Startup(context.Background()); err != nil {
		t.Fatalf("runtime.Startup() error = %v", err)
	}

	for _, path := range []string{"/health", "/health/live", "/health/ready", "/health/startup", "/metrics"} {
		rec := httptest.NewRecorder()
		runtime.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected DenseCloud-owned route %s to bypass root middleware, got %d", path, rec.Code)
		}
		if requestID := rec.Header().Get("X-Request-ID"); requestID == "" {
			t.Fatalf("expected DenseCloud middleware preset on system route %s", path)
		}
	}

	rec := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/blocked/123", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected middleware rejection status 401, got %d", rec.Code)
	}

	metricsRec := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(metricsRec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metricsRec.Body.String()
	if !strings.Contains(body, `densecloud_http_requests_total{service="dense-test",method="GET",path="/v1/*",status_class="4xx"} 1`) {
		t.Fatalf("expected early middleware rejection metric, got %q", body)
	}
}

func TestHTTPRuntime_AppliesSystemMiddlewareWithoutUsingRootMiddleware(t *testing.T) {
	t.Parallel()

	runtime, err := NewHTTPRuntime(HTTPRuntimeConfig{
		ServiceName: "dense-test",
		RootMiddleware: []func(http.Handler) http.Handler{
			func(http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					http.Error(w, "root blocked", http.StatusUnauthorized)
				})
			},
		},
		SystemMiddleware: []func(http.Handler) http.Handler{
			func(http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					http.Error(w, "system blocked", http.StatusForbidden)
				})
			},
		},
		DisableRegisteredExtensions: true,
	})
	if err != nil {
		t.Fatalf("NewHTTPRuntime() error = %v", err)
	}

	rec := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected system middleware to guard metrics, got %d", rec.Code)
	}
}

func TestHealthRegistrySummaryFailsWhenLivenessFails(t *testing.T) {
	t.Parallel()

	registry := NewHealthRegistry()
	registry.MarkStarted()
	registry.RegisterLiveness("runtime", func(context.Context) error {
		return errors.New("unhealthy")
	})

	rec := httptest.NewRecorder()
	registry.summaryHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected liveness failure to make health summary unavailable, got %d", rec.Code)
	}
}

func TestHTTPRuntime_ContentTypeUsesCustomAPIBasePath(t *testing.T) {
	t.Parallel()

	rootMux := http.NewServeMux()
	rootMux.HandleFunc("/root", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("/hello", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	runtime, err := NewHTTPRuntime(HTTPRuntimeConfig{
		RootMux:                     rootMux,
		APIMux:                      apiMux,
		APIBasePath:                 "/api",
		DisableHealthRoutes:         true,
		DisableMetricsRoute:         true,
		DisableRegisteredExtensions: true,
	})
	if err != nil {
		t.Fatalf("NewHTTPRuntime() error = %v", err)
	}

	apiRec := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(apiRec, httptest.NewRequest(http.MethodGet, "/api/hello", nil))
	if got := apiRec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected custom API path content type, got %q", got)
	}

	rootRec := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(rootRec, httptest.NewRequest(http.MethodGet, "/root", nil))
	if got := rootRec.Header().Get("Content-Type"); got != "" {
		t.Fatalf("expected non-API path to leave content type unset, got %q", got)
	}
}

func TestNewHTTPRuntimeRejectsInvalidRoutePrefixes(t *testing.T) {
	t.Parallel()

	if _, err := NewHTTPRuntime(HTTPRuntimeConfig{
		APIBasePath:                 "/api//v1",
		DisableRegisteredExtensions: true,
	}); err == nil || !strings.Contains(err.Error(), "APIBasePath") {
		t.Fatalf("expected invalid APIBasePath error, got %v", err)
	}

	if _, err := NewHTTPRuntime(HTTPRuntimeConfig{
		MetricsPath:                 "/internal//metrics",
		DisableRegisteredExtensions: true,
	}); err == nil || !strings.Contains(err.Error(), "MetricsPath") {
		t.Fatalf("expected invalid MetricsPath error, got %v", err)
	}
}

func TestHTTPRuntimeStartupPropagatesErrors(t *testing.T) {
	t.Parallel()

	runtime, err := NewHTTPRuntime(HTTPRuntimeConfig{
		StartupHooks: []StartupHook{
			func(context.Context) error { return errors.New("boom") },
		},
	})
	if err != nil {
		t.Fatalf("NewHTTPRuntime() error = %v", err)
	}

	if err := runtime.Startup(context.Background()); err == nil || err.Error() != "boom" {
		t.Fatalf("Startup() error = %v, want boom", err)
	}
}

func TestHTTPRuntimeStartupFailureRollsBackShutdownHooks(t *testing.T) {
	t.Parallel()

	var order []string
	runtime, err := NewHTTPRuntime(HTTPRuntimeConfig{
		DisableRegisteredExtensions: true,
		StartupHooks: []StartupHook{
			func(context.Context) error {
				order = append(order, "startup-ok")
				return nil
			},
			func(context.Context) error {
				order = append(order, "startup-fail")
				return errors.New("boom")
			},
		},
		ShutdownHooks: []ShutdownHook{
			func(context.Context) error {
				order = append(order, "shutdown-a")
				return nil
			},
			func(context.Context) error {
				order = append(order, "shutdown-b")
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("NewHTTPRuntime() error = %v", err)
	}

	if err := runtime.Startup(context.Background()); err == nil || err.Error() != "boom" {
		t.Fatalf("Startup() error = %v, want boom", err)
	}
	want := strings.Join([]string{"startup-ok", "startup-fail", "shutdown-a", "shutdown-b"}, ",")
	if got := strings.Join(order, ","); got != want {
		t.Fatalf("startup rollback order = %s, want %s", got, want)
	}

	readyRec := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(readyRec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if readyRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected readiness to fail-close after startup rollback, got %d", readyRec.Code)
	}
}

func TestHTTPRuntimeBeginShutdownFailsReadinessBeforeCleanup(t *testing.T) {
	t.Parallel()

	shutdownCalls := 0
	runtime, err := NewHTTPRuntime(HTTPRuntimeConfig{
		DisableMetricsRoute:         true,
		DisableRegisteredExtensions: true,
		ShutdownHooks: []ShutdownHook{
			func(context.Context) error {
				shutdownCalls++
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("NewHTTPRuntime() error = %v", err)
	}
	if err := runtime.Startup(context.Background()); err != nil {
		t.Fatalf("Startup() error = %v", err)
	}

	if err := runtime.BeginShutdown(context.Background()); err != nil {
		t.Fatalf("BeginShutdown() error = %v", err)
	}
	if shutdownCalls != 0 {
		t.Fatalf("cleanup ran before transport drain")
	}
	readyRec := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(readyRec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if readyRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want %d", readyRec.Code, http.StatusServiceUnavailable)
	}

	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("second Shutdown() error = %v", err)
	}
	if shutdownCalls != 1 {
		t.Fatalf("shutdown hook calls = %d, want 1", shutdownCalls)
	}
}

func TestHTTPRuntimeExtensionStartupFailureRollsBack(t *testing.T) {
	t.Parallel()

	stopped := false
	ext := runtimeTestExtension{
		name: "failing-extension",
		startup: func(context.Context) error {
			return errors.New("extension startup failed")
		},
		shutdown: func(context.Context) error {
			stopped = true
			return nil
		},
	}
	runtime := &HTTPRuntime{
		health:        NewHealthRegistry(),
		startupHooks:  []StartupHook{ext.Startup},
		shutdownHooks: []ShutdownHook{ext.Shutdown},
	}

	if err := runtime.Startup(context.Background()); err == nil || err.Error() != "extension startup failed" {
		t.Fatalf("Startup() error = %v, want extension startup failed", err)
	}
	if !stopped {
		t.Fatalf("expected extension shutdown hook to run during startup rollback")
	}
}

func TestNewHTTPRuntime_AllowsRootAPIBasePath(t *testing.T) {
	t.Parallel()

	apiMux := http.NewServeMux()
	apiMux.HandleFunc("/hello", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	runtime, err := NewHTTPRuntime(HTTPRuntimeConfig{
		ServiceName:                 "dense-test",
		APIMux:                      apiMux,
		APIBasePath:                 "/",
		DisableHealthRoutes:         true,
		DisableMetricsRoute:         true,
		DisableRegisteredExtensions: true,
	})
	if err != nil {
		t.Fatalf("NewHTTPRuntime() error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	runtime.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected root-mounted API route to be served, got %d", rec.Code)
	}
}

func TestHealthRegistryRegisterDependencyAcrossPhases(t *testing.T) {
	t.Parallel()

	registry := NewHealthRegistry()
	registry.RegisterDependency("redis", healthDependencyFunc(func(context.Context) error { return nil }), HealthPhaseStartup, HealthPhaseReady)

	registry.MarkStarted()

	startupRec := httptest.NewRecorder()
	registry.phaseHandler("startup").ServeHTTP(startupRec, httptest.NewRequest(http.MethodGet, "/health/startup", nil))
	if startupRec.Code != http.StatusOK {
		t.Fatalf("expected startup dependency to pass, got %d", startupRec.Code)
	}

	readyRec := httptest.NewRecorder()
	registry.phaseHandler("ready").ServeHTTP(readyRec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if readyRec.Code != http.StatusOK {
		t.Fatalf("expected readiness dependency to pass, got %d", readyRec.Code)
	}
}

func TestHealthRegistryLiveReadyAndDependencyFailure(t *testing.T) {
	t.Parallel()

	registry := NewHealthRegistry()
	registry.RegisterLiveness("process", func(context.Context) error { return nil })
	registry.RegisterReadiness("db", func(context.Context) error { return errors.New("db down") })
	registry.MarkStarted()

	liveRec := httptest.NewRecorder()
	registry.phaseHandler("live").ServeHTTP(liveRec, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if liveRec.Code != http.StatusOK {
		t.Fatalf("expected liveness to remain process-oriented, got %d", liveRec.Code)
	}

	readyRec := httptest.NewRecorder()
	registry.phaseHandler("ready").ServeHTTP(readyRec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if readyRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected readiness dependency failure to fail-close, got %d", readyRec.Code)
	}

	registry.MarkShuttingDown()
	liveRec = httptest.NewRecorder()
	registry.phaseHandler("live").ServeHTTP(liveRec, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if liveRec.Code != http.StatusOK {
		t.Fatalf("expected liveness to remain OK during shutdown, got %d", liveRec.Code)
	}
}

func TestHTTPRuntimeHealthCheckTimeoutFailsClosed(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	defer close(release)
	health := NewHealthRegistry(WithHealthCheckTimeout(time.Second))

	runtime, err := NewHTTPRuntime(HTTPRuntimeConfig{
		Health:                      health,
		HealthCheckTimeout:          20 * time.Millisecond,
		DisableMetricsRoute:         true,
		DisableRegisteredExtensions: true,
	})
	if err != nil {
		t.Fatalf("NewHTTPRuntime() error = %v", err)
	}
	runtime.Health().RegisterReadiness("stuck", func(context.Context) error {
		<-release
		return nil
	})
	if err := runtime.Startup(context.Background()); err != nil {
		t.Fatalf("Startup() error = %v", err)
	}

	started := time.Now()
	rec := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("health timeout took %s, expected a bounded response", elapsed)
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected timed out health check to fail closed, got %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"name":"stuck"`) || !strings.Contains(body, "timed out after 20ms") {
		t.Fatalf("expected named timeout failure in health report, got %q", body)
	}
}

func TestHealthRegistryCoalescesConcurrentLiveAndReadyChecks(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	registry := NewHealthRegistry(WithHealthCheckTimeout(200 * time.Millisecond))
	registry.RegisterCheck("shared", func(context.Context) error {
		calls.Add(1)
		time.Sleep(40 * time.Millisecond)
		return nil
	}, HealthPhaseLive, HealthPhaseReady)
	registry.MarkStarted()

	type response struct {
		code int
		body string
	}
	results := make(chan response, 2)
	var wg sync.WaitGroup
	for _, phase := range []string{"live", "ready"} {
		wg.Add(1)
		go func(phase string) {
			defer wg.Done()
			rec := httptest.NewRecorder()
			registry.phaseHandler(phase).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/"+phase, nil))
			results <- response{code: rec.Code, body: rec.Body.String()}
		}(phase)
	}
	wg.Wait()
	close(results)

	for result := range results {
		if result.code != http.StatusOK {
			t.Fatalf("expected shared healthy check to satisfy both probes, got code=%d body=%q", result.code, result.body)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("expected one shared in-flight execution, got %d", calls.Load())
	}
}

func TestHealthRegistryCoalescesSummaryAndPhaseChecks(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	registry := NewHealthRegistry(WithHealthCheckTimeout(200 * time.Millisecond))
	registry.RegisterReadiness("shared", func(context.Context) error {
		calls.Add(1)
		time.Sleep(40 * time.Millisecond)
		return nil
	})
	registry.MarkStarted()

	type response struct {
		code int
		body string
	}
	results := make(chan response, 2)
	var wg sync.WaitGroup
	startRequest := func(path string, handler http.Handler) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			results <- response{code: rec.Code, body: rec.Body.String()}
		}()
	}

	startRequest("/health", registry.summaryHandler())
	startRequest("/health/ready", registry.phaseHandler("ready"))
	wg.Wait()
	close(results)

	for result := range results {
		if result.code != http.StatusOK {
			t.Fatalf("expected summary and phase probes to share a healthy check, got code=%d body=%q", result.code, result.body)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("expected one shared execution across summary and phase probes, got %d", calls.Load())
	}
}

func TestHealthRegistryTimedOutSharedCheckFailsTruthfully(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	registry := NewHealthRegistry(WithHealthCheckTimeout(20 * time.Millisecond))
	registry.RegisterCheck("shared", func(context.Context) error {
		calls.Add(1)
		time.Sleep(50 * time.Millisecond)
		return nil
	}, HealthPhaseLive, HealthPhaseReady)
	registry.MarkStarted()

	type response struct {
		code int
		body string
	}
	results := make(chan response, 2)
	var wg sync.WaitGroup
	for _, phase := range []string{"live", "ready"} {
		wg.Add(1)
		go func(phase string) {
			defer wg.Done()
			rec := httptest.NewRecorder()
			registry.phaseHandler(phase).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/"+phase, nil))
			results <- response{code: rec.Code, body: rec.Body.String()}
		}(phase)
	}
	wg.Wait()
	close(results)

	for result := range results {
		if result.code != http.StatusServiceUnavailable || !strings.Contains(result.body, "timed out after 20ms") {
			t.Fatalf("expected shared timeout failure, got code=%d body=%q", result.code, result.body)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("expected one shared timed-out execution, got %d", calls.Load())
	}
}

func TestHealthRegistryContainsIgnoringCancellationCheck(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	var calls atomic.Int32
	registry := NewHealthRegistry(WithHealthCheckTimeout(20 * time.Millisecond))
	registry.RegisterReadiness("stuck", func(context.Context) error {
		calls.Add(1)
		<-release
		return nil
	})
	registry.MarkStarted()

	before := runtime.NumGoroutine()
	var wg sync.WaitGroup
	results := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			registry.phaseHandler("ready").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
			if rec.Code != http.StatusServiceUnavailable {
				results <- rec.Body.String()
				return
			}
			results <- rec.Body.String()
		}()
	}
	wg.Wait()
	close(results)

	for body := range results {
		if !strings.Contains(body, "timed out after 20ms") {
			t.Fatalf("expected timeout while the stuck check remained in flight, got %q", body)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("expected one stuck execution to be shared across overlapping probes, got %d", calls.Load())
	}

	close(release)
	time.Sleep(10 * time.Millisecond)
	after := runtime.NumGoroutine()
	if delta := after - before; delta > 4 {
		t.Fatalf("expected bounded goroutine growth, before=%d after=%d", before, after)
	}

	final := httptest.NewRecorder()
	registry.phaseHandler("ready").ServeHTTP(final, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if final.Code != http.StatusOK {
		t.Fatalf("expected readiness to recover after the stuck check returns, got %d body=%q", final.Code, final.Body.String())
	}
	if calls.Load() != 2 {
		t.Fatalf("expected a new execution after the stuck check exited, got %d", calls.Load())
	}
}

func TestHealthRegistryCheckPanicFailsClosed(t *testing.T) {
	t.Parallel()

	registry := NewHealthRegistry()
	registry.RegisterReadiness("panic", func(context.Context) error {
		panic("boom")
	})
	registry.MarkStarted()

	rec := httptest.NewRecorder()
	registry.phaseHandler("ready").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected panicking health check to fail closed, got %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "health check panicked: boom") {
		t.Fatalf("expected panic failure in health report, got %q", body)
	}
}

func TestHealthRegistryDistinguishesRequestCancellation(t *testing.T) {
	t.Parallel()

	registry := NewHealthRegistry(WithHealthCheckTimeout(time.Second))
	registry.RegisterLiveness("blocked", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	report, ok := registry.evaluate(ctx, "live")
	if ok || report.Status != "unavailable" {
		t.Fatalf("expected canceled health evaluation to fail closed, got %#v", report)
	}
	if got := report.Checks[0].Error; got != context.Canceled.Error() {
		t.Fatalf("health error = %q, want %q", got, context.Canceled.Error())
	}
}

type healthDependencyFunc func(context.Context) error

func (fn healthDependencyFunc) HealthCheck(ctx context.Context) error {
	return fn(ctx)
}

type runtimeTestExtension struct {
	name           string
	registerRoutes func(rootMux, apiMux *http.ServeMux)
	startup        func(context.Context) error
	shutdown       func(context.Context) error
}

func (e runtimeTestExtension) Name() string { return e.name }

func (e runtimeTestExtension) APIMiddleware() []func(http.Handler) http.Handler { return nil }

func (e runtimeTestExtension) RegisterRoutes(rootMux, apiMux *http.ServeMux) {
	if e.registerRoutes != nil {
		e.registerRoutes(rootMux, apiMux)
	}
}

func (e runtimeTestExtension) Startup(ctx context.Context) error {
	if e.startup != nil {
		return e.startup(ctx)
	}
	return nil
}

func (e runtimeTestExtension) Shutdown(ctx context.Context) error {
	if e.shutdown != nil {
		return e.shutdown(ctx)
	}
	return nil
}
