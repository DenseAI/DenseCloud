package main

import (
	"github.com/DenseAI/DenseCloud/go/middleware"
	"github.com/DenseAI/DenseCloud/go/telemetry"
	"google.golang.org/grpc"
)

func main() {
	grpcMetrics := telemetry.NewGRPCMetrics(telemetry.GRPCMetricsConfig{
		ServiceName: "dense-consumer-grpc",
	})
	unary, stream := middleware.GRPCServerPreset(middleware.GRPCServerPresetConfig{
		TracerName: "dense-consumer-grpc",
		Metrics:    grpcMetrics,
	})

	_ = grpc.NewServer(
		grpc.ChainUnaryInterceptor(unary...),
		grpc.ChainStreamInterceptor(stream...),
	)
}
