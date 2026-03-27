package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// OTelConfig holds OpenTelemetry configuration.
type OTelConfig struct {
	ServiceName    string
	ServiceVersion string
	// DeploymentEnvironment maps to deployment.environment when provided.
	DeploymentEnvironment string
	Endpoint              string
	Enabled               bool
	Insecure              bool
	SamplingRate          float64
	BatchTimeout          time.Duration
}

// DefaultOTelConfig returns sensible defaults for tracing.
func DefaultOTelConfig() OTelConfig {
	return OTelConfig{
		ServiceName:    "dense-service",
		ServiceVersion: "0.0.0",
		// Empty by default so callers can opt in explicitly per deployment.
		DeploymentEnvironment: "",
		Endpoint:              "localhost:4317",
		Enabled:               false,
		Insecure:              true,
		SamplingRate:          1.0,
		BatchTimeout:          5 * time.Second,
	}
}

// OTelProvider wraps the tracer provider and provides cleanup methods.
type OTelProvider struct {
	provider *sdktrace.TracerProvider
	config   OTelConfig
}

// InitOTelTracer initializes the OpenTelemetry tracer provider.
func InitOTelTracer(cfg OTelConfig) (*OTelProvider, error) {
	if !cfg.Enabled {
		slog.Info("OpenTelemetry tracing disabled")
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(cfg.Endpoint),
	}
	if cfg.Insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}

	exporter, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP exporter: %w", err)
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			resourceAttributes(cfg)...,
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	var sampler sdktrace.Sampler
	if cfg.SamplingRate >= 1.0 {
		sampler = sdktrace.AlwaysSample()
	} else if cfg.SamplingRate <= 0 {
		sampler = sdktrace.NeverSample()
	} else {
		sampler = sdktrace.TraceIDRatioBased(cfg.SamplingRate)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(cfg.BatchTimeout)),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	logAttrs := []any{
		slog.String("endpoint", cfg.Endpoint),
		slog.String("service", cfg.ServiceName),
		slog.Float64("sampling_rate", cfg.SamplingRate),
	}
	if env := strings.TrimSpace(cfg.DeploymentEnvironment); env != "" {
		logAttrs = append(logAttrs, slog.String("deployment_environment", env))
	}
	slog.Info("OpenTelemetry tracing initialized", logAttrs...)

	return &OTelProvider{
		provider: tp,
		config:   cfg,
	}, nil
}

// Shutdown gracefully shuts down the tracer provider.
func (p *OTelProvider) Shutdown(ctx context.Context) error {
	if p == nil || p.provider == nil {
		return nil
	}
	slog.Info("shutting down OpenTelemetry tracer provider")
	return p.provider.Shutdown(ctx)
}

// ShutdownWithTimeout shuts down the provider with a timeout.
func (p *OTelProvider) ShutdownWithTimeout(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return p.Shutdown(ctx)
}

func resourceAttributes(cfg OTelConfig) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceVersion(cfg.ServiceVersion),
	}
	if env := strings.TrimSpace(cfg.DeploymentEnvironment); env != "" {
		attrs = append(attrs, attribute.String("deployment.environment", env))
	}
	return attrs
}
