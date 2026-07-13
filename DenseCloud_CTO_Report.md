# DenseCloud CTO Report
## 2026-07-14 implementation audit

> 기준 저장소: `DenseCloud/`
>
> 기준 커밋: `01d46f6` (`Close DenseCloud's MVP operability gaps before release`)
>
> 실사 범위: `go/`, `charts/dense-base/`, `scripts/`, `.github/workflows/`
>
> 현재 판정: **저장소 구현 기준 MVP-ready, 공개 릴리스 갱신 전**

이 문서는 DenseCloud가 Dense Series의 공통 cloud-native chassis 역할을
실제로 수행하는지, MVP 구현이 어디까지 닫혔는지, 무엇이 아직 운영 환경
검증으로 남아 있는지를 코드와 실행된 검증 결과로 구분한다.

중요한 버전 경계가 있다. 현재 로컬 `main`의 구현은 `01d46f6`이지만,
공개 태그 `v1.0.0`과 `origin/main`은 `f1a63ff`를 가리킨다. 따라서 이
보고서의 MVP 판정은 현재 로컬 구현에 대한 것이며, 새 태그와 공개 배포가
완료됐다는 뜻은 아니다.

---

## 0. CTO Thesis

DenseCloud의 역할은 모델 추론 성능을 만드는 엔진이 아니다. DenseCore,
DenseDiffusion, DenseOps 및 다른 Dense 제품 서버가 같은 방식으로
기동되고, 준비 상태를 노출하고, 관측되고, 트래픽을 비운 뒤 종료되며,
Kubernetes에 패키징되도록 만드는 공통 서비스 운영 chassis다.

현재 구현은 다음 네 축을 하나의 저장소 계약으로 제공한다.

1. HTTP 및 gRPC 서버 조립과 lifecycle
2. health, metrics, tracing, logging과 middleware 순서
3. Helm 기반 Kubernetes 배포 계약
4. Docker와 kind를 통한 실제 실행 release gate

현재 코드 기준 결론은 다음과 같다.

> DenseCloud는 저장소 수준 MVP에 필요한 공통 operability chassis를
> 구현했다. 다만 실제 컨트롤러가 설치된 운영 클러스터에서의 동작과
> 제품별 채택 완료 여부는 별도 field qualification이 필요하다.

이 평가는 inference throughput, autoscaling 비용 효율, 모델 cold-start
시간을 증명하지 않는다. 그 책임은 각각 제품 runtime과 운영 환경에 있다.

---

## 1. Findings

### 1.1 남아 있던 repository-level MVP gap은 구현됐다

2026-07-14 기준으로 다음 갭이 닫혔다.

- DenseCloud-owned `GRPCRuntime` 추가
- HTTP canonical middleware preset의 `HTTPRuntime` 통합
- HTTP `/metrics`에 gRPC collector를 함께 노출하는 경로
- health check timeout, panic 격리, 중복 실행 억제
- readiness fail-close가 transport drain보다 먼저 실행되는 종료 순서
- startup 실패 rollback과 shutdown hook one-shot 보장
- 실제 Docker 컨테이너 endpoint smoke
- 실제 kind Deployment rollout 및 endpoint smoke
- release workflow에서 Go, Helm, Docker, kind gate 실행

따라서 현재의 주요 잔여 과제는 chassis 기능 발명이 아니라 공개 릴리스,
consumer migration, 실제 운영 컨트롤러 qualification이다.

### 1.2 HTTP와 gRPC 모두 기본 조립 경로가 있다

HTTP 진입점은 `server.NewHTTPRuntime(...)`이다.

- `/health*`, `/metrics`, API subrouter를 조립한다.
- `MiddlewarePreset`을 명시적으로 켜면 DenseCloud canonical root
  middleware 순서를 적용한다.
- `RootMiddleware`와 `APIMiddleware`에는 제품별 concern을 남긴다.
- `MetricsCollectors`를 통해 gRPC 등 추가 collector를 기본
  `/metrics` endpoint에 합친다.

gRPC 진입점은 `server.NewGRPCRuntime(...)`이다.

- canonical unary/stream interceptor preset을 조립한다.
- gRPC RED metrics를 소유한다.
- 표준 gRPC health service를 등록한다.
- 시작 전 `NOT_SERVING`, startup 성공 후 `SERVING`, 종료 시작 시
  fail-close를 보장한다.
