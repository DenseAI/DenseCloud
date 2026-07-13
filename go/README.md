# DenseCloud Go Runtime

Shared cloud-native runtime for Dense product servers.

Module: `github.com/DenseAI/DenseCloud`  
Package imports use functional subpaths, for example:

- `github.com/DenseAI/DenseCloud/go/middleware`
- `github.com/DenseAI/DenseCloud/go/server`
- `github.com/DenseAI/DenseCloud/go/telemetry`

Consumer modules should import this canonical path directly and avoid
committing local `replace` directives. For sibling-repo development, create a
local `go.work` file instead:

```bash
go work init ./DenseCloud ./DenseOps ./DenseEnterprise
go work use ./DenseCloud ./DenseOps ./DenseEnterprise
```

Run release validation with `GOWORK=off` when you need to prove the published
module graph is independent of local checkouts.

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
    RootMiddleware: server.DefaultHTTPMiddleware(server.HTTPMiddlewarePresetConfig{
        TracerName:     "densediffusion",
        RequestTimeout: 120*time.Second,
    }),
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
- `server.DefaultHTTPMiddleware(...)` provides the canonical DenseCloud root HTTP middleware order for shared chassis concerns.
- `middleware.GRPCServerPreset(...)` provides the canonical gRPC interceptor bundle while leaving auth and business interceptors to product repos.
- `server.NewHTTPRuntime(...)` bounds default HTTP metric cardinality by collapsing API subpaths such as `/v1/models/abc` to `/v1/*`. Consumers that need route-level labels can set `HTTPRuntimeConfig.MetricsPathLabeler`.
- `telemetry.NewGRPCMetrics(...)` can be appended to the shared `/metrics` endpoint through `HTTPMetricsConfig.Collectors` and populated by `middleware.GRPCMetricsUnary` / `middleware.GRPCMetricsStream`.

## Versioning

The runtime follows semantic versioning and is intended to be consumed by
`DenseCore`, `DenseDiffusion`, and future Dense workloads.
