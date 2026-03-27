# DenseCloud: Final Architecture & Implementation Report

**Document Owner:** Global Head Solution Architect (Senior Staff Engineer)
**Target Audience:** CTO, Engineering Leadership, SRE Teams
**Date:** 2026-03-19 (Last Synced with Codebase — shared runtime assembly, health/metrics, gRPC parity, cert-manager/NetworkPolicy hardening, kind apply-stage smoke evidence, DenseCore/DenseDiffusion/DenseOps consumer evidence, KEDA evidence boundary 반영)

---

## 1. Executive Summary & Architectural Vision

**"A Shared Cloud-Native Chassis for Global-Scale Dense Products"**

Dense Series 제품군이 공통된 서버 부트 경로와 운영 계약 위에서 움직일 수 있도록, **"Chassis Pattern (공통 뼈대 패턴)"** 기반의 `DenseCloud`를 Dense Series 공통 cloud-native chassis로 강화했습니다. 2026-03-19 기준 현재 코드에서 확인되는 직접 소비 경로는 DenseCore server, DenseDiffusion server, DenseOps API/control-plane이며, DenseBio/DenseVLA는 여전히 향후 마이그레이션 대상입니다. DenseCloud가 소유해야 하는 범위인 **HTTP/gRPC lifecycle, health, metrics, middleware/interceptor parity, Kubernetes packaging contract** 는 코드상 구현이 닫힌 상태이고, 남은 과제는 DenseOps 기능 추가가 아니라 잔여 consumer migration과 클러스터별 field validation 축적 성격이 더 강합니다. 특히 DenseDiffusion server는 shared `HTTPRuntime`/`Runner` 위에서 env-configurable keyed rate limiting, circuit breaker, request/body timeout, context-aware gRPC graceful stop 까지 DenseCloud 패턴으로 정렬됐습니다.

### 1.1 핵심 설계 철학 및 제약사항 분석 (Context & Constraints)
1. **관심사의 완벽한 분리 (Zero Domain Leakage):**
   - 각 도메인 레포지토리는 비즈니스 로직과 C++ 인퍼런스 엔진 최적화에만 집중력을 극대화하도록 격리했습니다.
   - `DenseCloud`는 서버 라이프사이클, 관측 가능성, 복원력, 쿠버네티스(K8s) 스켈레톤 등 순수 플랫폼 엔지니어링 도메인만 전담하며, CGO나 모델 관련 디펜던시를 일절 포함하지 않는 **순수 100% Go 상태**를 유지합니다.
   - Go 모듈 `github.com/DenseCore/DenseCloud` (Go 1.24+), 시맨틱 버저닝(`vX.Y.Z`)으로 전사에 배포됩니다.
2. **복잡도 제어 (O(1) Maintenance & PR Atomicity):**
   - DenseCloud가 직접 소유하는 Go 서버 보일러플레이트와 공통 K8s YAML 템플릿의 중복을 shared runtime/library chart로 회수했습니다.
   - 다만 모든 Dense 제품이 단일 Base Chart 소비로 완전히 수렴한 것은 아니며, 아직 남아 있는 consumer migration 범위는 별도 과제로 남습니다.
3. **분산 환경을 위한 스케일 인지 (Scale Awareness):**
   - 수백만 건의 트래픽을 처리하는 분산 환경을 전제로 설계되었으며 기본적으로 **Stateless** 구조를 지향합니다.

---

## 2. Architectural Strategy & Trade-offs (설계 전략 및 트레이드오프)

단순한 공통 모듈화가 아닌, 대규모 트래픽 하에서의 복원력을 보장하기 위한 아키텍처적 결정을 내렸습니다.

### 2.1 Resiliency & Fault Tolerance (복원력)
*   **Circuit Breaker (`go/middleware/circuitbreaker.go`):**
    *   **접근 방식:** 연속 5회 에러(`5xx`) 발생 시 회로를 차단(Open)하여 연쇄 장애(Cascading Failure)를 방어하는 `gobreaker/v2` 패턴을 채택했습니다.
    *   **Trade-off:** 타임아웃 30초, 하프-오픈 상태에서의 1회 테스트 요청 허용은 GPU/NPU 자원의 복구 시간을 감안한 보수적인 수치로 설정했습니다. `CircuitBreakerConfig` struct를 통해 `Name`, `MaxRequests`, `Interval`, `Timeout`, `ReadyToTrip` 파라미터를 도메인별로 커스터마이징할 수 있습니다.
    *   **상태 전이 관측:** `OnStateChange` 콜백에서 `slog.Warn` 구조화 로깅으로 서킷 상태 변화를 실시간 추적합니다.
