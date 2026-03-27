# dense-base render harness

This chart is a thin application wrapper around the `dense-base` library chart.
Use it to verify canonical `helm template` combinations without pulling in a
product-specific repo.

The sample values use `registry.example.invalid/...` on purpose. It is a
non-routable placeholder that satisfies chart validation without implying a
real Dense Series release artifact.

## Usage

Build the local dependency first:

```bash
helm dependency build charts/dense-base/examples/renderer
```

Render the baseline contract:

```bash
helm template dense-base-smoke charts/dense-base/examples/renderer \
  -f charts/dense-base/examples/renderer/values/minimal.yaml
```

Render ingress + cert-manager wiring:

```bash
helm template dense-base-smoke charts/dense-base/examples/renderer \
  -f charts/dense-base/examples/renderer/values/ingress-cert-manager.yaml
```

Render gRPC TLS + cert-manager wiring:

```bash
helm template dense-base-smoke charts/dense-base/examples/renderer \
  -f charts/dense-base/examples/renderer/values/grpc-cert-manager.yaml
```

Render strict NetworkPolicy hardening:

```bash
helm template dense-base-smoke charts/dense-base/examples/renderer \
  -f charts/dense-base/examples/renderer/values/networkpolicy-strict.yaml
```

Render ingress-controller-only NetworkPolicy preset:

```bash
helm template dense-base-smoke charts/dense-base/examples/renderer \
  -f charts/dense-base/examples/renderer/values/networkpolicy-ingress-nginx.yaml
```

Render monitoring scrape NetworkPolicy preset:

```bash
helm template dense-base-smoke charts/dense-base/examples/renderer \
  -f charts/dense-base/examples/renderer/values/networkpolicy-monitoring.yaml
```

Render OTel collector egress NetworkPolicy preset:

```bash
helm template dense-base-smoke charts/dense-base/examples/renderer \
  -f charts/dense-base/examples/renderer/values/networkpolicy-otel-collector.yaml
```

Repository helpers:

```bash
./scripts/helm_matrix.sh
./scripts/kind_smoke.sh
```

`kind_smoke.sh` creates a short-lived local kind cluster, installs placeholder
CRDs for `Certificate` and `ServiceMonitor`, and applies the full values matrix
to the API server in isolated namespaces.
