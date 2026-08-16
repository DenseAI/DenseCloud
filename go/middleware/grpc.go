package middleware

import (
	"context"
	"log/slog"
	"net"
	"runtime/debug"
	"strings"
	"time"

	"github.com/DenseAI/DenseCloud/go/telemetry"
	"github.com/sony/gobreaker/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// UnaryRateLimitKeyExtractor returns the rate-limit key for a unary gRPC request.
type UnaryRateLimitKeyExtractor func(context.Context, any, *grpc.UnaryServerInfo) string

// StreamRateLimitKeyExtractor returns the rate-limit key for a streaming gRPC request.
type StreamRateLimitKeyExtractor func(any, grpc.ServerStream, *grpc.StreamServerInfo) string

// ChainUnaryInterceptors combines unary interceptors in declaration order.
func ChainUnaryInterceptors(interceptors ...grpc.UnaryServerInterceptor) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		chained := handler
		for i := len(interceptors) - 1; i >= 0; i-- {
			if interceptors[i] == nil {
				continue
			}
			next := chained
			interceptor := interceptors[i]
			chained = func(currentCtx context.Context, currentReq any) (any, error) {
				return interceptor(currentCtx, currentReq, info, next)
			}
		}
		return chained(ctx, req)
	}
}

// ChainStreamInterceptors combines stream interceptors in declaration order.
func ChainStreamInterceptors(interceptors ...grpc.StreamServerInterceptor) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		chained := handler
		for i := len(interceptors) - 1; i >= 0; i-- {
			if interceptors[i] == nil {
				continue
			}
			next := chained
			interceptor := interceptors[i]
			chained = func(currentSrv any, currentStream grpc.ServerStream) error {
				return interceptor(currentSrv, currentStream, info, next)
			}
		}
		return chained(srv, ss)
	}
}

// GRPCRequestIDUnary injects request IDs into unary contexts and headers.
func GRPCRequestIDUnary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		requestID := grpcRequestID(ctx)
		ctx = context.WithValue(ctx, requestIDKey, requestID)
		_ = grpc.SetHeader(ctx, metadata.Pairs("x-request-id", requestID))
		return handler(ctx, req)
	}
}

// GRPCRequestIDStream injects request IDs into stream contexts and headers.
func GRPCRequestIDStream() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		requestID := grpcRequestID(ss.Context())
		ctx := context.WithValue(ss.Context(), requestIDKey, requestID)
		wrapped := &wrappedServerStream{ServerStream: ss, ctx: ctx}
		_ = ss.SetHeader(metadata.Pairs("x-request-id", requestID))
		return handler(srv, wrapped)
	}
}

// GRPCRecoveryUnary prevents panics from crashing the process.
func GRPCRecoveryUnary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (_ any, err error) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("grpc panic recovered",
					slog.String("request_id", GetRequestID(ctx)),
					slog.String("method", info.FullMethod),
					slog.Any("panic", rec),
					slog.String("stack", string(debug.Stack())),
				)
				err = status.Error(codes.Internal, "internal server error")
			}
		}()
		return handler(ctx, req)
	}
}

// GRPCRecoveryStream prevents panics from crashing the process.
func GRPCRecoveryStream() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("grpc panic recovered",
					slog.String("request_id", GetRequestID(ss.Context())),
					slog.String("method", info.FullMethod),
					slog.Any("panic", rec),
					slog.String("stack", string(debug.Stack())),
				)
				err = status.Error(codes.Internal, "internal server error")
			}
		}()
		return handler(srv, ss)
	}
}

// GRPCLoggingUnary records unary request metadata via slog.
func GRPCLoggingUnary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		logGRPCCompletion("grpc request completed", ctx, info.FullMethod, time.Since(start), err)
		return resp, err
	}
}

// GRPCLoggingStream records stream request metadata via slog.
func GRPCLoggingStream() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		start := time.Now()
		err := handler(srv, ss)
		logGRPCCompletion("grpc stream completed", ss.Context(), info.FullMethod, time.Since(start), err)
		return err
	}
}

// GRPCMetricsUnary records shared gRPC metrics for unary calls.
func GRPCMetricsUnary(metrics *telemetry.GRPCMetrics) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		if metrics == nil {
			return handler(ctx, req)
		}

		start := time.Now()
		metrics.BeginRPC()
		defer func() {
			code := status.Code(err)
			if rec := recover(); rec != nil {
				code = codes.Internal
				metrics.ObserveRPC(info.FullMethod, "unary", code.String(), time.Since(start).Seconds())
				panic(rec)
			}
			metrics.ObserveRPC(info.FullMethod, "unary", code.String(), time.Since(start).Seconds())
		}()
		return handler(ctx, req)
	}
}

// GRPCMetricsStream records shared gRPC metrics for stream calls.
func GRPCMetricsStream(metrics *telemetry.GRPCMetrics) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		if metrics == nil {
			return handler(srv, ss)
		}

		start := time.Now()
		metrics.BeginRPC()
		defer func() {
			code := status.Code(err)
			if rec := recover(); rec != nil {
				code = codes.Internal
				metrics.ObserveRPC(info.FullMethod, "stream", code.String(), time.Since(start).Seconds())
				panic(rec)
			}
			metrics.ObserveRPC(info.FullMethod, "stream", code.String(), time.Since(start).Seconds())
		}()
		return handler(srv, ss)
	}
}

