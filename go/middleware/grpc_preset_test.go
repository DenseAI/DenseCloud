package middleware

import (
	"context"
	"testing"

	"github.com/DenseAI/DenseCloud/go/telemetry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

func TestGRPCServerPreset_RecordsExtraInterceptorRejections(t *testing.T) {
	t.Parallel()

	metrics := telemetry.NewGRPCMetrics(telemetry.GRPCMetricsConfig{ServiceName: "dense-test"})
	unary, _ := GRPCServerPreset(GRPCServerPresetConfig{
		Metrics: metrics,
		ExtraUnaryInterceptors: []grpc.UnaryServerInterceptor{
			func(context.Context, any, *grpc.UnaryServerInfo, grpc.UnaryHandler) (any, error) {
				return nil, status.Error(codes.PermissionDenied, "blocked")
			},
		},
	})

	interceptor := ChainUnaryInterceptors(unary...)
	_, err := interceptor(
		context.Background(),
		nil,
		&grpc.UnaryServerInfo{FullMethod: "/dense.Test/Blocked"},
		func(context.Context, any) (any, error) {
			t.Fatalf("handler should not be called after extra interceptor rejection")
			return nil, nil
		},
	)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %v", err)
	}

	body := captureGRPCMetricsBody(metrics)
	requireMetricContains(t, body, `densecloud_grpc_requests_total{service="dense-test",method="/dense.Test/Blocked",rpc_type="unary",code="PermissionDenied"} 1`)
	requireMetricContains(t, body, `densecloud_grpc_request_errors_total{service="dense-test",method="/dense.Test/Blocked",rpc_type="unary",code="PermissionDenied"} 1`)
}
