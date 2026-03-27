# DenseCloud

[![Go Report Card](https://goreportcard.com/badge/github.com/DenseAI/DenseCloud)](https://goreportcard.com/report/github.com/DenseAI/DenseCloud)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)
[![Release](https://img.shields.io/github/v/release/DenseAI/DenseCloud)](https://github.com/DenseAI/DenseCloud/releases)

Shared cloud-native platform repository for Dense product lines.

DenseCloud packages the reusable platform pieces that Dense workloads share:

- Go runtime and middleware for HTTP/gRPC services
- Helm library chart for Kubernetes deployment scaffolding
- Reference release and migration documentation

This repository is intended to be consumed as a public building block rather
than deployed directly as a standalone product service.

## Scope

- Shared Go runtime (`go`) with functional directories (`middleware`, `server`, `telemetry`)
- Shared Helm library chart (`charts/dense-base`)
- Migration documentation for DenseCore, DenseDiffusion, and future services

DenseCloud owns only reusable chassis concerns:

- HTTP/gRPC service lifecycle, health, metrics, middleware/interceptor parity
- Kubernetes deployment skeletons and validation
- Extension points for OSS runtime modules

DenseCloud does not own product-specific control-plane behavior or policy/enforcement flows.

## Versioning

- Go module: `github.com/DenseAI/DenseCloud` (root semantic tags `vX.Y.Z`)
- Package imports: `github.com/DenseAI/DenseCloud/go/...`
- Helm chart: `dense-base` published as an OCI chart

## Installation

### Go runtime

```bash
go get github.com/DenseAI/DenseCloud@v1.0.0
```

Example imports:

- `github.com/DenseAI/DenseCloud/go/middleware`
- `github.com/DenseAI/DenseCloud/go/server`
- `github.com/DenseAI/DenseCloud/go/telemetry`

### Helm library chart

In a consumer chart:

```yaml
dependencies:
  - name: dense-base
    version: 1.0.0
    repository: oci://ghcr.io/DenseAI/charts
```

## Release Artifacts

- Source of truth: GitHub repository
- Go packages: semantic version tags on the repository root
- Helm chart: OCI artifact for `charts/dense-base`

Docker images are expected to be published by consumer application
repositories, not by DenseCloud itself.

## Design rules

- No C++/CGO/model-domain logic in shared runtime packages
- Domain repos own business APIs and inference behavior
- Shared runtime owns lifecycle, observability, deployment skeletons

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
- Security reporting: see `SECURITY.md`
- Contribution process: see `CONTRIBUTING.md`
- Release process: see `docs/release.md`