*   **Rate Limiter (`go/middleware/ratelimit.go`, `go/middleware/ratelimit_redis.go`):**
    *   **접근 방식:** Token Bucket 알고리즘 기반의 In-Memory 구현과 **Redis-backed 분산 구현**을 모두 제공합니다.
    *   **In-Memory:** `NewRateLimiter(requestsPerSecond, burst)` 팩토리로 생성합니다. 단일 Pod 또는 Redis 미사용 환경에 적합합니다.
    *   **Redis 분산 Rate Limiter (`RedisRateLimiter`):** Lua Script 기반 원자 연산으로 멀티 Pod 환경에서 글로벌 한도를 일관되게 적용합니다. `AllowKey(key)` 메서드로 클라이언트 IP/API Key 등 키 기반 제한을 지원합니다.
    *   **Scale-out 대응:** Redis 장애 시 내장 Circuit Breaker를 통해 In-Memory Fallback으로 무중단 복원력을 확보합니다. 연속 3회 Redis 에러 발생 시 회로 차단(Open), 30초 후 Half-Open 상태에서 재시도합니다.
    *   **확장 인터페이스:** `RateLimiterInterface` 와 `KeyedRateLimiter` 를 분리해 전역 리미터와 keyed limiter를 모두 공통 미들웨어/인터셉터에 연결할 수 있게 했습니다.
    *   **Bootstrap fail-safe:** `NewRedisOrPartitionedRateLimiter()` 가 Redis bootstrap 실패 시 keyed in-memory limiter로 자동 폴백하여 제품별 부트 코드에 예외 처리 부담을 남기지 않습니다.
    *   **방어적 nil 처리:** `isNilRateLimiter()` 함수가 `reflect.ValueOf`를 활용하여 typed nil 포인터까지 안전하게 감지하며, nil인 경우 미들웨어를 비활성화하고 `sync.Once`로 단 1회 경고를 로깅합니다.

### 2.2 Observability First (관측 가능성 최우선)
*   **Structured Logging (`go/telemetry/logger.go`):**
    *   `log/slog` 기반의 완전한 JSON 구조화 로깅을 채택했습니다. `telemetry.Init()` 함수는 `Config{ServiceName, Version, Level, Output}` 설정을 받아 글로벌 default 로거를 초기화합니다.
    *   서비스 이름과 버전이 모든 로그 라인에 자동 첨부됩니다.
*   **OpenTelemetry 분산 추적 (`go/telemetry/otel.go`):**
    *   `OTelConfig` struct를 통해 `ServiceName`, `ServiceVersion`, `DeploymentEnvironment`, `Endpoint`, `SamplingRate`, `BatchTimeout` 등을 설정합니다.
    *   `DeploymentEnvironment`는 기본값이 빈 문자열이며, 설정 시 `deployment.environment` OTel 리소스 어트리뷰트로 전파됩니다. 이 동작은 `otel_test.go`에서 포함/제외 양면 테스트로 검증됩니다.
    *   `InitOTelTracer()` 함수가 OTLP gRPC Exporter, TraceIDRatioBased 샘플러, W3C TraceContext + Baggage 복합 프로파게이터를 초기화합니다.
    *   `OTelProvider` struct가 `Shutdown(ctx)` 및 `ShutdownWithTimeout(duration)` 양쪽 그레이스풀 종료를 지원합니다.
*   **트레이싱 미들웨어 (`go/middleware/tracing.go`):**
    *   `Tracing(tracerName)` 미들웨어가 W3C Trace Context를 추출/주입하고, 응답에 `X-Trace-ID`/`X-Span-ID` 헤더를 전파합니다.
    *   `GetTraceID(ctx)` / `GetSpanID(ctx)` 헬퍼 함수로 컨텍스트에서 트레이스 ID를 추출할 수 있으며, 활성 OTel 스팬이 없으면 `RequestID`로 폴백합니다.
    *   `InitOTelPropagator()` 유틸리티로 글로벌 W3C TraceContext 프로파게이터를 독립 설정할 수 있습니다.
