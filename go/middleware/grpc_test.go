package middleware

import (
	"context"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestGRPCRateLimitUnaryWithKey_DefaultExtractorIgnoresSpoofedMetadata(t *testing.T) {
	t.Parallel()

	limiter := NewPartitionedRateLimiter(1, 1, time.Minute)
	interceptor := GRPCRateLimitUnaryWithKey(limiter, nil)

	call := func(forwardedFor, apiKey string) error {
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
			"x-forwarded-for", forwardedFor,
			"x-api-key", apiKey,
		))
		ctx = peer.NewContext(ctx, &peer.Peer{
			Addr: &net.TCPAddr{IP: net.ParseIP("203.0.113.12"), Port: 50051},
		})
		_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/dense.Service/Call"}, func(context.Context, any) (any, error) {
			return "ok", nil
		})
		return err
	}

	if err := call("198.51.100.1", "tenant-a"); err != nil {
		t.Fatalf("expected first default-key call to pass, got %v", err)
	}
	if code := status.Code(call("198.51.100.2", "tenant-b")); code != codes.ResourceExhausted {
		t.Fatalf("expected rotated x-forwarded-for/x-api-key metadata to stay limited by peer IP, got %s", code)
	}
}

func TestGRPCMetricsUnary_RecordsMetrics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		interceptor func(*telemetry.GRPCMetrics) grpc.UnaryServerInterceptor
		handler     grpc.UnaryHandler
		wantCode    codes.Code
		wantPanic   bool
	}{
		{
			name:        "success",
			interceptor: GRPCMetricsUnary,
			handler: func(context.Context, any) (any, error) {
				return "ok", nil
			},
			wantCode: codes.OK,
		},
		{
			name:        "handler error",
			interceptor: GRPCMetricsUnary,
			handler: func(context.Context, any) (any, error) {
				return nil, status.Error(codes.ResourceExhausted, "busy")
			},
			wantCode: codes.ResourceExhausted,
		},
		{
			name:        "handler panic",
			interceptor: GRPCMetricsUnary,
			handler: func(context.Context, any) (any, error) {
				panic("boom")
			},
			wantCode:  codes.Internal,
			wantPanic: true,
		},
		{
			name: "metrics inside recovery",
			interceptor: func(metrics *telemetry.GRPCMetrics) grpc.UnaryServerInterceptor {
				return ChainUnaryInterceptors(GRPCRecoveryUnary(), GRPCMetricsUnary(metrics))
			},
			handler: func(context.Context, any) (any, error) {
				panic("boom")
			},
			wantCode: codes.Internal,
		},
		{
			name: "metrics outside recovery",
			interceptor: func(metrics *telemetry.GRPCMetrics) grpc.UnaryServerInterceptor {
				return ChainUnaryInterceptors(GRPCMetricsUnary(metrics), GRPCRecoveryUnary())
			},
			handler: func(context.Context, any) (any, error) {
				panic("boom")
			},
			wantCode: codes.Internal,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			metrics := telemetry.NewGRPCMetrics(telemetry.GRPCMetricsConfig{ServiceName: "dense-test"})
			interceptor := tt.interceptor(metrics)
			info := &grpc.UnaryServerInfo{FullMethod: "/dense.Service/Call"}

			_, err, panicked := callUnaryAndRecover(interceptor, context.Background(), nil, info, tt.handler)
			if panicked != tt.wantPanic {
				t.Fatalf("panic = %v, want %v", panicked, tt.wantPanic)
			}
			if !tt.wantPanic && status.Code(err) != tt.wantCode {
				t.Fatalf("status.Code(err) = %s, want %s", status.Code(err), tt.wantCode)
			}

			body := captureGRPCMetricsBody(metrics)
			requireMetricContains(t, body, `densecloud_grpc_in_flight_requests{service="dense-test"} 0`)
			requireMetricContains(t, body, `densecloud_grpc_requests_total{service="dense-test",method="/dense.Service/Call",rpc_type="unary",code="`+tt.wantCode.String()+`"} 1`)
			requireMetricContains(t, body, `densecloud_grpc_request_duration_seconds_count{service="dense-test",method="/dense.Service/Call",rpc_type="unary"} 1`)
			if tt.wantCode != codes.OK {
				requireMetricContains(t, body, `densecloud_grpc_request_errors_total{service="dense-test",method="/dense.Service/Call",rpc_type="unary",code="`+tt.wantCode.String()+`"} 1`)
			}
		})
	}
}

