package server

import (
	"context"
	"errors"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DenseAI/DenseCloud/go/telemetry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpc_health_v1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"
)

const testBufconnSize = 1024 * 1024

func TestNewGRPCRuntime_DefaultAddressAndHealthTransitions(t *testing.T) {
	t.Parallel()

	runtime, err := NewGRPCRuntime(GRPCRuntimeConfig{
		ServiceName: "dense-test",
	})
	if err != nil {
		t.Fatalf("NewGRPCRuntime() error = %v", err)
	}

	if got := runtime.Address(); got != ":9090" {
		t.Fatalf("Address() = %q, want %q", got, ":9090")
	}
	if got := checkHealthStatus(t, runtime.Health(), ""); got != grpc_health_v1.HealthCheckResponse_NOT_SERVING {
		t.Fatalf("initial health = %s, want %s", got, grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	}

	if err := runtime.startup(context.Background()); err != nil {
		t.Fatalf("startup() error = %v", err)
	}
	if got := checkHealthStatus(t, runtime.Health(), ""); got != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Fatalf("health after startup = %s, want %s", got, grpc_health_v1.HealthCheckResponse_SERVING)
	}

	runtime.failHealth()
	if err := runtime.runShutdownHooks(context.Background()); err != nil {
		t.Fatalf("runShutdownHooks() error = %v", err)
	}
	if got := checkHealthStatus(t, runtime.Health(), ""); got != grpc_health_v1.HealthCheckResponse_NOT_SERVING {
		t.Fatalf("health after shutdown = %s, want %s", got, grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	}
}

func TestGRPCRuntime_StartServesHealthAndUsesProvidedMetrics(t *testing.T) {
	t.Parallel()

	listener := bufconn.Listen(testBufconnSize)
	metrics := telemetry.NewGRPCMetrics(telemetry.GRPCMetricsConfig{ServiceName: "dense-test"})

	runtime, err := NewGRPCRuntime(GRPCRuntimeConfig{
		ServiceName: "dense-test",
		Listener:    listener,
		Metrics:     metrics,
	})
	if err != nil {
		t.Fatalf("NewGRPCRuntime() error = %v", err)
	}
	if runtime.Metrics() != metrics {
		t.Fatalf("Metrics() did not return the provided collector")
	}

	startErrCh := make(chan error, 1)
	go func() {
		startErrCh <- runtime.Start()
	}()

	conn := newBufconnClient(t, listener)
	defer conn.Close()

	healthClient := grpc_health_v1.NewHealthClient(conn)
	waitForHealthStatus(t, healthClient, "", grpc_health_v1.HealthCheckResponse_SERVING)

	body := captureServerGRPCMetricsBody(metrics)
	requireMetricContains(t, body, `densecloud_grpc_requests_total{service="dense-test",method="/grpc.health.v1.Health/Check",rpc_type="unary",code="OK"} 1`)

	runtime.Stop()
	if err := <-startErrCh; err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if got := checkHealthStatus(t, runtime.Health(), ""); got != grpc_health_v1.HealthCheckResponse_NOT_SERVING {
		t.Fatalf("health after Stop() = %s, want %s", got, grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	}
}

func TestGRPCRuntime_StartupFailureRollsBackShutdownHooks(t *testing.T) {
	t.Parallel()

	startupErr := errors.New("startup boom")
	shutdownErr := errors.New("shutdown boom")
	var order []string

	runtime, err := NewGRPCRuntime(GRPCRuntimeConfig{
		ServiceName: "dense-test",
		Listener:    bufconn.Listen(testBufconnSize),
		StartupHooks: []StartupHook{
			func(context.Context) error {
				order = append(order, "startup-ok")
				return nil
			},
			func(context.Context) error {
				order = append(order, "startup-fail")
				return startupErr
			},
		},
		ShutdownHooks: []ShutdownHook{
			func(context.Context) error {
				order = append(order, "shutdown-a")
				return shutdownErr
			},
			func(context.Context) error {
				order = append(order, "shutdown-b")
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("NewGRPCRuntime() error = %v", err)
	}

	err = runtime.Start()
	if !errors.Is(err, startupErr) {
		t.Fatalf("Start() error = %v, want startup error", err)
	}
	if !errors.Is(err, shutdownErr) {
		t.Fatalf("Start() error = %v, want shutdown rollback error", err)
	}

	want := strings.Join([]string{"startup-ok", "startup-fail", "shutdown-a", "shutdown-b"}, ",")
	if got := strings.Join(order, ","); got != want {
		t.Fatalf("startup rollback order = %s, want %s", got, want)
	}
	if got := checkHealthStatus(t, runtime.Health(), ""); got != grpc_health_v1.HealthCheckResponse_NOT_SERVING {
		t.Fatalf("health after startup rollback = %s, want %s", got, grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	}
}

func TestGRPCRuntime_StopBeforeStartPreventsServing(t *testing.T) {
	t.Parallel()

	shutdownCalls := 0
	runtime, err := NewGRPCRuntime(GRPCRuntimeConfig{
		ServiceName: "dense-test",
		Listener:    bufconn.Listen(testBufconnSize),
		ShutdownHooks: []ShutdownHook{
			func(context.Context) error {
				shutdownCalls++
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("NewGRPCRuntime() error = %v", err)
	}

	runtime.Stop()
	err = runtime.Start()
	if err == nil || !strings.Contains(err.Error(), "shutting down") {
		t.Fatalf("Start() error = %v, want shutdown-state error", err)
	}
	if shutdownCalls != 1 {
		t.Fatalf("shutdown hook calls = %d, want 1", shutdownCalls)
	}
	if got := checkHealthStatus(t, runtime.Health(), ""); got != grpc_health_v1.HealthCheckResponse_NOT_SERVING {
		t.Fatalf("health after Stop() = %s, want %s", got, grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	}
}

func TestGRPCRuntime_GracefulStopReturnsContextError(t *testing.T) {
	t.Parallel()

	listener := bufconn.Listen(testBufconnSize)
	shutdownCalls := 0
	runtime, err := NewGRPCRuntime(GRPCRuntimeConfig{
		ServiceName: "dense-test",
		Listener:    listener,
		ShutdownHooks: []ShutdownHook{
			func(context.Context) error {
				shutdownCalls++
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("NewGRPCRuntime() error = %v", err)
	}

	service := &blockingService{entered: make(chan struct{}, 1)}
	runtime.Server().RegisterService(&blockingServiceDesc, service)

	startErrCh := make(chan error, 1)
	go func() {
		startErrCh <- runtime.Start()
	}()

	conn := newBufconnClient(t, listener)
	defer conn.Close()

	waitForHealthStatus(t, grpc_health_v1.NewHealthClient(conn), "", grpc_health_v1.HealthCheckResponse_SERVING)

	rpcErrCh := make(chan error, 1)
	go func() {
		rpcErrCh <- conn.Invoke(context.Background(), "/dense.test.Blocking/Wait", &emptypb.Empty{}, &emptypb.Empty{})
	}()

	select {
	case <-service.entered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for rpc handler entry")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err = runtime.GracefulStop(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("GracefulStop() error = %v, want %v", err, context.DeadlineExceeded)
	}
	if got := checkHealthStatus(t, runtime.Health(), ""); got != grpc_health_v1.HealthCheckResponse_NOT_SERVING {
		t.Fatalf("health after GracefulStop() = %s, want %s", got, grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	}
	if shutdownCalls != 1 {
		t.Fatalf("shutdown hook calls after forced stop = %d, want 1", shutdownCalls)
	}

	runtime.Stop()
	runtime.Stop()
	if shutdownCalls != 1 {
		t.Fatalf("shutdown hook calls = %d, want 1", shutdownCalls)
	}
	if err := <-startErrCh; err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	rpcErr := <-rpcErrCh
	if rpcErr == nil {
		t.Fatal("expected blocked rpc to terminate after Stop()")
	}
	code := status.Code(rpcErr)
	if code != codes.Unavailable && code != codes.Canceled {
		t.Fatalf("blocked rpc code = %s, want Unavailable or Canceled", code)
	}
}

func captureServerGRPCMetricsBody(grpcMetrics *telemetry.GRPCMetrics) string {
	metrics := telemetry.NewHTTPMetrics(telemetry.HTTPMetricsConfig{
		ServiceName: "dense-test",
		Collectors:  []telemetry.PrometheusCollector{grpcMetrics},
	})
	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	return rec.Body.String()
}

func requireMetricContains(t *testing.T, body, want string) {
	t.Helper()
	if !strings.Contains(body, want) {
		t.Fatalf("expected metric %q, got %q", want, body)
	}
}

func checkHealthStatus(
	t *testing.T,
	server grpc_health_v1.HealthServer,
	service string,
) grpc_health_v1.HealthCheckResponse_ServingStatus {
	t.Helper()

	resp, err := server.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{Service: service})
	if err != nil {
		t.Fatalf("Health.Check(%q) error = %v", service, err)
	}
	return resp.Status
}

func waitForHealthStatus(
	t *testing.T,
	client grpc_health_v1.HealthClient,
	service string,
	want grpc_health_v1.HealthCheckResponse_ServingStatus,
) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{Service: service})
		if err == nil && resp.Status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for health status %s", want)
}

func newBufconnClient(t *testing.T, listener *bufconn.Listener) *grpc.ClientConn {
	t.Helper()

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient() error = %v", err)
	}
	return conn
}

type blockingService struct {
	entered chan struct{}
}

type blockingServiceServer interface {
	Wait(context.Context, *emptypb.Empty) (*emptypb.Empty, error)
}

func (s *blockingService) Wait(ctx context.Context, _ *emptypb.Empty) (*emptypb.Empty, error) {
	select {
	case s.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

var blockingServiceDesc = grpc.ServiceDesc{
	ServiceName: "dense.test.Blocking",
	HandlerType: (*blockingServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Wait",
			Handler: func(
				srv any,
				ctx context.Context,
				dec func(any) error,
				interceptor grpc.UnaryServerInterceptor,
			) (any, error) {
				req := new(emptypb.Empty)
				if err := dec(req); err != nil {
					return nil, err
				}
				if interceptor == nil {
					return srv.(*blockingService).Wait(ctx, req)
				}
				info := &grpc.UnaryServerInfo{
					Server:     srv,
					FullMethod: "/dense.test.Blocking/Wait",
				}
				handler := func(callCtx context.Context, request any) (any, error) {
					return srv.(*blockingService).Wait(callCtx, request.(*emptypb.Empty))
				}
				return interceptor(ctx, req, info, handler)
			},
		},
	},
}