*   **OTel HTTP 계측 (`go/middleware/otelhttp.go`):**
    *   `OTelHTTPMiddleware(serviceName)` 미들웨어가 `otelhttp.NewHandler`를 래핑하여 자동 스팬 생성과 메트릭 수집을 제공합니다.
    *   Health 엔드포인트(`/health`, `/health/live`, `/health/ready`, `/health/startup`)는 필터링하여 노이즈를 방지합니다.
    *   스팬 이름은 `"GET /v1/chat/completions"` 형태로 자동 포매팅됩니다.
*   **공통 HTTP/gRPC RED 메트릭 (`go/telemetry/metrics.go`, `go/telemetry/grpc_metrics.go`):**
    *   DenseCloud 자체가 `/metrics` 엔드포인트와 HTTP RED 메트릭(`requests_total`, `request_errors_total`, `request_duration_seconds`, `in_flight_requests`)을 Prometheus text format으로 노출합니다.
    *   `GRPCMetrics` collector를 통해 gRPC의 `requests_total`, `request_errors_total`, `request_duration_seconds`, `in_flight_requests`도 같은 `/metrics` contract에 합류할 수 있습니다.
    *   `/health*` 와 `/metrics` 는 기본 ignore path 로 처리되어 autoscaling/scrape noise를 줄입니다.
*   **요청 로깅 (`go/middleware/http.go` — `Logging()`):**
    *   RequestID, HTTP method, path, status code, latency(ms), client IP (`X-Forwarded-For` 파싱), user agent, bytes written을 구조화된 JSON으로 기록합니다.
    *   상태 코드에 따라 `slog.Info`/`slog.Warn`/`slog.Error` 레벨을 자동 분류합니다.

### 2.3 Defensive Coding & Streaming Safety (방어적 프로그래밍)
*   **Graceful Shutdown (`go/server/runner.go`):**
    *   OS 시그널(`SIGINT`, `SIGTERM`)을 감지하여 HTTP/gRPC 서버와 백그라운드 워커를 순차적이고 안전하게 종료시키는 Runner 모델을 설계했습니다. 진행 중인 인퍼런스 요청의 유실을 방지합니다.
    *   `Options.StartupHooks` / `Options.ShutdownHooks` 를 통해 runtime startup, health warmup, 확장 모듈 초기화 및 종료를 공통 lifecycle에 연결합니다.
    *   gRPC 구현체가 `GracefulGRPCServer` 를 제공하면 context-aware graceful stop 을 우선 사용하고, 실패 시 강제 stop 으로 폴백합니다.
    *   `mergeRunAndShutdownError()` 로직이 `context.Canceled`(정상 종료)와 실제 에러를 구분하여 `errors.Join`으로 결합합니다. `runner_test.go`에서 6개 에러 조합 시나리오를 테이블 드리븐 테스트로 검증합니다.
*   **Runtime Assembly & Health Registry (`go/server/runtime.go`, `go/server/health.go`):**
    *   `HTTPRuntime` 이 `/v1` API subrouter, shared middleware, metrics wiring, health endpoints, registered runtime extensions 를 한 번에 조립합니다.
    *   `HealthRegistry` 가 `/health`, `/health/live`, `/health/ready`, `/health/startup` 계약을 DenseCloud가 직접 소유하도록 하며, startup 완료 전과 shutdown 중에는 readiness 를 fail-close 합니다.
    *   `RegisterDependency()` helper로 Redis, worker, exporter 같은 외부 의존성을 DenseCloud-owned probe phase에 쉽게 연결할 수 있습니다.
*   **gRPC Interceptor Parity (`go/middleware/grpc.go`):**
    *   HTTP와 동일한 request-id, recovery, logging, tracing, rate-limit, circuit-breaker 패턴을 unary/stream interceptor 양쪽에 제공하여 제품별 bespoke bootstrap 중복을 줄였습니다.
