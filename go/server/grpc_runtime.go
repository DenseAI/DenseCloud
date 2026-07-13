package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	densemiddleware "github.com/DenseAI/DenseCloud/go/middleware"
	"github.com/DenseAI/DenseCloud/go/telemetry"
	"google.golang.org/grpc"
	grpc_health "google.golang.org/grpc/health"
	grpc_health_v1 "google.golang.org/grpc/health/grpc_health_v1"
)

// GRPCRuntimeConfig defines DenseCloud's shared gRPC chassis assembly.
type GRPCRuntimeConfig struct {
	ServiceName          string
	Address              string
	Listener             net.Listener
	ServerOptions        []grpc.ServerOption
	MiddlewarePreset     densemiddleware.GRPCServerPresetConfig
	Metrics              *telemetry.GRPCMetrics
	DisableHealthService bool
	StartupHooks         []StartupHook
	ShutdownHooks        []ShutdownHook
}

// GRPCRuntime wires shared gRPC interceptors, health, metrics, and lifecycle.
type GRPCRuntime struct {
	server      *grpc.Server
	metrics     *telemetry.GRPCMetrics
	health      *grpc_health.Server
	serviceName string

	mu                sync.Mutex
	address           string
	listener          net.Listener
	serveAttempt      bool
	shutdownHooksOnce sync.Once
	shutdownError     error
	lifecycleMu       sync.Mutex
	shuttingDown      bool

	startupHooks  []StartupHook
	shutdownHooks []ShutdownHook
}

var _ GracefulGRPCServer = (*GRPCRuntime)(nil)

// NewGRPCRuntime creates a DenseCloud-owned shared gRPC runtime assembly.
func NewGRPCRuntime(cfg GRPCRuntimeConfig) (*GRPCRuntime, error) {
	serviceName := cfg.ServiceName
	if serviceName == "" {
		serviceName = "dense-service"
	}

	address := cfg.Address
	if address == "" {
		address = ":9090"
	}
	if cfg.Listener != nil {
		address = cfg.Listener.Addr().String()
	}

	metrics := cfg.Metrics
	if metrics == nil {
		metrics = telemetry.NewGRPCMetrics(telemetry.GRPCMetricsConfig{
			ServiceName: serviceName,
		})
	}

	presetCfg := cfg.MiddlewarePreset
	presetCfg.Metrics = metrics
	unary, stream := densemiddleware.GRPCServerPreset(presetCfg)

	serverOptions := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(unary...),
		grpc.ChainStreamInterceptor(stream...),
	}
	serverOptions = append(serverOptions, cfg.ServerOptions...)

	runtime := &GRPCRuntime{
		server:        grpc.NewServer(serverOptions...),
		metrics:       metrics,
		serviceName:   serviceName,
		address:       address,
		listener:      cfg.Listener,
		startupHooks:  append([]StartupHook(nil), cfg.StartupHooks...),
		shutdownHooks: append([]ShutdownHook(nil), cfg.ShutdownHooks...),
	}

	if !cfg.DisableHealthService {
		runtime.health = grpc_health.NewServer()
		runtime.setServingStatus(grpc_health_v1.HealthCheckResponse_NOT_SERVING)
		grpc_health_v1.RegisterHealthServer(runtime.server, runtime.health)
	}

	return runtime, nil
}

// Server returns the shared gRPC server owned by the runtime.
func (r *GRPCRuntime) Server() *grpc.Server {
	return r.server
}

// Metrics returns the shared gRPC metrics collector.
func (r *GRPCRuntime) Metrics() *telemetry.GRPCMetrics {
	return r.metrics
}

// Health returns the shared gRPC health service, or nil when disabled.
func (r *GRPCRuntime) Health() *grpc_health.Server {
	return r.health
}

// Address returns the configured or bound listener address.
func (r *GRPCRuntime) Address() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.address
}

// Start runs startup hooks, lazily binds the listener when needed, and serves.
func (r *GRPCRuntime) Start() error {
	listener, err := r.prepareListener()
	if err != nil {
		return err
	}

	if err := r.startup(context.Background()); err != nil {
		_ = listener.Close()
		return err
	}

	err = r.server.Serve(listener)
	if errors.Is(err, grpc.ErrServerStopped) {
		return nil
	}
	_ = listener.Close()
	r.failHealth()
	if shutdownErr := r.runShutdownHooks(context.Background()); shutdownErr != nil {
		return errors.Join(err, shutdownErr)
	}
	return err
}

// Stop fail-closes health, runs shutdown hooks once, and force-stops the server.
func (r *GRPCRuntime) Stop() {
	r.failHealth()
	r.server.Stop()
	_ = r.runShutdownHooks(context.Background())
}

// GracefulStop fail-closes health, runs shutdown hooks once, and waits for in-flight RPCs.
func (r *GRPCRuntime) GracefulStop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	r.failHealth()

	done := make(chan struct{})
	go func() {
		r.server.GracefulStop()
		close(done)
	}()

	select {
	case <-done:
		return r.runShutdownHooks(ctx)
	case <-ctx.Done():
		r.server.Stop()
		if shutdownErr := r.runShutdownHooks(ctx); shutdownErr != nil {
			return errors.Join(ctx.Err(), shutdownErr)
		}
		return ctx.Err()
	}
}

func (r *GRPCRuntime) startup(ctx context.Context) error {
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()

	if r.shuttingDown {
		return fmt.Errorf("grpc runtime is shutting down")
	}

	for _, hook := range r.startupHooks {
		if hook == nil {
			continue
		}
		if err := hook(ctx); err != nil {
			r.markShuttingDownLocked()
			if rollbackErr := r.runShutdownHooks(ctx); rollbackErr != nil {
				return errors.Join(err, rollbackErr)
			}
			return err
		}
	}
	r.setServingStatus(grpc_health_v1.HealthCheckResponse_SERVING)
	return nil
}

func (r *GRPCRuntime) runShutdownHooks(ctx context.Context) error {
	r.shutdownHooksOnce.Do(func() {
		for _, hook := range r.shutdownHooks {
			if hook == nil {
				continue
			}
			if err := hook(ctx); err != nil && r.shutdownError == nil {
				r.shutdownError = err
			}
		}
	})
	return r.shutdownError
}

func (r *GRPCRuntime) prepareListener() (net.Listener, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.serveAttempt {
		return nil, fmt.Errorf("grpc runtime already started")
	}
	r.serveAttempt = true

	if r.listener != nil {
		r.address = r.listener.Addr().String()
		return r.listener, nil
	}

	listener, err := net.Listen("tcp", r.address)
	if err != nil {
		return nil, err
	}
	r.listener = listener
	r.address = listener.Addr().String()
	return listener, nil
}

func (r *GRPCRuntime) setServingStatus(status grpc_health_v1.HealthCheckResponse_ServingStatus) {
	if r.health == nil {
		return
	}
	r.health.SetServingStatus("", status)
	if r.serviceName != "" {
		r.health.SetServingStatus(r.serviceName, status)
	}
}

func (r *GRPCRuntime) failHealth() {
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	r.markShuttingDownLocked()
}

func (r *GRPCRuntime) markShuttingDownLocked() {
	r.shuttingDown = true
	if r.health != nil {
		r.health.Shutdown()
	}
}
