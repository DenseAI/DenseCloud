package middleware

import (
	"context"
	"testing"

	"google.golang.org/grpc"
)

func TestGRPCServerPreset_OrderAndOptionalLayers(t *testing.T) {
	t.Parallel()

	cb := DefaultCircuitBreakerConfig()
	limiter := NewPartitionedRateLimiter(1, 1, 0)
	unary, stream := GRPCServerPreset(GRPCServerPresetConfig{
		TracerName: "dense-test",
		ExtraUnaryInterceptors: []grpc.UnaryServerInterceptor{
			func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
				return handler(ctx, req)
			},
		},
		ExtraStreamInterceptors: []grpc.StreamServerInterceptor{
			func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
				return handler(srv, ss)
			},
		},
		CircuitBreaker: &cb,
		RateLimiter:    limiter,
	})

	if len(unary) != 8 {
		t.Fatalf("expected 8 unary interceptors, got %d", len(unary))
	}
	if len(stream) != 8 {
		t.Fatalf("expected 8 stream interceptors, got %d", len(stream))
	}

	unaryNoExtras, streamNoExtras := GRPCServerPreset(GRPCServerPresetConfig{TracerName: "dense-test"})
	if len(unaryNoExtras) != 5 {
		t.Fatalf("expected 5 unary interceptors without extras, got %d", len(unaryNoExtras))
	}
	if len(streamNoExtras) != 5 {
		t.Fatalf("expected 5 stream interceptors without extras, got %d", len(streamNoExtras))
	}
}