// GRPCTracingUnary creates an OpenTelemetry span for unary requests.
// Callers are responsible for configuring the global text map propagator
// before using tracing middleware, for example via InitOTelPropagator.
func GRPCTracingUnary(tracerName string) grpc.UnaryServerInterceptor {
	tracer := otel.Tracer(tracerName)
	propagator := otel.GetTextMapPropagator()

	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx = propagator.Extract(ctx, metadataCarrierFromIncomingContext(ctx))
		ctx, span := tracer.Start(ctx, info.FullMethod)
		defer span.End()

		if span.SpanContext().HasTraceID() {
			_ = grpc.SetHeader(ctx, metadata.Pairs("x-trace-id", span.SpanContext().TraceID().String()))
		}
		if span.SpanContext().HasSpanID() {
			_ = grpc.SetHeader(ctx, metadata.Pairs("x-span-id", span.SpanContext().SpanID().String()))
		}

		return handler(ctx, req)
	}
}

// GRPCTracingStream creates an OpenTelemetry span for stream requests.
// Callers are responsible for configuring the global text map propagator
// before using tracing middleware, for example via InitOTelPropagator.
func GRPCTracingStream(tracerName string) grpc.StreamServerInterceptor {
	tracer := otel.Tracer(tracerName)
	propagator := otel.GetTextMapPropagator()

	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := propagator.Extract(ss.Context(), metadataCarrierFromIncomingContext(ss.Context()))
		ctx, span := tracer.Start(ctx, info.FullMethod)
		defer span.End()

		wrapped := &wrappedServerStream{ServerStream: ss, ctx: ctx}
		if span.SpanContext().HasTraceID() {
			_ = ss.SetHeader(metadata.Pairs("x-trace-id", span.SpanContext().TraceID().String()))
		}
		if span.SpanContext().HasSpanID() {
			_ = ss.SetHeader(metadata.Pairs("x-span-id", span.SpanContext().SpanID().String()))
		}

		return handler(srv, wrapped)
	}
}

// GRPCRateLimitUnary rate-limits unary requests.
func GRPCRateLimitUnary(limiter RateLimiterInterface) grpc.UnaryServerInterceptor {
	limiterNil := isNilRateLimiter(limiter)

	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if limiterNil {
			return handler(ctx, req)
		}
		if !limiter.Allow() {
			return nil, status.Error(codes.ResourceExhausted, "rate limit exceeded")
		}
		return handler(ctx, req)
	}
}

// GRPCRateLimitStream rate-limits streaming requests.
func GRPCRateLimitStream(limiter RateLimiterInterface) grpc.StreamServerInterceptor {
	limiterNil := isNilRateLimiter(limiter)

	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if limiterNil {
			return handler(srv, ss)
		}
		if !limiter.Allow() {
			return status.Error(codes.ResourceExhausted, "rate limit exceeded")
		}
		return handler(srv, ss)
	}
}

// GRPCRateLimitUnaryWithKey applies keyed unary rate limiting. When extractor is
// nil, DenseCloud uses the direct gRPC peer address. Proxy, tenant, or API-key
// rate limits must provide a custom extractor after that identity is trusted.
func GRPCRateLimitUnaryWithKey(limiter KeyedRateLimiter, extractor UnaryRateLimitKeyExtractor) grpc.UnaryServerInterceptor {
	limiterNil := isNilRateLimiter(limiter)

	if extractor == nil {
		extractor = func(ctx context.Context, _ any, info *grpc.UnaryServerInfo) string {
			return defaultGRPCKey(ctx, info.FullMethod)
		}
	}

	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if limiterNil {
			return handler(ctx, req)
		}
		key := extractor(ctx, req, info)
		if key == "" {
			key = defaultGRPCKey(ctx, info.FullMethod)
		}
		if !limiter.AllowKey(key) {
			return nil, status.Error(codes.ResourceExhausted, "rate limit exceeded")
		}
		return handler(ctx, req)
	}
}

// GRPCRateLimitStreamWithKey applies keyed stream rate limiting. When extractor
// is nil, DenseCloud uses the direct gRPC peer address. Proxy, tenant, or
// API-key rate limits must provide a custom extractor after that identity is
// trusted.
func GRPCRateLimitStreamWithKey(limiter KeyedRateLimiter, extractor StreamRateLimitKeyExtractor) grpc.StreamServerInterceptor {
	limiterNil := isNilRateLimiter(limiter)

	if extractor == nil {
		extractor = func(_ any, ss grpc.ServerStream, info *grpc.StreamServerInfo) string {
			return defaultGRPCKey(ss.Context(), info.FullMethod)
		}
	}

	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if limiterNil {
			return handler(srv, ss)
		}
		key := extractor(srv, ss, info)
		if key == "" {
			key = defaultGRPCKey(ss.Context(), info.FullMethod)
		}
		if !limiter.AllowKey(key) {
			return status.Error(codes.ResourceExhausted, "rate limit exceeded")
		}
		return handler(srv, ss)
	}
}