- listener bind, startup rollback, graceful stop, deadline 시 force stop을
  하나의 lifecycle로 관리한다.

auth, CORS, body limit, TLS credential, service registration, business
middleware는 제품 저장소 책임으로 유지된다. 이 경계가 맞다.

### 1.3 health contract가 무한 대기와 process crash를 방어한다

`HealthRegistry`는 `live`, `ready`, `startup`을 분리하고 다음 안전장치를
적용한다.

- 기본 dependency check timeout: 2초
- runtime startup 전 readiness/startup fail-close
- shutdown 시작 후 readiness fail-close
- check panic을 process crash가 아닌 unavailable 결과로 변환
- context를 무시하는 check가 timeout된 뒤 계속 실행되더라도 같은 check를
  중복 실행하지 않음
- 이전 실행이 끝나면 다음 probe에서 정상적으로 재평가

timeout은 요청 응답 시간을 제한하지만, dependency callback이 context를
무시하면 그 callback 자체를 강제로 종료할 수는 없다. DenseCloud는 이
경우 중복 goroutine 누적을 막고 fail-close한다.

### 1.4 shutdown 순서가 request lifecycle과 일치한다

`Runner.Shutdown(ctx)`의 현재 순서는 다음과 같다.

1. `PreShutdownHooks`
2. HTTP `Shutdown(ctx)`로 in-flight request drain
3. gRPC context-aware `GracefulStop(ctx)`
4. deadline 또는 오류 시 gRPC force `Stop()`
5. `ShutdownHooks`

`HTTPRuntime.BeginShutdown`을 pre-shutdown hook으로 연결하면 신규 readiness
traffic을 먼저 차단한 뒤 기존 HTTP/gRPC 요청을 비운다. `HTTPRuntime`과
`GRPCRuntime`의 shutdown hook은 한 번만 실행된다.

### 1.5 release gate가 manifest 검증에서 runtime 검증으로 확장됐다

`scripts/docker_smoke.sh`는 reference image를 빌드하고 실제 컨테이너에서
다음을 검증한다.

- `/health/startup`
- `/health/live`
- `/health/ready`
- `/metrics`의 `densecloud_http_requests_total`
- `/v1/hello`의 정상 JSON 응답

`scripts/kind_smoke.sh`는 Helm values matrix apply에 더해 다음을 수행한다.

- 고유 이름의 임시 kind cluster 생성
- 로컬 reference image build 및 kind load
- minimal chart를 실제 Deployment로 rollout
- Service port-forward
- Docker smoke와 동일한 runtime endpoint 검증
- 자신이 만든 cluster, kubeconfig, 임시 파일만 정리

`SKIP_RUNTIME_SMOKE=true`는 manifest-only 진단용 escape hatch이며, 기본
release gate는 runtime smoke를 수행한다.

### 1.6 maturity classification

| Surface | 현재 판정 | 근거 | 남은 조건 |
| --- | --- | --- | --- |
| HTTP runtime | MVP-ready | assembly, health, metrics, lifecycle tests | consumer e2e adoption |
| gRPC runtime | MVP-ready | health RPC, metrics, rollback, timeout tests | product TLS/service e2e |
| Health lifecycle | MVP-ready | timeout, panic, overlap, recovery tests | real dependency fault injection |
| Graceful shutdown | MVP-ready | ordered hook and forced-stop tests | long-lived production stream drain |
| Helm chart contract | MVP-ready | render matrix, negative validation, kind apply | real controllers and CNI |
| Local/CI release gates | MVP-ready | Docker and kind runtime smoke | repeated CI history after publish |
| Public distribution | pending | release workflow exists | push, new tag, chart publication |
| Dense Series-wide adoption | partial/unverified | outside this repo audit | per-product migration evidence |

---

## 2. Actual Execution Path / Ownership Map

### 2.1 Shared HTTP runtime

Owner: [`go/server/runtime.go`](go/server/runtime.go)

`NewHTTPRuntime`의 실제 책임:

- root mux와 API mux 생성 또는 주입
- 기본 `/v1` API mount와 path validation
- health route 등록
- HTTP metrics와 추가 Prometheus collector 등록
- runtime extension route/middleware/hook 연결
- API 및 root middleware chain 조립
- startup hook 실패 시 shutdown rollback
- readiness fail-close와 shutdown hook one-shot 실행

canonical middleware preset은 명시적 opt-in이다. 기존 소비자가 이미
middleware chain을 구성한 경우의 중복 적용과 행동 변경을 피하기 위한
호환성 선택이다.

