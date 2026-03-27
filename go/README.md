# DenseCloud Go Runtime

Shared cloud-native runtime for Dense product servers.

Module: `github.com/DenseAI/DenseCloud`  
Package imports use functional subpaths, for example:

- `github.com/DenseAI/DenseCloud/go/middleware`
- `github.com/DenseAI/DenseCloud/go/server`
- `github.com/DenseAI/DenseCloud/go/telemetry`

## Functional Directories

- `telemetry`: JSON structured logger initialization (`slog`) and shared HTTP/gRPC Prometheus exposition
- `middleware`: reusable HTTP/gRPC middleware and interceptor chain (recovery, request-id, timeout, logging, tracing, rate limit, circuit breaker)
- `server`: signal-aware HTTP/gRPC graceful runner, shared health registry, dependency-aware runtime assembly

## Minimal usage

```go
logger := telemetry.Init(telemetry.Config{
    ServiceName: "densediffusion",
    Version:     "v1.0.0",
    Level:       "info",
})
_ = logger

runtime, _ := server.NewHTTPRuntime(server.HTTPRuntimeConfig{
    ServiceName: "densediffusion",
    RootMiddleware: []func(http.Handler) http.Handler{
        middleware.Recovery(),
        middleware.RequestID(),
        middleware.RequestTimeout(120*time.Second),
        middleware.Logging(),
    },
})
runtime.APIMux().HandleFunc("/models", func(w http.ResponseWriter, r *http.Request) {})

httpServer := &http.Server{Addr: ":8080", Handler: runtime.Handler()}
runner, _ := server.NewRunner(server.Options{
    HTTPServer: httpServer,
    StartupHooks: []server.StartupHook{runtime.Startup},
    ShutdownHooks: []server.ShutdownHook{runtime.Shutdown},
})
_ = runner.RunBlocking(context.Background())
```

## Shared chassis helpers

- `server.HealthRegistry.RegisterDependency(...)` lets product runtimes wire Redis, worker, exporter readiness checks into DenseCloud-owned `/health*` phases without re-implementing probe handlers.
- `telemetry.NewGRPCMetrics(...)` can be appended to the shared `/metrics` endpoint through `HTTPMetricsConfig.Collectors` and populated by `middleware.GRPCMetricsUnary` / `middleware.GRPCMetricsStream`.

## Versioning

The runtime follows semantic versioning and is intended to be consumed by
`DenseCore`, `DenseDiffusion`, and future Dense workloads.
