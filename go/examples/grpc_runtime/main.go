package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/DenseAI/DenseCloud/go/middleware"
	"github.com/DenseAI/DenseCloud/go/server"
	"github.com/DenseAI/DenseCloud/go/telemetry"
)

func main() {
	grpcRuntime, err := server.NewGRPCRuntime(server.GRPCRuntimeConfig{
		ServiceName: "dense-consumer",
		Address:     ":9090",
		MiddlewarePreset: middleware.GRPCServerPresetConfig{
			TracerName: "dense-consumer",
		},
	})
	if err != nil {
		slog.Error("gRPC runtime setup failed", slog.String("error", err.Error()))
		return
	}

	// Register product protobuf services on grpcRuntime.Server() before serving.
	httpRuntime := server.MustNewHTTPRuntime(server.HTTPRuntimeConfig{
		ServiceName: "dense-consumer",
		MiddlewarePreset: &server.HTTPMiddlewarePresetConfig{
			TracerName:     "dense-consumer",
			RequestTimeout: 30 * time.Second,
		},
		MetricsCollectors: []telemetry.PrometheusCollector{grpcRuntime.Metrics()},
	})

	httpServer := &http.Server{
		Addr:              ":8080",
		Handler:           httpRuntime.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	runner, err := server.NewRunner(server.Options{
		HTTPServer:       httpServer,
		GRPCServer:       grpcRuntime,
		EnableGRPC:       true,
		StartupHooks:     []server.StartupHook{httpRuntime.Startup},
		PreShutdownHooks: []server.ShutdownHook{httpRuntime.BeginShutdown},
		ShutdownHooks:    []server.ShutdownHook{httpRuntime.Shutdown},
	})
	if err != nil {
		slog.Error("runner setup failed", slog.String("error", err.Error()))
		return
	}
	if err := runner.RunBlocking(context.Background()); err != nil {
		slog.Error("runner stopped", slog.String("error", err.Error()))
	}
}