### 2.2 Shared gRPC runtime

Owner: [`go/server/grpc_runtime.go`](go/server/grpc_runtime.go)

`NewGRPCRuntime`의 실제 책임:

- 기본 address `:9090` 또는 주입 listener 사용
- DenseCloud gRPC interceptor preset 조립
- RED metrics collector 생성 또는 주입
- 표준 gRPC health service 등록
- startup/shutdown hook lifecycle
- lazy listener bind
- startup 오류 rollback과 error joining
- context-aware graceful stop과 timeout force stop

제품은 `runtime.Server()`에 protobuf service를 등록한다. 제품별
interceptor는 `MiddlewarePreset.ExtraUnaryInterceptors`와
`ExtraStreamInterceptors`에, TLS 같은 transport option은
`ServerOptions`에 제공한다.

### 2.3 Shared health lifecycle

Owner: [`go/server/health.go`](go/server/health.go)

표준 endpoint:

- `/health`
- `/health/live`
- `/health/ready`
- `/health/startup`

dependency는 `RegisterCheck` 또는 `RegisterDependency`로 하나 이상의
phase에 등록한다. 같은 check가 여러 phase에 등록되면 실행 상태를
공유하므로 timeout 후 중복 실행이 누적되지 않는다.

### 2.4 Shared runner

Owner: [`go/server/runner.go`](go/server/runner.go)

`Runner`는 signal 또는 context cancellation을 기다리며 HTTP와 optional
gRPC server를 함께 관리한다. product resource의 준비 해제와 cleanup은
각각 `PreShutdownHooks`, `ShutdownHooks`에 배치한다.

권장 조립은 다음과 같다.

- startup: `HTTPRuntime.Startup`
- pre-shutdown: `HTTPRuntime.BeginShutdown`
- HTTP drain: `http.Server.Shutdown`
- gRPC drain: `GRPCRuntime.GracefulStop`
- cleanup: `HTTPRuntime.Shutdown` 및 product hooks

실제 HTTP/gRPC 통합 예제는
[`go/examples/grpc_runtime/main.go`](go/examples/grpc_runtime/main.go)에 있다.

### 2.5 Middleware and telemetry

HTTP owner:

- [`go/middleware/http.go`](go/middleware/http.go)
- [`go/server/http_preset.go`](go/server/http_preset.go)

gRPC owner:

- [`go/middleware/grpc.go`](go/middleware/grpc.go)
- [`go/middleware/grpc_preset.go`](go/middleware/grpc_preset.go)

telemetry owner:

- [`go/telemetry/metrics.go`](go/telemetry/metrics.go)
- [`go/telemetry/grpc_metrics.go`](go/telemetry/grpc_metrics.go)
- [`go/telemetry/otel.go`](go/telemetry/otel.go)

DenseCloud는 request ID, recovery, timeout, tracing, logging과 HTTP/gRPC RED
metrics를 공통 concern으로 소유한다. panic completion도 관측한 뒤
re-panic하여 recovery 계층의 책임을 보존한다.

제품별 queue depth, active request, KV cache, prefill/decode, benchmark
metric은 DenseCloud concern이 아니다. 제품 runtime이 collector 또는
endpoint를 통해 제공해야 한다.

### 2.6 Helm and Kubernetes contract

Owner:

- [`charts/dense-base/templates/`](charts/dense-base/templates/)
- [`charts/dense-base/values.yaml`](charts/dense-base/values.yaml)
- [`charts/dense-base/examples/renderer/`](charts/dense-base/examples/renderer/)

chart가 소유하는 공통 계약:

- Deployment, Service, ServiceAccount
- liveness, readiness, startup probes
- HTTP/gRPC port와 ingress
- gRPC TLS/mTLS Secret mount와 cert-manager Certificate
- ServiceMonitor
- KEDA ScaledObject와 trigger validation
- PDB
- NetworkPolicy 및 ingress/monitoring/OTel presets
- model PVC, emptyDir, hostPath source
- security context, resources, scheduling constraints

KEDA trigger query와 metric 선택, node pool, model warming, sticky routing,
실제 certificate reload는 consumer와 SRE 책임이다.

### 2.7 Release ownership

Local gates:

- [`scripts/helm_matrix.sh`](scripts/helm_matrix.sh)
- [`scripts/docker_smoke.sh`](scripts/docker_smoke.sh)
- [`scripts/kind_smoke.sh`](scripts/kind_smoke.sh)

