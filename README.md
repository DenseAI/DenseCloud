# DenseCloud

[![Go Report Card](https://goreportcard.com/badge/github.com/DenseAI/DenseCloud)](https://goreportcard.com/report/github.com/DenseAI/DenseCloud)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Release](https://img.shields.io/github/v/release/DenseAI/DenseCloud)](https://github.com/DenseAI/DenseCloud/releases)

Shared cloud-native serving chassis for Dense product lines.

DenseCloud packages the reusable serving pieces that Dense workloads share:

- Go runtime and middleware/interceptors for HTTP/gRPC services
- health, readiness, startup, metrics, tracing, and graceful shutdown contracts
- Helm library chart for Kubernetes deployment scaffolding
- Reference release and migration documentation

This repository is intended to be consumed as a public building block rather
than deployed directly as a standalone product service.

## Scope

- Shared Go runtime (`go`) with functional directories (`middleware`, `server`, `telemetry`)
- Shared Helm library chart (`charts/dense-base`)
- Migration documentation for DenseCore, DenseDiffusion, and future services

DenseCloud owns only reusable chassis concerns:

- HTTP/gRPC service lifecycle, health, startup/readiness/liveness probes,
  metrics, tracing, graceful shutdown, and middleware/interceptor parity
- Kubernetes deployment skeletons and validation
- Extension points for OSS runtime modules

DenseCloud is not a control plane. DenseOps owns UI/API surfaces, rollout
orchestration, desired/observed state, node operations, benchmark gates, and
operations workflows. DenseEnterprise owns auth, license, quota, audit,
feature-gate, attestation, and enterprise policy enforcement. DenseCloud must
stay domain-neutral and contains no DenseCore, DenseDiffusion, DenseOps,
DenseEnterprise, model, CGO, or inference-specific business logic.

## Versioning

- Go module: `github.com/DenseAI/DenseCloud` (root semantic tags `vX.Y.Z`)
- Package imports: `github.com/DenseAI/DenseCloud/go/...`
- Helm chart: `dense-base` published as an OCI chart

DenseCloud versions the shared chassis API independently from product release
numbers. Product compatibility is qualified explicitly instead of inferred from
matching version numbers. The first public DenseCore MVP target is:

| Product | DenseCloud runtime/chart | Qualification |
| --- | --- | --- |
| DenseCore `v0.1.0` | DenseCloud `v1.1.0` | local chart integration passed; public artifact pending |

The row becomes qualified only after the DenseCloud public artifacts resolve
and the DenseCore consumer chart and serving smoke gates pass against them.

## Installation

### Go runtime

```bash
go get github.com/DenseAI/DenseCloud@v1.1.0
```

Example imports:

- `github.com/DenseAI/DenseCloud/go/middleware`
- `github.com/DenseAI/DenseCloud/go/server`
- `github.com/DenseAI/DenseCloud/go/telemetry`

Consumer repositories should keep this canonical module path in committed
`go.mod` files. Local multi-repo development should use a workspace instead of
committing `replace` directives:

```bash
cd /path/to/workspace
go work init ./DenseCloud ./DenseOps ./DenseEnterprise
go work use ./DenseCloud ./DenseOps ./DenseEnterprise
```

For one consumer repo:

```bash
cd /path/to/DenseOps
go work init . ../DenseCloud
go work use . ../DenseCloud
```

Release builds should run from a clean module view, or with `GOWORK=off`, so
that the published dependency resolves through
`github.com/DenseAI/DenseCloud@vX.Y.Z` rather than a local checkout.

### Helm library chart

In a consumer chart:

```yaml
dependencies:
  - name: dense-base
    version: 1.1.0
    repository: oci://ghcr.io/denseai/charts
```

### Local Reference Runtime Image

The repository Dockerfile builds a local or CI-only reference image from the
minimal example:

```bash
docker build -t densecloud-local-reference:dev .
bash scripts/docker_smoke.sh
```

This image exists to validate DenseCloud's shared runtime contracts in smoke
tests. It is not a published DenseCloud release artifact and it is not the
consumer-facing runtime image for Dense products. Downstream product
repositories publish and qualify their own workload images.


## Release Artifacts

- Source of truth: GitHub repository
- Go packages: semantic version tags on the repository root
- Helm chart: OCI artifact for `charts/dense-base`
- Dockerfile image: local/CI reference validation image only

Docker images are expected to be published by consumer application
repositories, not by DenseCloud itself.

## Release Gates

DenseCloud releases are gated on the same validations run in CI:

- `go test ./...`
- `go test -race ./go/server`
- `go vet ./...`
- `go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...`
- `bash scripts/helm_matrix.sh`
- `bash scripts/docker_smoke.sh`
- `bash scripts/kind_smoke.sh`

## Design rules

- No C++/CGO/model-domain logic in shared runtime packages
- Domain repos own business APIs and inference behavior
- Shared runtime owns lifecycle, observability, deployment skeletons
- KEDA requires explicit product-provided triggers; DenseCloud intentionally
  provides no placeholder autoscaling query because it cannot know a product's
  correct workload metric.
- OpenTelemetry's default insecure local endpoint is for developer loops only.
  Production deployments must configure a real collector endpoint and TLS or a
  product-approved transport boundary.
- cert-manager Secret rotation support is a chart contract and reloader
  integration point. Secret issuance belongs to DenseCloud's chart boundary;
  actual zero-downtime certificate hot reload and process reload qualification
  remain product responsibilities.

## Repository layout

- `go`: reusable Go module
- `charts/dense-base`: Helm library chart
- `docs`: migration and operations notes

## Support Boundary

DenseCloud owns shared platform concerns only:

- server lifecycle and graceful shutdown
- observability, middleware, and health contracts
- common Kubernetes deployment primitives

Consumer application behavior, model logic, product APIs, and business
policies remain in product-specific repositories.

## Open Source

- License: Apache 2.0 ([LICENSE](LICENSE))
- Third-party notices: [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)
- Security reporting: see `SECURITY.md`
- Contribution process: see `CONTRIBUTING.md`
- Release process: see `docs/release.md`
