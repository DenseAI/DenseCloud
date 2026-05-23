package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/DenseAI/DenseCloud/go/server"
)

func main() {
	runtime := server.MustNewHTTPRuntime(server.HTTPRuntimeConfig{
		ServiceName: "dense-consumer",
		RootMiddleware: server.DefaultHTTPMiddleware(server.HTTPMiddlewarePresetConfig{
			TracerName:     "dense-consumer",
			RequestTimeout: 30 * time.Second,
		}),
	})

	runtime.APIMux().HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	httpServer := &http.Server{
		Addr:              ":8080",
		Handler:           runtime.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	runner, err := server.NewRunner(server.Options{
		HTTPServer:    httpServer,
		StartupHooks:  []server.StartupHook{runtime.Startup},
		ShutdownHooks: []server.ShutdownHook{runtime.Shutdown},
	})
	if err != nil {
		slog.Error("runner setup failed", slog.String("error", err.Error()))
		return
	}
	if err := runner.RunBlocking(context.Background()); err != nil {
		slog.Error("runner stopped", slog.String("error", err.Error()))
	}
}