// GRPCCircuitBreakerUnary wraps unary requests in a circuit breaker.
func GRPCCircuitBreakerUnary(cfg CircuitBreakerConfig) grpc.UnaryServerInterceptor {
	cb := newGRPCCircuitBreaker(cfg)
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		var resp any
		_, err := cb.Execute(func() (int, error) {
			var handlerErr error
			resp, handlerErr = handler(ctx, req)
			if handlerErr != nil && isGRPCServerFailure(status.Code(handlerErr)) {
				return int(status.Code(handlerErr)), handlerErr
			}
			return int(codes.OK), handlerErr
		})
		if err != nil {
			if st, ok := status.FromError(err); ok && isGRPCServerFailure(st.Code()) {
				return nil, err
			}
			slog.Warn("grpc circuit breaker open", slog.String("method", info.FullMethod))
			return nil, status.Error(codes.Unavailable, "service temporarily unavailable")
		}
		return resp, nil
	}
}

// GRPCCircuitBreakerStream wraps stream requests in a circuit breaker.
func GRPCCircuitBreakerStream(cfg CircuitBreakerConfig) grpc.StreamServerInterceptor {
	cb := newGRPCCircuitBreaker(cfg)
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		_, err := cb.Execute(func() (int, error) {
			handlerErr := handler(srv, ss)
			if handlerErr != nil && isGRPCServerFailure(status.Code(handlerErr)) {
				return int(status.Code(handlerErr)), handlerErr
			}
			return int(codes.OK), handlerErr
		})
		if err != nil {
			if st, ok := status.FromError(err); ok && isGRPCServerFailure(st.Code()) {
				return err
			}
			slog.Warn("grpc circuit breaker open", slog.String("method", info.FullMethod))
			return status.Error(codes.Unavailable, "service temporarily unavailable")
		}
		return nil
	}
}

type wrappedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedServerStream) Context() context.Context {
	return w.ctx
}

type metadataCarrier metadata.MD

func metadataCarrierFromIncomingContext(ctx context.Context) propagation.TextMapCarrier {
	md, _ := metadata.FromIncomingContext(ctx)
	return metadataCarrier(md.Copy())
}

func (c metadataCarrier) Get(key string) string {
	values := metadata.MD(c).Get(key)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func (c metadataCarrier) Set(key, value string) {
	metadata.MD(c).Set(key, value)
}

func (c metadataCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for key := range c {
		keys = append(keys, key)
	}
	return keys
}

func grpcRequestID(ctx context.Context) string {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if values := md.Get("x-request-id"); len(values) > 0 && values[0] != "" {
			return values[0]
		}
	}
	return newRequestID()
}

func defaultGRPCKey(ctx context.Context, fullMethod string) string {
	if peerAddr := grpcPeerKey(ctx); peerAddr != "" {
		return peerAddr
	}
	return "grpc-global"
}

func grpcPeerKey(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok || p.Addr == nil {
		return ""
	}

	addr := strings.TrimSpace(p.Addr.String())
	if addr == "" {
		return ""
	}

	host, _, err := net.SplitHostPort(addr)
	if err == nil && host != "" {
		return host
	}

	return addr
}

func logGRPCCompletion(message string, ctx context.Context, fullMethod string, latency time.Duration, err error) {
	code := status.Code(err)
	attrs := []any{
		slog.String("request_id", GetRequestID(ctx)),
		slog.String("trace_id", GetTraceID(ctx)),
		slog.String("method", fullMethod),
		slog.String("grpc_code", code.String()),
		slog.Float64("latency_ms", float64(latency.Microseconds())/1000.0),
	}

	switch {
	case isGRPCServerFailure(code):
		slog.Error(message, attrs...)
	case code != codes.OK:
		slog.Warn(message, attrs...)
	default:
		slog.Info(message, attrs...)
	}
}

func newGRPCCircuitBreaker(cfg CircuitBreakerConfig) *gobreaker.CircuitBreaker[int] {
	cfg = normalizeCircuitBreakerConfig(cfg, "grpc")
	return gobreaker.NewCircuitBreaker[int](gobreaker.Settings{
		Name:        "grpc:" + cfg.Name,
		MaxRequests: cfg.MaxRequests,
		Interval:    cfg.Interval,
		Timeout:     cfg.Timeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= cfg.ReadyToTrip
		},
		OnStateChange: func(name string, from gobreaker.State, to gobreaker.State) {
			slog.Warn("grpc circuit breaker state change",
				slog.String("name", name),
				slog.String("from", from.String()),
				slog.String("to", to.String()),
			)
		},
	})
}

func isGRPCServerFailure(code codes.Code) bool {
	switch code {
	case codes.Internal, codes.Unavailable, codes.Unknown, codes.DataLoss, codes.DeadlineExceeded:
		return true
	default:
		return false
	}
}
