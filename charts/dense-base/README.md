# dense-base (Helm Library Chart)

`dense-base` is the shared Kubernetes deployment skeleton for Dense workloads.

## Included building blocks

- Deployment
- Service (+ optional gRPC service)
- Ingress (+ optional dedicated gRPC ingress)
- cert-manager-managed gRPC Certificate
- PVC (optional)
- KEDA ScaledObject
- NetworkPolicy
- PodDisruptionBudget
- ServiceMonitor
- ServiceAccount

## Validation highlights

- `image.repository` is required.
- `ingress.enabled=true` requires `service.enabled=true` and at least one `ingress.hosts[*].paths[*]`.
- `grpc.ingress.enabled=true` requires `grpc.enabled=true`, `grpc.service.enabled=true`, and at least one `grpc.ingress.hosts[*].paths[*]`.
- `keda.enabled=true` requires at least one explicit `keda.triggers.custom[*]`; DenseCloud does not ship placeholder autoscaling queries.
- `serviceMonitor.enabled=true` requires a non-empty `serviceMonitor.path`, which should match the runtime's `/metrics` endpoint.
- cert-manager wiring is explicit: ingress and gRPC ingress require TLS entries plus exactly one issuer source, and direct gRPC TLS mounts can create a `Certificate` resource.
- `networkPolicy.enabled=true` enables an opt-in deny-by-default contract with explicit ingress and egress allow rules.

## Consumer usage

In app chart `Chart.yaml`:

```yaml
dependencies:
  - name: dense-base
    version: 1.0.0
    repository: oci://ghcr.io/DenseAI/charts
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