CI gates:

- [`.github/workflows/go-ci.yml`](.github/workflows/go-ci.yml)
- [`.github/workflows/docker-ci.yml`](.github/workflows/docker-ci.yml)
- [`.github/workflows/helm-ci.yml`](.github/workflows/helm-ci.yml)
- [`.github/workflows/release.yml`](.github/workflows/release.yml)

release workflow는 tag에 대해 검증 후 GitHub Release와 OCI Helm chart를
발행한다. 저장소 Dockerfile은 health/metrics/API 계약 검증용 reference
image이며 DenseCloud의 공개 workload image는 아니다.

---

## 3. Validation Evidence

### 3.1 2026-07-14 implementation validation

현재 MVP 구현에 대해 다음 검증이 통과했다.

- `go test ./...`
- `go vet ./...`
- `go test -race ./go/server`
- `bash scripts/helm_matrix.sh`
- `bash scripts/docker_smoke.sh`
- `bash scripts/kind_smoke.sh`
- `bash -n scripts/docker_smoke.sh scripts/kind_smoke.sh scripts/helm_matrix.sh`
- `git diff --cached --check`

Go tests는 HTTP/gRPC lifecycle, health transition, health RPC, metrics,
startup rollback, shutdown hook one-shot, timeout force stop, panic, timeout,
중복 health check 억제를 포함한다.

Docker smoke는 실제 컨테이너 endpoint를 검증했다. kind smoke는 values
matrix apply뿐 아니라 reference image의 Deployment rollout과 Service
endpoint를 검증했다.

### 3.2 Helm matrix

render/apply 대상:

- `minimal.yaml`
- `ingress-cert-manager.yaml`
- `grpc-cert-manager.yaml`
- `networkpolicy-ingress-nginx.yaml`
- `networkpolicy-monitoring.yaml`
- `networkpolicy-otel-collector.yaml`
- `networkpolicy-strict.yaml`
- `keda-custom-trigger.yaml`

negative gate:

- KEDA enabled 상태에서 trigger가 없으면 render 실패
- legacy placeholder query `vector(0)`가 render되면 실패

### 3.3 Validation boundary

다음은 위 검증으로 증명되지 않는다.

- 실제 cert-manager controller의 발급/갱신과 workload hot reload
- 실제 KEDA controller의 scaling behavior와 비용 효율
- CNI별 NetworkPolicy traffic enforcement
- 장시간 유지되는 production gRPC stream의 drain 특성
- DenseCore 등 제품별 inference 품질 또는 성능
- 모든 Dense Series consumer의 chassis migration 완료

---

## 4. Risks

1. **공개 릴리스가 현재 구현을 포함하지 않는다.** `v1.0.0`과
   `origin/main`은 `f1a63ff`이며 현재 로컬 구현보다 3커밋 뒤에 있다.
   새 구현을 배포하려면 push, version 결정, tag, release gate 통과가
   필요하다.
2. **controller-backed field evidence가 없다.** kind smoke는 placeholder
   CRD로 chart apply contract를 검증하지만 cert-manager, KEDA,
   Prometheus Operator의 실제 reconcile을 실행하지 않는다.
3. **Secret issuance와 reload는 별개다.** DenseCloud chart가 Certificate와
   Secret mount를 제공해도 consumer process의 zero-downtime reload는
   제품별로 검증해야 한다.
4. **NetworkPolicy는 CNI 의존적이다.** render/apply 성공은 실제 ingress,
   monitoring, OTel traffic enforcement를 증명하지 않는다.
5. **health callback은 협조적이어야 한다.** DenseCloud가 timeout과 중복
   실행은 통제하지만 context를 무시하는 외부 호출을 강제로 취소할 수는
   없다.
6. **consumer drift 위험이 남는다.** 제품 저장소가 bespoke boot path를
   유지하면 middleware 순서, probe semantics, shutdown behavior가 다시
   갈라질 수 있다.
7. **운영 경제성은 별도 책임이다.** 모델 cold-start, PVC throughput,
   node autoscaling latency와 product metric 품질은 DenseCloud만으로
   해결하거나 증명할 수 없다.

---

## 5. Release and Adoption Plan

### 5.1 Release closure

