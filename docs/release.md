# DenseCloud Release Guide

This guide covers the public release path for DenseCloud artifacts. DenseCloud
publishes source, a Go module, and the `dense-base` Helm library chart. The
repository Dockerfile builds a local or CI reference validation image only; it
is not a published DenseCloud release artifact.

## Public release flow

1. Complete the OSS checklist in `docs/oss-release-checklist.md`.
2. Ensure CI is green on the commit to be released.
3. Run the release gates locally or on the release branch when possible:
   - `go test ./...`
   - `go test -race ./go/server`
   - `go vet ./...`
   - `go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...`
   - `bash scripts/helm_matrix.sh`
   - `bash scripts/docker_smoke.sh`
   - `bash scripts/kind_smoke.sh`
4. Tag the repository root with a semantic version.
5. Verify the tagged Go module resolves with `GOPROXY=direct`.
6. Publish the Helm chart artifact and verify it is anonymously pullable from
   GHCR.
7. Publish a GitHub Release with upgrade notes and the verified chart archive.

If a tag-triggered run is interrupted by registry or mirror propagation, rerun
the workflow manually with the existing `vX.Y.Z` tag. Manual retries checkout
that immutable tag rather than the current branch.

## Go module release

1. Tag the repository root with the next semantic version after `v1.0.0`.
2. Publish repository and allow product repos to update:
   - module: `github.com/DenseAI/DenseCloud@vX.Y.Z`
   - package imports remain functional paths (for example `github.com/DenseAI/DenseCloud/go/server`)

## Helm library release

1. Package chart:

```bash
bash scripts/package_helm.sh /tmp/charts
```

2. Push to OCI chart registry (recommended):

```bash
helm push /tmp/charts/dense-base-1.1.0.tgz oci://ghcr.io/denseai/charts
```

3. Product chart dependency example:

```yaml
dependencies:
  - name: dense-base
    version: 1.1.0
    repository: oci://ghcr.io/denseai/charts
```

## Notes

- Root git tags version the Go module.
- Chart versions are managed in `charts/dense-base/Chart.yaml`.
- `scripts/package_helm.sh` includes the repository `LICENSE` and `NOTICE` in
  every chart archive.
- The Dockerfile reference image validates DenseCloud's shared health, metrics,
  and `/v1/hello` contracts in local and CI smoke runs only.
- Docker image publication belongs in downstream workload repositories that
  consume DenseCloud.
- cert-manager rotation support stops at chart resources and reloader wiring.
  Secret reload behavior and zero-downtime certificate qualification stay with
  the consuming product runtime.
- DenseCloud uses independent semantic versioning. Product repositories record
  the exact DenseCloud runtime and chart version they qualify; matching product
  and chassis version numbers is not a compatibility guarantee.