*   **Streaming Middleware Wrapping (`go/middleware/http.go`):**
    *   리스폰스 래핑 과정에서 발생할 수 있는 스트리밍 버그(예: SSE 붕괴)를 방어하기 위해 `responseWriter` struct가 다음 5개 인터페이스를 명시적으로 위임(Delegate)합니다:
        *   `http.Flusher` — SSE/스트리밍 응답에 필수
        *   `http.Hijacker` — WebSocket 업그레이드에 필수
        *   `http.Pusher` — HTTP/2 Server Push
        *   `io.ReaderFrom` — `sendfile(2)` 최적화 경로 보존
        *   `http.ResponseWriter` — 임베딩으로 기본 위임
    *   회귀 테스트(`http_test.go`)에서 `TestChainPreservesFlusher`와 `TestCircuitBreakerPreservesFlusher` 두 경로 모두 Flusher 위임을 검증합니다.

---

## 3. Implementation Status (구현 상태)

DenseCloud가 소유해야 하는 공통 cloud-native chassis 범위는 현재 다음 축으로 정리됩니다.

### 3.1 Shared Go Runtime (`go/*`)

| Package | 파일 | 설명 |
|---------|------|------|
| `middleware` | `http.go` | RequestID, RequestTimeout, Recovery, CORS, MaxBodySize, ContentType, Logging, Chain |
| `middleware` | `circuitbreaker.go` | 기본값 정규화를 포함한 HTTP 서킷 브레이커 |
| `middleware` | `ratelimit.go` | 전역/키 기반 HTTP rate limit 미들웨어 |
| `middleware` | `ratelimit_partitioned.go` | keyed in-memory rate limiter |
| `middleware` | `ratelimit_redis.go` | Redis keyed rate limiter + bootstrap/runtime fallback |
| `middleware` | `grpc.go` | unary/stream gRPC interceptor parity + shared gRPC metrics interceptors |
| `middleware` | `tracing.go`, `otelhttp.go` | HTTP tracing 및 otelhttp instrumentation |
| `server` | `runner.go` | startup/shutdown hook, graceful gRPC stop 지원 런타임 |
| `server` | `health.go` | `/health`, `/health/live`, `/health/ready`, `/health/startup` 계약 |
| `server` | `runtime.go` | `/v1` API subrouter, metrics, health, RuntimeExtension 조립 |
| `telemetry` | `logger.go`, `otel.go` | structured logging + OTLP tracing |
| `telemetry` | `metrics.go`, `grpc_metrics.go` | `/metrics` Prometheus text format + HTTP/gRPC RED metrics |

핵심 판정:

- DenseCloud는 더 이상 HTTP helper 모음이 아니라, **shared runtime assembly** 를 실제 코드로 소유합니다.
- 제품별 서버는 DenseCloud의 `HTTPRuntime` 과 `Runner` 위에서 business route만 연결하면 되도록 경계가 정리됐습니다.
- health/metrics/request lifecycle 계약이 제품 repo에서 DenseCloud로 회수됐습니다.

검증 상태:

- `go test ./...` 통과
- `go vet ./...` 통과
- health/runtime/metrics/gRPC/rate-limit 신규 테스트가 추가돼 regression surface가 넓어졌습니다.
- 2026-03-19 DenseCore consumer evidence: `DenseCore/server/internal/server/server.go` 는 shared `HTTPRuntime`/`Runner` 를 사용하고, DenseCloud `HealthRegistry` 및 shared `/metrics` collector를 서버 부트 경로에 연결합니다.
- 2026-03-19 DenseDiffusion consumer evidence: `DenseDiffusion/server` 는 로컬 workspace `replace ../../DenseCloud` 기준으로 `go test ./...` 가 다시 green 이고, shared `HTTPRuntime`/`Runner` 위에서 HTTP keyed rate limit, HTTP/gRPC circuit breaker, env-configurable timeout/body-limit, context-aware gRPC graceful stop 을 소비합니다.
- 2026-03-19 DenseOps consumer evidence: `cmd/denseops-api`, `cmd/denseops-control-plane` 모두 shared `HTTPRuntime`/`Runner` 위에서 `/health*`, `/metrics`, graceful shutdown 계약을 소비합니다.

### 3.2 Shared Helm Library Chart (`charts/dense-base` v1.0.0)

`dense-base` 는 이제 단순 Deployment skeleton이 아니라, DenseCloud의 운영 계약을 K8s 배포 레벨까지 강제하는 라이브러리 차트입니다.

주요 템플릿:

