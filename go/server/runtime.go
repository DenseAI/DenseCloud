package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	densemiddleware "github.com/DenseAI/DenseCloud/go/middleware"
	"github.com/DenseAI/DenseCloud/go/telemetry"
)

// HTTPRuntimeConfig defines DenseCloud's shared HTTP chassis assembly.
type HTTPRuntimeConfig struct {
	ServiceName                 string
	RootMux                     *http.ServeMux
	APIMux                      *http.ServeMux
	APIBasePath                 string
	RootMiddleware              []func(http.Handler) http.Handler
	APIMiddleware               []func(http.Handler) http.Handler
	Health                      *HealthRegistry
	Metrics                     *telemetry.HTTPMetrics
	MetricsPath                 string
	DisableHealthRoutes         bool
	DisableMetricsRoute         bool
	DisableRegisteredExtensions bool
	StartupHooks                []StartupHook
	ShutdownHooks               []ShutdownHook
}

// HTTPRuntime wires shared health, metrics, routes, and extension lifecycle.
type HTTPRuntime struct {
	handler       http.Handler
	rootMux       *http.ServeMux
	apiMux        *http.ServeMux
	health        *HealthRegistry
	metrics       *telemetry.HTTPMetrics
	extensions    []RuntimeExtension
	startupHooks  []StartupHook
	shutdownHooks []ShutdownHook
}

// NewHTTPRuntime creates a DenseCloud-owned shared HTTP runtime assembly.
func NewHTTPRuntime(cfg HTTPRuntimeConfig) (*HTTPRuntime, error) {
	if cfg.ServiceName == "" {
		cfg.ServiceName = "dense-service"
	}

	rootMux := cfg.RootMux
	if rootMux == nil {
		rootMux = http.NewServeMux()
	}
	apiMux := cfg.APIMux
	if apiMux == nil {
		apiMux = http.NewServeMux()
	}

	apiBasePath := cfg.APIBasePath
	if apiBasePath == "" {
		apiBasePath = "/v1"
	}
	apiBasePath = normalizeRoutePrefix(apiBasePath)

	health := cfg.Health
	if health == nil {
		health = NewHealthRegistry()
	}
	registerHealth := !cfg.DisableHealthRoutes
	if registerHealth {
		health.RegisterHandlers(rootMux)
	}

	metricsPath := cfg.MetricsPath
	if metricsPath == "" {
		metricsPath = "/metrics"
	}
	registerMetrics := !cfg.DisableMetricsRoute

	metrics := cfg.Metrics
	if metrics == nil && registerMetrics {
		metrics = telemetry.NewHTTPMetrics(telemetry.HTTPMetricsConfig{
			ServiceName: cfg.ServiceName,
			IgnorePaths: []string{
				"/health",
				"/health/live",
				"/health/ready",
				"/health/startup",
				metricsPath,
			},
		})
	}
	if registerMetrics && metrics != nil {
		rootMux.Handle(metricsPath, metrics.Handler())
	}

	runtime := &HTTPRuntime{
		rootMux:       rootMux,
		apiMux:        apiMux,
		health:        health,
		metrics:       metrics,
		startupHooks:  append([]StartupHook(nil), cfg.StartupHooks...),
		shutdownHooks: append([]ShutdownHook(nil), cfg.ShutdownHooks...),
	}

	useExtensions := !cfg.DisableRegisteredExtensions
	if useExtensions {
		runtime.extensions = RuntimeExtensions()
	}

	apiMiddleware := append([]func(http.Handler) http.Handler(nil), cfg.APIMiddleware...)
	for _, ext := range runtime.extensions {
		ext.RegisterRoutes(rootMux, apiMux)
		apiMiddleware = append(apiMiddleware, ext.APIMiddleware()...)
		runtime.startupHooks = append(runtime.startupHooks, ext.Startup)
		runtime.shutdownHooks = append(runtime.shutdownHooks, ext.Shutdown)
	}

	apiHandler := densemiddleware.Chain(apiMiddleware...)(apiMux)
	mountAPISubrouter(rootMux, apiBasePath, apiHandler)

	rootHandler := http.Handler(rootMux)
	if metrics != nil {
		rootHandler = metrics.Middleware()(rootHandler)
	}
	rootHandler = densemiddleware.Chain(cfg.RootMiddleware...)(rootHandler)
	runtime.handler = rootHandler

	return runtime, nil
}

// Handler returns the fully assembled root handler.
func (r *HTTPRuntime) Handler() http.Handler {
	return r.handler
}

// RootMux returns the root mux owned by the runtime.
func (r *HTTPRuntime) RootMux() *http.ServeMux {
	return r.rootMux
}

// APIMux returns the /v1 API sub-mux owned by the runtime.
func (r *HTTPRuntime) APIMux() *http.ServeMux {
	return r.apiMux
}

// Health returns the runtime's shared health registry.
func (r *HTTPRuntime) Health() *HealthRegistry {
	return r.health
}

// Metrics returns the runtime's shared HTTP metrics collector.
func (r *HTTPRuntime) Metrics() *telemetry.HTTPMetrics {
	return r.metrics
}

// Startup initializes registered extensions and marks startup complete.
func (r *HTTPRuntime) Startup(ctx context.Context) error {
	for _, hook := range r.startupHooks {
		if hook == nil {
			continue
		}
		if err := hook(ctx); err != nil {
			return err
		}
	}
	r.health.MarkStarted()
	return nil
}

// Shutdown fail-closes readiness and runs runtime shutdown hooks.
func (r *HTTPRuntime) Shutdown(ctx context.Context) error {
	r.health.MarkShuttingDown()
	var firstErr error
	for _, hook := range r.shutdownHooks {
		if hook == nil {
			continue
		}
		if err := hook(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func mountAPISubrouter(rootMux *http.ServeMux, prefix string, apiHandler http.Handler) {
	if rootMux == nil || apiHandler == nil {
		return
	}

	trimmed := strings.TrimSuffix(prefix, "/")
	if trimmed == "" {
		rootMux.Handle("/", apiHandler)
		return
	}
	rootMux.Handle(trimmed+"/", http.StripPrefix(trimmed, apiHandler))
	rootMux.Handle(trimmed, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, trimmed+"/", http.StatusTemporaryRedirect)
	}))
}

func normalizeRoutePrefix(prefix string) string {
	if prefix == "" {
		return "/"
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	if strings.Contains(prefix, "//") {
		return "/"
	}
	return strings.TrimSuffix(prefix, "/")
}

// MustNewHTTPRuntime is a convenience helper for bootstrapping examples/tests.
func MustNewHTTPRuntime(cfg HTTPRuntimeConfig) *HTTPRuntime {
	runtime, err := NewHTTPRuntime(cfg)
	if err != nil {
		panic(fmt.Sprintf("failed to build DenseCloud HTTP runtime: %v", err))
	}
	return runtime
}
