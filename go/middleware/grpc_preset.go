package middleware

import (
	"github.com/DenseAI/DenseCloud/go/telemetry"
	"google.golang.org/grpc"
)

// GRPCServerPresetConfig defines the DenseCloud-owned shared gRPC interceptor preset.
type GRPCServerPresetConfig struct {
	TracerName                  string
	Metrics                     *telemetry.GRPCMetrics
	ExtraUnaryInterceptors      []grpc.UnaryServerInterceptor
	ExtraStreamInterceptors     []grpc.StreamServerInterceptor
	CircuitBreaker              *CircuitBreakerConfig
	RateLimiter                 KeyedRateLimiter
	UnaryRateLimitKeyExtractor  UnaryRateLimitKeyExtractor
	StreamRateLimitKeyExtractor StreamRateLimitKeyExtractor
}

// GRPCServerPreset returns the canonical DenseCloud gRPC interceptor order.
// Product-specific interceptors such as auth should be supplied via Extra* fields.
func GRPCServerPreset(cfg GRPCServerPresetConfig) ([]grpc.UnaryServerInterceptor, []grpc.StreamServerInterceptor) {
	tracerName := cfg.TracerName
	if tracerName == "" {
		tracerName = "dense-grpc"
	}

	unary := []grpc.UnaryServerInterceptor{
		GRPCRequestIDUnary(),
		GRPCRecoveryUnary(),
		GRPCTracingUnary(tracerName),
		GRPCLoggingUnary(),
	}
	stream := []grpc.StreamServerInterceptor{
		GRPCRequestIDStream(),
		GRPCRecoveryStream(),
		GRPCTracingStream(tracerName),
		GRPCLoggingStream(),
	}

	unary = append(unary, GRPCMetricsUnary(cfg.Metrics))
	stream = append(stream, GRPCMetricsStream(cfg.Metrics))

	unary = append(unary, cfg.ExtraUnaryInterceptors...)
	stream = append(stream, cfg.ExtraStreamInterceptors...)

	if cfg.CircuitBreaker != nil {
		unary = append(unary, GRPCCircuitBreakerUnary(*cfg.CircuitBreaker))
		stream = append(stream, GRPCCircuitBreakerStream(*cfg.CircuitBreaker))
	}

	if !isNilRateLimiter(cfg.RateLimiter) {
		unary = append(unary, GRPCRateLimitUnaryWithKey(cfg.RateLimiter, cfg.UnaryRateLimitKeyExtractor))
		stream = append(stream, GRPCRateLimitStreamWithKey(cfg.RateLimiter, cfg.StreamRateLimitKeyExtractor))
	}

	return unary, stream
}