- `_deployment.tpl`: probe, TLS secret mount, secret reload annotation contract, security defaults
- `_service.tpl`, `_grpc-service.tpl`: HTTP/gRPC 서비스 노출
- `_ingress.tpl`, `_grpc-ingress.tpl`: ingress + cert-manager annotation contract
- `_grpc-certificate.tpl`: direct gRPC TLS secret을 위한 cert-manager `Certificate`
- `_networkpolicy.tpl`: opt-in deny-by-default NetworkPolicy
- `_servicemonitor.tpl`, `_keda.tpl`, `_pdb.tpl`: observability / autoscaling / disruption budget
- `_validate.tpl`: TLS, cert-manager, KEDA, ServiceMonitor, NetworkPolicy 관련 fail-fast 검증

핵심 판정:

- cert-manager 경로는 ingress shim에만 기대지 않고, gRPC direct secret mount까지 DenseCloud 차트가 계약으로 소유합니다.
- NetworkPolicy는 optional hardening 이지만, 켜는 순간 allow rule이 없으면 자연스럽게 deny-by-default posture 로 동작합니다.
- NetworkPolicy 예시는 단일 strict 샘플 하나에 머물지 않고, `ingress-nginx` ingress 허용, Prometheus scrape 허용, OTel collector egress 허용, 그리고 이 셋을 합친 composite strict baseline 으로 분해해 설명할 수 있는 수준까지 정리됐습니다.
- cert-manager secret rotation 은 "cert-manager가 Secret을 갱신한다"와 "Pod/프로세스가 새 Secret을 실제 사용 상태로 전환한다"를 분리해서 봐야 하며, DenseCloud는 후자에 대해 `grpc.tls.secretReloadAnnotations` 를 통해 reloader 계열 controller 와 연결되는 contract 를 제공합니다.
- placeholder autoscaling query를 제거하고 explicit KEDA trigger contract를 강제했습니다.

### 3.3 Render Harness / Value Matrix

DenseCloud는 이제 문서 설명만이 아니라 재현 가능한 render harness 를 함께 제공합니다.

- `charts/dense-base/examples/renderer`: 로컬 consumer chart wrapper
- `values/minimal.yaml`: 최소 서비스 skeleton
- `values/ingress-cert-manager.yaml`: ingress + cert-manager contract
- `values/grpc-cert-manager.yaml`: gRPC service/TLS/cert-manager contract
- `values/networkpolicy-ingress-nginx.yaml`: ingress controller 허용 preset
- `values/networkpolicy-monitoring.yaml`: Prometheus/ServiceMonitor scrape 허용 preset
- `values/networkpolicy-otel-collector.yaml`: OTel collector egress 허용 preset
- `values/networkpolicy-strict.yaml`: ingress + monitoring + OTel collector를 합친 strict NetworkPolicy hardening baseline

이 값 세트는 DenseOps 기능과 무관하며, DenseCloud 차트의 **공통 packaging contract** 검증용입니다.

추가된 검증 자산:

- `scripts/helm_matrix.sh`: lint + renderer values 7종 템플릿 검증
- `scripts/kind_smoke.sh`: placeholder CRD(`Certificate`, `ServiceMonitor`) 설치 후 renderer values 7종을 namespace 분리해 실제 API server `kubectl apply` 단계까지 검증
- `.github/workflows/helm-ci.yml`: lint/package에 renderer values matrix 7종 렌더링을 추가
- 현재 matrix/cluster apply evidence는 cert-manager, ServiceMonitor, NetworkPolicy preset 중심이며, KEDA는 `_keda.tpl` + `_validate.tpl` 수준의 chart contract/fail-fast validation 이 구현돼 있지만 전용 values preset이나 `ScaledObject` CRD apply-stage smoke는 아직 포함하지 않습니다.

### 3.4 Ownership Boundary

DenseCloud는 아래만 소유합니다.

- HTTP/gRPC lifecycle
- health, metrics, request/trace contract
- middleware/interceptor parity
- Kubernetes packaging skeleton
- RuntimeExtension 같은 OSS-friendly 확장 포인트

DenseCloud가 소유하지 않는 것:

- DenseOps UI/API, rollout orchestration, release catalog, diagnostics bundle
- auth/license/quota/audit/feature gate (product-specific)

즉 DenseCloud는 control plane이 아니라 **공통 serving chassis** 입니다.

