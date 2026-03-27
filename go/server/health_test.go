package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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
	t.Parallel()

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