func TestGRPCMetricsStream_RecordsMetrics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		interceptor func(*telemetry.GRPCMetrics) grpc.StreamServerInterceptor
		handler     grpc.StreamHandler
		wantCode    codes.Code
		wantPanic   bool
	}{
		{
			name:        "success",
			interceptor: GRPCMetricsStream,
			handler: func(any, grpc.ServerStream) error {
				return nil
			},
			wantCode: codes.OK,
		},
		{
			name:        "handler error",
			interceptor: GRPCMetricsStream,
			handler: func(any, grpc.ServerStream) error {
				return status.Error(codes.Unavailable, "down")
			},
			wantCode: codes.Unavailable,
		},
		{
			name:        "handler panic",
			interceptor: GRPCMetricsStream,
			handler: func(any, grpc.ServerStream) error {
				panic("boom")
			},
			wantCode:  codes.Internal,
			wantPanic: true,
		},
		{
			name: "metrics inside recovery",
			interceptor: func(metrics *telemetry.GRPCMetrics) grpc.StreamServerInterceptor {
				return ChainStreamInterceptors(GRPCRecoveryStream(), GRPCMetricsStream(metrics))
			},
			handler: func(any, grpc.ServerStream) error {
				panic("boom")
			},
			wantCode: codes.Internal,
		},
		{
			name: "metrics outside recovery",
			interceptor: func(metrics *telemetry.GRPCMetrics) grpc.StreamServerInterceptor {
				return ChainStreamInterceptors(GRPCMetricsStream(metrics), GRPCRecoveryStream())
			},
			handler: func(any, grpc.ServerStream) error {
				panic("boom")
			},
			wantCode: codes.Internal,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			metrics := telemetry.NewGRPCMetrics(telemetry.GRPCMetricsConfig{ServiceName: "dense-test"})
			interceptor := tt.interceptor(metrics)
			info := &grpc.StreamServerInfo{FullMethod: "/dense.Service/Stream"}
			stream := testServerStream{ctx: context.Background()}

			err, panicked := callStreamAndRecover(interceptor, nil, stream, info, tt.handler)
			if panicked != tt.wantPanic {
				t.Fatalf("panic = %v, want %v", panicked, tt.wantPanic)
			}
			if !tt.wantPanic && status.Code(err) != tt.wantCode {
				t.Fatalf("status.Code(err) = %s, want %s", status.Code(err), tt.wantCode)
			}

			body := captureGRPCMetricsBody(metrics)
			requireMetricContains(t, body, `densecloud_grpc_in_flight_requests{service="dense-test"} 0`)
			requireMetricContains(t, body, `densecloud_grpc_requests_total{service="dense-test",method="/dense.Service/Stream",rpc_type="stream",code="`+tt.wantCode.String()+`"} 1`)
			requireMetricContains(t, body, `densecloud_grpc_request_duration_seconds_count{service="dense-test",method="/dense.Service/Stream",rpc_type="stream"} 1`)
			if tt.wantCode != codes.OK {
				requireMetricContains(t, body, `densecloud_grpc_request_errors_total{service="dense-test",method="/dense.Service/Stream",rpc_type="stream",code="`+tt.wantCode.String()+`"} 1`)
			}
		})
	}
}

func callUnaryAndRecover(
	interceptor grpc.UnaryServerInterceptor,
	ctx context.Context,
	req any,
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (resp any, err error, panicked bool) {
	defer func() {
		if rec := recover(); rec != nil {
			panicked = true
		}
	}()
	resp, err = interceptor(ctx, req, info, handler)
	return resp, err, false
}

func callStreamAndRecover(
	interceptor grpc.StreamServerInterceptor,
	srv any,
	ss grpc.ServerStream,
	info *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) (err error, panicked bool) {
	defer func() {
		if rec := recover(); rec != nil {
			panicked = true
		}
	}()
	err = interceptor(srv, ss, info, handler)
	return err, false
}

func requireMetricContains(t *testing.T, body, want string) {
	t.Helper()
	if !strings.Contains(body, want) {
		t.Fatalf("expected metric %q, got %q", want, body)
	}
}

func captureGRPCMetricsBody(grpcMetrics *telemetry.GRPCMetrics) string {
	metrics := telemetry.NewHTTPMetrics(telemetry.HTTPMetricsConfig{
		ServiceName: "dense-test",
		Collectors:  []telemetry.PrometheusCollector{grpcMetrics},
	})
	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	return rec.Body.String()
}

type testServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s testServerStream) Context() context.Context {
	return s.ctx
}