---

## 4. Dependency Graph (외부 의존성)

| 의존성 | 버전 | 용도 |
|--------|------|------|
| `redis/go-redis/v9` | v9.7.0 | Redis keyed rate limiter |
| `sony/gobreaker/v2` | v2.1.0 | HTTP/gRPC circuit breaker |
| `otel/otel` | v1.39.0 | OpenTelemetry Core |
| `otel/otlptrace/otlptracegrpc` | v1.39.0 | OTLP gRPC Exporter |
| `otel/sdk` | v1.39.0 | TracerProvider |
| `otelhttp` | v0.64.0 | HTTP 계측 미들웨어 |
| `google.golang.org/grpc` | v1.77.0 | gRPC interceptor/runtime surface |

---

## 5. Synthesis & Handover (최종 평가 및 넥스트 스텝)

**결론 (CTO Summary):**
DenseCloud는 현재 Dense Series의 공통 cloud-native chassis 로서 필요한 핵심 primitive 를 갖췄습니다. health, metrics, gRPC parity, keyed rate limiting, cert-manager, NetworkPolicy, render/apply evidence 중심 packaging contract 까지 포함해 **DenseCloud-owned scope 기준으로는 production-ready** 라고 볼 수 있습니다. 다만 이 표현은 KEDA까지 포함한 모든 chart path가 동일한 수준의 apply-stage field evidence를 갖췄다는 뜻은 아니며, KEDA는 현재 chart contract와 fail-fast validation이 닫힌 상태로 보는 것이 정확합니다. 소비 제품 측에서는 DenseCore server, DenseDiffusion server, DenseOps API/control-plane가 DenseCloud 부트 경로를 실제로 사용하고 있고, DenseBio/DenseVLA는 여전히 migration 대상입니다.

### 구현 완료 항목 ✅

- [x] shared HTTP runtime assembly (`HTTPRuntime`)
- [x] shared health registry (`/health`, `/health/live`, `/health/ready`, `/health/startup`)
- [x] shared `/metrics` RED metrics exposition
- [x] shared gRPC RED metrics collector/interceptor
- [x] HTTP/gRPC request lifecycle parity
- [x] keyed rate limiting + Redis bootstrap/runtime fallback
- [x] RuntimeExtension startup/shutdown wiring
- [x] cert-manager-friendly gRPC TLS contract
- [x] opt-in NetworkPolicy hardening
- [x] controller-specific NetworkPolicy preset examples (ingress controller / monitoring / OTel collector / composite strict)
- [x] cert-manager secret rotation qualification boundary documented (`secretReloadAnnotations` + controller-specific ownership)
- [x] render harness + canonical `helm template` value matrix
- [x] Helm lint/render automation script and CI matrix
- [x] kind apply-stage smoke verification across renderer values matrix
- [x] DenseOps와 겹치지 않는 ownership boundary 명시
- [x] DenseCore shared `HTTPRuntime`/`Runner` + health lifecycle 회수
- [x] DenseDiffusion shared `HTTPRuntime`/`Runner` + gRPC metrics/interceptor 회수
- [x] DenseDiffusion consumer-side HTTP keyed rate limit / HTTP+gRPC circuit breaker / timeout-body hardening / graceful gRPC stop 정렬
- [x] DenseOps API/control-plane shared `HTTPRuntime`/`Runner` consumer evidence

### 남은 과제 (DenseCloud 범위 내)

- DenseBio / DenseVLA consumer migration을 통한 field evidence 축적
- controller-specific live secret rotation field evidence 축적 (예: Stakater Reloader 실제 재기동, ingress controller 별 secret watch 동작)
- privileged CI runner 또는 host clock 안정성이 보장된 환경으로 kind apply-stage smoke를 상시 자동화
- KEDA `ScaledObject` 경로를 위한 values preset + apply-stage smoke evidence 추가

### 제외 범위 재확인

- DenseOps control plane 기능은 DenseCloud backlog 가 아닙니다.
- Product-specific enforcement/security/compliance 기능도 DenseCloud backlog 가 아닙니다.

DenseCloud의 다음 단계는 기능 발명이 아니라, 이 공통 chassis 를 Dense Series 전 제품에 일관되게 이식하고 운영 증적을 쌓는 것입니다.
