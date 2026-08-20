package telemetry

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

func findAttributeString(attrs []attribute.KeyValue, key string) (string, bool) {
	for _, kv := range attrs {
		if string(kv.Key) == key {
			return kv.Value.AsString(), true
		}
	}
	return "", false
}

func TestOTelProviderShutdownRestoresPreviousGlobals(t *testing.T) {
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	defer func() {
		if otel.GetTracerProvider() != previousProvider {
			otel.SetTracerProvider(previousProvider)
		}
		if otel.GetTextMapPropagator() != previousPropagator {
			otel.SetTextMapPropagator(previousPropagator)
		}
	}()

	provider, err := InitOTelTracer(OTelConfig{
		Enabled:     true,
		ServiceName: "dense-test",
		Endpoint:    "127.0.0.1:4317",
		Insecure:    true,
	})
	if err != nil {
		t.Fatalf("InitOTelTracer() error = %v", err)
	}
	if provider == nil {
		t.Fatal("expected enabled tracing provider")
	}

	if err := provider.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if got := otel.GetTracerProvider(); got != previousProvider {
		t.Fatal("expected tracer provider to be restored after shutdown")
	}
	if got := otel.GetTextMapPropagator(); got != previousPropagator {
		t.Fatal("expected text-map propagator to be restored after shutdown")
	}
}

func TestDefaultOTelConfig_DeploymentEnvironmentEmpty(t *testing.T) {
	cfg := DefaultOTelConfig()
	if cfg.DeploymentEnvironment != "" {
		t.Fatalf("expected empty default deployment environment, got %q", cfg.DeploymentEnvironment)
	}
	if cfg.Insecure {
		t.Fatal("expected secure OTLP transport by default")
	}
}

func TestInitOTelTracerRejectsInsecureRemoteEndpoint(t *testing.T) {
	provider, err := InitOTelTracer(OTelConfig{
		Enabled:  true,
		Endpoint: "collector.example.com:4317",
		Insecure: true,
	})
	if err == nil || provider != nil {
		t.Fatalf("expected insecure remote endpoint rejection, provider=%v err=%v", provider, err)
	}
}

func TestResourceAttributes_DeploymentEnvironmentIncludedWhenSet(t *testing.T) {
	attrs := resourceAttributes(OTelConfig{
		ServiceName:           "dense-api",
		ServiceVersion:        "1.2.3",
		DeploymentEnvironment: "  staging  ",
	})

	if got, ok := findAttributeString(attrs, "service.name"); !ok || got != "dense-api" {
		t.Fatalf("expected service.name=dense-api, got %q (present=%t)", got, ok)
	}
	if got, ok := findAttributeString(attrs, "service.version"); !ok || got != "1.2.3" {
		t.Fatalf("expected service.version=1.2.3, got %q (present=%t)", got, ok)
	}
	if got, ok := findAttributeString(attrs, "deployment.environment"); !ok || got != "staging" {
		t.Fatalf("expected deployment.environment=staging, got %q (present=%t)", got, ok)
	}
}

func TestResourceAttributes_DeploymentEnvironmentOmittedWhenEmpty(t *testing.T) {
	attrs := resourceAttributes(OTelConfig{
		ServiceName:           "dense-api",
		ServiceVersion:        "1.2.3",
		DeploymentEnvironment: "   ",
	})

	if _, ok := findAttributeString(attrs, "deployment.environment"); ok {
		t.Fatalf("did not expect deployment.environment when config value is empty")
	}
}
