package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	densemiddleware "github.com/DenseAI/DenseCloud/go/middleware"
	"github.com/DenseAI/DenseCloud/go/telemetry"
)

// HTTPRuntimeConfig defines DenseCloud's shared HTTP chassis assembly.
type HTTPRuntimeConfig struct {
	ServiceName                 string
	RootMux                     *http.ServeMux
	APIMux                      *http.ServeMux
	APIBasePath                 string
	MiddlewarePreset            *HTTPMiddlewarePresetConfig
	RootMiddleware              []func(http.Handler) http.Handler
	APIMiddleware               []func(http.Handler) http.Handler
	Health                      *HealthRegistry
	HealthCheckTimeout          time.Duration
	Metrics                     *telemetry.HTTPMetrics
	MetricsCollectors           []telemetry.PrometheusCollector
	MetricsPath                 string
	MetricsPathLabeler          func(*http.Request) string
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
	shutdownOnce  sync.Once
	shutdownErr   error
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
	if err := validateRoutePrefix("APIBasePath", apiBasePath); err != nil {
		return nil, err
	}

	health := cfg.Health
	if health == nil {
		health = NewHealthRegistry(WithHealthCheckTimeout(cfg.HealthCheckTimeout))
	} else {
		health.setHealthCheckTimeout(cfg.HealthCheckTimeout)
	}
	registerHealth := !cfg.DisableHealthRoutes
	if registerHealth {
		health.RegisterHandlers(rootMux)
	}

	metricsPath := cfg.MetricsPath
	if metricsPath == "" {
		metricsPath = "/metrics"
	}
	metricsPath = normalizeRoutePrefix(metricsPath)
	if err := validateRoutePrefix("MetricsPath", metricsPath); err != nil {
		return nil, err
	}
	registerMetrics := !cfg.DisableMetricsRoute

	metrics := cfg.Metrics
	if metrics == nil && registerMetrics {
		metrics = telemetry.NewHTTPMetrics(telemetry.HTTPMetricsConfig{
			ServiceName: cfg.ServiceName,
			Collectors: append(
				[]telemetry.PrometheusCollector(nil),
				cfg.MetricsCollectors...,
			),
			PathLabeler: metricsPathLabelerOrDefault(
				cfg.MetricsPathLabeler,
				apiBasePath,
				metricsPath,
			),
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

	rootMiddleware := append([]func(http.Handler) http.Handler(nil), cfg.RootMiddleware...)
	if cfg.MiddlewarePreset != nil {
		rootMiddleware = append(DefaultHTTPMiddleware(*cfg.MiddlewarePreset), rootMiddleware...)
	}

	rootHandler := http.Handler(rootMux)
	rootHandler = densemiddleware.ContentTypeForPrefix(apiBasePath, "application/json")(rootHandler)
	rootHandler = densemiddleware.Chain(rootMiddleware...)(rootHandler)
	if metrics != nil {
		rootHandler = metrics.Middleware()(rootHandler)
	}
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
			if rollbackErr := r.Shutdown(ctx); rollbackErr != nil {
				return errors.Join(err, rollbackErr)
			}
			return err
		}
	}
	r.health.MarkStarted()
	return nil
}

// BeginShutdown fail-closes readiness before the transport drain starts.
func (r *HTTPRuntime) BeginShutdown(context.Context) error {
	r.health.MarkShuttingDown()
	return nil
}

// Shutdown fail-closes readiness and runs runtime shutdown hooks once.
func (r *HTTPRuntime) Shutdown(ctx context.Context) error {
	_ = r.BeginShutdown(ctx)
	r.shutdownOnce.Do(func() {
		for _, hook := range r.shutdownHooks {
			if hook == nil {
				continue
			}
			if err := hook(ctx); err != nil && r.shutdownErr == nil {
				r.shutdownErr = err
			}
		}
	})
	return r.shutdownErr
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
	return strings.TrimSuffix(prefix, "/")
}

func validateRoutePrefix(name, prefix string) error {
	if strings.Contains(prefix, "//") {
		return fmt.Errorf("%s must not contain empty path segments: %q", name, prefix)
	}
	return nil
}

func metricsPathLabelerOrDefault(
	configured func(*http.Request) string,
	apiBasePath string,
	metricsPath string,
) func(*http.Request) string {
	if configured != nil {
		return configured
	}

	apiBasePath = normalizeLabelPrefix(apiBasePath)
	metricsPath = normalizeLabelPrefix(metricsPath)
	return func(r *http.Request) string {
		if r == nil || r.URL == nil || r.URL.Path == "" {
			return "/"
		}
		path := r.URL.Path
		if path == metricsPath || path == "/health" || strings.HasPrefix(path, "/health/") {
			return path
		}
		if routePathHasPrefix(path, apiBasePath) {
			if apiBasePath == "/" {
				return "/*"
			}
			if path == apiBasePath {
				return apiBasePath
			}
			return apiBasePath + "/*"
		}
		return path
	}
}

func normalizeLabelPrefix(prefix string) string {
	prefix = normalizeRoutePrefix(prefix)
	if prefix == "" {
		return "/"
	}
	return prefix
}

func routePathHasPrefix(path, prefix string) bool {
	if prefix == "" || prefix == "/" {
		return true
	}
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// MustNewHTTPRuntime is a convenience helper for bootstrapping examples/tests.
func MustNewHTTPRuntime(cfg HTTPRuntimeConfig) *HTTPRuntime {
	runtime, err := NewHTTPRuntime(cfg)
	if err != nil {
		panic(fmt.Sprintf("failed to build DenseCloud HTTP runtime: %v", err))
	}
	return runtime
}
