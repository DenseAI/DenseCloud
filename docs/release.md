# DenseCloud Release Guide

This guide covers the public release path for DenseCloud artifacts. DenseCloud
publishes source, a Go module, and the `dense-base` Helm library chart. It does
not publish a standalone product runtime image.

## Public release flow

1. Complete the OSS checklist in `docs/oss-release-checklist.md`.
2. Ensure CI is green on the commit to be released.
3. Tag the repository root with a semantic version.
4. Publish a GitHub Release with upgrade notes.
5. Publish the Helm chart artifact.

## Go module release

1. Tag repository root with semantic version (`v0.1.0`, `v0.2.0`, ...).
2. Publish repository and allow product repos to update:
   - module: `github.com/DenseAI/DenseCloud@vX.Y.Z`
   - package imports remain functional paths (for example `github.com/DenseAI/DenseCloud/go/server`)

## Helm library release

1. Package chart:

```bash
helm package charts/dense-base
```

2. Push to OCI chart registry (recommended):

```bash
helm push dense-base-0.2.0.tgz oci://ghcr.io/DenseAI/charts
```

3. Product chart dependency example:

```yaml
dependencies:
  - name: dense-base
    version: 0.2.0
    repository: oci://ghcr.io/DenseAI/charts
```

## Notes

- Root git tags version the Go module.
- Chart versions are managed in `charts/dense-base/Chart.yaml`.
- Docker image publication belongs in downstream workload repositories that
  consume DenseCloud.
