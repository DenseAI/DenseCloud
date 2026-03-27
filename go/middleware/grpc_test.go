package middleware

import (
	"context"
	"net"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DenseAI/DenseCloud/go/telemetry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func TestGRPCRequestIDUnary_InjectsContextValue(t *testing.T) {
	t.Parallel()

	interceptor := GRPCRequestIDUnary()
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-request-id", "req-123"))

	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/dense.Service/Call"}, func(ctx context.Context, req any) (any, error) {
		if got := GetRequestID(ctx); got != "req-123" {
			t.Fatalf("expected request id in context, got %q", got)
		}
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("interceptor returned error: %v", err)
	}
}

func TestGRPCRecoveryUnary_RecoversPanics(t *testing.T) {
	t.Parallel()

	interceptor := GRPCRecoveryUnary()
	_, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/dense.Service/Call"}, func(context.Context, any) (any, error) {
		panic("boom")
	})
	if status.Code(err) != codes.Internal {
		t.Fatalf("expected internal error, got %v", err)
	}
}

func TestGRPCRateLimitUnaryWithKey_IsolatedByKey(t *testing.T) {
	t.Parallel()

	limiter := NewPartitionedRateLimiter(1, 1, 0)
	interceptor := GRPCRateLimitUnaryWithKey(limiter, func(ctx context.Context, _ any, _ *grpc.UnaryServerInfo) string {
		md, _ := metadata.FromIncomingContext(ctx)
		values := md.Get("x-tenant")
		if len(values) == 0 {
			return ""
		}
		return values[0]
	})

	call := func(tenant string) error {
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-tenant", tenant))
		_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/dense.Service/Call"}, func(context.Context, any) (any, error) {
			return "ok", nil
		})
		return err
	}

	if err := call("tenant-a"); err != nil {
		t.Fatalf("first tenant-a call failed: %v", err)
	}
	if code := status.Code(call("tenant-a")); code != codes.ResourceExhausted {
		t.Fatalf("expected tenant-a second call to be limited, got %s", code)
	}
	if err := call("tenant-b"); err != nil {
		t.Fatalf("tenant-b call failed: %v", err)
	}
}

func TestDefaultGRPCKey_PrefersStableClientIdentityOverRequestID(t *testing.T) {
	t.Parallel()

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-request-id", "req-123"))
	ctx = context.WithValue(ctx, requestIDKey, "req-123")
	ctx = peer.NewContext(ctx, &peer.Peer{
		Addr: &net.TCPAddr{IP: net.ParseIP("203.0.113.10"), Port: 54432},
	})

	if got := defaultGRPCKey(ctx, "/dense.Service/Call"); got != "203.0.113.10" {
		t.Fatalf("defaultGRPCKey() = %q, want stable peer IP", got)
	}
}

func TestGRPCMetricsUnary_RecordsMetrics(t *testing.T) {
	t.Parallel()

	metrics := telemetry.NewGRPCMetrics(telemetry.GRPCMetricsConfig{ServiceName: "dense-test"})
	interceptor := GRPCMetricsUnary(metrics)

	_, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/dense.Service/Call"}, func(context.Context, any) (any, error) {
		return nil, status.Error(codes.ResourceExhausted, "busy")
	})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("unexpected error code: %v", err)
	}

	httpMetrics := telemetry.NewHTTPMetrics(telemetry.HTTPMetricsConfig{
		ServiceName: "dense-test",
		Collectors:  []telemetry.PrometheusCollector{metrics},
	})
	body := captureMetricsBody(httpMetrics)
	if !strings.Contains(body, `densecloud_grpc_requests_total{service="dense-test",method="/dense.Service/Call",rpc_type="unary",code="ResourceExhausted"} 1`) {
		t.Fatalf("expected grpc request metric, got %q", body)
	}
	if !strings.Contains(body, `densecloud_grpc_request_errors_total{service="dense-test",method="/dense.Service/Call",rpc_type="unary",code="ResourceExhausted"} 1`) {
		t.Fatalf("expected grpc error metric, got %q", body)
	}
}

func captureMetricsBody(metrics *telemetry.HTTPMetrics) string {
	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	return rec.Body.String()
}
