package telemetry

import (
	"testing"

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

func TestDefaultOTelConfig_DeploymentEnvironmentEmpty(t *testing.T) {
	cfg := DefaultOTelConfig()
	if cfg.DeploymentEnvironment != "" {
		t.Fatalf("expected empty default deployment environment, got %q", cfg.DeploymentEnvironment)
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
