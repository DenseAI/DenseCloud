package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/DenseAI/DenseCloud/go/server"
	"github.com/DenseAI/DenseCloud/go/telemetry"
)

func main() {
	telemetry.Init(telemetry.Config{ServiceName: "dense-example", Version: "v1.0.0", Level: "info"})

	runtime, err := server.NewHTTPRuntime(server.HTTPRuntimeConfig{
		ServiceName: "dense-example",
		RootMiddleware: server.DefaultHTTPMiddleware(server.HTTPMiddlewarePresetConfig{
			TracerName:     "dense-example",
			RequestTimeout: 15 * time.Second,
		}),
	})
	if err != nil {
		slog.Error("failed to create runtime", slog.String("error", err.Error()))
		return
	}
	runtime.APIMux().HandleFunc("/hello", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	httpServer := &http.Server{Addr: ":8080", Handler: runtime.Handler()}
	runner, err := server.NewRunner(server.Options{
		HTTPServer:   httpServer,
		StartupHooks: []server.StartupHook{runtime.Startup},
		ShutdownHooks: []server.ShutdownHook{
			runtime.Shutdown,
		},
	})
	if err != nil {
		slog.Error("failed to create runner", slog.String("error", err.Error()))
		return
	}
	if err := runner.RunBlocking(context.Background()); err != nil {
		slog.Error("server exited with error", slog.String("error", err.Error()))
	}
}