1. 현재 3개 로컬 커밋의 공개 포함 여부를 확정한다.
2. `main` push 후 CI의 Go, Docker, Helm, kind gate를 확인한다.
3. semantic version을 결정한다. 기존 `v1.0.0`을 이동하지 말고 새 tag를
   생성한다.
4. release workflow로 GitHub Release와 OCI Helm chart를 발행한다.
5. 소비자 저장소에서 새 Go module/chart version으로 upgrade한다.

### 5.2 Field qualification

1. 실제 cert-manager, KEDA, Prometheus Operator를 설치한 cluster에서
   reconcile을 검증한다.
2. 지원 CNI에서 NetworkPolicy allow/deny traffic test를 실행한다.
3. Secret rotation 중 consumer의 certificate reload와 연결 유지 여부를
   검증한다.
4. 장기 HTTP request와 gRPC stream이 종료 deadline 내 drain되는지
   검증한다.
5. 제품별 readiness dependency와 KEDA metric을 실제 workload에 연결한다.

### 5.3 Consumer adoption gate

각 Dense product에 대해 다음을 확인한다.

- `HTTPRuntime` 또는 동등한 DenseCloud chassis entrypoint 사용
- gRPC 사용 시 `GRPCRuntime` 또는 canonical preset 사용
- `BeginShutdown`이 transport drain 전에 연결됨
- `/health*`, `/metrics` 계약 유지
- `dense-base` chart dependency 사용
- 제품 metric과 KEDA trigger의 책임 경계 유지
- bespoke server bootstrap을 추가할 경우 명시적 사유와 regression test

---

## 6. Evidence vs Hypothesis

### 6.1 Evidence

현재 코드와 실행 결과로 확인된 사실:

- DenseCloud-owned HTTP 및 gRPC runtime assembly가 존재한다.
- HTTP/gRPC 모두 health, metrics, middleware, lifecycle 진입점이 있다.
- health check는 기본 timeout, panic 격리, 중복 실행 억제를 갖는다.
- readiness fail-close가 transport drain보다 먼저 실행될 수 있다.
- gRPC graceful stop은 deadline 시 force stop으로 종료된다.
- Docker와 kind smoke가 실제 process endpoint를 검증한다.
- Helm chart는 주요 dependency와 invalid combination을 fail-fast한다.
- release workflow는 Go, Helm, Docker, kind 검증 후 artifact를 발행한다.
- 현재 공개 태그는 최신 MVP 구현을 포함하지 않는다.

### 6.2 Hypothesis or unverified claim

추가 증거가 필요한 판단:

- 모든 Dense Series 제품이 DenseCloud chassis로 수렴했다.
- 실제 운영 controller 조합에서 chart preset이 동일하게 동작한다.
- KEDA metric이 각 inference workload에서 안정적인 scaling을 만든다.
- Secret rotation 중 모든 consumer가 무중단으로 certificate를 reload한다.
- DenseCore CPU inference의 비용 우위 또는 throughput 목표가 달성된다.

이 항목들은 DenseCloud 저장소 unit/smoke 결과만으로 완료라고 주장하면 안
된다.

---

## 7. Bottom Line

DenseCloud의 현재 상태를 가장 정확하게 표현하면 다음과 같다.

> 로컬 `01d46f6` 기준 DenseCloud는 shared cloud-native chassis의
> repository-level MVP를 구현했다. HTTP와 gRPC lifecycle, health,
> observability, graceful shutdown, Kubernetes packaging, executable release
> gate가 하나의 유지 가능한 경로로 연결돼 있다.

동시에 다음 표현은 아직 과장이다.

> DenseCloud가 전 제품과 모든 운영 환경에서 production-qualified 됐다.

남은 작업은 세 가지다.

1. 최신 구현을 새 버전으로 공개 배포
2. 실제 Kubernetes controller/CNI 환경 qualification
3. Dense Series consumer migration과 제품별 e2e 증거 축적

따라서 CTO 관점의 현재 판정은 다음과 같다.

- **MVP 구현:** 완료
- **저장소 검증:** 완료
- **공개 릴리스 갱신:** 미완료
- **운영 field qualification:** 부분적
- **전 제품 채택:** 부분적 또는 미검증

DenseCloud는 더 이상 scaffold가 아니다. 공통 chassis로 사용할 수 있는
실제 runtime과 release gate를 갖췄다. 다음 투자 우선순위는 새 abstraction
추가보다 공개 버전 정렬, consumer adoption, 운영 환경 증거 확보가 맞다.
