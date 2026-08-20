# dense-base (Helm Library Chart)

`dense-base` is the shared Kubernetes deployment skeleton for Dense workloads.

## Included building blocks

- Deployment
- Service (+ optional gRPC service)
- Ingress (+ optional dedicated gRPC ingress)
- cert-manager-managed gRPC Certificate
- PVC (optional)
- KEDA ScaledObject with explicit consumer-provided triggers
- NetworkPolicy
- PodDisruptionBudget
- ServiceMonitor
- ServiceAccount

## Validation highlights

- `image.repository` is required.
- `image.repository` and `image.tag` should point at the consuming product's
  workload image. DenseCloud's repository Dockerfile only builds a local/CI
  reference image used by smoke validation.
- `ingress.enabled=true` requires `service.enabled=true` and at least one `ingress.hosts[*].paths[*]`.
- `grpc.ingress.enabled=true` requires `grpc.enabled=true`, `grpc.service.enabled=true`, and at least one `grpc.ingress.hosts[*].paths[*]`.
- `keda.enabled=true` requires at least one explicit `keda.triggers.custom[*]`; DenseCloud does not ship placeholder autoscaling queries.
- `serviceMonitor.enabled=true` requires a non-empty `serviceMonitor.path`, which should match the runtime's `/metrics` endpoint.
- cert-manager wiring is explicit: ingress and gRPC ingress require TLS entries plus exactly one issuer source, and direct gRPC TLS mounts can create a `Certificate` resource.
- `networkPolicy.enabled=true` enables an opt-in deny-by-default contract with explicit ingress and egress allow rules.

NetworkPolicy examples are intentionally exact:

- `networkpolicy-ingress-nginx.yaml` allows only pods labeled
  `app.kubernetes.io/name=ingress-nginx` in the `ingress-nginx` namespace to
  reach TCP `8080`, plus DNS egress.
- `networkpolicy-monitoring.yaml` allows only pods labeled
  `app.kubernetes.io/name=prometheus` in the `monitoring` namespace to scrape
  TCP `8080`, plus DNS egress.
- `networkpolicy-otel-collector.yaml` denies workload ingress and allows only
  DNS plus TCP `4317` egress to pods labeled
  `app.kubernetes.io/name=otel-collector` in the `observability` namespace.
- `networkpolicy-strict.yaml` combines ingress-nginx, Prometheus, DNS, and OTel
  collector assumptions. Product charts must update selectors, namespaces, and
  ports to match their installed controllers.

## Consumer usage

In app chart `Chart.yaml`:

```yaml
dependencies:
  - name: dense-base
    version: 1.1.1
    repository: oci://ghcr.io/denseai/charts
```

In app chart template (`templates/base.yaml`):

```yaml
{{- include "dense-base.validate" (dict "root" . "values" (index .Values "dense-base")) -}}
---
{{ include "dense-base.serviceAccount" (dict "root" . "values" (index .Values "dense-base")) }}
---
{{ include "dense-base.pvc" (dict "root" . "values" (index .Values "dense-base")) }}
---
{{ include "dense-base.grpcCertificate" (dict "root" . "values" (index .Values "dense-base")) }}
---
{{ include "dense-base.deployment" (dict "root" . "values" (index .Values "dense-base")) }}
---
{{ include "dense-base.service" (dict "root" . "values" (index .Values "dense-base")) }}
---
{{ include "dense-base.grpcService" (dict "root" . "values" (index .Values "dense-base")) }}
---
{{ include "dense-base.ingress" (dict "root" . "values" (index .Values "dense-base")) }}
---
{{ include "dense-base.grpcIngress" (dict "root" . "values" (index .Values "dense-base")) }}
---
{{ include "dense-base.networkPolicy" (dict "root" . "values" (index .Values "dense-base")) }}
---
{{ include "dense-base.keda" (dict "root" . "values" (index .Values "dense-base")) }}
---
{{ include "dense-base.pdb" (dict "root" . "values" (index .Values "dense-base")) }}
---
{{ include "dense-base.serviceMonitor" (dict "root" . "values" (index .Values "dense-base")) }}
```

## Render matrix

Reference `helm template` value sets and a local render harness live under
`charts/dense-base/examples/renderer`.

The render matrix includes the seven baseline renderer presets plus a valid
KEDA custom-trigger preset and an expected-failure preset for missing KEDA
triggers. KEDA templates fail defensively even if a consumer omits the shared
validation include. Runtime smoke additionally reuses the minimal preset with a
locally built DenseCloud reference image to verify the chart's health, metrics,
service, and API path wiring inside kind.

cert-manager support creates the chart resources and annotations needed for
Secret issuance and reloader integration. Zero-downtime certificate hot reload
still depends on controller behavior plus application/runtime qualification by
the consuming product. DenseCloud does not claim product reload behavior beyond
that chart boundary.

OTel examples assume a collector reachable at the configured endpoint. Any
insecure OTel transport setting is a local/dev default only; production values
should use TLS or a product-approved private transport boundary.
